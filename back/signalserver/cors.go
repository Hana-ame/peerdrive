// Package signalserver — CORS handling.
//
// Two flavours:
//
//   - allowCORS / handleCORS: wildcard Access-Control-Allow-Origin, used by
//     public panel endpoints (/peerjs/id, /discover/*).
//   - handleCORSPreflight: no Allow-Origin header at all, used by ops-facing
//     endpoints (/status, /discover/leave) so that a cross-origin fetch() gets
//     an opaque response.
//
// allowCORSFor is the tightening switch (opt-in via WithCORSOrigins): when a
// list is configured, only matching Origins are echoed back.
package signalserver

import (
	"net/http"
	"strings"
)

// allowCORS opens up cross-origin and short-circuits OPTIONS preflight.
//
// Discovery background: peerdrive's cloud storage UI is "a shared static panel (packages/peerdrive-client
// dist/panel.html, which can be opened via file:// double-click or hosted on any static space), connecting directly to nodes via PeerJS".
// The panel's first step is `GET /peerjs/id` to request a temporary id from the signaling server —— and the browser computes the
// origin of that page as `null` (file://) or the panel's own domain, **which is cross-origin from the signaling server**: responses without Access-Control-Allow-Origin
// are silently dropped by same-origin policy, and the client only shows a vague
// `server-error: Could not get an ID from the server`, with no indication it's CORS.
// These REST endpoints (fetch random id, discovery) are inherently public information, with no credentials to be stolen,
// so opening them up is safe. WS handshakes don't go through CORS and don't need handling.
func allowCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

// allowCORSFor is allowCORS with the optional explicit allow-list applied (2026-10-04).
//
// No list configured -> wildcard, byte-identical to the historical behavior, so not
// configuring anything keeps every existing deployment working exactly as before.
// List configured -> echo the request Origin when it matches; when it does not match,
// deliberately emit **no** Allow-Origin header, which is exactly what makes the browser
// block the read. That is the point of the switch: a wildcard means "every site on the
// internet may read this", which is what turned the discovery endpoint into a one-fetch
// census of every node on the network.
func (s *Server) allowCORSFor(w http.ResponseWriter, r *http.Request) {
	if len(s.corsOrigins) == 0 {
		allowCORS(w)
		return
	}
	origin := r.Header.Get("Origin")
	for _, allowed := range s.corsOrigins {
		if allowed == "*" {
			allowCORS(w)
			return
		}
		if origin != "" && strings.EqualFold(origin, allowed) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			return
		}
	}
	// Not allow-listed: fall through with no Allow-Origin, so the browser blocks it.
}

// handleCORS writes cross-origin headers and handles preflight; returns true when the request is fully handled and the caller should return immediately.
func (s *Server) handleCORS(w http.ResponseWriter, r *http.Request) bool {
	s.allowCORSFor(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

// handleCORSPreflight is handleCORS **without** the wildcard header, for endpoints that
// must not be readable cross-origin.
//
// Why (2026-10-04): allowCORS sets `Access-Control-Allow-Origin: *`, which is load-bearing for
// the public panel — it is opened via file:// (Origin `null`) or from any static host, so all of
// its origins are cross-origin from the signaling server (see the allowCORS comment).
// But that same header on the **ops-facing** endpoints turns "which nodes exist on this
// network" into something any web page can read. So: panel endpoints keep the wildcard,
// ops endpoints keep only the preflight short-circuit and omit the allow-origin header —
// a cross-origin caller then gets an opaque response, a same-origin/curl caller is unaffected.
func handleCORSPreflight(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}
