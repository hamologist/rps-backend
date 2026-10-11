// Package hub tracks live connections and their game attachments, serializes
// actions per game, and broadcasts events. SQLite (via store) is the source of
// truth; the hub only holds connection state in memory.
//
// Lock order: a game lock (lockGame) is always acquired before h.mu, and h.mu
// is only held for brief map operations. Never acquire a game lock while
// holding h.mu.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/hamologist/rps-backend/internal/clock"
	"github.com/hamologist/rps-backend/internal/rules"
	"github.com/hamologist/rps-backend/internal/store"
)

// Hub is the connection registry and action handler.
type Hub struct {
	store *store.Store
	clock clock.Clock
	log   *slog.Logger

	mu      sync.Mutex
	conns   map[*Conn]struct{}
	players map[string]*Conn              // playerID → live connection
	games   map[string]map[*Conn]struct{} // gameID → attached connections
	locks   map[string]*gameLock
}

type gameLock struct {
	mu   sync.Mutex
	refs int // holders + waiters; guarded by Hub.mu
}

// New creates a Hub.
func New(s *store.Store, c clock.Clock, log *slog.Logger) *Hub {
	return &Hub{
		store:   s,
		clock:   c,
		log:     log,
		conns:   map[*Conn]struct{}{},
		players: map[string]*Conn{},
		games:   map[string]map[*Conn]struct{}{},
		locks:   map[string]*gameLock{},
	}
}

// NewConn registers a new, unauthenticated connection.
func (h *Hub) NewConn() *Conn {
	c := newConn()
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
	return c
}

// IsAuthenticated reports whether c has authenticated as a player.
func (h *Hub) IsAuthenticated(c *Conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return c.playerID != ""
}

// Disconnect is called by the transport once a connection's socket is gone.
// The game is not changed; the opponent is told the player disconnected.
func (h *Hub) Disconnect(c *Conn) {
	c.Close(CloseNormal, "")
	h.detach(c, true)
	h.mu.Lock()
	if c.playerID != "" && h.players[c.playerID] == c {
		delete(h.players, c.playerID)
	}
	delete(h.conns, c)
	h.mu.Unlock()
}

// CloseAll closes every live connection with code.
func (h *Hub) CloseAll(code int, reason string) {
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		c.Close(code, reason)
	}
}

// lockGame acquires the per-game mutex and returns its release func.
func (h *Hub) lockGame(id string) func() {
	h.mu.Lock()
	l := h.locks[id]
	if l == nil {
		l = &gameLock{}
		h.locks[id] = l
	}
	l.refs++
	h.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		h.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(h.locks, id)
		}
		h.mu.Unlock()
	}
}

// attachedLocked returns the connections attached to gameID. Requires h.mu.
func (h *Hub) attachedLocked(gameID string) []*Conn {
	out := make([]*Conn, 0, len(h.games[gameID]))
	for c := range h.games[gameID] {
		out = append(out, c)
	}
	return out
}

func (h *Hub) attached(gameID string) []*Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attachedLocked(gameID)
}

// attachLocked attaches c to gameID in seat. Requires h.mu and the game lock.
func (h *Hub) attachLocked(c *Conn, gameID string, seat rules.Seat) {
	set := h.games[gameID]
	if set == nil {
		set = map[*Conn]struct{}{}
		h.games[gameID] = set
	}
	set[c] = struct{}{}
	c.gameID, c.seat = gameID, seat
}

// detachLocked detaches c from its game. Requires h.mu and the game lock.
func (h *Hub) detachLocked(c *Conn) bool {
	if c.gameID == "" {
		return false
	}
	set := h.games[c.gameID]
	delete(set, c)
	if len(set) == 0 {
		delete(h.games, c.gameID)
	}
	c.gameID, c.seat = "", ""
	return true
}

// detach detaches c from whatever game it is attached to. If notify is set,
// the remaining participants receive game::opponent_disconnected.
func (h *Hub) detach(c *Conn, notify bool) {
	for {
		h.mu.Lock()
		gameID := c.gameID
		h.mu.Unlock()
		if gameID == "" {
			return
		}

		unlock := h.lockGame(gameID)
		h.mu.Lock()
		if c.gameID != gameID {
			// Attachment changed while we waited for the lock; retry.
			h.mu.Unlock()
			unlock()
			continue
		}
		seat := c.seat
		h.detachLocked(c)
		others := h.attachedLocked(gameID)
		h.mu.Unlock()

		if notify {
			for _, o := range others {
				o.send("game::opponent_disconnected", "", seatPayload{Seat: seat})
			}
		}
		unlock()
		return
	}
}

