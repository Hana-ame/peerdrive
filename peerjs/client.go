// Package peerjs 实现一个纯出站的 PeerJS 客户端。
//
// 它不监听任何端口，也不提供 HTTP/WS 服务：启动时向 PeerServer 要一个候选 id，
// 用 wss 注册后把 id 交给调用方（通常打日志），之后只做两件事——
// 应答浏览器发来的 WebRTC offer，以及在 DataChannel 上收发字符串。
//
// 浏览器侧不需要任何私有协议，标准 PeerJS 用法即可：
//
//	const conn = peer.connect("<log 里的 id>", { serialization: "json" });
//	conn.on("open", () => conn.send("hello"));
//	conn.on("data", (text) => console.log(text));
package peerjs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

const (
	DefaultHost = "0.peerjs.com"
	DefaultPort = 443
	DefaultPath = "/"
	DefaultKey  = "peerjs"

	// SerializationJSON 是目前唯一支持的序列化方式，必须与浏览器侧
	// peer.connect(id, { serialization: "json" }) 保持一致。
	SerializationJSON = "json"

	heartbeatInterval = 5 * time.Second
	registerTimeout   = 30 * time.Second
)

// ErrNoPeer 表示该对端当前没有打开的 DataChannel。
var ErrNoPeer = errors.New("peerjs: 对端没有打开的通道")

// Config 是客户端配置。零值即可连公共服务器。
type Config struct {
	Host string // 默认 0.peerjs.com
	Port int    // 默认 443
	Path string // 默认 /
	Key  string // 默认 peerjs

	// ID 为空时向 PeerServer 要一个候选 id；填了就用这个固定 id 注册。
	ID string

	// ICEServers 默认是 PeerJS 云端的 STUN + TURN。
	ICEServers []webrtc.ICEServer
	Logger     *log.Logger
	HTTPClient *http.Client
}

// Client 是一个已注册的 PeerJS peer。
type Client struct {
	cfg Config
	log *log.Logger
	id  string

	conn *websocket.Conn
	wmu  sync.Mutex

	mu       sync.Mutex
	sessions map[string]*session // connectionId → session
	byPeer   map[string]*session // 对端 id → session

	ready  chan struct{}
	readyOnce sync.Once
	closed chan struct{}
	closeOnce sync.Once

	// OnPeer 在某个对端的 DataChannel 打开/关闭时触发。
	OnPeer func(remote string, connected bool)
	// OnMessage 收到字符串时触发。在回调里调用 Send 即可即时回复。
	OnMessage func(remote string, text string)
}

type session struct {
	connID string
	remote string
	pc     *webrtc.PeerConnection
	dc     *webrtc.DataChannel
}

type message struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Src     string          `json:"src,omitempty"`
	Dst     string          `json:"dst,omitempty"`
}

type sdpBody struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type offerPayload struct {
	Type          string  `json:"type"`
	ConnectionID  string  `json:"connectionId"`
	Label         string  `json:"label"`
	Serialization string  `json:"serialization"`
	SDP           sdpBody `json:"sdp"`
}

type candidateBody struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex"`
	UsernameFragment *string `json:"usernameFragment"`
}

type candidatePayload struct {
	ConnectionID string        `json:"connectionId"`
	Candidate    candidateBody `json:"candidate"`
}

// Dial 取一个 id、连上信令并等注册完成。
func Dial(ctx context.Context, cfg Config) (*Client, error) {
	cfg = withDefaults(cfg)

	id := cfg.ID
	if id == "" {
		got, err := fetchCandidateID(ctx, cfg)
		if err != nil {
			return nil, err
		}
		id = got
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL(cfg, id, randomUUID()), nil)
	if err != nil {
		return nil, fmt.Errorf("peerjs: 连接信令: %w", err)
	}

	c := &Client{
		cfg:      cfg,
		log:      cfg.Logger,
		id:       id,
		conn:     conn,
		sessions: make(map[string]*session),
		byPeer:   make(map[string]*session),
		ready:    make(chan struct{}),
		closed:   make(chan struct{}),
	}
	go c.readLoop()
	go c.heartbeat()

	select {
	case <-c.ready:
		return c, nil
	case <-ctx.Done():
		conn.Close()
		return nil, ctx.Err()
	case <-time.After(registerTimeout):
		conn.Close()
		return nil, errors.New("peerjs: 注册超时")
	}
}

// ID 返回本端标识符（就是打在日志里的那条串）。
func (c *Client) ID() string { return c.id }

// Send 向指定对端发送一个字符串。
func (c *Client) Send(remote, text string) error {
	c.mu.Lock()
	s := c.byPeer[remote]
	c.mu.Unlock()
	if s == nil || s.dc == nil {
		return ErrNoPeer
	}
	return s.dc.Send([]byte(encodeText(text)))
}

// Peers 返回当前已打开通道的对端 id。
func (c *Client) Peers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.byPeer))
	for id := range c.byPeer {
		out = append(out, id)
	}
	return out
}

// Close 关闭信令连接与所有 PeerConnection。
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		c.mu.Lock()
		for _, s := range c.sessions {
			_ = s.pc.Close()
		}
		c.sessions = map[string]*session{}
		c.byPeer = map[string]*session{}
		c.mu.Unlock()
		err = c.conn.Close()
	})
	return err
}

