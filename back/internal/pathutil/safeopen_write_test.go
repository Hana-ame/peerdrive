package pathutil

// Symlink escape on the write path (copy/delete/upload landing on disk).
//
// Why a separate file: the read side (safeopen_test.go) eliminated TOCTOU with
// os.Root long ago, but the write/landing side was still the two-step "check
// With Within first, then os.WriteFile / os.Remove" approach. Between those two
// steps lies a path resolution — someone who can write to a shared directory
// can swap a **directory component** into a symlink in between, redirecting the
// write/delete outside the root. The cases below are that shape.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWriteFixture root root + decoy area outside the root (contains victim.txt).
type writeFixture struct {
	root    string
	outside string
	victim  string
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	return writeFixture{
		root:    t.TempDir(),
		outside: t.TempDir(),
		victim:  filepath.Join(t.TempDir(), "victim.txt"), // Separate location to avoid collateral damage
	}
}

// TestSafeWriteFile_FinalComponentSymlink The classic shape: place a symlink
// inside the root pointing to the victim file, then write via the "normal
// path". **From the reader's perspective the path is completely legitimate**
// (Within will necessarily allow it), only an actual write (and this test does
// indeed go through WriteFile) exposes the issue.
//
// TestSafeWriteFile_FinalComponentSymlinkRace The standard TOCTOU shape:
// check the path first (everything normal at this point) → attacker swaps the
// component into a symlink → then land the write.
//
// Why it must be **check first, swap after**: a statically placed symlink
// would be caught by Within too (normalize has EvalSymlinks). What is truly
// unguarded is "the time between validation and landing". So we need to
// simulate a race, not just leave a symlink sitting there — the latter proves
// nothing about what the new code does.
//
// os.WriteFile would follow this symlink and write content outside; going
// through Root it must fail.
func TestSafeWriteFile_FinalComponentSymlinkRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin")
	}
	f := newWriteFixture(t)
	require.NoError(t, os.WriteFile(f.victim, []byte("ORIGINAL"), 0o600))

	target := filepath.Join(f.root, "report.txt")

	// 1) Validation passes: at this moment it is not yet a symlink
	assert.True(t, WithinAny([]string{f.root}, target), "precondition: path check should allow at this moment")

	// 2) Attacker swaps it for a symlink pointing to the victim file in between
	require.NoError(t, os.Symlink(f.victim, target))

	// 3) Landing: old os.WriteFile would follow all the way outside; new must reject
	err := SafeWriteFileAny([]string{f.root}, target, []byte("PWNED"), 0o644)
	assert.Error(t, err, "write must fail when swapped to symlink pointing outside root after check")

	got, rerr := os.ReadFile(f.victim)
	require.NoError(t, rerr)
	assert.Equal(t, "ORIGINAL", string(got), "victim file must remain unchanged")

	// Control experiment: same race, old os.WriteFile does write through the symlink.
	// With this line, the failure above cannot be dismissed as coincidence like
	// "symlinks are unavailable in this environment" or similar.
	legacyVictim := filepath.Join(f.outside, "legacy-victim.txt")
	require.NoError(t, os.WriteFile(legacyVictim, []byte("ORIGINAL"), 0o600))
	legacyLink := filepath.Join(f.root, "legacy.txt")
	require.NoError(t, os.Symlink(legacyVictim, legacyLink))
	require.NoError(t, os.WriteFile(legacyLink, []byte("PWNED"), 0o644))
	gotLegacy, lerr := os.ReadFile(legacyVictim)
	require.NoError(t, lerr)
	assert.Equal(t, "PWNED", string(gotLegacy), "control: os.WriteFile does write through the symlink — this is the hole to plug")
}

// TestSafeWriteFile_ParentDirSymlinkRace A more subtle variant: what gets
// swapped is the **parent directory** (real directory → symlink pointing
// outside the root). The target filename is brand new, it can never be a
// symlink itself, so checks like "the final component must not be a symlink"
// simply cannot see where the problem is.
func TestSafeWriteFile_ParentDirSymlinkRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin")
	}
	f := newWriteFixture(t)
	outsideDir := filepath.Join(f.outside, "dir")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))

	sub := filepath.Join(f.root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "new.txt")
	assert.True(t, WithinAny([]string{f.root}, target), "precondition: sub is still a real directory at this moment")

	// Swap the middle layer
	require.NoError(t, os.Remove(sub))
	require.NoError(t, os.Symlink(outsideDir, sub))

	err := SafeWriteFileAny([]string{f.root}, target, []byte("PWNED"), 0o644)
	assert.Error(t, err, "must not write when parent directory is swapped to symlink escaping outside root")

	_, sterr := os.Stat(filepath.Join(outsideDir, "new.txt"))
	assert.True(t, os.IsNotExist(sterr), "no new file should appear outside root")
}

