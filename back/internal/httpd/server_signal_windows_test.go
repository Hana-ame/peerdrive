//go:build windows

// package httpd — Windows twin of server_signal_test.go.
//
// The real tests signal the test process itself, which needs POSIX signal
// delivery. On Windows syscall.Kill does not exist at all, and signal.Notify only
// catches console Ctrl+C/Ctrl+Break events, so a self-sent SIGTERM would never
// reach the handler the test is trying to exercise. Skipping is the honest option
// here; the alternative would be to drop the assertion rather than to skip it,
// which would be worse.
//
// The cost of the skip: httpd.Run is not exercised by any test on Windows. The
// signal plumbing it owns is small and GOOS-uniform (signal.Notify on a channel
// plus a select on that channel and ctx.Done), and the neighbouring Serve/Shutdown
// code paths Run composes are still covered by the tests that do run on Windows.
//
// Discovery background: go-build.yml's windows-latest cell failed to compile this
// package (server_test.go:328: undefined: syscall.Kill) while the other four
// platforms passed, so the httpd split itself is Windows-safe — only the signal
// test was not.

package httpd

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunShutsDownOnContextCancel verifies that httpd.Run executes and shuts down gracefully
// via context cancellation on Windows.
//
// 发现背景：Issue #100。虽然 Windows 缺乏 POSIX syscall.Kill 无法在单进程单元测试中
// 自发信号触发 SIGTERM/SIGINT，但 httpd.Run 内部对 ctx.Done() 的监听和退出路径在 Windows
// 生产环境中是完全真实生效的。本测试直接验证 httpd.Run 在 Windows 上的启动、请求服务以及
// 上下文退出与端口回收。
func TestRunShutsDownOnContextCancel(t *testing.T) {
	srv, err := New(Config{Addr: "127.0.0.1:0"}, echoHandler())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx)
	}()

	addr := waitAddr(t, srv)

	// Verify server responds
	resp, err := http.Get("http://" + addr + "/echo?msg=windows-ok")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Cancel context and verify Run returns without error
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("httpd.Run did not exit within timeout after context cancellation on Windows")
	}
}

func TestRunShutsDownOnSIGTERM(t *testing.T) {
	t.Skip("self-signalling needs POSIX signal delivery; Windows has no syscall.Kill and signal.Notify only catches console Ctrl+C/Ctrl+Break events (see server_signal_test.go)")
}

func TestRunShutsDownOnSIGINT(t *testing.T) {
	t.Skip("self-signalling needs POSIX signal delivery; Windows has no syscall.Kill and signal.Notify only catches console Ctrl+C/Ctrl+Break events (see server_signal_test.go)")
}
