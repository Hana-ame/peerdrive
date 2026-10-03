// Package peerjs provides a PeerJS-compatible signaling client and WebRTC DataChannel transport layer.
// Module positioning: transport primitives (signaling + data plane); the business frame protocol (verb) is defined by the upper layer —
// consistent with hana-link's "format-agnostic" principle, ensuring future extensibility.
package peerjs

import (
	"encoding/json"
	"time"

	"github.com/pion/webrtc/v4"
)

// MessageType is the signaling message type.
// Extensibility: string type; custom message types can be used directly as literals without modifying the library.
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

// ConnectionType is the connection type (consistent with peerjs ConnectionType).
const (
	ConnData  = "data"
	ConnMedia = "media"
)

// Message is the generic message transported between the signaling server and a peer.
// payload is arbitrary JSON; its specific structure is determined by the message type (see OfferPayload, etc.).
type Message struct {
	Type    MessageType     `json:"type"`
	Src     string          `json:"src,omitempty"`
	Dst     string          `json:"dst,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewMessage constructs a message directed at dst.
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

// OfferPayload is the payload for an OFFER message (constructed by peerjs negotiator._makeOffer).
type OfferPayload struct {
	SDP           *webrtc.SessionDescription `json:"sdp"`
	Type          string                     `json:"type"` // "data" | "media"
	ConnectionID  string                     `json:"connectionId"`
	Label         string                     `json:"label"`
	Reliable      bool                       `json:"reliable"`
	Serialization string                     `json:"serialization"`
	Metadata      json.RawMessage            `json:"metadata"`
}

// AnswerPayload is the payload for an ANSWER message.
type AnswerPayload struct {
	SDP          *webrtc.SessionDescription `json:"sdp"`
	Type         string                     `json:"type"`
	ConnectionID string                     `json:"connectionId"`
}

// CandidatePayload is the payload for a CANDIDATE (ICE candidate) message.
type CandidatePayload struct {
	Candidate    webrtc.ICECandidateInit `json:"candidate"`
	Type         string                  `json:"type"`
	ConnectionID string                  `json:"connectionId"`
}

// Options is the signaling client configuration. New configuration items should maintain backward compatibility (default values do not change existing behavior).
type Options struct {
	Host         string // signaling server address (default 0.peerjs.com)
	Port         string
	Secure       bool               // wss/https
	Path         string             // path prefix for self-hosted server (default "/")
	Key          string             // API key (default "peerjs")
	ID           string             // node ID; empty means the server assigns a random ID
	Token        string             // empty means randomly generated
	PingInterval time.Duration      // signaling heartbeat interval (default 5s)
	ICEServers   []webrtc.ICEServer // WebRTC ICE/TURN server list
}
