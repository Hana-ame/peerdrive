package service

// node_directory.go: Node marketplace directory (doc/NETDISK.md M1).
//
// Target capability: the frontend "Market" page must list other nodes, with join and leave actions.
// After joining, the node becomes a persistent peer of this node (auto-reconnects after restart).
//
// Three data sources are merged into one marketplace entry:
//  1. Discovery server /discover/nodes (online + nodeType + share summary loadInfo)
//  2. This node's current WebRTC direct connection table (connected)
//  3. Local joined_nodes.json (nodes the operator has joined; offline entries are retained --
//     otherwise after restart everything seen in the market would be lost, and users would think
//     "the nodes I added are gone")
//
// Why joined uses a standalone JSON file instead of SQLite: this is a small operator preference
// (peerId + timestamp), and it must work in no-DB scenarios (pure client mode / unit tests / CI).
// Atomic write (temp file + rename) ensures no partial file remains when the process is killed.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

// joinedFileName is the relative filename of the joined list (located under storageDir).
const joinedFileName = "joined_nodes.json"

// joinedFile is the disk format. Wrapped in an object rather than a bare array: future fields
// (notes, aliases, share snapshot at join time) won't require changing the top-level structure.
type joinedFile struct {
	Peers []joinedPeer `json:"peers"`
}

