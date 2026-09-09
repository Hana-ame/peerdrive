// media-node — peerdrive 的 standalone 媒体节点（先行验证版）。
//
// 定位：peerdrive 其余部分尚未实现，本模块单独先行——一个 Go 二进制，
// 注册到 PeerJS 信令服务器，接收浏览器 WebRTC 连接，**只提供
// video-cf.twimg.com 的内容访问**。整条链路内置在本二进制内，
// 不监听任何额外端口，不依赖任何外部代理进程。
//
// 访问方式：**直接集成 ECH（Encrypted Client Hello）**。
//   - 浏览器请求 video-cf.twimg.com 的真实 URL 原样发来
//   - TCP 连 cloudflare-ech.com 外壳（该域名不被墙）
//   - TLS 握手时用 ECH 加密的 ClientHello 携带真实目标域名
//     （video-cf.twimg.com），Cloudflare 边缘据此路由到 twitter CDN
//   - GFW 只见外壳域名的明文 SNI，放行
//   - ECH 域前置只对 Cloudflare 托管的域名有效（video-cf.twimg.com 正是）
//
// 用法：
//   go run ./cmd/media-node [--peer-id media-node]
//   本地需走代理：HTTPS_PROXY=http://172.29.80.1:10809 go run ./cmd/media-node
//
// 帧协议（与浏览器端 peerdrive-media 一致，DataChannel 上 JSON 文本帧 +
// 二进制块）：
//   浏览器 → node: {"type":"url","url":"https://video-cf.twimg.com/...","reqId":"1"}
//   node  → 浏览器: {"type":"meta","status":200,"mime":"video/mp4","size":N,"reqId":"1"}
//   node  → 浏览器: <二进制块 × N>
//   node  → 浏览器: {"type":"done","reqId":"1"}
//   node  → 浏览器: {"type":"err","msg":"...","reqId":"1"}
//   keepalive：node 每 5s 发 {"type":"ping"}，浏览器回 {"type":"ping-ack"}
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

// twimgHost 唯一允许访问的后端目标（写死，不接受其它域名）。
const twimgHost = "video-cf.twimg.com"

// twimgURLPrefix 允许的 URL 前缀（https://video-cf.twimg.com/）。
const twimgURLPrefix = "https://" + twimgHost + "/"

// Msg 协议帧。
type Msg struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	ReqID  string `json:"reqId,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Status int    `json:"status,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

// guessMime 按 Content-Type 或扩展名决定媒体类型（浏览器端 mount 分派 img/video 用）。
func guessMime(url, contentType string) string {
	if contentType != "" {
		return contentType
	}
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(lower, ".png"):
		return "image/png"
	case strings.Contains(lower, ".jpg") || strings.Contains(lower, ".jpeg"):
		return "image/jpeg"
	case strings.Contains(lower, ".gif"):
		return "image/gif"
	case strings.Contains(lower, ".webp"):
		return "image/webp"
	case strings.Contains(lower, ".mp4"):
		return "video/mp4"
	case strings.Contains(lower, ".webm"):
		return "video/webm"
	case strings.Contains(lower, ".mp3"):
		return "audio/mpeg"
	}
	return "application/octet-stream"
}

// fetchTwimg 经内置 ECH 客户端直接访问 video-cf.twimg.com。
// twitter CDN 防盗链要求 Referer: https://x.com，且校验 User-Agent。
func fetchTwimg(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://x.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	return ech.Do(req)
}

