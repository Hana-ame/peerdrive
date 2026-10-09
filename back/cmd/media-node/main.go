// media-node — standalone media node for peerdrive (early verification build).
//
// Role: the rest of peerdrive is not yet implemented, so this module runs
// independently — a single Go binary that registers with the PeerJS signaling
// server, accepts browser WebRTC connections, and **only provides access to
// video-cf.twimg.com content**. The entire pipeline is built into this binary;
// it listens on no extra ports and depends on no external proxy processes.
//
// Access method: **direct ECH (Encrypted Client Hello) integration**.
//   - The browser sends the real URL of video-cf.twimg.com as-is
//   - TCP connects to the cloudflare-ech.com shell domain (not blocked)
//   - During the TLS handshake, the ECH-encrypted ClientHello carries the
//     real target domain (video-cf.twimg.com); the Cloudflare edge routes
//     to the Twitter CDN accordingly
//   - The GFW only sees the plaintext SNI of the shell domain and allows it
//   - ECH domain fronting works only for Cloudflare-hosted domains
//     (video-cf.twimg.com qualifies)
//
// Usage:
//   go run ./cmd/media-node [--peer-id media-node]
//   Local proxy required: HTTPS_PROXY=http://172.29.80.1:10809 go run ./cmd/media-node
//
// Frame protocol (consistent with the browser-side peerdrive-media;
// DataChannel JSON text frames + binary chunks):
//   browser → node: {"type":"url","url":"https://video-cf.twimg.com/...","reqId":"1"}
//   node  → browser: {"type":"meta","status":200,"mime":"video/mp4","size":N,"reqId":"1"}
//   node  → browser: <binary chunk × N>
//   node  → browser: {"type":"done","reqId":"1"}
//   node  → browser: {"type":"err","msg":"...","reqId":"1"}
//   keepalive: the node **proactively** sends {"type":"ping"} every 5s
//   (to all connections); the peer replies with {"type":"ping-ack"};
//   if no frame arrives within 15s, the node disconnects.
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Hana-ame/go-peerjs"
	"peerdrive/internal/echcore"
)

// twimgHost is the only backend host allowed (hardcoded; no other domains accepted).
const twimgHost = "video-cf.twimg.com"

// twimgURLPrefix is the allowed URL prefix (https://video-cf.twimg.com/).
const twimgURLPrefix = "https://" + twimgHost + "/"

// Msg is the protocol frame.
type Msg struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	ReqID  string `json:"reqId,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Status int    `json:"status,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

// guessMime determines the media type from Content-Type or file extension
// (used by the browser-side mount dispatch for img/video).
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

// fetchTwimg accesses video-cf.twimg.com via the built-in ECH client.
// The Twitter CDN hotlink protection requires Referer: https://x.com and validates User-Agent.
func fetchTwimg(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://x.com")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	return echcore.Do(req)
}

// serveRequest handles a single media request: validate domain → ECH fetch → meta → 64KB chunks × N → done.
func serveRequest(conn *peerjs.Connection, msg Msg, chunkSize int) {
	reqID := msg.ReqID
	url := msg.URL
	if url == "" {
		_ = conn.SendJSON(Msg{Type: "err", ReqID: reqID, Msg: "url required"})
		return
	}
	// Only video-cf.twimg.com is allowed (hardcoded to prevent SSRF to other backends)
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
	size := resp.ContentLength // -1 = unknown (streaming with no content-length)
	_ = conn.SendJSON(Msg{Type: "meta", Status: resp.StatusCode, Mime: mime, Size: size, ReqID: reqID})

	// Stream chunked transfer (pion's Connection.SendFrame has built-in low-water flow control)
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

// main only parses signaling parameters and runs the media node — no HTTP ports are listened on.
func main() {
	peerID := "media-node"
	host := "peersignal.moonchan.xyz"
	port := "443"
	secure := true
	key := "pd-signal-1edf5e05e4a52b7351392574"
	chunkSize := 64 * 1024 // 64KB
	proxyURL := ""         // empty means read from HTTPS_PROXY env var
	ipMode := ""           // empty means auto (use OS family)

	flag.StringVar(&peerID, "peer-id", peerID, "PeerJS peer id (for browser connection)")
	flag.StringVar(&host, "host", host, "Signaling server host")
	flag.StringVar(&port, "port", port, "Signaling server port")
	flag.BoolVar(&secure, "secure", secure, "Signaling over TLS")
	flag.StringVar(&key, "key", key, "Signaling API key")
	flag.IntVar(&chunkSize, "chunk-size", chunkSize, "Data chunk size (bytes)")
	flag.StringVar(&proxyURL, "proxy", proxyURL, "HTTP proxy (defaults to HTTPS_PROXY)")
	flag.StringVar(&ipMode, "ip-mode", ipMode, "IP family preference: v4, v6, or auto")
	flag.Parse()

	// Initialize the built-in ECH client (DoH fetches the ECH config for cloudflare-ech.com)
	if err := echcore.InitDefault(echcore.Config{ProxyURL: proxyURL, IPMode: ipMode}); err != nil {
		log.Fatalf("ECH init failed: %v", err)
	}
	log.Printf("ECH ready (accesses only %s, no external proxy, no extra ports, ip_mode=%s)", twimgHost, ipMode)

	// Register with the signaling server
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
		log.Fatalf("signaling connection failed: %v", err)
	}
	log.Printf("registered with %s:%s, peer id=%s (browser PeerMedia connects to this id)", host, port, peerID)

	// Handle browser connections
	peer.OnConnection(func(conn *peerjs.Connection) {
		log.Printf("browser connection: %s (label=%s)", conn.PeerID, conn.Label)

		// Keepalive: this side **proactively** sends pings (user requirement, to all connections).
		// Each DataChannel is independent: send {"type":"ping"} every 5s to generate traffic;
		// any incoming frame refreshes lastActive; if no frame arrives within 15s → disconnect
		// (in environments without STUN, WebRTC disconnection has no close event; timeout detection is required).
		var lastActive atomic.Int64
		lastActive.Store(time.Now().UnixMilli())
		kaStop := make(chan struct{})
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-kaStop:
					return
				case <-ticker.C:
				}
				if time.Now().UnixMilli()-lastActive.Load() > 15000 {
					log.Printf("keepalive timeout, disconnecting %s (label=%s)", conn.PeerID, conn.Label)
					conn.Close()
					return
				}
				// Proactively send ping (peer replies with ping-ack, refreshing lastActive)
				if err := conn.SendJSON(Msg{Type: "ping"}); err != nil {
					conn.Close()
					return
				}
			}
		}()

		conn.OnMessage(func(frame peerjs.Frame) {
			// Any frame (text/binary) refreshes activity — the peer is still alive, connection is not dead
			lastActive.Store(time.Now().UnixMilli())
			if !frame.IsText {
				return // Binary frames are sent by the browser (this module only receives text)
			}
			var msg Msg
			if err := json.Unmarshal(frame.Data, &msg); err != nil || msg.Type == "" {
				return
			}
			switch msg.Type {
			case "url":
				go serveRequest(conn, msg, chunkSize)
			case "ping-ack":
				// Keepalive response (lastActive already refreshed)
			}
		})

		conn.OnClose(func(*peerjs.Connection) {
			close(kaStop)
			log.Printf("browser connection closed: %s", conn.PeerID)
		})
	})

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	peer.Close()
	log.Println("exited")
}
