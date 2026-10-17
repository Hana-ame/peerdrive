package source

// contract_test.go: Source 接口契约测试（mock 实现把接口承诺写死成可执行断言）。
//
// 发现背景：本 PR 把 ECH 访问与 sha-文件访问收敛到同一个 Source 接口，并把它定为
// 「以后添加任何新数据源的参考基座」。参考基座的前提是契约可执行——新数据源接入时
// 跑一遍这套测试，就知道自己有没有违背接口语义（hash 校验、偏移钳制、整文件 sha256
// 校验、Close 幂等、能力位声明、可选接口不破坏兼容）。
//
// mock 设计：contractMock 是「完全可编程」的 Source 实现——记录每次调用的入参、可设定
// 成功/失败、可按 offset/size 切片返回固定缓冲。这样契约测试验证的是「接口+管理器」
// 的组合语义，而不是某个具体实现的巧合行为。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 编译期接口满足断言 ----
//
// 这是契约的「骨架」：任何实现都必须在编译期满足 Source；声明了元数据能力的实现
// 必须同时满足 MetaSource；有写路径的实现必须满足 LocalControl。新数据源接入时，
// 往这里加一行 var _ 就能得到编译期检查。

var (
	_ Source       = (*LocalSource)(nil)
	_ Source       = (*PeerSource)(nil)
	_ Source       = (*URLSource)(nil)
	_ Source       = (*EchSource)(nil)
	_ Source       = (*ShaSource)(nil)
	_ MetaSource   = (*EchSource)(nil)
	_ MetaSource   = (*ShaSource)(nil)
	_ LocalControl = (*LocalSource)(nil)
	_ LocalControl = (*ShaSource)(nil)
	_ Egress       = egressFunc(nil)
	_ Egress       = echcoreEgress{}
)

// TestContract_ValidHash 契约：所有源入口都必须拒绝非法 hash（64 位小写 hex）。
// 背景：对端/HTTP 传入的 hash 不可信，裸取 hash[:2] 会越界 panic，而源调用常在 goroutine
// 里——一次 panic 直接杀进程（transport/inbound.go 的 H1 修复背景）。
func TestContract_ValidHash(t *testing.T) {
	valid := makeValidHash()
	cases := []struct {
		in   string
		want bool
	}{
		{in: valid, want: true},
		{in: "", want: false},
		{in: "short", want: false},
		{in: "g11111111111111111111111111111111111111111111111111111111111111", want: false}, // 含非 hex 字符
		{in: strings.ToUpper(valid), want: false},                                            // 大写不接受
		{in: valid[:63], want: false},                                                        // 少一位
		{in: valid + "0", want: false},                                                       // 多一位
	}
	for _, c := range cases {
		if c.want {
			assert.NoError(t, validHash(c.in), "valid hash must pass: %q", c.in)
		} else {
			assert.Error(t, validHash(c.in), "invalid hash must be rejected: %q", c.in)
		}
	}
}

// TestContract_CapabilityDeclarations 契约：源声明的能力位必须与其行为一致——这是「参考
// 基座」的关键：路由（Manager 按能力位选调用模式）完全信任声明，声明了却做不到就会静默
// 失败。新数据源接入时照这份表对齐声明与实现。
func TestContract_CapabilityDeclarations(t *testing.T) {
	ctx := context.Background()
	data := []byte("capability-behavior")
	m := newBufferMock("decl", data) // 声明 CapStream|CapFile|CapMeta|CapVerify

	// CapStream → Open 支持 offset/size 分片
	assert.True(t, IsStream(m))
	r, err := m.Open(ctx, m.hash, 2, 3)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, "pab", string(got))

	// CapFile → Fetch 整文件取回
	assert.True(t, IsFile(m))
	all, err := m.Fetch(ctx, m.hash)
	require.NoError(t, err)
	assert.Equal(t, data, all)

	// CapMeta → Info 返回非 nil
	assert.True(t, IsMeta(m))
	fi, err := m.Info(ctx, m.hash)
	require.NoError(t, err)
	require.NotNil(t, fi)
	assert.Equal(t, m.hash, fi.Hash)

	// CapVerify → 声明了就必须真校验（verifyReadCloser 的行为见 TestContract_FullRequestVerification）
	assert.True(t, IsVerify(m))

	// 非流式源（CapFile only，URL 模板不带 %d）：OpenRange 必须跳过它
	url := NewURLSource("https://example.com/%s", nil)
	assert.False(t, IsStream(url))
	assert.True(t, IsFile(url))
	assert.True(t, IsVerify(url)) // URLSource 整文件取回校验 sha256
	mgr := New()
	require.NoError(t, mgr.Register(url))
	_, err = mgr.OpenRange(ctx, makeValidHash(), 0, -1)
	require.Error(t, err, "没有流式源时 OpenRange 必须报错，不能退回 CapFile 全量缓冲")
}

