// WebSocket 文件传输中枢 — 通过 WebSocket 连接实现文件请求/响应的广播和点对点传输。
package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"peerdrive/internal/log"
	"peerdrive/internal/repository"

	"github.com/gorilla/websocket"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type wsMsg struct {
	Type    string `json:"type"`
	Hash    string `json:"hash,omitempty"`
	Size    int    `json:"size,omitempty"`
	Message string `json:"message,omitempty"`
}

type wsHub struct {
	mu    sync.RWMutex
	conns map[*wsConn]struct{}
}

type wsConn struct {
	conn *websocket.Conn
	hub  *wsHub
	mu   sync.Mutex
}

func newWSHub() *wsHub {
	return &wsHub{
		conns: make(map[*wsConn]struct{}),
	}
}

func (h *wsHub) add(c *wsConn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *wsHub) remove(c *wsConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

func (h *wsHub) count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

func (h *wsHub) broadcast(msg wsMsg, exclude *wsConn) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.conns {
		if c == exclude {
			continue
		}
		c.writeJSON(msg)
	}
}

func (c *wsConn) writeJSON(v interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.WriteJSON(v)
}

func (c *wsConn) writeBinary(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (p *P2PService) WSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			log.LogDebug("ws upgrade failed: %v", err)
			return
		}

		wsc := &wsConn{conn: conn, hub: p.wsHub}
		p.wsHub.add(wsc)
		defer p.wsHub.remove(wsc)
		defer conn.Close()

		for {
			_, msgBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msg wsMsg
			if err := json.Unmarshal(msgBytes, &msg); err != nil {
				wsc.writeJSON(wsMsg{Type: "error", Message: "invalid json"})
				continue
			}

			switch msg.Type {
			case "request":
				if msg.Hash == "" || len(msg.Hash) != 64 {
					wsc.writeJSON(wsMsg{Type: "error", Hash: msg.Hash, Message: "invalid hash"})
					continue
				}

				log.LogDebug("ws request: hash=%s", msg.Hash)
				data := p.lookupFile(msg.Hash)
				if data == nil {
					if peers := p.GetConnectedPeers(); len(peers) > 0 {
						p.wsHub.broadcast(wsMsg{Type: "request", Hash: msg.Hash}, wsc)
					}
					wsc.writeJSON(wsMsg{Type: "error", Hash: msg.Hash, Message: "not found locally"})
					continue
				}

				dataHash := sha256Hex(data)
				if dataHash != msg.Hash {
					wsc.writeJSON(wsMsg{Type: "error", Hash: msg.Hash, Message: "hash mismatch"})
					continue
				}

				wsc.writeJSON(wsMsg{Type: "response", Hash: msg.Hash, Size: len(data)})
				wsc.writeBinary(data)

			case "response":
				p.wsHub.broadcast(msg, nil)

			case "ping":
				wsc.writeJSON(wsMsg{Type: "pong"})

			default:
				wsc.writeJSON(wsMsg{Type: "error", Message: fmt.Sprintf("unknown message type: %s", msg.Type)})
			}
		}
	}
}

func (p *P2PService) lookupFile(hash string) []byte {
	filePath := filepath.Join(p.storageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err == nil {
		return data
	}

	meta, _ := repository.GetFileMeta(hash)
	if meta != nil {
		providers, _ := repository.GetFileProviders(hash)
		for _, prov := range providers {
			if prov.ProviderType == "local" {
				data, err = os.ReadFile(prov.Path)
				if err == nil {
					return data
				}
			}
		}
	}

	return nil
}

func (p *P2PService) WSCount() int {
	return p.wsHub.count()
}
