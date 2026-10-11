package ws_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/hamologist/rps-backend/internal/clock"
	"github.com/hamologist/rps-backend/internal/httpapi"
	"github.com/hamologist/rps-backend/internal/hub"
	"github.com/hamologist/rps-backend/internal/store"
	"github.com/hamologist/rps-backend/internal/sweeper"
	"github.com/hamologist/rps-backend/internal/ws"
)

const waitFor = 3 * time.Second

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type env struct {
	t       *testing.T
	srv     *httptest.Server
	store   *store.Store
	clock   *clock.Fake
	sweeper *sweeper.Sweeper
}

type envOpts struct {
	authTimeout time.Duration
	playerTTL   time.Duration
	gameTTL     time.Duration
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	if o.authTimeout == 0 {
		o.authTimeout = 10 * time.Second
	}
	if o.playerTTL == 0 {
		o.playerTTL = 24 * time.Hour
	}
	if o.gameTTL == 0 {
		o.gameTTL = 24 * time.Hour
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	clk := clock.NewFake(t0)
	h := hub.New(st, clk, log)
	wsh := ws.New(ws.Options{Hub: h, Logger: log, AuthTimeout: o.authTimeout, PingInterval: time.Minute})
	srv := httptest.NewServer(httpapi.New(httpapi.Options{Store: st, Clock: clk, Logger: log, WS: wsh}))
	t.Cleanup(func() {
		h.CloseAll(hub.CloseNormal, "test done")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		wsh.Wait(ctx)
		srv.Close()
		st.Close()
	})
	return &env{
		t: t, srv: srv, store: st, clock: clk,
		sweeper: &sweeper.Sweeper{Store: st, Hub: h, Clock: clk, Logger: log, PlayerTTL: o.playerTTL, GameTTL: o.gameTTL, Interval: time.Hour},
	}
}

func (e *env) post(path string, body any) map[string]any {
	e.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(e.srv.URL+path, "application/json", strings.NewReader(string(b)))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("POST %s: %d", path, resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

type player struct{ id, secret, name string }

func (e *env) player(name string) player {
	out := e.post("/players", map[string]string{"username": name})
	return player{out["id"].(string), out["secret"].(string), out["display_name"].(string)}
}

type game struct{ id, secret string }

func (e *env) game(host player) game {
	out := e.post("/games", map[string]string{"player_id": host.id, "player_secret": host.secret})
	return game{out["id"].(string), out["secret"].(string)}
}

type msg struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

func (m msg) decode(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(m.Payload, v); err != nil {
		t.Fatalf("decode %s: %v", m.Payload, err)
	}
}

func (m msg) errCode(t *testing.T) string {
	t.Helper()
	if m.Type != "error" {
		t.Fatalf("expected error, got %s %s", m.Type, m.Payload)
	}
	var p struct{ Code string }
	m.decode(t, &p)
	return p.Code
}

type client struct {
	t      *testing.T
	conn   *websocket.Conn
	msgs   chan msg
	done   chan struct{}
	err    error
	nextID int
}

func (e *env) dial() *client {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.srv.URL, "http")+"/ws", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	c := &client{t: e.t, conn: conn, msgs: make(chan msg, 64), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				c.err = err
				close(c.msgs)
				return
			}
			var m msg
			if err := json.Unmarshal(data, &m); err != nil {
				c.err = err
				close(c.msgs)
				return
			}
			c.msgs <- m
		}
	}()
	e.t.Cleanup(func() { conn.CloseNow() })
	return c
}

func (c *client) sendRaw(s string) {
	c.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitFor)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, []byte(s)); err != nil {
		c.t.Fatal(err)
	}
}

// do sends an action and returns the direct reply.
func (c *client) do(action string, payload any) msg {
	c.t.Helper()
	c.nextID++
	id := action + "#" + string(rune('0'+c.nextID%10))
	env := map[string]any{"action": action, "request_id": id}
	if payload != nil {
		env["payload"] = payload
	}
	b, _ := json.Marshal(env)
	c.sendRaw(string(b))
	m := c.next()
	if m.RequestID != id {
		c.t.Fatalf("reply %s request_id=%q, want %q", m.Type, m.RequestID, id)
	}
	return m
}

// ok sends an action and requires a successful reply.
func (c *client) ok(action string, payload any) msg {
	c.t.Helper()
	m := c.do(action, payload)
	if m.Type != action+"::ok" {
		c.t.Fatalf("%s: got %s %s", action, m.Type, m.Payload)
	}
	return m
}

func (c *client) fail(action string, payload any, code string) {
	c.t.Helper()
	if got := c.do(action, payload).errCode(c.t); got != code {
		c.t.Fatalf("%s: error %s, want %s", action, got, code)
	}
}