// TestContract_OpenRangeSemantics 契约：offset<0 归一化为 0；size<0 表达到文件尾；
// offset+size 越界截断到文件尾。用 bufferMock（按 offset/size 切片）把偏移语义钉死。
func TestContract_OpenRangeSemantics(t *testing.T) {
	data := []byte("0123456789")
	m := newBufferMock("range-mock", data)
	s := NewShaSource("sha", m)
	ctx := context.Background()
	hash := m.hash

	cases := []struct {
		name   string
		offset int64
		size   int64
		want   string
	}{
		{name: "negative offset normalizes to 0", offset: -5, size: -1, want: "0123456789"},
		{name: "negative size means to end", offset: 3, size: -1, want: "3456789"},
		{name: "size clamps to end of file", offset: 7, size: 100, want: "789"},
		{name: "exact range", offset: 2, size: 3, want: "234"},
		{name: "zero-length range", offset: 5, size: 0, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := s.Open(ctx, hash, c.offset, c.size)
			require.NoError(t, err)
			defer r.Close()
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			assert.Equal(t, c.want, string(got))
		})
	}
}

// TestContract_FullRequestVerification 契约：声明 CapVerify 的源对整文件请求（offset==0
// && size<0）做 sha256 校验——内容寻址的基线。远端/对端内容可能被篡改或截断，不校验
// 就会把错误内容当成「就是这个文件」。用 verifyReadCloser 直接验证两种结果。
func TestContract_FullRequestVerification(t *testing.T) {
	t.Run("correct content passes", func(t *testing.T) {
		content := []byte("the truth")
		hash := sha256Hex(content)
		v := &verifyReadCloser{r: io.NopCloser(strings.NewReader(string(content))), hash: hash, label: "test"}
		got, err := io.ReadAll(v)
		assert.Equal(t, content, got)
		assert.NoError(t, err, "io.ReadAll swallows io.EOF: success must yield nil")
		assert.NoError(t, v.Close())
	})

	t.Run("tampered content fails at EOF", func(t *testing.T) {
		content := []byte("the truth")
		hash := sha256Hex(content) // 期望 hash 是正确内容的
		v := &verifyReadCloser{r: io.NopCloser(strings.NewReader("TAMPERED")), hash: hash, label: "test"}
		_, err := io.ReadAll(v)
		require.Error(t, err, "tampered content must fail verification")
		assert.Contains(t, err.Error(), "hash mismatch")
		assert.Contains(t, err.Error(), "test:")
		// 失败后再读只重复错误，不 panic
		_, err = v.Read(make([]byte, 16))
		require.Error(t, err)
	})

	t.Run("empty content verifies to empty-hash", func(t *testing.T) {
		hash := sha256Hex([]byte{})
		v := &verifyReadCloser{r: io.NopCloser(strings.NewReader("")), hash: hash, label: "test"}
		got, err := io.ReadAll(v)
		assert.Empty(t, got)
		assert.NoError(t, err, "io.ReadAll swallows io.EOF")
	})

	t.Run("underlying read error propagates", func(t *testing.T) {
		v := &verifyReadCloser{r: io.NopCloser(errReader(fmt.Errorf("boom"))), hash: sha256Hex([]byte("x")), label: "test"}
		_, err := io.ReadAll(v)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})
}

