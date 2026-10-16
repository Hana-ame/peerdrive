package source

// ech_test.go: EchSource 表驱动测试（httptest 假远端，离线验证整条语义）。
//
// 发现背景：EchSource 是「取」这条链路的参考实现——resolver 把 sha 解析成直连 CDN URL，
// egress 走 echcore 域前置出网，整文件回来算 sha256 校验。真实出口要拨号 Cloudflare，
// 无法在 CI 里复现；因此把「出口」提成 Egress 接口，注入 httptest 假远端，让 Range 头、
// 206/200/416/3xx/4xx 分支、校验通过/失败、能力位声明都能在本地确定性验证。
//
// 假远端 fakeRemote 是「服务端行为可编程」的：支持/不支持 Range、强制返回指定状态码、
// 记录收到的请求（用于断言 Range / User-Agent / 自定义头是否正确发出）。

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- 假远端 ----

type fakeRemoteCfg struct {
	body         string // 服务端持有的内容
	rangeCapable bool   // 是否支持 Range（支持则按 Range 头回 206 分片）
	status       int    // 非 0 时强制返回该状态码（模拟 416/404/500/302 等分支）
}

// fakeRemote 记录收到的请求并返回可配置响应的假 CDN。
type fakeRemote struct {
	srv *httptest.Server
	cfg fakeRemoteCfg

	mu      sync.Mutex
	lastURL string
	lastReq http.Header
}

func newFakeRemote(t *testing.T, cfg fakeRemoteCfg) *fakeRemote {
	t.Helper()
	f := &fakeRemote{cfg: cfg}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRemote) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.lastURL = r.URL.String()
	f.lastReq = r.Header.Clone()
	f.mu.Unlock()

	if f.cfg.status != 0 {
		w.WriteHeader(f.cfg.status)
		return
	}
	off, size := int64(0), int64(-1)
	if rh := r.Header.Get("Range"); rh != "" {
		if o, s, ok := parseRangeHeader(rh); ok {
			off, size = o, s
		}
	}
	total := int64(len(f.cfg.body))
	if f.cfg.rangeCapable && (off > 0 || size >= 0) {
		lo := off
		hi := total
		if size >= 0 && off+size < total {
			hi = off + size
		}
		if lo >= total || lo >= hi {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", lo, hi-1, total))
		w.WriteHeader(http.StatusPartialContent)
		io.WriteString(w, f.cfg.body[lo:hi])
		return
	}
	io.WriteString(w, f.cfg.body)
}

func (f *fakeRemote) egress() Egress {
	return egressFunc(func(ctx context.Context, req *http.Request) (*http.Response, error) {
		if req.Context() == nil || req.Context() != ctx {
			req = req.WithContext(ctx)
		}
		return f.srv.Client().Do(req)
	})
}

func (f *fakeRemote) url(path string) string {
	return f.srv.URL + path
}

func (f *fakeRemote) request() (string, http.Header) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastURL, f.lastReq
}

// parseRangeHeader 解析 "bytes=start-end"（end 可空 = 到文件尾）。
func parseRangeHeader(h string) (off, size int64, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(h, prefix) {
		return 0, -1, false
	}
	parts := strings.SplitN(strings.TrimPrefix(h, prefix), "-", 2)
	if len(parts) != 2 {
		return 0, -1, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, -1, false
	}
	end := strings.TrimSpace(parts[1])
	if end == "" {
		return start, -1, true
	}
	e, err := strconv.ParseInt(end, 10, 64)
	if err != nil {
		return 0, -1, false
	}
	return start, e - start + 1, true
}

// staticResolver 固定返回一个 URL 的 resolver。
func staticResolver(url string) Resolver {
	return func(context.Context, string) (string, error) { return url, nil }
}

// recResolver 记录被解析的 hash（断言 resolver 确实收到了正确地址）。
type recResolver struct {
	mu   sync.Mutex
	hash string
	url  string
	err  error
}

func (r *recResolver) resolve(ctx context.Context, hash string) (string, error) {
	r.mu.Lock()
	r.hash = hash
	r.mu.Unlock()
	return r.url, r.err
}

func (r *recResolver) gotHash() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hash
}

// newEch 用假远端搭一个默认可用的 EchSource（开校验、开 Range）。
func newEch(t *testing.T, f *fakeRemote, path string) *EchSource {
	return NewEchSource("ech", staticResolver(f.url(path)), WithEgress(f.egress()))
}

