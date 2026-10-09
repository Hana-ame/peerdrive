// Package signalserver — configuration and options for the signaling server.
//
// This file holds the Option type and all With* constructors, plus the
// RateLimitConfig struct. Everything here is pure configuration data — no
// runtime state, no mutexes, no handlers. Kept in a separate file so the
// Server struct in signalserver.go stays focused on state and behaviour.
package signalserver

import "strings"

// Option is a signaling server configuration option.
type Option func(*Server)

// WithCORSOrigins sets an explicit CORS allow-list for the panel-facing REST endpoints.
//
// Default (not called) stays `Access-Control-Allow-Origin: *`, which is what every existing
// deployment relies on — see the allowCORS comment for why the panel cannot be given an
// exact origin by default (file:// has Origin `null`, and the panel may be hosted anywhere).
//
// Once an operator supplies a list, the server echoes back the request's Origin when it
// matches instead of the wildcard. That is the tightening switch; it is opt-in so that
// turning it on is a deliberate deployment decision rather than a surprise after an upgrade.
//
// Practical note: `null` must be listed explicitly to keep file:// panels working:
//
//	-cors-origin "https://peerdrive.pages.dev,null"
func WithCORSOrigins(origins []string) Option {
	return func(s *Server) {
		cleaned := make([]string, 0, len(origins))
		for _, o := range origins {
			o = strings.TrimSpace(o)
			if o != "" {
				cleaned = append(cleaned, o)
			}
		}
		s.corsOrigins = cleaned
	}
}

// WithTokenWhitelist sets the signaling token whitelist: the WS connection token must be on the list,
// otherwise the upgrade is rejected ("Invalid token provided"). Empty list = unrestricted (default, compatible
// with current public deployments).
// Pitfall (2026-08-18 code review): tokens were originally just ID occupancy protection—any client could choose
// a token to connect, so an attacker could register any ID to impersonate an online node and receive signaling
// (combined with a self-chosen ID, this can send OFFERs to any node luring connections to the attacker).
// A whitelist lets self-hosted deployments trust only known nodes.
// Note: discovery endpoints (announce/nodes) remain public—discovery's purpose is to let anyone find
// nodes; the whitelist only constrains the signaling plane.
// WithOpsToken sets the single credential for ops-facing endpoints (/status, /status/key).
//
// 2026-10-06: this was previously taken from the *signaling* token whitelist, which coupled two
// unrelated decisions — securing the ops surface silently turned on signaling auth for every
// node. Deployed that way, all existing nodes (which send no token) could no longer open a
// WebSocket, while HTTP probes kept returning 200, so it looked healthy. Hence a separate flag:
// turning the ops gate on must never break node connectivity.
func WithOpsToken(token string) Option {
	return func(s *Server) {
		s.opsToken = token
	}
}

func WithTokenWhitelist(tokens []string) Option {
	return func(s *Server) {
		if len(tokens) == 0 {
			return
		}
		s.tokenWhitelist = make(map[string]bool, len(tokens))
		for _, t := range tokens {
			s.tokenWhitelist[t] = true
		}
	}
}

// RateLimitConfig holds the per-endpoint rate limits (req/s and burst).
//
// Any field left at 0 disables that bucket **independently**, so an operator can
// throttle announce without touching WebSocket connects.
type RateLimitConfig struct {
	// AnnounceRPS / AnnounceBurst governs POST /discover/announce.
	//
	// Why this endpoint first: it is unauthenticated (by design — nodes announce
	// from any HTTP endpoint), and each accepted call inserts into the discovery
	// roster (disc / peerStats / peerColls / peerLinks). So it was the cheapest
	// available way to grow server-side state at will. 2026-10-06 audit N3.
	AnnounceRPS   float64
	AnnounceBurst int

	// WSRPS / WSBurst governs the /peerjs WebSocket upgrade.
	//
	// Why it matters beyond CPU: the read limit (40KB) and the 60s read deadline
	// bound a single connection's memory, but nothing bounded how many
	// connections could exist — each accepted upgrade costs a goroutine plus a
	// readLoop and a clients-table entry. Reconnects (normal on flaky mobile)
	// also mean the upgrade path is hit far more often than the steady-state
	// connection count suggests, so the bucket is sized for reconnects, not for
	// "how many nodes are online".
	WSRPS   float64
	WSBurst int

	// IDRPS / IDBurst governs GET /peerjs/id (the panel's first call).
	//
	// Kept separate from the WS bucket on purpose: a single page load does
	// GET /id and then opens the socket, so sharing one bucket would let either
	// half throttle the other — a user reloading the panel could be rejected on
	// /id because their socket is reconnecting. Sized generously because it
	// costs a random-ID generation and nothing else.
	IDRPS   float64
	IDBurst int
}

// WithRateLimit enables per-IP rate limiting on announce / WS upgrade / id.
//
// Before this there was no limit anywhere in this file: HandleWS had a read
// limit and a read deadline (both per-connection, neither bounds the *number*
// of connections), and announce had a body-size cap and a collection-count cap
// (both per-request, neither bounds the *number* of requests). Per-request and
// per-connection caps are not a substitute for a rate limit — an attacker who
// sends N small requests pays nothing extra and gets N roster entries.
//
// Left unset, this package keeps its historical unlimited behavior. That is
// deliberate: it is a standalone module whose consumers include deployments
// that already rate-limit at the nginx layer, and silently imposing a default
// would be a breaking change for them. The peerdrive wiring (services.SignalHandler
// and cmd/peersignal) passes an explicit config.
func WithRateLimit(cfg RateLimitConfig) Option {
	return func(s *Server) {
		if cfg.AnnounceRPS > 0 {
			s.announceLim = newTokenBucket(cfg.AnnounceRPS, cfg.AnnounceBurst)
		}
		if cfg.WSRPS > 0 {
			s.wsLim = newTokenBucket(cfg.WSRPS, cfg.WSBurst)
		}
		if cfg.IDRPS > 0 {
			s.idLim = newTokenBucket(cfg.IDRPS, cfg.IDBurst)
		}
	}
}
