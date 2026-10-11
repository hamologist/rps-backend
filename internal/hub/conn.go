package hub

import (
	"encoding/json"
	"sync"

	"github.com/hamologist/rps-backend/internal/rules"
)

// WebSocket close codes used by the server.
const (
	CloseNormal        = 1000
	CloseReplaced      = 4000
	CloseAuthTimeout   = 4001
	ClosePlayerExpired = 4002
	CloseGameExpired   = 4003
	CloseSlowConsumer  = 4004
)

// SendBuffer is the per-connection outbound buffer size.
const SendBuffer = 32

// Conn is the hub's view of one live client connection. The transport layer
// drains Outbound and closes the socket once Closing fires.
type Conn struct {
	out         chan []byte
	closing     chan struct{}
	closeOnce   sync.Once
	closeCode   int
	closeReason string

	// Guarded by Hub.mu.
	playerID string
	gameID   string
	seat     rules.Seat
}

func newConn() *Conn {
	return &Conn{out: make(chan []byte, SendBuffer), closing: make(chan struct{})}
}

// Outbound yields serialized server messages to write to the socket.
func (c *Conn) Outbound() <-chan []byte { return c.out }

// Closing is closed once the connection should be shut down.
func (c *Conn) Closing() <-chan struct{} { return c.closing }

// CloseStatus returns the close code and reason. Valid once Closing has fired.
func (c *Conn) CloseStatus() (int, string) { return c.closeCode, c.closeReason }

// Close requests the connection be closed with code. The first call wins.
func (c *Conn) Close(code int, reason string) {
	c.closeOnce.Do(func() {
		c.closeCode, c.closeReason = code, reason
		close(c.closing)
	})
}

func (c *Conn) isClosing() bool {
	select {
	case <-c.closing:
		return true
	default:
		return false
	}
}

type message struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id,omitempty"`
	Payload   any    `json:"payload"`
}

// empty marshals as {}.
type empty struct{}

type errorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// send enqueues a message without blocking. If the buffer is full the
// connection is closed as a slow consumer.
func (c *Conn) send(typ, requestID string, payload any) {
	if c.isClosing() {
		return
	}
	b, err := json.Marshal(message{Type: typ, RequestID: requestID, Payload: payload})
	if err != nil {
		panic(err) // payloads are server-defined types; this is a programming error
	}
	select {
	case c.out <- b:
	default:
		c.Close(CloseSlowConsumer, "slow consumer")
	}
}

// SendError enqueues an error reply.
func (c *Conn) SendError(requestID, code, msg string) {
	c.send("error", requestID, errorPayload{Code: code, Message: msg})
}
