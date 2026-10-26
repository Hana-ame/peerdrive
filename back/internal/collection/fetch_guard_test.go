package collection

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// disableSSRFGuard 关掉 SSRF 闸门，用完自动恢复默认（开着）。
//
// httptest.NewServer 监听在 127.0.0.1，正是闸门要拦的目标。那些只想验证
// 取文件降级顺序 / 备选优先级、不关心 SSRF 的用例必须显式关掉闸门——
// 这样「默认是安全的」才不会被测试代码隐式改掉。
func disableSSRFGuard(t *testing.T) {
	t.Helper()
	SetGuardCollectionFetch(false)
	t.Cleanup(func() { SetGuardCollectionFetch(true) })
}

// 发现背景（Issue #378 收尾，回查 PR #419 第 12 项时发现）：collection 条目里的
// Source.URL / ECHURL / Private.URL 是对端可控的——匿名合集可由任何有权限的用户
// 创建并同步过来，而 validateSourceURL 只校验 scheme+host，https://127.0.0.1:PORT/
// 和 https://169.254.169.254/ 都能过。fetchHTTP 原来直接把 URL 丢给
// http.DefaultClient（既无目标 IP 校验也无超时），本节点就替对端发了一次内网请求。
// 本用例把闸门行为钉死：开着时拒绝回环地址，关掉时保持旧行为。
func TestFetchHTTP_GuardBlocksLoopbackURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("reachable"))
	}))
	t.Cleanup(srv.Close)

	// 默认姿态必须是开着（安全默认）——这是本用例的前置断言，不是摆设。
	require.True(t, GuardCollectionFetchEnabled())

	// httptest 监听在 127.0.0.1，正是闸门该拦的目标。
	_, _, err := fetchHTTP(context.Background(), FetchOptions{}.client(), srv.URL)
	require.Error(t, err, "loopback URL must be rejected while the guard is on")
	require.Contains(t, err.Error(), "ssrf guard")

	// 关掉闸门后同一个 URL 可达，证明拦截来自闸门本身而不是网络。
	disableSSRFGuard(t)
	rc, n, err := fetchHTTP(context.Background(), FetchOptions{}.client(), srv.URL)
	require.NoError(t, err, "guard off must restore the legacy fetch path")
	require.Equal(t, int64(9), n)
	t.Cleanup(func() { _ = rc.Close() })
}

// 网关开着时，注入的 client 不会被换掉（调用方显式指定 Transport 的场景必须尊重），
// 但 URL 闸门仍然生效——两层防护独立于 client 注入。
func TestFetchHTTP_GuardAppliesWithInjectedClient(t *testing.T) {
	restore := func() { SetGuardCollectionFetch(true) }
	t.Cleanup(restore)
	SetGuardCollectionFetch(true)

	// http.Client 指向 127.0.0.1 的注入 client：闸门在 dial 之前就拦住，
	// 因此这个 client 一次都不会被调用。
	called := false
	injected := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	_, _, err := fetchHTTP(context.Background(), injected, "http://127.0.0.1:9/nothing")
	require.Error(t, err)
	require.False(t, called, "URL guard must reject before the injected client is used")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// 合法公网形态的 URL 不应被闸门误伤（scheme+host 合法即可过 URL 层；实际连接失败
// 是网络问题，不是闸门问题）。
func TestFetchHTTP_GuardAllowsPublicShapedURL(t *testing.T) {
	restore := func() { SetGuardCollectionFetch(true) }
	t.Cleanup(restore)
	SetGuardCollectionFetch(true)

	called := false
	injected := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, context.Canceled
	})}
	_, _, err := fetchHTTP(context.Background(), injected, "https://example.com/file.bin")
	require.True(t, called, "public-shaped URL must reach the client")
	require.Error(t, err) // 注入的 transport 故意返回错误，仅用于证明调用发生
	require.False(t, strings.Contains(err.Error(), "ssrf guard"), "no false positive from the guard")
}
