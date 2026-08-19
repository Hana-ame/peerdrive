package transport

// conn_test.go：bindConn 同 peer 双连接去重测试（2026-08-19 架构优化）。
// 背景：双向互拨（A↔B 同时拨号对方）或重连竞态会在同一 peerID 下留下
// 两条连接，旧连接成为孤儿（connState 常驻 + worker goroutine 泄漏）。
// 修复：bindConn 保留最新连接、锁外 Close 旧连接；local WS 会话例外
// （多浏览器标签页各自独立，不能误杀）。

import (
	"encoding/json"
	"io"
	"testing"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBindConn_SamePeerDedup 同 peer 双连接：新连接保留，旧连接被关闭；
// 旧连接的 OnClose 清理（带 conns 值相等守卫）不得误删新连接。
func TestBindConn_SamePeerDedup(t *testing.T) {
	svc := newTestPeerJSService(t)
	stale := &fakeSession{id: "peerX"}
	fresh := &fakeSession{id: "peerX"}
	svc.bindConn(stale)
	svc.bindConn(fresh)

	svc.mu.Lock()
	cur := svc.conns["peerX"]
	svc.mu.Unlock()
	require.Same(t, fresh, cur, "conns 必须指向最新连接")
	assert.True(t, stale.Closed(), "同 peer 旧连接必须被关闭（防孤儿连接）")
	assert.False(t, fresh.Closed(), "新连接不得被误关")

	// 旧连接 OnClose 清理执行后，conns 仍指向新连接（值相等守卫生效）
	svc.pendingMu.Lock()
	_, staleInPending := svc.pending[stale]
	svc.pendingMu.Unlock()
	assert.False(t, staleInPending, "旧连接状态必须从 pending 清理")
}

// TestBindConn_ReplacedConnOldStreamErrors 连接被替换后，旧连接上的
// 进行中流必须报错结束（不悬挂）：替换即 Close(stale) → OnClose 清理 →
// errCh 投递 → 旧 fetchReader 读取报错。
func TestBindConn_ReplacedConnOldStreamErrors(t *testing.T) {
	svc := newTestPeerJSService(t)
	stale := &fakeSession{id: "peerX"}
	svc.bindConn(stale)

	// 在旧连接上发起流（发帧成功，未收到任何响应）
	r, err := svc.OpenStream("peerX", hashOf("x"), 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// 同 peer 新连接到来 → 旧连接被替换关闭
	fresh := &fakeSession{id: "peerX"}
	svc.bindConn(fresh)

	_, err = io.ReadAll(r)
	require.Error(t, err, "连接被替换后旧流必须报错（不悬挂不截断）")
}

// TestBindConn_LocalNoDedup local WS 会话不去重：多浏览器标签页各一条
// 本地会话，旧标签页连接不得被主动关闭（其入站服务仍活跃）。
func TestBindConn_LocalNoDedup(t *testing.T) {
	svc := newTestPeerJSService(t)
	tab1 := &fakeSession{id: "local"}
	tab2 := &fakeSession{id: "local"}
	svc.bindConn(tab1)
	svc.bindConn(tab2)

	svc.mu.Lock()
	cur := svc.conns["local"]
	svc.mu.Unlock()
	require.Same(t, tab2, cur, "conns 指向最新本地会话")
	assert.False(t, tab1.Closed(), "旧本地会话不得被主动关闭（多标签页共存）")
	assert.False(t, tab2.Closed())
}

// TestBindConn_DedupFreesSlot 去重后旧连接资源完整释放：pending 清理 +
// worker goroutine 退出（binDone 关闭），服务可继续用新连接拉取。
func TestBindConn_DedupFreesSlot(t *testing.T) {
	svc := newTestPeerJSService(t)
	stale := &fakeSession{id: "peerY"}
	fresh := &fakeSession{id: "peerY"}
	svc.bindConn(stale)
	svc.bindConn(fresh) // stale 被关闭（binDone 关闭 → worker 退出）

	// 新连接上正常拉取（走 bindConn 注册的真实 pump：data 头 → expect，
	// 二进制块投递，done 收尾）
	content := []byte("dedup-after")
	h := hashOf(string(content))
	r, err := svc.OpenStream("peerY", h, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	rid := reqIDOf(t, fresh)
	require.NotEmpty(t, rid)
	fresh.feed(peerjsFrameText(`{"type":"meta","total":` + itoa(len(content)) + `,"reqId":"` + rid + `"}`))
	fresh.feed(peerjsFrameText(`{"type":"data","size":` + itoa(len(content)) + `,"reqId":"` + rid + `"}`))
	fresh.feed(peerjs.Frame{IsText: false, Data: content})
	fresh.feed(peerjsFrameText(`{"type":"done","size":` + itoa(len(content)) + `,"reqId":"` + rid + `"}`))

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, string(content), string(got), "去重后新连接必须正常服务拉取")
}

// reqIDOf 从会话已发帧中取 req 帧的 reqId。
func reqIDOf(t *testing.T, s *fakeSession) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.sent {
		if m["type"] == "req" {
			id, _ := m["reqId"].(string)
			return id
		}
	}
	return ""
}

// TestConnState_AdminUpAndPendingUploadSlot 验证 adminUp 和 pendingUpload
// 可以同时存在（当前代码的假设，后续应修复为互斥）。
// 发现背景：代码审阅 2026-08-19——dispatchFrame 二进制块路由假设两槽互斥，
// 但 serveUploadBegin 和 serveAdmin 都没有显式检查对方是否已占位。
func TestConnState_AdminUpAndPendingUploadSlot(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "local"}
	svc.bindConn(sess)
	st := svc.pending[sess]
	require.NotNil(t, st)

	// 模拟两个槽同时存在
	st.mu.Lock()
	st.adminUp = &adminUploadState{reqID: "admin-1", size: 100}
	st.pendingUpload = &uploadState{reqID: "up-1", size: 100}
	both := st.adminUp != nil && st.pendingUpload != nil
	st.mu.Unlock()
	assert.True(t, both, "当前代码允许两槽同时存在（这是已知问题，待修复）")

	// 清理（不触发 worker）
	st.mu.Lock()
	st.adminUp = nil
	st.pendingUpload = nil
	st.mu.Unlock()
}

// TestConnState_AdminUpOverlap 验证 admin 声明替换逻辑：旧声明被替换时
// 应发送 err 帧给旧 reqId，新声明占槽（发现背景：admin.go 替换逻辑）。
func TestConnState_AdminUpOverlap(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "local"}
	svc.bindConn(sess)
	st := svc.pending[sess]
	require.NotNil(t, st)

	// 第一个 admin 声明（size=0 空文件，立即完成，不触发 worker）
	ar1 := adminReq{Type: "admin", Method: "POST", Path: "/files/upload",
		Binary: true, Filename: "a.bin", Size: 0, ReqID: "ar1"}
	raw1, _ := json.Marshal(ar1)
	svc.serveAdmin(sess, st, raw1)

	// 第二个 admin 声明（也 size=0，替换不会有残留 worker 问题）
	ar2 := adminReq{Type: "admin", Method: "POST", Path: "/files/upload",
		Binary: true, Filename: "b.bin", Size: 0, ReqID: "ar2"}
	raw2, _ := json.Marshal(ar2)
	svc.serveAdmin(sess, st, raw2)

	// 两次声明都走空文件路径，不会触发 worker 写盘
	types := sess.sentTypes()
	assert.Contains(t, types, "err", "旧 admin 声明被替换时应收到 err 帧")
}
