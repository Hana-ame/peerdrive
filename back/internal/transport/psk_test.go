package transport

// psk_test.go: Behavioral contract for the pre-shared key gate (psk.go).
//
// Why a separate file: the gate's failure mode is *silent* -- the worst case is not
// an error, but "forgot to block" or "blocked the wrong thing" (blocking our own
// responses). Both manifest as "mysterious timeouts" in a real network and are
// extremely costly to debug. So here we nail down every pass/block boundary.

import (
	"encoding/json"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pskFrame builds a text frame (same JSON header as on a DataChannel).
func pskFrame(t *testing.T, v map[string]any) peerjs.Frame {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return peerjs.Frame{IsText: true, Data: b}
}

// waitSent waits for a frame of a given type (serveShare and similar verbs are
// goroutines, so delivery is async).
func waitSent(s *fakeSession, typ string, d time.Duration) (map[string]any, bool) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, f := range s.sentFrames() {
			if f.header["type"] == typ {
				return f.header, true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, false
}

// TestPSK_OpenModeNoGate No PSK configured = open mode: a peer that never presents
// a key can still ask for a share. This is the backward-compatibility baseline --
// after an upgrade, existing peers should not all be rejected.
func TestPSK_OpenModeNoGate(t *testing.T) {
	svc := newTestPeerJSService(t) // cfg.PeerPSK == ""
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))

	_, ok := waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "open mode must respond to share as normal")
	for _, f := range sess.sentFrames() {
		assert.NotEqual(t, "err", f.header["type"], "open mode should not reply err")
	}
}

// TestPSK_RejectVerbBeforeAuth With PSK configured: all inbound verbs before
// authentication get err, and err carries code=PSK_REQUIRED (the client uses
// the code to prompt "enter key" -- do not match on message text).
func TestPSK_RejectVerbBeforeAuth(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	for _, verb := range []string{"share", "req", "list", "fwd-open"} {
		svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": verb, "reqId": "r1"}))
	}
	types := sess.sentTypes()
	// First one is our own auth (sent by bindConn), then one err per verb
	require.Len(t, types, 5, "auth + 4 err, actual: %v", types)
	assert.Equal(t, "psk-auth", types[0])
	for _, got := range types[1:] {
		assert.Equal(t, "err", got)
	}
	for _, f := range sess.sentFrames()[1:] {
		assert.Equal(t, "PSK_REQUIRED", f.header["code"], "err frame must carry a machine-readable code")
	}
}

// TestPSK_AuthThenServe Presenting the correct key -> psk-ok, after which verbs
// on the same connection are allowed.
func TestPSK_AuthThenServe(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "psk-auth", "psk": "s3cret"}))
	okFrame, ok := waitSent(sess, "psk-ok", 2*time.Second)
	require.True(t, ok, "correct key must reply psk-ok")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok = waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "after passing the gate, share must be answered")
	_ = okFrame
}

// TestPSK_WrongKeyStillGated Wrong key -> psk-err, and the gate stays closed
// (not "let through after one wrong attempt").
func TestPSK_WrongKeyStillGated(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "psk-auth", "psk": "guess"}))
	_, ok := waitSent(sess, "psk-err", 2*time.Second)
	require.True(t, ok, "wrong key must reply psk-err")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok = waitSent(sess, "err", 2*time.Second)
	assert.True(t, ok, "after a wrong key, verbs must still be blocked")
}

// TestPSK_ResponseFramesNotGated The gate only blocks verbs where "the peer asks
// me to do work", not "my responses to the peer's requests".
// Discovery background (design-time self-audit): if we also locked down the default
// branch, then when "peer is open, we have PSK configured" our own pulls would be
// killed by ourselves -- the peer never presented a key (it doesn't need to), so
// all meta/data/done would be dropped, leaving the pull with only timeouts and no
// useful trace in the logs.
func TestPSK_ResponseFramesNotGated(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	before := len(sess.sentFrames())

	for _, typ := range []string{"meta", "done", "share-resp"} {
		svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": typ, "reqId": "r1"}))
	}
	assert.Len(t, sess.sentFrames(), before, "response frames must not trigger gate err (blocking = self-harm)")
}

// TestPSK_LocalSessionExempt Local (browser admin panel WS session) is exempt
// from the gate: it runs on localhost, and requiring it to present a key first
// would lock the admin panel out.
func TestPSK_LocalSessionExempt(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "local", local: true}
	svc.bindConn(sess)

	assert.Empty(t, sess.sentFrames(), "local session should not present a key")
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok := waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "local session verbs are not affected by the gate")
}

// TestPSK_AuthIsFirstFrame Auth presented during bindConn must be our first
// frame: order is semantics (the presenter sends business frames without waiting
// for psk-ok, relying on this ordering guarantee).
func TestPSK_AuthIsFirstFrame(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	frames := sess.sentFrames()
	require.NotEmpty(t, frames)
	assert.Equal(t, "psk-auth", frames[0].header["type"])
	assert.Equal(t, "s3cret", frames[0].header["psk"])
}

