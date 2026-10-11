# RPS Backend — Implementation Spec

This spec is the authoritative source for implementation. `Plan.md` holds the original intent; where they differ, **this document wins**. It is written so a fresh session can build the whole project from scratch.

## 1. Overview

A single-process Go webserver that manages Rock-Paper-Scissors games between anonymous, temporary players.

- Players are created over HTTP and receive a secret.
- Games are created over HTTP by a player (who becomes the **host**) and receive a secret that is shared out-of-band with a second player (the **guest**).
- All gameplay happens over a WebSocket at `/ws` using a JSON envelope protocol.
- A **game** is a *series of rounds* between the same host and guest. Each round is one throw per player. Win/draw counters accumulate across rematches.
- Players and games expire after 24h of inactivity.

## 2. Technology

| Concern | Choice |
|---|---|
| Language | Go 1.26 |
| Module path | `github.com/hamologist/rps-backend` |
| HTTP routing | stdlib `net/http` `ServeMux` (method patterns, e.g. `POST /players`) |
| WebSocket | `github.com/coder/websocket` |
| Database | SQLite via `github.com/mattn/go-sqlite3` (**requires CGO / gcc**) |
| IDs | UUIDv4 (`github.com/google/uuid`) |
| Logging | `log/slog` |

> **Environment note:** the development sandbox does **not** have gcc installed. Install it before building (e.g. `apt-get update && apt-get install -y gcc`), and build with `CGO_ENABLED=1`.

## 3. Project Layout

```
cmd/rps-backend/main.go     – entrypoint; `serve` (default) and `healthcheck` subcommands
internal/config             – env vars + flag overrides
internal/clock              – Clock interface (real + fake for tests)
internal/store              – SQLite open/migrate, embedded schema.sql, queries, transactions
internal/rules              – pure RPS logic: round resolution, state transitions, validation
internal/hub                – connection registry, per-game locking, broadcast, action handling
internal/ws                 – /ws handler, envelope (de)serialization, read/write pumps, dispatch
internal/httpapi            – /players, /games, /healthz, CORS, JSON error helpers
internal/sweeper            – periodic expiry loop
docker/dev/                 – Dockerfile + .air.toml for hot-reload development
docker/deploy/              – production Dockerfile + compose.yaml
compose.yaml                – dev compose (repo root)
.dockerignore
README.md
SPEC.md
```

Package boundaries are guidance; keep `rules` free of I/O so it is trivially unit-testable.

## 4. Configuration

Env vars, each overridable by a flag of the same name in lowercase-kebab without the prefix (e.g. `RPS_DB_PATH` → `-db-path`). Flags win over env.