// TestEchSource_RangeSemantics 表驱动：offset/size 如何映射成 Range 头与服务端分支。
// 覆盖：整文件 200、206 分片、服务端不支持 Range（200 全量后本地抽取）、416、4xx、3xx。
func TestEchSource_RangeSemantics(t *testing.T) {
	body := "0123456789"
	hash := sha256Hex([]byte(body))
	ctx := context.Background()

	cases := []struct {
		name         string
		rangeCapable bool
		status       int
		offset       int64
		size         int64
		want         string
		wantRange    string
		wantSize     int64
		wantErr      string
	}{
		{
			name: "full request via 200", offset: 0, size: -1,
			rangeCapable: true, want: body, wantRange: "", wantSize: int64(len(body)),
		},
		{
			name: "range returns 206 slice", offset: 3, size: 4,
			rangeCapable: true, want: "3456", wantRange: "bytes=3-6", wantSize: -1,
		},
		{
			name: "range to end", offset: 7, size: -1,
			rangeCapable: true, want: "789", wantRange: "bytes=7-", wantSize: -1,
		},
		{
			name: "negative offset normalizes to 0", offset: -1, size: 3,
			rangeCapable: true, want: "012", wantRange: "bytes=0-2", wantSize: -1,
		},
		{
			name: "server ignores range extracts locally", offset: 4, size: 3,
			rangeCapable: false, want: "456", wantRange: "bytes=4-6", wantSize: -1,
		},
		{
			name: "range not satisfiable", offset: 100, size: 10,
			rangeCapable: true, wantRange: "bytes=100-109", wantErr: "range not satisfiable",
		},
		{
			name: "404 rejected", offset: 0, size: -1, status: http.StatusNotFound,
			wantRange: "", wantErr: "HTTP 404",
		},
		{
			name: "500 rejected", offset: 0, size: -1, status: http.StatusInternalServerError,
			wantRange: "", wantErr: "HTTP 500",
		},
		{
			name: "302 not followed", offset: 0, size: -1, status: http.StatusFound,
			wantRange: "", wantErr: "redirect not followed",
		},
		{
			// 请求的 end 超出文件尾：EchSource 不做 HEAD 探测拿总长，Range 头原样发出，
			// 由服务端把 206 截断到实际可得段。契约是「结果正确」，不是「头最小」。
			name: "oversized range clamped by server", offset: 8, size: 100,
			rangeCapable: true, want: "89", wantRange: "bytes=8-107", wantSize: -1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRemote(t, fakeRemoteCfg{body: body, rangeCapable: c.rangeCapable, status: c.status})
			s := newEch(t, f, "/f/"+hash)

			r, err := s.Open(ctx, hash, c.offset, c.size)
			if c.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), c.wantErr)
				return
			}
			require.NoError(t, err)
			defer r.Close()
			got, err := io.ReadAll(r)
			require.NoError(t, err)
			assert.Equal(t, c.want, string(got))

			// 断言请求头确实按契约发出（wantRange 为空表示整文件请求，不发 Range 头）
			_, hdr := f.request()
			require.NotNil(t, hdr)
			assert.Equal(t, c.wantRange, hdr.Get("Range"))
		})
	}
}

// TestEchSource_OpenMetaSize 契约：meta.Size 只在「整文件 200」时可信（分片 206 的
// Content-Length 是分片长度，不是总长），其余填 -1 表示未知。
func TestEchSource_OpenMetaSize(t *testing.T) {
	body := "0123456789"
	hash := sha256Hex([]byte(body))
	ctx := context.Background()

	cases := []struct {
		name         string
		rangeCapable bool
		offset       int64
		size         int64
		wantSize     int64
	}{
		{name: "full 200 reports total", rangeCapable: true, offset: 0, size: -1, wantSize: int64(len(body))},
		{name: "206 slice reports unknown", rangeCapable: true, offset: 2, size: 3, wantSize: -1},
		{name: "200 full via non-range server", rangeCapable: false, offset: 0, size: -1, wantSize: int64(len(body))},
		{name: "200 full but range requested reports unknown", rangeCapable: false, offset: 2, size: 3, wantSize: -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRemote(t, fakeRemoteCfg{body: body, rangeCapable: c.rangeCapable})
			s := newEch(t, f, "/f/"+hash)
			r, fi, err := s.OpenMeta(ctx, hash, c.offset, c.size)
			require.NoError(t, err)
			defer r.Close()
			_, err = io.ReadAll(r)
			require.NoError(t, err)
			require.NotNil(t, fi)
			assert.Equal(t, hash, fi.Hash)
			assert.Equal(t, c.wantSize, fi.Size)
		})
	}
}

