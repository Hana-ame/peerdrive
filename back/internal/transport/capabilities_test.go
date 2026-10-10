package transport

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// capabilities_test.go — Frame protocol capability negotiation test suite (Issue #213).

// TestCapabilities_BitsetConversion verifies round-trip conversion between capability strings and bitmasks.
// 发现背景：Issue #213（帧协议 capability 协商：open 握手加功能位集合（非版本号），取交集降级）。
func TestCapabilities_BitsetConversion(t *testing.T) {
	input := []string{CapReq, CapShare, CapForward}
	mask := CapsToBitset(input)
	assert.Equal(t, BitReq|BitShare|BitForward, mask)

	recovered := CapsFromBitset(mask)
	assert.Equal(t, []string{CapForward, CapReq, CapShare}, recovered) // sorted

	// Empty
	assert.Equal(t, CapBit(0), CapsToBitset(nil))
	assert.Empty(t, CapsFromBitset(0))
}

// TestCapabilities_FilterKnownAndIntersection verifies unknown capabilities are discarded and intersections computed.
// 发现背景：Issue #213（未知段落严格丢弃；双方各自声明能力，握手时取交集协商）。
func TestCapabilities_FilterKnownAndIntersection(t *testing.T) {
	// Unknown identifiers discarded
	input := []string{"future_quantum_x", CapReq, "unknown_v9", CapShare, CapReq}
	filtered := FilterKnown(input)
	assert.Equal(t, []string{CapReq, CapShare}, filtered)

	// Intersection
	local := []string{CapReq, CapShare, CapIndex, CapForward}
	remote := []string{CapReq, CapShare, CapPull, "alien_cap"}
	intersected := Intersect(local, remote)
	assert.Equal(t, []string{CapReq, CapShare}, intersected)
}

// TestCapabilities_NegotiationBetweenNodes verifies symmetrical capability negotiation between two nodes.
// 发现背景：Issue #213（open 握手阶段双方各自声明能力，协商出交集集合）。
func TestCapabilities_NegotiationBetweenNodes(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-b"}
	svc.bindConn(sess)

	// Peer B declares a subset: req and share
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"cap","capabilities":["req","share"]}`),
	})

	caps, ok := svc.PeerCapabilities("peer-b")
	require.True(t, ok)
	assert.Equal(t, []string{CapReq, CapShare}, caps)
	assert.True(t, svc.HasCapability("peer-b", CapReq))
	assert.True(t, svc.HasCapability("peer-b", CapShare))
	assert.False(t, svc.HasCapability("peer-b", CapForward))
	assert.False(t, svc.HasCapability("peer-b", CapIndex))
}

// TestCapabilities_OmissionDefaultsToMinimal verifies omitting the capabilities field defaults to MinimalCapabilities.
// 发现背景：Issue #213（字段省略视为最简功能集，不破坏现有 client）。
func TestCapabilities_OmissionDefaultsToMinimal(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-minimal"}
	svc.bindConn(sess)

	// Frame without capabilities field
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"cap"}`),
	})

	caps, ok := svc.PeerCapabilities("peer-minimal")
	require.True(t, ok)
	assert.Equal(t, []string{CapReq, CapShare}, caps, "omitted capabilities field must resolve to MinimalCapabilities")
	assert.True(t, svc.HasCapability("peer-minimal", CapReq))
	assert.True(t, svc.HasCapability("peer-minimal", CapShare))
	assert.False(t, svc.HasCapability("peer-minimal", CapForward))
}

