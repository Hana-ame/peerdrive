package transport

import (
	"fmt"
	"sort"
	"strings"

	"peerdrive/internal/log"
)

// capabilities.go — Frame protocol capability negotiation (Issue #213).
//
// Background:
// Wire frame verbs (req/meta/data/done/err, share/share-resp, create/upload/list/search/info/delete/sync,
// pull, fwd-open/challenge/auth/ok/err/data/close) historically relied solely on omitempty for compatibility.
// Older clients silently drop unknown verbs without recognizing that the remote protocol is newer or incompatible.
//
// In Phase 7 (challenge-auth / encryption negotiation), silent frame drops become fatal:
// nodes cannot express unsupported capabilities or perform graceful degradation.
//
// Mechanism:
// 1. Bit set / string set capability identifiers (non-monotonic versioning).
// 2. Both sides announce supported capabilities during open handshake.
// 3. Negotiated capabilities are the intersection: local ∩ remote.
// 4. Unknown capability identifiers are strictly discarded (forward-compatible).
// 5. Omission of capability declarations defaults to MinimalCapabilities, preserving existing client compatibility.
// 6. Capability mismatches (when specific required capabilities are unmet) explicitly reject or degrade with machine-readable error codes.

const (
	// CapReq allows content-addressed file fetch and streaming: req, meta, data, done, err.
	CapReq = "req"
	// CapShare allows querying explicitly shared manifests: share, share-resp.
	CapShare = "share"
	// CapIndex allows SQLite file index management: create, upload, list, search, info, delete, sync.
	CapIndex = "index"
	// CapPull allows URL remote ingestion: pull, pulled.
	CapPull = "pull"
	// CapForward allows TCP port forwarding: fwd-open, fwd-challenge, fwd-auth, fwd-ok, fwd-err, fwd-data, fwd-close.
	CapForward = "fwd"
	// CapAuth reserved for Phase 7 identity / challenge-auth plugins.
	CapAuth = "auth"
	// CapAdmin allows remote control / administrative execution over authenticated channels (Issue #234).
	CapAdmin = "admin"
	// CapDisplay allows remote screen sync and public display screen control (Issue #243).
	CapDisplay = "display"
	// CapStream allows P2P live stream chunk broadcast and subscription based on file SHA (Issue #244).
	CapStream = "stream"
	// CapBT allows BitTorrent integration and transfers (Issue #263).
	CapBT = "bt"
	// CapIPFS allows IPFS bridge and content resolution (Issue #263).
	CapIPFS = "ipfs"
	// CapIwara allows Iwara integration and media fetching (Issue #263).
	CapIwara = "iwara"
)

// Machine-readable capability error codes.
const (
	ErrCodeCapUnsupported = "CAPABILITY_UNSUPPORTED"
	ErrCodeCapMismatch    = "CAPABILITY_MISMATCH"
)

// KnownCapabilities contains all valid capability identifiers in this implementation.
var KnownCapabilities = map[string]bool{
	CapReq:     true,
	CapShare:   true,
	CapIndex:   true,
	CapPull:    true,
	CapForward: true,
	CapAuth:    true,
	CapAdmin:   true,
	CapDisplay: true,
	CapStream:  true,
	CapBT:      true,
	CapIPFS:    true,
	CapIwara:   true,
}

// DefaultNodeCapabilities represents the full capability set of a standard peerdrive Go node.
var DefaultNodeCapabilities = []string{
	CapReq, CapShare, CapIndex, CapPull, CapDisplay, CapStream,
}

// MinimalCapabilities is the minimal feature baseline when capabilities are omitted in a handshake frame.
var MinimalCapabilities = []string{
	CapReq, CapShare,
}

// LegacyCapabilities represents the default capabilities assigned to legacy peers that send no capability frames.
var LegacyCapabilities = []string{
	CapReq, CapShare, CapIndex, CapPull, CapForward,
}

// CapBit bitmask representation of capabilities for bitwise operations.
type CapBit uint32

const (
	BitReq CapBit = 1 << iota
	BitShare
	BitIndex
	BitPull
	BitForward
	BitAuth
	BitAdmin
	BitDisplay
	BitStream
)

var capStringToBit = map[string]CapBit{
	CapReq:     BitReq,
	CapShare:   BitShare,
	CapIndex:   BitIndex,
	CapPull:    BitPull,
	CapForward: BitForward,
	CapAuth:    BitAuth,
	CapAdmin:   BitAdmin,
	CapDisplay: BitDisplay,
	CapStream:  BitStream,
}

var capBitToString = map[CapBit]string{
	BitReq:     CapReq,
	BitShare:   CapShare,
	BitIndex:   CapIndex,
	BitPull:    CapPull,
	BitForward: CapForward,
	BitAuth:    CapAuth,
	BitAdmin:   CapAdmin,
	BitDisplay: CapDisplay,
	BitStream:  CapStream,
}