| Env var | Default | Meaning |
|---|---|---|
| `RPS_PORT` | `8080` | Listen port; server binds `0.0.0.0` |
| `RPS_DB_PATH` | `./rps.db` | SQLite file path |
| `RPS_ALLOWED_ORIGINS` | *(empty)* | Comma-separated origins (e.g. `http://localhost:5173`). Empty = same-origin only. `*` allows any. Used for CORS **and** WebSocket origin checks |
| `RPS_PLAYER_TTL` | `24h` | Player inactivity expiry |
| `RPS_GAME_TTL` | `24h` | Game inactivity expiry |
| `RPS_SWEEP_INTERVAL` | `1m` | Sweeper period |
| `RPS_AUTH_TIMEOUT` | `10s` | Time allowed for a WS connection to authenticate |
| `RPS_PING_INTERVAL` | `30s` | WS ping period; a ping that is not answered within one interval drops the connection |
| `RPS_LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |

Durations use Go `time.ParseDuration` syntax.

## 5. Data Model

### 5.1 Conventions

- IDs: UUIDv4 strings.
- Secrets: 32 bytes from `crypto/rand`, base64url (no padding). **Only the SHA-256 hex digest is stored.** Verification hashes the presented secret and compares with `crypto/subtle.ConstantTimeCompare`. Plaintext secrets are returned exactly once (at creation) and never logged.
- Timestamps: RFC3339 UTC strings (`time.RFC3339Nano`), produced from the injected `Clock`.
- SQLite pragmas on open: `journal_mode=WAL`, `foreign_keys=ON`, `busy_timeout=5000`.
- Schema lives in `internal/store/schema.sql`, embedded via `embed`, applied at startup with `CREATE TABLE IF NOT EXISTS` (idempotent).

### 5.2 Schema

```sql
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
```

`host_player_id` is nullable only so that `ON DELETE SET NULL` works after player expiry; it is always set at creation.

### 5.3 Enums

- **Move**: `ROCK`, `PAPER`, `SCISSORS` (+ `PENDING` in storage only).
- **Status**: `WAITING_FOR_GUEST_TO_JOIN`, `ACTIVE`, `COMPLETE`, `HOST_ISSUED_REMATCH`, `GUEST_ISSUED_REMATCH`, `CLOSED`.
- **GameState**: `WAITING_FOR_MOVES`, `DRAW`, `HOST_WON`, `GUEST_WON`.
- **Seat**: `HOST`, `GUEST`.

## 6. Game Rules & State Machine

### 6.1 Round resolution

ROCK beats SCISSORS, SCISSORS beats PAPER, PAPER beats ROCK; equal moves draw. When the second move locks in (same transaction):

- `game_state` ← `HOST_WON` / `GUEST_WON` / `DRAW`
- increment `host_player_wins` / `guest_player_wins` / `draws`
- `status` ← `COMPLETE`
- moves remain stored (revealed) until a rematch resets them.

### 6.2 Transitions

| From status | Trigger | To status | Other effects |
|---|---|---|---|
| *(none)* | `POST /games` | `WAITING_FOR_GUEST_TO_JOIN` | host = creator, `game_state=WAITING_FOR_MOVES`, moves `PENDING` |
| `WAITING_FOR_GUEST_TO_JOIN` | non-host `game::connect` | `ACTIVE` | guest = connector |
| `ACTIVE` | first `game::move` | `ACTIVE` | that seat's move stored |
| `ACTIVE` | second `game::move` | `COMPLETE` | resolve round (6.1) |
| `COMPLETE` | `game::rematch` by seat X | `X_ISSUED_REMATCH` | |
| `X_ISSUED_REMATCH` | `game::rematch` by the *other* seat (accept) | `ACTIVE` | both moves → `PENDING`, `game_state=WAITING_FOR_MOVES`; counters kept |
| `X_ISSUED_REMATCH` | `game::rematch_decline` by the other seat | `COMPLETE` | |
| any except `CLOSED` | `game::leave` by host or guest | `CLOSED` | |
| any except `CLOSED` | host or guest player expires | `CLOSED` | |

`CLOSED` is terminal: no connect, move, rematch, decline, or leave is accepted afterwards. The row stays until game expiry deletes it.

A disconnect (socket drop, replacement) **never** changes game state. There is no forfeit timer.

## 7. HTTP API

All request/response bodies are JSON. Request bodies are limited to 4 KiB. Unknown fields are ignored.

**Error shape** (all non-2xx):
```json
{ "error": { "code": "invalid_username", "message": "human readable" } }
```
Status codes: `400` validation / malformed JSON, `401` bad credentials, `409` conflict, `500` internal (message must not leak internals).

### 7.1 `POST /players` → `201`

Request: `{ "username": "hamologist" }`

Validation: trim whitespace; length 1–32; charset `[A-Za-z0-9_.-]`. Fail → `400 invalid_username`.

Discriminator: random integer in `[0, 9999]`; on unique-constraint collision retry with a new random value up to 20 times, then fall back to scanning for any free value for that `base_name`; if all 10,000 are taken → `409 username_exhausted`.

Sets `last_active_at = now`.

Response:
```json
{ "id": "uuid", "discriminator": 42, "display_name": "hamologist#0042", "secret": "base64url" }
```
`display_name` = `base_name + "#" + zero-padded 4-digit discriminator`.

### 7.2 `POST /games` → `201`

Request: `{ "player_id": "uuid", "player_secret": "..." }`

Unknown player or wrong secret → `401 invalid_credentials` (same response for both).

Creates a game with the creator as host, status `WAITING_FOR_GUEST_TO_JOIN`, `game_state WAITING_FOR_MOVES`, `last_active_at = now`. Updates the player's `last_active_at`.

Response: `{ "id": "uuid", "secret": "base64url" }`

The creator is **not** automatically attached over WebSocket; they must `game::connect` like anyone else.

### 7.3 `GET /healthz` → `200`

Response: `{ "status": "ok" }`. Should also verify the DB is reachable (`PingContext`); on failure return `503` with the standard error shape.

### 7.4 CORS

Applies to `/players`, `/games`, `/healthz`. If the request `Origin` matches `RPS_ALLOWED_ORIGINS` (or the list contains `*`), set `Access-Control-Allow-Origin` to the request origin, `Vary: Origin`, and on `OPTIONS` preflight respond `204` with `Access-Control-Allow-Methods: GET, POST, OPTIONS` and `Access-Control-Allow-Headers: Content-Type`. Non-matching origins get no CORS headers.

## 8. WebSocket Protocol

### 8.1 Connection

- Route: `GET /ws`, upgraded with `websocket.Accept`.
- Origin check: pass `OriginPatterns` derived from `RPS_ALLOWED_ORIGINS` (coder/websocket matches on **host**, so convert each origin URL to its host[:port]; `*` → `"*"`). Empty list = library default (same-origin).
- Read limit: 4 KiB per message (`conn.SetReadLimit(4096)`).
- Text frames only; each message is one JSON envelope.
- Server pings every `RPS_PING_INTERVAL`; failure → treat as disconnect.
- Pings do **not** update `last_active_at`.
- If the connection has not successfully authenticated within `RPS_AUTH_TIMEOUT`, close it with code `4001`.

### 8.2 Envelopes

Client → server:
```json
{ "action": "game::move", "request_id": "optional-client-string", "payload": { ... } }
```
`payload` may be omitted for actions with no payload.

Server → client:
```json
{ "type": "game::move::ok", "request_id": "echoed if provided", "payload": { ... } }
```

- Every client action gets exactly one direct reply: `"<action>::ok"` or `"error"`, echoing `request_id` if present.
- Error payload: `{ "code": "...", "message": "..." }`.
- Server-initiated **events** have no `request_id`.
- Errors never close the connection (except the cases in 8.6).

### 8.3 Actions

Common preconditions, checked in order:
1. Malformed JSON / missing `action` → `bad_request`.
2. Unknown action → `unknown_action`.
3. Any action other than `player::authenticate` before auth → `not_authenticated`.
4. Game actions (`move`, `rematch`, `rematch_decline`, `leave`) when the connection is not attached to a game → `not_in_game`.

#### `player::authenticate`
Payload: `{ "player_id", "player_secret" }`
- Already authenticated on this connection → `already_authenticated`.
- Bad credentials → `invalid_credentials`.
- If the player already has another live connection, that old connection receives event `connection::replaced` (payload `{}`), is detached from its game (opponent gets `game::opponent_disconnected`), and is closed with code `4000`.
- Updates player `last_active_at`.
- Reply `player::authenticate::ok` → `{ "player_id", "display_name" }`.
- Does **not** auto-attach to any previous game.

#### `game::connect`
Payload: `{ "game_id", "game_secret" }`
- Unknown game or wrong secret → `invalid_game_credentials` (same for both).
- Game `CLOSED` → `game_closed`.
- Player is host or guest → (re)attach. Player is neither, and guest slot empty → becomes guest; status `WAITING_FOR_GUEST_TO_JOIN` → `ACTIVE`. Player is neither and guest slot filled → `game_full`.
- A host may attach alone while `WAITING_FOR_GUEST_TO_JOIN`.
- If this connection is attached to a different game, it is first detached from that game (that game is **not** closed; its other participant gets `game::opponent_disconnected`). Reconnecting to the same game is a no-op re-attach.
- Updates player and game `last_active_at`.
- Reply `game::connect::ok` → snapshot (8.5) for this player.
- The opponent (if connected) receives `game::opponent_connected` and a fresh `game::state`.

#### `game::move`
Payload: `{ "move": "ROCK" | "PAPER" | "SCISSORS" }`
- Invalid/missing move → `invalid_move`.
- Status not `ACTIVE` → `invalid_state`.
- This seat already locked in this round → `already_locked_in`.
- Store move; if both locked, resolve (6.1).
- Updates player and game `last_active_at`.
- Reply `game::move::ok` (`{}`), then broadcast `game::state` to both connected participants.

#### `game::rematch`
No payload.
- Status `COMPLETE` → status `<SEAT>_ISSUED_REMATCH`; opponent gets `game::rematch_requested` (`{ "by": "HOST"|"GUEST" }`).
- Status is the *opponent's* `_ISSUED_REMATCH` → accept: reset per 6.2.
- Status is *own* `_ISSUED_REMATCH` → `rematch_already_issued`.
- Any other status → `invalid_state`.
- Allowed even if the opponent is disconnected (they see it in the snapshot on reconnect).
- Updates player and game `last_active_at`.
- Reply `game::rematch::ok` (`{}`), then broadcast `game::state`.

#### `game::rematch_decline`
No payload.
- Only valid when status is the *opponent's* `_ISSUED_REMATCH`; otherwise `invalid_state`.
- Status → `COMPLETE`; issuer gets `game::rematch_declined` (`{ "by": seat }`).
- Updates player and game `last_active_at`.
- Reply `game::rematch_decline::ok` (`{}`), then broadcast `game::state`.

#### `game::leave`
No payload.
- Status `CLOSED` → `game_closed`.
- Status → `CLOSED`. Leaver's connection is detached.
- Remaining participant (if connected) receives `game::closed` (`{ "reason": "left", "by": seat }`) and a final `game::state`, then is detached too.
- Updates player and game `last_active_at`.
- Reply `game::leave::ok` (`{}`).

### 8.4 Events (server-initiated)

| Type | Payload | When |
|---|---|---|
| `game::state` | snapshot | After any change to a game the recipient is attached to |
| `game::opponent_connected` | `{ "seat" }` | Opponent attaches |
| `game::opponent_disconnected` | `{ "seat" }` | Opponent's socket drops, is replaced, or they connect to another game |
| `game::rematch_requested` | `{ "by" }` | Opponent issued a rematch |
| `game::rematch_declined` | `{ "by" }` | Opponent declined your rematch |
| `game::closed` | `{ "reason": "left" \| "player_expired", "by"?: seat }` | Game closed by opponent leaving or expiring |
| `connection::replaced` | `{}` | Sent to the old connection before it is closed |

### 8.5 Snapshot (`game::state` payload, also `game::connect::ok`)

Personalized per recipient:
```json
{
  "game_id": "uuid",
  "status": "ACTIVE",
  "game_state": "WAITING_FOR_MOVES",
  "you": "HOST",
  "host":  { "player_id": "uuid", "display_name": "a#0001", "connected": true,  "locked_in": true,  "move": "ROCK", "wins": 2 },
  "guest": { "player_id": "uuid", "display_name": "b#0420", "connected": false, "locked_in": false, "move": null,   "wins": 1 },
  "draws": 0
}
```
- `guest` is `null` until a guest joins. `host`/`guest` may also be `null` if that player row was deleted by expiry.
- `connected` reflects whether that player currently has a live connection attached to *this* game.
- `locked_in` = stored move ≠ `PENDING`.
- `move`: own move always shown (or `null` if `PENDING`). Opponent's move is `null` while status is `ACTIVE`; in every other status it shows the stored move (or `null` if `PENDING`).
- Secrets are never included.

### 8.6 Close codes

| Code | Reason |
|---|---|
| `1000` | Normal / server shutdown |
| `4000` | Replaced by a newer connection for the same player |
| `4001` | Authentication timeout |
| `4002` | Player expired |
| `4003` | Game expired |
| `4004` | Slow consumer (send buffer full) |

### 8.7 Error codes (WebSocket)

`bad_request`, `unknown_action`, `not_authenticated`, `already_authenticated`, `invalid_credentials`, `invalid_game_credentials`, `game_full`, `game_closed`, `not_in_game`, `invalid_move`, `invalid_state`, `already_locked_in`, `rematch_already_issued`, `internal`.

## 9. Activity Tracking

| Action | Updates player `last_active_at` | Updates game `last_active_at` |
|---|---|---|
| `POST /players` | ✓ (initial) | |
| `POST /games` | ✓ | ✓ (initial) |
| `player::authenticate` | ✓ | |
| `game::connect` | ✓ | ✓ |
| `game::move` | ✓ | ✓ |
| `game::rematch` | ✓ | ✓ |
| `game::rematch_decline` | ✓ | ✓ |
| `game::leave` | ✓ | ✓ |

Only successful actions update timestamps. A connected but idle player can still expire.

## 10. Expiry (Sweeper)

Runs every `RPS_SWEEP_INTERVAL` (and once at startup), using the injected `Clock`.

**Players** with `last_active_at < now - RPS_PLAYER_TTL`, for each:
1. For every non-`CLOSED` game where they are host or guest: set `CLOSED`; notify the other participant if connected (`game::closed` `{reason:"player_expired", by: seat}` + `game::state`) and detach them. Game `last_active_at` is **not** updated.
2. Terminate the player's live connection (close code `4002`).
3. Delete the player row (FKs become `NULL`).

**Games** with `last_active_at < now - RPS_GAME_TTL`, for each:
1. Terminate the connections of all players attached to the game (close code `4003`). This closes the whole socket, not just a detach.
2. Delete the game row.

Each entity is processed under the relevant game lock(s) (see 11) so it does not race with in-flight actions.

## 11. Concurrency Architecture

- **Single process.** Running multiple instances is out of scope.
- **SQLite is the source of truth** for all player and game state. In-memory state is only live connections and attachments; a restart drops connections but loses no game data.
- **Hub** holds:
  - `playerID → *Conn` (at most one live connection per player)
  - `gameID → set of *Conn` attached
  - `gameID → *sync.Mutex` (created lazily; removable when no attachments and not in use)
- **Per-game mutex** serializes the full action pipeline for a game: load from DB → validate via `rules` → DB transaction → update in-memory attachments → enqueue broadcasts.
- **Lock order:** acquire the game mutex first, then the hub registry mutex for brief map operations. Never acquire a game mutex while holding the registry mutex. When an operation touches two games (switching games), lock the old game, detach, unlock, then lock the new game.
- **Per connection:** one read goroutine that dispatches actions synchronously, and one write goroutine draining a buffered send channel (size 32). If a send would block because the buffer is full, close the connection with `4004`.
- **Graceful shutdown** on SIGINT/SIGTERM: stop accepting connections, stop the sweeper, close all sockets with `1000`, `http.Server.Shutdown` with a 10s timeout, close the DB.

## 12. Binary & Subcommands

- `rps-backend` / `rps-backend serve`: runs the server.
- `rps-backend healthcheck`: GETs `http://127.0.0.1:$RPS_PORT/healthz` with a 3s timeout; exits `0` on 200, otherwise `1`. Used by Docker `HEALTHCHECK` because the deploy image has no shell or curl.