func (c *client) next() msg {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		if !ok {
			c.t.Fatalf("connection closed: %v", c.err)
		}
		return m
	case <-time.After(waitFor):
		c.t.Fatal("timed out waiting for message")
	}
	return msg{}
}

func (c *client) expect(typ string) msg {
	c.t.Helper()
	m := c.next()
	if m.Type != typ {
		c.t.Fatalf("got %s %s, want %s", m.Type, m.Payload, typ)
	}
	if m.RequestID != "" {
		c.t.Fatalf("event %s has request_id %q", typ, m.RequestID)
	}
	return m
}

func (c *client) quiet() {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		if ok {
			c.t.Fatalf("unexpected message %s %s", m.Type, m.Payload)
		}
		c.t.Fatalf("unexpected close: %v", c.err)
	case <-time.After(50 * time.Millisecond):
	}
}

func (c *client) expectClose(code websocket.StatusCode) {
	c.t.Helper()
	for {
		select {
		case m, ok := <-c.msgs:
			if ok {
				c.t.Fatalf("expected close %d, got message %s", code, m.Type)
			}
			if got := websocket.CloseStatus(c.err); got != code {
				c.t.Fatalf("close status %d (%v), want %d", got, c.err, code)
			}
			return
		case <-time.After(waitFor):
			c.t.Fatalf("timed out waiting for close %d", code)
		}
	}
}

func (c *client) auth(p player) {
	c.t.Helper()
	var out struct {
		PlayerID    string `json:"player_id"`
		DisplayName string `json:"display_name"`
	}
	m := c.ok("player::authenticate", map[string]string{"player_id": p.id, "player_secret": p.secret})
	m.decode(c.t, &out)
	if out.PlayerID != p.id || out.DisplayName != p.name {
		c.t.Fatalf("auth reply %s", m.Payload)
	}
}

func (c *client) connect(g game) snapshot {
	c.t.Helper()
	var s snapshot
	c.ok("game::connect", map[string]string{"game_id": g.id, "game_secret": g.secret}).decode(c.t, &s)
	return s
}

type seatView struct {
	PlayerID    string  `json:"player_id"`
	DisplayName string  `json:"display_name"`
	Connected   bool    `json:"connected"`
	LockedIn    bool    `json:"locked_in"`
	Move        *string `json:"move"`
	Wins        int     `json:"wins"`
}

type snapshot struct {
	GameID    string    `json:"game_id"`
	Status    string    `json:"status"`
	GameState string    `json:"game_state"`
	You       string    `json:"you"`
	Host      *seatView `json:"host"`
	Guest     *seatView `json:"guest"`
	Draws     int       `json:"draws"`
}

func (c *client) state() snapshot {
	c.t.Helper()
	var s snapshot
	c.expect("game::state").decode(c.t, &s)
	return s
}

func moveOf(v *seatView) string {
	if v == nil || v.Move == nil {
		return ""
	}
	return *v.Move
}

// setup creates two authenticated players connected to one ACTIVE game.
func setup(t *testing.T, e *env) (host, guest *client, hp, gp player, g game) {
	t.Helper()
	hp, gp = e.player("alice"), e.player("bob")
	g = e.game(hp)
	host, guest = e.dial(), e.dial()
	host.auth(hp)
	guest.auth(gp)
	host.connect(g)
	guest.connect(g)
	host.expect("game::opponent_connected")
	host.state()
	return
}

func play(t *testing.T, host, guest *client, hm, gm string) snapshot {
	t.Helper()
	host.ok("game::move", map[string]string{"move": hm})
	host.state()
	guest.state()
	guest.ok("game::move", map[string]string{"move": gm})
	s := guest.state()
	host.state()
	return s
}

