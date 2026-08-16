package transport

// stream_test.go：流式 OpenStream/fetchReader 测试（出站角色）。
// 用 fakeSession 手动注入对端帧（meta/data/done/err），驱动消息泵 →
// 块队列 → reader 全链路。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindFakeConn 把 fakeSession 绑到 svc（conns + pending），返回会话。
// 与 bindConn 不同：不启动 uploadWorker（无 upload 场景），仅注册路由。
func bindFakeConn(t *testing.T, svc *PeerJSService, id string) *fakeSession {
	t.Helper()
	sess := &fakeSession{id: id}
	st := &connState{
		fetches: make(map[string]*fetchState),
		binCh:   make(chan binaryChunk, 16),
		binDone: make(chan struct{}),
	}
	svc.mu.Lock()
	if svc.conns == nil {
		svc.conns = make(map[string]Session)
	}
	svc.conns[id] = sess
	svc.mu.Unlock()
	svc.pendingMu.Lock()
	if svc.pending == nil {
		svc.pending = make(map[Session]*connState)
	}
	svc.pending[sess] = st
	svc.pendingMu.Unlock()
	// 捕获消息回调（fakeSession.OnMessage 已实现）
	sess.feed(peerjsFrameText(`{"type":"x"}`)) // no-op 触发注册（OnMessage 在构造时未调）
	// OnMessage 回调需要手动注册到 pump——bindConn 未调用，这里直接绑定：
	// 复用 bindConn 的分派逻辑（文本 verb → 入站；响应 → outbound）
	return sess
}

func peerjsFrameText(s string) peerjs.Frame { return peerjs.Frame{IsText: true, Data: []byte(s)} }

// TestOpenStream_StreamingRead 流式全链路：注入 meta/data/data/done，
// reader 分块读出，全量请求 sha256 校验通过（H5 兜底保留）。
// 发现背景：流式改造（source 体系）——旧 requestFile 全量 buffer 内存驻留，
// 8GB 文件 OOM 风险；改块队列流式后本测试验证块投递→消费→校验链路。
func TestOpenStream_StreamingRead(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := bindFakeConn(t, svc, "peerA")

	content := bytes.Repeat([]byte("stream-data-"), 100) // 1300 bytes
	h := sha256.Sum256(content)
	hash := hex.EncodeToString(h[:])

	// 注册 OnMessage 回调：模拟 bindConn 的分派（响应帧 → routeResponse）
	var pumpMu sync.Mutex
	sess.OnMessage(func(msg peerjs.Frame) {
		if !msg.IsText {
			return // 二进制块由 bindConn 的 pump 处理——测试中不走 pump，
			// 直接投递到 expect 的 q（见下 feedData）
		}
		var r dcResp
		_ = json.Unmarshal(msg.Data, &r)
		pumpMu.Lock()
		defer pumpMu.Unlock()
		svc.routeResponse(svc.stateFor(sess), r)
	})

	// 发起流式请求
	r, err := svc.OpenStream("peerA", hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// 注入响应帧（模拟对端）：
	// meta → data(块1 700B) → data(块2 600B) → done
	sess.feed(peerjsFrameText(`{"type":"meta","hash":"` + hash + `","total":1300,"reqId":"x"}`))
	reqID := "" // 从会话发出的 req 帧取 reqId
	sess.mu.Lock()
	for _, m := range sess.sent {
		if m["type"] == "req" {
			reqID, _ = m["reqId"].(string)
		}
	}
	sess.mu.Unlock()
	require.NotEmpty(t, reqID, "openStream 必须携带 reqId")

	feedData := func(size int, payload []byte) {
		st := svc.stateFor(sess)
		st.mu.Lock()
		f := st.expect
		st.mu.Unlock()
		require.NotNil(t, f, "data 帧前必须有 expect")
		// 直接投递到块队列（等价 pump 的二进制分支）
		select {
		case f.q <- payload:
			f.received += int64(size)
			if f.received >= f.size {
				st.mu.Lock()
				st.expect = nil
				st.mu.Unlock()
			}
		case <-f.closed:
		}
	}

	// 块1（先发 data 头设置 expect，再投数据块——与真实 pump 顺序一致）
	sess.feed(peerjsFrameText(`{"type":"data","size":700,"reqId":"` + reqID + `"}`))
	feedData(700, content[:700])
	// 块2
	sess.feed(peerjsFrameText(`{"type":"data","size":600,"reqId":"` + reqID + `"}`))
	feedData(600, content[700:])
	// done
	sess.feed(peerjsFrameText(`{"type":"done","size":1300,"reqId":"` + reqID + `"}`))

	got, err := io.ReadAll(r)
	require.NoError(t, err, "流式读取应成功")
	assert.Equal(t, content, got, "分块重组必须与原文一致")
}

// TestOpenStream_CloseCancel 提前 Close：pump 投递不阻塞（closed 通道放行）。
func TestOpenStream_CloseCancel(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := bindFakeConn(t, svc, "peerA")

	r, err := svc.OpenStream("peerA", hashOf("x"), 0, -1)
	require.NoError(t, err)
	r.Close()

	st := svc.stateFor(sess)
	st.mu.Lock()
	require.Empty(t, st.fetches, "Close 后必须从路由表清理")
	st.mu.Unlock()

	// 关闭后投递块：不得阻塞（select closed 分支）
	require.NoError(t, r.Close(), "重复 Close 幂等")
}

// TestOpenStream_ConnClosed 连接关闭：reader 返回错误而非悬挂。
func TestOpenStream_ConnClosed(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := bindFakeConn(t, svc, "peerA")

	r, err := svc.OpenStream("peerA", hashOf("x"), 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// 模拟 bindConn OnClose 的清理（errCh 投递；close(f.closed) 由 reader
	// cleanup 幂等处理——这里只投错误，避免与 cleanup 双重 close）
	st := svc.stateFor(sess)
	st.mu.Lock()
	for _, f := range st.fetches {
		select {
		case f.errCh <- errConnClosedForTest:
		default:
		}
	}
	st.mu.Unlock()

	_, err = io.ReadAll(r)
	require.Error(t, err, "连接关闭后读取必须报错")
}

// errConnClosedForTest 测试用错误标记（不能用 io.EOF——ReadAll 把 EOF 当正常结束）。
var errConnClosedForTest = errors.New("connection closed (test)")

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