## 13. Docker

### 13.1 `docker/dev/`

- `docker/dev/Dockerfile`: `FROM golang:1.26-bookworm` (includes gcc). `CGO_ENABLED=1`. Installs `github.com/air-verse/air` (pin a version). `WORKDIR /app`. `CMD ["air", "-c", "docker/dev/.air.toml"]`.
- `docker/dev/.air.toml`: builds `./cmd/rps-backend` to `./tmp/rps-backend` and runs it. Watches `.go` and `.sql`, excludes `tmp`, `docker`, `.git`. Add `tmp/` to `.gitignore`.

### 13.2 Root `compose.yaml` (dev)

- Service `rps-backend`: `build: { context: ., dockerfile: docker/dev/Dockerfile }`.
- Bind mount `.:/app`. Named volumes for `/go/pkg/mod` and `/root/.cache/go-build`, plus `dev-data:/data`.
- `ports: ["8080:8080"]`.
- `environment`: `RPS_DB_PATH=/data/rps.db`, `RPS_ALLOWED_ORIGINS=${RPS_ALLOWED_ORIGINS:-}`, `RPS_LOG_LEVEL=debug`. Optional `env_file: { path: .env, required: false }`.
- `docker compose up` starts hot-reload development.

### 13.3 `docker/deploy/`

