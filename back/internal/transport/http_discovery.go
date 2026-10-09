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
	"peerdrive/internal/model"
)

// PresenceRoom node-level "presence room" — all nodes with node-level interconnection
// enabled join it, so they can discover each other even without any shared content hash
// (the foundational capability of the interconnection layer).
//
// Aliased to model.PresenceRoom for backward compatibility across transport.
const PresenceRoom = model.PresenceRoom

// HTTPDiscovery room discovery via self-hosted signaling server (replaces MQTT public broker).
// Self-hosted servers naturally know all online nodes (all connect for signaling), so
// discovery becomes an HTTP query:
//   - announce: POST /discover/announce {peerId, collections, peers} (online + 30s heartbeat)
//   - discover: GET /discover/nodes?coll={hash} → online node list → onPeer callback interconnect
type HTTPDiscovery struct {
	baseURL     string // e.g. http://vps.moonchan.xyz:9000
	peerID      string
	collections []string
	onPeer      func(peerID string)
	peers       func() []string // current WebRTC direct peers, for signaling server to draw graph; can be nil

	client *http.Client
	mu     sync.Mutex
	seen   map[string]bool // already reported nodes (dedup, avoid duplicate onPeer)
	ctx    context.Context
	cancel context.CancelFunc

	// shareInfo this node's sharing summary (loadInfo.shares), for marketplace card display
	// "this node shares N collections/M files". **Only reports count, not hash**: announce
	// is broadcast via the discovery server; reporting hash equals publicly declaring "what
	// this node has". nil = sharing not enabled.
	shareInfo func() map[string]any
}

// SetShareInfo injects the sharing summary reader (must be called before Start, first
// announce uses it).
func (d *HTTPDiscovery) SetShareInfo(fn func() map[string]any) { d.shareInfo = fn }

// NewHTTPDiscovery creates the discovery component.
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

// Start announce + polling discovery (asynchronous).
func (d *HTTPDiscovery) Start() {
	go d.loop()
}

// Stop stops discovery.
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

// announce reports which collections this node is in and current direct peers (for graph).
func (d *HTTPDiscovery) announce() {
	body, _ := json.Marshal(map[string]any{
		"peerId":      d.peerID,
		"collections": d.collections,
		"peers":       d.peersList(),
		"nodeType":    "go-persistent",
		"loadInfo":    d.loadInfo(),
	})
	resp, err := d.client.Post(d.baseURL+"/discover/announce", "application/json", bytes.NewReader(body))
	if err != nil {
		log.LogDebug("discover: announce failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

// loadInfo reports this node's load/capability information (currently only sharing summary).
// Returns nil when not configured; JSON loadInfo is null, discovery server treats as "not
// reported".
func (d *HTTPDiscovery) loadInfo() map[string]any {
	if d.shareInfo == nil {
		return nil
	}
	return d.shareInfo()
}

func (d *HTTPDiscovery) peersList() []string {
	if d.peers == nil {
		return nil
	}
	return d.peers()
}

// discover queries collection online nodes and calls onPeer (with dedup).
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
		// M15: decode response limited to 256KB — when a compromised/abnormal discovery
		// server returns huge JSON, don't load the entire thing into memory
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
