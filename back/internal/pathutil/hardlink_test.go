package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// open test helper: after hardlink detection switched to taking *os.File, test
// cases must open real handles.
func openRO(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestRejectHardlink the minimal closed loop for hardlink detection: actually
// create one inode with two names.
//
// Why not mock FileInfo in the unit test: the input to the detection is nlink,
// and a mocked number is just rewriting the logic under test -- it proves
// nothing.
//
// This test case must also run on Windows (cannot t.Skip): this Windows defense
// was empty before, precisely because nobody actually verified it on Windows.
func TestRejectHardlink(t *testing.T) {
	if !NlinkSupported() {
		t.Fatal("this platform must be able to get link count")
	}
	base := t.TempDir()
	inside := filepath.Join(base, "inside.txt")
	alias := filepath.Join(base, "alias.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// single name: allowed
	if err := RejectHardlink(inside, openRO(t, inside)); err != nil {
		t.Fatalf("single-name ordinary file should not be rejected: %v", err)
	}

	if err := os.Link(inside, alias); err != nil {
		t.Skipf("this environment cannot create hard links: %v", err)
	}
	f2 := openRO(t, alias)
	if n := NlinkOf(f2); n < 2 {
		t.Fatalf("precondition failed: link count should be 2+, actual %d", n)
	}
	if err := RejectHardlink(alias, f2); err == nil {
		t.Fatal("file with two names must be rejected")
	}

	// escape hatch
	t.Setenv("PEERDRIVE_ALLOW_HARDLINKS", "1")
	if err := RejectHardlink(alias, f2); err != nil {
		t.Fatalf("should not reject after setting the escape hatch: %v", err)
	}
}

// TestNlinkOf_TracksBothNames the handle-based read must change with the link
// count: for the same inode, adding another name must make the number on the
// handle go up -- if this breaks, the detection is useless.
func TestNlinkOf_TracksBothNames(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := openRO(t, a)
	before := NlinkOf(f)
	if before != 1 {
		t.Fatalf("new file link count should be 1, actual %d", before)
	}
	b := filepath.Join(base, "b.txt")
	if err := os.Link(a, b); err != nil {
		t.Skipf("this environment cannot create hard links: %v", err)
	}
	if after := NlinkOf(f); after <= before {
		t.Fatalf("link count should increase after adding a name: before=%d after=%d", before, after)
	}
}

// TestRejectHardlink_IgnoresNonRegular Directories/device files don't
// participate: directories naturally have nlink>1, and if they were checked the
// entire directory tree couldn't be registered.
//
// A directory can't be os.Open'd directly as a handle (Windows won't let you),
// so here we instead use the "can't get a handle" path to exercise the nil
// branch, plus one **open directory handle** to cover the non-regular-file
// detection.
// A directory can't be guaranteed openable via os.Open (depends on whether the
// platform passes FILE_FLAG_BACKUP_SEMANTICS to CreateFile); if it can't be
// opened, fall back to verifying only the nil-handle path.
func TestRejectHardlink_IgnoresNonRegular(t *testing.T) {
	dir := t.TempDir()
	if df, err := os.Open(dir); err == nil {
		t.Cleanup(func() { _ = df.Close() })
		if err := RejectHardlink(dir, df); err != nil {
			t.Fatalf("directory should not be blocked by hardlink check: %v", err)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if err := RejectHardlink(dir, nil); err != nil {
		t.Fatalf("treat as allowed when no handle obtained: %v", err)
	}
}
