// peerdrive-media 资源代理服务器（Go 版）
//
// 职责：
//   1. 连接 PeerJS 信令服务器
//   2. 接收浏览器 WebRTC 连接
//   3. 接收 url 请求，fetch 文件
//   4. 通过 DataChannel 分块发送回浏览器
//
// 用法：
//   go run ./cmd/media-server --peer-id media-node
//
// 协议（JSON 帧）：
//   url:     {"type":"url","url":"https://...","reqId":"1"}
//   meta:    {"type":"meta","status":200,"mime":"image/png","size":1234,"reqId":"1"}
//   done:    {"type":"done","reqId":"1"}
//   err:     {"type":"err","msg":"...","reqId":"1"}
//   ping:    {"type":"ping"}
//   ping-ack:{"type":"ping-ack"}
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Hana-ame/go-peerjs"
)

// Msg 协议消息
type Msg struct {
	Type  string `json:"type"`
	URL   string `json:"url,omitempty"`
	ReqID string `json:"reqId,omitempty"`
	Mime  string `json:"mime,omitempty"`
	Size  int64  `json:"size,omitempty"`
	Status int   `json:"status,omitempty"`
	Msg   string `json:"msg,omitempty"`
}

// allowPrefix 检查 URL 是否在白名单内
func allowPrefix(prefixes []string) func(string) bool {
	return func(url string) bool {
		for _, prefix := range prefixes {
			if strings.HasPrefix(url, prefix) {
				return true
			}
		}
		return false
	}
}

// guessMime 根据 URL 和 Content-Type 猜测 MIME
func guessMime(url, contentType string) string {
	if contentType != "" {
		return contentType
	}
	lower := strings.ToLower(url)
	if strings.Contains(lower, ".png") {
		return "image/png"
	}
	if strings.Contains(lower, ".jpg") || strings.Contains(lower, ".jpeg") {
		return "image/jpeg"
	}
	if strings.Contains(lower, ".gif") {
		return "image/gif"
	}
	if strings.Contains(lower, ".webp") {
		return "image/webp"
	}
	if strings.Contains(lower, ".svg") {
		return "image/svg+xml"
	}
	if strings.Contains(lower, ".mp4") {
		return "video/mp4"
	}
	if strings.Contains(lower, ".webm") {
		return "video/webm"
	}
	if strings.Contains(lower, ".mp3") {
		return "audio/mpeg"
	}
	return "application/octet-stream"
}

// handleRequest 处理文件请求
func handleRequest(ctx context.Context, conn *peerjs.Connection, msg Msg, chunkSize int) error {
	url := msg.URL
	reqID := msg.ReqID
	log.Printf("[req %s] url=%s", reqID, url)

	// fetch 文件
	resp, err := http.Get(url)
	if err != nil {
		conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: fmt.Sprintf("fetch failed: %v", err)})
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: fmt.Sprintf("upstream %d", resp.StatusCode)})
		return fmt.Errorf("upstream %d", resp.StatusCode)
	}

	// 发送 meta
	mime := guessMime(url, resp.Header.Get("Content-Type"))
	size := resp.ContentLength
	conn.SendJSON(Msg{
		Type:   "meta",
		ReqID:  reqID,
		Status: resp.StatusCode,
		Mime:   mime,
		Size:   size,
	})

	// 分块发送数据
	buf := make([]byte, chunkSize)
	totalSent := int64(0)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if err := conn.Send(buf[:n]); err != nil {
				return err
			}
			totalSent += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	// 发送 done
	conn.SendJSON(Msg{Type: "done", ReqID: reqID})

	log.Printf("[req %s] done, sent %d bytes", reqID, totalSent)
	return nil
}

func main() {
	// 默认配置
	peerID := "media-node"
	host := "peersignal.moonchan.xyz"
	port := "443"
	secure := true
	key := "pd-signal-b9447b406828e500"
	allowPrefixes := "https://twimg.l.moonchan.xyz/"
	chunkSize := 64 * 1024 // 64KB
	statusAddr := ":9001"

	// 解析命令行参数
	flag.StringVar(&peerID, "peer-id", peerID, "Peer ID")
	flag.StringVar(&host, "host", host, "Signaling host")
	flag.StringVar(&port, "port", port, "Signaling port")
	flag.BoolVar(&secure, "secure", secure, "Use HTTPS for signaling")
	flag.StringVar(&key, "key", key, "PeerJS API key")
	flag.StringVar(&allowPrefixes, "allow", allowPrefixes, "Allowed URL prefixes (comma-separated)")
	flag.IntVar(&chunkSize, "chunk-size", chunkSize, "Chunk size in bytes")
	flag.StringVar(&statusAddr, "status-addr", statusAddr, "Status API listen address")
	flag.Parse()

	allow := allowPrefix(strings.Split(allowPrefixes, ","))

	// 创建 PeerJS 客户端
	opts := peerjs.Options{
		ID:     peerID,
		Host:   host,
		Port:   port,
		Secure: secure,
		Key:    key,
		PingInterval: 5 * time.Second,
	}

	peer := peerjs.NewPeer(peerID, opts)

	// 连接信令
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := peer.Dial(ctx); err != nil {
		log.Fatalf("Failed to connect to signaling: %v", err)
	}
	log.Printf("Connected to signaling: %s:%s", host, port)

	// 处理连接
	peer.OnConnection(func(conn *peerjs.Connection) {
		log.Printf("New connection from %s", conn.PeerID)

		// 处理消息
		conn.OnMessage(func(frame peerjs.Frame) {
			if !frame.IsText {
				return // 忽略二进制帧（浏览器不应发送）
			}

			var msg Msg
			if err := json.Unmarshal(frame.Data, &msg); err != nil {
				log.Printf("Invalid frame: %s", string(frame.Data))
				return
			}

			switch msg.Type {
			case "url":
				if !allow(msg.URL) {
					conn.SendJSON(Msg{Type: "err", ReqID: msg.ReqID, Msg: "url not allowed"})
					return
				}
				go handleRequest(context.Background(), conn, msg, chunkSize)
			case "ping-ack":
				// keepalive 响应，忽略
			}
		})

		// 处理关闭
		conn.OnClose(func(*peerjs.Connection) {
			log.Printf("Connection closed from %s", conn.PeerID)
		})
	})

	// 启动状态 API
	go func() {
		http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"peerId": peerID,
				"status": "running",
			})
		})
		log.Printf("Status API listening on %s", statusAddr)
		http.ListenAndServe(statusAddr, nil)
	}()

	// 等待退出信号
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down...")
	peer.Close()
}
