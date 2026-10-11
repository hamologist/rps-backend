package hub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hamologist/rps-backend/internal/rules"
	"github.com/hamologist/rps-backend/internal/store"
)

// Action names.
const (
	ActionAuthenticate   = "player::authenticate"
	ActionConnect        = "game::connect"
	ActionMove           = "game::move"
	ActionRematch        = "game::rematch"
	ActionRematchDecline = "game::rematch_decline"
	ActionLeave          = "game::leave"
)

type handlerFunc func(h *Hub, ctx context.Context, c *Conn, requestID string, payload json.RawMessage) error

var handlers = map[string]handlerFunc{
	ActionAuthenticate:   (*Hub).authenticate,
	ActionConnect:        (*Hub).connect,
	ActionMove:           (*Hub).move,
	ActionRematch:        (*Hub).rematch,
	ActionRematchDecline: (*Hub).rematchDecline,
	ActionLeave:          (*Hub).leave,
}

// gameActions require the connection to be attached to a game.
var gameActions = map[string]bool{
	ActionMove:           true,
	ActionRematch:        true,
	ActionRematchDecline: true,
	ActionLeave:          true,
}

func protoErr(code, msg string) *rules.Error { return &rules.Error{Code: code, Message: msg} }

var (
	errBadPayload           = protoErr("bad_request", "malformed payload")
	errNotAuthenticated     = protoErr("not_authenticated", "authenticate first")
	errAlreadyAuthenticated = protoErr("already_authenticated", "connection is already authenticated")
	errInvalidCredentials   = protoErr("invalid_credentials", "invalid player credentials")
	errInvalidGameCreds     = protoErr("invalid_game_credentials", "invalid game credentials")
	errGameFull             = protoErr("game_full", "game already has two players")
	errNotInGame            = protoErr("not_in_game", "connection is not attached to a game")
)

// Handle runs one client action, sending exactly one direct reply.
func (h *Hub) Handle(ctx context.Context, c *Conn, action, requestID string, payload json.RawMessage) {
	if c.isClosing() {
		return
	}
	fn, ok := handlers[action]
	if !ok {
		c.SendError(requestID, "unknown_action", "unknown action")
		return
	}
	h.mu.Lock()
	authed, inGame := c.playerID != "", c.gameID != ""
	h.mu.Unlock()
	switch {
	case action != ActionAuthenticate && !authed:
		h.replyErr(c, requestID, action, errNotAuthenticated)
		return
	case gameActions[action] && !inGame:
		h.replyErr(c, requestID, action, errNotInGame)
		return
	}
	if err := fn(h, ctx, c, requestID, payload); err != nil {
		h.replyErr(c, requestID, action, err)
	}
}

func (h *Hub) replyErr(c *Conn, requestID, action string, err error) {
	var re *rules.Error
	if errors.As(err, &re) {
		c.SendError(requestID, re.Code, re.Message)
		return
	}
	h.log.Error("action failed", "action", action, "err", err)
	c.SendError(requestID, "internal", "internal error")
}

func (h *Hub) playerOf(c *Conn) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return c.playerID
}

type authPayload struct {
	PlayerID     string `json:"player_id"`
	PlayerSecret string `json:"player_secret"`
}

type authOK struct {
	PlayerID    string `json:"player_id"`
	DisplayName string `json:"display_name"`
}

func (h *Hub) authenticate(ctx context.Context, c *Conn, requestID string, raw json.RawMessage) error {
	var p authPayload
	if err := decode(raw, &p); err != nil {
		return err
	}
	if h.IsAuthenticated(c) {
		return errAlreadyAuthenticated
	}
	player, err := h.store.GetPlayer(ctx, p.PlayerID)
	if errors.Is(err, store.ErrNotFound) {
		return errInvalidCredentials
	}
	if err != nil {
		return err
	}
	if !store.VerifySecret(p.PlayerSecret, player.SecretHash) {
		return errInvalidCredentials
	}
	if err := h.store.TouchPlayer(ctx, player.ID, h.clock.Now()); err != nil {
		return err
	}

	h.mu.Lock()
	old := h.players[player.ID]
	h.players[player.ID] = c
	c.playerID = player.ID
	h.mu.Unlock()

	c.send(ActionAuthenticate+"::ok", requestID, authOK{PlayerID: player.ID, DisplayName: player.DisplayName()})

	if old != nil && old != c {
		old.send("connection::replaced", "", empty{})
		old.Close(CloseReplaced, "replaced by a newer connection")
		h.detach(old, true)
	}
	return nil
}

type connectPayload struct {
	GameID     string `json:"game_id"`
	GameSecret string `json:"game_secret"`
}

func (h *Hub) loadGameWithSecret(ctx context.Context, id, secret string) (store.Game, error) {
	g, err := h.store.GetGame(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Game{}, errInvalidGameCreds
	}
	if err != nil {
		return store.Game{}, err
	}
	if !store.VerifySecret(secret, g.SecretHash) {
		return store.Game{}, errInvalidGameCreds
	}
	return g, nil
}

