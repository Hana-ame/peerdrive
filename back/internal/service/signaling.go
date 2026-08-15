// Package service implements WebRTC signaling and room management for
// browser-to-browser file transfers. The signaling hub acts as a lightweight
// relay for SDP offers/answers and ICE candidates between peers that wish
// to establish a direct WebRTC DataChannel connection.
//
// Room-based signaling: peers join a "room" identified by a file hash.
// Once two peers are in the same room, the hub relays signaling messages
// (offer, answer, ice) between them so they can establish a direct
// RTCPeerConnection.
package service

import (
	"net/http"
	"sync"
	"time"

	"peerdrive/internal/log"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// SignalingMessage is the WS message format for WebRTC signaling.
type SignalingMessage struct {
	Type      string   `json:"type"`
	From      string   `json:"from,omitempty"`
	To        string   `json:"to,omitempty"`
	PeerID    string   `json:"peer_id,omitempty"`
	Token     string   `json:"token,omitempty"`
	SDP       string   `json:"sdp,omitempty"`
	Candidate string   `json:"candidate,omitempty"`
	Hash      string   `json:"hash,omitempty"`
	Peers     []string `json:"peers,omitempty"`
	Message   string   `json:"message,omitempty"`
}

type peerConn struct {
	PeerID string
	Conn   *websocket.Conn
	mu     sync.Mutex
}

func (p *peerConn) send(msg SignalingMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// M2：写 deadline 防止对端不读时 WriteJSON 永久阻塞（gorilla 默认无超时）。
	_ = p.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return p.Conn.WriteJSON(msg)
}

// SignalingHub manages WebRTC signaling between peers, including room-based
// groups identified by a file hash.
type SignalingHub struct {
	mu    sync.RWMutex
	peers map[string]*peerConn           // peerID -> connection
	rooms map[string]map[string]struct{} // hash -> set of peerIDs in that room
	files map[string][]string            // hash -> []peerID (who has what file)
}

// NewSignalingHub 创建新的信令中枢实例。
func NewSignalingHub() *SignalingHub {
	return &SignalingHub{
		peers: make(map[string]*peerConn),
		rooms: make(map[string]map[string]struct{}),
		files: make(map[string][]string),
	}
}

// PeerCount 返回已连接的对端数量。
func (h *SignalingHub) PeerCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.peers)
}

// GetPeers 返回所有已连接的对端 ID 列表。
func (h *SignalingHub) GetPeers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	peers := make([]string, 0, len(h.peers))
	for pid := range h.peers {
		peers = append(peers, pid)
	}
	return peers
}

// RoomPeers 返回指定房间内的对端 ID 列表。
func (h *SignalingHub) RoomPeers(hash string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[hash]
	result := make([]string, 0, len(room))
	for pid := range room {
		result = append(result, pid)
	}
	return result
}

