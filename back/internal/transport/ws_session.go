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
// Permission Isolation:
//   - Standard WSSession (/ws/peer, /ws/admin): dataOnly=false, IsLocal()=true (management & data plane).
//   - Data-only WSSession (/ws/data): dataOnly=true, IsLocal()=false (strictly data plane only;
//     admin frames and privileged index mutations are rejected by serveAdmin / inbound gates).
type WSSession struct {
	*wsconn.Session
	dataOnly bool
}

// NewWSSession wraps an already-upgraded WebSocket connection and starts the
// read loop and the keep-alive. id is the session identifier — "local" for
// the /ws/peer endpoint, so several browser tabs can coexist.
func NewWSSession(id string, conn *websocket.Conn) *WSSession {
	return &WSSession{Session: wsconn.New(id, conn, wsconn.Options{}), dataOnly: false}
}

// NewWSDataSession wraps an upgraded WebSocket connection for the /ws/data endpoint.
// It is restricted to data-plane operations (IsLocal() == false), preventing management-plane
// privilege escalation over data sockets.
func NewWSDataSession(id string, conn *websocket.Conn) *WSSession {
	return &WSSession{Session: wsconn.New(id, conn, wsconn.Options{}), dataOnly: true}
}

// OnMessage bridges wsconn.Frame → peerjs.Frame.
func (s *WSSession) OnMessage(f func(peerjs.Frame)) {
	s.Session.OnMessage(func(fr wsconn.Frame) {
		f(peerjs.Frame{IsText: fr.IsText, Data: fr.Data})
	})
}

// IsLocal treats this session as the operator's own management connection to this node
// UNLESS the session was opened on the restricted /ws/data endpoint (dataOnly == true).
func (s *WSSession) IsLocal() bool { return !s.dataOnly }
