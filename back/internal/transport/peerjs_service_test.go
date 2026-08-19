package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
)

// fakeSession 内存版 Session：记录发送的 JSON 帧（头+体），可捕获 OnMessage
// 回调并手动注入帧（H1/H6/M6 单元测试 + 流式 OpenStream + forward 测试用）。
// Close 触发 OnClose 回调 + 标记 closed（模拟真实连接关闭的清理路径——
// bindConn 同 peer 去重测试依赖此行为）。
type fakeSession struct {
	id   string
	mu   sync.Mutex
	sent []map[string]any
	// frames 完整帧记录（含 SendFrame 的二进制体）——forward 数据透传断言用
	frames []fakeFrame

	onMessage func(peerjs.Frame)
	onClose   func()
	closed    bool
}

// fakeFrame 一帧的完整记录（头 JSON + 可选二进制体）。
type fakeFrame struct {
	header map[string]any
	body   []byte
}

func (f *fakeSession) ID() string { return f.id }
func (f *fakeSession) SendJSON(v any) error {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.frames = append(f.frames, fakeFrame{header: m})
	f.mu.Unlock()
	return nil
}
func (f *fakeSession) SendFrame(header any, body []byte) error {
	b, _ := json.Marshal(header)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.frames = append(f.frames, fakeFrame{header: m, body: body})
	f.mu.Unlock()
	return nil
}
func (f *fakeSession) OnMessage(fn func(peerjs.Frame)) {
	f.mu.Lock()
	f.onMessage = fn
	f.mu.Unlock()
}
func (f *fakeSession) OnClose(fn func()) {
	f.mu.Lock()
	f.onClose = fn
	f.mu.Unlock()
}
func (f *fakeSession) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	fn := f.onClose
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Closed 返回会话是否已被 Close（去重测试断言旧连接被关闭）。
func (f *fakeSession) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// feed 手动注入一帧到 OnMessage 回调（模拟对端到达的帧）。
func (f *fakeSession) feed(frame peerjs.Frame) {
	f.mu.Lock()
	fn := f.onMessage
	f.mu.Unlock()
	if fn != nil {
		fn(frame)
	}
}

// sentFrames 返回已发送帧（含 body）副本。
func (f *fakeSession) sentFrames() []fakeFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeFrame(nil), f.frames...)
}

func (f *fakeSession) sentFrameTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.frames))
	for _, fr := range f.frames {
		if t, ok := fr.header["type"].(string); ok {
			out = append(out, t)
		}
	}
	return out
}

// sentTypes 返回已发送帧的 type 序列。
func (f *fakeSession) sentTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, m := range f.sent {
		if t, ok := m["type"].(string); ok {
			out = append(out, t)
		}
	}
	return out
}

func newTestPeerJSService(t *testing.T) *PeerJSService {
	t.Helper()
	return &PeerJSService{
		cfg:          &config.Config{},
		storageDir:   t.TempDir(),
		conns:        map[string]Session{},
		pending:      map[Session]*connState{},
		connecting:   map[string]struct{}{},
		forwardRules: map[string][]int{},
		fwNonces:     map[string]*fwdNonce{},
		fileIndex:    NewFileIndexService(t.TempDir()),
		ctx:          context.Background(),
	}
}

// TestServeFile_InvalidHashNoPanic 非法 hash（空/短）→ err 帧，不 panic。
// 发现背景：H1 远程崩溃漏洞——serveFile 直接 req.Hash[:2]，对端发
// {"type":"req","hash":""} 或 "a" 即越界 panic 杀进程（公共信令网络上
// 任意节点一行 JSON 打崩全节点）。修复：先 isValidHash 校验。
func TestServeFile_InvalidHashNoPanic(t *testing.T) {
	svc := newTestPeerJSService(t)
	for _, bad := range []string{"", "a", "abc", "not-hex!"} {
		sess := &fakeSession{id: "remote"}
		svc.serveFile(sess, dcReq{Type: "req", Hash: bad, ReqID: "r1"})
		types := sess.sentTypes()
		require.Len(t, types, 1, "hash=%q 应恰好回一个 err 帧", bad)
		assert.Equal(t, "err", types[0], "hash=%q", bad)
	}
}

