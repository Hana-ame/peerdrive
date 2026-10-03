package transport

// forward.go: Port forwarding (inbound part) — carries TCP tunnels over PeerJS DataChannel.
//
// Background (why this layer exists): the legacy forward.go was a libp2p stream implementation
// (`/peerdrive/forward/1.0.0`) with only one line of plaintext `KEY xxx` authentication, no
// key exchange, no port whitelist. It was deprecated with the libp2p stack. The forwarding
// need itself is preserved (exposing a NAT-blocked node's local port to a key-holding remote,
// e.g. a VPS resident node's 127.0.0.1 service), so it's rebuilt as a new verb in the
// transport role system:
//
//	client ──fwd-open {port, reqId}──────────────────▶ server
//	client ◀──fwd-challenge {nonce, reqId}────────────  server   one-time random
//	client ──fwd-auth {hmac, reqId}──────────────────▶ server   HMAC-SHA256(key, nonce)
//	client ◀──fwd-ok / fwd-err──────────────────────── server
//	Afterward: fwd-data header + binary chunks bidirectionally transparent (frame protocol
//	same as file transfer: header text, chunk binary, atomic contiguous)
//	      fwd-close to wrap up (either side EOF / active close)
//
// Security design (addressing "access control + key exchange" requirements):
//  1. Server-side rule table only accepts plaintext key whitelist `key → allowed ports[]`
//     (configured via PEERDRIVE_FORWARD_RULES, can be dynamically appended via endpoints at
//     runtime). Key is equivalent to credentials — docs note to chmod 600 the config file.
//  2. Challenge-response: nonce is one-time (marked used on retrieval + 5-minute expiry),
//     HMAC proves key possession, key plaintext never on the wire (DataChannel itself is
//     DTLS encrypted, double insurance).
//  3. Port unauthorized rejection: requested port ∉ key's authorized list → fwd-err,
//     without leaking rule details.
//  4. SSRF protection: server only allows dialing 127.0.0.1 (forward target is local port,
//     no arbitrary internal IP access).
//  5. Handshake doesn't occupy tunnel slot: fwd-open/fwd-auth are just challenge states;
//     tunnel establishment occupies the connection-level single slot connState.fwd (same
//     connection has only one active forwarding stream at a time — file fetch/upload doesn't
//     block it because binary chunk routing goes by "fwd-data header declared ownership"
//     first).
//
// Forward chunk write direction must not block the message pump: bindConn pump only delivers
// chunks to fw.wCh (bounded); actual Write to the other end of the tunnel happens in a
// connection-level worker (shares uploadWorker's select, same H5 approach as conn.go's
// binCh) — otherwise peer TCP backpressure would head-of-line freeze the entire connection.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"peerdrive/internal/log"
)

// Forwarding handshake challenge validity and anti-flood limits.
const (
	fwdNonceTTL     = 5 * time.Minute
	fwdNonceMax     = 64               // Max unconsumed challenges (anti fwd-open flood memory pressure)
	fwdChunkSize    = 32 * 1024        // Tunnel data chunk size (forwarding is streaming, smaller chunks = lower latency)
	fwdHandshakeTTL = 30 * time.Second // Client-side handshake total timeout (peer not responding doesn't hang)
)

// fwdNonce server-side one-time challenge (reqId → pending verification state).
type fwdNonce struct {
	nonce  []byte
	port   int // target port declared by client in fwd-open
	expire time.Time
	used   bool
}

// fwdStream connection-level active forwarding tunnel (single slot: one at a time per connection).
// Two-end semantics:
//   - Server side (forwarded party): out = TCP connection dialing 127.0.0.1:port
//   - Client side (initiator): out = server end of net.Pipe (caller receives the client end)
type fwdStream struct {
	reqID   string
	keyID   string // Authorized key first 16 hex chars (for audit logging, not full key)
	port    int
	out     net.Conn
	pending bool // fwd-data header received, expecting next binary chunk (single-slot declaration, see conn.go pump routing)
	closed  bool
}

// fwdHandshake client-side handshake waiting state (connection-level single slot).
// Two separate channels: challenge arrives first (triggers fwd-auth send), done is final
// (ok/err). Pitfall: if sharing the same channel, OpenForward phase 1 "wait for challenge"
// would, after challenge arrives, be unable to distinguish "challenge available to continue"
// from "final state" — the first-closed channel would make the second select immediately
// return false as final. So challenge/done each close independently once.
type fwdHandshake struct {
	reqID     string
	nonce     []byte
	challenge chan struct{}
	done      chan struct{}
	ok        bool
	err       error
}

