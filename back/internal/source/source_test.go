package source

// source_test.go：Manager 路由 + LocalSource 测试。
// 发现背景：source 体系（统一文件管理）——路由优先级（本地优先）、
// 能力标记（CapStream 分片）、统计管理（Snapshot）、错误降级链。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

func testHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// TestLocalSource_OpenCAS 内容寻址存储读取（分片 + 全量）。
func TestLocalSource_OpenCAS(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("hello-source-", 100)
	hash := testHash(content)
	// 按 CAS 布局写文件：storageDir/<h[:2]>/<h>
	p := filepath.Join(dir, hash[:2], hash)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	s := NewLocalSource(dir, nil)
	require.True(t, s.Available(context.Background()))

	// 分片读取（CapStream）
	r, err := s.Open(context.Background(), hash, 100, 50)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content[100:150], string(got), "分片读取必须按 offset/size 截取")

	// 全量读取
	r, err = s.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	got, err = io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content, string(got))

	// 非法 hash 拒绝
	_, err = s.Open(context.Background(), "short", 0, -1)
	require.Error(t, err, "非法 hash 必须拒绝")

	// 不存在
	_, err = s.Open(context.Background(), testHash("missing"), 0, -1)
	require.Error(t, err)
}

// TestLocalSource_IndexPriority file_index 映射优先于 CAS（同一 hash 两处存在）。
func TestLocalSource_IndexPriority(t *testing.T) {
	dir := t.TempDir()
	// fileIndex 的允许根 = 它的 uploadDir（IsPathAllowed 检查）——测试文件写在这下面；
	// Create 依赖 SQLite 持久化 → 先 InitDB（与 transport 测试同模式）
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	fi := transport.NewFileIndexService(idxDir)
	content := "indexed-content"
	path := filepath.Join(idxDir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	created, err := fi.Create(path)
	require.NoError(t, err)

	s := NewLocalSource(dir, fi)
	r, err := s.Open(context.Background(), created.Hash, 0, -1)
	require.NoError(t, err)
	got, _ := io.ReadAll(r)
	r.Close()
	assert.Equal(t, content, string(got), "应命中 file_index 映射路径")
}

// TestManager_RoutePriority 路由：local 命中即返回；local 未命中降级 peer。
// 用 stub source（可控成功/失败）验证优先级与降级链。
func TestManager_RoutePriority(t *testing.T) {
	m := New()

	// 模拟 source：可编程的 stub
	local := &stubSource{name: "local", priority: 0, caps: CapStream, ok: true}
	peer := &stubSource{name: "peer", priority: 1, caps: CapStream, ok: false}
	url := &stubSource{name: "url", priority: 2, caps: CapFile, ok: true}
	require.NoError(t, m.Register(local))
	require.NoError(t, m.Register(peer))
	require.NoError(t, m.Register(url))
	// 重名拒绝
	require.Error(t, m.Register(&stubSource{name: "local"}))

	hash := testHash("x")

	// 1. local 命中 → local 被调用，peer 不被调
	r, err := m.OpenRange(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	got, _ := io.ReadAll(r)
	r.Close()
	assert.Equal(t, "local-data", string(got))
	assert.Equal(t, []string{"local"}, local.calls)
	assert.Empty(t, peer.calls, "local 命中后不应再尝试 peer")

	// 2. local 未命中 → 降级 peer（失败）→ url（CapFile 跳过 OpenRange）→ 全失败
	local.ok = false
	_, err = m.OpenRange(context.Background(), hash, 0, -1)
	require.Error(t, err, "local 失败 + peer 失败 + url 非流式 → 必须报错")

	// 3. OpenAny：url（CapFile）可兜底整体拉取
	peer.ok = false
	url.ok = true
	r, err = m.OpenAny(context.Background(), hash)
	require.NoError(t, err)
	got, _ = io.ReadAll(r)
	r.Close()
	assert.Equal(t, "file-data", string(got))

	// 4. 优先级运行时调整：peer 提到 local 前
	require.NoError(t, m.SetPriority("peer", -1))
	local.ok = false
	peer.ok = true
	r, err = m.OpenRange(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	got, _ = io.ReadAll(r)
	r.Close()
	assert.Equal(t, "peer-data", string(got))

	// 5. Snapshot 管理面：统计记录
	st := m.Snapshot()
	require.Len(t, st, 3)
	byName := map[string]SourceStatus{}
	for _, s := range st {
		byName[s.Name] = s
	}
	assert.True(t, byName["peer"].Stats.Success >= 1, "peer 成功次数应被记录")
	assert.True(t, byName["local"].Stats.Fail >= 1, "local 失败次数应被记录")
}

// stubSource 可编程测试源。
type stubSource struct {
	name     string
	priority int
	caps     Capability
	ok       bool

	calls []string
}

func (s *stubSource) Name() string             { return s.name }
func (s *stubSource) Type() string             { return "stub" }
func (s *stubSource) Capabilities() Capability { return s.caps }
func (s *stubSource) Priority() int            { return s.priority }
func (s *stubSource) SetPriority(p int)        { s.priority = p }
func (s *stubSource) Available(ctx context.Context) bool {
	s.calls = append(s.calls, s.name)
	return true
}
func (s *stubSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if !s.ok {
		return nil, errStub
	}
	return io.NopCloser(strings.NewReader(s.name + "-data")), nil
}
func (s *stubSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if !s.ok {
		return nil, errStub
	}
	return []byte("file-data"), nil
}
func (s *stubSource) Info(ctx context.Context, hash string) (*FileMeta, error) { return nil, nil }

var errStub = &stubErr{}

type stubErr struct{}

func (e *stubErr) Error() string { return "stub source failed" }

// TestLocalSourceControl 验证 LocalSource 的 Source 控制面：
// AddLocalFile 添加本地文件、WriteFile 直接写文件（发现背景：控制面设计
// doc/source-control.md，2026-08-19）。
func TestLocalSourceControl(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	storageDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)

	s := NewLocalSource(storageDir, idx)

	// 1. AddLocalFile：把已有文件加入 source
	external := filepath.Join(idxDir, "existing.txt")
	content := "control-add-local"
	require.NoError(t, os.WriteFile(external, []byte(content), 0o644))
	meta, err := s.AddLocalFile(external)
	require.NoError(t, err)
	assert.Equal(t, testHash(content), meta.Hash)
	assert.Equal(t, int64(len(content)), meta.Size)
	assert.Equal(t, "existing.txt", meta.Name)

	// 加入后应能通过 local source 读取
	r, err := s.Open(context.Background(), meta.Hash, 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content, string(got))

	// 2. WriteFile：直接写文件到 source
	content2 := "control-write-file"
	meta2, err := s.WriteFile("new.bin", strings.NewReader(content2))
	require.NoError(t, err)
	assert.Equal(t, testHash(content2), meta2.Hash)
	assert.Equal(t, int64(len(content2)), meta2.Size)
	assert.Equal(t, "new.bin", meta2.Name)

	r, err = s.Open(context.Background(), meta2.Hash, 0, -1)
	require.NoError(t, err)
	got, err = io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content2, string(got))
}

// TestLocalSourceControl_NilFileIndex 验证 fileIndex 为 nil 时
// AddLocalFile / WriteFile 返回 ErrControlUnsupported（发现背景：
// NewLocalSource 允许 fileIndex 为 nil，控制面应明确拒绝）。
func TestLocalSourceControl_NilFileIndex(t *testing.T) {
	s := NewLocalSource(t.TempDir(), nil)
	_, err := s.AddLocalFile("/some/path")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = s.WriteFile("x.bin", strings.NewReader("x"))
	assert.ErrorIs(t, err, ErrControlUnsupported)
}

// TestLocalSourceControl_AddLocalFileOutsideRoot 添加根目录外文件 → 拒绝。
func TestLocalSourceControl_AddLocalFileOutsideRoot(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	_, err := s.AddLocalFile(outside)
	assert.Error(t, err, "根目录外文件必须拒绝")
}

// TestLocalSourceControl_AddLocalFileDuplicate 重复添加同一文件返回相同 hash。
func TestLocalSourceControl_AddLocalFileDuplicate(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	inRoot := filepath.Join(idxDir, "dup.bin")
	require.NoError(t, os.WriteFile(inRoot, []byte("dup"), 0o644))
	meta1, err := s.AddLocalFile(inRoot)
	require.NoError(t, err)
	meta2, err := s.AddLocalFile(inRoot)
	require.NoError(t, err)
	assert.Equal(t, meta1.Hash, meta2.Hash, "重复添加同一文件返回相同 hash")
}

// TestLocalSourceControl_WriteFileEmptyReader 空 reader → 空文件可写入。
func TestLocalSourceControl_WriteFileEmptyReader(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	meta, err := s.WriteFile("empty.bin", strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, testHash(""), meta.Hash)
	assert.Equal(t, int64(0), meta.Size)
}

// TestLocalSourceControl_WriteFileNilReader nil reader → 报错。
func TestLocalSourceControl_WriteFileNilReader(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	_, err := s.WriteFile("nil.bin", nil)
	assert.Error(t, err, "nil reader 必须拒绝")
}

// TestManager_Get 验证 Manager.Get 按名字查找 source（发现背景：控制面路由需要）。
func TestManager_Get(t *testing.T) {
	m := New()
	local := &stubSource{name: "local", priority: 0, caps: CapStream, ok: true}
	peer := &stubSource{name: "peer", priority: 1, caps: CapStream, ok: false}
	require.NoError(t, m.Register(local))
	require.NoError(t, m.Register(peer))

	got := m.Get("local")
	require.NotNil(t, got)
	assert.Equal(t, "local", got.Name())

	got = m.Get("peer")
	require.NotNil(t, got)
	assert.Equal(t, "peer", got.Name())

	got = m.Get("nonexistent")
	assert.Nil(t, got, "不存在的 name 返回 nil")
}

// TestLocalControlOf_NonLocalSource 非 LocalSource 不应支持 LocalControl。
func TestLocalControlOf_NonLocalSource(t *testing.T) {
	dir := t.TempDir()
	// URLSource 不支持 LocalControl
	urlSrc := NewURLSource("https://example.com/%s", nil)
	_, ok := LocalControlOf(urlSrc)
	assert.False(t, ok, "URLSource 不应支持 LocalControl")

	// LocalSource 应支持
	ls := NewLocalSource(dir, nil)
	_, ok = LocalControlOf(ls)
	assert.True(t, ok, "LocalSource 应支持 LocalControl")
}

// TestBTControl_NilClient nil 客户端 → 所有方法返回 ErrControlUnsupported。
func TestBTControl_NilClient(t *testing.T) {
	ctrl := NewBTControl(nil)
	_, err := ctrl.DownloadTorrent([]byte("data"))
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = ctrl.DownloadMagnet("magnet:?xt=urn:btih:xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	list := ctrl.ListDownloads()
	assert.Nil(t, list, "nil 客户端返回 nil 列表")
	ds := ctrl.GetDownload("xxx")
	assert.Nil(t, ds, "nil 客户端返回 nil 状态")
	err = ctrl.PauseDownload("xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	err = ctrl.ResumeDownload("xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	err = ctrl.RemoveDownload("xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
}

// TestBTControlOf_NonBTSource 非 BT source 不应支持 BTControl。
func TestBTControlOf_NonBTSource(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocalSource(dir, nil)
	_, ok := any(ls).(BTControl)
	assert.False(t, ok, "LocalSource 不应支持 BTControl")

	// btController 应支持
	bc := NewBTControl(nil)
	_, ok = any(bc).(BTControl)
	assert.True(t, ok, "btController 应支持 BTControl")
}

// TestIPFSControl_NilProvider nil 提供者 → 所有方法返回 ErrControlUnsupported。
func TestIPFSControl_NilProvider(t *testing.T) {
	ctrl := NewIPFSControl(nil, t.TempDir())
	_, err := ctrl.PinCID("QmTest")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	err = ctrl.UnpinCID("QmTest")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = ctrl.ListPins()
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = ctrl.GatewayStatus()
	assert.ErrorIs(t, err, ErrControlUnsupported)
}

// ─────────────────────────────────────────────────────────────────
// 模块级集成测试：Source 控制面子系统
// ─────────────────────────────────────────────────────────────────

// TestManager_ControlRegistration 验证 Manager 可以注册和获取控制面实例。
func TestManager_ControlRegistration(t *testing.T) {
	m := New()

	// 注册 Local source（必须，因为控制面操作需要它）
	local := NewLocalSource(t.TempDir(), nil)
	require.NoError(t, m.Register(local))

	// 注册 BTControl
	btCtrl := NewBTControl(nil)
	m.SetBTControl(btCtrl)
	assert.NotNil(t, m.GetBTControl())

	// 注册 IPFSControl
	ipfsCtrl := NewIPFSControl(nil, t.TempDir())
	m.SetIPFSControl(ipfsCtrl)
	assert.NotNil(t, m.GetIPFSControl())

	// 验证 source 仍然可以正常工作
	ls := m.Get("local")
	require.NotNil(t, ls)
	assert.Equal(t, "local", ls.Name())

	// 验证控制面各自独立
	assert.NotNil(t, m.GetBTControl())
	assert.NotNil(t, m.GetIPFSControl())
}

// TestManager_ControlNilDefault 验证未设置控制面时 Get 方法返回 nil。
func TestManager_ControlNilDefault(t *testing.T) {
	// 先清除全局控制面（之前测试可能已设置）
	m := New()
	m.SetBTControl(nil)
	m.SetIPFSControl(nil)
	assert.Nil(t, m.GetBTControl(), "未设置 BTControl 时应返回 nil")
	assert.Nil(t, m.GetIPFSControl(), "未设置 IPFSControl 时应返回 nil")
}

// TestIPFSGatewayStatus_MockProvider 测试 GatewayStatus 使用 mock provider。
// 不依赖真实 HTTP 请求（provider 本身带 HTTP 客户端，但测试可构造空 provider）。
func TestIPFSGatewayStatus_MockProvider(t *testing.T) {
	// 空 provider（无 gateways）→ GatewayStatus 返回 ErrControlUnsupported
	prov := provider.NewIPFSProvider([]string{})
	ctrl := NewIPFSControl(prov, t.TempDir())
	_, err := ctrl.GatewayStatus()
	assert.ErrorIs(t, err, ErrControlUnsupported, "空 gateways 应返回 ErrControlUnsupported")

	// 有 gateways 但无网络连接 → 返回 offline 状态（不报错）
	prov2 := provider.NewIPFSProvider([]string{"https://nonexistent-gateway.example.com"})
	ctrl2 := NewIPFSControl(prov2, t.TempDir())
	status, err := ctrl2.GatewayStatus()
	// 即使网络不可达，也不应该返回错误（每个网关的状态是独立的）
	require.NoError(t, err)
	require.Len(t, status, 1)
	assert.False(t, status[0].Online, "不存在的网关应标记为 offline")
}