// TestServeFile_IndexPathOutsideRoot 索引命中但路径越权 → err 帧，不回传
// 根目录外文件（H2）。
// 发现背景：H2 任意文件读取漏洞——对端 create 任意绝对路径后 req 读取。
// 防御性测试：模拟历史脏数据（根外路径已 upsert 进索引），serveFile 必须拒绝。
func TestServeFile_IndexPathOutsideRoot(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)

	// 根目录内放一个文件并正常登记
	inRoot := filepath.Join(svc.fileIndex.uploadDir, "in.bin")
	require.NoError(t, os.MkdirAll(svc.fileIndex.uploadDir, 0o755))
	content := []byte("inside-root")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	// 模拟历史脏数据：把索引路径改写为根外文件
	outside := filepath.Join(t.TempDir(), "shadow.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	_, err = repository.UpsertFileIndex(fi.Hash, outside, "shadow.txt", 6, false)
	require.NoError(t, err)

	// 请求该 hash：必须拒绝，不得回传根外内容
	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: fi.Hash, ReqID: "r1"})
	types := sess.sentTypes()
	require.Len(t, types, 1)
	assert.Equal(t, "err", types[0], "根外路径不得回传")
}

// TestServeFile_IndexPathAllowed fallback 分支 file_index 命中合法路径 →
// 服务该路径文件（内容寻址只是兜底，create 的文件只有绝对路径 + 索引）。
// 发现背景：2026-08-18 serveFile 多源路由重写时丢了 file_index 分支
// （路由未命中时只查 CAS），集成测试 TestFrameVerbs_CreateListInfoDownload
// 报 not found；恢复后本测试锁定「索引命中合法路径必须回传」。
func TestServeFile_IndexPathAllowed(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	content := []byte("indexed-path-content")
	inRoot := filepath.Join(svc.fileIndex.uploadDir, "in.bin")
	require.NoError(t, os.MkdirAll(svc.fileIndex.uploadDir, 0o755))
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: fi.Hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes())
	assert.Equal(t, float64(len(content)), frames[0].header["total"])
	assert.Equal(t, content, frames[1].body)
}

// TestRouteResponse_DataSizeCap 恶意 data 帧声明超大 size → errCh（H6）。
// 发现背景：H6——f.size = r.Size 无上限，恶意对端声明 1<<62 并持续发
// data 帧 → f.got 无界 append OOM。修复：≤8GB 上限。
func TestRouteResponse_DataSizeCap(t *testing.T) {
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	f := newTestFetchState("r1")
	st.fetches["r1"] = f

	svc.routeResponse(st, dcResp{Type: "data", ReqID: "r1", Size: 1 << 62})
	select {
	case err := <-f.errCh:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("超上限 data 帧必须报错")
	}
}

// TestRouteResponse_DoneSizeMismatch 对端提前 done（截断文件当成功）→ errCh（H6）。
// 发现背景：H6——done 不校验实收字节，对端只发 meta+done 就把空/截断数据
// 当成功返回 → 静默数据损坏。修复：done.Size 与实收字节对比。
func TestRouteResponse_DoneSizeMismatch(t *testing.T) {
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	f := newTestFetchState("r1")
	st.fetches["r1"] = f

	// 声明发送 100 字节，实际 0 字节（无 data 帧）→ 必须报错
	svc.routeResponse(st, dcResp{Type: "done", ReqID: "r1", Size: 100})
	select {
	case err := <-f.errCh:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("截断 done 必须报错")
	}
	select {
	case <-f.done:
		t.Fatal("不得把截断数据当成功")
	default:
	}
}

