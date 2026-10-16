// Package signalserver — room discovery and node graph.
//
// This file owns the three discovery REST handlers (announce, leave, nodes)
// plus the data types they exchange (NodeInfo, GraphLink, PeerStats).
// All state mutations go through s.mu, shared with signalserver.go.
package signalserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// HandleAnnounce POST /discover/announce {peerId, collections[], nodeType, loadInfo, peers} node registration.
// Decoupled from signaling connections (nodes can report via any HTTP endpoint); lastSeen is refreshed by heartbeats.
// M15: unbounded decode risk—limit body size (8KB is sufficient: peerId + collections + peers + loadInfo)
// and the number of collections (a single node follows a limited number of rooms).
func (s *Server) HandleAnnounce(w http.ResponseWriter, r *http.Request) {
	if s.handleCORS(w, r) {
		return
	}
	// Rate limit before decoding (2026-10-06, N3). The existing M15 caps (8KB body,
	// 64 collections) are **per-request**; they bound one call's cost but not the
	// number of calls, and each accepted announce writes the roster (disc /
	// peerStats / peerColls / peerLinks). Unauthenticated + per-request-only caps
	// = an unbounded-growth endpoint, so this is the missing half.
	if !s.announceLim.allow(clientIP(r)) {
		rateLimited(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	var body struct {
		PeerID      string         `json:"peerId"`
		Collections []string       `json:"collections"`
		NodeType    string         `json:"nodeType,omitempty"`
		LoadInfo    map[string]any `json:"loadInfo,omitempty"`
		Peers       []string       `json:"peers,omitempty"` // list of currently WebRTC-connected peer ids
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}
	// 2026-10-06 security fix (audit A-11): validate the peer id before writing it to the
	// discovery table. An unauthenticated announce used to let anyone register arbitrary
	// ids (including reserved names like "local") as dictionary keys, poisoning discovery.
	if !validPeerID(body.PeerID) {
		http.Error(w, "invalid peerId", http.StatusBadRequest)
		return
	}
	const maxCollectionsPerAnnounce = 64
	if len(body.Collections) > maxCollectionsPerAnnounce {
		http.Error(w, "too many collections", http.StatusBadRequest)
		return
	}
	// Normalize the collection list: trim whitespace and remove empty strings; disc and peerColls use the same data.
	cleanColls := make([]string, 0, len(body.Collections))
	for _, coll := range body.Collections {
		coll = strings.TrimSpace(coll)
		if coll != "" {
			cleanColls = append(cleanColls, coll)
		}
	}

	now := time.Now()
	s.mu.Lock()
	collSet := make(map[string]bool, len(cleanColls))
	for _, coll := range cleanColls {
		collSet[coll] = true
		peers, ok := s.disc[coll]
		if !ok {
			peers = make(map[string]time.Time)
			s.disc[coll] = peers
		}
		peers[body.PeerID] = now
	}
	// Immediately remove this node from old collections (no need to wait for TTL after collections change)
	for coll, peers := range s.disc {
		if !collSet[coll] {
			delete(peers, body.PeerID)
			if len(peers) == 0 {
				delete(s.disc, coll)
			}
		}
	}
	// Update node metadata (nodeType/collections/loadInfo), shared by dashboard and discovery API.
	stats := s.peerStats[body.PeerID]
	if stats == nil {
		stats = &PeerStats{}
		s.peerStats[body.PeerID] = stats
	}
	stats.NodeType = body.NodeType
	stats.LastSeen = now
	stats.LoadInfo = body.LoadInfo
	// Always update collections (even clear old values when empty) to avoid stale collections remaining after a node clears its set.
	s.peerColls[body.PeerID] = cleanColls
	// Update graph edges: this node's currently directly connected peers.
	// Always update (even clear old edges when peers is empty/missing) to avoid stale connections lingering in the graph.
	links := make(map[string]time.Time, len(body.Peers))
	for _, nid := range body.Peers {
		nid = strings.TrimSpace(nid)
		if nid != "" && nid != body.PeerID {
			links[nid] = now
		}
	}
	s.peerLinks[body.PeerID] = links
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// HandleLeave POST /discover/leave graceful node shutdown.
//
// 2026-10-06 security fix (audit A-11): this endpoint used to be unauthenticated —
// any caller could POST {"peerId":"<victim>"} and erase that node from every discovery
// collection, peerStats, peerColls, and peerLinks graph edges. The operation is idempotent
// (returns 200 + {"ok":true}), leaves no log, and the victim would only notice "nobody
// connects to me" — a silent DoS vector. Now gated with opsTokenOK (same as /status).
// No known client calls leave (nodes rely on heartbeat expiry for offline detection).
func (s *Server) HandleLeave(w http.ResponseWriter, r *http.Request) {
	// 审计 R2 MEDIUM（2026-10-08）：HandleLeave 是 ops 端点（需要 ops token），
	// 不应向跨源暴露 Access-Control-Allow-Origin: *。改用 handleCORSPreflight
	// （只处理 OPTIONS preflight，不设 Allow-Origin），与 HandleStatus 对齐。
	if handleCORSPreflight(w, r) {
		return
	}
	if !s.opsTokenOK(r) {
		http.Error(w, "ops token required", http.StatusUnauthorized)
		return
	}
	var body struct {
		PeerID string `json:"peerId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		http.Error(w, "peerId required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	// Remove from all collections
	for _, peers := range s.disc {
		delete(peers, body.PeerID)
	}
	delete(s.peerStats, body.PeerID)
	delete(s.peerColls, body.PeerID)
	delete(s.peerLinks, body.PeerID)
	// Also remove this node from other nodes' neighbor lists
	for _, links := range s.peerLinks {
		delete(links, body.PeerID)
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// GraphLink is an edge in the graph (source ↔ target have established a WebRTC connection).
type GraphLink struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	LastSeen int64  `json:"lastSeen,omitempty"`
}

// HandleNodes GET /discover/nodes?coll=&type= → online node list (heartbeat-expired entries removed) + graph edges.
// Empty coll means return nodes from all collections; type can be used to filter node types.
func (s *Server) HandleNodes(w http.ResponseWriter, r *http.Request) {
	if s.handleCORS(w, r) {
		return
	}
	coll := r.URL.Query().Get("coll")
	nodeType := r.URL.Query().Get("type")
	cutoff := time.Now().Add(-s.heartbeatTTL)
	s.mu.Lock()
	out := make([]NodeInfo, 0)
	seen := make(map[string]bool)

	if coll != "" {
		// Specified collection: query only this room
		peers := s.disc[coll]
		out = make([]NodeInfo, 0, len(peers))
		for id, last := range peers {
			if last.After(cutoff) && !seen[id] {
				info := s.nodeInfo(id, last)
				// Don't mark as seen when type filter doesn't pass: allow this node to be checked again in other collections
				// while graph edges are only based on actually returned nodes.
				if nodeType != "" && info.NodeType != nodeType {
					continue
				}
				seen[id] = true
				out = append(out, info)
			}
		}
	} else {
		// 2026-10-04: `type` is an **ops filter** — the public panel never sends it
		// (panel/app.js discoverNow calls discoverNodes(sig) with neither coll nor type, to answer
		// "who is online at all"). The dashboard uses it to colour the node graph by role.
		// Gating on "no coll" was this change's first attempt and it was **wrong**: CI caught it —
		// the E2E panel step failed with "Discovery service returned HTTP 401" because the panel's
		// own auto-search IS a coll-less query. Keep the no-coll form public (it is the panel's
		// entry point) and require an ops token only for the inventory-style query.
		// Trade-off accepted for now: the roster is still enumerable without a token, because the
		// panel depends on it. What is closed here is /status (ops-only) and its cross-origin
		// readability; the roster proper waits for Round 3 panel token support.
		if nodeType != "" && !s.opsTokenOK(r) {
			s.mu.Unlock()
			http.Error(w, "ops token required for ?type= queries", http.StatusUnauthorized)
			return
		}
		// Empty coll: iterate all collections and return deduplicated online nodes
		for _, peers := range s.disc {
			for id, last := range peers {
				if last.After(cutoff) && !seen[id] {
					info := s.nodeInfo(id, last)
					if nodeType != "" && info.NodeType != nodeType {
						continue
					}
					seen[id] = true
					out = append(out, info)
				}
			}
		}
	}
	// Collect graph edges: only keep edges where both ends are still active, to avoid showing offline ghost nodes.
	links := make([]GraphLink, 0)
	linkSeen := make(map[string]bool)
	for src, neighbors := range s.peerLinks {
		if !seen[src] {
			continue
		}
		for dst, last := range neighbors {
			if !seen[dst] {
				continue
			}
			a, b := src, dst
			if a > b {
				a, b = b, a
			}
			key := a + "\x00" + b
			if linkSeen[key] {
				continue
			}
			linkSeen[key] = true
			links = append(links, GraphLink{Source: a, Target: b, LastSeen: last.Unix()})
		}
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"nodes": out, "links": links})
}

// PeerStats is node statistics and load information.
type PeerStats struct {
	NodeType string         `json:"nodeType,omitempty"`
	Uptime   int64          `json:"uptime,omitempty"`
	LoadInfo map[string]any `json:"loadInfo,omitempty"`
	LastSeen time.Time      `json:"-"`
}

// nodeInfo assembles a discovery response entry from peerStats/peerColls.
func (s *Server) nodeInfo(id string, last time.Time) NodeInfo {
	info := NodeInfo{PeerID: id, LastSeen: last.Unix()}
	if st := s.peerStats[id]; st != nil {
		info.NodeType = st.NodeType
		info.Uptime = st.Uptime
		info.LoadInfo = st.LoadInfo
	}
	if colls := s.peerColls[id]; len(colls) > 0 {
		info.Collections = colls
	}
	return info
}

// NodeInfo is a discovery response entry.
type NodeInfo struct {
	PeerID      string         `json:"peerId"`
	LastSeen    int64          `json:"lastSeen"`
	NodeType    string         `json:"nodeType,omitempty"`
	Collections []string       `json:"collections,omitempty"`
	Uptime      int64          `json:"uptime,omitempty"`
	LoadInfo    map[string]any `json:"loadInfo,omitempty"`
}
