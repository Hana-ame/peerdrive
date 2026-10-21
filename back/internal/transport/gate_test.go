package transport

// gate_test.go: Behavioral contracts for admission, access control, and identity gates (gate.go).
//
// Covers:
//   - Part 1: PSK admission gate contracts & timing invariants
//   - Part 2: Session locality gate (isSelfSession)
//   - Part 3: Share download gate (ShareGate)
//   - Part 4: Identity & authorization gate (Authorizer & DefaultAuthorizer)

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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

// tokenAuthorizer is a mock Phase 7 Authorizer that grants access if token matches expected.
type tokenAuthorizer struct {
	expectedToken string
}

func (a *tokenAuthorizer) AuthorizeDownload(session Session, hash string, token string) bool {
	if isSelfSession(session) {
		return true
	}
	return token == a.expectedToken
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
// 发现背景 (2026-09-21, CI panel E2E flaky red): OnMessage 必须在 pskSendAuth 之前挂载。
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
func TestPSK_SpoofedLocalIDNotExempt(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "local", local: false}
	svc.bindConn(sess)

	sentTypes := sess.sentTypes()
	require.NotEmpty(t, sentTypes)
	assert.Equal(t, "psk-auth", sentTypes[0], "spoofed local-id session must still present PSK")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	foundErr := false
	for _, f := range sess.sentFrames()[1:] {
		if f.header["type"] == "err" {
			foundErr = true
			assert.Equal(t, "PSK_REQUIRED", f.header["code"])
		}
	}
	assert.True(t, foundErr, "spoofed local-id session must be gated by PSK")
}

// TestPSK_UnauthedConnectionClosedAfterTimeout
// 发现背景：C-13（审计）— 未认证连接无超时，资源耗尽向量。
func TestPSK_UnauthedConnectionClosedAfterTimeout(t *testing.T) {
	old := pskAuthTimeout
	pskAuthTimeout = 100 * time.Millisecond
	defer func() { pskAuthTimeout = old }()

	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	time.Sleep(200 * time.Millisecond)
	assert.True(t, sess.Closed(), "unauthed session must be closed after pskAuthTimeout")
}

// TestPSK_PreBindConnFrameNotDropped
// 发现背景：CI 面板 E2E（run 37911874004）—— OnMessage 在 OnOpen 之前注册，
// 即使 psk-auth 帧在 bindConnPrepared 之前到达也能被正确接收并回复 psk-ok。
func TestPSK_PreBindConnFrameNotDropped(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"

	sess := &fakeSession{id: "peer-x"}
	st := &connState{
		fetches:   make(map[string]*fetchState),
		verbWaits: make(map[string]chan []byte),
		binCh:     make(chan binaryChunk, 16),
		binDone:   make(chan struct{}),
		fwdCh:     make(chan fwdChunk, 16),
	}
	sess.OnMessage(func(msg peerjs.Frame) { svc.dispatchFrame(sess, st, msg) })
	sess.OnClose(func() { svc.cleanupConn(sess, st) })

	sess.onMessage(pskFrame(t, map[string]any{"type": "psk-auth", "psk": "s3cret"}))
	svc.bindConnPrepared(sess, st, nil)

	_, ok := waitSent(sess, "psk-ok", 2*time.Second)
	assert.True(t, ok, "psk-auth must be received and psk-ok sent, even if frame arrived before bindConnPrepared")
}

