// Package signalframe provides the signaling message frame used between a
// PeerJS-style signaling client and server: message construction
// (Message/NewMessage), serialization (Encode), and a concurrency-safe,
// deadline-bounded sender (Sender) that wraps a JSON-writing socket.
//
// Module positioning: the two endpoints of the signaling plane — the client
// (back/peerjs, module github.com/Hana-ame/go-peerjs) and the relay server
// (back/signalserver, module github.com/Hana-ame/go-peerserver) — used to keep
// their own copies of the message struct and of the "lock + write deadline +
// WriteJSON" send wrapper (byte-identical apart from the deadline). Both
// endpoints now build on this single frame so the wire format cannot drift
// between them. It intentionally has zero third-party dependencies: the
// JSONWriter interface is satisfied by *websocket.Conn from gorilla/websocket.
package signalframe

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// MessageType is the signaling message type (string; custom types can be used
// directly as literals without modifying the library).
type MessageType string

// Standard types (consistent with peerjs-server's MessageType enum).
const (
	MsgOpen      MessageType = "OPEN"
	MsgLeave     MessageType = "LEAVE"
	MsgCandidate MessageType = "CANDIDATE"
	MsgOffer     MessageType = "OFFER"
	MsgAnswer    MessageType = "ANSWER"
	MsgExpire    MessageType = "EXPIRE"
	MsgHeartbeat MessageType = "HEARTBEAT"
	MsgIDTaken   MessageType = "ID-TAKEN"
	MsgError     MessageType = "ERROR"
)

// Message is the generic message transported between the signaling server and
// a peer. Payload is arbitrary JSON; its specific structure is determined by
// the message type (see peerjs.OfferPayload etc.).
type Message struct {
	Type    MessageType     `json:"type"`
	Src     string          `json:"src,omitempty"`
	Dst     string          `json:"dst,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewMessage constructs a message directed at dst. A non-nil payload is
// marshaled; an unmarshalable payload leaves Payload empty rather than
// failing the call (historical behavior preserved).
func NewMessage(t MessageType, dst string, payload any) Message {
	m := Message{Type: t, Dst: dst}
	if payload != nil {
		b, err := json.Marshal(payload)
		if err == nil {
			m.Payload = b
		}
	}
	return m
}

// Encode serializes the message to its wire form.
func (m Message) Encode() ([]byte, error) {
	return json.Marshal(m)
}

// JSONWriter is the minimal JSON-writing socket interface Sender writes to.
// *websocket.Conn from gorilla/websocket satisfies it.
type JSONWriter interface {
	SetWriteDeadline(t time.Time) error
	WriteJSON(v any) error
	Close() error
}

// ErrNotConnected is returned by Send when no writer is attached (e.g. before
// Dial succeeded or after Close).
var ErrNotConnected = errors.New("signalframe: not connected")

// Sender serializes writes to a signaling socket.
//
// gorilla/websocket does not allow concurrent writes — a panic otherwise
// occurs when heartbeat / ICE candidates / ANSWER send from different
// goroutines. Sender therefore guards every write with a mutex and also uses
// it to protect writer attach/detach, so a Send racing with Close/SetWriter is
// either an error or a no-op, never a concurrent socket access.
type Sender struct {
	mu      sync.Mutex
	w       JSONWriter
	timeout time.Duration
}

// NewSender creates a Sender writing to w with the given write deadline.
// timeout <= 0 disables the deadline (kept for callers that never set one).
func NewSender(w JSONWriter, timeout time.Duration) *Sender {
	return &Sender{w: w, timeout: timeout}
}

// SetWriter attaches a writer (after Dial) or detaches it (nil: disconnect /
// close). Detaching makes subsequent Sends return ErrNotConnected.
func (s *Sender) SetWriter(w JSONWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.w = w
}

// Writer returns the currently attached writer (nil when detached). Used by
// Close() on the owning socket for read-side teardown.
func (s *Sender) Writer() JSONWriter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w
}

// Send writes m to the attached writer. When no writer is attached it returns
// ErrNotConnected. A write deadline is applied before WriteJSON; the
// deadline's own error is deliberately ignored (historical behavior) — only
// the WriteJSON error is returned.
func (s *Sender) Send(m Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.w == nil {
		return ErrNotConnected
	}
	if s.timeout > 0 {
		_ = s.w.SetWriteDeadline(time.Now().Add(s.timeout))
	}
	return s.w.WriteJSON(m)
}

// Close detaches the writer and closes the underlying socket (idempotent).
func (s *Sender) Close() error {
	s.mu.Lock()
	w := s.w
	s.w = nil
	s.mu.Unlock()
	if w == nil {
		return nil
	}
	return w.Close()
}