// TestCapabilities_UnsupportedVerbExplicitRejection verifies that attempting a verb lacking negotiated capability returns CAPABILITY_UNSUPPORTED.
// 发现背景：Issue #213（能力不一致时握手能明确降级或拒绝，而非静默丢帧）。
func TestCapabilities_UnsupportedVerbExplicitRejection(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-restricted"}
	svc.bindConn(sess)

	// Negotiate down to req and share only
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"cap","capabilities":["req","share"]}`),
	})

	// Attempt verb requiring "fwd" (e.g. fwd-open)
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"fwd-open","port":8080,"reqId":"r-fwd"}`),
	})

	// Must explicitly return error frame with CAPABILITY_UNSUPPORTED
	var errFrame map[string]any
	for _, f := range sess.sentFrames() {
		if f.header["type"] == "err" && f.header["reqId"] == "r-fwd" {
			errFrame = f.header
			break
		}
	}
	require.NotNil(t, errFrame, "unsupported capability verb must return explicit err frame instead of silent drop")
	assert.Equal(t, ErrCodeCapUnsupported, errFrame["code"])
	assert.Contains(t, errFrame["msg"], "fwd")
}

// TestCapabilities_OutboundRejection verifies that outbound requests reject early if remote lacks the required capability.
// 发现背景：Issue #213（客户端主动发起请求时提前根据协商能力降级或报错，不盲发超时帧）。
func TestCapabilities_OutboundRejection(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-nofwd"}
	svc.bindConn(sess)

	// Downgrade connection to only req and share
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"cap","capabilities":["req","share"]}`),
	})

	// 1. OpenForward requires CapForward
	_, err := svc.OpenForward(svc.ctx, "peer-nofwd", "secret", 8080)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `lacks capability "fwd"`)

	// 2. Outbound verb requiring index (e.g. "list")
	_, err = svc.requestVerbPayload("peer-nofwd", dcReq{Type: "list"}, 1*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `lacks capability "index"`)
}

// TestCapabilities_StrictRequirementsMismatch verifies that unmet required capabilities trigger explicit handshake rejection.
// 发现背景：Issue #213（能力不一致时握手能明确降级或拒绝，而非静默丢帧）。
func TestCapabilities_StrictRequirementsMismatch(t *testing.T) {
	svc := newTestPeerJSService(t)
	// Service requires "auth" capability
	svc.SetRequiredCapabilities([]string{CapReq, CapAuth})

	sess := &fakeSession{id: "peer-noauth"}
	svc.bindConn(sess)

	// Remote peer only announces basic capabilities
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"cap","capabilities":["req","share"]}`),
	})

	// Node must explicitly return CAPABILITY_MISMATCH and close connection
	var errFrame map[string]any
	for _, f := range sess.sentFrames() {
		if f.header["type"] == "err" && f.header["code"] == ErrCodeCapMismatch {
			errFrame = f.header
			break
		}
	}
	require.NotNil(t, errFrame, "unmet required capability must return CAPABILITY_MISMATCH")
	assert.Contains(t, errFrame["msg"], "auth")
	assert.True(t, sess.closed, "connection must be closed on capability mismatch")
}