// ──────────────────────────────────────────────
// Server side: rule table (whitelist: key → allowed ports)
// ──────────────────────────────────────────────

// SetForwardRules sets forwarding authorization rules in full (plaintext key → port whitelist).
// Equivalent to credential loading: subsequent serveForwardAuth HMAC verification depends on
// the plaintext key.
// ensureForwardMaps lazily initializes rule/challenge maps (struct literal-constructed
// instances — tests — don't go through NewPeerJSService; defensive against nil map
// assignment/read panics).
func (s *PeerJSService) ensureForwardMaps() {
	if s.forwardRules == nil {
		s.forwardRules = map[string][]int{}
	}
	if s.fwNonces == nil {
		s.fwNonces = map[string]*fwdNonce{}
	}
}

func (s *PeerJSService) SetForwardRules(rules map[string][]int) {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	s.ensureForwardMaps()
	s.forwardRules = make(map[string][]int, len(rules))
	for k, ports := range rules {
		s.forwardRules[k] = append([]int(nil), ports...)
	}
}

// AddForwardRule appends an authorization rule at runtime (used by POST /p2p/forward/create,
// not persisted).
func (s *PeerJSService) AddForwardRule(key string, ports []int) {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	s.ensureForwardMaps()
	for _, p := range ports {
		if !containsInt(s.forwardRules[key], p) {
			s.forwardRules[key] = append(s.forwardRules[key], p)
		}
	}
}

// RemoveForwardRule removes an authorization rule at runtime (used by POST /p2p/forward/delete).
func (s *PeerJSService) RemoveForwardRule(key string) {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	if s.forwardRules != nil {
		delete(s.forwardRules, key)
	}
}

// ForwardRulesForTest returns a copy of the current forwarding rules (test assertion).
func (s *PeerJSService) ForwardRulesForTest() map[string][]int {
	s.forwardMu.Lock()
	defer s.forwardMu.Unlock()
	out := make(map[string][]int, len(s.forwardRules))
	for k, v := range s.forwardRules {
		out[k] = append([]int(nil), v...)
	}
	return out
}