// TestAuthorizer_DefaultBehavior verifies that DefaultAuthorizer faithfully reproduces
// the pre-Phase 7 gate behavior (ShareGate + local WS session check).
// 发现背景：Phase 7 预埋 Authorizer 接口，默认必须 100% 行为等价于原 ShareGate，不得影响未接入 Phase 7 的现有节点。
func TestAuthorizer_DefaultBehavior(t *testing.T) {
	initTestDB(t)
	svc := newShareTestService(t)
	content := []byte("default-authorizer-content")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	dlDir := t.TempDir()
	svc.cfg.DownloadDir = dlDir
	svc.fileIndex = NewFileIndexService(dlDir)
	t.Cleanup(svc.fileIndex.Close)

	inRoot := filepath.Join(dlDir, "test-file.txt")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	_, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	svc.SetShareGate(fakeShareGate{private: hash, friends: []string{"friend-peer"}})

	stranger := &fakeSession{id: "stranger-peer"}
	svc.serveFile(stranger, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"err"}, stranger.sentTypes(), "stranger should be denied for private file")

	friend := &fakeSession{id: "friend-peer"}
	svc.serveFile(friend, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r2"})
	require.Equal(t, []string{"meta", "data", "done"}, friend.sentFrameTypes(), "friend should be allowed")

	self := &fakeSession{id: "local-client", local: true}
	svc.serveFile(self, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r3"})
	require.Equal(t, []string{"meta", "data", "done"}, self.sentFrameTypes(), "local session should always be allowed")
}

// TestAuthorizer_CustomAuthorizerPluginPoint verifies that SetAuthorizer allows injecting
// a custom token-based authorizer without modifying the transport serveFile dispatch logic.
// 发现背景：Phase 7 (Identity) 到来时需直接注入身份验证逻辑，验证此注入点是否有效支持 token 鉴权与重置回退。
func TestAuthorizer_CustomAuthorizerPluginPoint(t *testing.T) {
	initTestDB(t)
	svc := newShareTestService(t)
	content := []byte("custom-authorizer-content")
	sum := sha256.Sum256(content)
	hash := hex.EncodeToString(sum[:])

	dlDir := t.TempDir()
	svc.cfg.DownloadDir = dlDir
	svc.fileIndex = NewFileIndexService(dlDir)
	t.Cleanup(svc.fileIndex.Close)

	inRoot := filepath.Join(dlDir, "token-file.txt")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	_, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	svc.SetAuthorizer(&tokenAuthorizer{expectedToken: "valid-token-123"})

	p1 := &fakeSession{id: "any-peer"}
	svc.serveFile(p1, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"err"}, p1.sentTypes(), "peer without token should be denied")

	p2 := &fakeSession{id: "any-peer"}
	svc.serveFile(p2, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r2", Token: "wrong-token"})
	require.Equal(t, []string{"err"}, p2.sentTypes(), "peer with wrong token should be denied")

	p3 := &fakeSession{id: "any-peer"}
	svc.serveFile(p3, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r3", Token: "valid-token-123"})
	require.Equal(t, []string{"meta", "data", "done"}, p3.sentFrameTypes(), "peer with valid token should be allowed")

	svc.SetAuthorizer(nil)
	p4 := &fakeSession{id: "any-peer"}
	svc.serveFile(p4, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r4"})
	require.Equal(t, []string{"meta", "data", "done"}, p4.sentFrameTypes(), "without gate configured, default authorizer allows public content")
}

// TestShare_TokenPluginPoints verifies requester token propagation in share frames
// and responder token generation in share-resp frames.
// 发现背景：Phase 7 中 share 帧需要支持可选的 requester token 及响应的身份标识，验证此注入点可用性。
func TestShare_TokenPluginPoints(t *testing.T) {
	svc := newShareTestService(t)

	var receivedPeerID, receivedToken string
	svc.SetShareProviderWithToken(func(peerID, token string) ShareSnapshot {
		receivedPeerID = peerID
		receivedToken = token
		if token == "secret-pass" {
			return ShareSnapshot{
				Files: []ShareFileInfo{{Hash: "hash-secret", Name: "secret.txt", Size: 42}},
			}
		}
		return ShareSnapshot{}
	})

	svc.SetShareTokenGenerator(func(peerID string) string {
		return "responder-identity-token"
	})

	sess := &fakeSession{id: "requester-node"}
	svc.serveShare(sess, dcResp{Type: "share", ReqID: "s1", Token: "secret-pass"})

	require.Equal(t, "requester-node", receivedPeerID)
	require.Equal(t, "secret-pass", receivedToken)

	frames := sess.sentFrames()
	require.Len(t, frames, 1)
	header := frames[0].header
	require.Equal(t, "share-resp", header["type"])
	require.Equal(t, "responder-identity-token", header["token"])
	require.Equal(t, float64(1), header["total"])
}

// TestCredentialFunc_PluginPoint verifies connection-level credentialFunc and service-level fallback.
// 发现背景：Phase 7 中 FetchFromPeer / OpenStreamFrom 需要支持连接级凭据附加，验证 credentialFunc 注入与回调。
func TestCredentialFunc_PluginPoint(t *testing.T) {
	svc := newShareTestService(t)

	peerConn := &fakeSession{id: "peer-target"}
	st := &connState{fetches: make(map[string]*fetchState), verbWaits: make(map[string]chan []byte)}
	svc.conns["peer-target"] = peerConn
	svc.pending[peerConn] = st

	cred := svc.credentialFor(peerConn, st)
	require.Empty(t, cred)

	svc.SetDefaultCredentialFunc(func(peerID string) string {
		return "default-token-for-" + peerID
	})
	credDefault := svc.credentialFor(peerConn, st)
	require.Equal(t, "default-token-for-peer-target", credDefault)

	svc.SetConnectionCredentialFunc("peer-target", func() string {
		return "connection-specific-token"
	})
	credConn := svc.credentialFor(peerConn, st)
	require.Equal(t, "connection-specific-token", credConn)
}
