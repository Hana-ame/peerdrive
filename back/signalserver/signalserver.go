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
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed dashboard.html
var dashboardFS embed.FS

type Server struct {
	key            string
	path           string
	queueTTL       time.Duration   // 离线队列存活时间（OFFER 过期用）
	heartbeatTTL   time.Duration   // 发现的心跳过期时间
	tokenWhitelist map[string]bool // 允许的信令 token（nil/空 = 不限制）

	startedAt time.Time // 服务器启动时间
	msgCount  int64     // 总转发消息数（原子访问）

	mu        sync.Mutex
	clients   map[string]*client              // id → 在线连接
	queues    map[string][]queuedMsg          // dst → 待转发消息
	disc      map[string]map[string]time.Time // collection → peerId → lastSeen
	peerLinks map[string]map[string]time.Time // peerId → 邻居 peerId → lastSeen（graph 用）
	peerColls map[string][]string             // peerId -> collections
	peerStats map[string]*PeerStats           // peerId -> stats
}

// Option 信令服务器配置项。
type Option func(*Server)

// WithTokenWhitelist 设置信令 token 白名单：WS 连接的 token 必须在名单内，
// 否则拒绝升级（"Invalid token provided"）。空名单 = 不限制（默认，兼容
// 公共部署现状）。
// 坑（2026-08-18 代码审阅）：token 原本只是 ID 占用保护——任意客户端可自定
// token 连接，攻击者可注册任意 ID 冒充在线节点收信令（配合其自选 ID 可以
// 对任何节点发起 OFFER 诱导连向攻击者）；白名单让自托管部署只信任已知节点。
// 注意：发现端点（announce/nodes）保持公开——发现的目的就是让任何人找到
// 节点，白名单只约束信令面。
func WithTokenWhitelist(tokens []string) Option {
	return func(s *Server) {
		if len(tokens) == 0 {
			return
		}
		s.tokenWhitelist = make(map[string]bool, len(tokens))
		for _, t := range tokens {
			s.tokenWhitelist[t] = true
		}
	}
}

// 离线队列限制（H3 修复）：每 dst 最多缓存 maxQueuedPerDst 条消息。
// 坑：原实现无上限——dst 永不连接时队列无限增长（每个恶意客户端可对任意随机 ID
// 发 OFFER 把服务器内存打爆）。超限丢最旧（信令消息过期即失效，丢旧比丢新合理）。
const maxQueuedPerDst = 100

// Start 启动后台 sweeper：定期清理已过期队列项与空队列。
// 背景：过期清理原只在 flushQueue（dst 上线）时做，dst 永不连接则过期消息堆积。
func (s *Server) Start() {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			s.sweepQueues()
			s.sweepDiscovery()
		}
	}()
}

func (s *Server) sweepQueues() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for dst, q := range s.queues {
		kept := q[:0]
		for _, qm := range q {
			if qm.expire.After(now) && s.clients[dst] == nil {
				kept = append(kept, qm)
			}
		}
		if len(kept) == 0 {
			delete(s.queues, dst)
		} else {
			s.queues[dst] = kept
		}
	}
}

// sweepDiscovery 定期清理心跳过期的发现节点，并同步清理 graph/元数据，防止长期运行内存和 graph 残留。
func (s *Server) sweepDiscovery() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-s.heartbeatTTL)

	active := make(map[string]bool)
	for coll, peers := range s.disc {
		for id, last := range peers {
			if last.Before(cutoff) {
				delete(peers, id)
				continue
			}
			active[id] = true
		}
		if len(peers) == 0 {
			delete(s.disc, coll)
		}
	}

	// 清理不再活跃节点的 graph 起点
	for id := range s.peerLinks {
		if !active[id] {
			delete(s.peerLinks, id)
		}
	}
	// 清理指向不再活跃节点的边
	for _, links := range s.peerLinks {
		for nid := range links {
			if !active[nid] {
				delete(links, nid)
			}
		}
	}
	// 清理不再活跃节点的统计/集合
	for id := range s.peerStats {
		if !active[id] {
			delete(s.peerStats, id)
		}
	}
	for id := range s.peerColls {
		if !active[id] {
			delete(s.peerColls, id)
		}
	}
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
func NewServer(key string, opts ...Option) *Server {
	if key == "" {
		key = "peerjs"
	}
	s := &Server{
		key:          key,
		path:         "",
		queueTTL:     30 * time.Second,
		heartbeatTTL: 90 * time.Second,
		startedAt:    time.Now(),
		clients:      make(map[string]*client),
		queues:       make(map[string][]queuedMsg),
		disc:         make(map[string]map[string]time.Time),
		peerLinks:    make(map[string]map[string]time.Time),
		peerColls:    make(map[string][]string),
		peerStats:    make(map[string]*PeerStats),
	}
	for _, o := range opts {
		o(s)
	}
	return s
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
	// token 白名单：空名单 = 不限制（默认）；非空则 token 必须在名单内。
	// 见 WithTokenWhitelist 的坑说明（任意 token 可冒充节点收信令）。
	if len(s.tokenWhitelist) > 0 && !s.tokenWhitelist[token] {
		s.wsError(w, "Invalid token provided")
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true }, // 自托管，由调用方配置白名单
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// 读限制：信令消息很小（SDP/ICE 文案），40KB 足够；防恶意客户端塞超大 payload。
	// 同时设 60s 读超时兜底——readLoop 里靠 HEARTBEAT 刷新，断连客户端不再占资源。
	conn.SetReadLimit(40 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))

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

	_ = cl.send(Message{Type: "OPEN"})
	s.flushQueue(cl)

	go s.readLoop(cl)
}