func TestFullRound(t *testing.T) {
	e := newEnv(t, envOpts{})
	hp, gp := e.player("alice"), e.player("bob")
	g := e.game(hp)

	host, guest := e.dial(), e.dial()
	host.auth(hp)
	guest.auth(gp)

	s := host.connect(g)
	if s.Status != "WAITING_FOR_GUEST_TO_JOIN" || s.You != "HOST" || s.Guest != nil ||
		s.Host == nil || !s.Host.Connected || s.Host.DisplayName != hp.name || s.GameID != g.id {
		t.Fatalf("host waiting snapshot: %+v", s)
	}
	host.fail("game::move", map[string]string{"move": "ROCK"}, "invalid_state")

	s = guest.connect(g)
	if s.Status != "ACTIVE" || s.You != "GUEST" || s.Guest == nil || s.Guest.PlayerID != gp.id || !s.Host.Connected {
		t.Fatalf("guest snapshot: %+v", s)
	}
	var seat struct{ Seat string }
	host.expect("game::opponent_connected").decode(t, &seat)
	if seat.Seat != "GUEST" {
		t.Fatalf("opponent seat %s", seat.Seat)
	}
	if s := host.state(); s.Status != "ACTIVE" || !s.Guest.Connected || s.You != "HOST" {
		t.Fatalf("host state after join: %+v", s)
	}

	// Host moves; the guest must not see it.
	host.fail("game::move", map[string]string{"move": "LIZARD"}, "invalid_move")
	host.fail("game::move", nil, "invalid_move")
	host.ok("game::move", map[string]string{"move": "ROCK"})
	hs, gs := host.state(), guest.state()
	if moveOf(hs.Host) != "ROCK" || !hs.Host.LockedIn {
		t.Fatalf("host should see own move: %+v", hs.Host)
	}
	if gs.Host.Move != nil || !gs.Host.LockedIn || gs.Guest.LockedIn {
		t.Fatalf("guest must not see host move: %+v %+v", gs.Host, gs.Guest)
	}
	host.fail("game::move", map[string]string{"move": "PAPER"}, "already_locked_in")

	guest.ok("game::move", map[string]string{"move": "SCISSORS"})
	gs, hs = guest.state(), host.state()
	for _, s := range []snapshot{gs, hs} {
		if s.Status != "COMPLETE" || s.GameState != "HOST_WON" || s.Host.Wins != 1 || s.Guest.Wins != 0 || s.Draws != 0 ||
			moveOf(s.Host) != "ROCK" || moveOf(s.Guest) != "SCISSORS" {
			t.Fatalf("resolved snapshot: %+v host=%+v guest=%+v", s, s.Host, s.Guest)
		}
	}
	host.fail("game::move", map[string]string{"move": "ROCK"}, "invalid_state")
}

func TestRematch(t *testing.T) {
	e := newEnv(t, envOpts{})
	host, guest, _, _, _ := setup(t, e)
	play(t, host, guest, "PAPER", "PAPER")

	// Issue → accept.
	host.ok("game::rematch", nil)
	var by struct{ By string }
	guest.expect("game::rematch_requested").decode(t, &by)
	if by.By != "HOST" {
		t.Fatalf("rematch by %s", by.By)
	}
	if s := host.state(); s.Status != "HOST_ISSUED_REMATCH" {
		t.Fatalf("status %s", s.Status)
	}
	guest.state()
	host.fail("game::rematch", nil, "rematch_already_issued")
	host.fail("game::rematch_decline", nil, "invalid_state")

	guest.ok("game::rematch", nil)
	s := guest.state()
	host.state()
	if s.Status != "ACTIVE" || s.GameState != "WAITING_FOR_MOVES" || s.Draws != 1 ||
		s.Host.LockedIn || s.Guest.LockedIn || s.Host.Move != nil || s.Guest.Move != nil {
		t.Fatalf("after accept: %+v %+v %+v", s, s.Host, s.Guest)
	}
	guest.fail("game::rematch", nil, "invalid_state")

	// Round 2, then issue → decline.
	s = play(t, host, guest, "ROCK", "PAPER")
	if s.GameState != "GUEST_WON" || s.Guest.Wins != 1 || s.Draws != 1 {
		t.Fatalf("round 2: %+v", s)
	}
	guest.ok("game::rematch", nil)
	host.expect("game::rematch_requested")
	host.state()
	guest.state()
	host.ok("game::rematch_decline", nil)
	guest.expect("game::rematch_declined").decode(t, &by)
	if by.By != "HOST" {
		t.Fatalf("declined by %s", by.By)
	}
	if s := guest.state(); s.Status != "COMPLETE" || moveOf(s.Host) != "ROCK" {
		t.Fatalf("after decline: %+v", s)
	}
	host.state()
}

func TestLeave(t *testing.T) {
	e := newEnv(t, envOpts{})
	host, guest, _, _, g := setup(t, e)

	guest.ok("game::leave", nil)
	var closed struct{ Reason, By string }
	host.expect("game::closed").decode(t, &closed)
	if closed.Reason != "left" || closed.By != "GUEST" {
		t.Fatalf("closed payload %+v", closed)
	}
	if s := host.state(); s.Status != "CLOSED" || s.Guest.Connected {
		t.Fatalf("final state %+v", s)
	}
	guest.quiet()

	// Both are detached.
	host.fail("game::move", map[string]string{"move": "ROCK"}, "not_in_game")
	guest.fail("game::leave", nil, "not_in_game")
	host.fail("game::rematch", nil, "not_in_game")
	// Reconnecting to a closed game is rejected.
	host.fail("game::connect", map[string]string{"game_id": g.id, "game_secret": g.secret}, "game_closed")
	guest.fail("game::connect", map[string]string{"game_id": g.id, "game_secret": g.secret}, "game_closed")

	stored, _ := e.store.GetGame(context.Background(), g.id)
	if stored.Status != "CLOSED" {
		t.Fatalf("stored status %s", stored.Status)
	}
}

