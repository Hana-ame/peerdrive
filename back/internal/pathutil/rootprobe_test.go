package pathutil

// Handling when os.Root establishment fails (doc/NETDISK.md §11.5 item 4).
//
// What we guarantee is not "it will never fail", but three things:
//  1. Failure **can be diagnosed** (unsupported vs nonexistent vs no permission)
//     -- getting the direction wrong wastes half a day of debugging;
//  2. Default fail closed, don't quietly allow it through;
//  3. When an operator explicitly opens the hatch, the fallback is a clear,
//     observable, and actually usable path.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeRootSupport_NormalDir(t *testing.T) {
	assert.NoError(t, ProbeRootSupport(t.TempDir()), "ordinary directory must support Root")
}

func TestProbeRootSupport_MissingDirIsNotUnsupported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	err := ProbeRootSupport(missing)
	require.Error(t, err)
	assert.False(t, RootUnavailable(err), "directory doesn't exist != filesystem not supported: diagnosis direction must not point wrong")
	assert.Contains(t, ExplainRootFailure(missing, err), "not exist")
}

func TestProbeRootSupport_FileNotRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	err := ProbeRootSupport(f)
	require.Error(t, err, "configuring an ordinary file as root directory is a config error, must have feedback")
	assert.False(t, RootUnavailable(err), "this is a config error, not filesystem not supported")
}

func TestExplainRootFailure_Permission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root user can't see permission denied")
	}
	if runtime.GOOS == "windows" {
		// On Windows, os.Chmod only flips the FILE_ATTRIBUTE_READONLY bit, and
		// the owner can still open this directory -- EACCES can't be produced
		// (you'd really have to change the ACL to do that). So this specific
		// branch isn't verified on Windows -- the Windows-side "must not be
		// misjudged as unsupported" is covered by the TestProbeRootSupport_*
		// negative test cases.
		t.Skip("Windows chmod can't produce EACCES")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := ProbeRootSupport(dir)
	require.Error(t, err)
	assert.Contains(t, ExplainRootFailure(dir, err), "permission", "permission errors should point to ACL/owner, not a generic error")
}

// TestRootFallback_OffByDefault default must fail closed: that is the meaning of
// this defense's existence. Only degrade after opening the hatch, and the hatch
// is an explicit environment variable, not "an internal quiet judgment".
func TestRootFallback_OffByDefault(t *testing.T) {
	assert.False(t, RootFallbackEnabled(), "default must not degrade")
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "1")
	assert.True(t, RootFallbackEnabled())
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "")
	assert.False(t, RootFallbackEnabled())
}
