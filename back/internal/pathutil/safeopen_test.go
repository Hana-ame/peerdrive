package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeOpen_Inside(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "sub", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("hello"), 0o644))

	f, err := SafeOpen(root, p)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 8)
	n, err := f.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(buf[:n]))
}

func TestSafeOpen_RejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))

	for name, p := range map[string]string{
		"upward escape":       filepath.Join(root, "..", filepath.Base(outside), "secret.txt"),
		"outside absolute path": secret,
		"system file":        "/etc/passwd",
		"NUL":                filepath.Join(root, "a.txt") + "\x00",
		"empty path":         "",
		"empty root":         "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SafeOpen(root, p)
			assert.Error(t, err, "SafeOpen must reject: %q", p)
		})
	}

	t.Run("empty root", func(t *testing.T) {
		_, err := SafeOpen("", filepath.Join(root, "a.txt"))
		assert.Error(t, err, "empty root must be rejected")
	})
}

// TestSafeOpen_AllowsInnerAbsoluteSymlink This is one of the reasons SafeOpen exists.
//
// Using os.Root.Open directly would also reject "symlinks inside the root whose
// target is an absolute path" (Go cannot validate an absolute target without
// escaping), which would break the legitimate use case of "organizing a media
// library with absolute symlinks inside a shared directory".
// SafeOpen first normalizes the path into one without symlinks before handing it
// to os.Root, so this use case continues to work as expected.
func TestSafeOpen_AllowsInnerAbsoluteSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real.txt")
	require.NoError(t, os.WriteFile(real, []byte("REAL"), 0o644))

	abs := filepath.Join(root, "abs.txt")
	require.NoError(t, os.Symlink(real, abs))
	rel := filepath.Join(root, "rel.txt")
	require.NoError(t, os.Symlink("real.txt", rel))

	for _, p := range []string{abs, rel, real} {
		f, err := SafeOpen(root, p)
		require.NoError(t, err, "inner symlink must be openable: %s", p)
		buf := make([]byte, 8)
		n, _ := f.Read(buf)
		f.Close()
		assert.Equal(t, "REAL", string(buf[:n]), "opened but not the same file: %s", p)
	}
}

// TestSafeOpen_ToctouSwap The value of this test case is to **first prove the
// window really exists**.
//
// Attack pattern: it is not about giving a directly unauthorized path (that kind
// would be blocked by IsPathAllowed), but first giving a legitimate path that
// passes validation, and then between "validation and open" replacing some
// **directory component** in the path with a symlink. The old two-step
// approach (Within → os.Open) would be vulnerable.
func TestSafeOpen_ToctouSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "a.txt"), []byte("top secret"), 0o600))

	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "a.txt"), []byte("benign"), 0o644))
	target := filepath.Join(sub, "a.txt")

	// 1) Validation phase: path is fully legitimate
	require.True(t, Within(root, target), "precondition: this path was legitimate before the swap")

	// 2) Attacker replaces sub with a symlink pointing outside after validation
	require.NoError(t, os.RemoveAll(sub))
	require.NoError(t, os.Symlink(outside, sub))

	// 3) Prove the window really exists: old os.Open would read the external file
	if f, err := os.Open(target); err == nil {
		buf := make([]byte, 32)
		n, _ := f.Read(buf)
		f.Close()
		require.Equal(t, "top secret", string(buf[:n]),
			"precondition: os.Open does read the external file (this is the TOCTOU window)")
	}

	// 4) SafeOpen must reject: the kernel re-evaluates at the moment of open
	_, err := SafeOpen(root, target)
	assert.Error(t, err, "SafeOpen must reject after components are swapped to symlinks (os.Open would be vulnerable)")
}

func TestSafeOpenAny(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	outside := t.TempDir()

	pb := filepath.Join(b, "x.txt")
	require.NoError(t, os.WriteFile(pb, []byte("B"), 0o644))
	po := filepath.Join(outside, "y.txt")
	require.NoError(t, os.WriteFile(po, []byte("O"), 0o644))

	f, err := SafeOpenAny([]string{a, b}, pb)
	require.NoError(t, err, "should be openable when inside the second root")
	f.Close()

	_, err = SafeOpenAny([]string{a, b}, po)
	assert.Error(t, err, "should be rejected when not in any")

	_, err = SafeOpenAny(nil, pb)
	assert.Error(t, err, "should be rejected when no root")
}