// TestCapabilities_LegacyClientCompatibility verifies that legacy peers without cap frames communicate without regression.
// 发现背景：Issue #213（新老 client 互通不回归）。
func TestCapabilities_LegacyClientCompatibility(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "legacy-peer"}
	svc.bindConn(sess)

	// Legacy peer sends no cap frame, immediately queries share
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"share","reqId":"r-legacy"}`),
	})

	_, ok := waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "legacy peer without cap frame must succeed on standard verbs without regression")
}

// TestCapabilities_LegacyOptOutDefault verifies that un-negotiated peers get
// the minimal capability set (opt-out default) rather than the full set.
// 发现背景：Issue #281（LegacyCapabilities 默认全功能 → 改 opt-out 最小集）。
// Legacy peers that omit the cap frame must NOT implicitly gain
// CapIndex/CapPull/CapForward — those require explicit negotiation.
func TestCapabilities_LegacyOptOutDefault(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "legacy-optout"}
	svc.bindConn(sess)

	// No cap frame sent — connection is un-negotiated.
	caps, ok := svc.PeerCapabilities("legacy-optout")
	require.True(t, ok)
	// Default must be the minimal set, NOT the old full set.
	assert.Equal(t, []string{CapReq, CapShare}, caps,
		"un-negotiated peer must get minimal capabilities, not full set")

	// Minimal capabilities (req, share) must be allowed.
	assert.True(t, svc.HasCapability("legacy-optout", CapReq))
	assert.True(t, svc.HasCapability("legacy-optout", CapShare))

	// Non-minimal capabilities must be denied to un-negotiated peers.
	assert.False(t, svc.HasCapability("legacy-optout", CapIndex),
		"CapIndex must NOT be granted to un-negotiated legacy peer")
	assert.False(t, svc.HasCapability("legacy-optout", CapPull),
		"CapPull must NOT be granted to un-negotiated legacy peer")
	assert.False(t, svc.HasCapability("legacy-optout", CapForward),
		"CapForward must NOT be granted to un-negotiated legacy peer")
	assert.False(t, svc.HasCapability("legacy-optout", CapAdmin),
		"CapAdmin must NOT be granted to un-negotiated legacy peer")
	assert.False(t, svc.HasCapability("legacy-optout", CapDisplay),
		"CapDisplay must NOT be granted to un-negotiated legacy peer")

	// Attempt an index verb — must be rejected.
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"list","reqId":"r-list"}`),
	})
	var errFrame map[string]any
	for _, f := range sess.sentFrames() {
		if f.header["type"] == "err" && f.header["reqId"] == "r-list" {
			errFrame = f.header
			break
		}
	}
	require.NotNil(t, errFrame, "legacy peer without cap must get explicit CAPABILITY_UNSUPPORTED for index verb")
	assert.Equal(t, ErrCodeCapUnsupported, errFrame["code"])
}

// TestCapabilities_PSKHandshakePiggyback verifies capabilities are negotiated inside psk-auth / psk-ok without extra frames.
// 发现背景：Issue #213（PSK 门禁模式下 capabilities 伴随 psk-auth 与 psk-ok 完成握手）。
func TestCapabilities_PSKHandshakePiggyback(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "topsecret"

	sess := &fakeSession{id: "psk-peer"}
	svc.bindConn(sess)

	// Outgoing psk-auth from our side must include capabilities
	require.NotEmpty(t, sess.sentFrames())
	assert.Equal(t, "psk-auth", sess.sentFrames()[0].header["type"])
	assert.NotEmpty(t, sess.sentFrames()[0].header["capabilities"])

	// Remote presents psk-auth with subset capabilities
	remoteAuth, _ := json.Marshal(map[string]any{
		"type":         "psk-auth",
		"psk":          "topsecret",
		"capabilities": []string{CapReq, CapShare},
	})
	svc.dispatchFrame(sess, svc.pending[sess], peerjs.Frame{IsText: true, Data: remoteAuth})

	// Response must be psk-ok containing our capabilities
	var okFrame map[string]any
	for _, f := range sess.sentFrames() {
		if f.header["type"] == "psk-ok" {
			okFrame = f.header
			break
		}
	}
	require.NotNil(t, okFrame)
	assert.NotEmpty(t, okFrame["capabilities"])

	// Negotiated capabilities must be [req, share]
	caps, ok := svc.PeerCapabilities("psk-peer")
	require.True(t, ok)
	assert.Equal(t, []string{CapReq, CapShare}, caps)
}

// TestCapabilities_RemoteControlAdminCapability verifies that CapAdmin is only advertised
// when RemoteControlEnable is true.
// 发现背景：Issue #234（遥控功能验证：RemoteControlEnable 开启时才在 capability 中通告 admin）。
func TestCapabilities_RemoteControlAdminCapability(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.RemoteControlEnable = false
	capsDisabled := svc.currentLocalCaps()
	assert.NotContains(t, capsDisabled, CapAdmin)

	svc.cfg.RemoteControlEnable = true
	capsEnabled := svc.currentLocalCaps()
	assert.Contains(t, capsEnabled, CapAdmin)
	assert.Equal(t, CapAdmin, VerbRequiredCap("admin"))
}

