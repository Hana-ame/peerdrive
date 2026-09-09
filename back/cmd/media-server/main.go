// peerdrive-media 资源代理服务器（Go 版，集成 ECH 直接访问）
//
// 职责：
//   1. 连接 PeerJS 信令服务器
//   2. 接收浏览器 WebRTC 连接
//   3. 接收 url 请求，经 **ECH 域前置** 直接 fetch 目标文件
//      （不依赖 ech-proxy 独立进程——本二进制内置 ECH 客户端）
//   4. 通过 DataChannel 分块发送回浏览器
//
// ECH（Encrypted Client Hello）：
//   浏览器请求的真实 URL（如 https://video-cf.twimg.com/xxx）原样发给本节点；
//   TCP 连 cloudflare-ech.com 外壳（不被墙），TLS ECH 加密的 ClientHello
//   内含真实目标域名（video-cf.twimg.com），Cloudflare 边缘路由过去。
//   GFW 只看到外壳域名的明文 SNI，放行。
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
	"peerdrive/ech"
)

// Msg 协议消息
type Msg struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	ReqID  string `json:"reqId,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Status int    `json:"status,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

// guessMime 根据 URL 和 Content-Type 猜测 MIME（无 Content-Type 时按扩展名）。
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

// fetchViaECH 经 ECH 域前置 fetch URL。
// referer 非空时附加（防盗链，如 twitter 需要 https://x.com）。
func fetchViaECH(url, referer string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	// User-Agent：twitter CDN 有 UA 校验，填浏览器 UA
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	return ech.Do(req)
}

// handleRequest 处理文件请求
func handleRequest(ctx context.Context, conn *peerjs.Connection, msg Msg, chunkSize int) error {
	url := msg.URL
	reqID := msg.ReqID
	log.Printf("[req %s] url=%s", reqID, url)

	// 经 ECH 直接访问目标（video-cf.twimg.com 等 twitter CDN）
	resp, err := fetchViaECH(url, "https://x.com")
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

	// 分块发送数据（64KB 块，对流式大文件友好）
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
	// 默认配置（生产默认：peersignal.moonchan.xyz + ECH 直连）
	peerID := "media-node"
	host := "peersignal.moonchan.xyz"
	port := "443"
	secure := true
	key := "pd-signal-b9447b406828e500"
	chunkSize := 64 * 1024 // 64KB
	statusAddr := ":9001"
	proxyURL := "" // 空则读 HTTPS_PROXY 环境变量（本地测试需要走代理）

	// 解析命令行参数
	flag.StringVar(&peerID, "peer-id", peerID, "Peer ID")
	flag.StringVar(&host, "host", host, "Signaling host")
	flag.StringVar(&port, "port", port, "Signaling port")
	flag.BoolVar(&secure, "secure", secure, "Use HTTPS for signaling")
	flag.StringVar(&key, "key", key, "PeerJS API key")
	flag.IntVar(&chunkSize, "chunk-size", chunkSize, "Chunk size in bytes")
	flag.StringVar(&statusAddr, "status-addr", statusAddr, "Status API listen address")
	flag.StringVar(&proxyURL, "proxy", proxyURL, "HTTP proxy for ECH (default: HTTPS_PROXY env)")
	flag.Parse()

	// 初始化 ECH 客户端（内置，不依赖外部 ech-proxy 进程）
	echCfg := ech.Config{ProxyURL: proxyURL}
	if err := ech.InitDefault(echCfg); err != nil {
		log.Printf("ECH init failed (将只用普通 fetch 兜底): %v", err)
	} else {
		log.Printf("ECH 客户端就绪（直接访问 twitter CDN，无需 ech-proxy）")
	}

	// 创建 PeerJS 客户端
	opts := peerjs.Options{
		ID:           peerID,
		Host:         host,
		Port:         port,
		Secure:       secure,
		Key:          key,
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
				// 直接访问，无白名单限制
				go handleRequest(context.Background(), conn, msg, chunkSize)
			case "ping-ack":
				// keepalive 响应，忽略
			}
		})

		conn.OnClose(func(*peerjs.Connection) {
			log.Printf("Connection closed from %s", conn.PeerID)
		})
	})

	// 状态 API
	go func() {
		http.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"peerId": peerID,
				"status": "running",
				"ech":    true,
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