func TestGameFullAndBadSecret(t *testing.T) {
	e := newEnv(t, envOpts{})
	_, _, _, _, g := setup(t, e)
	third := e.dial()
	third.auth(e.player("carol"))
	third.fail("game::connect", map[string]string{"game_id": g.id, "game_secret": g.secret}, "game_full")
	third.fail("game::connect", map[string]string{"game_id": g.id, "game_secret": "nope"}, "invalid_game_credentials")
	third.fail("game::connect", map[string]string{"game_id": "missing", "game_secret": g.secret}, "invalid_game_credentials")
}

func TestAuthErrors(t *testing.T) {
	e := newEnv(t, envOpts{})
	p := e.player("dave")
	c := e.dial()
	c.fail("game::connect", map[string]string{}, "not_authenticated")
	c.fail("game::move", map[string]string{"move": "ROCK"}, "not_authenticated")
	c.fail("player::authenticate", map[string]string{"player_id": p.id, "player_secret": "bad"}, "invalid_credentials")
	c.fail("player::authenticate", map[string]string{"player_id": "nobody", "player_secret": p.secret}, "invalid_credentials")
	c.auth(p)
	c.fail("player::authenticate", map[string]string{"player_id": p.id, "player_secret": p.secret}, "already_authenticated")
	c.fail("game::move", map[string]string{"move": "ROCK"}, "not_in_game")
	c.fail("game::leave", nil, "not_in_game")
}

func TestMalformedKeepsConnectionOpen(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.dial()
	c.sendRaw(`{not json`)
	if code := c.next().errCode(t); code != "bad_request" {
		t.Fatalf("got %s", code)
	}
	c.sendRaw(`{"request_id":"x"}`)
	m := c.next()
	if m.errCode(t) != "bad_request" || m.RequestID != "x" {
		t.Fatalf("missing action: %+v", m)
	}
	c.sendRaw(`{"action":"game::dance","request_id":"r1"}`)
	m = c.next()
	if m.errCode(t) != "unknown_action" || m.RequestID != "r1" {
		t.Fatalf("unknown action: %+v", m)
	}
	// Still usable.
	c.auth(e.player("erin"))
}

func TestRequestIDEchoAndOmitted(t *testing.T) {
	e := newEnv(t, envOpts{})
	p := e.player("frank")
	c := e.dial()
	c.sendRaw(`{"action":"player::authenticate","request_id":"abc-123","payload":{"player_id":"` + p.id + `","player_secret":"` + p.secret + `"}}`)
	if m := c.next(); m.Type != "player::authenticate::ok" || m.RequestID != "abc-123" {
		t.Fatalf("%+v", m)
	}
	c.sendRaw(`{"action":"game::leave"}`)
	if m := c.next(); m.RequestID != "" || m.errCode(t) != "not_in_game" {
		t.Fatalf("%+v", m)
	}
}

func TestConnectionReplaced(t *testing.T) {
	e := newEnv(t, envOpts{})
	host, guest, hp, _, g := setup(t, e)

	newHost := e.dial()
	newHost.auth(hp)
	host.expect("connection::replaced")
	host.expectClose(hub.CloseReplaced)
	var seat struct{ Seat string }
	guest.expect("game::opponent_disconnected").decode(t, &seat)
	if seat.Seat != "HOST" {
		t.Fatalf("seat %s", seat.Seat)
	}

	// New connection is not auto-attached but can reattach.
	newHost.fail("game::move", map[string]string{"move": "ROCK"}, "not_in_game")
	s := newHost.connect(g)
	if s.You != "HOST" || s.Status != "ACTIVE" {
		t.Fatalf("reattach %+v", s)
	}
	guest.expect("game::opponent_connected")
	guest.state()
}