- `docker/deploy/Dockerfile`, multi-stage:
  - **builder** `golang:1.26-bookworm`: copy `go.mod`/`go.sum` first and run `go mod download` (layer cache), copy the source, `CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/rps-backend ./cmd/rps-backend`.
  - **runtime** `gcr.io/distroless/base-debian12:nonroot` (provides the glibc the CGO binary needs). Copy the binary, `ENV RPS_DB_PATH=/data/rps.db RPS_PORT=8080`, `VOLUME /data`, `EXPOSE 8080`, `USER nonroot:nonroot`, `HEALTHCHECK CMD ["/rps-backend","healthcheck"]`, `ENTRYPOINT ["/rps-backend"]`.
  - `/data` must be writable by the nonroot user: create it in the builder with the right ownership (uid/gid `65532`) and `COPY --chown`.
- `docker/deploy/compose.yaml`: service built with `context: ../..`, `dockerfile: docker/deploy/Dockerfile`; named volume `rps-data:/data`; `ports: ["8080:8080"]`; `restart: unless-stopped`; `healthcheck` using `["CMD","/rps-backend","healthcheck"]`; `RPS_ALLOWED_ORIGINS` passed through from the environment.

### 13.4 `.dockerignore` (repo root)

Exclude at least `.git`, `.claude`, `*.db`, `*.db-*`, `tmp/`, `.env`, `coverage.*`, `*.test`.

