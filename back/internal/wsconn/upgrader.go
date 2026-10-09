package wsconn

import (
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// IsLoopbackRemote reports whether a TCP peer address is a local one, i.e.
// RemoteAddr like 127.0.0.1:54321 or [::1]:54321.
//
// Only RemoteAddr is checked, never X-Forwarded-For: behind a reverse proxy
// XFF trust is partly client-controlled, and this is a security boundary —
// better to be conservative. In reverse-proxy deployments browser requests
// always carry an Origin header, so the whitelist path is used instead.
func IsLoopbackRemote(remote string) bool {
	host := remote
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i] // IPv4: strip port
	}
	host = strings.Trim(host, "[]") // IPv6: [::1] → ::1
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// OriginPolicy is the RFC 6455 §13.1 origin decision for a handshake.
//
// The two branches are deliberately asymmetric, and the asymmetry is the
// point:
//
//   - No Origin header: the client is not a browser (scripts, curl, wscat).
//     A browser always sends Origin on a handshake, so an Origin-less
//     request reaching a reachable port is a script — accept it only if it
//     really comes from localhost. Before this rule any Origin-less request
//     was accepted, which gave scripts on the network full access to the
//     admin surface, and those sessions are treated as "self".
//   - Origin present: no allowlist configured accepts everything (the
//     historical default); otherwise defer to the caller's allowlist, which
//     is the same list as HTTP CORS.
type OriginPolicy struct {
	// Allow is called only when the request carries an Origin header. It
	// must not dereference a nil config: a nil Allow reproduces the
	// historical "no allowlist configured" behavior, which accepts.
	Allow func(origin string) bool
}

// CheckOrigin is the origin decision expressed in gorilla's terms, so it can
// be handed to Upgrader.CheckOrigin as-is.
func (p OriginPolicy) CheckOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return IsLoopbackRemote(r.RemoteAddr)
	}
	if p.Allow == nil {
		return true
	}
	return p.Allow(origin)
}

// Upgrader performs the WebSocket handshake for this node's local session
// endpoint. It owns exactly two things: the origin decision and the handoff
// to gorilla. The response body written to a rejected request, and the JSON
// error frame the caller sends back, both stay with the caller because they
// are HTTP routing concerns, not transport concerns.
type Upgrader struct {
	origin OriginPolicy
}

// NewUpgrader builds an Upgrader with the given origin policy.
func NewUpgrader(origin OriginPolicy) *Upgrader {
	return &Upgrader{origin: origin}
}

// Upgrade runs the handshake and returns the upgraded socket. On failure it
// has already written the HTTP error response to w and returns a
// *websocket.HandshakeError.
func (u *Upgrader) Upgrade(w http.ResponseWriter, r *http.Request, responseHeader http.Header) (*websocket.Conn, error) {
	up := websocket.Upgrader{CheckOrigin: u.origin.CheckOrigin}
	return up.Upgrade(w, r, responseHeader)
}
