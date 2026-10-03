package pathutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestHasReservedName(t *testing.T) {
	yes := []string{
		`C:\data\CON`, `C:\data\con.txt`, `C:\data\NUL`, `C:\data\COM1`,
		`C:\data\LPT9.log`, `C:/data/AUX`, "CON", `\\?\C:\data\COM1:stream`,
		`data\PRN\x.txt`,
	}
	// Note: the function itself is **platform-agnostic** (platform checking
	// happens in normalize). `/data/CON` is just an ordinary filename on POSIX,
	// but "contains a reserved name" is a real fact, left to the caller to decide
	// whether to care -- this way Linux CI can unit test the Windows branch.
	no := []string{
		`C:\data\file.txt`, `C:\data\concert.mp3`, `C:\data\null.txt`,
		`C:\data\com10.txt`, `C:\data\lpt0.txt`, `C:`,
		`C:\data\.`, `C:\data\..`, "",
	}
	for _, p := range yes {
		if !HasReservedName(p) {
			t.Errorf("HasReservedName(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if HasReservedName(p) {
			t.Errorf("HasReservedName(%q) = true, want false", p)
		}
	}
}

// TestWithin_ReservedDeviceName On Windows the "inside the root" textual check
// can be bypassed by device names: `root\CON` is textually inside the root, but
// opening it yields the console device. It must be judged as not inside the root.
func TestWithin_ReservedDeviceName(t *testing.T) {
	root := t.TempDir()
	old := foldCase
	defer func() { foldCase = old }()

	// foldCase is this package's switch for "judging with Windows semantics" (see
	// path.go comments), so this check can also verify the Windows branch on Linux CI.
	foldCase = true
	for _, name := range []string{"CON", "nul.txt", "COM1", "lpt9"} {
		p := filepath.Join(root, name)
		if Within(root, p) {
			t.Errorf("reserved device names should not count as inside root under Windows semantics: %s", p)
		}
	}
	// ordinary filenames are unaffected (CONCERT just starts with CON, it's not a device name)
	foldCase = true
	if !Within(root, filepath.Join(root, "concert.mp3")) {
		t.Error("CONCERT.mp3 is an ordinary filename, should not be treated as a device name")
	}

	foldCase = false // POSIX: CON is just an ordinary filename
	if runtime.GOOS != "windows" && !Within(root, filepath.Join(root, "CON")) {
		t.Error("on POSIX, CON is an ordinary filename, should be allowed")
	}
}