// TestContract_LimitedReadCloser 契约：LimitReader 限制读取长度（防越界读取），
// Close 关闭底层资源。
func TestContract_LimitedReadCloser(t *testing.T) {
	closed := 0
	data := io.NopCloser(strings.NewReader("0123456789"))
	l := &limitedReadCloser{r: io.LimitReader(data, 4), c: closerFunc(func() error {
		closed++
		return nil
	})}
	got, err := io.ReadAll(l)
	require.NoError(t, err)
	assert.Equal(t, "0123", string(got), "must truncate to the limit")
	require.NoError(t, l.Close())
	assert.Equal(t, 1, closed)
}

// TestContract_CloseIdempotent 契约：Open 返回的 io.ReadCloser 必须可安全重复 Close
// （调用方可能 defer + 显式 close）。用 manager 的真实路径验证不 panic。
func TestContract_CloseIdempotent(t *testing.T) {
	s := NewShaSource("sha", newBufferMock("close-mock", []byte("payload")))
	hash := makeValidHash()
	_ = hash // bufferMock 用自己的 hash
	ctx := context.Background()

	// manager 侧：多次 Close 不 panic
	m := New()
	require.NoError(t, m.Register(newBufferMock("close-mock-2", []byte("payload2"))))
	r, err := m.OpenRange(ctx, sha256Hex([]byte("payload2")), 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.NoError(t, r.Close(), "second Close must be a no-op, not a panic")

	_ = s
}

// TestContract_MockRoutingThroughManager 契约：Manager 的路由语义——优先级升序、
// Available 软健康检查跳过、失败聚合、能力位决定调用模式、统计记录。
func TestContract_MockRoutingThroughManager(t *testing.T) {
	ctx := context.Background()

	local := newBufferMock("local", []byte("LOCAL"))
	peer := newBufferMock("peer", []byte("PEER"))
	url := newBufferMock("url", []byte("URL"))
	local.priority = 0
	peer.priority = 1
	url.priority = 2

	m := New()
	require.NoError(t, m.Register(local))
	require.NoError(t, m.Register(peer))
	require.NoError(t, m.Register(url))

	// 1. 优先级升序：local 命中即返回，后面的不被调用
	r, err := m.OpenRange(ctx, sha256Hex([]byte("LOCAL")), 0, -1)
	require.NoError(t, err)
	got, _ := io.ReadAll(r)
	require.NoError(t, r.Close())
	assert.Equal(t, "LOCAL", string(got))
	assert.Equal(t, 1, local.opens, "local must be tried exactly once")
	assert.Equal(t, 0, peer.opens, "peer must not be tried after a local hit")

	// 2. Available==false 直接跳过（软健康检查，不做真实探测）
	peer.available = false
	_, err = m.OpenRange(ctx, sha256Hex([]byte("PEER")), 0, -1)
	require.Error(t, err, "local miss + peer unavailable + url not registered for this hash → must error")
	assert.Equal(t, 0, peer.opens, "unavailable peer must not be opened")

	// 3. 本地 miss → 降级 peer
	peer.available = true
	r, err = m.OpenRange(ctx, sha256Hex([]byte("PEER")), 0, -1)
	require.NoError(t, err)
	got, _ = io.ReadAll(r)
	require.NoError(t, r.Close())
	assert.Equal(t, "PEER", string(got))

	// 4. 非法 hash 在入口就被拒（不打到任何源）
	before := local.opens + peer.opens + url.opens
	_, err = m.OpenRange(ctx, "not-a-hash", 0, -1)
	require.Error(t, err)
	assert.Equal(t, before, local.opens+peer.opens+url.opens, "invalid hash must not reach any source")

	// 5. 统计：成功与失败都被记录
	st := m.Snapshot()
	byName := map[string]SourceStatus{}
	for _, s := range st {
		byName[s.Name] = s
	}
	assert.GreaterOrEqual(t, byName["local"].Stats.Success, int64(1))
	assert.GreaterOrEqual(t, byName["peer"].Stats.Success, int64(1))
	assert.Equal(t, "local", st[0].Name, "snapshot must be ordered by ascending priority")
	assert.Equal(t, "peer", st[1].Name)
	assert.Equal(t, "url", st[2].Name)
}

// TestContract_InfoPassthrough 契约：Info 按优先级找第一个有元数据的源；都不支持时
// 返回 nil, nil（不是错误）——这是可选能力的语义。
func TestContract_InfoPassthrough(t *testing.T) {
	ctx := context.Background()

	withInfo := newBufferMock("with-info", []byte("meta-data-here"))
	withoutInfo := newBufferMock("without-info", []byte("plain"))
	withoutInfo.infoNil = true
	withoutInfo.caps = CapStream | CapFile // 不声明 CapMeta
	m := New()
	require.NoError(t, m.Register(withInfo))
	require.NoError(t, m.Register(withoutInfo))

	fi, err := m.Info(ctx, withInfo.hash)
	require.NoError(t, err)
	require.NotNil(t, fi)
	assert.Equal(t, int64(len([]byte("meta-data-here"))), fi.Size)

	// 全部不支持：Manager.Info 收敛成「not found in any source」错误
	// （单个 Source.Info 的 nil,nil 语义在源头保留，Manager 不向上传播 nil）
	m2 := New()
	require.NoError(t, m2.Register(withoutInfo))
	fi, err = m2.Info(ctx, withoutInfo.hash)
	require.Error(t, err)
	assert.Nil(t, fi)

	// 源头语义：单个不支持元数据的 Source.Info 返回 nil, nil（可选能力，不是错误）
	fi, err = withoutInfo.Info(ctx, withoutInfo.hash)
	require.NoError(t, err)
	assert.Nil(t, fi)
}

// TestContract_MetaSourceOptional 契约：MetaSource 是可选增强——实现了就走 OpenMeta
// （少一次往返），没实现就回退到 Open + Info 组合。ShaSource 两种情况都要正确处理。
func TestContract_MetaSourceOptional(t *testing.T) {
	ctx := context.Background()

	t.Run("path implements MetaSource", func(t *testing.T) {
		metaSrc := newBufferMock("meta", []byte("ABCDE"))
		s := NewShaSource("sha", metaSrc)
		r, fi, err := s.OpenMeta(ctx, metaSrc.hash, 1, 2)
		require.NoError(t, err)
		defer r.Close()
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, "BC", string(got))
		require.NotNil(t, fi)
		assert.Equal(t, int64(5), fi.Size)
		assert.Equal(t, metaSrc.hash, fi.Hash)
	})

	t.Run("path without MetaSource falls back to Open+Info", func(t *testing.T) {
		// 真实实现 LocalSource 只实现 Source（未实现 MetaSource），正好验证回退路径。
		dir := t.TempDir()
		content := "FGHIJ"
		h := sha256Hex([]byte(content))
		writeCAS(t, dir, h, content)
		plain := NewLocalSource(dir, nil)
		s := NewShaSource("sha", plain)
		r, fi, err := s.OpenMeta(ctx, h, 0, -1)
		require.NoError(t, err)
		defer r.Close()
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, content, string(got))
		require.NotNil(t, fi)
		assert.Equal(t, int64(len(content)), fi.Size, "Size must come from Info fallback")
	})
}

