package source

// sha_test.go: ShaSource 表驱动测试（本地优先 → peer 兜底的多路径合并）。
//
// 发现背景：sha 是地址、取是路径。旧装配把 LocalSource 和 PeerSource 作为两个独立源注册，
// 「本地优先、peer 兜底」靠 Manager 的优先级排序隐式实现；新接口把它们收敛成一个 ShaSource
// 的多条 paths，降级链写在源内部、语义显式。这两条必须等价，否则装配切换会改变取回顺序。
// 因此这里用**真实** LocalSource（file_index + CAS，SQLite :memory:）+ **真实** PeerSource
// （经 newRaceSvc 注入内存 peer，走真实的 req/meta/data/done 帧）验证两条路径，
// 只有注入失败用的可控路径（bufferMock）是 mock。
//
// 覆盖：能力位并集、Available 并集、本地命中不碰 peer、本地未命中走 peer 真实帧、
// 全部路径失败的聚合错误、跳过不可用/非流式路径、OpenMeta 的 Info 回查与 MetaSource 分支、
// Info 首个非 nil 胜出、LocalControl 委托与不支持、Fetch/入口 hash 校验、并发安全。

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

// newShaFixture 一个带 file_index（SQLite :memory:）与 CAS 目录的本地源。
// InitDB 每次调用都会重建全局 DB，所以每个用例各自初始化是安全的。
func newShaFixture(t *testing.T) (storageDir, idxDir string, local *LocalSource) {
	t.Helper()
	require.NoError(t, repository.InitDB(":memory:"))
	storageDir = t.TempDir()
	idxDir = t.TempDir()
	return storageDir, idxDir, NewLocalSource(storageDir, transport.NewFileIndexService(idxDir))
}

// TestShaSource_Basics name 默认值与 Type 标签。
func TestShaSource_Basics(t *testing.T) {
	assert.Equal(t, "sha", NewShaSource("").Name())
	assert.Equal(t, "mysha", NewShaSource("mysha").Name())
	assert.Equal(t, "sha", NewShaSource("x").Type())

	// 运行时可调整优先级（统一管理面能力）
	s := NewShaSource("x")
	assert.Equal(t, 0, s.Priority())
	s.SetPriority(3)
	assert.Equal(t, 3, s.Priority())
}

// TestShaSource_CapabilitiesUnion 能力位是各路径的并集——任一路径能流式就声明 CapStream，
// 否则 Manager.OpenRange 会整体跳过这个源（路由信任声明，声明错误即静默失败）。
func TestShaSource_CapabilitiesUnion(t *testing.T) {
	cases := []struct {
		name string
		caps []Capability
		want Capability
	}{
		{"empty", nil, 0},
		{"single", []Capability{CapStream | CapMeta}, CapStream | CapMeta},
		{"file only", []Capability{CapFile | CapVerify}, CapFile | CapVerify},
		{
			"union across paths",
			[]Capability{CapStream | CapMeta, CapFile | CapVerify},
			CapStream | CapFile | CapMeta | CapVerify,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			paths := make([]Source, 0, len(c.caps))
			for _, cap := range c.caps {
				m := newBufferMock("m", []byte("x"))
				m.caps = cap
				paths = append(paths, m)
			}
			assert.Equal(t, c.want, NewShaSource("sha", paths...).Capabilities())
		})
	}
}

// TestShaSource_Availability 任一路径可用即可用（本地目录可读 OR 有在线对端）；
// 全部不可用或没有路径 → false，Manager 会把它整条跳过。
func TestShaSource_Availability(t *testing.T) {
	ctx := context.Background()

	t.Run("no paths is unavailable", func(t *testing.T) {
		assert.False(t, NewShaSource("sha").Available(ctx))
	})

	t.Run("any path available is enough", func(t *testing.T) {
		ok := newBufferMock("ok", []byte("x"))
		dead := newBufferMock("dead", []byte("x"))
		dead.available = false
		assert.True(t, NewShaSource("sha", dead, ok).Available(ctx))
	})

	t.Run("all paths unavailable", func(t *testing.T) {
		a := newBufferMock("a", []byte("x"))
		b := newBufferMock("b", []byte("x"))
		a.available = false
		b.available = false
		assert.False(t, NewShaSource("sha", a, b).Available(ctx))
	})

	t.Run("real local source tracks the storage dir", func(t *testing.T) {
		storageDir, _, local := newShaFixture(t)
		s := NewShaSource("sha", local)
		assert.True(t, s.Available(ctx))
		// 目录被删后立刻不可用（软健康检查，不做网络探测）
		require.NoError(t, os.RemoveAll(storageDir))
		assert.False(t, s.Available(ctx))
	})
}

