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

import "testing"

func TestRunShutsDownOnSIGTERM(t *testing.T) {
	t.Skip("self-signalling needs POSIX signal delivery; Windows has no syscall.Kill and signal.Notify only catches console Ctrl+C/Ctrl+Break events (see server_signal_test.go)")
}

func TestRunShutsDownOnSIGINT(t *testing.T) {
	t.Skip("self-signalling needs POSIX signal delivery; Windows has no syscall.Kill and signal.Notify only catches console Ctrl+C/Ctrl+Break events (see server_signal_test.go)")
}