// M2 修复：处理 WebSocket 信令连接的入口。
// - SetReadLimit：信令消息（SDP/ICE 文案）很小，单帧上限 40KB 防恶意超大 payload
// - 读超时 + PongHandler：对端断连/不活跃时不再被它无限挂住
func (h *SignalingHub) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.LogError("signal: upgrade error: %v", err)
		return
	}
	conn.SetReadLimit(40 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	var peer *peerConn
	peerID := ""
	defer func() {
		if peerID != "" {
			// L4：带连接身份 unregister，避免旧连接/重复注册的断开误删新连接
			h.unregister(peerID, peer)
			h.broadcast(SignalingMessage{Type: "peer_left", PeerID: peerID})
			log.LogInfo("signal: peer left: %s", peerID)
		}
		conn.Close()
	}()

	for {
		var msg SignalingMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		switch msg.Type {
		case "register":
			if msg.PeerID == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "peer_id is required"})
				continue
			}
			peerID = msg.PeerID
			peer = &peerConn{PeerID: msg.PeerID, Conn: conn}
			// L4：同 ID 已有连接（重复注册）→ 顶替并关闭旧连接，防幽灵 goroutine 误清理
			if old := h.register(msg.PeerID, peer); old != nil && old.Conn != conn {
				log.LogWarn("signal: peer %s re-registered, closing old connection", msg.PeerID[:min(len(msg.PeerID), 12)])
				_ = old.Conn.Close()
			}
			conn.WriteJSON(SignalingMessage{
				Type:   "registered",
				PeerID: msg.PeerID,
			})
			h.broadcast(SignalingMessage{Type: "peer_joined", PeerID: msg.PeerID}, msg.PeerID)
			log.LogInfo("signal: peer registered: %s", msg.PeerID[:min(len(msg.PeerID), 12)])

		case "join":
			// Room-based join: peer joins a room identified by file hash.
			if msg.PeerID == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "peer_id is required"})
				continue
			}
			if msg.Hash == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "hash is required for join"})
				continue
			}
			peerID = msg.PeerID
			peer = &peerConn{PeerID: msg.PeerID, Conn: conn}
			// L4：同 ID 已有连接 → 顶替并关闭旧连接
			if old := h.register(msg.PeerID, peer); old != nil && old.Conn != conn {
				log.LogWarn("signal: peer %s joined with existing connection, closing old", msg.PeerID[:min(len(msg.PeerID), 12)])
				_ = old.Conn.Close()
			}
			h.joinRoom(msg.PeerID, msg.Hash)
			shortID := msg.PeerID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			shortHash := msg.Hash
			if len(shortHash) > 16 {
				shortHash = shortHash[:16]
			}
			log.LogInfo("signal: peer %s joined room %s", shortID, shortHash)

			// Notify the joining peer of existing room occupants.
			roomPeers := h.RoomPeers(msg.Hash)
			conn.WriteJSON(SignalingMessage{
				Type:   "room_joined",
				Hash:   msg.Hash,
				Peers:  roomPeers,
				PeerID: msg.PeerID,
			})

			// Notify other room occupants of the new peer.
			h.broadcastRoom(msg.Hash, SignalingMessage{
				Type:   "peer_joined_room",
				PeerID: msg.PeerID,
				Hash:   msg.Hash,
			}, msg.PeerID)

		case "room_peers":
			if msg.Hash == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "hash is required"})
				continue
			}
			conn.WriteJSON(SignalingMessage{
				Type:  "room_peers",
				Hash:  msg.Hash,
				Peers: h.RoomPeers(msg.Hash),
			})

		case "request_peers":
			conn.WriteJSON(SignalingMessage{Type: "peers", Peers: h.GetPeers()})

		case "offer", "answer":
			if msg.To == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "target peer_id is required"})
				continue
			}
			// Browser-to-node support: if target starts with "node:", route to the specified node
			if msg.PeerID == "" {
				msg.PeerID = peerID
			}
			h.relay(msg)

		case "ice_candidate", "ice":
			if msg.To == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "target peer_id is required"})
				continue
			}
			// Support both "ice_candidate" (legacy) and "ice" (new protocol).
			relayMsg := msg
			relayMsg.Type = "ice_candidate"
			if msg.PeerID == "" {
				relayMsg.PeerID = peerID
			}
			h.relay(relayMsg)

		case "announce_file":
			if msg.PeerID == "" {
				msg.PeerID = peerID
			}
			if msg.PeerID == "" || msg.Hash == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "peer_id and hash required"})
				continue
			}
			h.mu.Lock()
			h.files[msg.Hash] = appendIfMissing(h.files[msg.Hash], msg.PeerID)
			h.mu.Unlock()
			shortHash := msg.Hash
			if len(shortHash) > 16 {
				shortHash = shortHash[:16]
			}
			log.LogInfo("signal: file announced: %s by %s", shortHash, msg.PeerID[:min(len(msg.PeerID), 12)])

		case "find_file":
			if msg.Hash == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "hash is required"})
				continue
			}
			h.mu.RLock()
			providers := h.files[msg.Hash]
			h.mu.RUnlock()
			conn.WriteJSON(SignalingMessage{
				Type:  "file_providers",
				Hash:  msg.Hash,
				Peers: providers,
			})

		case "direct_message":
			// Direct message support for node-to-browser signaling
			if msg.To == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "target peer_id is required"})
				continue
			}
			if msg.PeerID == "" {
				msg.PeerID = peerID
			}
			h.relay(msg)

		default:
			log.LogDebug("signal: unknown message type: %s", msg.Type)
		}
	}
}

