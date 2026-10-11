package hub

import (
	"context"
	"errors"

	"github.com/hamologist/rps-backend/internal/rules"
	"github.com/hamologist/rps-backend/internal/store"
)

// PlayerView is one seat in a snapshot.
type PlayerView struct {
	PlayerID    string      `json:"player_id"`
	DisplayName string      `json:"display_name"`
	Connected   bool        `json:"connected"`
	LockedIn    bool        `json:"locked_in"`
	Move        *rules.Move `json:"move"`
	Wins        int         `json:"wins"`
}

// Snapshot is the personalized game::state payload.
type Snapshot struct {
	GameID    string          `json:"game_id"`
	Status    rules.Status    `json:"status"`
	GameState rules.GameState `json:"game_state"`
	You       rules.Seat      `json:"you"`
	Host      *PlayerView     `json:"host"`
	Guest     *PlayerView     `json:"guest"`
	Draws     int             `json:"draws"`
}

// gameView holds everything needed to render per-recipient snapshots.
type gameView struct {
	g         store.Game
	names     map[rules.Seat]string
	connected map[rules.Seat]bool
}

// view loads display names and connection state for g. The caller must hold
// the game lock and must not hold h.mu.
func (h *Hub) view(ctx context.Context, g store.Game) (gameView, error) {
	v := gameView{g: g, names: map[rules.Seat]string{}, connected: map[rules.Seat]bool{}}
	for _, seat := range []rules.Seat{rules.Host, rules.Guest} {
		id := g.PlayerIDOf(seat)
		if id == "" {
			continue
		}
		p, err := h.store.GetPlayer(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return gameView{}, err
		}
		v.names[seat] = p.DisplayName()
	}
	h.mu.Lock()
	for c := range h.games[g.ID] {
		if seat, ok := g.SeatOf(c.playerID); ok && !c.isClosing() {
			v.connected[seat] = true
		}
	}
	h.mu.Unlock()
	return v, nil
}

func (v gameView) For(you rules.Seat) Snapshot {
	s := Snapshot{
		GameID:    v.g.ID,
		Status:    v.g.Status,
		GameState: v.g.GameState,
		You:       you,
		Draws:     v.g.Draws,
	}
	s.Host = v.player(rules.Host, you)
	s.Guest = v.player(rules.Guest, you)
	return s
}

func (v gameView) player(seat, viewer rules.Seat) *PlayerView {
	name, ok := v.names[seat]
	if !ok {
		return nil
	}
	pv := &PlayerView{
		PlayerID:    v.g.PlayerIDOf(seat),
		DisplayName: name,
		Connected:   v.connected[seat],
		LockedIn:    v.g.MoveOf(seat) != rules.Pending,
		Wins:        v.g.HostWins,
	}
	if seat == rules.Guest {
		pv.Wins = v.g.GuestWins
	}
	if m, ok := rules.VisibleMove(v.g.State, viewer, seat); ok {
		pv.Move = &m
	}
	return pv
}
