// Package store persists players and games in SQLite.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/mattn/go-sqlite3"

	"github.com/hamologist/rps-backend/internal/rules"
)

//go:embed schema.sql
var schema string

var (
	ErrNotFound          = errors.New("not found")
	ErrUsernameExhausted = errors.New("all discriminators for this username are taken")
)

// MaxDiscriminator is the largest discriminator value (inclusive).
const MaxDiscriminator = 9999

// discriminatorRetries is how many random discriminators are tried before
// falling back to scanning for a free one.
const discriminatorRetries = 20

// timeFormat is RFC3339 with fixed-width nanoseconds so that stored
// timestamps sort lexicographically in time order (time.RFC3339Nano trims
// trailing zeros, which breaks string comparison). It parses with
// time.RFC3339Nano.
const timeFormat = "2006-01-02T15:04:05.000000000Z07:00"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

// Player is a stored player.
type Player struct {
	ID            string
	BaseName      string
	Discriminator int
	SecretHash    string
	LastActiveAt  time.Time
}

// DisplayName returns "base_name#0042".
func (p Player) DisplayName() string { return DisplayName(p.BaseName, p.Discriminator) }

// DisplayName formats a base name and discriminator.
func DisplayName(base string, disc int) string { return fmt.Sprintf("%s#%04d", base, disc) }

// Game is a stored game. HostPlayerID and GuestPlayerID are empty when NULL.
type Game struct {
	ID            string
	SecretHash    string
	HostPlayerID  string
	GuestPlayerID string
	rules.State
	LastActiveAt time.Time
}

// SeatOf returns the seat playerID occupies in g, if any.
func (g Game) SeatOf(playerID string) (rules.Seat, bool) {
	switch {
	case playerID == "":
		return "", false
	case g.HostPlayerID == playerID:
		return rules.Host, true
	case g.GuestPlayerID == playerID:
		return rules.Guest, true
	}
	return "", false
}

// PlayerIDOf returns the player ID in seat (empty if none).
func (g Game) PlayerIDOf(seat rules.Seat) string {
	if seat == rules.Host {
		return g.HostPlayerID
	}
	return g.GuestPlayerID
}

// Store is the SQLite-backed persistence layer.
type Store struct {
	db *sql.DB
	// Discriminator returns a random discriminator in [0, MaxDiscriminator].
	// Replaceable for tests.
	Discriminator func() int
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	q.Set("_journal_mode", "WAL")
	q.Set("_foreign_keys", "on")
	q.Set("_busy_timeout", "5000")
	// Take the write lock at BEGIN so read-then-write transactions never hit
	// SQLITE_BUSY on lock upgrade.
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite3", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, Discriminator: func() int { return rand.IntN(MaxDiscriminator + 1) }}
	if err := s.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Migrate applies the (idempotent) schema.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func isUniqueViolation(err error) bool {
	var se sqlite3.Error
	return errors.As(err, &se) && se.ExtendedCode == sqlite3.ErrConstraintUnique
}

// CreatePlayer inserts a player with a random free discriminator.
func (s *Store) CreatePlayer(ctx context.Context, baseName, secretHash string, now time.Time) (Player, error) {
	p := Player{
		ID:           uuid.NewString(),
		BaseName:     baseName,
		SecretHash:   secretHash,
		LastActiveAt: now.UTC(),
	}
	insert := func(disc int) error {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO players (id, base_name, discriminator, secret_hash, last_active_at) VALUES (?, ?, ?, ?, ?)`,
			p.ID, baseName, disc, secretHash, formatTime(now))
		return err
	}

	for range discriminatorRetries {
		disc := s.Discriminator()
		err := insert(disc)
		if err == nil {
			p.Discriminator = disc
			return p, nil
		}
		if !isUniqueViolation(err) {
			return Player{}, fmt.Errorf("insert player: %w", err)
		}
	}

	// Fallback: scan for any free discriminator.
	for {
		disc, err := s.freeDiscriminator(ctx, baseName)
		if err != nil {
			return Player{}, err
		}
		err = insert(disc)
		if err == nil {
			p.Discriminator = disc
			return p, nil
		}
		if !isUniqueViolation(err) {
			return Player{}, fmt.Errorf("insert player: %w", err)
		}
		// Lost a race for that value; scan again.
	}
}

func (s *Store) freeDiscriminator(ctx context.Context, baseName string) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT discriminator FROM players WHERE base_name = ? ORDER BY discriminator`, baseName)
	if err != nil {
		return 0, fmt.Errorf("scan discriminators: %w", err)
	}
	defer rows.Close()
	next := 0
	for rows.Next() {
		var d int
		if err := rows.Scan(&d); err != nil {
			return 0, err
		}
		if d > next {
			break
		}
		next = d + 1
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if next > MaxDiscriminator {
		return 0, ErrUsernameExhausted
	}
	return next, nil
}

const playerColumns = `id, base_name, discriminator, secret_hash, last_active_at`

func scanPlayer(row interface{ Scan(...any) error }) (Player, error) {
	var p Player
	var ts string
	if err := row.Scan(&p.ID, &p.BaseName, &p.Discriminator, &p.SecretHash, &ts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Player{}, ErrNotFound
		}
		return Player{}, err
	}
	t, err := parseTime(ts)
	if err != nil {
		return Player{}, err
	}
	p.LastActiveAt = t
	return p, nil
}

// GetPlayer loads a player by ID.
func (s *Store) GetPlayer(ctx context.Context, id string) (Player, error) {
	return getPlayer(ctx, s.db, id)
}

func getPlayer(ctx context.Context, q querier, id string) (Player, error) {
	return scanPlayer(q.QueryRowContext(ctx, `SELECT `+playerColumns+` FROM players WHERE id = ?`, id))
}

// TouchPlayer sets a player's last_active_at.
func (s *Store) TouchPlayer(ctx context.Context, id string, now time.Time) error {
	return touchPlayer(ctx, s.db, id, now)
}

func touchPlayer(ctx context.Context, q querier, id string, now time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE players SET last_active_at = ? WHERE id = ?`, formatTime(now), id)
	return err
}

// ExpiredPlayerIDs returns players inactive since before cutoff.
func (s *Store) ExpiredPlayerIDs(ctx context.Context, cutoff time.Time) ([]string, error) {
	return s.ids(ctx, `SELECT id FROM players WHERE last_active_at < ?`, formatTime(cutoff))
}

// ExpiredGameIDs returns games inactive since before cutoff.
func (s *Store) ExpiredGameIDs(ctx context.Context, cutoff time.Time) ([]string, error) {
	return s.ids(ctx, `SELECT id FROM games WHERE last_active_at < ?`, formatTime(cutoff))
}

// OpenGameIDsForPlayer returns non-CLOSED games where the player is host or guest.
func (s *Store) OpenGameIDsForPlayer(ctx context.Context, playerID string) ([]string, error) {
	return s.ids(ctx,
		`SELECT id FROM games WHERE status != 'CLOSED' AND (host_player_id = ? OR guest_player_id = ?)`,
		playerID, playerID)
}

func (s *Store) ids(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeletePlayer deletes a player; games referencing it get NULL foreign keys.
func (s *Store) DeletePlayer(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM players WHERE id = ?`, id)
	return err
}

