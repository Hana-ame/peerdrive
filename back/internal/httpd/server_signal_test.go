//go:build !windows

// package httpd — signal-driven shutdown tests for httpd.Run.
//
// These tests signal the test process itself, so they need real POSIX signal
// delivery. Windows has neither: syscall.Kill does not exist in the Windows
// syscall package, and signal.Notify there only catches console Ctrl+C/Ctrl+Break
// events, so a self-sent SIGTERM would never reach the handler. The Windows
// twin lives in server_signal_windows_test.go and skips with the same reason.
// Discovery background: go-build.yml's windows-latest cell failed to compile
// this package (server_test.go:328: undefined: syscall.Kill); the rest of the
// matrix passed, so the httpd split itself is Windows-safe — only the test was
// not.

package httpd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunShutsDownOnSIGTERM 收到 SIGTERM 必须优雅退出且 listener 关闭。
//
// 发现背景：Run 是 serverapp.RunHTTP 抽出来的信号处理部分，四个子命令里只有
// `peerdrive serve` 走它。抽走之后最容易出的问题是 signal.Notify 忘了 defer
// Stop，或 cancel 没接上，表现为 Ctrl-C 之后进程不退。
func TestRunShutsDownOnSIGTERM(t *testing.T) {
	runShutdownOnSignal(t, syscall.SIGTERM)
}

// TestRunShutsDownOnSIGINT SIGINT 与 SIGTERM 两条退出路径必须等价。
//
// 发现背景：Ctrl-C 发 SIGINT，systemd/k8s 发 SIGTERM；只测其中一条，
// 另一条的优雅退出会静默回归（直接退，进行中的传输被截断）。
func TestRunShutsDownOnSIGINT(t *testing.T) {
	// Guard: with a handler registered, a self-sent signal cannot kill the
	// test process even if timing goes wrong.
	runShutdownOnSignal(t, syscall.SIGINT)
}

func runShutdownOnSignal(t *testing.T, sig syscall.Signal) {
	t.Helper()

	guard := make(chan os.Signal, 1)
	signal.Notify(guard, sig)
	t.Cleanup(func() { signal.Stop(guard) })

	srv, err := New(Config{Addr: "127.0.0.1:0"}, echoHandler())
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background()) }()

	addr := waitAddr(t, srv)
	assertResponse(t, addr, "http", "ok")

	require.NoError(t, syscall.Kill(os.Getpid(), sig), "failed to signal the test process")

	select {
	case err := <-done:
		assert.NoError(t, err, "signal-driven shutdown is clean and must not surface an error")
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the shutdown signal")
	}
	assert.Error(t, dial(addr), "listener must be closed after signal shutdown")
}
