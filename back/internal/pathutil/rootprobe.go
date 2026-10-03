package pathutil

// What to do when os.Root can't be opened.
//
// Status quo (pre-2026-09-20 approach): when OpenRoot fails, just return an error. The symptom is
// "that directory can't be shared", and the log only says "open root ...: <errno>". Operators can't tell
// whether it's filesystem unsupported, directory missing, or permission denied -- so they either think
// the product is broken or go chmod/chown a bunch of stuff for nothing.
//
// This does two things:
//  1. Classification + actionable explanation: unsupported vs missing vs permission, each with next steps;
//  2. An **explicit**, off-by-default escape hatch PEERDRIVE_ROOT_FALLBACK=1: when you're on an unsupported
//     filesystem and must use the directory, the operator can choose to fall back to "path-based checking
//     + path-based opening" and continue receiving warnings in the log. Off by default -- less is more;
//     don't open a TOCTOU window by default.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"peerdrive/internal/log"
)

// openRootOrFallback opens the allowed root; if os.Root is unavailable on the filesystem, the escape
// hatch decides whether to fall back to "path-based operations". A nil *os.Root return means **degraded**
// (use base + rel to construct the full path for the old os.* functions).
func openRootOrFallback(root string) (*os.Root, error) {
	var (
		dir *os.Root
		err error
	)
	// Root doesn't exist yet (first run, storageDir / uploadDir both missing): creating the directory
	// is the expected behavior -- the boundary is "can't go beyond this root", not "this root must exist first".
	if root != "" {
		if dir, err = openRootFn(root); err != nil && errors.Is(err, os.ErrNotExist) {
			if mkErr := os.MkdirAll(root, 0o755); mkErr == nil {
				dir, err = openRootFn(root)
			}
		}
	}
	if err == nil {
		return dir, nil
	}
	if RootUnavailable(err) && RootFallbackEnabled() {
		if WarnOnce("root-fallback:" + root) {
			log.LogWarn("pathutil: %s", RootFallbackWhy)
			log.LogWarn("pathutil: degraded dir=%s underlying cause=%v", root, err)
		}
		// In degraded mode there's no Root fallback, so the directory must be created first: subsequent writes use it as parent
		_ = os.MkdirAll(root, 0o755)
		return nil, nil
	}
	return nil, fmt.Errorf("%s: %w", ExplainRootFailure(root, err), err)
}

// WarnOnce returns true if this key is being seen for the first time (should log); repeated calls don't bother anyone.
//
// Safety-degradation/unsupported messages flooding the log will drown the real problem, and
// every request shouldn't pay for a string format call either.
func WarnOnce(key string) bool {
	warnMu.Lock()
	defer warnMu.Unlock()
	if warned[key] {
		return false
	}
	warned[key] = true
	warnLines = append(warnLines, key)
	return true
}

// RootFallbackEnabled returns whether the operator explicitly requested "fall back to path-based ops when Root is unavailable".
//
// Why it exists: some filesystems (certain network filesystems, 9P, old overlay/SMB mounts,
// WSL DrvFs on certain kernels) can't do the "open a directory as root then tree-scoped resolution" thing;
// in that case fail-closed means "this directory can't be shared at all" and the product is unusable.
// Giving a switch to someone who **knows what they're doing** is better than having them guess in the dark.
func RootFallbackEnabled() bool {
	return os.Getenv("PEERDRIVE_ROOT_FALLBACK") == "1"
}

// RootUnavailable returns whether this error is "platform/filesystem can't do this" (as opposed to
// "directory doesn't exist" or "permission denied"). Distinguishing matters: the troubleshooting direction
// is completely different.
func RootUnavailable(err error) bool {
	if err == nil {
		return false
	}
	// os.Root wraps errno as *PathError internally, and errors.Is can see through it
	for _, target := range []error{os.ErrInvalid, errNotSupported} {
		if errors.Is(err, target) {
			return true
		}
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return RootUnavailable(pe.Err)
	}
	// When openat2 is unrecognized the kernel returns EINVAL; if not implemented at all it's ENOSYS/ENOTSUP.
	// Also recognizes Windows ERROR_NOT_SUPPORTED (syscall errno 1150).
	msg := err.Error()
	return strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "operation not supported") ||
		strings.Contains(msg, "not implemented")
}

// errNotSupported uses a comparable sentinel for syscall.Errno checks outside of RootUnavailable,
// to avoid scattering magic numbers.
var errNotSupported = errors.New("root operations unsupported")

// ExplainRootFailure translates an OpenRoot failure into human-readable text + next steps.
func ExplainRootFailure(root string, err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("directory %s does not exist: create it (or fix the config) and restart", root)
	case errors.Is(err, os.ErrPermission):
		return fmt.Sprintf("process lacks permission to access %s: check directory owner/ACL, don't just chmod 777", root)
	case RootUnavailable(err):
		extra := ""
		if !RootFallbackEnabled() {
			extra = "; if you confirm this directory should be used on such a filesystem, set PEERDRIVE_ROOT_FALLBACK=1 explicitly (falls back to path-based checking, has TOCTOU risk)"
		}
		return fmt.Sprintf("filesystem does not support directory handle constraints (os.Root), safety boundary cannot be enforced on this directory%v", extra)
	default:
		return fmt.Sprintf("%v", err)
	}
}

// ProbeRootSupport tries to create an os.Root on a directory; returns nil if it works.
//
// When to use: **startup self-check**. Don't wait until someone tries to fetch a file to discover
// the directory can't be served.
func ProbeRootSupport(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("empty root")
	}
	r, err := openRootFn(root)
	if err != nil {
		return err
	}
	defer r.Close()
	// Opening successfully is not enough: on some mounts openat2 works but subsequent *_at calls fail.
	if _, err := r.Stat("."); err != nil {
		return err
	}
	return nil
}

// warnOnce: warn only once per directory: safety-degradation/unsupported messages flooding the log
// will drown the real problem, and every request shouldn't pay for a string format call.
var (
	warnMu    sync.Mutex
	warned    = map[string]bool{}
	warnLines = []string{}
)

// openRootFn: replaceable seam for unit tests.
//
// Why it's needed: whether os.Root works depends on the **filesystem of the running machine**, and
// on a well-supported machine you can't create an "unsupported" environment -- so the degradation
// branch would never be executed, and we shouldn't just hope it "looks like it should be fine".
var openRootFn = os.OpenRoot

// RootFallbackWhy: explains why the degraded path was taken (the log/error message should make clear:
// this is an intentional degradation, not "we didn't implement defenses").
const RootFallbackWhy = "PEERDRIVE_ROOT_FALLBACK=1: this filesystem cannot use os.Root; fell back to path-based checking (has TOCTOU window, only for scenarios where the directory's users are trusted)"
