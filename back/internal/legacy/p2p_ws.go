// WebSocket 文件传输中枢 — 通过 WebSocket 连接实现文件请求/响应的广播和点对点传输。
package legacy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	// 低危 4 修复：快照后解锁再写——之前持 RLock 做阻塞写（writeJSON 无超时），
	// 慢客户端会卡住 hub 的 add/remove/count 所有操作
	h.mu.RLock()
	conns := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()
	for _, c := range conns {
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

// WSHandler 返回 WebSocket 文件传输的 HTTP 处理函数，支持请求/响应/广播消息。
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
				// M4 修复：流式发送——之前 lookupFile 整文件驻留内存。
				// 协议不变（response JSON + 紧随的单个二进制消息），
				// 数据面用 NextWriter + io.Copy 直接从文件写
				path, size, err := p.lookupFilePath(msg.Hash)
				if err != nil {
					if peers := p.GetConnectedPeers(); len(peers) > 0 {
						p.wsHub.broadcast(wsMsg{Type: "request", Hash: msg.Hash}, wsc)
					}
					wsc.writeJSON(wsMsg{Type: "error", Hash: msg.Hash, Message: "not found locally"})
					continue
				}

				f, err := os.Open(path)
				if err != nil {
					wsc.writeJSON(wsMsg{Type: "error", Hash: msg.Hash, Message: "not found locally"})
					continue
				}
				wsc.writeJSON(wsMsg{Type: "response", Hash: msg.Hash, Size: int(size)})
				// 单次二进制消息直接流式写入（NextWriter 避免整块驻留内存）
				w, err := wsc.conn.NextWriter(websocket.BinaryMessage)
				if err != nil {
					f.Close()
					return
				}
				hasher := sha256.New()
				_, err = io.Copy(io.MultiWriter(w, hasher), f)
				_ = w.Close()
				f.Close()
				if err != nil {
					return
				}
				if hex.EncodeToString(hasher.Sum(nil)) != msg.Hash {
					wsc.writeJSON(wsMsg{Type: "error", Hash: msg.Hash, Message: "hash mismatch"})
					continue
				}

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

// lookupFilePath 查找 hash 对应的本地文件路径（内容寻址存储或 provider 登记），
// 不读内容——配合 WSHandler 的 NextWriter 流式发送（M4）。
func (p *P2PService) lookupFilePath(hash string) (string, int64, error) {
	filePath := filepath.Join(p.storageDir, hash[:2], hash)
	if info, err := os.Stat(filePath); err == nil {
		return filePath, info.Size(), nil
	}

	meta, _ := repository.GetFileMeta(hash)
	if meta != nil {
		providers, _ := repository.GetFileProviders(hash)
		for _, prov := range providers {
			if prov.ProviderType == "local" {
				if info, err := os.Stat(prov.Path); err == nil {
					return prov.Path, info.Size(), nil
				}
			}
		}
	}

	return "", 0, fmt.Errorf("file %s not found", hash)
}

// WSCount 返回当前 WebSocket 连接数。
func (p *P2PService) WSCount() int {
	return p.wsHub.count()
}