func (h *Hub) connect(ctx context.Context, c *Conn, requestID string, raw json.RawMessage) error {
	var p connectPayload
	if err := decode(raw, &p); err != nil {
		return err
	}
	g, err := h.loadGameWithSecret(ctx, p.GameID, p.GameSecret)
	if err != nil {
		return err
	}
	if g.Status == rules.StatusClosed {
		return rules.ErrGameClosed
	}
	playerID := h.playerOf(c)

	// Leave any other game first; that game is not closed.
	h.mu.Lock()
	current := c.gameID
	h.mu.Unlock()
	if current != "" && current != g.ID {
		h.detach(c, true)
	}

	unlock := h.lockGame(g.ID)
	defer unlock()

	if g, err = h.loadGameWithSecret(ctx, p.GameID, p.GameSecret); err != nil {
		return err
	}
	if g.Status == rules.StatusClosed {
		return rules.ErrGameClosed
	}
	seat, ok := g.SeatOf(playerID)
	if !ok {
		if g.GuestPlayerID != "" {
			return errGameFull
		}
		if g.State, err = rules.Join(g.State); err != nil {
			return err
		}
		g.GuestPlayerID, seat = playerID, rules.Guest
	}
	if err := h.save(ctx, &g, playerID); err != nil {
		return err
	}

	h.mu.Lock()
	wasAttached := c.gameID == g.ID
	h.attachLocked(c, g.ID, seat)
	h.mu.Unlock()

	v, err := h.view(ctx, g)
	if err != nil {
		return err
	}
	c.send(ActionConnect+"::ok", requestID, v.For(seat))
	if wasAttached {
		return nil
	}
	for _, o := range h.attached(g.ID) {
		if o == c {
			continue
		}
		o.send("game::opponent_connected", "", seatPayload{Seat: seat})
		o.send("game::state", "", v.For(seat.Other()))
	}
	return nil
}

// save stamps g with the current time and persists it, touching playerID.
func (h *Hub) save(ctx context.Context, g *store.Game, playerID string) error {
	now := h.clock.Now()
	g.LastActiveAt = now
	return h.store.SaveGame(ctx, *g, playerID, now)
}

// withGame runs fn under the lock of the game c is attached to.
func (h *Hub) withGame(ctx context.Context, c *Conn, fn func(g store.Game, seat rules.Seat, playerID string) error) error {
	h.mu.Lock()
	gameID := c.gameID
	h.mu.Unlock()
	if gameID == "" {
		return errNotInGame
	}
	unlock := h.lockGame(gameID)
	defer unlock()

	h.mu.Lock()
	stillAttached := c.gameID == gameID
	seat, playerID := c.seat, c.playerID
	h.mu.Unlock()
	if !stillAttached {
		return errNotInGame
	}
	g, err := h.store.GetGame(ctx, gameID)
	if errors.Is(err, store.ErrNotFound) {
		return errNotInGame
	}
	if err != nil {
		return err
	}
	return fn(g, seat, playerID)
}

// sendOpponents sends an event to everyone attached to gameID except c.
func (h *Hub) sendOpponents(c *Conn, gameID, typ string, payload any) {
	for _, o := range h.attached(gameID) {
		if o != c {
			o.send(typ, "", payload)
		}
	}
}

type movePayload struct {
	Move string `json:"move"`
}

func (h *Hub) move(ctx context.Context, c *Conn, requestID string, raw json.RawMessage) error {
	var p movePayload
	if err := decode(raw, &p); err != nil {
		return rules.ErrInvalidMove
	}
	m, err := rules.ParseMove(p.Move)
	if err != nil {
		return err
	}
	return h.withGame(ctx, c, func(g store.Game, seat rules.Seat, playerID string) error {
		if g.State, err = rules.PlayMove(g.State, seat, m); err != nil {
			return err
		}
		if err := h.save(ctx, &g, playerID); err != nil {
			return err
		}
		c.send(ActionMove+"::ok", requestID, empty{})
		h.broadcastState(ctx, g)
		return nil
	})
}

func (h *Hub) rematch(ctx context.Context, c *Conn, requestID string, _ json.RawMessage) error {
	return h.withGame(ctx, c, func(g store.Game, seat rules.Seat, playerID string) error {
		next, accepted, err := rules.Rematch(g.State, seat)
		if err != nil {
			return err
		}
		g.State = next
		if err := h.save(ctx, &g, playerID); err != nil {
			return err
		}
		c.send(ActionRematch+"::ok", requestID, empty{})
		if !accepted {
			h.sendOpponents(c, g.ID, "game::rematch_requested", byPayload{By: seat})
		}
		h.broadcastState(ctx, g)
		return nil
	})
}

func (h *Hub) rematchDecline(ctx context.Context, c *Conn, requestID string, _ json.RawMessage) error {
	return h.withGame(ctx, c, func(g store.Game, seat rules.Seat, playerID string) error {
		var err error
		if g.State, err = rules.DeclineRematch(g.State, seat); err != nil {
			return err
		}
		if err := h.save(ctx, &g, playerID); err != nil {
			return err
		}
		c.send(ActionRematchDecline+"::ok", requestID, empty{})
		h.sendOpponents(c, g.ID, "game::rematch_declined", byPayload{By: seat})
		h.broadcastState(ctx, g)
		return nil
	})
}

func (h *Hub) leave(ctx context.Context, c *Conn, requestID string, _ json.RawMessage) error {
	return h.withGame(ctx, c, func(g store.Game, seat rules.Seat, playerID string) error {
		var err error
		if g.State, err = rules.Close(g.State); err != nil {
			return err
		}
		if err := h.save(ctx, &g, playerID); err != nil {
			return err
		}
		h.mu.Lock()
		h.detachLocked(c)
		h.mu.Unlock()
		c.send(ActionLeave+"::ok", requestID, empty{})
		h.closeGameFor(ctx, g, closedPayload{Reason: "left", By: seat})
		return nil
	})
}

// KnownAction reports whether action is a recognized client action.
func KnownAction(action string) bool {
	_, ok := handlers[action]
	return ok
}
