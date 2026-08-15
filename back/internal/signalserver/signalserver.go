// Package signalserver 自托管 PeerJS 信令服务器（兼容 peerjs-server 协议子集）
// + 内置房间发现（替代公共信令 + 公共 MQTT broker）。
//
// 职责：
//  1. 信令：节点注册（WS + token）、OFFER/ANSWER/CANDIDATE/LEAVE 按 dst 转发、
//     dst 不在线入队（带过期）、心跳保活、ID 分配
//  2. 发现：节点 announce 自己关注的集合，`GET /discover/nodes?coll=` 查询在线节点
//     ——自托管后服务器天然知道所有在线节点，不再需要 MQTT 广播
//
// 协议细节对齐 peers/peerjs-server（src/services/webSocketServer、messageHandler）：
//   - WS URL: /{path}peerjs?key=&id=&token=
//   - 消息 {type, src, dst, payload}，服务端覆盖 src
//   - dst 在线转发；不在线入队（LEAVE/EXPIRE 不入队）
//   - OPEN / ID-TAKEN / ERROR 控制消息
//   - 客户端每 5s 发 HEARTBEAT 保活
package signalserver

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Server struct {
	key          string
	path         string
	queueTTL     time.Duration // 离线队列存活时间（OFFER 过期用）
	heartbeatTTL time.Duration // 发现的心跳过期时间

	mu      sync.Mutex
	clients map[string]*client              // id → 在线连接
	queues  map[string][]queuedMsg          // dst → 待转发消息
	disc    map[string]map[string]time.Time // collection → peerId → lastSeen
}

// client 一条在线信令连接。
type client struct {
	id     string
	token  string
	conn   *websocket.Conn
	sendMu sync.Mutex // gorilla 不允许并发写
	last   time.Time  // 最后心跳
}

// queuedMsg 离线队列条目（入队时带过期时间）。
type queuedMsg struct {
	msg    Message
	expire time.Time
}