## 14. Testing Requirements

`go vet ./...` and `go test ./...` (with `-race`) must pass.

- **Clock:** all time comes from `clock.Clock` (`Now() time.Time`). Tests use a fake clock that can advance.
- **`rules` unit tests** (table-driven): all 9 move pairs; every transition in 6.2 including illegal ones; snapshot move-visibility rules.
- **`store` tests** against a temp SQLite file (`t.TempDir()`): schema is idempotent; player creation and discriminator collision/retry (inject the random source); `username_exhausted`; secret hash verification; `ON DELETE SET NULL`; expiry queries.
- **`httpapi` tests** (`httptest`): validation errors, 401s, response shapes, CORS preflight allow/deny, `/healthz`.
- **End-to-end tests:** `httptest.Server` plus the `coder/websocket` client, covering:
  - create players → create game → both authenticate → host connects (waiting) → guest connects (ACTIVE, host notified) → moves hidden until resolution → round resolves with correct counters
  - rematch issue → accept → state reset, counters kept; rematch issue → decline → `COMPLETE`; issuer re-sending rematch → `rematch_already_issued`
  - leave → `CLOSED`, opponent notified and detached; any action on a `CLOSED` game is rejected
  - third player → `game_full`; wrong game secret → `invalid_game_credentials`
  - connection replacement: old connection gets `connection::replaced` and close `4000`
  - disconnect + reconnect: state preserved, opponent sees disconnected/connected
  - switching games detaches without closing the old one
  - unauthenticated actions → `not_authenticated`; auth timeout → close `4001` (short timeout via config)
  - malformed JSON / unknown action keep the connection open
  - expiry: advance the fake clock and trigger a sweep directly (expose a `SweepOnce(ctx)` method); verify player expiry closes games, notifies the opponent, and closes with `4002`; game expiry closes sockets with `4003` and deletes the row
  - `request_id` is echoed

