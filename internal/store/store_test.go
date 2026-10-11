package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hamologist/rps-backend/internal/rules"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestSchemaIdempotent(t *testing.T) {
	s, path := open(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlayer(ctx, "a", "h", t0); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	ids, err := s2.ExpiredPlayerIDs(ctx, t0.Add(time.Hour))
	if err != nil || len(ids) != 1 {
		t.Fatalf("data lost after reopen: %v %v", ids, err)
	}
}

func TestPragmas(t *testing.T) {
	s, _ := open(t)
	var mode string
	var fk, timeout int
	ctx := context.Background()
	s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
	s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk)
	s.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout)
	if mode != "wal" || fk != 1 || timeout != 5000 {
		t.Fatalf("pragmas: %s %d %d", mode, fk, timeout)
	}
}

func TestCreatePlayerAndGet(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	s.Discriminator = func() int { return 42 }
	p, err := s.CreatePlayer(ctx, "hamologist", "hash", t0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Discriminator != 42 || p.DisplayName() != "hamologist#0042" {
		t.Fatalf("player: %+v %s", p, p.DisplayName())
	}
	got, err := s.GetPlayer(ctx, p.ID)
	if err != nil || got.BaseName != "hamologist" || !got.LastActiveAt.Equal(t0) {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := s.GetPlayer(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestDiscriminatorCollisionRetry(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	seq := []int{7, 7, 7, 9}
	i := 0
	s.Discriminator = func() int { v := seq[i%len(seq)]; i++; return v }
	a, err := s.CreatePlayer(ctx, "x", "h", t0)
	if err != nil || a.Discriminator != 7 {
		t.Fatalf("a: %+v %v", a, err)
	}
	b, err := s.CreatePlayer(ctx, "x", "h", t0)
	if err != nil || b.Discriminator != 9 {
		t.Fatalf("b: %+v %v", b, err)
	}
	// Same discriminator on another base name is fine.
	i = 0
	c, err := s.CreatePlayer(ctx, "y", "h", t0)
	if err != nil || c.Discriminator != 7 {
		t.Fatalf("c: %+v %v", c, err)
	}
}

func TestDiscriminatorFallbackScan(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	s.Discriminator = func() int { return 0 }
	for want := range 3 {
		p, err := s.CreatePlayer(ctx, "z", "h", t0)
		if err != nil || p.Discriminator != want {
			t.Fatalf("want %d got %+v %v", want, p, err)
		}
	}
}

func TestUsernameExhausted(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for d := 0; d <= MaxDiscriminator; d++ {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO players (id, base_name, discriminator, secret_hash, last_active_at) VALUES (?, 'full', ?, 'h', ?)`,
			"id-"+DisplayName("full", d), d, formatTime(t0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlayer(ctx, "full", "h", t0); !errors.Is(err, ErrUsernameExhausted) {
		t.Fatalf("want exhausted, got %v", err)
	}
}

func TestSecrets(t *testing.T) {
	plain, hash := NewSecret()
	if len(plain) != 43 || len(hash) != 64 {
		t.Fatalf("lengths %d %d", len(plain), len(hash))
	}
	if !VerifySecret(plain, hash) {
		t.Fatal("verify failed")
	}
	if VerifySecret(plain+"x", hash) || VerifySecret("", hash) {
		t.Fatal("verify accepted wrong secret")
	}
	other, _ := NewSecret()
	if other == plain {
		t.Fatal("secrets not random")
	}
}

func TestGameLifecycleAndSetNull(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	host, _ := s.CreatePlayer(ctx, "host", "h", t0)
	guest, _ := s.CreatePlayer(ctx, "guest", "h", t0)

	g, err := s.CreateGame(ctx, host.ID, "gh", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := s.GetPlayer(ctx, host.ID); !p.LastActiveAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("host not touched: %v", p.LastActiveAt)
	}
	g.GuestPlayerID = guest.ID
	g.State, _ = rules.Join(g.State)
	g.State, _ = rules.PlayMove(g.State, rules.Host, rules.Rock)
	g.LastActiveAt = t0.Add(2 * time.Minute)
	if err := s.SaveGame(ctx, g, guest.ID, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetGame(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.GuestPlayerID != guest.ID || got.Status != rules.StatusActive || got.HostMove != rules.Rock ||
		got.GuestMove != rules.Pending || !got.LastActiveAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("game: %+v", got)
	}
	if seat, ok := got.SeatOf(guest.ID); !ok || seat != rules.Guest {
		t.Fatalf("seat: %v %v", seat, ok)
	}

	if err := s.DeletePlayer(ctx, guest.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetGame(ctx, g.ID)
	if got.GuestPlayerID != "" || got.HostPlayerID != host.ID {
		t.Fatalf("set null: %+v", got)
	}

	if err := s.DeleteGame(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetGame(ctx, g.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted game: %v", err)
	}
}

func TestExpiryQueries(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	old, _ := s.CreatePlayer(ctx, "old", "h", t0)
	fresh, _ := s.CreatePlayer(ctx, "fresh", "h", t0.Add(10*time.Hour))
	// Sub-second precision must still compare correctly.
	if err := s.TouchPlayer(ctx, fresh.ID, t0.Add(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	cutoff := t0.Add(time.Millisecond)
	ids, err := s.ExpiredPlayerIDs(ctx, cutoff)
	if err != nil || len(ids) != 1 || ids[0] != old.ID {
		t.Fatalf("expired players: %v %v", ids, err)
	}

	g1, _ := s.CreateGame(ctx, old.ID, "h", t0)
	g2, _ := s.CreateGame(ctx, fresh.ID, "h", t0.Add(time.Hour))
	gids, err := s.ExpiredGameIDs(ctx, t0.Add(time.Minute))
	if err != nil || len(gids) != 1 || gids[0] != g1.ID {
		t.Fatalf("expired games: %v %v", gids, err)
	}

	open, err := s.OpenGameIDsForPlayer(ctx, fresh.ID)
	if err != nil || len(open) != 1 || open[0] != g2.ID {
		t.Fatalf("open games: %v %v", open, err)
	}
	g2.Status = rules.StatusClosed
	if err := s.SaveGame(ctx, g2, "", t0); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.OpenGameIDsForPlayer(ctx, fresh.ID); len(open) != 0 {
		t.Fatalf("closed game listed: %v", open)
	}
}