// TestEchSource_Verification 契约：整文件请求默认做 sha256 校验（内容寻址基线）——
// 远端内容可能被篡改或截断，不校验就会把错误内容当成「就是这个文件」；分片请求不校验
// （分片无法独立校验，由整文件路径兜底）。
func TestEchSource_Verification(t *testing.T) {
	good := "the truth"
	ctx := context.Background()

	t.Run("correct content passes", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: good})
		hash := sha256Hex([]byte(good))
		s := newEch(t, f, "/f/"+hash)
		got, err := s.Fetch(ctx, hash)
		require.NoError(t, err)
		assert.Equal(t, good, string(got))
	})

	t.Run("tampered content fails with label", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "TAMPERED"})
		hash := sha256Hex([]byte(good)) // 期望 hash 属于正确内容
		s := newEch(t, f, "/f/"+hash)
		got, err := s.Fetch(ctx, hash)
		require.Error(t, err, "tampered full content must fail verification")
		assert.Contains(t, err.Error(), "hash mismatch")
		assert.Contains(t, err.Error(), "ech")
		// 校验在 EOF 时才判定，已读到的字节会随错误一起返回——调用方必须只用无错误结果。
		assert.Equal(t, "TAMPERED", string(got))
	})

	t.Run("truncated content fails", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "the tru"})
		hash := sha256Hex([]byte(good))
		s := newEch(t, f, "/f/"+hash)
		_, err := s.Fetch(ctx, hash)
		require.Error(t, err, "truncated content must fail verification")
		assert.Contains(t, err.Error(), "hash mismatch")
	})

	t.Run("empty content verifies", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: ""})
		hash := sha256Hex([]byte{})
		s := newEch(t, f, "/f/"+hash)
		got, err := s.Fetch(ctx, hash)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("WithoutVerify disables the check", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "TAMPERED"})
		hash := sha256Hex([]byte("the truth"))
		s := NewEchSource("ech", staticResolver(f.url("/x")), WithEgress(f.egress()), WithoutVerify())
		got, err := s.Fetch(ctx, hash)
		require.NoError(t, err, "verify disabled: tampered content passes through")
		assert.Equal(t, "TAMPERED", string(got))
	})

	t.Run("range requests are not verified", func(t *testing.T) {
		// 分片请求即使 hash 不匹配也不会校验（分片无法独立校验）
		f := newFakeRemote(t, fakeRemoteCfg{body: "0123456789", rangeCapable: true})
		hash := sha256Hex([]byte("something-else"))
		s := newEch(t, f, "/f/"+hash)
		r, err := s.Open(ctx, hash, 2, 3)
		require.NoError(t, err)
		defer r.Close()
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		assert.Equal(t, "234", string(got))
	})
}

// TestEchSource_RequestCustomization 契约：User-Agent 与自定义头必须真的发到远端
// （iwara 的 X-Site/Referer 之类场景靠它）。
func TestEchSource_RequestCustomization(t *testing.T) {
	f := newFakeRemote(t, fakeRemoteCfg{body: "abc"})
	hash := sha256Hex([]byte("abc"))
	s := NewEchSource("ech", staticResolver(f.url("/x")),
		WithEgress(f.egress()), WithUserAgent("peerdrive/1.0"), WithHeader("X-Site", "iwara"))

	r, err := s.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	_, err = io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())

	_, hdr := f.request()
	assert.Equal(t, "peerdrive/1.0", hdr.Get("User-Agent"))
	assert.Equal(t, "iwara", hdr.Get("X-Site"))
}