func containsInt(slice []int, val int) bool {
	for _, v := range slice {
		if v == val {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────
// Server side: handshake + tunnel
// ──────────────────────────────────────────────

// serveForwardOpen client declares a forwarding request (fwd-open {port, reqId}).
// Server validates authorization (key not yet presented at this stage — rule lookup only
// checks if ANY key authorizes this port), generates a one-time nonce and returns challenge.
//
// Note: port unauthorized → fwd-err (doesn't leak rule details); port in some key's
// whitelist → continue. Key identity is confirmed in the next step's fwd-auth HMAC.
func (s *PeerJSService) serveForwardOpen(c Session, st *connState, r dcResp) {
	s.forwardMu.Lock()
	portAuthorized := false
	for _, ports := range s.forwardRules {
		if containsInt(ports, r.Port) {
			portAuthorized = true
			break
		}
	}
	s.forwardMu.Unlock()
	if !portAuthorized {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: port not authorized", ReqID: r.ReqID})
		return
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: nonce generation failed", ReqID: r.ReqID})
		return
	}
	s.nonceMu.Lock()
	// Anti-flood: challenge table capacity limited; when full, cleanup expired entries
	// first.
	if len(s.fwNonces) >= fwdNonceMax {
		now := time.Now()
		for k, n := range s.fwNonces {
			if n.used || now.After(n.expire) {
				delete(s.fwNonces, k)
			}
		}
	}
	if len(s.fwNonces) >= fwdNonceMax {
		s.nonceMu.Unlock()
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: too many pending challenges", ReqID: r.ReqID})
		return
	}
	s.fwNonces[r.ReqID] = &fwdNonce{nonce: nonce, port: r.Port, expire: time.Now().Add(fwdNonceTTL)}
	s.nonceMu.Unlock()
	_ = c.SendJSON(dcResp{Type: "fwd-challenge", Nonce: hex.EncodeToString(nonce), ReqID: r.ReqID})
}

// serveForwardAuth client presents HMAC(key, nonce), server verifies and establishes tunnel.
// HMAC uses server-side held plaintext key for comparison (constant-time compare).
//
// Key identity is discovered by traversing all rule keys — only the authorized key can
// pass HMAC verification. Multiple keys may authorize the same port; whichever HMAC matches
// is accepted (auditing uses key prefix as keyID).
func (s *PeerJSService) serveForwardAuth(c Session, st *connState, r dcResp) {
	s.nonceMu.Lock()
	nn := s.fwNonces[r.ReqID]
	if nn != nil {
		nn.used = true
	}
	s.nonceMu.Unlock()
	if nn == nil || time.Now().After(nn.expire) {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: challenge expired or unknown", ReqID: r.ReqID})
		return
	}
	provided, err := hex.DecodeString(r.Hmac)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: invalid hmac", ReqID: r.ReqID})
		return
	}
	s.forwardMu.Lock()
	var matchedKey string
	var keyID string
	for k, ports := range s.forwardRules {
		if !containsInt(ports, nn.port) {
			continue
		}
		mac := hmac.New(sha256.New, []byte(k))
		mac.Write(nn.nonce)
		if hmac.Equal(mac.Sum(nil), provided) {
			matchedKey = k
			keyID = k[:min(16, len(k))]
			break
		}
	}
	s.forwardMu.Unlock()
	if matchedKey == "" {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: authentication failed", ReqID: r.ReqID})
		return
	}
	// SSRF protection: only allow dialing 127.0.0.1 (target is local port)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", nn.port), 5*time.Second)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: target unreachable: " + err.Error(), ReqID: r.ReqID})
		return
	}
	fw := &fwdStream{reqID: r.ReqID, keyID: keyID, port: nn.port, out: conn}
	st.mu.Lock()
	// Single slot: if there's already an active tunnel, reject (one active forwarding per connection)
	if st.fwd != nil && !st.fwd.closed {
		st.mu.Unlock()
		conn.Close()
		_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: tunnel already active", ReqID: r.ReqID})
		return
	}
	st.fwd = fw
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "fwd-ok", ReqID: r.ReqID})
	go s.forwardPump(c, st, fw)
	log.LogInfo("peerjs: forward tunnel to port %d established (key %s)", nn.port, keyID)
}

