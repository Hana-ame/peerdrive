// echclient — validation client for media-node (temporary tool).
// Connects to media-node on peersignal.moonchan.xyz, sends a URL request,
// and verifies the full ECH path to twimg (signaling → WebRTC → ECH → twimg).
// Usage: go run ./cmd/echclient
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
)

type Frame struct {
	Type   string `json:"type"`
	URL    string `json:"url,omitempty"`
	ReqID  string `json:"reqId,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Status int    `json:"status,omitempty"`
	Msg    string `json:"msg,omitempty"`
}

func main() {
	urlFlag := flag.String("url", "https://video-cf.twimg.com/ext_tw_video/example.mp4", "URL to fetch (media-node only allows video-cf.twimg.com)")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	opts := peerjs.DefaultOptions()
	opts.Host = "peersignal.moonchan.xyz"
	opts.Port = "443"
	opts.Secure = true
	opts.Key = "pd-signal-1edf5e05e4a52b7351392574"

	peer := peerjs.NewPeer(fmt.Sprintf("echclient-%d", time.Now().UnixNano()%100000), opts)
	if err := peer.Dial(ctx); err != nil {
		log.Fatalf("dial: %v", err)
	}
	log.Printf("connected as %s", peer.ID())

	conn, err := peer.Connect(ctx, "media-node", "media")
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	opened := make(chan struct{})
	conn.OnOpen(func(c *peerjs.Connection) { close(opened) })
	select {
	case <-opened:
		log.Printf("data channel open")
	case <-time.After(30 * time.Second):
		log.Fatalf("open timeout")
	}

	url := *urlFlag
	reqID := "t1"
	conn.SendJSON(Frame{Type: "url", URL: url, ReqID: reqID})
	log.Printf("sent url request %s -> %s", reqID, url)

	total := 0
	done := make(chan struct{})
	conn.OnMessage(func(f peerjs.Frame) {
		if f.IsText {
			var resp Frame
			if err := json.Unmarshal(f.Data, &resp); err != nil {
				return
			}
			log.Printf("<< %s reqId=%s mime=%s size=%d msg=%s", resp.Type, resp.ReqID, resp.Mime, resp.Size, resp.Msg)
			if resp.Type == "done" || resp.Type == "err" {
				close(done)
			}
		} else {
			total += len(f.Data)
		}
	})

	select {
	case <-done:
		log.Printf("finished, received %d bytes", total)
	case <-time.After(60 * time.Second):
		log.Printf("TIMEOUT, received %d bytes", total)
	}
}