// TestEchSource_ResolverErrors 契约：resolver 失败（ID 反查不到、上游 API 拒绝）必须
// 上抛带 hash 的错误——Manager 会把本源记为失败并继续降级下一个源。
func TestEchSource_ResolverErrors(t *testing.T) {
	f := newFakeRemote(t, fakeRemoteCfg{body: "abc"})
	r := &recResolver{url: f.url("/x"), err: fmt.Errorf("id not found")}
	hash := sha256Hex([]byte("abc"))
	s := NewEchSource("ech", r.resolve, WithEgress(f.egress()))

	_, err := s.Open(context.Background(), hash, 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id not found")
	assert.Contains(t, err.Error(), hash)
	assert.Equal(t, hash, r.gotHash(), "resolver must be called with the requested hash")
}

// TestEchSource_EgressErrors 契约：出口失败（网络不通、握手失败）原样上抛。
func TestEchSource_EgressErrors(t *testing.T) {
	hash := sha256Hex([]byte("abc"))
	s := NewEchSource("ech", staticResolver("https://unreachable.invalid/x"),
		WithEgress(egressFunc(func(context.Context, *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial timeout")
		})))

	_, err := s.Open(context.Background(), hash, 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dial timeout")
}

// TestEchSource_EgressCancelsWithContext 契约：出口必须尊重 ctx 取消。
func TestEchSource_EgressCancelsWithContext(t *testing.T) {
	hash := sha256Hex([]byte("abc"))
	s := NewEchSource("ech", staticResolver("https://unreachable.invalid/x"),
		WithEgress(egressFunc(func(ctx context.Context, _ *http.Request) (*http.Response, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.Open(ctx, hash, 0, -1)
	require.Error(t, err, "cancelled context must abort the fetch")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestEchSource_Availability 契约：Available 只判「配置完整性」，不做真实网络探测。
func TestEchSource_Availability(t *testing.T) {
	ctx := context.Background()

	f := newFakeRemote(t, fakeRemoteCfg{body: "x"})
	assert.True(t, newEch(t, f, "/x").Available(ctx))

	// 模板-only 源（无 resolver）也可用——resolveURL 会走模板分支
	templateOnly := NewEchSource("ech", nil, WithEgress(f.egress()))
	templateOnly.template = f.url("/ipfs/%s")
	assert.True(t, templateOnly.Available(ctx))

	// resolver 与模板都没有：不可用
	assert.False(t, NewEchSource("ech", nil, WithEgress(f.egress())).Available(ctx))

	// egress 为 nil：不可用（有 resolver 也发不出请求）
	s := &EchSource{name: "ech", resolver: staticResolver("https://x"), verify: true, rangeReq: true}
	assert.False(t, s.Available(ctx))

	// 零值 EchSource（未配置）：不可用
	assert.False(t, (&EchSource{}).Available(ctx))
}

// TestEchSource_Capabilities 契约：能力位声明必须与实际行为一致——路由完全信任声明，
// 声明了却做不到就会静默失败（Manager 按 CapStream 决定调 Open 还是 Fetch）。
func TestEchSource_Capabilities(t *testing.T) {
	t.Run("default: file + stream + verify", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "x"})
		s := newEch(t, f, "/x")
		assert.Equal(t, CapFile|CapStream|CapVerify, s.Capabilities())
		assert.True(t, IsStream(s))
		assert.True(t, IsFile(s))
		assert.True(t, IsVerify(s))
		assert.False(t, IsMeta(s))
	})

	t.Run("WithoutRange drops CapStream", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "x"})
		s := NewEchSource("ech", staticResolver(f.url("/x")), WithEgress(f.egress()), WithoutRange())
		assert.Equal(t, CapFile|CapVerify, s.Capabilities())
		assert.False(t, IsStream(s))
	})

	t.Run("WithoutVerify drops CapVerify", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "x"})
		s := NewEchSource("ech", staticResolver(f.url("/x")), WithEgress(f.egress()), WithoutVerify())
		assert.Equal(t, CapFile|CapStream, s.Capabilities())
	})

	t.Run("Info always nil (optional capability)", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: "x"})
		s := newEch(t, f, "/x")
		fi, err := s.Info(context.Background(), makeValidHash())
		require.NoError(t, err)
		assert.Nil(t, fi)
	})
}

