//go:build windows

package btloader

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// dllFileName is the runtime library shipped next to the executable.
const dllFileName = "peerdrive_bt.dll"

// dllEnvVar overrides the library location (absolute path).
const dllEnvVar = "PEERDRIVE_BT_DLL"

// maxReplyBytes bounds how far readCString will scan for the terminator. The
// largest legitimate reply is torrent_bytes on a 16 MiB .torrent (~22 MB as
// base64); 64 MiB leaves room while still making a corrupted pointer fail fast
// instead of walking off into unmapped memory.
const maxReplyBytes = 64 << 20

// Available reports whether a runtime library can be found. It does NOT load it:
// loading maps a second Go runtime into the process, which must only happen when
// BT is actually enabled.
func Available() bool {
	_, err := resolveDLLPath()
	return err == nil
}

// dllTransport drives the runtime through its C ABI.
type dllTransport struct {
	dll       *syscall.LazyDLL
	procOpen  *syscall.LazyProc
	procCall  *syscall.LazyProc
	procFree  *syscall.LazyProc
	procClose *syscall.LazyProc
	procErr   *syscall.LazyProc
	procVer   *syscall.LazyProc

	handle int32
	mu     sync.RWMutex
	path   string
}

// openTransport loads the DLL and opens one runtime handle.
func openTransport(cfg wireConfig) (transport, error) {
	path, err := resolveDLLPath()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	dll := syscall.NewLazyDLL(path)
	if err := dll.Load(); err != nil {
		// A wrong-architecture DLL (386 vs amd64) fails here with
		// ERROR_BAD_EXE_FORMAT; surface the path so the fix is obvious.
		return nil, fmt.Errorf("%w: load %s: %v", ErrUnavailable, path, err)
	}

	t := &dllTransport{
		dll:       dll,
		procOpen:  dll.NewProc("pd_bt_open"),
		procCall:  dll.NewProc("pd_bt_call"),
		procFree:  dll.NewProc("pd_bt_free"),
		procClose: dll.NewProc("pd_bt_close"),
		procErr:   dll.NewProc("pd_bt_last_error"),
		procVer:   dll.NewProc("pd_bt_version"),
		path:      path,
	}

	// Resolve every export up front: a stale DLL that predates an export would
	// otherwise fail at the first use, deep inside a request.
	for name, proc := range map[string]*syscall.LazyProc{
		"pd_bt_open": t.procOpen, "pd_bt_call": t.procCall, "pd_bt_free": t.procFree,
		"pd_bt_close": t.procClose, "pd_bt_last_error": t.procErr, "pd_bt_version": t.procVer,
	} {
		if err := proc.Find(); err != nil {
			return nil, fmt.Errorf("%w: %s is missing export %s (stale DLL?): %v", ErrUnavailable, path, name, err)
		}
	}

	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode bt runtime config: %w", err)
	}
	cfgPtr, err := syscall.BytePtrFromString(string(cfgJSON))
	if err != nil {
		return nil, err
	}

	r, _, callErr := t.procOpen.Call(uintptr(unsafe.Pointer(cfgPtr)))
	if r == 0 {
		return nil, fmt.Errorf("%w: pd_bt_open failed: %s", ErrUnavailable, t.lastError(callErr))
	}
	t.handle = int32(r)

	return t, nil
}

// Describe identifies the loaded runtime for logs.
func (t *dllTransport) Describe() string {
	if t == nil {
		return "bt runtime"
	}
	ver := "unknown"
	if r, _, err := t.procVer.Call(); r != 0 {
		ver = readCString(r)
		_, _, _ = t.procFree.Call(r)
	} else {
		ver = t.lastError(err)
	}
	return fmt.Sprintf("%s [%s]", filepath.Base(t.path), ver)
}

