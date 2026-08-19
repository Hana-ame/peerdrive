package source

// peer_test.go：PeerSource 竞速拉取测试（2026-08-19 并发竞速改造）。
// 用真实 PeerJSService + BindLocal 注入任意数量内存 fake Session 对端，
// 验证：多对端并发发起 req（真并发）→ 先响应对端胜出 → 全部输家流被
// 收割关闭（fetch 状态清理、peer 流互斥锁释放、迟到帧静默忽略）。
// 覆盖：双端/三端/部分失败/忙对端跳过/多块重组/胜者锁复用/err 帧。
// 发现背景：串行尝试对端时第一个慢对端卡住整个回源；改造为并发竞速。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// fakePeerSess 内存 Session（source 包测试用，等价 transport 包 fakeSession）。
type fakePeerSess struct {
	id string

	mu        sync.Mutex
	failSend  bool // 置真后 SendJSON 报错（模拟对端发帧失败/断连）
	sentReq   string
	onMessage func(peerjs.Frame)
	onClose   func()
}

func newFakePeerSess(id string) *fakePeerSess { return &fakePeerSess{id: id} }

func (f *fakePeerSess) ID() string { return f.id }
func (f *fakePeerSess) SendJSON(v any) error {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if t, _ := m["type"].(string); t == "req" {
		r, _ := m["reqId"].(string)
		f.mu.Lock()
		f.sentReq = r
		fail := f.failSend
		f.mu.Unlock()
		if fail {
			return errSendFail
		}
	}
	return nil
}
func (f *fakePeerSess) SendFrame(header any, body []byte) error { return f.SendJSON(header) }
func (f *fakePeerSess) OnMessage(fn func(peerjs.Frame)) {
	f.mu.Lock()
	f.onMessage = fn
	f.mu.Unlock()
}
func (f *fakePeerSess) OnClose(fn func()) {
	f.mu.Lock()
	f.onClose = fn
	f.mu.Unlock()
}

// Close 模拟对端断开：触发 bindConn OnClose 的清理（errCh 投递 + fetch
// 状态清除 + binDone 关闭）——与真实 WSSession.Close 语义一致。
func (f *fakePeerSess) Close() {
	f.mu.Lock()
	fn := f.onClose
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// feed 注入一帧到 bindConn 的 pump（真实 routeResponse 链路）。
func (f *fakePeerSess) feed(frame peerjs.Frame) {
	f.mu.Lock()
	fn := f.onMessage
	f.mu.Unlock()
	if fn != nil {
		fn(frame)
	}
}

func (f *fakePeerSess) reqID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sentReq
}