// TestShaSource_OpenLocalHit 本地命中：CAS 与 file_index 两条本地子路径都能服务，
// 且不触碰 peer 路径（peer 从未收到请求帧）。这是「本地是权威」的核心保证。
func TestShaSource_OpenLocalHit(t *testing.T) {
	ctx := context.Background()

	t.Run("cas path", func(t *testing.T) {
		storageDir, _, local := newShaFixture(t)
		peer := newBufferMock("peer", []byte("not-mine")) // 不拥有这个 hash → 必然失败
		content := "cas-content-0123456789"
		hash := sha256Hex([]byte(content))
		writeCAS(t, storageDir, hash, content)

		s := NewShaSource("sha", local, peer)
		r, err := s.Open(ctx, hash, 5, 6)
		require.NoError(t, err)
		got, err := readAllTimeout(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, []byte(content[5:11]), got)
		assert.Equal(t, 0, peer.openCount(), "local hit must not reach the peer path")
	})

	t.Run("file_index path", func(t *testing.T) {
		_, idxDir, local := newShaFixture(t)
		content := "indexed-content"
		p := filepath.Join(idxDir, "a.txt")
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		created, err := local.fileIndex.Create(p)
		require.NoError(t, err)

		peer := newBufferMock("peer", []byte("not-mine"))
		s := NewShaSource("sha", local, peer)
		r, err := s.Open(ctx, created.Hash, 0, -1)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, content, string(got))
		assert.Equal(t, 0, peer.openCount(), "local hit must not reach the peer path")
	})
}

// TestShaSource_PeerFallback 本地未命中 → 真实 peer 通道兜底（走 req/meta/data/done 帧）。
// 用真实 PeerJSService 而不是 mock，因为 peer 路径的并发 race、单槽锁、trace 防环都是
// 不可 mock 的行为——本用例只要保证「本地没货时 ShaSource 会把请求交出去并读回完整内容」。
func TestShaSource_PeerFallback(t *testing.T) {
	ctx := context.Background()
	_, _, local := newShaFixture(t)
	_, sess, peer := newRaceSvc(t, "peerA")
	content := []byte(strings.Repeat("peer-fallback-", 64))
	hash := sha256Hex(content)

	s := NewShaSource("sha", local, peer)

	r, err := s.Open(ctx, hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, sess[0])
	feedFullResponse(t, sess[0], hash, content)

	got, err := readAllTimeout(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, content, got, "peer fallback must return complete content")
}

// TestShaSource_PeerFallbackRange 分片请求走 peer 通道：offset/size 透传给 req 帧，
// 对端返回的正是被请求的分片（进度条/断点续传依赖这一点）。
func TestShaSource_PeerFallbackRange(t *testing.T) {
	ctx := context.Background()
	_, _, local := newShaFixture(t)
	_, sess, peer := newRaceSvc(t, "peerA")
	content := []byte("0123456789")
	hash := sha256Hex(content)

	s := NewShaSource("sha", local, peer)
	r, err := s.Open(ctx, hash, 3, 4)
	require.NoError(t, err)
	waitReqs(t, sess[0])
	// 对端只回被请求的 3..6 分片
	feedFullResponse(t, sess[0], hash, content[3:7])

	got, err := readAllTimeout(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, content[3:7], got)
}

