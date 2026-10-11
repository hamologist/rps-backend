// Package sweeper periodically expires inactive players and games.
package sweeper

import (
	"context"
	"log/slog"
	"time"

	"github.com/hamologist/rps-backend/internal/clock"
	"github.com/hamologist/rps-backend/internal/hub"
	"github.com/hamologist/rps-backend/internal/store"
)

// Sweeper expires players and games after a period of inactivity.
type Sweeper struct {
	Store     *store.Store
	Hub       *hub.Hub
	Clock     clock.Clock
	Logger    *slog.Logger
	PlayerTTL time.Duration
	GameTTL   time.Duration
	Interval  time.Duration
}

// Run sweeps once immediately and then every Interval until ctx is done.
func (s *Sweeper) Run(ctx context.Context) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	for {
		if err := s.SweepOnce(ctx); err != nil && ctx.Err() == nil {
			s.Logger.Error("sweep failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SweepOnce expires every player and game whose inactivity exceeds its TTL.
func (s *Sweeper) SweepOnce(ctx context.Context) error {
	now := s.Clock.Now()

	playerCutoff := now.Add(-s.PlayerTTL)
	players, err := s.Store.ExpiredPlayerIDs(ctx, playerCutoff)
	if err != nil {
		return err
	}
	for _, id := range players {
		if err := s.Hub.ExpirePlayer(ctx, id, playerCutoff); err != nil {
			return err
		}
		s.Logger.Info("player expired", "player_id", id)
	}

	gameCutoff := now.Add(-s.GameTTL)
	games, err := s.Store.ExpiredGameIDs(ctx, gameCutoff)
	if err != nil {
		return err
	}
	for _, id := range games {
		if err := s.Hub.ExpireGame(ctx, id, gameCutoff); err != nil {
			return err
		}
		s.Logger.Info("game expired", "game_id", id)
	}
	return nil
}