type joinedPeer struct {
	PeerID   string    `json:"peer_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// discoveredNode is a single response from the discovery server /discover/nodes.
// Fields align with back/signalserver's NodeInfo; missing fields are tolerated (online signaling
// is maintained externally and cannot be assumed to always return nodeType/loadInfo).
type discoveredNode struct {
	PeerID      string         `json:"peerId"`
	LastSeen    int64          `json:"lastSeen"`
	NodeType    string         `json:"nodeType"`
	Uptime      int64          `json:"uptime"`
	Collections []string       `json:"collections"`
	LoadInfo    map[string]any `json:"loadInfo"`
}

// NodeDirectory is the node marketplace directory service.
type NodeDirectory struct {
	discoverURL string
	path        string // absolute path of joined_nodes.json

	selfID    func() string
	connected func() map[string]bool
	dial      func(peerID string)
	// shareSummary provides this node's share summary (injected by M2's NodeShare service; nil = sharing not enabled)
	shareSummary func() model.NodeShares

	http *http.Client

	mu     sync.Mutex
	joined map[string]time.Time // peerID -> join time
}

// NewNodeDirectory creates the directory service and loads the joined list.
// When storageDir is empty, falls back to the current directory (consistent with storage error
// tolerance everywhere: better to write to the wrong place than to fail startup). When discoverURL
// is empty, there is no discovery server -- the marketplace can only see joined nodes (no error,
// frontend shows empty state + hint).
func NewNodeDirectory(storageDir, discoverURL string) *NodeDirectory {
	if storageDir == "" {
		storageDir = "."
	}
	d := &NodeDirectory{
		discoverURL: strings.TrimRight(strings.TrimSpace(discoverURL), "/"),
		path:        filepath.Join(storageDir, joinedFileName),
		http:        &http.Client{Timeout: 10 * time.Second},
		joined:      make(map[string]time.Time),
	}
	d.load()
	return d
}

// SetSelfID injects this node's peer id reader (used to exclude/mark self in the marketplace list).
func (d *NodeDirectory) SetSelfID(fn func() string) { d.selfID = fn }

// SetConnected injects the current direct connection table reader (peerID -> true).
func (d *NodeDirectory) SetConnected(fn func() map[string]bool) { d.connected = fn }

// SetDial injects the dialer: after Join, immediately attempts to establish a connection (not waiting
// for the next discovery poll).
func (d *NodeDirectory) SetDial(fn func(peerID string)) { d.dial = fn }

// ---- Joined list (persistent) ----

func (d *NodeDirectory) load() {
	raw, err := os.ReadFile(d.path)
	if err != nil {
		// File not found is a normal starting point (first run), no warning
		if !os.IsNotExist(err) {
			log.LogWarn("node-directory: read %s failed: %v", d.path, err)
		}
		return
	}
	var f joinedFile
	if err := json.Unmarshal(raw, &f); err != nil {
		// Corrupted list does not block the service: ignore and keep the original file (for manual
		// inspection), next Join will overwrite it
		log.LogWarn("node-directory: parse %s failed: %v", d.path, err)
		return
	}
	for _, p := range f.Peers {
		if p.PeerID == "" {
			continue
		}
		t := p.JoinedAt
		if t.IsZero() {
			t = time.Now()
		}
		d.joined[p.PeerID] = t
	}
}

// saveLocked writes to disk atomically (caller holds the lock): temp file + rename.
// Why not WriteFile directly: a process kill / power outage would leave a partial JSON, and the
// next startup would fail to parse it, silently falling back to the environment variable defaults,
// and the carefully selected join list would be lost.
func (d *NodeDirectory) saveLocked() error {
	f := joinedFile{Peers: make([]joinedPeer, 0, len(d.joined))}
	for id, t := range d.joined {
		f.Peers = append(f.Peers, joinedPeer{PeerID: id, JoinedAt: t})
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// List returns the current marketplace list: online nodes from discovery + joined nodes merged
// into unified entries. Offline joined nodes are retained (the operator should be able to see
// previously added peers even when they're offline).
func (d *NodeDirectory) List() []model.MarketNode {
	self := ""
	if d.selfID != nil {
		self = d.selfID()
	}
	// Fetch online nodes from discovery (failure does not block the list)
	online := make(map[string]*discoveredNode)
	if d.discoverURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.discoverURL+"/discover/nodes", nil)
		if err == nil {
			resp, err := d.http.Do(req)
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				var nodes []discoveredNode
				if err == nil && json.Unmarshal(body, &nodes) == nil {
					for i := range nodes {
						online[nodes[i].PeerID] = &nodes[i]
					}
				}
			}
		}
	}

	connected := map[string]bool{}
	if d.connected != nil {
		connected = d.connected()
	}

	// Merge: online (discover + connected + share summary) + joined not on the online list
	out := make([]model.MarketNode, 0, len(online)+len(d.joined))
	for id, node := range online {
		if id == self {
			continue // Don't show self in the marketplace
		}
		joinedAt := d.joined[id]
		nodeType := node.NodeType
		uptime := node.Uptime
		shares := nodeSharesFromMap(node.LoadInfo)
		out = append(out, model.MarketNode{
			PeerID:     id,
			Online:     true,
			Connected:  connected[id],
			Joined:     joinedAt != (time.Time{}),
			JoinedAt:   &joinedAt,
			NodeType:   nodeType,
			Uptime:     &uptime,
			Collections: node.Collections,
			Shares:     shares,
		})
	}
	d.mu.Lock()
	for id, joinedAt := range d.joined {
		if id == self {
			continue
		}
		if _, ok := online[id]; ok {
			continue // Already included above
		}
		out = append(out, model.MarketNode{
			PeerID:   id,
			Online:   false,
			Joined:   true,
			JoinedAt: &joinedAt,
		})
	}
	d.mu.Unlock()
	return out
}

// Join adds a node to the joined list and attempts an immediate connection.
func (d *NodeDirectory) Join(peerID string) error {
	if err := validatePeerID(peerID); err != nil {
		return err
	}
	self := ""
	if d.selfID != nil {
		self = d.selfID()
	}
	if peerID == self {
		return fmt.Errorf("cannot join self")
	}
	d.mu.Lock()
	if _, ok := d.joined[peerID]; !ok {
		d.joined[peerID] = time.Now()
	}
	d.mu.Unlock()
	if err := d.save(); err != nil {
		return err
	}
	if d.dial != nil {
		d.dial(peerID)
	}
	log.LogInfo("node-directory: joined %s", peerID)
	return nil
}

// Leave removes a node from the joined list.
func (d *NodeDirectory) Leave(peerID string) error {
	d.mu.Lock()
	if _, ok := d.joined[peerID]; !ok {
		d.mu.Unlock()
		return fmt.Errorf("node %s is not joined", peerID)
	}
	delete(d.joined, peerID)
	d.mu.Unlock()
	if err := d.save(); err != nil {
		return err
	}
	log.LogInfo("node-directory: left %s", peerID)
	return nil
}

func (d *NodeDirectory) save() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.saveLocked()
}

// ---- Helpers ----

// nodeSharesFromMap extracts NodeShares from the discovery server's loadInfo map.
func nodeSharesFromMap(m map[string]any) model.NodeShares {
	if m == nil {
		return model.NodeShares{}
	}
	return model.NodeShares{
		Collections: asInt(m["collections"]),
		Files:       asInt(m["files"]),
		Dirs:        asInt(m["dirs"]),
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

// validatePeerID validates a node id: non-empty, length-limited, no whitespace/control characters.
// Why validate: peerId goes to disk (joined_nodes.json) and into signaling query parameters.
// Allowing newlines/overly long strings would pollute the list file and logs.
func validatePeerID(peerID string) error {
	if peerID == "" {
		return fmt.Errorf("peer is required")
	}
	if len(peerID) > 128 {
		return fmt.Errorf("peer id too long")
	}
	for _, r := range peerID {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("peer id contains invalid character")
		}
	}
	return nil
}