// TestEchSource_ManagerRouting 契约：EchSource 通过 Manager 的路由语义（能力位、
// 统计、Available 跳过）与 mock 源完全一致——这是「新数据源接入参考基座」的验收点。
func TestEchSource_ManagerRouting(t *testing.T) {
	ctx := context.Background()

	t.Run("full fetch through manager records stats", func(t *testing.T) {
		body := "ech-body"
		f := newFakeRemote(t, fakeRemoteCfg{body: body})
		hash := sha256Hex([]byte(body))
		ech := newEch(t, f, "/f/"+hash)
		local := newBufferMock("local", []byte("local-data"))

		m := New()
		require.NoError(t, m.Register(local))
		require.NoError(t, m.Register(ech))

		r, err := m.OpenRange(ctx, hash, 0, -1)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, body, string(got))

		st := m.Snapshot()
		byName := map[string]SourceStatus{}
		for _, s := range st {
			byName[s.Name] = s
		}
		assert.Equal(t, "ech", byName["ech"].Type)
		assert.Equal(t, int(CapFile|CapStream|CapVerify), int(byName["ech"].Capabilities))
		assert.GreaterOrEqual(t, byName["ech"].Stats.Success, int64(1))
		assert.Equal(t, 0, local.openCount(), "local must miss this hash before ech is tried")
	})

	t.Run("resolver failure degrades to next source", func(t *testing.T) {
		hash := sha256Hex([]byte("abc"))
		f := newFakeRemote(t, fakeRemoteCfg{body: "abc"})
		r := &recResolver{url: f.url("/x"), err: fmt.Errorf("resolve failed")}
		ech := NewEchSource("ech", r.resolve, WithEgress(f.egress()))
		fallback := newBufferMock("fallback", []byte("abc"))

		m := New()
		require.NoError(t, m.Register(ech))
		require.NoError(t, m.Register(fallback))

		rr, err := m.OpenRange(ctx, hash, 0, -1)
		require.NoError(t, err)
		got, err := io.ReadAll(rr)
		require.NoError(t, err)
		require.NoError(t, rr.Close())
		assert.Equal(t, "abc", string(got))

		st := m.Snapshot()
		byName := map[string]SourceStatus{}
		for _, s := range st {
			byName[s.Name] = s
		}
		assert.GreaterOrEqual(t, byName["ech"].Stats.Fail, int64(1))
		assert.Contains(t, byName["ech"].Stats.LastErr, "resolve failed")
		assert.GreaterOrEqual(t, byName["fallback"].Stats.Success, int64(1))
	})

	t.Run("unavailable ech source is skipped", func(t *testing.T) {
		hash := sha256Hex([]byte("abc"))
		ech := NewEchSource("ech", nil) // resolver nil → Available==false
		fallback := newBufferMock("fallback", []byte("abc"))

		m := New()
		require.NoError(t, m.Register(ech))
		require.NoError(t, m.Register(fallback))

		rr, err := m.OpenRange(ctx, hash, 0, -1)
		require.NoError(t, err)
		got, err := io.ReadAll(rr)
		require.NoError(t, err)
		require.NoError(t, rr.Close())
		assert.Equal(t, "abc", string(got))

		st := m.Snapshot()
		byName := map[string]SourceStatus{}
		for _, s := range st {
			byName[s.Name] = s
		}
		assert.False(t, byName["ech"].Available)
		assert.Equal(t, int64(0), byName["ech"].Stats.Success)
		assert.GreaterOrEqual(t, byName["ech"].Stats.Fail, int64(1))
		assert.Contains(t, byName["ech"].Stats.LastErr, "unavailable")
	})

	t.Run("OpenRange skips CapFile-only source, OpenAny fetches it", func(t *testing.T) {
		// WithoutRange → 只声明 CapFile：OpenRange 必须跳过（不做整文件缓冲降级），
		// OpenAny 允许整文件取回。这是路由契约里「流式优先、整文件兜底」的边界。
		body := "whole"
		f := newFakeRemote(t, fakeRemoteCfg{body: body})
		hash := sha256Hex([]byte(body))
		fileOnly := NewEchSource("ech", staticResolver(f.url("/f/"+hash)),
			WithEgress(f.egress()), WithoutRange())
		assert.False(t, IsStream(fileOnly))

		m := New()
		require.NoError(t, m.Register(fileOnly))

		_, err := m.OpenRange(ctx, hash, 0, -1)
		require.Error(t, err, "CapFile-only source must be skipped by OpenRange")
		assert.Contains(t, err.Error(), "no stream source available")

		r, err := m.OpenAny(ctx, hash)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, body, string(got))
	})

	t.Run("OpenAny falls back to next source when fetch fails", func(t *testing.T) {
		body := "whole"
		hash := sha256Hex([]byte(body))
		ech := NewEchSource("ech", staticResolver("https://x"),
			WithEgress(egressFunc(func(context.Context, *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("stream down")
			})))
		mock := newBufferMock("mock", []byte(body))

		m := New()
		require.NoError(t, m.Register(ech))
		require.NoError(t, m.Register(mock))

		r, err := m.OpenAny(ctx, hash)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, body, string(got))
	})
}