func testPeerHash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// waitReqs 等待所有对端都收到 req 帧（竞速必须并发发给全部候选对端）。
func waitReqs(t *testing.T, sess ...*fakePeerSess) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, s := range sess {
			if s.reqID() == "" {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("not all peers received req within deadline")
}

// feedFullResponse 给对端注入完整 meta/data/done 响应（单块，数据内容为 content）。
func feedFullResponse(t *testing.T, s *fakePeerSess, hash string, content []byte) {
	t.Helper()
	feedFullResponseBlocks(t, s, hash, [][]byte{content})
}

// feedFullResponseBlocks 给对端注入完整响应（多块：每块一个 data 头 + 二进制块，
// 模拟真实 64KB 分块传输；最后 done 收尾）。
func feedFullResponseBlocks(t *testing.T, s *fakePeerSess, hash string, blocks [][]byte) {
	t.Helper()
	rid := s.reqID()
	require.NotEmpty(t, rid, "对端必须已收到 req 帧")
	total := 0
	for _, b := range blocks {
		total += len(b)
	}
	size := strconv.Itoa(total)
	s.feed(peerjsFrameText(`{"type":"meta","total":` + size + `,"reqId":"` + rid + `"}`))
	for _, b := range blocks {
		s.feed(peerjsFrameText(`{"type":"data","size":` + strconv.Itoa(len(b)) + `,"reqId":"` + rid + `"}`))
		// 数据块走 pump 的二进制分支（st.expect 已由 data 头建立）
		s.feed(peerjs.Frame{IsText: false, Data: b})
	}
	s.feed(peerjsFrameText(`{"type":"done","size":` + size + `,"reqId":"` + rid + `"}`))
}

// feedErrResponse 给对端注入 err 帧（读取期失败：竞速只保证流建立，数据
// 失败在 Read 时才暴露）。
func feedErrResponse(t *testing.T, s *fakePeerSess, msg string) {
	t.Helper()
	rid := s.reqID()
	require.NotEmpty(t, rid, "对端必须已收到 req 帧")
	s.feed(peerjsFrameText(`{"type":"err","msg":"` + msg + `","reqId":"` + rid + `"}`))
}

// setFailSends 使指定对端发帧失败（模拟连接断开/发帧错误）。
func setFailSends(sess ...*fakePeerSess) {
	for _, s := range sess {
		s.mu.Lock()
		s.failSend = true
		s.mu.Unlock()
	}
}

// newRaceSvc 创建竞速测试的服务与一组对端。
func newRaceSvc(t *testing.T, ids ...string) (*transport.PeerJSService, []*fakePeerSess, *PeerSource) {
	t.Helper()
	svc := transport.NewPeerJSService(&config.Config{}, t.TempDir())
	sess := make([]*fakePeerSess, 0, len(ids))
	for _, id := range ids {
		s := newFakePeerSess(id)
		svc.BindLocal(s)
		sess = append(sess, s)
	}
	return svc, sess, NewPeerSource(svc)
}

func peerjsFrameText(s string) peerjs.Frame { return peerjs.Frame{IsText: true, Data: []byte(s)} }

// errSendFail fakeSession 发送失败错误（失败路径测试用）。
var errSendFail = assert.AnError

// TestPeerSource_RaceWinsFastest 双对端竞速：数据完整，输家流被收割
// （fetch 状态清理），迟到帧静默忽略。
// 竞速胜负不确定（Connections 的 map 迭代顺序随机、goroutine 调度不定）——
// 所以响应喂给所有候选对端：无论谁胜出都能读到；输家的迟到帧被忽略。
func TestPeerSource_RaceWinsFastest(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]

	content := bytes.Repeat([]byte("race-data-"), 64)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, a, b)

	// 喂所有候选对端：胜者收到数据，输家迟到帧被静默忽略
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "胜者流的数据必须完整")

	// 输家已被收割：给收割 goroutine 时间，其 fetch 状态必须已清理；
	// 再补喂的迟到帧（done 后重复 feed）不得重新登记状态
	time.Sleep(50 * time.Millisecond)
	for _, s := range sess {
		require.Empty(t, svc.PendingFetchesForTest(s), "输家 %s 不应残留 fetch 状态", s.id)
		feedFullResponse(t, s, hash, content) // 迟到响应：不 panic 即通过
		time.Sleep(20 * time.Millisecond)
		require.Empty(t, svc.PendingFetchesForTest(s), "迟到帧不得重新登记 fetch 状态", s.id)
	}
}

// TestPeerSource_RaceAllFail 全部对端失败 → 汇总错误，peer 流互斥锁释放
// （后续可再次 Open，不被 TryLock 卡死）。
func TestPeerSource_RaceAllFail(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]
	setFailSends(a, b)

	hash := testPeerHash([]byte("x"))

	_, err := ps.Open(context.Background(), hash, 0, -1)
	require.Error(t, err, "全失败必须报错")
	// 两个对端都应收到 req（真并发发起）
	require.NotEmpty(t, a.reqID())
	require.NotEmpty(t, b.reqID())
	require.Empty(t, svc.PendingFetchesForTest(a), "失败路径不得残留 fetch 状态")
	require.Empty(t, svc.PendingFetchesForTest(b))
}

// TestPeerSource_RaceThreePeers 三端竞速：全部对端收到 req（真并发），
// 胜者流数据完整；全部输家被收割（fetch 状态清理），迟到帧静默忽略；
// 收割完成后再次 Open 成功（所有 peer 锁无泄漏）。
func TestPeerSource_RaceThreePeers(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")

	content := bytes.Repeat([]byte("three-race-"), 100)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// 响应喂给全部候选对端：胜者读到数据，输家迟到帧被忽略
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "胜者流的数据必须完整")

	// 全部输家都已被收割：fetch 状态清理 + 迟到帧静默忽略
	time.Sleep(50 * time.Millisecond)
	for _, s := range sess {
		require.Empty(t, svc.PendingFetchesForTest(s), "输家 %s 不应残留 fetch 状态", s.id)
		feedFullResponse(t, s, hash, content) // 迟到响应：不 panic 即通过
		time.Sleep(20 * time.Millisecond)
		require.Empty(t, svc.PendingFetchesForTest(s), "迟到帧不得重新登记 fetch 状态", s.id)
	}

	// 锁无泄漏：再开一次竞速（三端都还在线），响应喂全部 → 成功
	r2, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r2.Close()
	waitReqs(t, sess...)
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	got2, err := readAllTimeout(r2)
	require.NoError(t, err)
	assert.Equal(t, content, got2, "第二轮竞速胜者流的数据必须完整")
}

