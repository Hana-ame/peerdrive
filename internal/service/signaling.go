package service

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// SignalingMessage is the WS message format for WebRTC signaling.
type SignalingMessage struct {
	Type      string `json:"type"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	PeerID    string `json:"peer_id,omitempty"`
	Token     string `json:"token,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Hash      string `json:"hash,omitempty"`
	Peers     []string `json:"peers,omitempty"`
	Message   string `json:"message,omitempty"`
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

// SignalingHub manages WebRTC signaling between peers.
type SignalingHub struct {
	mu       sync.RWMutex
	peers    map[string]*peerConn // peerID -> connection
	files    map[string][]string  // hash -> []peerID (who has what file)
}

// NewSignalingHub creates a new signaling hub.
func NewSignalingHub() *SignalingHub {
	return &SignalingHub{
		peers: make(map[string]*peerConn),
		files: make(map[string][]string),
	}
}

// PeerCount returns the number of connected peers.
func (h *SignalingHub) PeerCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.peers)
}

// GetPeers returns all connected peer IDs.
func (h *SignalingHub) GetPeers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	peers := make([]string, 0, len(h.peers))
	for pid := range h.peers {
		peers = append(peers, pid)
	}
	return peers
}

// HandleConnection handles a new WebSocket signaling connection.
func (h *SignalingHub) HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[signal] upgrade error: %v", err)
		return
	}

	var peer *peerConn
	defer func() {
		if peer != nil && peer.PeerID != "" {
			h.unregister(peer.PeerID)
			h.broadcast(SignalingMessage{Type: "peer_left", PeerID: peer.PeerID})
			log.Printf("[signal] peer left: %s", peer.PeerID)
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
			msg.From = msg.PeerID
			conn.WriteJSON(SignalingMessage{Type: "registered", PeerID: msg.PeerID})
			h.broadcast(SignalingMessage{Type: "peer_joined", PeerID: msg.PeerID})
			log.Printf("[signal] peer registered: %s", msg.PeerID)

		case "request_peers":
			conn.WriteJSON(SignalingMessage{Type: "peers", Peers: h.GetPeers()})

		case "offer", "answer", "ice_candidate":
			h.relay(msg)

		case "announce_file":
			h.mu.Lock()
			h.files[msg.Hash] = appendIfMissing(h.files[msg.Hash], msg.PeerID)
			h.mu.Unlock()
			log.Printf("[signal] file announced: %s by %s", msg.Hash[:16], msg.PeerID)

		case "find_file":
			h.mu.RLock()
			providers := h.files[msg.Hash]
			h.mu.RUnlock()
			conn.WriteJSON(SignalingMessage{
				Type:  "file_providers",
				Hash:  msg.Hash,
				Peers: providers,
			})
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

func (h *SignalingHub) relay(msg SignalingMessage) {
	h.mu.RLock()
	target, ok := h.peers[msg.To]
	h.mu.RUnlock()
	if !ok {
		return
	}
	msg.From = msg.PeerID // the sender is the peer who sent this
	target.send(msg)
}

func (h *SignalingHub) broadcast(msg SignalingMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, peer := range h.peers {
		peer.send(msg)
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