// TestShaSource_AllPathsFail 全部路径失败：返回聚合错误（带每条路径名与原因），
// 便于多源排查；不能吞掉原因只回一个笼统的「all failed」。
func TestShaSource_AllPathsFail(t *testing.T) {
	ctx := context.Background()
	_, _, local := newShaFixture(t) // CAS 目录里没有任何东西

	l1 := newBufferMock("l1", []byte("l1"))
	l2 := newBufferMock("l2", []byte("l2"))
	s := NewShaSource("sha", local, l1, l2)

	_, err := s.Open(ctx, sha256Hex([]byte("missing")), 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha: all paths failed")
	assert.Contains(t, err.Error(), "l2") // 最后一条路径的原因被保留

	// Fetch / OpenMeta 同语义
	_, err = s.Fetch(ctx, sha256Hex([]byte("missing")))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all paths failed")

	_, _, err = s.OpenMeta(ctx, sha256Hex([]byte("missing")), 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all paths failed")
}

// TestShaSource_NoUsablePath 没有任何可用路径（非流式/空）→ 明确报「no usable path」，
// 而不是返回一个 nil reader 让上层 panic。
func TestShaSource_NoUsablePath(t *testing.T) {
	ctx := context.Background()

	t.Run("no paths", func(t *testing.T) {
		_, err := NewShaSource("sha").Open(ctx, makeValidHash(), 0, -1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no usable path")
	})

	t.Run("file only paths are not usable for streaming", func(t *testing.T) {
		m := newBufferMock("fileonly", []byte("x"))
		m.caps = CapFile | CapVerify // 没有 CapStream
		_, err := NewShaSource("sha", m).Open(ctx, m.hash, 0, -1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no usable path")
		assert.Equal(t, 0, m.openCount(), "non-stream path must be skipped")
	})
}

// TestShaSource_SkipsUnavailablePath 不可用路径被跳过（不进入请求），但原因仍然参与聚合；
// 可用路径继续被尝试。对应 Manager.OpenRange 的 Available 门控语义。
func TestShaSource_SkipsUnavailablePath(t *testing.T) {
	ctx := context.Background()

	dead := newBufferMock("dead", []byte("x"))
	dead.available = false
	dead.data = []byte("whatever")

	t.Run("skipped and reported as unavailable", func(t *testing.T) {
		live := newBufferMock("live", []byte("live-data"))
		live.available = false // 两条都不可用
		_, err := NewShaSource("sha", dead, live).Open(ctx, makeValidHash(), 0, -1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "live")
		assert.Contains(t, err.Error(), "unavailable")
		assert.Equal(t, 0, dead.openCount())
		assert.Equal(t, 0, live.openCount())
	})

	t.Run("fallback after an unavailable path still works", func(t *testing.T) {
		content := []byte("fallback-data")
		hash := sha256Hex(content)
		peer := newBufferMock("peer", content) // 拥有这个 hash
		r, err := NewShaSource("sha", dead, peer).Open(ctx, hash, 0, -1)
		require.NoError(t, err, "an unavailable path must not stop the fallback chain")
		got, err := readAllTimeout(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, content, got)
		assert.Equal(t, 0, dead.openCount(), "the unavailable path must not be opened")
	})
}

// TestShaSource_OpenMeta MetaSource 分支与 Info 回查：
//   - 路径实现了 MetaSource → 直接用它的 OpenMeta（少一次往返）；
//   - meta.Size 未知（-1）→ 回查该路径的 Info 补总长（进度条需要总长）；
//   - 路径未实现 MetaSource → Open + Info 组合；
//   - Hash 缺失时回填请求的 hash。
func TestShaSource_OpenMeta(t *testing.T) {
	ctx := context.Background()

	t.Run("uses the path's OpenMeta directly", func(t *testing.T) {
		content := []byte("meta-direct")
		m := newBufferMock("m", content)
		s := NewShaSource("sha", m)
		r, fi, err := s.OpenMeta(ctx, m.hash, 2, 3)
		require.NoError(t, err)
		got, err := readAllTimeout(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.NotNil(t, fi)
		assert.Equal(t, content[2:5], got)
		assert.Equal(t, m.hash, fi.Hash)
		assert.Equal(t, int64(len(content)), fi.Size)
	})

	t.Run("probes Info when the meta size is unknown", func(t *testing.T) {
		content := []byte("size-unknown")
		m := newBufferMock("m", content)
		wrapped := &metaMinusOne{bufferMock: m}
		s := NewShaSource("sha", wrapped)
		r, fi, err := s.OpenMeta(ctx, m.hash, 0, -1)
		require.NoError(t, err)
		_, err = readAllTimeout(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.NotNil(t, fi)
		assert.Equal(t, int64(len(content)), fi.Size, "unknown size must be backfilled via Info")
	})

	t.Run("open plus info fallback for non-metasource paths", func(t *testing.T) {
		content := []byte("no-meta")
		p := &noMetaSource{name: "p", hash: sha256Hex(content), data: content}
		s := NewShaSource("sha", p)
		r, fi, err := s.OpenMeta(ctx, p.hash, 0, -1)
		require.NoError(t, err)
		got, err := readAllTimeout(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.NotNil(t, fi)
		assert.Equal(t, content, got)
		assert.Equal(t, int64(len(content)), fi.Size)
	})

	t.Run("backfills the requested hash", func(t *testing.T) {
		content := []byte("hash-fill")
		wrapped := &metaMinusOne{bufferMock: newBufferMock("m", content), hash: "fillme"}
		s := NewShaSource("sha", wrapped)
		_, fi, err := s.OpenMeta(ctx, sha256Hex(content), 0, -1)
		require.NoError(t, err)
		require.NoError(t, wrapped.close())
		require.NotNil(t, fi)
		// 路径给的 meta 带了别的 hash：原样保留（调用方要自己判断是否可信）；
		// 只有 meta.Hash 为空时才回填请求的 hash（见 noMetaSource 用例）。
		assert.Equal(t, "fillme", fi.Hash)

		wrapped.hash = ""
		_, fi2, err := s.OpenMeta(ctx, sha256Hex(content), 0, -1)
		require.NoError(t, err)
		require.NoError(t, wrapped.close())
		require.NotNil(t, fi2)
		assert.Equal(t, sha256Hex(content), fi2.Hash, "an empty meta hash must be backfilled")
	})
}

// TestShaSource_Info Info 按路径序查询，第一个返回非 nil 的路径胜出（本地路径带 name/path，
// 元数据最完整）；全部 nil → 返回 nil,nil（可选能力语义，不是错误）；有错误 → 上抛。
func TestShaSource_Info(t *testing.T) {
	ctx := context.Background()

	t.Run("first non-nil wins", func(t *testing.T) {
		content := []byte("info-one")
		a := newBufferMock("a", content)
		b := newBufferMock("b", content)
		b.infoNil = true // b 不支持元数据
		fi, err := NewShaSource("sha", b, a).Info(ctx, a.hash)
		require.NoError(t, err)
		require.NotNil(t, fi)
		assert.Equal(t, a.hash, fi.Hash)
	})

	t.Run("first path wins when both answer", func(t *testing.T) {
		content := []byte("info-first")
		a := newBufferMock("a", content)
		b := newBufferMock("b", content)
		b.data = []byte("different-size")
		fi, err := NewShaSource("sha", a, b).Info(ctx, a.hash)
		require.NoError(t, err)
		require.NotNil(t, fi)
		assert.Equal(t, int64(len(content)), fi.Size, "first path's size must win")
	})

	t.Run("all nil returns nil nil", func(t *testing.T) {
		a := newBufferMock("a", []byte("x"))
		b := newBufferMock("b", []byte("y"))
		a.infoNil = true
		b.infoNil = true
		fi, err := NewShaSource("sha", a, b).Info(ctx, makeValidHash())
		require.NoError(t, err)
		assert.Nil(t, fi, "unsupported meta is nil,nil, not an error")
	})

	t.Run("error from a path is surfaced", func(t *testing.T) {
		p := &noMetaSource{name: "p", hash: makeValidHash(), data: []byte("x"), err: errors.New("boom")}
		_, err := NewShaSource("sha", p).Info(ctx, p.hash)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})
}

// TestShaSource_Fetch 整文件取回：委托 Open(0,-1)+ReadAll，保留各路径自己的校验语义。
func TestShaSource_Fetch(t *testing.T) {
	ctx := context.Background()
	content := []byte("fetch-through-sha")
	m := newBufferMock("m", content)
	s := NewShaSource("sha", m)
	got, err := s.Fetch(ctx, m.hash)
	require.NoError(t, err)
	assert.Equal(t, content, got)
}

// TestShaSource_InvalidHash 所有入口都拒绝非法 hash（64 位小写 hex）——对端/HTTP 传入的
// hash 不可信，裸取 hash[:2] 会越界 panic，而源调用常在 goroutine 里。
func TestShaSource_InvalidHash(t *testing.T) {
	ctx := context.Background()
	s := NewShaSource("sha", newBufferMock("m", []byte("x")))
	for _, bad := range []string{"", "short", "zz", strings.Repeat("a", 63), strings.Repeat("a", 65)} {
		t.Run("bad hash "+strconvQuote(bad), func(t *testing.T) {
			_, err := s.Open(ctx, bad, 0, -1)
			require.Error(t, err)
			_, _, err = s.OpenMeta(ctx, bad, 0, -1)
			require.Error(t, err)
			_, err = s.Fetch(ctx, bad)
			require.Error(t, err)
			_, err = s.Info(ctx, bad)
			require.Error(t, err)
		})
	}
}

// TestShaSource_LocalControlDelegation 控制面委托：AddLocalFile / WriteFile 委托给第一条
// 实现了 LocalControl 的路径（本地路径）。这是「用 NewShaSource("local", local, peer) 替换
// 两条独立注册」能保持 /sources/local/* 控制面可用的前提。
func TestShaSource_LocalControlDelegation(t *testing.T) {
	ctx := context.Background()
	storageDir, idxDir, local := newShaFixture(t)
	peer := newBufferMock("peer", []byte("not-mine"))
	s := NewShaSource("local", local, peer)

	lc, ok := LocalControlOf(s)
	require.True(t, ok, "ShaSource with a local path must expose LocalControl")

	// AddLocalFile：把 idxDir 下的一个已有文件登记进 file_index
	content := "delegated-add"
	p := filepath.Join(idxDir, "in.txt")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	fi, err := lc.AddLocalFile(p)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex([]byte(content)), fi.Hash)
	assert.Equal(t, int64(len(content)), fi.Size)
	assert.NotEmpty(t, fi.Path)

	// 登记后必须能通过 sha 源读到（读路径与写路径闭环）
	r, err := s.Open(ctx, fi.Hash, 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, content, string(got))

	// WriteFile：直接写入并登记
	fi2, err := lc.WriteFile("written.bin", strings.NewReader("delegated-write"))
	require.NoError(t, err)
	assert.Equal(t, sha256Hex([]byte("delegated-write")), fi2.Hash)
	got2, err := s.Fetch(ctx, fi2.Hash)
	require.NoError(t, err)
	assert.Equal(t, "delegated-write", string(got2))

	// 非委托路径（CAS 目录）没有被写脏：storageDir 里不应出现这两个文件
	entries, err := os.ReadDir(storageDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "delegation must not touch the CAS directory")
}

// TestShaSource_LocalControlSkipsNonLocalPaths 委托会跳过不支持 LocalControl 的路径，
// 命中第一条支持的路径（paths 顺序：mock 在前、local 在后）。
func TestShaSource_LocalControlSkipsNonLocalPaths(t *testing.T) {
	ctx := context.Background()
	_, idxDir, local := newShaFixture(t)
	s := NewShaSource("sha", newBufferMock("mock", []byte("x")), local)

	lc, ok := LocalControlOf(s)
	require.True(t, ok)
	p := filepath.Join(idxDir, "skip.txt")
	require.NoError(t, os.WriteFile(p, []byte("skip-target"), 0o644))
	fi, err := lc.AddLocalFile(p)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex([]byte("skip-target")), fi.Hash)

	r, err := s.Open(ctx, fi.Hash, 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, "skip-target", string(got))
}

// TestShaSource_LocalControlUnsupported 没有本地路径 → ErrControlUnsupported（不是 panic，
// 不是 nil,nil）：控制面入口必须能给出明确的可诊断错误。
func TestShaSource_LocalControlUnsupported(t *testing.T) {
	s := NewShaSource("sha", newBufferMock("peer", []byte("x")))
	_, err := s.AddLocalFile("/tmp/whatever")
	require.ErrorIs(t, err, ErrControlUnsupported)
	_, err = s.WriteFile("f.txt", strings.NewReader("x"))
	require.ErrorIs(t, err, ErrControlUnsupported)
}

// metaMinusOne 一个 OpenMeta 返回 Size=-1 的包装（模拟「打开时不知道总长」的 meta 帧语义），
// 用于覆盖 ShaSource.OpenMeta 的 Info 回查分支。hash 为空表示 meta 没带 hash（走回填）。
type metaMinusOne struct {
	*bufferMock
	hash string
	r    io.ReadCloser
}

func (m *metaMinusOne) OpenMeta(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, *FileMeta, error) {
	r, err := m.bufferMock.Open(ctx, hash, offset, size)
	if err != nil {
		return nil, nil, err
	}
	m.r = r
	return r, &FileMeta{Hash: m.hash, Size: -1}, nil
}

func (m *metaMinusOne) close() error {
	if m.r == nil {
		return nil
	}
	return m.r.Close()
}

// noMetaSource 只实现 Source 接口（没有 OpenMeta）——覆盖 ShaSource.OpenMeta 的
// 「路径未实现 MetaSource → Open + Info 组合」回退分支。
type noMetaSource struct {
	name string
	hash string
	data []byte
	err  error // Info 用：模拟路径错误上抛
}

func (n *noMetaSource) Name() string                   { return n.name }
func (n *noMetaSource) Type() string                   { return "mock" }
func (n *noMetaSource) Capabilities() Capability       { return CapStream | CapMeta }
func (n *noMetaSource) Priority() int                  { return 0 }
func (n *noMetaSource) SetPriority(int)                {}
func (n *noMetaSource) Available(context.Context) bool { return true }
func (n *noMetaSource) Open(ctx context.Context, hash string, _, _ int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(n.data)), nil
}
func (n *noMetaSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	return append([]byte(nil), n.data...), nil
}
func (n *noMetaSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if n.err != nil {
		return nil, n.err
	}
	return &FileMeta{Hash: n.hash, Size: int64(len(n.data))}, nil
}

// strconvQuote 测试辅助：给 subtest 名一个可读的表示（避免空字符串做名字）。
func strconvQuote(s string) string {
	if s == "" {
		return "empty"
	}
	if len(s) > 6 {
		return s[:4] + "..."
	}
	return s
}

// TestShaSource_Concurrent 并发打开 + 运行时改优先级（-race 下验证 paths 遍历与
// bufferMock 计数、ShaSource 的 RWMutex 无数据竞争）。
func TestShaSource_Concurrent(t *testing.T) {
	ctx := context.Background()
	content := []byte("sha-race-content")
	m := newBufferMock("m", content)
	s := NewShaSource("sha", m)

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var off, size int64
			if i%3 == 0 {
				off, size = 0, -1
			} else if i%3 == 1 {
				off, size = 1, 4
			} else {
				off, size = 0, int64(len(content))
			}
			r, err := s.Open(ctx, m.hash, off, size)
			if err != nil {
				errs <- err
				return
			}
			got, err := io.ReadAll(r)
			if err != nil {
				errs <- err
				return
			}
			if err := r.Close(); err != nil {
				errs <- err
				return
			}
			var want string
			if size < 0 {
				want = string(content)
			} else {
				want = string(content[off : off+size])
			}
			if string(got) != want {
				errs <- errors.New("goroutine content mismatch")
			}
			if i%2 == 0 {
				s.SetPriority(i) // 并发改优先级
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	assert.GreaterOrEqual(t, m.openCount(), 16)
}

// TestShaSource_ManagerRouting 把 ShaSource 注册进 Manager：能力位、Available 门控、
// 统计与降级链的语义与单个源一致（路由层完全信任源声明）。
func TestShaSource_ManagerRouting(t *testing.T) {
	ctx := context.Background()
	content := []byte("sha-manager-routing")
	hash := sha256Hex(content)
	m := newBufferMock("inner", content)
	sha := NewShaSource("sha", m)
	sha.SetPriority(1)
	url := newBufferMock("url", content)
	url.priority = 5

	mgr := New()
	require.NoError(t, mgr.Register(sha))
	require.NoError(t, mgr.Register(url))

	r, err := mgr.OpenRange(ctx, hash, 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, content, got)

	// 统计：sha 源成功一次；url 从未被尝试
	st := mgr.Snapshot()
	byName := map[string]SourceStatus{}
	for _, s := range st {
		byName[s.Name] = s
	}
	assert.GreaterOrEqual(t, byName["sha"].Stats.Success, int64(1))
	assert.Equal(t, int64(0), byName["url"].Stats.Success)
	assert.Equal(t, "sha", byName["sha"].Type)

	// sha 源不可用时，Manager 继续降级到下一个源
	m.available = false
	r2, err := mgr.OpenRange(ctx, hash, 0, -1)
	require.NoError(t, err)
	got2, err := io.ReadAll(r2)
	require.NoError(t, err)
	require.NoError(t, r2.Close())
	assert.Equal(t, content, got2)

	st = mgr.Snapshot()
	byName = map[string]SourceStatus{}
	for _, s := range st {
		byName[s.Name] = s
	}
	assert.False(t, byName["sha"].Available)
	assert.GreaterOrEqual(t, byName["url"].Stats.Success, int64(1))
}

// TestShaSource_RealPeerUnavailable 本地可用、peer 无在线对端 → Available 仍为真
// （本地优先），且 Open 命中本地（peer 路径不可用不影响）。
func TestShaSource_RealPeerUnavailable(t *testing.T) {
	ctx := context.Background()
	storageDir, _, local := newShaFixture(t)
	content := []byte("local-only-peer-down")
	hash := sha256Hex(content)
	writeCAS(t, storageDir, hash, string(content))

	_, _, peer := newRaceSvc(t) // 没有任何对端 → PeerSource.Available()==false
	assert.False(t, peer.Available(ctx), "no online peer -> peer source unavailable")

	s := NewShaSource("sha", local, peer)
	assert.True(t, s.Available(ctx), "a live local path keeps the sha source available")

	r, err := s.Open(ctx, hash, 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	assert.Equal(t, content, got)
}

// TestShaSource_PeerTimeout 所有路径都失败且失败发生在**读取阶段**（不是 Open 阶段）：
// 对端收到 req 后不回帧，reader 必须超时报错而不是永久挂住。
func TestShaSource_PeerTimeout(t *testing.T) {
	ctx := context.Background()
	_, _, local := newShaFixture(t)
	_, sess, peer := newRaceSvc(t, "peerA")
	hash := sha256Hex([]byte("never-answered"))

	s := NewShaSource("sha", local, peer)
	r, err := s.Open(ctx, hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, sess[0])
	// 故意不回帧

	got, err := readAllTimeout(r)
	require.Error(t, err, "a peer that never answers must time out")
	assert.Nil(t, got)
}

// TestShaSource_OpensSkipsDeadlock 单条路径不可用 + 无其它路径 → 快速失败（不挂住）。
func TestShaSource_OpensSkipsDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dead := newBufferMock("dead", []byte("x"))
	dead.available = false
	s := NewShaSource("sha", dead)
	_, err := s.Open(ctx, makeValidHash(), 0, -1)
	require.Error(t, err)
}
