//go:build !windows

package btloader

import (
	"fmt"
	"runtime"
)

// Available always reports false off Windows: the runtime is only shipped as a
// Windows DLL. Go's c-shared form on Linux/macOS is a .so/.dylib, and dlopen-ing
// a second Go runtime into a Go process is not a supported configuration — the
// two runtimes fight over signal handlers and TLS.
func Available() bool { return false }

// openTransport always fails off Windows, with the reason.
func openTransport(cfg wireConfig) (transport, error) {
	_ = cfg // nothing to configure: there is no runtime to start
	return nil, fmt.Errorf("%w: the BT runtime is only built as a Windows DLL (peerdrive_bt.dll); this is %s",
		ErrUnavailable, runtime.GOOS)
}
