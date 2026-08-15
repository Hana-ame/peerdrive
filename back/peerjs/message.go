// Package peerjs 提供 PeerJS 兼容的信令客户端与 WebRTC DataChannel 传输层。
// 模块定位：传输原语（信令 + 数据面），业务帧协议（verb）由上层定义——
// 与 hana-link 的「格式无关」原则一致，保证后续扩展性。
package peerjs

import (
	"encoding/json"
	"time"

	"github.com/pion/webrtc/v4"
)

// MessageType 信令消息类型。
// 扩展性：string 类型，自定义消息类型可直接用字面量，无需改库。
type MessageType string

// 标准类型（与 peerjs-server MessageType 枚举一致）。
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

// ConnectionType 连接类型（与 peerjs ConnectionType 一致）。
const (
	ConnData  = "data"
	ConnMedia = "media"
)

// Message 信令服务器与 peer 之间传输的通用消息。
// payload 是任意 JSON，具体结构由消息类型决定（见 OfferPayload 等）。
type Message struct {
	Type    MessageType     `json:"type"`
	Src     string          `json:"src,omitempty"`
	Dst     string          `json:"dst,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewMessage 构造一条指向 dst 的消息。
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

// OfferPayload OFFER 消息的负载（peerjs negotiator._makeOffer 构造）。
type OfferPayload struct {
	SDP           *webrtc.SessionDescription `json:"sdp"`
	Type          string                     `json:"type"` // "data" | "media"
	ConnectionID  string                     `json:"connectionId"`
	Label         string                     `json:"label"`
	Reliable      bool                       `json:"reliable"`
	Serialization string                     `json:"serialization"`
	Metadata      json.RawMessage            `json:"metadata"`
}

// AnswerPayload ANSWER 消息的负载。
type AnswerPayload struct {
	SDP          *webrtc.SessionDescription `json:"sdp"`
	Type         string                     `json:"type"`
	ConnectionID string                     `json:"connectionId"`
}

// CandidatePayload CANDIDATE（ICE 候选）消息的负载。
type CandidatePayload struct {
	Candidate    webrtc.ICECandidateInit `json:"candidate"`
	Type         string                  `json:"type"`
	ConnectionID string                  `json:"connectionId"`
}

// Options 信令客户端配置。新增配置项时应保持向后兼容（默认值不改变既有行为）。
type Options struct {
	Host         string // 信令服务器地址（默认 0.peerjs.com）
	Port         string
	Secure       bool               // wss/https
	Path         string             // 自托管 server 的路径前缀（默认 "/"）
	Key          string             // API key（默认 "peerjs"）
	ID           string             // 节点 ID；空则服务端分配随机 ID
	Token        string             // 空则随机生成
	PingInterval time.Duration      // 信令心跳间隔（默认 5s）
	ICEServers   []webrtc.ICEServer // WebRTC ICE/TURN 服务器列表
}
