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
	return p.Conn.WriteJSON(msg)
}

// SignalingHub manages WebRTC signaling between peers, including room-based
// groups identified by a file hash.
type SignalingHub struct {
	mu       sync.RWMutex
	peers    map[string]*peerConn  // peerID -> connection
	rooms    map[string]map[string]struct{} // hash -> set of peerIDs in that room
	files    map[string][]string  // hash -> []peerID (who has what file)
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

// HandleConnection 处理 WebSocket 信令连接，负责注册、房间管理、offer/answer/ICE 转发等。
func (h *SignalingHub) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.LogError("signal: upgrade error: %v", err)
		return
	}

	var peer *peerConn
	defer func() {
		if peer != nil && peer.PeerID != "" {
			h.unregister(peer.PeerID)
			h.broadcast(SignalingMessage{Type: "peer_left", PeerID: peer.PeerID})
			log.LogInfo("signal: peer left: %s", peer.PeerID)
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
			peer = &peerConn{PeerID: msg.PeerID, Conn: conn}
			h.register(msg.PeerID, peer)
			conn.WriteJSON(SignalingMessage{Type: "registered", PeerID: msg.PeerID})
			h.broadcast(SignalingMessage{Type: "peer_joined", PeerID: msg.PeerID})
			log.LogInfo("signal: peer registered: %s", msg.PeerID)

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
			peer = &peerConn{PeerID: msg.PeerID, Conn: conn}
			h.register(msg.PeerID, peer)
			h.joinRoom(msg.PeerID, msg.Hash)
			log.LogInfo("signal: peer %s joined room %s", msg.PeerID[:8], msg.Hash[:16])

			// Notify the joining peer of existing room occupants.
			roomPeers := h.RoomPeers(msg.Hash)
			conn.WriteJSON(SignalingMessage{
				Type:  "room_joined",
				Hash:  msg.Hash,
				Peers: roomPeers,
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
			h.relay(msg)

		case "ice_candidate", "ice":
			// Support both "ice_candidate" (legacy) and "ice" (new protocol).
			relayMsg := msg
			relayMsg.Type = "ice_candidate"
			h.relay(relayMsg)

		case "announce_file":
			if msg.PeerID == "" || msg.Hash == "" {
				conn.WriteJSON(SignalingMessage{Type: "error", Message: "peer_id and hash required"})
				continue
			}
			h.mu.Lock()
			h.files[msg.Hash] = appendIfMissing(h.files[msg.Hash], msg.PeerID)
			h.mu.Unlock()
			log.LogInfo("signal: file announced: %s by %s", msg.Hash[:16], msg.PeerID[:8])

		case "find_file":
			h.mu.RLock()
			providers := h.files[msg.Hash]
			h.mu.RUnlock()
			conn.WriteJSON(SignalingMessage{
				Type:  "file_providers",
				Hash:  msg.Hash,
				Peers: providers,
			})

		default:
			log.LogDebug("signal: unknown message type: %s", msg.Type)
		}
	}
}

func (h *SignalingHub) register(peerID string, pc *peerConn) {
	h.mu.Lock()
	h.peers[peerID] = pc
	h.mu.Unlock()
}

func (h *SignalingHub) unregister(peerID string) {
	h.mu.Lock()
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

func (h *SignalingHub) broadcast(msg SignalingMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, p := range h.peers {
		p.send(msg)
	}
}

// broadcastRoom sends a message to all peers in a room except the sender.
func (h *SignalingHub) broadcastRoom(hash string, msg SignalingMessage, excludePeerID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[hash]
	for pid := range room {
		if pid == excludePeerID {
			continue
		}
		if p, ok := h.peers[pid]; ok {
			p.send(msg)
		}
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