// Message 信令消息（与 peerjs 客户端协议一致）。
type Message struct {
	Type    string          `json:"type"`
	Src     string          `json:"src,omitempty"`
	Dst     string          `json:"dst,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewServer 创建信令服务器。
func NewServer(key string) *Server {
	if key == "" {
		key = "peerjs"
	}
	return &Server{
		key:          key,
		path:         "",
		queueTTL:     30 * time.Second,
		heartbeatTTL: 90 * time.Second,
		clients:      make(map[string]*client),
		queues:       make(map[string][]queuedMsg),
		disc:         make(map[string]map[string]time.Time),
	}
}

// HandleID GET /{path}{key}/id → 随机 id（peerjs API 兼容）。
func (s *Server) HandleID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, randomID())
}

// HandleWS 处理信令 WebSocket 升级与消息循环。
// 路径形如 /{path}peerjs?key=&id=&token=（gin 路由挂载时提供 {path}）。
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, token, key := q.Get("id"), q.Get("token"), q.Get("key")
	if id == "" || token == "" || key == "" {
		s.wsError(w, "No id, token, or key supplied to websocket server")
		return
	}
	if key != s.key {
		s.wsError(w, "Invalid key provided")
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true }, // 自托管，由调用方配置白名单
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.mu.Lock()
	// ID 占用：token 匹配则复用连接，否则拒绝
	if existing, ok := s.clients[id]; ok {
		if existing.token != token {
			s.mu.Unlock()
			_ = conn.WriteJSON(Message{Type: "ID-TAKEN", Payload: raw(`{"msg":"ID is taken"}`)})
			_ = conn.Close()
			return
		}
		existing.closeConn()
	}
	cl := &client{id: id, token: token, conn: conn, last: time.Now()}
	s.clients[id] = cl
	s.mu.Unlock()

	_ = conn.WriteJSON(Message{Type: "OPEN"})
	s.flushQueue(cl)

	go s.readLoop(cl)
}

// readLoop 读取客户端消息并路由。
func (s *Server) readLoop(cl *client) {
	defer func() {
		s.removeClient(cl)
		_ = cl.conn.Close()
	}()
	for {
		var m Message
		if err := cl.conn.ReadJSON(&m); err != nil {
			return
		}
		m.Src = cl.id // 服务端覆盖 src
		s.mu.Lock()
		cl.last = time.Now()
		s.mu.Unlock()
		s.route(m)
	}
}

// route 路由消息：dst 在线转发，不在线入队（LEAVE/EXPIRE 除外）。
func (s *Server) route(m Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dst := s.clients[m.Dst]
	if dst != nil {
		_ = dst.send(m)
		return
	}
	if m.Type == "LEAVE" || m.Type == "EXPIRE" {
		return
	}
	if m.Dst == "" {
		return
	}
	// 入队：目标上线后补发（OFFER/ANSWER/CANDIDATE）
	s.queues[m.Dst] = append(s.queues[m.Dst], queuedMsg{msg: m, expire: time.Now().Add(s.queueTTL)})
}

// flushQueue 客户端上线后补发离线队列（含过期清理）。
func (s *Server) flushQueue(cl *client) {
	s.mu.Lock()
	q := s.queues[cl.id]
	delete(s.queues, cl.id)
	s.mu.Unlock()
	now := time.Now()
	for _, qm := range q {
		if qm.expire.After(now) {
			_ = cl.send(qm.msg)
		}
	}
}

// removeClient 断开清理：通知其他节点 LEAVE、删除发现记录。
func (s *Server) removeClient(cl *client) {
	s.mu.Lock()
	if s.clients[cl.id] != cl {
		s.mu.Unlock()
		return
	}
	delete(s.clients, cl.id)
	leave := Message{Type: "LEAVE", Src: cl.id}
	var victims []*client
	for _, c := range s.clients {
		victims = append(victims, c)
	}
	for coll, peers := range s.disc {
		delete(peers, cl.id)
		if len(peers) == 0 {
			delete(s.disc, coll)
		}
	}
	s.mu.Unlock()
	for _, c := range victims {
		_ = c.send(leave)
	}
}

// HandleAnnounce POST /discover/announce {peerId, collections[]} 节点登记房间。
// 与信令连接解耦（节点可通过任意 HTTP 入口上报），lastSeen 由心跳刷新。
func (s *Server) HandleAnnounce(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeerID      string   `json:"peerId"`
		Collections []string `json:"collections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}
	now := time.Now()
	s.mu.Lock()
	for _, coll := range body.Collections {
		coll = strings.TrimSpace(coll)
		if coll == "" {
			continue
		}
		peers, ok := s.disc[coll]
		if !ok {
			peers = make(map[string]time.Time)
			s.disc[coll] = peers
		}
		peers[body.PeerID] = now
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// HandleNodes GET /discover/nodes?coll= → 在线节点列表（心跳过期剔除）。
func (s *Server) HandleNodes(w http.ResponseWriter, r *http.Request) {
	coll := r.URL.Query().Get("coll")
	cutoff := time.Now().Add(-s.heartbeatTTL)
	s.mu.Lock()
	peers := s.disc[coll]
	out := make([]NodeInfo, 0, len(peers))
	for id, last := range peers {
		if last.After(cutoff) {
			out = append(out, NodeInfo{PeerID: id, LastSeen: last.Unix()})
		}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": out})
}

// NodeInfo 发现响应条目。
type NodeInfo struct {
	PeerID   string `json:"peerId"`
	LastSeen int64  `json:"lastSeen"`
}

// wsError 升级失败（HTTP 层）。
func (s *Server) wsError(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}

func (cl *client) send(m Message) error {
	cl.sendMu.Lock()
	defer cl.sendMu.Unlock()
	cl.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return cl.conn.WriteJSON(m)
}

func (cl *client) closeConn() {
	cl.sendMu.Lock()
	defer cl.sendMu.Unlock()
	_ = cl.conn.Close()
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// randomID 生成符合 PeerJS 规则的首尾字母数字 id。
func randomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = chars[b[i]%byte(len(chars))]
	}
	return string(b)
}
