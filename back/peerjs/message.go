// Package peerjs provides a PeerJS-compatible signaling client and WebRTC DataChannel transport layer.
// Module positioning: transport primitives (signaling + data plane); the business frame protocol (verb) is defined by the upper layer —
// consistent with hana-link's "format-agnostic" principle, ensuring future extensibility.
package peerjs

import (
	"encoding/json"
	"time"

	"github.com/Hana-ame/go-signalframe"
	"github.com/pion/webrtc/v4"
)

// MessageType is the signaling message type.
// Alias of signalframe.MessageType: the frame construction/serialization/send
// logic lives in the shared signalframe module (see signalframe package docs).
type MessageType = signalframe.MessageType

// Standard types (consistent with peerjs-server's MessageType enum).
const (
	MsgOpen      MessageType = signalframe.MsgOpen
	MsgLeave     MessageType = signalframe.MsgLeave
	MsgCandidate MessageType = signalframe.MsgCandidate
	MsgOffer     MessageType = signalframe.MsgOffer
	MsgAnswer    MessageType = signalframe.MsgAnswer
	MsgExpire    MessageType = signalframe.MsgExpire
	MsgHeartbeat MessageType = signalframe.MsgHeartbeat
	MsgIDTaken   MessageType = signalframe.MsgIDTaken
	MsgError     MessageType = signalframe.MsgError
)

// ConnectionType is the connection type (consistent with peerjs ConnectionType).
const (
	ConnData  = "data"
	ConnMedia = "media"
)

// Message is the generic message transported between the signaling server and a peer.
// Alias of signalframe.Message (the single wire-format definition).
type Message = signalframe.Message

// NewMessage constructs a message directed at dst.
func NewMessage(t MessageType, dst string, payload any) Message {
	return signalframe.NewMessage(t, dst, payload)
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
