package transport

import (
	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/gorilla/websocket"

	"peerdrive/internal/wsconn"
)

// Session is the transport abstraction shared by the local WebSocket session
// (WSSession) and the WebRTC DataChannel session (rtcSession). The two have
// identical frame semantics, so the file-transfer, file-index and admin verbs
// are written once against this interface.
type Session interface {
	ID() string
	SendJSON(v any) error
	SendFrame(header any, body []byte) error
	OnMessage(f func(peerjs.Frame))
	OnClose(f func())
	Close()
}

// WSSession adapts a local WebSocket connection to the Session interface
// (frame protocol identical to DataChannel).
//
// Semantic reuse: the browser uses the same req/meta/data/done/err frames
// locally over WS and remotely over WebRTC DataChannel — the frontend only
// needs one protocol codec.
//
// feat/ws-split: the WebSocket machinery moved to internal/wsconn — frame
// codec, read/write scheduling, heartbeat, write mutex, the read/write
// deadlines and the close ordering. What is left here is only boundary work:
// bridging wsconn.Frame to peerjs.Frame so this package can keep talking to
// the DataChannel session type, and carrying the IsLocal marker that
// isSelfSession (share.go) uses to treat the operator's own connection as
// "self".
type WSSession struct {
	*wsconn.Session
}

// NewWSSession wraps an already-upgraded WebSocket connection and starts the
// read loop and the keep-alive. id is the session identifier — "local" for
// the /ws/peer endpoint, so several browser tabs can coexist.
//
// wsconn.Options{} keeps the historical constants (3x64KB read limit,
// 90s read deadline, 15s write deadline, 30s ping interval, 10s ping timeout).
func NewWSSession(id string, conn *websocket.Conn) *WSSession {
	return &WSSession{Session: wsconn.New(id, conn, wsconn.Options{})}
}

// OnMessage bridges wsconn.Frame → peerjs.Frame. The two structs are
// field-for-field identical ({IsText, Data}), so this is a plain copy — no
// lossy conversion and no allocation beyond the value.
func (s *WSSession) OnMessage(f func(peerjs.Frame)) {
	s.Session.OnMessage(func(fr wsconn.Frame) {
		f(peerjs.Frame{IsText: fr.IsText, Data: fr.Data})
	})
}

// IsLocal treats this session as the operator's own connection to this node,
// so private shared content is served through it (see isSelfSession).
// Only WSSession implements it; the type assertion in isSelfSession therefore
// fails closed for WebRTC sessions.
func (s *WSSession) IsLocal() bool { return true }