func TestDisconnectReconnect(t *testing.T) {
	e := newEnv(t, envOpts{})
	host, guest, _, gp, g := setup(t, e)
	guest.ok("game::move", map[string]string{"move": "PAPER"})
	guest.state()
	host.state()

	guest.conn.Close(websocket.StatusNormalClosure, "bye")
	var seat struct{ Seat string }
	host.expect("game::opponent_disconnected").decode(t, &seat)
	if seat.Seat != "GUEST" {
		t.Fatalf("seat %s", seat.Seat)
	}

	g2 := e.dial()
	g2.auth(gp)
	s := g2.connect(g)
	if s.Status != "ACTIVE" || moveOf(s.Guest) != "PAPER" || !s.Guest.LockedIn || s.You != "GUEST" {
		t.Fatalf("state not preserved: %+v %+v", s, s.Guest)
	}
	host.expect("game::opponent_connected")
	if s := host.state(); !s.Guest.Connected || s.Guest.Move != nil {
		t.Fatalf("host view: %+v", s.Guest)
	}

	// Same-game reconnect on the same socket is a no-op re-attach.
	g2.connect(g)
	host.quiet()
}

func TestSwitchGames(t *testing.T) {
	e := newEnv(t, envOpts{})
	host, guest, _, gp, g1 := setup(t, e)

	g2 := e.game(gp)
	s := guest.connect(g2)
	if s.Status != "WAITING_FOR_GUEST_TO_JOIN" || s.You != "HOST" {
		t.Fatalf("g2 %+v", s)
	}
	host.expect("game::opponent_disconnected")

	stored, _ := e.store.GetGame(context.Background(), g1.id)
	if stored.Status != "ACTIVE" {
		t.Fatalf("old game status %s", stored.Status)
	}
	// Switch back.
	s = guest.connect(g1)
	if s.Status != "ACTIVE" || s.You != "GUEST" {
		t.Fatalf("back to g1 %+v", s)
	}
	host.expect("game::opponent_connected")
	host.state()
}

func TestAuthTimeout(t *testing.T) {
	e := newEnv(t, envOpts{authTimeout: 100 * time.Millisecond})
	c := e.dial()
	c.expectClose(hub.CloseAuthTimeout)

	ok := e.dial()
	ok.auth(e.player("gina"))
	time.Sleep(200 * time.Millisecond)
	ok.quiet()
}

func TestPlayerExpiry(t *testing.T) {
	e := newEnv(t, envOpts{})
	host, guest, hp, gp, g := setup(t, e)

	e.clock.Advance(20 * time.Hour)
	guest.ok("game::move", map[string]string{"move": "ROCK"}) // keeps guest + game fresh
	guest.state()
	host.state()

	e.clock.Advance(5 * time.Hour) // host idle 25h, guest 5h
	if err := e.sweeper.SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	host.expectClose(hub.ClosePlayerExpired)
	var closed struct{ Reason, By string }
	guest.expect("game::closed").decode(t, &closed)
	if closed.Reason != "player_expired" || closed.By != "HOST" {
		t.Fatalf("closed %+v", closed)
	}
	if s := guest.state(); s.Status != "CLOSED" {
		t.Fatalf("final %+v", s)
	}
	guest.fail("game::move", map[string]string{"move": "ROCK"}, "not_in_game")

	ctx := context.Background()
	if _, err := e.store.GetPlayer(ctx, hp.id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("host not deleted: %v", err)
	}
	if _, err := e.store.GetPlayer(ctx, gp.id); err != nil {
		t.Fatalf("guest deleted: %v", err)
	}
	stored, err := e.store.GetGame(ctx, g.id)
	if err != nil || stored.Status != "CLOSED" || stored.HostPlayerID != "" ||
		!stored.LastActiveAt.Equal(t0.Add(20*time.Hour)) {
		t.Fatalf("game after expiry: %+v %v", stored, err)
	}

	// The expired player's credentials no longer work.
	c := e.dial()
	c.fail("player::authenticate", map[string]string{"player_id": hp.id, "player_secret": hp.secret}, "invalid_credentials")
	// Guest can still see the closed game is closed.
	guest.fail("game::connect", map[string]string{"game_id": g.id, "game_secret": g.secret}, "game_closed")
}

func TestGameExpiry(t *testing.T) {
	e := newEnv(t, envOpts{gameTTL: time.Hour})
	host, guest, hp, _, g := setup(t, e)

	e.clock.Advance(30 * time.Minute)
	if err := e.sweeper.SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	host.quiet()

	e.clock.Advance(time.Hour)
	if err := e.sweeper.SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	host.expectClose(hub.CloseGameExpired)
	guest.expectClose(hub.CloseGameExpired)
	if _, err := e.store.GetGame(context.Background(), g.id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("game not deleted: %v", err)
	}
	if _, err := e.store.GetPlayer(context.Background(), hp.id); err != nil {
		t.Fatalf("player should survive game expiry: %v", err)
	}
}
