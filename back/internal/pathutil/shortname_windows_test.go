//go:build windows

package pathutil

// Dedicated test cases for 8.3 short names.
//
// Every case in this file previously had nowhere to run on Linux CI (8.3 is an
// NTFS/Windows concept), so "Windows semantics" remained unverified for a long
// time. They are now part of the Windows real-machine test matrix (see
// doc/NETDISK.md §11.6).

import (
	"os"
	=path/filepath
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithin_83ShortNameInsideRoot The bug being fixed: the file **is really
// inside the allowed root**, it's just that the path form uses 8.3 short
// names — previously it would be judged as unauthorized.
//
// This is not nitpicking: shared directories registered by other tools, paths
// passing through Symlink/UNC/old panels may all become short-name forms,
// resulting in "file is there, hash matches, just can't read it".
func TestWithin_83ShortNameInsideRoot(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "this test case is only meaningful on Windows")

	base := t.TempDir()
	root := filepath.Join(base, "Shared Media Library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(root, "movie.mkv")
	require.NoError(t, os.WriteFile(realFile, []byte("x"), 0o644))

	shortRoot := ShortNameOf(root)
	if shortRoot == "" {
		t.Skip("this volume did not generate an 8.3 short name for this directory (8.3 generation may be disabled)")
	}
	t.Logf("root=%s short=%s", root, shortRoot)
	require.NotEqual(t, root, shortRoot, "precondition: short name must really differ from long name")

	shortFile := filepath.Join(shortRoot, "movie.mkv")

	assert.True(t, Within(root, realFile), "long name form must be inside root")
	assert.True(t, Within(root, shortFile), "8.3 short name form of the same file must also be inside root")

	// Do a real open: just having consistent checks is not enough, Open must
	// also recognize this alias
	f, err := SafeOpen(root, shortFile)
	require.NoError(t, err, "short name form must actually open")
	require.NoError(t, f.Close())
}

// TestWithin_83AliasCannotEscape After fixing relax, must confirm we didn't
// accidentally open the door: a short name is an alias, it can point to the
// same directory, but it **cannot** turn things outside the root into
// things inside.
func TestWithin_83AliasCannotEscape(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "this test case is only meaningful on Windows")

	base := t.TempDir()
	parent := filepath.Join(base, "Parent Dir")
	share := filepath.Join(parent, "Shared Media Library")
	private := filepath.Join(parent, "Private Vault")
	require.NoError(t, os.MkdirAll(share, 0o755))
	require.NoError(t, os.MkdirAll(private, 0o755))
	secret := filepath.Join(private, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))

	shortParent := ShortNameOf(parent)
	if shortParent == "" {
		t.Skip("this volume did not generate 8.3 short names")
	}
	// Use short name to reach outside the shared directory
	viaAlias := filepath.Join(shortParent, "Private Vault", "secret.txt")
	assert.False(t, Within(share, viaAlias), "short name must not make files outside the shared directory reachable")

	// Conversely: short name with shared directory, long name request, must
	// also be consistent (mixing both sides must not create gaps)
	shortShare := ShortNameOf(share)
	if shortShare != "" {
		assert.True(t, Within(share, filepath.Join(shortShare, "a.txt")),
			"the shared directory's own short name alias should still count as inside root")
	}
}

// TestExpandShortNames_NewFile The new file being written — its last segment
// does not exist yet — the expansion must not give up: the directory part
// must still be restored to long names.
func TestExpandShortNames_NewFile(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "this test case is only meaningful on Windows")

	base := t.TempDir()
	dir := filepath.Join(base, "Brand New Dir")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	shortDir := ShortNameOf(dir)
	if shortDir == "" {
		t.Skip("this volume did not generate 8.3 short names")
	}
	got := ExpandShortNames(filepath.Join(shortDir, "not-created-yet.bin"))
	// Note the baseline must also be expanded: t.TempDir() itself may already
	// be in short-name form (on CI it's RUNNER~1), directly using it for the
	// expected value would always be red on that machine, while the expansion
	// logic is actually correct.
	assert.Equal(t, filepath.Join(ExpandShortNames(base), "Brand New Dir", "not-created-yet.bin"), got,
		"directory segments should be restored to long names, filename kept as-is")

	// Being able to write into it means it really works (the write path also
	// goes through the same expansion)
	require.NoError(t, SafeWriteFileAny([]string{dir}, filepath.Join(shortDir, "not-created-yet.bin"), []byte("ok"), 0o644))
	b, err := os.ReadFile(filepath.Join(dir, "not-created-yet.bin"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(b))
}

// TestExpandShortNames_UnknownPath A non-existent path must not be "expanded
// into" something else: if the long name cannot be obtained, return as-is.
func TestExpandShortNames_UnknownPath(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "this test case is only meaningful on Windows")
	base := t.TempDir()
	p := filepath.Join(base, "No Such Thing", "x.txt")
	// Same as above: the baseline may be in short-name form, only after
	// expansion is it a comparable long-name form
	assert.Equal(t, filepath.Join(ExpandShortNames(base), "No Such Thing", "x.txt"), ExpandShortNames(p))
}

// TestSafeWriteFile_ShortNameRootAlias When the allowed root itself is
// configured as an 8.3 short name, the write path must also recognize it.
//
// This actually caused problems: on GitHub's Windows runner t.TempDir() is
// `C:\Users\RUNNER~1\...`, while pickRoot only expanded the path but not the
// root → root short name + path long name were treated as two different
// trees → a write clearly inside the root was judged "path outside allowed
// root" (this machine's username has no 8.3 alias, so it couldn't be
// reproduced on a local real machine, could only rely on creating the shape
// here).
func TestSafeWriteFile_ShortNameRootAlias(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "this test case is only meaningful on Windows")

	base := t.TempDir()
	dir := filepath.Join(base, "Shared Media Library")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	short := ShortNameOf(dir)
	if short == "" {
		t.Skip("this volume did not generate 8.3 short names")
	}
	require.NotEqual(t, short, dir, "short name must really differ from long name, otherwise this test case tests nothing")

	// Allowed root uses short name, write path also uses short name: mixing
	// both sides must not create a "can't check in" gap
	require.NoError(t, SafeWriteFileAny([]string{short}, filepath.Join(short, "a.bin"), []byte("ok"), 0o644))
	b, err := os.ReadFile(filepath.Join(dir, "a.bin"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(b))
}