## 15. README

Must document:
- prerequisites (Go 1.26, gcc / CGO)
- running locally (`go run ./cmd/rps-backend`)
- the config table
- running tests
- Docker dev (`docker compose up`) and deploy (`docker compose -f docker/deploy/compose.yaml up --build`)
- a short protocol quick-reference that links to this spec

## 16. Acceptance Criteria / Definition of Done

1. All code in the layout above; `go vet ./...` clean; `go test -race ./...` passes.
2. `docker build -f docker/dev/Dockerfile .` and `docker build -f docker/deploy/Dockerfile .` both succeed.
3. `docker compose -f docker/deploy/compose.yaml up -d --build` starts, the container reports **healthy**, and `curl localhost:8080/healthz` returns `{"status":"ok"}`. A `POST /players` smoke request succeeds. Tear down afterwards.
4. `docker compose up` (dev) starts and serves `/healthz`.
5. README is complete per section 15.

## 17. Suggested Implementation Order

1. `go mod init`, config, clock, `store` + schema + tests.
2. `rules` + tests.
3. `httpapi` (`/players`, `/games`, `/healthz`, CORS) + tests.
4. `hub` + `ws` (auth, connect, move, rematch, decline, leave, events, snapshots) + end-to-end tests.
5. `sweeper` + expiry tests.
6. `main.go` wiring, graceful shutdown, `healthcheck` subcommand.
7. Docker dev/deploy, compose files, `.dockerignore`, then smoke tests.
8. README.