// Call executes one op.
func (t *dllTransport) Call(op string, args any) (json.RawMessage, error) {
	if t == nil {
		return nil, ErrUnavailable
	}

	argsJSON := ""
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("%s: encode args: %w", op, err)
		}
		argsJSON = string(raw)
	}

	opPtr, err := syscall.BytePtrFromString(op)
	if err != nil {
		return nil, err
	}
	var argsPtr *byte
	if argsJSON != "" {
		if argsPtr, err = syscall.BytePtrFromString(argsJSON); err != nil {
			return nil, err
		}
	}

	// RWMutex, not Mutex: the runtime is thread-safe on its side (client mutexes),
	// and one slow op must not stall the others — a bridge_fetch can block for up
	// to 90s, and holding an exclusive lock would freeze /bt/downloads behind it.
	// The lock only guarantees the handle is not cleared by a concurrent Close
	// while a call is in flight.
	t.mu.RLock()
	h := t.handle
	if h == 0 {
		t.mu.RUnlock()
		return nil, ErrUnavailable
	}
	r, _, callErr := t.procCall.Call(
		uintptr(h),
		uintptr(unsafe.Pointer(opPtr)),
		uintptr(unsafe.Pointer(argsPtr)),
	)
	t.mu.RUnlock()

	if r == 0 {
		return nil, fmt.Errorf("%s: runtime call failed: %s", op, t.lastError(callErr))
	}
	raw := readCString(r)
	if debugABI() {
		fmt.Fprintf(os.Stderr, "[btabi] op=%s handle=%d ptr=%#x len=%d head=% x op_ptr=%#x\n",
			op, h, r, len(raw), headBytes(raw, 24), uintptr(unsafe.Pointer(opPtr)))
	}
	_, _, _ = t.procFree.Call(r)

	var env envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		// A malformed reply means the two sides disagree about the ABI (stale DLL,
		// wrong architecture, corrupted pointer). Printing the size and a hex
		// prefix turns a mystery into a diagnosis, and stays out of the way on the
		// happy path.
		return nil, fmt.Errorf("%s: malformed runtime reply (%d bytes, prefix % x): %w",
			op, len(raw), headBytes(raw, 16), err)
	}
	if !env.OK {
		if env.Error == "" {
			env.Error = "runtime returned an error with no message"
		}
		return nil, errors.New(env.Error)
	}
	return env.Data, nil
}

// Close stops the runtime. The DLL itself stays mapped: unloading a Go c-shared
// library would leave its runtime threads (netpoller, timers) pointing into
// unmapped memory, and the process is going away anyway.
func (t *dllTransport) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	h := t.handle
	t.handle = 0
	t.mu.Unlock()
	if h != 0 {
		_, _, _ = t.procClose.Call(uintptr(h))
	}
	return nil
}

// lastError reads pd_bt_last_error, falling back to the Win32 error.
func (t *dllTransport) lastError(fallback error) string {
	if t != nil && t.procErr != nil {
		if r, _, err := t.procErr.Call(); r != 0 {
			msg := readCString(r)
			_, _, _ = t.procFree.Call(r)
			if msg != "" {
				return msg
			}
		} else if err != nil && err != syscall.Errno(0) {
			return err.Error()
		}
	}
	if fallback != nil && fallback != syscall.Errno(0) {
		return fallback.Error()
	}
	return "unknown error"
}

// readCString converts a NUL-terminated C string produced by the runtime.
//
// Deliberately not C.GoString: the host is built with CGO_ENABLED=0 (see
// Makefile build-win), so there is no cgo helper to call — this is plain
// syscall + unsafe.
//
// strings.Clone is mandatory, not defensive: unsafe.String produces a string
// header that POINTS AT the runtime's malloc buffer, and the caller frees that
// buffer as soon as this returns. Without the clone the string would silently
// alias freed memory — observed in practice as a correct-looking reply that
// turned into heap garbage one allocation later.
//
// `GOOS=windows go vet` reports "possible misuse of unsafe.Pointer" on the two
// conversions below. That is the expected false positive for a pointer received
// across a C ABI: it is not derived from a Go pointer, it stays valid until
// pd_bt_free, and nothing here hands it back to the GC. Host-OS vet (what CI
// runs) never compiles this file.
func readCString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for n < maxReplyBytes {
		if *(*byte)(unsafe.Pointer(p + uintptr(n))) == 0 {
			break
		}
		n++
	}
	if n == 0 {
		return ""
	}
	return strings.Clone(unsafe.String((*byte)(unsafe.Pointer(p)), n))
}

// headBytes returns at most n leading bytes, for error messages only.
func headBytes(s string, n int) []byte {
	if len(s) > n {
		return []byte(s[:n])
	}
	return []byte(s)
}

// debugABI reports whether the ABI tracing line should be printed.
// Opt-in only: this is how you find out why a reply looks wrong on a machine you
// cannot attach a debugger to.
func debugABI() bool {
	v := os.Getenv("PEERDRIVE_BT_ABI_DEBUG")
	return v != "" && v != "0"
}

// resolveDLLPath finds the runtime library.
//
// Order: PEERDRIVE_BT_DLL, then next to the executable, then <exe>/dist. The
// current working directory is deliberately NOT searched: dropping a malicious
// peerdrive_bt.dll in the directory the user happens to launch from is the
// textbook DLL-planting vector, and a portable install already satisfies the
// exe-adjacent rule.
func resolveDLLPath() (string, error) {
	if p := os.Getenv(dllEnvVar); p != "" {
		st, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("%s=%s: %w", dllEnvVar, p, err)
		}
		if st.IsDir() {
			return "", fmt.Errorf("%s=%s is a directory", dllEnvVar, p)
		}
		return p, nil
	}

	var tried []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, dllFileName),
			filepath.Join(dir, "dist", dllFileName),
		} {
			tried = append(tried, cand)
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand, nil
			}
		}
	}

	return "", fmt.Errorf("cannot find %s (searched %v); set %s to its full path", dllFileName, tried, dllEnvVar)
}
