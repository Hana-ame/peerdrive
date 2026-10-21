package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// All test cases create directories on the fly with t.TempDir(), relying on no
// platform-specific fixed path (Linux's /tmp and Windows's C:\Users aren't
// guaranteed to exist).

func TestWithin_Basic(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "movies", "a.mkv")

	if !Within(root, inner) {
		t.Fatalf("file inside root should be judged as inside: %s", inner)
	}
	if !Within(root, root) {
		t.Fatal("root directory itself should be judged as inside")
	}
	// parent and child directories differ by just one character; a wrong
	// prefix match would falsely allow it
	sibling := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-other", "x.txt")
	if Within(root, sibling) {
		t.Fatalf("sibling directory with same prefix should not be falsely judged as inside: %s", sibling)
	}
	outside := filepath.Join(filepath.Dir(root), "elsewhere.txt")
	if Within(root, outside) {
		t.Fatalf("file outside root should not be judged as inside: %s", outside)
	}
}

func TestWithin_EmptyIsRejected(t *testing.T) {
	// an empty root means "allow everything", which must be blocked -- when
	// config isn't applied, behave "less permissive" rather than "more permissive"
	if Within("", "/anything") {
		t.Fatal("empty root must be rejected")
	}
	if Within(t.TempDir(), "") {
		t.Fatal("empty path must be rejected")
	}
}

func TestWithin_TraversalAndVolume(t *testing.T) {
	root := t.TempDir()
	// ".." escape in a relative path
	if Within(root, filepath.Join(root, "..", "..", "etc", "passwd")) {
		t.Fatal(".. escape must be blocked")
	}
	// cross-drive/cross-volume: on Windows C: and D: can never be in a containment relationship
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(root)
		other := "D:\\some\\file.txt"
		if vol != "" && strings.EqualFold(vol, "C:") && Within(root, other) {
			t.Fatal("cross-drive path should not be judged as inside")
		}
	}
}

// Windows's case insensitivity is the **easiest to get wrong and hardest to find
// on Linux CI**: previously written as `if runtime.GOOS == "windows" { t.Skip() }`,
// so the Windows branch was never run from start to finish. Changed to an
// explicit switch foldCase, so both branches can be verified on any platform.
func TestWithin_CaseFolding(t *testing.T) {
	if foldCase != (runtime.GOOS == "windows") {
		t.Fatalf("foldCase default doesn't match platform: foldCase=%v GOOS=%s", foldCase, runtime.GOOS)
	}

	old := foldCase
	defer func() { foldCase = old }()
	root := t.TempDir()
	swapped := filepath.Join(swapCaseASCII(root), "a.txt")

	if runtime.GOOS == "windows" {
		// On Windows **filepath.Rel itself folds case**: path/filepath's
		// sameWord is strings.EqualFold on Windows, so element-by-element
		// comparison treats `C:\Temp` and `c:\tEMP` as the same thing. So
		// whatever foldCase is set to doesn't change the result -- this is
		// consistent with NTFS semantics (different case means the same
		// directory) and is correct.
		// Observed on a real Windows machine on 2026-09-20: Linux CI can never catch this.
		if !Within(root, swapped) {
			t.Fatalf("on Windows, different case is still the same path, should be allowed: %s vs %s", root, swapped)
		}
	} else {
		foldCase = false // Linux/ext4: different case means two different paths
		if Within(root, swapped) {
			t.Fatalf("should not be allowed on case-sensitive filesystem: %s vs %s", root, swapped)
		}
	}

	foldCase = true // Windows/NTFS: different case is still the same directory
	if !Within(root, swapped) {
		t.Fatalf("should be allowed on case-insensitive filesystem: %s vs %s", root, swapped)
	}

	// folding can only make "the same directory" equal; it must not fold an
	// outside-root path inside
	foldCase = true
	outside := filepath.Join(filepath.Dir(root), "elsewhere.txt")
	if Within(root, swapCaseASCII(outside)) {
		t.Fatalf("case folding should not put outside path inside root: %s", outside)
	}
}

func TestWithin_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		// a non-elevated shell can't create symlinks (developer mode not
		// enabled), just skip
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this environment doesn't allow creating symlinks: %v", err)
	}
	// a symlink pointing outside the root: judge as out-of-bounds after resolution (prefer less permissive)
	if Within(root, link) {
		t.Fatal("symlink pointing outside root must be judged as unauthorized")
	}

	// a symlink pointing inside the root: should be allowed
	inTarget := filepath.Join(root, "real.txt")
	if err := os.WriteFile(inTarget, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	inLink := filepath.Join(root, "alias.txt")
	if err := os.Symlink(inTarget, inLink); err != nil {
		t.Skipf("this environment doesn't allow creating symlinks: %v", err)
	}
	if !Within(root, inLink) {
		t.Fatal("symlink pointing inside root should be allowed")
	}
}

func TestWithinAny(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	other := t.TempDir()

	if !WithinAny([]string{a, b}, filepath.Join(b, "x", "y.txt")) {
		t.Fatal("should be allowed when falling inside the second root")
	}
	if WithinAny([]string{a, b}, filepath.Join(other, "x.txt")) {
		t.Fatal("should be rejected when not in any")
	}
	if WithinAny(nil, filepath.Join(a, "x.txt")) {
		t.Fatal("should be rejected when no root")
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList(" /data/a , , /data/b ,, ")
	if len(got) != 2 || got[0] != "/data/a" || got[1] != "/data/b" {
		t.Fatalf("split result is wrong: %#v", got)
	}
	if SplitList("   ") != nil {
		t.Fatal("empty config should return nil")
	}
}

func swapCaseASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		} else if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}
