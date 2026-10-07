package transport

// psk.go: Pre-shared key gate for node access (config.PeerPSK / PEERDRIVE_PSK).
//
// Why it's needed: the cloud drive panel is a **public static page** (GitHub Pages / file://),
// anyone with the node id + same self-hosted signaling can handshake and ask for shares,
// fetch files. Signaling only handles matchmaking; it doesn't do access control — so
// access control must be built into the node's own connection, which is what this file does.
//
// Handshake (deliberately one-way "present", not challenge-response):
//
//	Connect → endpoint with PSK configured immediately sends {"type":"psk-auth","psk":"<key>"}
//	          → peer verifies: match → {"type":"psk-ok"}; mismatch → {"type":"psk-err","msg":"..."}
//
// Two design trade-offs:
//
//  1. **Plaintext key transmission**: DataChannel itself enforces DTLS encryption; the key
//     won't ride bare on the wire. Moreover, the server must hold the plaintext to compare;
//     challenge-response would just move the plaintext out of the channel, trading complexity
//     (nonce state, dual-end timing, HMAC implementation) for no real benefit. What we
//     really need to prevent is "strangers connecting", not "channel eavesdropping".
//  2. **Don't wait for psk-ok before sending business frames**: the same DataChannel is
//     ordered; the presenter sends "auth first then business"; the server processes in
//     order and necessarily sees auth first — so no need to wait for an RTT, avoiding
//     artificial latency per connection.
//
// Semantic boundaries (symmetric, each end independent):
//   - This node has no PSK configured → open mode, serve anyone (old behavior, backward
//     compatible);
//   - This node has PSK configured → peer must present the same key, otherwise all data
//     verbs return err;
//   - Peer has no PSK but this node does → this node rejects the peer (peer can't fetch
//     our stuff), **but our requests to the peer are unaffected** (peer is open, it serves
//     us normally).
//     i.e.: PSK protects the outgoing content of "the node that configured it".
//   - Local WS management session exempted (isSelfSession type assertion, share.go:120):
//     it's the operator's own management channel, doesn't go through the gate. A remote
//     peer registering with ?id=local on the signaling server does NOT exempt them —
//     rtcSession never implements IsLocal(), so the assertion fails closed (audit A-9,
//     2026-10-06).

import (
	"crypto/subtle"

	"peerdrive/internal/log"
)

// pskErrCode error code for err frames (consumers branch on code, don't match msg text).
const pskErrCode = "PSK_REQUIRED"

// servedVerbs frame types that require gate access: these are "peer asks me to do work"
// inbound verbs.
// Frames not in the list (meta/data/done/err/share-resp…) are responses to our own
// requests; blocking them would cut off our own fetches (peer open, this node has PSK →
// self-harm).
var servedVerbs = map[string]bool{
	"req": true, "create": true, "upload": true, "list": true,
	"share": true, "info": true, "delete": true, "sync": true,
	"pull": true, // Network ingestion (pull.go): lets a node make an outbound request on
	// behalf of the peer, the most gate-worthy verb of all
	"fwd-open": true, "fwd-auth": true, "fwd-data": true, "fwd-close": true,
}

// pskEnabled checks if this node has the key gate enabled.
func (s *PeerJSService) pskEnabled() bool {
	return s.cfg != nil && s.cfg.PeerPSK != ""
}

// pskSendAuth presents this node's key on connection establishment (only if configured).
// MUST be sent **before** registering OnMessage, guaranteeing it's always our first frame —
// order is semantics (see file header trade-off 2).
func (s *PeerJSService) pskSendAuth(c Session) {
	// 2026-10-06 security fix (audit A-9): the old check `c.ID() == "local"` was
	// bypassable by a remote peer registering with ?id=local on the signaling server.
	// isSelfSession uses type assertion — only WSSession implements IsLocal().
	if !s.pskEnabled() || isSelfSession(c) {
		return
	}
	// Use a plain map instead of dcResp: dcResp has many omitempty fields, making the frame
	// fatter; we only need two fields here.
	if err := c.SendJSON(map[string]string{"type": "psk-auth", "psk": s.cfg.PeerPSK}); err != nil {
		log.LogWarn("peerjs: send psk-auth to %s failed: %v", c.ID(), err)
		return
	}
	log.LogInfo("peerjs: psk-auth sent to %s", c.ID())
}

// servePskAuth verifies the peer's presented key.
// Uses constant-time compare, and **doesn't close the connection on failure**: lets the
// peer retry with the correct key, and also lets it receive an explicit err on the next
// verb (closing would only make it see a timeout, harder to debug).
func (s *PeerJSService) servePskAuth(c Session, st *connState, got string) {
	if !s.pskEnabled() {
		return // Open mode: peer sent extra auth, ignore (forward compatible)
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.PeerPSK)) != 1 {
		_ = c.SendJSON(dcResp{Type: "psk-err", Msg: "psk: key mismatch", Code: pskErrCode})
		log.LogWarn("peerjs: psk mismatch from %s, rejected", c.ID())
		return
	}
	st.mu.Lock()
	st.pskOK = true
	st.mu.Unlock()
	_ = c.SendJSON(dcResp{Type: "psk-ok"})
	log.LogInfo("peerjs: psk ok from %s", c.ID())
}

// pskGate gate decision: should this inbound verb be blocked.
// Returns true if err has been sent and the caller must stop processing the frame.
func (s *PeerJSService) pskGate(c Session, st *connState, r dcResp) bool {
	// 2026-10-06 security fix (audit A-9): the old check `c.ID() == "local"` was
	// bypassable by a remote peer registering with ?id=local on the signaling server.
	// isSelfSession uses type assertion — only WSSession implements IsLocal().
	if !s.pskEnabled() || !servedVerbs[r.Type] || isSelfSession(c) {
		return false
	}
	st.mu.Lock()
	ok := st.pskOK
	st.mu.Unlock()
	if ok {
		return false
	}
	_ = c.SendJSON(dcResp{
		Type:  "err",
		Msg:   "psk: this node requires a pre-shared key (present via psk-auth frame first)",
		Code:  pskErrCode,
		ReqID: r.ReqID,
		Hash:  r.Hash,
	})
	log.LogWarn("peerjs: verb %q from %s rejected: psk required", r.Type, c.ID())
	return true
}

// PSKState provides self-inspection for status API (GET /peerjs/node): whether this node
// has the gate enabled + how many connections' peers have passed. (Exported because HTTP
// layer needs cross-package read; not involved in gating decisions.)
func (s *PeerJSService) PSKState() (enabled bool, n int) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for _, st := range s.pending {
		st.mu.Lock()
		if st.pskOK {
			n++
		}
		st.mu.Unlock()
	}
	return s.pskEnabled(), n
}
