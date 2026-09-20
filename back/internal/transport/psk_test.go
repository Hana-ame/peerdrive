package transport

// psk_test.go：预共享密钥门禁（psk.go）的行为契约。
//
// 为什么单列一个文件：门禁的失败模式是**静默的**——最坏的情况不是报错，
// 而是「忘了拦」或「拦错了对象」（把自己的应答也拦了），两者在真实网络里
// 都表现为"莫名超时"，排查成本极高。所以这里把放行/拦截的边界逐条钉死。

import (
	"encoding/json"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pskFrame 造一个文本帧（同 DataChannel 上的 JSON 头）。
func pskFrame(t *testing.T, v map[string]any) peerjs.Frame {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return peerjs.Frame{IsText: true, Data: b}
}

// waitSent 等某类型帧出现（serveShare 等 verb 是 go 出去的，异步）。
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

// TestPSK_OpenModeNoGate 没配 PSK = 开放模式：对端不出示密钥也能问 share。
// 这是向后兼容的底线——存量节点升级后不该把已有对端全拒了。
func TestPSK_OpenModeNoGate(t *testing.T) {
	svc := newTestPeerJSService(t) // cfg.PeerPSK == ""
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))

	_, ok := waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "开放模式必须照常应答 share")
	for _, f := range sess.sentFrames() {
		assert.NotEqual(t, "err", f.header["type"], "开放模式不该回 err")
	}
}

// TestPSK_RejectVerbBeforeAuth 配了 PSK：出示之前所有入站 verb 一律回 err，
// 且 err 带 code=PSK_REQUIRED（消费端靠 code 提示"请填密钥"，不要匹配文案）。
func TestPSK_RejectVerbBeforeAuth(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	for _, verb := range []string{"share", "req", "list", "fwd-open"} {
		svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": verb, "reqId": "r1"}))
	}
	types := sess.sentTypes()
	// 第一个是本端出示的 auth（bindConn 发的），其后每个 verb 各一个 err
	require.Len(t, types, 5, "auth + 4 个 err，实际：%v", types)
	assert.Equal(t, "psk-auth", types[0])
	for _, got := range types[1:] {
		assert.Equal(t, "err", got)
	}
	for _, f := range sess.sentFrames()[1:] {
		assert.Equal(t, "PSK_REQUIRED", f.header["code"], "err 帧必须带机器可读的 code")
	}
}

// TestPSK_AuthThenServe 出示正确密钥 → psk-ok，之后同一条连接上的 verb 放行。
func TestPSK_AuthThenServe(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "psk-auth", "psk": "s3cret"}))
	okFrame, ok := waitSent(sess, "psk-ok", 2*time.Second)
	require.True(t, ok, "正确密钥必须回 psk-ok")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok = waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "通过门禁后 share 必须被应答")
	_ = okFrame
}

// TestPSK_WrongKeyStillGated 错密钥 → psk-err，且门禁不开（不是"错一次就放过"）。
func TestPSK_WrongKeyStillGated(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "psk-auth", "psk": "guess"}))
	_, ok := waitSent(sess, "psk-err", 2*time.Second)
	require.True(t, ok, "错密钥必须回 psk-err")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok = waitSent(sess, "err", 2*time.Second)
	assert.True(t, ok, "错密钥之后 verb 仍须被拦")
}

// TestPSK_ResponseFramesNotGated 门禁只拦「对端要我干活」的 verb，不拦
// «对端对我请求的应答»。
// 发现背景（设计期自查）：如果对 default 分支也上锁，会出现「对端开放、本端
// 配了 PSK」时**我自己的拉取**被自己掐死——对端从没出示过密钥（它不需要），
// 于是 meta/data/done 全被丢掉，拉取只剩超时，且日志里看不出所以然。
func TestPSK_ResponseFramesNotGated(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	before := len(sess.sentFrames())

	for _, typ := range []string{"meta", "done", "share-resp"} {
		svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": typ, "reqId": "r1"}))
	}
	assert.Len(t, sess.sentFrames(), before, "应答帧不得触发门禁 err（拦了=自伤）")
}

// TestPSK_LocalSessionExempt local（浏览器管理台 WS 会话）豁免门禁：它走本机，
// 让它先出示密钥等于把管理台锁死。
func TestPSK_LocalSessionExempt(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	sess := &fakeSession{id: "local"}
	svc.bindConn(sess)

	assert.Empty(t, sess.sentFrames(), "local 会话不该出示密钥")
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok := waitSent(sess, "share-resp", 2*time.Second)
	assert.True(t, ok, "local 会话的 verb 不受门禁影响")
}

// TestPSK_AuthIsFirstFrame bindConn 时出示必须是本端第一帧：顺序即语义
// （出示方不等 psk-ok 就发业务帧，靠的就是这条保序）。
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

// TestPSKState_ExposedForStatus GET /peerjs/node 用它显示门禁状态：
// 开启标志 + 已通过的连接数（"对端拉不到"第一个要查的就是这个）。
func TestPSKState_ExposedForStatus(t *testing.T) {
	svc := newTestPeerJSService(t)
	enabled, n := svc.PSKState()
	assert.False(t, enabled, "没配 PSK 时状态必须是关闭")
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
	assert.Equal(t, 1, n, "只有 a 通过了门禁，b 还没出示")
}

// messageHandler 取当前挂着的入站回调（nil = 还没挂；库在 nil 时会**丢弃**整帧）。
func (f *fakeSession) messageHandler() func(peerjs.Frame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.onMessage
}

// pskRaceSession 建模「入站帧与本端出示帧并发」这个真实形状：
// 第一次 SendJSON（bindConn 里的 psk-auth）时，同步把对端的 psk-auth 投递给
// 已注册的 OnMessage —— 真机上 dc.OnOpen（跑 bindConn）与 dc.OnMessage 是两条
// 可并发的回调，发送又可能让出，所以对端在 open 那一刻发出的第一帧完全可能
// 赶在注册之前到达。
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

// TestPSK_AuthArrivingDuringBindIsNotDropped 对端在 bindConn 期间送达的
// psk-auth **不许被丢**。
//
// 发现背景（2026-09-21，CI 面板 E2E 偶发红）：现象是面板明明带了 psk 却一直
// 收到「本节点需要预共享密钥」，节点日志里只有自己发出的 psk-auth，既没有
// psk ok 也没有 mismatch —— 即对端那帧根本没被看见。根因是 bindConn 里
// OnMessage 挂在 pskSendAuth **之后**，而库对 nil 回调的处理是静默丢弃。
// 这个用例是竞态形状的：把 OnMessage 挪回发送之后，delivered 会是 false
// 且整条连接永远卡在门禁上（后续 verb 全被拒，且不报错）。
func TestPSK_AuthArrivingDuringBindIsNotDropped(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "s3cret"
	inner := &fakeSession{id: "peer-x"}
	sess := &pskRaceSession{fakeSession: inner}
	svc.bindConn(sess)

	require.True(t, sess.delivered,
		"bindConn 期间送达的 psk-auth 必须被收到：OnMessage 要先于任何发送挂上，否则整帧被丢弃")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "share", "reqId": "r1"}))
	_, ok := waitSent(inner, "share-resp", 2*time.Second)
	assert.True(t, ok, "收到 auth 之后 share 必须被应答，而不是永远卡在门禁上")
}
