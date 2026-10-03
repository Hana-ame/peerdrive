package transport

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// nowPlus returns a write-deadline time point.
func nowPlus(sec int) time.Time { return time.Now().Add(time.Duration(sec) * time.Second) }

// Session is a session abstraction: a channel carrying the frame protocol.
// The two implementations have identical semantics (same reqId state machine / frame-protocol reuse):
//   - *peerjs.Connection: WebRTC DataChannel (remote node / browser connecting directly via public cloud signaling)
//   - *WSSession: local WebSocket (browser connecting directly to this node, no NAT traversal/signaling overhead)
//
// See doc/REFACTOR.md Section 4 for the frame protocol: text frames = control headers (JSON), binary frames = data chunks;
// SendFrame guarantees atomic consecutive header + body.
type Session interface {
	ID() string
	SendJSON(v any) error
	SendFrame(header any, body []byte) error
	OnMessage(f func(peerjs.Frame))
	OnClose(f func())
	Close()
}

// WSSession adapts a local WebSocket to the Session interface (frame protocol identical to DataChannel).
// Semantic reuse: the browser uses the same req/meta/data/done/err frames locally over WS and remotely over
// WebRTC DataChannel — the frontend only needs one protocol codec.
type WSSession struct {
	id   string
	conn *websocket.Conn

	sendMu    sync.Mutex // gorilla does not allow concurrent writes
	onMessage func(peerjs.Frame)
	onClose   func()
	closeOnce sync.Once
}

// NewWSSession wraps an already-upgraded WebSocket connection and starts the read loop and keep-alive.
// M5 fixes:
//   - SetReadLimit: previously no read limit; a malicious/faulty browser sending oversized frames would consume unbounded memory
//   - ping/pong keep-alive: previously no ReadDeadline; a dead browser tab would leave the readLoop
//     goroutine and session alive forever, the connection map would never clean up, and pending fetches would hang for 5 minutes
func NewWSSession(id string, conn *websocket.Conn) *WSSession {
	// Data chunks ≤64KB + JSON control header margin (2× headroom)
	conn.SetReadLimit(3 * 64 * 1024)
	conn.SetReadDeadline(nowPlus(90))
	conn.SetPongHandler(func(string) error {
		// Refresh the read deadline on pong (browsers auto-reply pong to ping; protocol-level behavior)
		conn.SetReadDeadline(nowPlus(90))
		return nil
	})
	s := &WSSession{id: id, conn: conn}
	go s.readLoop()
	go s.heartbeatLoop()
	return s
}

// heartbeatLoop periodically pings to keep the connection alive: a dead connection with no pong within 90s → read timeout →
// ReadMessage errors → readLoop exits → Close cleans up the session. If ping fails (connection already closed),
// exit immediately with no leak.
func (s *WSSession) heartbeatLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		s.sendMu.Lock()
		err := s.conn.WriteControl(websocket.PingMessage, nil, nowPlus(10))
		s.sendMu.Unlock()
		if err != nil {
			return
		}
	}
}

// ID returns the session identifier (local sessions are "local").
func (s *WSSession) ID() string { return s.id }

// IsLocal treats a local WS session (/ws/peer) as "self" (used by isSelfSession in share.go).
//
// It carries this node's management channel (admin panel/dashboard connecting directly to this node), not a P2P connection to a remote node,
// so private shared content is always allowed through it — the operator must always be able to retrieve their own stuff
// without first adding themselves to the friends list.
func (s *WSSession) IsLocal() bool { return true }

// SendJSON sends a text frame (JSON control header).
func (s *WSSession) SendJSON(v any) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.conn.SetWriteDeadline(nowPlus(15))
	return s.conn.WriteJSON(v)
}

// SendFrame atomically sends a "JSON header + binary body" frame (same constraint as DataChannel).
func (s *WSSession) SendFrame(header any, body []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.conn.SetWriteDeadline(nowPlus(15))
	if err := s.conn.WriteJSON(header); err != nil {
		return err
	}
	if len(body) > 0 {
		s.conn.SetWriteDeadline(nowPlus(15))
		return s.conn.WriteMessage(websocket.BinaryMessage, body)
	}
	return nil
}

// OnMessage registers a frame callback (text/binary frame distinction consistent with DataChannel).
func (s *WSSession) OnMessage(f func(peerjs.Frame)) {
	s.sendMu.Lock()
	s.onMessage = f
	s.sendMu.Unlock()
}

// OnClose registers a close callback.
func (s *WSSession) OnClose(f func()) {
	s.sendMu.Lock()
	s.onClose = f
	s.sendMu.Unlock()
}

// Close closes the session.
func (s *WSSession) Close() {
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.sendMu.Lock()
		f := s.onClose
		s.onClose = nil
		s.sendMu.Unlock()
		if f != nil {
			f()
		}
	})
}

// readLoop reads frames and dispatches them (text → IsText=true, binary → IsText=false).
func (s *WSSession) readLoop() {
	defer s.Close()
	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		s.sendMu.Lock()
		f := s.onMessage
		s.sendMu.Unlock()
		if f != nil {
			f(peerjs.Frame{IsText: mt == websocket.TextMessage, Data: data})
		}
	}
}
