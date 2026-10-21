package pathutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsUnsafeRoot(t *testing.T) {
	cases := []struct {
		root string
		want bool
	}{
		{"/", true},
		{"//", true},
		{"/tmp", false},
		{"/tmp/", false},
		{"", false},         // Empty string is handled by other checks, not judged here
		{"relative", false}, // Relative to cwd, not a volume root
		{".", false},
		{"/data/media", false},
	}
	if runtime.GOOS == "windows" {
		cases = []struct {
			root string
			want bool
		}{
			{`C:\`, true},
			{`c:\`, true},
			{`C:/`, true},
			// "D:" is a **drive-relative** form (Windows semantics = current
			// directory on D: drive, not D: root).
			// Go's filepath.Abs cannot get "current directory on D:" (process
			// cwd is on another drive), it will join it as <cwd>\D: —
			// configuration intent and actual landing point are different
			// things, treated the same as a volume root.
			{`D:`, true},
			{`C:\data`, false},
			{`\\?\C:\`, true},
			// UNC share root: VolumeName consumes the entire segment
			// (`\\server\share`), rest is empty →
			// judged as a volume root. Semantically also correct: this is
			// equivalent to sharing out an entire network share drive.
			{`\\server\share`, true},
			{`\\server\share\media`, false},
			{"", false},
		}
	}
	for _, c := range cases {
		t.Run(c.root, func(t *testing.T) {
			// On non-Windows, Windows-form paths have no volume name, skip
			// (the platform branch picks its own table)
			if runtime.GOOS != "windows" && (filepath.VolumeName(c.root) != "" || len(c.root) > 1 && c.root[1] == ':') {
				t.Skip("skipping Windows form on non-Windows platforms")
			}
			if got := IsUnsafeRoot(c.root); got != c.want {
				t.Fatalf("IsUnsafeRoot(%q) = %v, want %v", c.root, got, c.want)
			}
		})
	}
}

func TestUnsafeRoots(t *testing.T) {
	got := UnsafeRoots("", "   ", "/data/media", "/")
	if len(got) != 1 || got[0] != "/" {
		t.Fatalf("should only pick volume roots, got=%#v", got)
	}
	if len(UnsafeRoots()) != 0 {
		t.Fatal("should not return anything when no candidates")
	}
	if len(UnsafeRoots("/tmp", "/var")) != 0 {
		t.Fatal("ordinary directories should not be picked")
	}
}
