package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// HTTPDiscovery 自托管信令服务器的房间发现（替代 MQTT 公共 broker）。
// 自托管服务器天然知道所有在线节点（都连着它做信令），发现变成 HTTP 查询：
//   - announce：POST /discover/announce {peerId, collections, peers}（上线 + 30s 心跳）
//   - 发现：GET /discover/nodes?coll={hash} → 在线节点列表 → onPeer 回调互联
type HTTPDiscovery struct {
	baseURL     string // 如 http://vps.moonchan.xyz:9000
	peerID      string
	collections []string
	onPeer      func(peerID string)
	peers       func() []string // 当前 WebRTC 直连对端，供信令服务器画 graph；可为 nil

	client *http.Client
	mu     sync.Mutex
	seen   map[string]bool // 已上报过的节点（去重，避免重复 onPeer）
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHTTPDiscovery 创建发现组件。
func NewHTTPDiscovery(baseURL, peerID string, collections []string, onPeer func(peerID string), peers ...func() []string) *HTTPDiscovery {
	ctx, cancel := context.WithCancel(context.Background())
	var peersFn func() []string
	if len(peers) > 0 {
		peersFn = peers[0]
	}
	return &HTTPDiscovery{
		baseURL:     baseURL,
		peerID:      peerID,
		collections: collections,
		onPeer:      onPeer,
		peers:       peersFn,
		client:      &http.Client{Timeout: 10 * time.Second},
		seen:        make(map[string]bool),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start announce + 轮询发现（异步）。
func (d *HTTPDiscovery) Start() {
	go d.loop()
}

// Stop 停止。
func (d *HTTPDiscovery) Stop() {
	d.cancel()
}

func (d *HTTPDiscovery) loop() {
	d.announce()
	poll := time.NewTicker(10 * time.Second)
	hb := time.NewTicker(30 * time.Second)
	defer poll.Stop()
	defer hb.Stop()
	for {
		select {
		case <-poll.C:
			d.discover()
		case <-hb.C:
			d.announce()
		case <-d.ctx.Done():
			return
		}
	}
}

// announce 上报本节点在哪些集合以及当前直连对端（graph 用）。
func (d *HTTPDiscovery) announce() {
	body, _ := json.Marshal(map[string]any{
		"peerId":      d.peerID,
		"collections": d.collections,
		"peers":       d.peersList(),
		"nodeType":    "go-persistent",
	})
	resp, err := d.client.Post(d.baseURL+"/discover/announce", "application/json", bytes.NewReader(body))
	if err != nil {
		log.LogDebug("discover: announce failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

func (d *HTTPDiscovery) peersList() []string {
	if d.peers == nil {
		return nil
	}
	return d.peers()
}

// discover 查询集合在线节点并回调 onPeer（去重）。
func (d *HTTPDiscovery) discover() {
	for _, coll := range d.collections {
		resp, err := d.client.Get(d.baseURL + "/discover/nodes?coll=" + coll)
		if err != nil {
			log.LogDebug("discover: query failed: %v", err)
			return
		}
		var out struct {
			Nodes []struct {
				PeerID string `json:"peerId"`
			} `json:"nodes"`
		}
		// M15：解码响应限 256KB——被攻破/异常的发现服务器回巨大 JSON 时不整包入内存
		decErr := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&out)
		resp.Body.Close()
		if decErr != nil {
			continue
		}
		for _, n := range out.Nodes {
			if n.PeerID == "" || n.PeerID == d.peerID || len(n.PeerID) > 128 {
				continue
			}
			d.mu.Lock()
			if d.seen[n.PeerID] {
				d.mu.Unlock()
				continue
			}
			d.seen[n.PeerID] = true
			d.mu.Unlock()
			if d.onPeer != nil {
				d.onPeer(n.PeerID)
			}
		}
	}
}

var _ = fmt.Sprintf