// CreateGame creates a game hosted by hostID and touches the host.
func (s *Store) CreateGame(ctx context.Context, hostID, secretHash string, now time.Time) (Game, error) {
	g := Game{
		ID:           uuid.NewString(),
		SecretHash:   secretHash,
		HostPlayerID: hostID,
		State:        rules.NewState(),
		LastActiveAt: now.UTC(),
	}
	err := s.Tx(ctx, func(tx *Tx) error {
		_, err := tx.tx.ExecContext(ctx,
			`INSERT INTO games (id, secret_hash, host_player_id, status, game_state, last_active_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			g.ID, g.SecretHash, hostID, g.Status, g.GameState, formatTime(now))
		if err != nil {
			return fmt.Errorf("insert game: %w", err)
		}
		return tx.TouchPlayer(ctx, hostID, now)
	})
	if err != nil {
		return Game{}, err
	}
	return g, nil
}

const gameColumns = `id, secret_hash, host_player_id, guest_player_id,
	host_player_latest_move, guest_player_latest_move,
	host_player_wins, guest_player_wins, draws, status, game_state, last_active_at`

func scanGame(row interface{ Scan(...any) error }) (Game, error) {
	var g Game
	var host, guest sql.NullString
	var ts string
	err := row.Scan(&g.ID, &g.SecretHash, &host, &guest,
		&g.HostMove, &g.GuestMove, &g.HostWins, &g.GuestWins, &g.Draws,
		&g.Status, &g.GameState, &ts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Game{}, ErrNotFound
		}
		return Game{}, err
	}
	g.HostPlayerID, g.GuestPlayerID = host.String, guest.String
	if g.LastActiveAt, err = parseTime(ts); err != nil {
		return Game{}, err
	}
	return g, nil
}

// GetGame loads a game by ID.
func (s *Store) GetGame(ctx context.Context, id string) (Game, error) {
	return scanGame(s.db.QueryRowContext(ctx, `SELECT `+gameColumns+` FROM games WHERE id = ?`, id))
}

// DeleteGame deletes a game.
func (s *Store) DeleteGame(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM games WHERE id = ?`, id)
	return err
}

// Tx is a write transaction.
type Tx struct{ tx *sql.Tx }

// Tx runs fn in a transaction, committing if it returns nil.
func (s *Store) Tx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Tx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TouchPlayer sets a player's last_active_at.
func (t *Tx) TouchPlayer(ctx context.Context, id string, now time.Time) error {
	return touchPlayer(ctx, t.tx, id, now)
}

func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// UpdateGame writes the game's mutable fields, including last_active_at.
func (t *Tx) UpdateGame(ctx context.Context, g Game) error {
	res, err := t.tx.ExecContext(ctx,
		`UPDATE games SET host_player_id = ?, guest_player_id = ?,
			host_player_latest_move = ?, guest_player_latest_move = ?,
			host_player_wins = ?, guest_player_wins = ?, draws = ?,
			status = ?, game_state = ?, last_active_at = ?
		 WHERE id = ?`,
		nullable(g.HostPlayerID), nullable(g.GuestPlayerID),
		g.HostMove, g.GuestMove, g.HostWins, g.GuestWins, g.Draws,
		g.Status, g.GameState, formatTime(g.LastActiveAt), g.ID)
	if err != nil {
		return fmt.Errorf("update game: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveGame writes g and, if playerID is non-empty, touches that player, in
// one transaction.
func (s *Store) SaveGame(ctx context.Context, g Game, playerID string, now time.Time) error {
	return s.Tx(ctx, func(tx *Tx) error {
		if err := tx.UpdateGame(ctx, g); err != nil {
			return err
		}
		if playerID == "" {
			return nil
		}
		return tx.TouchPlayer(ctx, playerID, now)
	})
}