// min returns the smaller of a and b (avoids needing Go 1.21+ builtin).
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// register 登记 peerID→连接。返回被顶替的旧连接（若有）。
// L4：重复 peerID 注册（同 ID 重连/两个连接抢注）时，旧连接必须被主动关闭，
// 否则它留在 map 外的幽灵 goroutine 里，断开时 unregister 会误删新连接。
func (h *SignalingHub) register(peerID string, pc *peerConn) *peerConn {
	h.mu.Lock()
	old := h.peers[peerID]
	h.peers[peerID] = pc
	h.mu.Unlock()
	return old
}

// unregister 移除 peerID 的注册。pc 为身份凭据：仅当 h.peers[peerID] 仍指向
// 该连接时才真正清理——旧连接迟到的断开不能清掉已顶替它的新连接（L4）。
// 注意：旧连接在 rooms/files 里遗留的同 ID 条目保留（新连接 join 时覆盖），
// 因为 peerID 相同意味着「该节点仍在线」，条目语义不失效。
func (h *SignalingHub) unregister(peerID string, pc *peerConn) {
	h.mu.Lock()
	if pc != nil && h.peers[peerID] != pc {
		h.mu.Unlock()
		return
	}
	delete(h.peers, peerID)
	// Remove from all rooms.
	for hash, room := range h.rooms {
		delete(room, peerID)
		if len(room) == 0 {
			delete(h.rooms, hash)
		}
	}
	// Clean up file announcements.
	for hash, peers := range h.files {
		filtered := make([]string, 0)
		for _, p := range peers {
			if p != peerID {
				filtered = append(filtered, p)
			}
		}
		h.files[hash] = filtered
	}
	h.mu.Unlock()
}

func (h *SignalingHub) joinRoom(peerID, hash string) {
	h.mu.Lock()
	if h.rooms[hash] == nil {
		h.rooms[hash] = make(map[string]struct{})
	}
	h.rooms[hash][peerID] = struct{}{}
	h.mu.Unlock()
}

func (h *SignalingHub) relay(msg SignalingMessage) {
	// M2 修复：原先在 RLock 内直接 target.send —— 对端写阻塞会卡住整个 hub。
	// 改为锁内只查引用（锁外调用 send，写方向 peerConn 自带互斥与 deadline）。
	h.mu.RLock()
	target, ok := h.peers[msg.To]
	h.mu.RUnlock()
	if !ok {
		log.LogDebug("signal: relay target %s not found", msg.To)
		return
	}
	msg.From = msg.PeerID // the sender is the peer who sent this
	target.send(msg)
}

func (h *SignalingHub) broadcast(msg SignalingMessage, excludePeerIDs ...string) {
	// M2：快照目标列表后锁外逐个 send，避免持锁调用阻塞式网络写。
	h.mu.RLock()
	exclude := make(map[string]struct{}, len(excludePeerIDs))
	for _, id := range excludePeerIDs {
		exclude[id] = struct{}{}
	}
	targets := make([]*peerConn, 0, len(h.peers))
	for pid, p := range h.peers {
		if _, ok := exclude[pid]; ok {
			continue
		}
		targets = append(targets, p)
	}
	h.mu.RUnlock()
	for _, p := range targets {
		p.send(msg)
	}
}

// broadcastRoom sends a message to all peers in a room except the sender.
func (h *SignalingHub) broadcastRoom(hash string, msg SignalingMessage, excludePeerID string) {
	// M2：同 broadcast，快照后锁外发送。
	h.mu.RLock()
	room := h.rooms[hash]
	targets := make([]*peerConn, 0, len(room))
	for pid := range room {
		if pid == excludePeerID {
			continue
		}
		if p, ok := h.peers[pid]; ok {
			targets = append(targets, p)
		}
	}
	h.mu.RUnlock()
	for _, p := range targets {
		p.send(msg)
	}
}

func appendIfMissing(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}