// CapsToBitset converts a slice of capability strings into a CapBit bitmask.
func CapsToBitset(caps []string) CapBit {
	var mask CapBit
	for _, c := range caps {
		if b, ok := capStringToBit[c]; ok {
			mask |= b
		}
	}
	return mask
}

// CapsFromBitset converts a CapBit bitmask into a sorted slice of capability strings.
func CapsFromBitset(mask CapBit) []string {
	var res []string
	for bit, name := range capBitToString {
		if mask&bit != 0 {
			res = append(res, name)
		}
	}
	sort.Strings(res)
	return res
}

// FilterKnown filters out any unknown capability strings ("未知段落严格丢弃").
func FilterKnown(caps []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, c := range caps {
		c = strings.TrimSpace(c)
		if KnownCapabilities[c] && !seen[c] {
			seen[c] = true
			res = append(res, c)
		}
	}
	sort.Strings(res)
	return res
}

// Intersect computes the capability intersection of two string sets ("取交集协商").
func Intersect(a, b []string) []string {
	setA := CapsToMap(a)
	var res []string
	for _, c := range b {
		c = strings.TrimSpace(c)
		if setA[c] {
			res = append(res, c)
			delete(setA, c) // prevent duplicates
		}
	}
	sort.Strings(res)
	return res
}

// CapsToMap converts a slice of capability strings into a lookup map.
func CapsToMap(caps []string) map[string]bool {
	m := make(map[string]bool, len(caps))
	for _, c := range caps {
		c = strings.TrimSpace(c)
		if c != "" {
			m[c] = true
		}
	}
	return m
}

// CapsFromMap converts a capability map into a sorted slice.
func CapsFromMap(m map[string]bool) []string {
	res := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			res = append(res, k)
		}
	}
	sort.Strings(res)
	return res
}

// CheckRequirements verifies if negotiated capabilities satisfy all required capabilities.
func CheckRequirements(negotiated, required []string) (missing []string, ok bool) {
	negMap := CapsToMap(negotiated)
	for _, req := range required {
		req = strings.TrimSpace(req)
		if req != "" && !negMap[req] {
			missing = append(missing, req)
		}
	}
	return missing, len(missing) == 0
}

// VerbRequiredCap maps an incoming wire verb to its required capability identifier.
// Verbs with empty return strings do not require capability gating (e.g. handshake/control frames).
func VerbRequiredCap(verb string) string {
	switch verb {
	case "req":
		return CapReq
	case "share":
		return CapShare
	case "create", "upload", "list", "search", "info", "delete", "sync":
		return CapIndex
	case "pull":
		return CapPull
	case "fwd-open", "fwd-auth", "fwd-data", "fwd-close":
		return CapForward
	case "admin":
		return CapAdmin
	case "display":
		return CapDisplay
	case "stream":
		return CapStream
	default:
		return ""
	}
}

// SetLocalCapabilities overrides this node's declared capabilities.
func (s *PeerJSService) SetLocalCapabilities(caps []string) {
	s.capsMu.Lock()
	defer s.capsMu.Unlock()
	s.localCaps = FilterKnown(caps)
}

// LocalCapabilities returns a copy of this node's declared capabilities.
func (s *PeerJSService) LocalCapabilities() []string {
	s.capsMu.RLock()
	defer s.capsMu.RUnlock()
	return append([]string(nil), s.currentLocalCaps()...)
}

// computeLocalCaps builds the active capability set from node configuration (Issue #263).
func (s *PeerJSService) computeLocalCaps() []string {
	caps := []string{CapReq, CapShare, CapIndex, CapPull, CapDisplay, CapStream}
	if s.cfg != nil {
		if s.cfg.PortFwdEnable || s.cfg.ForwardRules != "" {
			caps = append(caps, CapForward)
		}
		if s.cfg.BTEnable || s.cfg.BTDHTEnabled {
			caps = append(caps, CapBT)
		}
		if s.cfg.IPFSEnable {
			caps = append(caps, CapIPFS)
		}
		if s.cfg.IwaraEnable {
			caps = append(caps, CapIwara)
		}
		if s.cfg.RemoteControlEnable {
			caps = append(caps, CapAdmin)
		}
	} else {
		// When cfg is nil (e.g. lightweight test stub), include default forward capability
		caps = append(caps, CapForward)
	}
	return caps
}

// currentLocalCaps returns the current local capabilities slice (falling back to computeLocalCaps).
func (s *PeerJSService) currentLocalCaps() []string {
	if len(s.localCaps) > 0 {
		return s.localCaps
	}
	return s.computeLocalCaps()
}

