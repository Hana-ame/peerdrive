// Package signalserver — server status and dashboard.
//
// HandleStatus returns the full server snapshot (nodes, graph, counts).
// HandleDashboard serves the embedded dashboard.html.
// Both are ops-facing and must not be readable cross-origin (handleCORSPreflight).
package signalserver

import (
	"encoding/json"
	"embed"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

//go:embed dashboard.html
var dashboardFS embed.FS

// HandleStatus GET /status → server status snapshot (for dashboard polling).
//
// 2026-10-04: now requires an ops token and drops the wildcard CORS header.
// /status embeds the full online-node roster (peer ids + share summary), so leaving it
// readable from any web page turned the roster into a one-fetch list of every node on the
// network. The bundled dashboard.html is served from this same origin, so it is unaffected;
// external tooling should pass `?token=` or an Authorization header.
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	// CORS preflight first (Round 3), then the ops-token gate (Round 2). Both apply and
	// neither replaces the other: the preflight decides what the browser may read, the
	// token decides who may read it at all. Resolved conflict during the 2026-10-04 merge
	// of PR #4 and PR #5, which both touched this block.
	if handleCORSPreflight(w, r) {
		return
	}
	if !s.opsTokenOK(r) {
		http.Error(w, "ops token required", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	clientCount := len(s.clients)
	queueCount := len(s.queues)
	totalQueued := 0
	for _, q := range s.queues {
		totalQueued += len(q)
	}
	// Collect active discovery nodes
	discoveredNodes := make([]NodeInfo, 0)
	seen := make(map[string]bool)
	cutoff := time.Now().Add(-s.heartbeatTTL)
	for _, peers := range s.disc {
		for id, last := range peers {
			if last.After(cutoff) && !seen[id] {
				seen[id] = true
				discoveredNodes = append(discoveredNodes, s.nodeInfo(id, last))
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

	uptime := time.Since(s.startedAt).Seconds()
	resp := map[string]any{
		// ⚠️ 这里原来回 `s.key`。2026-10-06 实测线上
		//   `curl https://peersignal.moonchan.xyz/status` 直接返回
		//   {"key":"pd-signal-<redacted>", ...}，无需任何凭据。
		// 于是「把 key 藏进二进制、轮换 git 里的硬编码」这条路走不通——
		// 任何匿名客户端读一次 /status 就拿到了，轮换没有意义。
		//
		// 信号 key 是这个公开中继的唯一凭据（HandleWS 会拿它比对，
		// signalserver.go:372），拿到就能注册任意 peer id 给全网中继流量。
		// 所以它不再出现在任何响应体里。
		// 唯一还需要知道它的场景是「运维要核对部署配置对不对」，
		// 为此新增 /status/key（带完整 ops 鉴权 + 不走 CORS），见 HandleOpsKey。
		"uptimeSec":   int64(uptime),
		"uptimeStr":   formatDuration(uptime),
		"clients":     clientCount,
		"queues":      queueCount,
		"totalQueued": totalQueued,
		"discovered":  len(discoveredNodes),
		"nodes":       discoveredNodes,
		"links":       links,
		"msgCount":    atomic.LoadInt64(&s.msgCount),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleDashboard GET / → dashboard HTML (embedded).
func (s *Server) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := dashboardFS.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "dashboard not found", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(b)
}

// formatDuration converts seconds to a human-readable duration.
func formatDuration(seconds float64) string {
	d := time.Duration(seconds) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", seconds)
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", seconds/60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", seconds/3600)
	}
	return fmt.Sprintf("%.1fd", seconds/86400)
}
