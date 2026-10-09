package transport

// gate.go: Unified admission, access control, and identity gates (doc/NETDISK.md §12.6, ROADMAP Phase 7).
//
// Responsibilities:
//   1. Admission Gate (PSK): Verifies pre-shared key for node access on untrusted networks (config.PeerPSK / PEERDRIVE_PSK).
//   2. Session Locality Gate: Identifies local management sessions (isSelfSession via IsLocal() type assertion).
//   3. Share Gate: Download permission gate determining whether content hash is accessible to requester (doc/NETDISK.md §12.6).
//   4. Identity & Authorization Gate (Phase 7 plugin point): Authorizer interface + DefaultAuthorizer fallback.
//
// Decoupling:
//   Gating logic is strictly decoupled from verb routing (inbound.go, outbound.go, share.go, admin.go),
//   allowing Phase 7 identity verification to evolve without touching wire verb dispatch.

import (
	"crypto/subtle"

	"peerdrive/internal/log"
)

// ============================================================================
// Part 1: Session Locality Gate
// ============================================================================

// isSelfSession determines whether the session comes from "self" (local WS direct connection, i.e., the admin panel/dashboard
// connecting via /ws/peer locally).
//
// Why it's needed: the semantics of private is "only self and friends can download." On a P2P connection, the only
// identifier available is the peer's self-reported peer id. The operator's own panel also gets a random id (which may differ each time),
// so identity cannot be determined by id. A connection coming through the local WS is this node's management channel by definition,
// and it IS "self."
func isSelfSession(c Session) bool {
	ls, ok := c.(interface{ IsLocal() bool })
	return ok && ls.IsLocal()
}

// ============================================================================
// Part 2: Share Download Gate (doc/NETDISK.md §12.6)
// ============================================================================

// ShareGate is a download gate: determines whether a given hash can be sent to the requester (doc/NETDISK.md §12.6).
//
// Why separate the manifest from downloads: the essential difference between the three tiers lies in "list or not" and "give or not"
// (unlisted = don't list but give). A single provider can only answer "what's in the manifest," not
// "can this hash be fetched."
//
// Only private blocks access: public / unlisted / undeclared all pass through — content-addressed retrieval is this
// system's existing behavior, and PSK is the admission gate. Blocking "undeclared" would even break basic
// self-checks like "upload → retrieve by hash to verify."
//
// self = this node's local channel (HTTP management API / local WS direct connection), always "self."
type ShareGate interface {
	AllowsDownload(peerID, hash string, self bool) bool
}

// SetShareGate injects the download gate (main wires service.NodeShare). nil = no gating.
func (s *PeerJSService) SetShareGate(g ShareGate) {
	s.shareMu.Lock()
	s.shareGate = g
	s.shareMu.Unlock()
}

// currentShareGate takes a locked snapshot of the download gate.
func (s *PeerJSService) currentShareGate() ShareGate {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	return s.shareGate
}

// ============================================================================
// Part 3: Identity & Authorization Gate (ROADMAP Phase 7 Plugin Point)
// ============================================================================

// Authorizer determines whether a peer session is authorized to download a given content hash.
// This is an identity plugin point reserved for ROADMAP Phase 7 (Identity Management).
// In Phase 7, cryptographic identity verification (e.g. signature, auth token) will replace or
// extend the simple peer ID / friend list check.
type Authorizer interface {
	AuthorizeDownload(session Session, hash string, token string) bool
}

// DefaultAuthorizer implements Authorizer using the node's configured ShareGate and session locality.
// Behavior is strictly identical to the pre-Phase 7 gate:
// - Local sessions (IsLocal() == true) always pass;
// - Non-private content passes;
// - Private content requires the peerID to be on the friends list in ShareGate.
type DefaultAuthorizer struct {
	gateFunc func() ShareGate
}

// NewDefaultAuthorizer constructs a DefaultAuthorizer with a dynamic gate lookup function.
func NewDefaultAuthorizer(gateFunc func() ShareGate) *DefaultAuthorizer {
	return &DefaultAuthorizer{gateFunc: gateFunc}
}

// AuthorizeDownload checks whether session is authorized to download hash.
func (a *DefaultAuthorizer) AuthorizeDownload(session Session, hash string, token string) bool {
	if isSelfSession(session) {
		return true
	}
	if a.gateFunc == nil {
		return true
	}
	g := a.gateFunc()
	if g == nil {
		return true
	}
	return g.AllowsDownload(session.ID(), hash, false)
}

// SetAuthorizer injects a custom download authorizer (Phase 7 identity plugin point).
// Passing nil resets to DefaultAuthorizer.
func (s *PeerJSService) SetAuthorizer(a Authorizer) {
	s.shareMu.Lock()
	s.authorizer = a
	s.shareMu.Unlock()
}

// currentAuthorizer returns the active Authorizer, falling back to DefaultAuthorizer.
func (s *PeerJSService) currentAuthorizer() Authorizer {
	s.shareMu.RLock()
	defer s.shareMu.RUnlock()
	if s.authorizer != nil {
		return s.authorizer
	}
	if s.defaultAuthorizer != nil {
		return s.defaultAuthorizer
	}
	return NewDefaultAuthorizer(s.currentShareGate)
}

// ============================================================================
// Part 4: Admission Gate (Pre-Shared Key / PSK)
// ============================================================================

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
	"search": true,
	"pull":   true,
	"fwd-open": true, "fwd-auth": true, "fwd-data": true, "fwd-close": true,
}

// pskEnabled checks if this node has the key gate enabled.
func (s *PeerJSService) pskEnabled() bool {
	return s.cfg != nil && s.cfg.PeerPSK != ""
}

// pskSendAuth presents this node's key on connection establishment (only if configured).
// MUST be sent **before** registering OnMessage, guaranteeing it's always our first frame —
// order is semantics.
func (s *PeerJSService) pskSendAuth(c Session) {
	// 2026-10-06 security fix (audit A-9): isSelfSession uses type assertion — only WSSession implements IsLocal().
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
	// 2026-10-06 security fix (audit A-9): isSelfSession uses type assertion — only WSSession implements IsLocal().
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