type seatPayload struct {
	Seat rules.Seat `json:"seat"`
}

type byPayload struct {
	By rules.Seat `json:"by"`
}

type closedPayload struct {
	Reason string     `json:"reason"`
	By     rules.Seat `json:"by,omitempty"`
}

func (h *Hub) broadcastState(ctx context.Context, g store.Game) {
	v, err := h.view(ctx, g)
	if err != nil {
		h.log.Error("build snapshot", "game_id", g.ID, "err", err)
		return
	}
	h.mu.Lock()
	conns := h.attachedLocked(g.ID)
	seats := make([]rules.Seat, len(conns))
	for i, c := range conns {
		seats[i] = c.seat
	}
	h.mu.Unlock()
	for i, c := range conns {
		c.send("game::state", "", v.For(seats[i]))
	}
}

// ExpirePlayer closes the player's open games (notifying opponents),
// terminates their connection and deletes them. It is a no-op if the player
// no longer exists or has been active since cutoff.
func (h *Hub) ExpirePlayer(ctx context.Context, playerID string, cutoff time.Time) error {
	p, err := h.store.GetPlayer(ctx, playerID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !p.LastActiveAt.Before(cutoff) {
		return nil
	}

	gameIDs, err := h.store.OpenGameIDsForPlayer(ctx, playerID)
	if err != nil {
		return err
	}
	for _, gameID := range gameIDs {
		if err := h.closeForExpiredPlayer(ctx, gameID, playerID); err != nil {
			return err
		}
	}

	h.mu.Lock()
	c := h.players[playerID]
	h.mu.Unlock()
	if c != nil {
		c.Close(ClosePlayerExpired, "player expired")
		h.detach(c, true)
	}
	return h.store.DeletePlayer(ctx, playerID)
}

func (h *Hub) closeForExpiredPlayer(ctx context.Context, gameID, playerID string) error {
	unlock := h.lockGame(gameID)
	defer unlock()

	g, err := h.store.GetGame(ctx, gameID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	seat, ok := g.SeatOf(playerID)
	if !ok || g.Status == rules.StatusClosed {
		return nil
	}
	g.State, _ = rules.Close(g.State)
	// last_active_at is deliberately left unchanged.
	if err := h.store.SaveGame(ctx, g, "", g.LastActiveAt); err != nil {
		return err
	}

	h.mu.Lock()
	for _, c := range h.attachedLocked(gameID) {
		if c.playerID == playerID {
			h.detachLocked(c)
		}
	}
	h.mu.Unlock()

	h.closeGameFor(ctx, g, closedPayload{Reason: "player_expired", By: seat})
	return nil
}

// closeGameFor sends game::closed plus a final game::state to everyone still
// attached to the (now CLOSED) game g, then detaches them. Requires the game
// lock.
func (h *Hub) closeGameFor(ctx context.Context, g store.Game, payload closedPayload) {
	v, err := h.view(ctx, g)
	if err != nil {
		h.log.Error("build snapshot", "game_id", g.ID, "err", err)
	}
	h.mu.Lock()
	conns := h.attachedLocked(g.ID)
	seats := make([]rules.Seat, len(conns))
	for i, c := range conns {
		seats[i] = c.seat
		h.detachLocked(c)
	}
	h.mu.Unlock()
	for i, c := range conns {
		c.send("game::closed", "", payload)
		if err == nil {
			c.send("game::state", "", v.For(seats[i]))
		}
	}
}

// ExpireGame terminates every connection attached to the game and deletes it.
// It is a no-op if the game no longer exists or has been active since cutoff.
func (h *Hub) ExpireGame(ctx context.Context, gameID string, cutoff time.Time) error {
	unlock := h.lockGame(gameID)
	defer unlock()

	g, err := h.store.GetGame(ctx, gameID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !g.LastActiveAt.Before(cutoff) {
		return nil
	}

	h.mu.Lock()
	conns := h.attachedLocked(gameID)
	for _, c := range conns {
		h.detachLocked(c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		c.Close(CloseGameExpired, "game expired")
	}
	return h.store.DeleteGame(ctx, gameID)
}

// decode unmarshals an optional payload.
func decode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return errBadPayload
	}
	return nil
}
