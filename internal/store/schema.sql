CREATE TABLE IF NOT EXISTS players (
    id             TEXT PRIMARY KEY,
    base_name      TEXT NOT NULL,
    discriminator  INTEGER NOT NULL CHECK (discriminator BETWEEN 0 AND 9999),
    secret_hash    TEXT NOT NULL,
    last_active_at TEXT NOT NULL,
    UNIQUE (base_name, discriminator)
);

CREATE TABLE IF NOT EXISTS games (
    id                       TEXT PRIMARY KEY,
    secret_hash              TEXT NOT NULL,
    host_player_id           TEXT REFERENCES players(id) ON DELETE SET NULL,
    guest_player_id          TEXT REFERENCES players(id) ON DELETE SET NULL,
    host_player_latest_move  TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (host_player_latest_move  IN ('ROCK','PAPER','SCISSORS','PENDING')),
    guest_player_latest_move TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (guest_player_latest_move IN ('ROCK','PAPER','SCISSORS','PENDING')),
    host_player_wins         INTEGER NOT NULL DEFAULT 0,
    guest_player_wins        INTEGER NOT NULL DEFAULT 0,
    draws                    INTEGER NOT NULL DEFAULT 0,
    status                   TEXT NOT NULL
        CHECK (status IN ('WAITING_FOR_GUEST_TO_JOIN','ACTIVE','COMPLETE',
                          'HOST_ISSUED_REMATCH','GUEST_ISSUED_REMATCH','CLOSED')),
    game_state               TEXT NOT NULL
        CHECK (game_state IN ('WAITING_FOR_MOVES','DRAW','HOST_WON','GUEST_WON')),
    last_active_at           TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_players_last_active ON players(last_active_at);
CREATE INDEX IF NOT EXISTS idx_games_last_active   ON games(last_active_at);