// TestRouteResponse_DoneSizeMatch 正常 done（Size 与实收一致）→ 成功。
func TestRouteResponse_DoneSizeMatch(t *testing.T) {
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	f := newTestFetchState("r1")
	st.fetches["r1"] = f

	svc.routeResponse(st, dcResp{Type: "data", ReqID: "r1", Size: 3})
	f.received = 3
	svc.routeResponse(st, dcResp{Type: "done", ReqID: "r1", Size: 3})
	select {
	case <-f.done:
		// done 已 close = 传输完成（流式语义：数据经 f.q 消费）
	case <-time.After(time.Second):
		t.Fatal("匹配的 done 应成功返回")
	}
}

// newTestFetchState 构造测试用 fetchState（流式字段全初始化）。
func newTestFetchState(reqID string) *fetchState {
	return &fetchState{
		reqID:  reqID,
		q:      make(chan []byte, 8),
		done:   make(chan struct{}),
		errCh:  make(chan error, 1),
		closed: make(chan struct{}),
	}
}

// TestServeUploadBegin_StalePendingCleared 旧 upload 头占位超时 → 自动清空（M6）。
// 发现背景：M6——对端发 upload 头后不发数据块，pendingUpload 永久占用，
// 该连接后续所有 upload 全部 "already in progress"（连接级 DoS，重连才恢复）。
func TestServeUploadBegin_StalePendingCleared(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	sess := &fakeSession{id: "remote"}

	// 过期占位（31s 前创建）
	st.mu.Lock()
	st.pendingUpload = &uploadState{reqID: "stale", created: time.Now().Add(-31 * time.Second)}
	st.mu.Unlock()

	svc.serveUploadBegin(sess, st, dcResp{Type: "upload", Name: "new.bin", Size: 10, ReqID: "r2"})
	types := sess.sentTypes()
	require.Len(t, types, 1)
	assert.Equal(t, "meta", types[0], "过期占位应被清空并正常开始上传")

	st.mu.Lock()
	assert.NotNil(t, st.pendingUpload, "新上传应占用槽位")
	st.mu.Unlock()
}

// TestUploadWorker_WriteThenComplete 上传 worker 落盘 + 完成回帧（H5 链路）。
// 发现背景：H5——二进制帧的 WriteAt/Complete 移出消息泵到连接级 worker，
// 本测试直接驱动 worker 验证 chunk 投递 → 落盘 → uploaded 回帧全链路。
func TestUploadWorker_WriteThenComplete(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	st := &connState{
		fetches: make(map[string]*fetchState),
		binCh:   make(chan binaryChunk, 1),
		binDone: make(chan struct{}),
	}
	sess := &fakeSession{id: "remote"}

	content := make([]byte, 10)
	for i := range content {
		content[i] = byte(i)
	}
	us, err := svc.fileIndex.BeginUpload("w.bin", 10)
	require.NoError(t, err)
	up := &uploadState{reqID: "r1", offset: 0, size: 10, sess: us}

	// 单 worker 处理一个分片（last=true → Complete）
	done := make(chan struct{})
	go func() {
		svc.uploadWorker(sess, st)
		close(done)
	}()
	st.binCh <- binaryChunk{up: up, offset: 0, data: content, last: true}

	// 等 worker 回 uploaded 帧（轮询 + 超时；不能立刻 close(binDone)——
	// select 随机选路会把未处理的 chunk 直接丢掉）
	var types []string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		types = sess.sentTypes()
		if len(types) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(st.binDone)
	<-done

	require.Len(t, types, 1)
	assert.Equal(t, "uploaded", types[0], "分片收齐应回 uploaded")
}

// TestHashMatchesSHA256_AllowsEmptyFile 空文件也可通过内容寻址校验。
// 发现背景：代码审阅——原实现 `len(data)==0` 直接 return false，导致
// sha256(空)（e3b0c442...）这类合法空文件永远无法从对端拉取。
// 修复：移除空数据特判，空文件只校验其真实 sha256。
func TestHashMatchesSHA256_AllowsEmptyFile(t *testing.T) {
	emptyHash := sha256.Sum256([]byte{})
	assert.True(t, hashMatchesSHA256(hex.EncodeToString(emptyHash[:]), []byte{}), "空文件的 sha256 应被接受")
}