// readLoop 读取客户端消息并路由。
func (s *Server) readLoop(cl *client) {
	defer func() {
		s.removeClient(cl)
		cl.closeConn()
	}()
	for {
		var m Message
		if err := cl.conn.ReadJSON(&m); err != nil {
			return
		}
		m.Src = cl.id // 服务端覆盖 src
		s.mu.Lock()
		cl.last = time.Now()
		// 收到消息即视为活跃，续读超时（配合 HandleWS 的 SetReadDeadline）
		_ = cl.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		s.mu.Unlock()
		s.route(m)
	}
}

// route 路由消息：dst 在线转发，不在线入队（LEAVE/EXPIRE 除外）。
//
// 两处对标 peers/peerjs-server 的修复（src/messageHandler/handlers/transmission）：
//
//  1. send 失败不再静默丢弃。旧实现 `_ = dst.send(m)`：目标 socket 已半开但还
//     没从 clients 表摘除时（对端崩溃、NAT 映射消失、连接半开未 FIN），
//     OFFER/ANSWER/CANDIDATE 会被吞掉，发起方永久卡在等握手。这里摘除死连接
//     并向发起方补发 LEAVE 让其停止重试。
//  2. send 前释放 s.mu。send 内部 WriteJSON 带 10s 写超时，一个慢/死客户端
//     会持锁 10s 卡死整个信令服务器的路由。因此出锁后再写。
func (s *Server) route(m Message) {
	s.mu.Lock()
	dst := s.clients[m.Dst]
	if dst != nil {
		s.mu.Unlock() // 出锁后再写，避免持 s.mu 阻塞在慢客户端上
		if err := dst.send(m); err == nil {
			atomic.AddInt64(&s.msgCount, 1)
			return
		}
		s.handleDeadDst(dst, m)
		return
	}
	s.mu.Unlock()

	if m.Type == "LEAVE" || m.Type == "EXPIRE" || m.Dst == "" {
		return
	}
	// 入队：目标上线后补发（OFFER/ANSWER/CANDIDATE）
	// H3：队列无上限 → OOM。超 maxQueuedPerDst 丢最旧。
	s.mu.Lock()
	defer s.mu.Unlock()
	q := append(s.queues[m.Dst], queuedMsg{msg: m, expire: time.Now().Add(s.queueTTL)})
	if len(q) > maxQueuedPerDst {
		q = q[len(q)-maxQueuedPerDst:]
	}
	s.queues[m.Dst] = q
	atomic.AddInt64(&s.msgCount, 1)
}

// handleDeadDst 处理"在 clients 表里但发送失败"的目标：摘除表项、关连接、
// 广播 LEAVE。
func (s *Server) handleDeadDst(dst *client, m Message) {
	s.mu.Lock()
	if cur, ok := s.clients[m.Dst]; !ok || cur != dst {
		s.mu.Unlock()
		return // 已被别的流程摘除/顶替，交给那边的清理
	}
	delete(s.clients, m.Dst)
	var victims []*client
	for _, c := range s.clients {
		if c != dst {
			victims = append(victims, c)
		}
	}
	// 清理该死连接在发现/图/元数据中的残留（removeClient 因 clients 已摘除不会再来清理）
	for coll, peers := range s.disc {
		delete(peers, m.Dst)
		if len(peers) == 0 {
			delete(s.disc, coll)
		}
	}
	delete(s.peerLinks, m.Dst)
	for _, links := range s.peerLinks {
		delete(links, m.Dst)
	}
	delete(s.peerStats, m.Dst)
	delete(s.peerColls, m.Dst)
	s.mu.Unlock()

	dst.closeConn()
	leave := Message{Type: "LEAVE", Src: m.Dst}
	for _, c := range victims {
		_ = c.send(leave)
	}
	if m.Src == "" || m.Src == m.Dst {
		return
	}
	s.mu.Lock()
	src := s.clients[m.Src]
	s.mu.Unlock()
	if src != nil {
		_ = src.send(Message{Type: "LEAVE", Src: m.Dst, Dst: m.Src})
	}
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
	delete(s.peerLinks, cl.id)
	for _, links := range s.peerLinks {
		delete(links, cl.id)
	}
	delete(s.peerStats, cl.id)
	delete(s.peerColls, cl.id)
	s.mu.Unlock()
	for _, c := range victims {
		_ = c.send(leave)
	}
}