// serveRequest 处理一个媒体请求：校验域名 → ECH 抓取 → meta → 64KB 块 × N → done。
func serveRequest(conn *peerjs.Connection, msg Msg, chunkSize int) {
	reqID := msg.ReqID
	url := msg.URL
	if url == "" {
		_ = conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: "url required"})
		return
	}
	// 只允许 video-cf.twimg.com（写死，防 SSRF 到其它后端）
	if !strings.HasPrefix(url, twimgURLPrefix) {
		_ = conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: "only " + twimgURLPrefix + " allowed"})
		return
	}
	log.Printf("[req %s] %s", reqID, url)

	resp, err := fetchTwimg(url)
	if err != nil {
		_ = conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: "fetch failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		_ = conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: fmt.Sprintf("upstream %d", resp.StatusCode)})
		return
	}

	mime := guessMime(url, resp.Header.Get("Content-Type"))
	size := resp.ContentLength // -1 = 未知（流式无 content-length）
	_ = conn.SendJSON(Msg{Type: "meta", Status: resp.StatusCode, Mime: mime, Size: size, ReqID: reqID})

	// 流式分块发送（pion 的 Connection.SendFrame 已内置低水位流控）
	buf := make([]byte, chunkSize)
	sent := int64(0)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if serr := conn.Send(buf[:n]); serr != nil {
				return
			}
			sent += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: "read failed: " + err.Error()})
			return
		}
	}
	_ = conn.SendJSON(Msg{Type: "done", ReqID: reqID})
	log.Printf("[req %s] done, %d bytes", reqID, sent)
}

// main 仅解析信令参数并运行媒体节点——不监听任何 HTTP 端口。
func main() {
	peerID := "media-node"
	host := "peersignal.moonchan.xyz"
	port := "443"
	secure := true
	key := "pd-signal-b9447b406828e500"
	chunkSize := 64 * 1024 // 64KB
	proxyURL := ""         // 空则读 HTTPS_PROXY 环境变量

	flag.StringVar(&peerID, "peer-id", peerID, "PeerJS peer id（浏览器连接用）")
	flag.StringVar(&host, "host", host, "信令服务器 host")
	flag.StringVar(&port, "port", port, "信令服务器 port")
	flag.BoolVar(&secure, "secure", secure, "信令走 TLS")
	flag.StringVar(&key, "key", key, "信令 API key")
	flag.IntVar(&chunkSize, "chunk-size", chunkSize, "数据块大小（字节）")
	flag.StringVar(&proxyURL, "proxy", proxyURL, "HTTP 代理（默认读 HTTPS_PROXY）")
	flag.Parse()

	// 初始化内置 ECH 客户端（DoH 拉取 cloudflare-ech.com 的 ECH 配置）
	if err := ech.InitDefault(ech.Config{ProxyURL: proxyURL}); err != nil {
		log.Fatalf("ECH init failed: %v", err)
	}
	log.Printf("ECH 就绪（仅访问 %s，无外部代理、无额外端口）", twimgHost)

	// 注册到信令服务器
	opts := peerjs.Options{
		ID:           peerID,
		Host:         host,
		Port:         port,
		Secure:       secure,
		Key:          key,
		PingInterval: 5 * time.Second,
	}
	peer := peerjs.NewPeer(peerID, opts)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := peer.Dial(ctx); err != nil {
		log.Fatalf("信令连接失败: %v", err)
	}
	log.Printf("已注册到 %s:%s，peer id=%s（浏览器 PeerMedia 连这个 id）", host, port, peerID)

	// 处理浏览器连接
	peer.OnConnection(func(conn *peerjs.Connection) {
		log.Printf("浏览器连接: %s (label=%s)", conn.PeerID, conn.Label)

		conn.OnMessage(func(frame peerjs.Frame) {
			if !frame.IsText {
				return // 二进制帧由浏览器端发出（本模块只收文本）
			}
			var msg Msg
			if err := json.Unmarshal(frame.Data, &msg); err != nil || msg.Type == "" {
				return
			}
			switch msg.Type {
			case "url":
				go serveRequest(conn, msg, chunkSize)
			case "ping-ack":
				// keepalive 响应，忽略
			}
		})

		conn.OnClose(func(*peerjs.Connection) {
			log.Printf("浏览器连接关闭: %s", conn.PeerID)
		})
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	peer.Close()
	log.Println("已退出")
}