// TestPeerSource_RaceMixedFailSuccess 部分失败 + 部分成功：失败对端
// 立即释放锁（不阻塞后续 Open），成功对端正常竞速。
func TestPeerSource_RaceMixedFailSuccess(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")
	a, b, c := sess[0], sess[1], sess[2]
	setFailSends(a)

	content := bytes.Repeat([]byte("mixed-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// B、C 收到 req（并发发起），A 发帧失败
	waitReqs(t, b, c)
	time.Sleep(20 * time.Millisecond)
	require.Empty(t, svc.PendingFetchesForTest(a), "失败对端不得残留 fetch 状态")

	// 响应喂给成功对端 B、C：胜者读到数据，输家迟到帧被忽略
	for _, s := range []*fakePeerSess{b, c} {
		feedFullResponse(t, s, hash, content)
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "混合场景胜者流的数据必须完整")

	// 输家（B 或 C 之一）被收割
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, svc.PendingFetchesForTest(a), "失败对端不得残留 fetch 状态")

	// A 的锁已被失败路径释放 → 去掉 failSend 后 A 单独参与竞速必须成功
	a.mu.Lock()
	a.failSend = false
	a.mu.Unlock()
	setFailSends(b, c)
	r2, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r2.Close()
	waitReqs(t, a)
	feedFullResponse(t, a, hash, content)
	got2, err := readAllTimeout(r2)
	require.NoError(t, err)
	assert.Equal(t, content, got2, "失败对端锁释放后必须可再次竞速并胜出")
}

// TestPeerSource_RaceBusyPeerSkipped 忙对端跳过：某对端已有流（互斥锁
// 被占用）→ 不收 req 且不阻塞竞速；其余对端正常胜出。
func TestPeerSource_RaceBusyPeerSkipped(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]

	// 模拟 peerA 已有流进行中（连接级 expect 单槽）：直接占用其互斥锁
	muI, _ := ps.peerLocks.LoadOrStore(a.id, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	content := bytes.Repeat([]byte("busy-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// 只有 B 收到 req；A 被跳过（TryLock 失败不等待）
	waitReqs(t, b)
	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, a.reqID(), "忙对端必须被跳过（不发 req）")
	assert.Empty(t, svc.PendingFetchesForTest(a), "忙对端不得残留 fetch 状态")

	feedFullResponse(t, b, hash, content)
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "忙对端跳过场景胜者流的数据必须完整")
}

// TestPeerSource_MultiBlockTransfer 胜者流多块重组：数据 >64KB 分 3 块
// 传输（meta + data×3 + done），竞速胜出后必须完整重组。
func TestPeerSource_MultiBlockTransfer(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	// 3 块：64KB + 64KB + 尾块（真实块粒度）
	k := 1024
	block1 := bytes.Repeat([]byte("x"), 64*k)
	block2 := bytes.Repeat([]byte("y"), 64*k)
	block3 := bytes.Repeat([]byte("z"), 1024)
	content := append(append(append([]byte{}, block1...), block2...), block3...)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// 多块响应喂全部候选对端（胜负不确定，喂所有才能保证胜者收到）
	for _, s := range sess {
		feedFullResponseBlocks(t, s, hash, [][]byte{block1, block2, block3})
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "多块传输必须按序完整重组")
	assert.Equal(t, len(content), len(got))
}

// TestPeerSource_WinnerPeerLockReleased 竞速后所有对端锁释放：第一轮
// 竞速建立后立即关闭（胜者锁由 Close 释放、输家锁由收割释放，两把锁
// 各走一遍「占用→释放」）；随后用 failSend 把竞争收敛到唯一对端，
// 逐对端验证锁已释放（若锁泄漏，TryLock 失败 → 该对端被跳过报错）。
func TestPeerSource_WinnerPeerLockReleased(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]

	content := bytes.Repeat([]byte("lock-race-"), 32)
	hash := testPeerHash(content)

	// 第一轮：竞速建立后不读数据直接 Close——A、B 两把锁各走一遍释放
	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, a, b)
	require.NoError(t, r.Close())
	time.Sleep(50 * time.Millisecond) // 等收割 goroutine 完成（输家锁释放）

	// 第二轮：A 单独（B failSend）→ 成功 ⇒ A 锁已释放（A 可能是胜者或输家）
	setFailSends(b)
	r2, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, a)
	feedFullResponse(t, a, hash, content)
	got, err := readAllTimeout(r2)
	require.NoError(t, err)
	assert.Equal(t, content, got, "A 的锁必须已释放（可单独竞速）")
	require.NoError(t, r2.Close())

	// 第三轮：B 单独（A failSend）→ 成功 ⇒ B 锁已释放
	setFailSends(a)
	b.mu.Lock()
	b.failSend = false
	b.mu.Unlock()
	r3, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, b)
	feedFullResponse(t, b, hash, content)
	got3, err := readAllTimeout(r3)
	require.NoError(t, err)
	assert.Equal(t, content, got3, "B 的锁必须已释放（可单独竞速）")
	require.NoError(t, r3.Close())
}