// TestEchSource_Template 契约：EchSourceFromTemplate 用模板解析 URL，模板带 %d 说明端点
// 走 query 传分片参数 → 自动关闭 Range 头（声明 CapFile only）。
func TestEchSource_Template(t *testing.T) {
	ctx := context.Background()
	body := "0123456789"
	hash := sha256Hex([]byte(body))

	t.Run("hash-only template keeps Range header", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: body, rangeCapable: true})
		s := EchSourceFromTemplate("ech", f.url("/f/%s"), WithEgress(f.egress()))
		assert.True(t, IsStream(s))

		r, err := s.Open(ctx, hash, 2, 3)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, "234", string(got))

		url, hdr := f.request()
		assert.Equal(t, "/f/"+hash, url)
		assert.Equal(t, "bytes=2-4", hdr.Get("Range"))
	})

	t.Run("template with %d uses query and disables Range", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: body, rangeCapable: false})
		s := EchSourceFromTemplate("ech", f.url("/f/%s?off=%d&size=%d"), WithEgress(f.egress()))
		assert.False(t, IsStream(s))
		assert.True(t, IsFile(s))

		// 模板里的 %d 必须填**本次请求**的 offset/size（模板在每次请求时重新 Sprintf，
		// 不是解析期一次性绑定），同时不再发 Range 头——端点用自己的协议参数传分片。
		r, err := s.Open(ctx, hash, 4, 3)
		require.NoError(t, err)
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		assert.Equal(t, "456", string(got))

		url, hdr := f.request()
		assert.Contains(t, url, "/f/"+hash+"?off=4&size=3")
		assert.Empty(t, hdr.Get("Range"))
	})

	t.Run("hash is substituted into the URL", func(t *testing.T) {
		f := newFakeRemote(t, fakeRemoteCfg{body: body})
		s := EchSourceFromTemplate("ech", f.url("/ipfs/%s"), WithEgress(f.egress()))
		_, err := s.Open(ctx, hash, 0, -1)
		require.NoError(t, err)
		url, _ := f.request()
		assert.Equal(t, "/ipfs/"+hash, url)
	})
}

// TestEchSource_CapMetaNotDeclared 契约：EchSource 不声明 CapMeta（没有独立元数据通道），
// 但实现了 MetaSource（OpenMeta 在打开时附带 Size）。两个声明必须自洽。
func TestEchSource_CapMetaNotDeclared(t *testing.T) {
	f := newFakeRemote(t, fakeRemoteCfg{body: "hello"})
	s := newEch(t, f, "/x")
	assert.False(t, IsMeta(s), "Info is not implemented → must not declare CapMeta")
	_, ok := OpenMetaOf(s)
	assert.True(t, ok, "OpenMeta is implemented → OpenMetaOf must find it")
}

// TestEchSource_ConcurrentOpen 并发打开同一 hash（-race 下验证 Egress/verifyReadCloser
// 无数据竞争，且每个流的内容都完整）。
func TestEchSource_ConcurrentOpen(t *testing.T) {
	body := strings.Repeat("ech-race-", 256)
	hash := sha256Hex([]byte(body))
	f := newFakeRemote(t, fakeRemoteCfg{body: body, rangeCapable: true})
	s := newEch(t, f, "/f/"+hash)

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var off, size int64
			if i%2 == 0 {
				off, size = 0, -1
			} else {
				off, size = 10, 50
			}
			r, err := s.Open(ctx, hash, off, size)
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
				want = body
			} else {
				want = body[off : off+size]
			}
			if string(got) != want {
				errs <- fmt.Errorf("goroutine %d: unexpected content length %d, want %d", i, len(got), len(want))
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

// TestEchSource_CapabilitiesWithPriority 契约：SetPriority/Priority 可运行时调整。
func TestEchSource_CapabilitiesWithPriority(t *testing.T) {
	f := newFakeRemote(t, fakeRemoteCfg{body: "x"})
	s := newEch(t, f, "/x")
	assert.Equal(t, 0, s.Priority())
	s.SetPriority(7)
	assert.Equal(t, 7, s.Priority())
}