// SetRequiredCapabilities sets strict capability requirements that remote peers must support.
// If negotiation fails to include all required capabilities, the handshake is rejected.
func (s *PeerJSService) SetRequiredCapabilities(caps []string) {
	s.capsMu.Lock()
	defer s.capsMu.Unlock()
	s.requiredCaps = FilterKnown(caps)
}

// RequiredCapabilities returns strict capability requirements.
func (s *PeerJSService) RequiredCapabilities() []string {
	s.capsMu.RLock()
	defer s.capsMu.RUnlock()
	return append([]string(nil), s.requiredCaps...)
}

// currentRequiredCaps returns strict capability requirements (caller must hold or not need lock).
func (s *PeerJSService) currentRequiredCaps() []string {
	return s.requiredCaps
}

// PeerCapabilities returns the negotiated capabilities with a connected peer.
func (s *PeerJSService) PeerCapabilities(peerID string) ([]string, bool) {
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return nil, false
	}
	if isSelfSession(conn) {
		return DefaultNodeCapabilities, true
	}
	st := s.stateFor(conn)
	if st == nil {
		return nil, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.caps == nil {
		return append([]string(nil), LegacyCapabilities...), true
	}
	return CapsFromMap(st.caps), true
}

// HasCapability checks whether a specific connection currently supports a capability.
func (s *PeerJSService) HasCapability(peerID string, cap string) bool {
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return false
	}
	if isSelfSession(conn) {
		return true // local session has all capabilities
	}
	st := s.stateFor(conn)
	if st == nil {
		return false
	}
	return st.hasCapability(cap)
}

// hasCapability checks if connState has the given capability (thread-safe).
func (st *connState) hasCapability(cap string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.caps == nil {
		return true // un-negotiated legacy node fallback
	}
	return st.caps[cap]
}

// capSendAnnouncement proactively presents this node's capabilities when opening a connection.
func (s *PeerJSService) capSendAnnouncement(c Session, st *connState) {
	if isSelfSession(c) || s.pskEnabled() {
		// Local sessions never announce; PSK mode piggybacks capabilities directly in psk-auth.
		return
	}
	st.mu.Lock()
	if st.capSent {
		st.mu.Unlock()
		return
	}
	st.capSent = true
	st.mu.Unlock()

	s.capsMu.RLock()
	caps := append([]string(nil), s.currentLocalCaps()...)
	s.capsMu.RUnlock()

	_ = c.SendJSON(dcResp{
		Type:         "cap",
		Capabilities: caps,
	})
	log.LogInfo("peerjs: cap announcement sent to %s: %v", c.ID(), caps)
}

// serveCap handles an incoming cap/open handshake frame and performs intersection negotiation.
func (s *PeerJSService) serveCap(c Session, st *connState, r dcResp) {
	if isSelfSession(c) {
		return
	}

	remoteRaw := r.Capabilities
	if len(remoteRaw) == 0 && len(r.Caps) > 0 {
		remoteRaw = r.Caps
	}

	s.negotiateCapabilities(c, st, remoteRaw, r.ReqID)
}

// negotiateCapabilities computes intersection, validates requirements, and stores negotiated state.
func (s *PeerJSService) negotiateCapabilities(c Session, st *connState, remoteRaw []string, reqID string) {
	var effectiveRemote []string
	if remoteRaw == nil {
		// "字段省略视为最简功能集，不破坏现有 client"
		effectiveRemote = MinimalCapabilities
	} else {
		// "未知段落严格丢弃"
		effectiveRemote = FilterKnown(remoteRaw)
	}

	s.capsMu.RLock()
	local := s.currentLocalCaps()
	reqs := s.currentRequiredCaps()
	s.capsMu.RUnlock()

	negotiated := Intersect(local, effectiveRemote)

	// Check if required capabilities are satisfied
	if missing, ok := CheckRequirements(negotiated, reqs); !ok {
		log.LogWarn("peerjs: capability mismatch with %s, missing required: %v", c.ID(), missing)
		_ = c.SendJSON(dcResp{
			Type:  "err",
			Code:  ErrCodeCapMismatch,
			Msg:   fmt.Sprintf("capability mismatch: missing required %v", missing),
			ReqID: reqID,
		})
		c.Close()
		return
	}

	st.mu.Lock()
	st.capNegotiated = true
	st.caps = CapsToMap(negotiated)
	alreadySent := st.capSent
	st.mu.Unlock()

	log.LogInfo("peerjs: capabilities negotiated with %s: %v (remote declared: %v)", c.ID(), negotiated, effectiveRemote)

	// Symmetric handshake: if we haven't sent our capabilities yet or peer provided reqId, reply with cap.
	if !alreadySent || reqID != "" {
		st.mu.Lock()
		st.capSent = true
		st.mu.Unlock()
		_ = c.SendJSON(dcResp{
			Type:         "cap",
			Capabilities: local,
			ReqID:        reqID,
		})
	}
}