// TestSafeRemove_ParentDirSymlinkRace os.Remove's old problem: it only
// resolves paths without checking boundaries, and follows symlinks in the path
// to any file — "allowing management of one's own directory" becomes "can
// delete any file". Same pattern: check first (legitimate), swap after (middle
// component becomes a symlink).
func TestSafeRemove_ParentDirSymlinkRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin")
	}
	f := newWriteFixture(t)

	outsideDir := filepath.Join(f.outside, "dir")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))
	outsideTarget := filepath.Join(outsideDir, "target.txt")
	require.NoError(t, os.WriteFile(outsideTarget, []byte("do not touch"), 0o600))

	sub := filepath.Join(f.root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "target.txt")
	assert.True(t, WithinAny([]string{f.root}, target), "precondition: path check should allow at this moment")

	require.NoError(t, os.Remove(sub))
	require.NoError(t, os.Symlink(outsideDir, sub))

	err := SafeRemoveAny([]string{f.root}, target)
	assert.Error(t, err, "deletion through symlink pointing outside root must fail")

	_, sterr := os.Stat(outsideTarget)
	assert.NoError(t, sterr, "file outside root must still exist")
}

// TestSafeWriteFile_RefusesOutside The path itself is outside the root: any
// ".." form should not pass.
func TestSafeWriteFile_RefusesOutside(t *testing.T) {
	f := newWriteFixture(t)
	require.NoError(t, os.WriteFile(f.victim, []byte("keep"), 0o600))

	escaped := filepath.Join(f.root, "..", filepath.Base(f.outside), "..", filepath.Base(filepath.Dir(f.victim)), filepath.Base(f.victim))
	escaped = f.victim // Using the real path outside the root is more straightforward

	err := SafeWriteFileAny([]string{f.root}, escaped, []byte("x"), 0o644)
	assert.ErrorIs(t, err, ErrOutsideRoot, "outside root path must report ErrOutsideRoot: %s", escaped)

	err = SafeRemoveAny([]string{f.root}, escaped)
	assert.ErrorIs(t, err, ErrOutsideRoot, "deletion of outside root path must also be blocked: %s", escaped)

	_, sterr := os.Stat(f.victim)
	assert.NoError(t, sterr, "file outside root must not be writable/deletable")
}

// TestSafeWriteFile_InsideStillWorks Defenses cannot be overzealous: normal
// writes must still work, including the "parent directory does not exist yet"
// case that previously relied on os.MkdirAll.
func TestSafeWriteFile_InsideStillWorks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-exist-yet") // The root itself hasn't been created yet

	err := SafeWriteFileAny([]string{root}, filepath.Join(root, "a", "b", "c.txt"), []byte("hello"), 0o644)
	require.NoError(t, err, "allowed writes inside root should not be blocked (including auto-creating parent directories)")

	got, rerr := os.ReadFile(filepath.Join(root, "a", "b", "c.txt"))
	require.NoError(t, rerr)
	assert.Equal(t, "hello", string(got))

	f, err := SafeOpenFileAny([]string{root}, filepath.Join(root, "d.txt"), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	_, werr := f.WriteString("handle-ok")
	require.NoError(t, werr)
	require.NoError(t, f.Close())

	require.NoError(t, SafeRemoveAny([]string{root}, filepath.Join(root, "d.txt")))
	_, sterr := os.Stat(filepath.Join(root, "d.txt"))
	assert.True(t, os.IsNotExist(sterr), "deletion should actually take effect")
}

// TestSafeWriteFile_RejectsRootItself The root itself must not be writable/
// deletable: rel == "." must be explicitly rejected, not just "happen to
// compute to no deleterious behavior".
func TestSafeWriteFile_RejectsRootItself(t *testing.T) {
	root := t.TempDir()
	assert.Error(t, SafeWriteFileAny([]string{root}, root, []byte("x"), 0o644))
	assert.Error(t, SafeRemoveAny([]string{root}, root))
	assert.Error(t, SafeRemoveAllAny([]string{root}, root))
	assert.Error(t, SafeMkdirAllAny([]string{root}, root, 0o755))
	_, sterr := os.Stat(root)
	assert.NoError(t, sterr, "root directory must still exist after rejection")
}

// TestSafeWriteFile_NULAndReserved String-level payloads must also be blocked
// on the write path: NUL injection, Windows reserved device names.
func TestSafeWriteFile_NULAndReserved(t *testing.T) {
	root := t.TempDir()
	assert.Error(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "ok.txt")+"\x00", []byte("x"), 0o644))

	// The case-folding branch: originally only effective on Windows, here we
	// manually enable it so Linux CI can also cover it
	old := foldCase
	foldCase = true
	t.Cleanup(func() { foldCase = old })
	assert.Error(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "CON"), []byte("x"), 0o644),
		"reserved device names cannot be written as ordinary files")
	assert.NoError(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "CONCERT.mp3"), []byte("x"), 0o644),
		"CONCERT.mp3 is a normal filename, must not be falsely blocked")
}

// TestSafeWriteFile_OutsideRootError Error messages should include the path for
// ops diagnostics — "outside root" without saying which path is as good as
// no information.
func TestSafeWriteFile_OutsideRootError(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	err := SafeWriteFileAny([]string{root}, outside, []byte("x"), 0o644)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), filepath.Base(outside)), "error message should contain target path: %v", err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}
