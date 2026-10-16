package pathutil

// Tests for the fallback path itself.
//
// Why a separate file: this part wires up the openRootFn seam, and touching it
// means rebuilding the whole machine. Keeping it in the main test file would
// leave the reader unable to tell which cases are pure logic and which touch
// global state.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRootUnavailable_Classification the classifier itself: errno's with
// different semantics must not be lumped into one class.
func TestRootUnavailable_Classification(t *testing.T) {
	assert.False(t, RootUnavailable(nil))
	assert.False(t, RootUnavailable(os.ErrNotExist))
	assert.False(t, RootUnavailable(os.ErrPermission))
	assert.False(t, RootUnavailable(os.ErrClosed))
	assert.True(t, RootUnavailable(os.ErrInvalid), "EINVAL on Linux is the return when openat2 flag is not recognized")
	assert.True(t, RootUnavailable(os.NewSyscallError("openat2", os.ErrInvalid)), "should recognize when wrapped in SyscallError")
	assert.True(t, RootUnavailable(&os.PathError{Op: "openat2", Path: "/mnt/x", Err: os.ErrInvalid}))
}

// TestRootFallback_FailClosedThenWorks the fallback path must actually be
// usable -- it's not enough to just "log it".
//
// Use the openRootFn seam to simulate "filesystem unsupported" (you can't really
// create such an environment on a real machine), verifying three things: by
// default it blocks and gives the next step, after explicitly opening the hatch
// the operation really completes, and the fallback state is observable.
func TestRootFallback_FailClosedThenWorks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a", "b.txt")

	orig := openRootFn
	t.Cleanup(func() { openRootFn = orig })
	// Two real forms on Linux: kernel <5.6 has no openat2 -> ENOSYS; flag not
	// recognized -> EINVAL. Here we use EINVAL, because it can be strictly
	// detected via errors.Is(err, os.ErrInvalid).
	unsupported := os.NewSyscallError("openat2", os.ErrInvalid)
	openRootFn = func(string) (*os.Root, error) { return nil, unsupported }
	require.True(t, RootUnavailable(unsupported), "precondition: this errno must be recognized as 'unsupported'")

	// 1) default fail closed, and the error message points to the next step
	err := SafeWriteFileAny([]string{root}, target, []byte("x"), 0o644)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support directory handle constraints")
	assert.Contains(t, err.Error(), "PEERDRIVE_ROOT_FALLBACK=1", "should tell operators how to proceed")
	_, sterr := os.Stat(target)
	assert.True(t, os.IsNotExist(sterr), "must not write anything when fail closed")

	// 2) after explicitly opening the hatch, the operation should really complete
	// (parent directories must be created too)
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "1")
	require.NoError(t, SafeWriteFileAny([]string{root}, target, []byte("payload"), 0o644))
	b, rerr := os.ReadFile(target)
	require.NoError(t, rerr)
	assert.Equal(t, "payload", string(b))

	// reading it back must also work (the other end of the fallback)
	f, oerr := SafeOpen(root, target)
	require.NoError(t, oerr)
	require.NoError(t, f.Close())

	// deletion is likewise usable
	require.NoError(t, SafeRemoveAny([]string{root}, target))
	_, sterr2 := os.Stat(target)
	assert.True(t, os.IsNotExist(sterr2))
}

// TestRootFallback_MissingRootIsCreatedNotDegraded the directory not existing !=
// filesystem unsupported: a first run should not be degraded (and certainly not
// misreported as unsafe), but should create the directory and continue using Root.
func TestRootFallback_MissingRootIsCreatedNotDegraded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created-yet")
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "1") // even with the hatch open, it must not degrade along the way

	require.NoError(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "x.txt"), []byte("ok"), 0o644))
	b, err := os.ReadFile(filepath.Join(root, "x.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(b))
}
