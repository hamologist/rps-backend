# rps-backend

A single-process Go server that runs Rock-Paper-Scissors games between anonymous, temporary players. Players and games are created over HTTP; all gameplay happens over a WebSocket at `/ws`. State lives in SQLite.

The full behaviour is defined in [SPEC.md](SPEC.md).

## Prerequisites

- Go 1.26
- A C compiler (`gcc`). The SQLite driver (`mattn/go-sqlite3`) uses CGO, so builds need `CGO_ENABLED=1` (the default when `gcc` is on `PATH`). On Debian/Ubuntu: `apt-get install -y gcc`.
- Docker, if you want the containerized dev or deploy setups.

## Running locally

```sh
go run ./cmd/rps-backend            # same as: go run ./cmd/rps-backend serve
go run ./cmd/rps-backend -port 9000 -db-path /tmp/rps.db -log-level debug
```

`rps-backend healthcheck` GETs `http://127.0.0.1:$RPS_PORT/healthz` and exits `0` if it returns 200, `1` otherwise. The Docker `HEALTHCHECK` uses it.

## Configuration

Every setting comes from an environment variable and can be overridden by a flag. The flag name is the variable name without the `RPS_` prefix, in lowercase-kebab (`RPS_DB_PATH` → `-db-path`). Flags take precedence over env.

| Env var | Flag | Default | Meaning |
|---|---|---|---|
| `RPS_PORT` | `-port` | `8080` | Listen port (binds `0.0.0.0`) |
| `RPS_DB_PATH` | `-db-path` | `./rps.db` | SQLite file path |
| `RPS_ALLOWED_ORIGINS` | `-allowed-origins` | *(empty)* | Comma-separated origins, e.g. `http://localhost:5173`. Empty means same-origin only; `*` allows any origin. Used for both CORS and the WebSocket origin check |
| `RPS_PLAYER_TTL` | `-player-ttl` | `24h` | How long a player can be inactive before expiring |
| `RPS_GAME_TTL` | `-game-ttl` | `24h` | How long a game can be inactive before expiring |
| `RPS_SWEEP_INTERVAL` | `-sweep-interval` | `1m` | How often the expiry sweeper runs |
| `RPS_AUTH_TIMEOUT` | `-auth-timeout` | `10s` | How long a WebSocket has to authenticate |
| `RPS_PING_INTERVAL` | `-ping-interval` | `30s` | WebSocket ping period. A ping not answered within one interval drops the connection |
| `RPS_LOG_LEVEL` | `-log-level` | `info` | `debug` / `info` / `warn` / `error` |

Durations use Go's [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration) syntax.

## Tests

```sh
go vet ./...
go test -race ./...
```

The tests cover the pure game rules, the SQLite store (against temp files), the HTTP API, and end-to-end WebSocket flows. Expiry tests use a fake clock and trigger `Sweeper.SweepOnce` directly.

## Docker

### Development (hot reload)

```sh
docker compose up
```

This builds `docker/dev/Dockerfile`, bind-mounts the repo at `/app`, and runs [air](https://github.com/air-verse/air), which rebuilds and restarts the server when `.go` or `.sql` files change. The database is kept in the `dev-data` volume at `/data/rps.db`. Set `RPS_ALLOWED_ORIGINS` in your shell or in a `.env` file to allow a frontend dev server.

### Deploy

```sh
docker compose -f docker/deploy/compose.yaml up -d --build
```

This builds a multi-stage image: a CGO build on `golang:1.26-bookworm`, run on `gcr.io/distroless/base-debian12:nonroot`. Data is stored in the `rps-data` volume. The container reports healthy once `/healthz` responds.

## Protocol quick reference

Full details, including every transition and error code, are in [SPEC.md](SPEC.md) §7–§8.

### HTTP

| Request | Body | Response |
|---|---|---|
| `POST /players` | `{"username":"hamologist"}` | `201 {"id","discriminator","display_name":"hamologist#0042","secret"}` |
| `POST /games` | `{"player_id","player_secret"}` | `201 {"id","secret"}` (the caller is the host) |
| `GET /healthz` | none | `200 {"status":"ok"}` or `503` |

Errors look like `{"error":{"code":"invalid_username","message":"..."}}`.

### WebSocket (`/ws`)

Client → server: `{"action": "...", "request_id": "optional", "payload": {...}}`
Server → client: `{"type": "...", "request_id": "echoed", "payload": {...}}`

Each action gets exactly one reply, either `<action>::ok` or `error` (`{"code","message"}`). Events the server initiates have no `request_id`.

| Action | Payload | Notes |
|---|---|---|
| `player::authenticate` | `{"player_id","player_secret"}` | Must be the first action. Replaces any older connection for the same player (that connection is closed with `4000`) |
| `game::connect` | `{"game_id","game_secret"}` | Joins as guest, or re-attaches as host or guest. Replies with a snapshot |
| `game::move` | `{"move":"ROCK"\|"PAPER"\|"SCISSORS"}` | The round resolves once both players have locked in |
| `game::rematch` | none | Issues a rematch, or accepts the opponent's |
| `game::rematch_decline` | none | Declines the opponent's rematch |
| `game::leave` | none | Closes the game for both players |

Events: `game::state` (snapshot), `game::opponent_connected`, `game::opponent_disconnected`, `game::rematch_requested`, `game::rematch_declined`, `game::closed`, `connection::replaced`.

Close codes: `1000` normal/shutdown, `4000` replaced, `4001` auth timeout, `4002` player expired, `4003` game expired, `4004` slow consumer.

Typical flow: create two players, have one create a game, share the game id and secret out-of-band, then both players `player::authenticate` and `game::connect`. After that, each sends `game::move`, and either one can start a rematch with `game::rematch`.