// TestPSKState_ExposedForStatus GET /peerjs/node uses it to display gate status:
// enabled flag + count of connections that have passed ("peer can't pull" is the
// first thing to check here).
func TestPSKState_ExposedForStatus(t *testing.T) {
	svc := newTestPeerJSService(t)
	enabled, n := svc.PSKState()
	assert.False(t, enabled, "when no PSK is set, status must be disabled")
	assert.Equal(t, 0, n)

	svc.cfg.PeerPSK = "s3cret"
	a := &fakeSession{id: "peer-a"}
	b := &fakeSession{id: "peer-b"}
	svc.bindConn(a)
	svc.bindConn(b)
	svc.dispatchFrame(a, svc.pending[a], pskFrame(t, map[string]any{"type": "psk-auth", "psk": "s3cret"}))
	_, ok := waitSent(a, "psk-ok", 2*time.Second)
	require.True(t, ok)

	enabled, n = svc.PSKState()
	assert.True(t, enabled)
	assert.Equal(t, 1, n, "only a passed the gate, b has not presented yet")
}

// messageHandler retrieves the currently attached inbound callback (nil = not
// attached yet; the library *drops* the entire frame on nil).
func (f *fakeSession) messageHandler() func(peerjs.Frame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.onMessage
}

// pskRaceSession models the real shape of "inbound frame arrives concurrently
// with our auth frame": on the first SendJSON (the psk-auth inside bindConn),
// synchronously deliver the peer's psk-auth to the already-registered OnMessage.
// On a real device, dc.OnOpen (running bindConn) and dc.OnMessage are two
// concurrent callbacks, and sending may yield, so the peer's very first frame sent
// at the moment of open can arrive before registration completes.
type pskRaceSession struct {
	*fakeSession
	fired     bool
	delivered bool
}

func (s *pskRaceSession) SendJSON(v any) error {
	err := s.fakeSession.SendJSON(v)
	if !s.fired {
		s.fired = true
		if h := s.fakeSession.messageHandler(); h != nil {
			s.delivered = true
			b, _ := json.Marshal(map[string]any{"type": "psk-auth", "psk": "s3cret"})
			h(peerjs.Frame{IsText: true, Data: b})
		}
	}
	return err
}

// TestPSK_AuthArrivingDuringBindIsNotDropped A psk-auth that arrives during
// bindConn must **not** be dropped.
//
// Discovery background (2026-09-21, CI panel E2E flaky red): the symptom was that
// the panel clearly sent the PSK but kept receiving "this node requires a pre-
// shared key". The node log only showed the psk-auth we sent -- no psk-ok and no
// mismatch, meaning the peer's frame was never seen. Root cause: inside bindConn,
// OnMessage was attached *after* pskSendAuth, and the library silently drops frames
// when the callback is nil. This test is a race condition: if OnMessage is moved
// back after sending, delivered will be false and the entire connection stays stuck
// on the gate forever (subsequent verbs are all rejected, with no error).
func TestPSK_AuthArrivingDuringBindIsNotDropped(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	inner := &fakeSession{id: "peer-x"}
	sess := &pskRaceSession{fakeSession: inner}
	svc.bindConn(sess)

	require.True(t, sess.delivered,
		"psk-auth arriving during bindConn must be received: OnMessage must be attached before any send, or the whole frame is dropped")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok := waitSent(inner, "share-resp", 2*time.Second)
	assert.True(t, ok, "after receiving auth, share must be answered, not stuck forever on the gate")
}

// TestPSK_SpoofedLocalIDNotExempt 发现背景：审计 A-9（2026-10-06）。
// 旧实现用 `c.ID() == "local"` 豁免 PSK 门禁；攻击者注册 ?id=local 即可跳过。
// 修后用 isSelfSession 类型断言，rtcSession 未实现 IsLocal() 故自动落空。
// 本用例构造 id="local" 但 local=false 的 fakeSession（模拟 WebRTC 对端自报 local），
// 断言 pskGate 仍然拦截 servedVerbs。
func TestPSK_SpoofedLocalIDNotExempt(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	// id="local" 模拟信令自报，但 local=false 表示非本地 WS 会话
	sess := &fakeSession{id: "local", local: false}
	svc.bindConn(sess)

	// pskSendAuth 应该被调用（因为 isSelfSession 返回 false）
	sentTypes := sess.sentTypes()
	require.NotEmpty(t, sentTypes)
	assert.Equal(t, "psk-auth", sentTypes[0], "spoofed local-id session must still present PSK")

	// servedVerbs 应该被 pskGate 拦截
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	types := sess.sentTypes()
	foundErr := false
	for _, f := range sess.sentFrames()[1:] {
		if f.header["type"] == "err" {
			foundErr = true
			assert.Equal(t, "PSK_REQUIRED", f.header["code"])
		}
	}
	assert.True(t, foundErr, "spoofed local-id session must be gated by PSK")
}
