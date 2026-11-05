// 本文件把「信令」与「WebRTC 会话」拆开。
//
// 默认情况下 Client 自己创建 PeerConnection 并自动应答 OFFER（见 client.go），
// 那是给「只要一条 DataChannel 互传字符串」的场景用的。当调用方想自己控制
// PeerConnection / DataChannel（例如 webrtc 模块要做多路复用、传文件、传 metadata），
// 就用 RegisterTransport 接管：Client 只负责 wss 收发，SDP 全部转交给 Transport。
package peerjs

import (
	"errors"
	"sync"

	"github.com/pion/webrtc/v4"
)

// ErrNoSignal 表示信令连接尚未就绪或已断开。
var ErrNoSignal = errors.New("peerjs: 信令不可用")

// Offer 是浏览器发来的一个 WebRTC offer，原样交给 Transport。
type Offer struct {
	Remote        string // 对端 peer id（信令里的 src）
	ConnectionID  string // PeerJS 的 connectionId，后续 CANDIDATE 靠它路由
	Label         string // DataChannel label，浏览器侧一般与 connectionId 相同
	Serialization string // 浏览器 connect() 时指定的序列化方式
	SDP           string // offer 的 sdp 原文
}

// Transport 接管一条连接的 SDP 交换。
//
// 实现方负责：建 PeerConnection、SetRemoteDescription、CreateAnswer、
// 发送 answer（用 Signal.SendAnswer / Signal.SendCandidate）。
// Client 不会为被接管的连接创建 PeerConnection。
type Transport interface {
	// Accept 在收到 OFFER 时被调用。可以阻塞，Client 在独立 goroutine 里调用它。
	Accept(o Offer, sig Signal)
	// Offer 在收到 CANDIDATE 时被调用，交给对应连接。
	Offer(connectionID string, cand webrtc.ICECandidateInit)
}

// Signal 是 Transport 回写信令的出口。
type Signal interface {
	// SendAnswer 回一个 answer 给对端。
	SendAnswer(remote, connectionID, sdp string) error
	// SendCandidate 回一个 ICE candidate 给对端。
	SendCandidate(remote, connectionID string, cand webrtc.ICECandidateInit) error
	// Release 解除这条连接在信令侧的路由记账（连接已结束）。
	Release(connectionID string)
}

// RegisterTransport 让 t 接管所有入站 OFFER。传 nil 恢复默认行为（自动应答）。
func (c *Client) RegisterTransport(t Transport) {
	c.mu.Lock()
	c.transport = t
	c.mu.Unlock()
}

// NewICEServers 返回默认 ICE 配置（STUN + PeerJS 公共 TURN）。
// 供需要自建 PeerConnection 的调用方复用，避免重复写一份。
func NewICEServers() []webrtc.ICEServer {
	return []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"turn:0.peerjs.com:3478"}, Username: "peerjs", Credential: "peerjsp"},
	}
}

// signalOut 是 Signal 接口在 Client 上的实现。
type signalOut struct {
	c    *Client
	once sync.Once
}

func (c *Client) newSignal() *signalOut {
	return &signalOut{c: c}
}

func (s *signalOut) SendAnswer(remote, connectionID, sdp string) error {
	return s.c.write(map[string]any{
		"type": "ANSWER",
		"payload": map[string]any{
			"sdp":          map[string]any{"type": "answer", "sdp": sdp},
			"type":         "data",
			"connectionId": connectionID,
		},
		"dst": remote,
	})
}

func (s *signalOut) SendCandidate(remote, connectionID string, cand webrtc.ICECandidateInit) error {
	body := map[string]any{"candidate": cand.Candidate}
	if cand.SDPMid != nil {
		body["sdpMid"] = *cand.SDPMid
	}
	if cand.SDPMLineIndex != nil {
		body["sdpMLineIndex"] = *cand.SDPMLineIndex
	}
	if cand.UsernameFragment != nil {
		body["usernameFragment"] = *cand.UsernameFragment
	}
	return s.c.write(map[string]any{
		"type": "CANDIDATE",
		"payload": map[string]any{
			"type":         "data",
			"connectionId": connectionID,
			"candidate":    body,
		},
		"dst": remote,
	})
}

func (s *signalOut) Release(connectionID string) {
	s.once.Do(func() {
		s.c.mu.Lock()
		delete(s.c.trans, connectionID)
		delete(s.c.peers, connectionID)
		s.c.mu.Unlock()
	})
	s.c.dropSession(connectionID)
}

// Remote 返回该 connectionId 对应的对端 peer id。
func (c *Client) Remote(connectionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.peers[connectionID]; ok {
		return id
	}
	if s := c.sessions[connectionID]; s != nil {
		return s.remote
	}
	return ""
}
