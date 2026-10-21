package wsconn

import "time"

// Conn is the socket surface Session drives. *websocket.Conn from
// github.com/gorilla/websocket satisfies it, which is why this package keeps
// gorilla out of the read/write scheduling path: tests drive a real Session
// against a scripted fakeConn instead of a real socket, so the write mutex,
// the deadline ladder and the close ordering can be asserted directly
// (see session_test.go).
//
// The set is deliberately the smallest that makes the session's behavior
// reproducible. Nothing is added here for convenience.
type Conn interface {
	// SetReadLimit bounds a single inbound message. Takes no error because
	// the limit is advisory (it is enforced lazily as frames are read).
	SetReadLimit(n int64)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	// SetPongHandler installs the handler that refreshes the read deadline
	// when a pong arrives — the browser replies to a ping with a pong, so
	// the deadline is renewed by protocol rather than by a timer.
	SetPongHandler(h func(string) error)
	// WriteControl writes a control frame (ping/pong/close). Separate from
	// WriteMessage because gorilla requires it for frames that must be
	// interleaved with data frames.
	WriteControl(messageType int, data []byte, deadline time.Time) error
	WriteJSON(v any) error
	WriteMessage(messageType int, data []byte) error
	ReadMessage() (messageType int, data []byte, err error)
	Close() error
}