// TestContract_OpenMetaOf 契约：OpenMetaOf 是类型断言助手，ok=false 表示未实现。
func TestContract_OpenMetaOf(t *testing.T) {
	_, ok := OpenMetaOf(newBufferMock("mock", []byte("x")))
	assert.True(t, ok)
	_, ok = OpenMetaOf(NewLocalSource(t.TempDir(), nil))
	assert.False(t, ok)
	_, ok = OpenMetaOf(&EchSource{})
	assert.True(t, ok)
	_, ok = OpenMetaOf(&ShaSource{})
	assert.True(t, ok)
}

// TestContract_BufferMockRangeBehavior 契约：一个 Source 的 Open 必须尊重 offset/size
// 并返回恰好请求的分片（bufferMock 自身行为，作为其他测试的可信基线）。
func TestContract_BufferMockRangeBehavior(t *testing.T) {
	data := []byte("0123456789")
	m := newBufferMock("baseline", data)
	ctx := context.Background()
	cases := []struct {
		offset int64
		size   int64
		want   string
	}{
		{0, -1, "0123456789"},
		{-1, -1, "0123456789"},
		{4, -1, "456789"},
		{4, 3, "456"},
		{8, 100, "89"},
		{9, 1, "9"},
		{0, 0, ""},
		{10, 1, ""}, // offset == len → 空
	}
	for _, c := range cases {
		r, err := m.Open(ctx, m.hash, c.offset, c.size)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, c.want, string(got), "offset=%d size=%d", c.offset, c.size)
	}
	// Fetch = Open(0,-1) 的整文件语义
	got, err := m.Fetch(ctx, m.hash)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}