// TestPeerSource_WinnerErrFrame 胜者流对端回 err 帧：读取期报错
// （竞速只保证流建立成功，数据失败在 Read 时暴露——不静默返回坏数据）。
// 胜负不确定：err 帧喂给所有候选对端，胜者的 err 必然生效。
func TestPeerSource_WinnerErrFrame(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	hash := testPeerHash([]byte("err-frame"))

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	for _, s := range sess {
		feedErrResponse(t, s, "boom")
	}
	_, err = readAllTimeout(r)
	require.Error(t, err, "胜者流收到 err 帧必须报错")
	assert.Contains(t, err.Error(), "boom")
}

// TestPeerSource_NoConnections 无在线对端：明确报错（回退语义的末端：
// 上层 Manager 拿到汇总错误前，PeerSource 自身必须给出可诊断原因）。
func TestPeerSource_NoConnections(t *testing.T) {
	_, _, ps := newRaceSvc(t) // 不绑定任何对端

	_, err := ps.Open(context.Background(), testPeerHash([]byte("x")), 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no online peer available")
}

// TestPeerSource_OpenAfterSvcClose 服务已关闭后再 Open：连接已全部释放
// → 无在线对端报错（不得悬挂/panic）。
func TestPeerSource_OpenAfterSvcClose(t *testing.T) {
	svc, _, ps := newRaceSvc(t, "peerA", "peerB")
	svc.Close() // 关闭服务：conns 清空 + 会话 Close（fake Close 触发 OnClose）

	_, err := ps.Open(context.Background(), testPeerHash([]byte("x")), 0, -1)
	require.Error(t, err, "服务关闭后 Open 必须报错")
	assert.Contains(t, err.Error(), "no online peer available")
}

// TestPeerSource_ReadAfterSvcClose 竞速建立后服务关闭：读取期 ctx 取消 +
// 连接清理 → 报错（不悬挂、不静默截断当成功）。
func TestPeerSource_ReadAfterSvcClose(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")

	content := bytes.Repeat([]byte("shutdown-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// 喂数据到 q（读侧缓冲），然后服务关闭
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	time.Sleep(30 * time.Millisecond) // 让块投递进 q
	svc.Close()                       // 服务关闭 → ctx cancel + 会话 Close

	_, err = readAllTimeout(r)
	require.Error(t, err, "服务关闭后读取必须报错（不能把已缓冲数据当完整结果）")
}

// TestPeerSource_WinnerPeerDropped 竞速胜出后对端中途断开：读取期报错，
// 不返回截断数据当成功（内容寻址语义：坏数据必须显式失败）。
func TestPeerSource_WinnerPeerDropped(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	content := bytes.Repeat([]byte("drop-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// 只喂一部分数据（meta + 半个文件），随后所有对端断开——胜者流
	// 未收齐 → 必须报错而非返回部分数据
	for _, s := range sess {
		rid := s.reqID()
		require.NotEmpty(t, rid)
		size := strconv.Itoa(len(content))
		s.feed(peerjsFrameText(`{"type":"meta","total":` + size + `,"reqId":"` + rid + `"}`))
		s.feed(peerjsFrameText(`{"type":"data","size":` + strconv.Itoa(len(content)) + `,"reqId":"` + rid + `"}`))
		s.feed(peerjs.Frame{IsText: false, Data: content})
		// 不发 done 帧——模拟对端在传输中掉线
	}
	time.Sleep(30 * time.Millisecond)
	for _, s := range sess {
		s.Close() // 对端断开：bindConn OnClose → errCh + fetch 状态清理
	}

	_, err = readAllTimeout(r)
	require.Error(t, err, "对端中途断开必须报错（不把截断数据当成功）")
}

// TestPeerSource_AllFailErrorDetail 全失败错误聚合：每个对端的失败原因
// 必须出现在最终报错里（回退到上层 source 时可诊断是哪个对端怎么挂的）。
func TestPeerSource_AllFailErrorDetail(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")
	setFailSends(sess...)

	_, err := ps.Open(context.Background(), testPeerHash([]byte("x")), 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all peers failed")
	// 每个失败对端的原因都聚合进报错（3 个对端 = 3 份原因）
	assert.Equal(t, 3, strings.Count(err.Error(), errSendFail.Error()),
		"聚合错误必须包含每个对端的失败原因")
}

// waitLockFree 等待指定 peer 的流互斥锁可获取（竞速 goroutine 是异步
// 的：Open 返回 ≠ 所有候选 goroutine 已结束，失败对端解锁可能晚于返回；
// 连续多轮竞速时下一轮 TryLock 会撞上未释放的锁——发现背景：
// TestPeerSource_ParallelStreamsAcrossPeers -count=5 偶发失败，错误
// 「peer peerA: ...」表明上一轮失败者尚未释放锁）。只探测本轮候选锁：
// 胜者锁被 reader 故意持有到测试结束，不能等待全部锁空闲。
// TryLock 探测 + 立即释放，无副作用。
func waitLockFree(t *testing.T, ps *PeerSource, s *fakePeerSess) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		muI, _ := ps.peerLocks.LoadOrStore(s.id, &sync.Mutex{})
		mu := muI.(*sync.Mutex)
		if mu.TryLock() {
			mu.Unlock()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer lock %s not released within deadline", s.id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPeerSource_ParallelStreamsAcrossPeers 多连接并行流：3 对端 = 3 条
// 连接，同时发起 3 个拉取（每轮 failSend 收敛到唯一对端）——peer 流
// 互斥锁按 peer 隔离，多连接并行互不阻塞；各自数据完整。
func TestPeerSource_ParallelStreamsAcrossPeers(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")
	a, b, c := sess[0], sess[1], sess[2]

	contents := [][]byte{
		bytes.Repeat([]byte("parallel-a-"), 32),
		bytes.Repeat([]byte("parallel-b-"), 32),
		bytes.Repeat([]byte("parallel-c-"), 32),
	}
	hashes := []string{
		testPeerHash(contents[0]),
		testPeerHash(contents[1]),
		testPeerHash(contents[2]),
	}

	type job struct {
		sess *fakePeerSess
		hash string
		body []byte
	}
	jobs := []job{{a, hashes[0], contents[0]}, {b, hashes[1], contents[1]}, {c, hashes[2], contents[2]}}

	// 每轮只留一个候选对端（其余 failSend）：三轮并行互不干扰。
	// 每轮前等待锁空闲（上一轮竞速 goroutine 可能尚未结束）
	var readers []io.ReadCloser
	for _, j := range jobs {
		waitLockFree(t, ps, j.sess)
		for _, s := range sess {
			s.mu.Lock()
			s.failSend = s != j.sess
			s.mu.Unlock()
		}
		r, err := ps.Open(context.Background(), j.hash, 0, -1)
		require.NoError(t, err, "%s 拉取建立失败", j.sess.id)
		readers = append(readers, r)
		waitReqs(t, j.sess)
	}
	defer func() {
		for _, r := range readers {
			r.Close()
		}
	}()

	// 三流并行期间同时注入响应 → 各自胜出
	for _, j := range jobs {
		feedFullResponse(t, j.sess, j.hash, j.body)
	}
	for i, r := range readers {
		got, err := readAllTimeout(r)
		require.NoError(t, err, "%s 读取失败", jobs[i].sess.id)
		assert.Equal(t, jobs[i].body, got, "%s 数据必须完整", jobs[i].sess.id)
	}
	// 全部结束后无残留状态（锁 + fetch 全部释放）
	for _, s := range sess {
		require.Empty(t, svc.PendingFetchesForTest(s), "并行流结束后 %s 不得残留 fetch 状态", s.id)
	}
}

// readAllTimeout io.ReadAll + 超时防悬挂（竞速测试防止 reader 永不结束）。
func readAllTimeout(r io.ReadCloser) ([]byte, error) {
	done := make(chan struct{})
	var (
		buf []byte
		err error
	)
	go func() {
		buf, err = io.ReadAll(r)
		close(done)
	}()
	select {
	case <-done:
		return buf, err
	case <-time.After(3 * time.Second):
		return nil, assert.AnError
	}
}