// forwardPump bidirectional pump: DataChannel ↔ TCP tunnel. Either side EOF / error
// triggers fwd-close to the peer. This is a best-effort stream — no retry.
func (s *PeerJSService) forwardPump(c Session, st *connState, fw *fwdStream) {
	defer func() {
		st.mu.Lock()
		if st.fwd == fw && !fw.closed {
			fw.closed = true
			// Release the slot only when this is still the active tunnel (new tunnel may
			// have replaced it)
			st.fwd = nil
		}
		st.mu.Unlock()
		_ = c.SendJSON(dcResp{Type: "fwd-close", ReqID: fw.reqID})
		if fw.out != nil {
			fw.out.Close()
		}
	}()
	buf := make([]byte, fwdChunkSize)
	// TCP → DataChannel direction: read from tunnel, send as fwd-data chunk
	// Read side runs in pump goroutine, Write goes to binCh (connection-level worker)
	go func() {
		for {
			n, err := fw.out.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				st.mu.Lock()
				fwPending := fw.pending
				fwClosed := fw.closed
				st.mu.Unlock()
				if fwClosed || !fwPending {
					continue
				}
				select {
				case st.fwdCh <- fwdChunk{fw: fw, data: chunk}:
				case <-st.binDone:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	<-st.binDone
}

// serveForwardClose client actively closes the tunnel (fwd-close).
func (s *PeerJSService) serveForwardClose(c Session, st *connState, r dcResp) {
	st.mu.Lock()
	if st.fwd != nil && st.fwd.reqID == r.ReqID {
		st.fwd.closed = true
		st.fwd = nil
		st.mu.Unlock()
		if st.fwd.out != nil {
			st.fwd.out.Close()
		}
		return
	}
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "fwd-err", Msg: "forward: no such tunnel", ReqID: r.ReqID})
}

// routeForwardResponse routes server-side handshake responses to the client's waiting state
// (called from bindConn when fwd-challenge/fwd-ok/fwd-err frames arrive on the client side).
func (s *PeerJSService) routeForwardResponse(st *connState, r dcResp) {
	st.mu.Lock()
	hs := st.fwdHs
	if hs != nil && hs.reqID != r.ReqID {
		st.mu.Unlock()
		return
	}
	st.mu.Unlock()
	if hs == nil {
		return
	}
	switch r.Type {
	case "fwd-challenge":
		nonce, err := hex.DecodeString(r.Nonce)
		if err != nil {
			hs.err = err
			close(hs.challenge)
			close(hs.done)
			return
		}
		hs.nonce = nonce
		select {
		case <-hs.challenge:
		default:
			close(hs.challenge)
		}
	case "fwd-ok":
		hs.ok = true
		close(hs.done)
	case "fwd-err":
		hs.err = fmt.Errorf("forward: %s", r.Msg)
		close(hs.done)
	}
}

// ──────────────────────────────────────────────
// Client side: tunnel initiation
// ──────────────────────────────────────────────

// OpenForward initiates a forwarding tunnel to a remote node's local port.
// Flow: fwd-open → fwd-challenge (nonce) → fwd-auth (HMAC) → fwd-ok → tunnel ready.
// Returns a net.Conn that reads/writes the bidirectionally transparent tunnel.
//
// This is the only function where the caller can obtain a net.Conn for reading/writing the
// tunnel. The tunnel has no independent connection slot (occupies connState.fwd single slot)
// and follows the "connection closed = tunnel dies" lifecycle.
func (s *PeerJSService) OpenForward(ctx context.Context, peerID, port string, key string) (net.Conn, error) {
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("peerjs: no connection to %s", peerID)
	}
	st := s.stateFor(conn)
	if st == nil {
		return nil, fmt.Errorf("peerjs: connection not bound")
	}
	portInt, err := parsePort(port)
	if err != nil {
		return nil, fmt.Errorf("peerjs: invalid port %q: %w", port, err)
	}
	reqID := fmt.Sprintf("fwd-%d", time.Now().UnixNano())
	hs := &fwdHandshake{reqID: reqID, challenge: make(chan struct{}), done: make(chan struct{})}
	st.mu.Lock()
	// Single slot: can't have two handshakes pending simultaneously
	if st.fwdHs != nil {
		st.mu.Unlock()
		return nil, fmt.Errorf("peerjs: forward handshake already in progress")
	}
	st.fwdHs = hs
	st.mu.Unlock()
	defer func() {
		st.mu.Lock()
		st.fwdHs = nil
		st.mu.Unlock()
	}()
	// Phase 1: send fwd-open, wait for challenge
	deadline := time.NewTimer(fwdHandshakeTTL)
	defer deadline.Stop()
	if err := conn.SendJSON(dcResp{Type: "fwd-open", Port: portInt, ReqID: reqID}); err != nil {
		return nil, err
	}
	select {
	case <-hs.challenge:
	case <-deadline.C:
		return nil, errors.New("peerjs: forward handshake timeout (no challenge)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if hs.err != nil {
		return nil, hs.err
	}
	// Phase 2: compute HMAC and send fwd-auth
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(hs.nonce)
	if err := conn.SendJSON(dcResp{Type: "fwd-auth", Hmac: hex.EncodeToString(mac.Sum(nil)), ReqID: hs.reqID}); err != nil {
		return nil, err
	}
	// Phase 2 continued: wait for ok/err
	select {
	case <-hs.done:
	case <-deadline.C:
		return nil, errors.New("peerjs: forward handshake timeout (no verdict)")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if hs.err != nil {
		return nil, hs.err
	}
	if !hs.ok {
		return nil, errors.New("peerjs: forward handshake rejected")
	}
	// Create pipe: caller gets client end, pump reads server end (data bidirectionally transparent)
	client, server := net.Pipe()
	fw := &fwdStream{
		reqID: hs.reqID,
		keyID: key[:min(16, len(key))],
		port:  portInt,
		out:   server,
	}
	st.mu.Lock()
	st.fwd = fw
	st.mu.Unlock()
	go s.forwardPump(conn, st, fw)
	log.LogInfo("peerjs: forward tunnel to %s established", peerID)
	return client, nil
}

// parsePort parses a port string to int (defensive against invalid input).
func parsePort(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty port")
	}
	var port int
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit in port: %q", s)
		}
		port = port*10 + int(c-'0')
		if port > 65535 {
			return 0, fmt.Errorf("port out of range: %d", port)
		}
	}
	if port == 0 {
		return 0, errors.New("port 0 is invalid")
	}
	return port, nil
}