// ---- mock 实现 ----

// bufferMock 是可编程的 Source：只服务自己拥有的内容地址（hash != m.hash 返回错误，
// 模拟真实源「我没有这个文件」的语义），按 offset/size 对固定缓冲切片返回。
// 同时实现 MetaSource，便于契约测试覆盖元数据路径。
//
// infoNil=true 时 Info 返回 nil, nil，模拟「不支持元数据」的源（可选能力语义）。
type bufferMock struct {
	name      string
	priority  int
	caps      Capability
	data      []byte
	hash      string
	available bool
	verify    bool
	infoNil   bool

	mu    sync.Mutex
	opens int
}

func newBufferMock(name string, data []byte) *bufferMock {
	return &bufferMock{
		name:      name,
		caps:      CapStream | CapFile | CapMeta | CapVerify,
		data:      append([]byte(nil), data...),
		hash:      sha256Hex(data),
		available: true,
		verify:    true,
	}
}

func (m *bufferMock) Name() string             { return m.name }
func (m *bufferMock) Type() string             { return "mock" }
func (m *bufferMock) Capabilities() Capability { return m.caps }
func (m *bufferMock) Priority() int            { return m.priority }
func (m *bufferMock) SetPriority(p int)        { m.priority = p }
func (m *bufferMock) Available(context.Context) bool {
	return m.available
}

// errNotFound 该 mock 不拥有这个内容地址（真实源的「我没有这个文件」语义）。
func (m *bufferMock) errNotFound(hash string) error {
	if hash == m.hash {
		return nil
	}
	return fmt.Errorf("mock %q: not found %s", m.name, hash[:8])
}

func (m *bufferMock) slice(offset, size int64) []byte {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(m.data)) {
		offset = int64(len(m.data))
	}
	end := int64(len(m.data))
	if size >= 0 && offset+size < end {
		end = offset + size
	}
	out := make([]byte, end-offset)
	copy(out, m.data[offset:end])
	return out
}

func (m *bufferMock) Open(_ context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if err := m.errNotFound(hash); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.opens++
	m.mu.Unlock()
	return io.NopCloser(bytes.NewReader(m.slice(offset, size))), nil
}

func (m *bufferMock) OpenMeta(_ context.Context, hash string, offset, size int64) (io.ReadCloser, *FileMeta, error) {
	r, err := m.Open(context.Background(), hash, offset, size)
	if err != nil {
		return nil, nil, err
	}
	return r, &FileMeta{Hash: hash, Size: int64(len(m.data))}, nil
}

func (m *bufferMock) Fetch(_ context.Context, hash string) ([]byte, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	return append([]byte(nil), m.data...), nil
}

func (m *bufferMock) Info(_ context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if m.infoNil {
		return nil, nil // 不支持元数据：返回 nil, nil（可选能力语义）
	}
	return &FileMeta{Hash: hash, Size: int64(len(m.data))}, nil
}

func (m *bufferMock) openCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opens
}

// ---- 测试辅助 ----

// sha256Hex 计算内容的 64 位小写 hex sha256（契约测试用的内容地址）。
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// makeValidHash 返回一个合法的 64 位小写 hex hash（用于只需要「合法形状」的测试）。
func makeValidHash() string {
	return sha256Hex([]byte("valid-shape"))
}

// writeCAS 按内容寻址布局 dir/<h[:2]>/<h> 写入一个文件（LocalSource 的 CAS 兜底路径）。
func writeCAS(t *testing.T, dir, hash, content string) {
	t.Helper()
	p := filepath.Join(dir, hash[:2], hash)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// errReader 永远返回错误的 Reader。
type errReaderFn func([]byte) (int, error)

func (f errReaderFn) Read(p []byte) (int, error) { return f(p) }

func errReader(err error) io.Reader {
	return errReaderFn(func([]byte) (int, error) { return 0, err })
}

// closerFunc 函数式 Closer。
type closerFunc func() error

func (c closerFunc) Close() error { return c() }
