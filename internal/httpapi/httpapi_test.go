package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hamologist/rps-backend/internal/clock"
	"github.com/hamologist/rps-backend/internal/store"
)

func newServer(t *testing.T, origins ...string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(New(Options{
		Store:          st,
		Clock:          clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		AllowedOrigins: origins,
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

func post(t *testing.T, url, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func TestCreatePlayer(t *testing.T) {
	srv, _ := newServer(t)
	resp, body := post(t, srv.URL+"/players", `{"username":"  hamologist  ","extra":1}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d %v", resp.StatusCode, body)
	}
	disc := int(body["discriminator"].(float64))
	if !regexp.MustCompile(`^hamologist#\d{4}$`).MatchString(body["display_name"].(string)) || disc < 0 || disc > 9999 {
		t.Fatalf("body %v", body)
	}
	if body["id"] == "" || len(body["secret"].(string)) != 43 {
		t.Fatalf("body %v", body)
	}
}

func TestCreatePlayerValidation(t *testing.T) {
	srv, _ := newServer(t)
	for _, tc := range []struct {
		body, code string
	}{
		{`{"username":""}`, "invalid_username"},
		{`{"username":"   "}`, "invalid_username"},
		{`{}`, "invalid_username"},
		{`{"username":"has space"}`, "invalid_username"},
		{`{"username":"emoji😀"}`, "invalid_username"},
		{`{"username":"` + strings.Repeat("a", 33) + `"}`, "invalid_username"},
		{`{"username":`, "bad_request"},
		{`not json`, "bad_request"},
		{`{"username":"` + strings.Repeat("a", 5000) + `"}`, "bad_request"},
	} {
		resp, body := post(t, srv.URL+"/players", tc.body)
		if resp.StatusCode != http.StatusBadRequest || errCode(body) != tc.code {
			t.Errorf("%.40s: status %d body %v", tc.body, resp.StatusCode, body)
		}
	}
	resp, body := post(t, srv.URL+"/players", `{"username":"`+strings.Repeat("a", 32)+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("32 chars: %d %v", resp.StatusCode, body)
	}
}

func TestCreateGame(t *testing.T) {
	srv, st := newServer(t)
	_, p := post(t, srv.URL+"/players", `{"username":"host"}`)
	id, secret := p["id"].(string), p["secret"].(string)

	for _, body := range []string{
		`{"player_id":"` + id + `","player_secret":"wrong"}`,
		`{"player_id":"nope","player_secret":"` + secret + `"}`,
		`{}`,
	} {
		resp, out := post(t, srv.URL+"/games", body)
		if resp.StatusCode != http.StatusUnauthorized || errCode(out) != "invalid_credentials" {
			t.Errorf("%s: %d %v", body, resp.StatusCode, out)
		}
	}

	resp, out := post(t, srv.URL+"/games", `{"player_id":"`+id+`","player_secret":"`+secret+`"}`)
	if resp.StatusCode != http.StatusCreated || out["id"] == "" || len(out["secret"].(string)) != 43 {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	g, err := st.GetGame(context.Background(), out["id"].(string))
	if err != nil || g.HostPlayerID != id || g.Status != "WAITING_FOR_GUEST_TO_JOIN" || g.GameState != "WAITING_FOR_MOVES" {
		t.Fatalf("game %+v %v", g, err)
	}
	if !store.VerifySecret(out["secret"].(string), g.SecretHash) {
		t.Fatal("game secret does not verify")
	}
}

func TestHealthz(t *testing.T) {
	srv, st := newServer(t)
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"status":"ok"}` {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}

	st.Close()
	resp, err = http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || errCode(out) == "" {
		t.Fatalf("closed db: %d %v", resp.StatusCode, out)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/players")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func preflight(t *testing.T, url, origin string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodOptions, url, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestCORS(t *testing.T) {
	srv, _ := newServer(t, "http://localhost:5173")

	resp := preflight(t, srv.URL+"/players", "http://localhost:5173")
	if resp.StatusCode != http.StatusNoContent ||
		resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" ||
		resp.Header.Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" ||
		resp.Header.Get("Access-Control-Allow-Headers") != "Content-Type" ||
		resp.Header.Get("Vary") != "Origin" {
		t.Fatalf("allowed preflight: %d %v", resp.StatusCode, resp.Header)
	}

	resp = preflight(t, srv.URL+"/games", "http://evil.example")
	if resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.Header.Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("denied preflight got CORS headers: %v", resp.Header)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/healthz", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("simple request missing ACAO: %v", r2.Header)
	}
}

func TestCORSWildcard(t *testing.T) {
	srv, _ := newServer(t, "*")
	resp := preflight(t, srv.URL+"/players", "http://anything.example")
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://anything.example" {
		t.Fatalf("wildcard: %v", resp.Header)
	}
}

func TestCORSEmptyIsSameOriginOnly(t *testing.T) {
	srv, _ := newServer(t)
	resp := preflight(t, srv.URL+"/players", "http://localhost:5173")
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("empty list allowed cross origin: %v", resp.Header)
	}
}