func (c *Client) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.log.Printf("信令断开: %v", err)
			return
		}
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			c.log.Printf("非 JSON 信令: %.200s", string(data))
			continue
		}
		switch m.Type {
		case "OPEN":
			c.readyOnce.Do(func() { close(c.ready) })
		case "OFFER":
			go c.handleOffer(m)
		case "CANDIDATE":
			c.handleCandidate(m)
		case "HEARTBEAT":
		case "ID-TAKEN", "ERROR":
			c.log.Printf("服务器拒绝 %s: %.300s", m.Type, string(m.Payload))
		}
	}
}

func (c *Client) heartbeat() {
	t := time.NewTicker(heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.write(map[string]any{"type": "HEARTBEAT"})
		case <-c.closed:
			return
		}
	}
}

func (c *Client) write(v any) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.conn.WriteJSON(v); err != nil {
		c.log.Printf("信令写失败: %v", err)
	}
}

func (c *Client) handleOffer(m message) {
	var p offerPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		c.log.Printf("解析 OFFER 失败: %v", err)
		return
	}
	if p.Serialization != "" && p.Serialization != SerializationJSON {
		c.log.Printf("对端 serialization=%s，本实现只支持 json，消息可能解析失败", p.Serialization)
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: c.cfg.ICEServers})
	if err != nil {
		c.log.Printf("创建 PeerConnection 失败: %v", err)
		return
	}

	s := &session{connID: p.ConnectionID, remote: m.Src, pc: pc}
	c.mu.Lock()
	c.sessions[p.ConnectionID] = s
	c.byPeer[m.Src] = s
	c.mu.Unlock()

	pc.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}
		c.write(map[string]any{
			"type": "CANDIDATE",
			"payload": map[string]any{
				"type":         "data",
				"connectionId": p.ConnectionID,
				"candidate":    cand.ToJSON(),
			},
			"dst": m.Src,
		})
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		s.dc = dc
		dc.OnOpen(func() {
			c.log.Printf("通道就绪 · %s", m.Src)
			if c.OnPeer != nil {
				c.OnPeer(m.Src, true)
			}
		})
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if c.OnMessage != nil {
				c.OnMessage(m.Src, decodeText(msg.Data))
			}
		})
		dc.OnClose(func() {
			c.log.Printf("通道关闭 · %s", m.Src)
			if c.OnPeer != nil {
				c.OnPeer(m.Src, false)
			}
		})
	})

	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			c.dropSession(p.ConnectionID)
		}
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  p.SDP.SDP,
	}); err != nil {
		c.log.Printf("SetRemoteDescription 失败: %v", err)
		c.dropSession(p.ConnectionID)
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		c.log.Printf("CreateAnswer 失败: %v", err)
		c.dropSession(p.ConnectionID)
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		c.log.Printf("SetLocalDescription 失败: %v", err)
		c.dropSession(p.ConnectionID)
		return
	}

	c.write(map[string]any{
		"type": "ANSWER",
		"payload": map[string]any{
			"sdp":          map[string]any{"type": "answer", "sdp": pc.LocalDescription().SDP},
			"type":         "data",
			"connectionId": p.ConnectionID,
		},
		"dst": m.Src,
	})
}

func (c *Client) handleCandidate(m message) {
	var p candidatePayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		return
	}
	c.mu.Lock()
	s := c.sessions[p.ConnectionID]
	c.mu.Unlock()
	if s == nil {
		return
	}
	init := webrtc.ICECandidateInit{Candidate: p.Candidate.Candidate}
	if p.Candidate.SDPMid != nil {
		init.SDPMid = p.Candidate.SDPMid
	}
	if p.Candidate.SDPMLineIndex != nil {
		init.SDPMLineIndex = p.Candidate.SDPMLineIndex
	}
	if err := s.pc.AddICECandidate(init); err != nil {
		c.log.Printf("AddICECandidate: %v", err)
	}
}

func (c *Client) dropSession(connID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[connID]
	if s == nil {
		return
	}
	if c.byPeer[s.remote] == s {
		delete(c.byPeer, s.remote)
	}
	delete(c.sessions, connID)
}

func withDefaults(cfg Config) Config {
	if cfg.Host == "" {
		cfg.Host = DefaultHost
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	if cfg.Key == "" {
		cfg.Key = DefaultKey
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.ICEServers == nil {
		cfg.ICEServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
			{URLs: []string{"turn:0.peerjs.com:3478"}, Username: "peerjs", Credential: "peerjsp"},
		}
	}
	return cfg
}

func fetchCandidateID(ctx context.Context, cfg Config) (string, error) {
	u := fmt.Sprintf("https://%s:%d%s/id?ts=%d", cfg.Host, cfg.Port, endpointPath(cfg), time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("peerjs: 取候选 id: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(body))
	if id == "" || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("peerjs: 取候选 id 失败 (http %d, body %.100q)", resp.StatusCode, id)
	}
	return id, nil
}

func endpointPath(cfg Config) string {
	p := cfg.Path
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p + cfg.Key
}

func wsURL(cfg Config, id, token string) string {
	return fmt.Sprintf("wss://%s:%d%s?key=%s&id=%s&token=%s",
		cfg.Host, cfg.Port, endpointPath(cfg), cfg.Key, id, token)
}

func randomUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

// PeerJS 的 json serializer 发送的是 encoder.encode(JSON.stringify(data))，
// 也就是二进制帧；这里必须对称地用 JSON 编解码。
func encodeText(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func decodeText(b []byte) string {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		return s
	}
	return string(b)
}
