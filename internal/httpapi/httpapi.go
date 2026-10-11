// Package httpapi serves the REST endpoints: /players, /games and /healthz.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hamologist/rps-backend/internal/clock"
	"github.com/hamologist/rps-backend/internal/store"
)

// MaxBodyBytes limits request bodies.
const MaxBodyBytes = 4096

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

// Options configures the API.
type Options struct {
	Store          *store.Store
	Clock          clock.Clock
	Logger         *slog.Logger
	AllowedOrigins []string
	// WS, if set, is mounted at GET /ws (without CORS handling).
	WS http.Handler
}

type api struct {
	store *store.Store
	clock clock.Clock
	log   *slog.Logger
}

// New returns the server's root handler.
func New(o Options) http.Handler {
	a := &api{store: o.Store, clock: o.Clock, log: o.Logger}

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("POST /players", a.createPlayer)
	apiMux.HandleFunc("POST /games", a.createGame)
	apiMux.HandleFunc("GET /healthz", a.healthz)
	cors := corsMiddleware(o.AllowedOrigins, apiMux)

	root := http.NewServeMux()
	for _, p := range []string{"/players", "/games", "/healthz"} {
		root.Handle(p, cors)
	}
	if o.WS != nil {
		root.Handle("GET /ws", o.WS)
	}
	return root
}

func corsMiddleware(allowed []string, next http.Handler) http.Handler {
	allowAny := slices.Contains(allowed, "*")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		ok := origin != "" && (allowAny || slices.Contains(allowed, origin))
		if ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			if ok {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	var b errorBody
	b.Error.Code, b.Error.Message = code, msg
	writeJSON(w, status, b)
}

func (a *api) internal(w http.ResponseWriter, what string, err error) {
	a.log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal server error")
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "malformed JSON body")
		return false
	}
	return true
}

type createPlayerRequest struct {
	Username string `json:"username"`
}

type createPlayerResponse struct {
	ID            string `json:"id"`
	Discriminator int    `json:"discriminator"`
	DisplayName   string `json:"display_name"`
	Secret        string `json:"secret"`
}

func (a *api) createPlayer(w http.ResponseWriter, r *http.Request) {
	var req createPlayerRequest
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Username)
	if !usernameRE.MatchString(name) {
		writeError(w, http.StatusBadRequest, "invalid_username",
			"username must be 1-32 characters of A-Z, a-z, 0-9, '_', '.', '-'")
		return
	}
	secret, hash := store.NewSecret()
	p, err := a.store.CreatePlayer(r.Context(), name, hash, a.clock.Now())
	if errors.Is(err, store.ErrUsernameExhausted) {
		writeError(w, http.StatusConflict, "username_exhausted", "no discriminators left for this username")
		return
	}
	if err != nil {
		a.internal(w, "create player", err)
		return
	}
	writeJSON(w, http.StatusCreated, createPlayerResponse{
		ID:            p.ID,
		Discriminator: p.Discriminator,
		DisplayName:   p.DisplayName(),
		Secret:        secret,
	})
}

type createGameRequest struct {
	PlayerID     string `json:"player_id"`
	PlayerSecret string `json:"player_secret"`
}

type createGameResponse struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

func (a *api) createGame(w http.ResponseWriter, r *http.Request) {
	var req createGameRequest
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := a.store.GetPlayer(r.Context(), req.PlayerID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.internal(w, "load player", err)
		return
	}
	if err != nil || !store.VerifySecret(req.PlayerSecret, p.SecretHash) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid player credentials")
		return
	}
	secret, hash := store.NewSecret()
	g, err := a.store.CreateGame(r.Context(), p.ID, hash, a.clock.Now())
	if err != nil {
		a.internal(w, "create game", err)
		return
	}
	writeJSON(w, http.StatusCreated, createGameResponse{ID: g.ID, Secret: secret})
}

func (a *api) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		a.log.Error("health check: database unreachable", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "database unreachable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