// HandleAnnounce POST /discover/announce {peerId, collections[], nodeType, loadInfo, peers} 节点登记。
// 与信令连接解耦（节点可通过任意 HTTP 入口上报），lastSeen 由心跳刷新。
// M15：无界 decode 风险——限制 body 大小（8KB 足够：peerId + collections + peers + loadInfo）
// 与 collection 数量（单节点关注房间数有限）。
func (s *Server) HandleAnnounce(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		PeerID      string         `json:"peerId"`
		Collections []string       `json:"collections"`
		NodeType    string         `json:"nodeType,omitempty"`
		LoadInfo    map[string]any `json:"loadInfo,omitempty"`
		Peers       []string       `json:"peers,omitempty"` // 当前 WebRTC 直连的对端 peer id 列表
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}
	const maxCollectionsPerAnnounce = 64
	if len(body.Collections) > maxCollectionsPerAnnounce {
		http.Error(w, "too many collections", http.StatusBadRequest)
		return
	}
	// 规范化 collection 列表：trim 并去掉空串，disc 与 peerColls 使用同一份数据。
	cleanColls := make([]string, 0, len(body.Collections))
	for _, coll := range body.Collections {
		coll = strings.TrimSpace(coll)
		if coll != "" {
			cleanColls = append(cleanColls, coll)
		}
	}

	now := time.Now()
	s.mu.Lock()
	collSet := make(map[string]bool, len(cleanColls))
	for _, coll := range cleanColls {
		collSet[coll] = true
		peers, ok := s.disc[coll]
		if !ok {
			peers = make(map[string]time.Time)
			s.disc[coll] = peers
		}
		peers[body.PeerID] = now
	}
	// 立即从旧集合移除该节点（collections 变更后不用等 TTL）
	for coll, peers := range s.disc {
		if !collSet[coll] {
			delete(peers, body.PeerID)
			if len(peers) == 0 {
				delete(s.disc, coll)
			}
		}
	}
	// 更新节点元数据（nodeType/collections/loadInfo），dashboard 与发现 API 共用。
	stats := s.peerStats[body.PeerID]
	if stats == nil {
		stats = &PeerStats{}
		s.peerStats[body.PeerID] = stats
	}
	stats.NodeType = body.NodeType
	stats.LastSeen = now
	stats.LoadInfo = body.LoadInfo
	// 总是更新 collections（空也清空旧值），避免节点清空集合后旧集合残留。
	s.peerColls[body.PeerID] = cleanColls
	// 更新 graph 边：该节点当前直连的对端列表。
	// 总是更新（即使 peers 为空/缺失也清空旧边），避免 graph 残留过期连接。
	links := make(map[string]time.Time, len(body.Peers))
	for _, nid := range body.Peers {
		nid = strings.TrimSpace(nid)
		if nid != "" && nid != body.PeerID {
			links[nid] = now
		}
	}
	s.peerLinks[body.PeerID] = links
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// HandleLeave POST /discover/leave 节点优雅下线。
func (s *Server) HandleLeave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PeerID string `json:"peerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	// 从所有 collection 中移除
	for _, peers := range s.disc {
		delete(peers, body.PeerID)
	}
	delete(s.peerStats, body.PeerID)
	delete(s.peerColls, body.PeerID)
	delete(s.peerLinks, body.PeerID)
	// 同时从其他节点的邻居列表中移除该节点
	for _, links := range s.peerLinks {
		delete(links, body.PeerID)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// GraphLink graph 中的一条边（source ↔ target 已建立 WebRTC 连接）。
type GraphLink struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	LastSeen int64  `json:"lastSeen,omitempty"`
}

// HandleNodes GET /discover/nodes?coll=&type= → 在线节点列表（心跳过期剔除）+ graph 边。
// 空 coll 表示返回所有 collection 的节点；type 可用于过滤节点类型。
func (s *Server) HandleNodes(w http.ResponseWriter, r *http.Request) {
	coll := r.URL.Query().Get("coll")
	nodeType := r.URL.Query().Get("type")
	cutoff := time.Now().Add(-s.heartbeatTTL)
	s.mu.Lock()
	out := make([]NodeInfo, 0)
	seen := make(map[string]bool)

	if coll != "" {
		// 指定 collection：只查这一个房间
		peers := s.disc[coll]
		out = make([]NodeInfo, 0, len(peers))
		for id, last := range peers {
			if last.After(cutoff) && !seen[id] {
				info := s.nodeInfo(id, last)
				// 类型过滤不通过时不标记 seen：允许该节点在其他 collection
				// 再次被检查，同时 graph 边只基于实际返回的节点。
				if nodeType != "" && info.NodeType != nodeType {
					continue
				}
				seen[id] = true
				out = append(out, info)
			}
		}
	} else {
		// 空 coll：遍历所有 collection，返回去重后的全部在线节点
		for _, peers := range s.disc {
			for id, last := range peers {
				if last.After(cutoff) && !seen[id] {
					info := s.nodeInfo(id, last)
					if nodeType != "" && info.NodeType != nodeType {
						continue
					}
					seen[id] = true
					out = append(out, info)
				}
			}
		}
	}
	// 收集 graph 边：仅保留两端都仍活跃的边，避免展示离线幽灵节点。
	links := make([]GraphLink, 0)
	linkSeen := make(map[string]bool)
	for src, neighbors := range s.peerLinks {
		if !seen[src] {
			continue
		}
		for dst, last := range neighbors {
			if !seen[dst] {
				continue
			}
			a, b := src, dst
			if a > b {
				a, b = b, a
			}
			key := a + "\x00" + b
			if linkSeen[key] {
				continue
			}
			linkSeen[key] = true
			links = append(links, GraphLink{Source: a, Target: b, LastSeen: last.Unix()})
		}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": out, "links": links})
}

// PeerStats 节点统计与负载信息。
type PeerStats struct {
	NodeType string         `json:"nodeType,omitempty"`
	Uptime   int64          `json:"uptime,omitempty"`
	LoadInfo map[string]any `json:"loadInfo,omitempty"`
	LastSeen time.Time      `json:"-"`
}

// nodeInfo 从 peerStats/peerColls 组装发现响应条目。
func (s *Server) nodeInfo(id string, last time.Time) NodeInfo {
	info := NodeInfo{PeerID: id, LastSeen: last.Unix()}
	if st := s.peerStats[id]; st != nil {
		info.NodeType = st.NodeType
		info.Uptime = st.Uptime
		info.LoadInfo = st.LoadInfo
	}
	if colls := s.peerColls[id]; len(colls) > 0 {
		info.Collections = colls
	}
	return info
}

// NodeInfo 发现响应条目。
type NodeInfo struct {
	PeerID      string         `json:"peerId"`
	LastSeen    int64          `json:"lastSeen"`
	NodeType    string         `json:"nodeType,omitempty"`
	Collections []string       `json:"collections,omitempty"`
	Uptime      int64          `json:"uptime,omitempty"`
	LoadInfo    map[string]any `json:"loadInfo,omitempty"`
}

// HandleStatus GET /status → 服务器状态快照（dashboard 轮询用）。
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	clientCount := len(s.clients)
	queueCount := len(s.queues)
	totalQueued := 0
	for _, q := range s.queues {
		totalQueued += len(q)
	}
	// 收集活跃发现节点
	discoveredNodes := make([]NodeInfo, 0)
	seen := make(map[string]bool)
	cutoff := time.Now().Add(-s.heartbeatTTL)
	for _, peers := range s.disc {
		for id, last := range peers {
			if last.After(cutoff) && !seen[id] {
				seen[id] = true
				discoveredNodes = append(discoveredNodes, s.nodeInfo(id, last))
			}
		}
	}
	// 收集 graph 边：仅保留两端都仍活跃的边，避免展示离线幽灵节点。
	links := make([]GraphLink, 0)
	linkSeen := make(map[string]bool)
	for src, neighbors := range s.peerLinks {
		if !seen[src] {
			continue
		}
		for dst, last := range neighbors {
			if !seen[dst] {
				continue
			}
			a, b := src, dst
			if a > b {
				a, b = b, a
			}
			key := a + "\x00" + b
			if linkSeen[key] {
				continue
			}
			linkSeen[key] = true
			links = append(links, GraphLink{Source: a, Target: b, LastSeen: last.Unix()})
		}
	}
	s.mu.Unlock()

	uptime := time.Since(s.startedAt).Seconds()
	resp := map[string]any{
		"key":         s.key,
		"uptimeSec":   int64(uptime),
		"uptimeStr":   formatDuration(uptime),
		"clients":     clientCount,
		"queues":      queueCount,
		"totalQueued": totalQueued,
		"discovered":  len(discoveredNodes),
		"nodes":       discoveredNodes,
		"links":       links,
		"msgCount":    atomic.LoadInt64(&s.msgCount),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleDashboard GET / → dashboard HTML（内嵌）。
func (s *Server) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(b)
}

// formatDuration 秒数转人可读时长。
func formatDuration(seconds float64) string {
	d := time.Duration(seconds) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", seconds)
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", seconds/60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", seconds/3600)
	}
	return fmt.Sprintf("%.1fd", seconds/86400)
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
