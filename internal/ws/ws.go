// Package ws is the WebSocket transport: it upgrades /ws, runs the read,
// write and ping loops for each connection, decodes envelopes and hands
// actions to the hub.
package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/hamologist/rps-backend/internal/hub"
)

// ReadLimit is the maximum size of one client message.
const ReadLimit = 4096

const writeTimeout = 10 * time.Second

// Handler serves GET /ws.
type Handler struct {
	hub          *hub.Hub
	log          *slog.Logger
	accept       websocket.AcceptOptions
	authTimeout  time.Duration
	pingInterval time.Duration
	wg           sync.WaitGroup
}

// Options configures a Handler.
type Options struct {
	Hub            *hub.Hub
	Logger         *slog.Logger
	AllowedOrigins []string
	AuthTimeout    time.Duration
	PingInterval   time.Duration
}

// New creates a Handler.
func New(o Options) *Handler {
	return &Handler{
		hub:          o.Hub,
		log:          o.Logger,
		accept:       AcceptOptions(o.AllowedOrigins),
		authTimeout:  o.AuthTimeout,
		pingInterval: o.PingInterval,
	}
}

// AcceptOptions converts allowed origins to coder/websocket accept options.
// The library matches origin patterns against the origin's host, so each
// origin URL is reduced to host[:port]. "*" allows any origin; an empty list
// keeps the library's same-origin default.
func AcceptOptions(origins []string) websocket.AcceptOptions {
	var opts websocket.AcceptOptions
	for _, o := range origins {
		if o == "*" {
			opts.InsecureSkipVerify = true
			opts.OriginPatterns = nil
			return opts
		}
		if u, err := url.Parse(o); err == nil && u.Host != "" {
			o = u.Host
		}
		opts.OriginPatterns = append(opts.OriginPatterns, o)
	}
	return opts
}

// Wait blocks until every connection handler has returned or ctx is done.
func (h *Handler) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() { h.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

type envelope struct {
	Action    string          `json:"action"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	opts := h.accept
	conn, err := websocket.Accept(w, r, &opts)
	if err != nil {
		h.log.Debug("websocket accept failed", "err", err, "origin", r.Header.Get("Origin"))
		return
	}
	h.wg.Add(1)
	defer h.wg.Done()
	conn.SetReadLimit(ReadLimit)

	c := h.hub.NewConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var loops sync.WaitGroup
	loops.Go(func() { h.writeLoop(ctx, conn, c) })
	loops.Go(func() { h.pingLoop(ctx, conn, c) })

	authTimer := time.AfterFunc(h.authTimeout, func() {
		if !h.hub.IsAuthenticated(c) {
			c.Close(hub.CloseAuthTimeout, "authentication timeout")
		}
	})
	defer authTimer.Stop()

	h.readLoop(ctx, conn, c)
	h.hub.Disconnect(c) // also marks c closing, which stops the writer and pinger
	// Let the writer finish its close handshake, but don't hang on it.
	loopsDone := make(chan struct{})
	go func() { loops.Wait(); close(loopsDone) }()
	select {
	case <-loopsDone:
	case <-time.After(writeTimeout):
		cancel()
		<-loopsDone
	}
}

func (h *Handler) readLoop(ctx context.Context, conn *websocket.Conn, c *hub.Conn) {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			c.SendError("", "bad_request", "only text frames are accepted")
			continue
		}
		var env envelope
		if err := json.Unmarshal(data, &env); err != nil || env.Action == "" {
			c.SendError(env.RequestID, "bad_request", "malformed envelope")
			continue
		}
		h.hub.Handle(ctx, c, env.Action, env.RequestID, env.Payload)
	}
}

func (h *Handler) writeLoop(ctx context.Context, conn *websocket.Conn, c *hub.Conn) {
	write := func(msg []byte) bool {
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, msg) == nil
	}
	for {
		select {
		case msg := <-c.Outbound():
			if !write(msg) {
				conn.CloseNow()
				return
			}
		case <-c.Closing():
			code, reason := c.CloseStatus()
			if code != hub.CloseSlowConsumer {
				// Flush anything queued before the close was requested.
			drain:
				for {
					select {
					case msg := <-c.Outbound():
						if !write(msg) {
							break drain
						}
					default:
						break drain
					}
				}
			}
			_ = conn.Close(websocket.StatusCode(code), reason)
			return
		case <-ctx.Done():
			conn.CloseNow()
			return
		}
	}
}

func (h *Handler) pingLoop(ctx context.Context, conn *websocket.Conn, c *hub.Conn) {
	t := time.NewTicker(h.pingInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Closing():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, h.pingInterval)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				select {
				case <-c.Closing():
				default:
					h.log.Debug("ping failed; dropping connection", "err", err)
					conn.CloseNow()
				}
				return
			}
		}
	}
}
