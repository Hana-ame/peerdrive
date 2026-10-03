package pathutil

// Path traversal attack matrix.
//
// Why a separate file: Within/WithinAny is the single entry point for all
// "can this file be touched" decisions. Once it's bypassed, every layer above
// (registration, read, copy, delete, share listing) is wasted. So this does
// not test "is the function correct", but categorizes payloads from an
// attacker's perspective, each category as a table-driven test — when a new
// payload is seen, just add a row to the table.
//
// Organization:
//   - TestTraversal_Rejected    —— must reject (unauthorized)
//   - TestTraversal_Allowed     —— must allow (don't break legitimate usage
//                                  due to over-defense)
//   - TestTraversal_Platform    —— payloads only meaningful on specific
//                                  platforms
//   - TestTraversal_SymlinkTree —— symlink combinations (directory-level
//                                  symlinks, root itself is a symlink)
//
// Cross-platform note: payloads are always built with filepath.Join, never
// hardcode "/" or "\" in tests. Pure Windows semantics (drive letters, UNC,
// \\?\) go in TestTraversal_Platform and skip by GOOS.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// traversalRoot Creates a real root directory + a directory that "must never
// be touched", both with content. Uses real files rather than pure string
// comparison because the decision chain includes EvalSymlinks — when a path
// doesn't exist it fails and takes a different branch, which pure string
// comparison can't test.
type traversalRoot struct {
	root    string // Allowed root
	outside string // Another directory outside the root (same parent, simulating
	// something like /etc "right next door")
	secret  string // File inside outside
}

func newTraversalRoot(t *testing.T) traversalRoot {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return traversalRoot{root: root, outside: outside, secret: secret}
}

func TestTraversal_Rejected(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root

	// name → attacker input relative to root. Uses Join so separators follow
	// the platform.
	cases := []struct {
		name string
		path string
	}{
		{"single dot upward escape", filepath.Join(root, "..", "outside", "secret.txt")},
		{"multi-level upward escape", filepath.Join(root, "sub", "..", "..", "outside", "secret.txt")},
		{"absolute path directly outside root", tr.secret},
		{"absolute path to system file", "/etc/passwd"},
		{"sibling directory same prefix", filepath.Join(filepath.Dir(root), "root-other", "x.txt")},
		{"sibling directory same prefix with suffix", filepath.Join(filepath.Dir(root), "root.bak", "secret.txt")},
		{"current directory dot escape", filepath.Join(root, ".", "..", "outside", "secret.txt")},
		{"repeated separator smuggling escape", root + string(filepath.Separator) + string(filepath.Separator) + ".." +
			string(filepath.Separator) + "outside" + string(filepath.Separator) + "secret.txt"},
		{"bare dot dots", filepath.Join(root, "..", "..", "..", "..", "..", "etc", "passwd")},
		// NUL: Go's os.Open will reject it itself (invalid argument), but
		// **pure string checks don't recognize NUL**. The two below, after
		// removing NUL, fall inside the root / or are caught by Clean — used
		// to separately verify the "NUL always reject" rule itself takes
		// effect, rather than being incidentally blocked by other reasons.
		{"NUL ending (otherwise inside root)", filepath.Join(root, "ok.txt") + "\x00"},
		{"NUL truncation smuggling escape", filepath.Join(root, "ok.txt") + "\x00" + filepath.Join("..", "outside", "secret.txt")},
		{"empty path", ""},
		{"only dot", "."},
		{"only dot dot", ".."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if Within(root, c.path) {
				t.Fatalf("must reject but was allowed: root=%s path=%q", root, c.path)
			}
			if WithinAny([]string{root}, c.path) {
				t.Fatalf("WithinAny must reject but was allowed: path=%q", c.path)
			}
		})
	}
}

func TestTraversal_Allowed(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root

	// These are **legitimate usage** — over-defense that kills them is
	// equally a bug: the previous "shared directory must be inside download"
	// was a false constraint forced out by over-checking.
	cases := []struct {
		name string
		path string
	}{
		{"root itself", root},
		{"file inside root", filepath.Join(root, "a.txt")},
		{"deep subdirectory inside root", filepath.Join(root, "sub", "deep", "a.txt")},
		{"with redundant current directory", filepath.Join(root, ".", "a.txt")},
		{"with redundant separators", root + string(filepath.Separator) + string(filepath.Separator) + "sub" +
			string(filepath.Separator) + "a.txt"},
		{"with redundant dot in the middle", filepath.Join(root, "sub", ".", "a.txt")},
		{"round trip back inside root", filepath.Join(root, "sub", "..", "a.txt")},
		{"filename with dots but not escape", filepath.Join(root, "a..b.txt")},
		{"round trip far and back inside root", filepath.Join(root, "sub", "deep", "..", "..", "sub", "a.txt")},
		{"root itself with trailing separator", root + string(filepath.Separator)},
		{"root itself with dot", filepath.Join(root, ".")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !Within(root, c.path) {
				t.Fatalf("should allow but was rejected: root=%s path=%q", root, c.path)
			}
		})
	}
}

// TestTraversal_EmptyRootIsNotAllowAll Empty root directory = "allow
// everything", which is the most dangerous configuration error.
func TestTraversal_EmptyRootIsNotAllowAll(t *testing.T) {
	tr := newTraversalRoot(t)
	if Within("", tr.secret) {
		t.Fatal("empty root allowed outside file")
	}
	if WithinAny([]string{"", "   "}, tr.secret) {
		t.Fatal("whitespace root allowed outside file")
	}
	if WithinAny([]string{}, tr.secret) {
		t.Fatal("no root allowed outside file")
	}
	// A mixed list with one legitimate root must not let an empty string
	// take effect
	if WithinAny([]string{""}, filepath.Join(tr.root, "a.txt")) {
		t.Fatal("empty root should not allow anything")
	}
}

// TestTraversal_OneBadRootDoesNotOpenEverything WithinAny is "match any one
// to allow", so if there's even one misconfiguration in the list (like an
// empty string, or the root directory itself), it must not open up
// everything else.
func TestTraversal_OneBadRootDoesNotOpenEverything(t *testing.T) {
	tr := newTraversalRoot(t)
	other := filepath.Join(filepath.Dir(tr.root), "other-root")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two legitimate roots + one empty string: the empty string must not
	// become "any path counts as a match"
	if WithinAny([]string{tr.root, "", other}, tr.secret) {
		t.Fatal("empty string in the list became allow-all")
	}
	if !WithinAny([]string{tr.root, "", other}, filepath.Join(other, "x.txt")) {
		t.Fatal("second legitimate root should be effective")
	}
}

// TestTraversal_Platform Payloads that only make sense on the corresponding
// platform.
func TestTraversal_Platform(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root

	if runtime.GOOS == "windows" {
		// Cross-drive: a file in the C: root can never belong to D:
		if len(filepath.VolumeName(root)) > 0 {
			otherVol := "D:" + string(filepath.Separator) + "secret.txt"
			if Within(root, otherVol) {
				t.Fatalf("cross-drive path was judged as inside: %s", otherVol)
			}
		}
		// UNC and \\?\ extended-length paths: should not be treated as "inside
		// the root"
		for _, p := range []string{
			`\\server\share\secret.txt`,
			`\\?\C:\Windows\win.ini`,
			`\\.\C:\Windows\win.ini`,
		} {
			if Within(root, p) {
				t.Fatalf("special prefix path was judged as inside: %s", p)
			}
		}
		// Drive letter with different case is still the same drive (folding
		// in effect)
		vol := filepath.VolumeName(root)
		if strings.EqualFold(vol, "c:") {
			swapped := strings.ToUpper(vol[:1]) + ":" + root[len(vol):]
			if vol != swapped && !Within(root, filepath.Join(swapped, "a.txt")) {
				t.Fatalf("drive letter with different case should be treated as same volume: %s vs %s", root, swapped)
			}
		}
	} else {
		// Linux/macOS: backslash is a **legitimate filename character**, not
		// a separator. Implementations that hand-write strings.HasPrefix(a+"/")
		// are most likely to get this wrong.
		weird := filepath.Join(root, `a\b.txt`)
		if err := os.WriteFile(weird, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !Within(root, weird) {
			t.Fatalf("ordinary filename with backslash should be allowed: %s", weird)
		}
		// Conversely: real separator escape must be blocked
		if Within(root, root+string(filepath.Separator)+".."+string(filepath.Separator)+"outside") {
			t.Fatal("separator escape not blocked")
		}
	}
}

// TestTraversal_SymlinkTree Symlink combinations. Key point: what is judged
// is the **resolved** path.
func TestTraversal_SymlinkTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	tr := newTraversalRoot(t)
	root := tr.root

	mk := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("this environment doesn't allow creating symlinks: %v", err)
		}
	}

	t.Run("inner symlink pointing outside root file", func(t *testing.T) {
		mk(tr.secret, filepath.Join(root, "link.txt"))
		if Within(root, filepath.Join(root, "link.txt")) {
			t.Fatal("symlink pointing outside root must be judged as unauthorized")
		}
	})

	t.Run("inner directory symlink pointing outside root directory", func(t *testing.T) {
		// Directory-level symlinks are the most insidious: link/x.txt looks
		// like it's "two levels under root", without resolution it would be
		// allowed
		mk(tr.outside, filepath.Join(root, "alias"))
		if Within(root, filepath.Join(root, "alias", "secret.txt")) {
			t.Fatal("symlink pointing outside root directory must be judged as unauthorized")
		}
	})

	t.Run("inner symlink pointing inside root", func(t *testing.T) {
		real := filepath.Join(root, "real.txt")
		if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		mk(real, filepath.Join(root, "alias.txt"))
		if !Within(root, filepath.Join(root, "alias.txt")) {
			t.Fatal("symlink pointing inside root should be allowed")
		}
	})

	t.Run("symlink points outside root but target doesn't exist", func(t *testing.T) {
		// EvalSymlinks will fail → falls back to the Clean-ed path. At this
		// point root/link is string-wise really under root, allowing it is
		// expected (when actually opened, os.Open will also fail), but we
		// can't judge it as unauthorized just because stat failed —
		// registering a file that hasn't landed yet is legitimate usage.
		mk(filepath.Join(tr.outside, "not-there.txt"), filepath.Join(root, "dangling"))
		if !Within(root, filepath.Join(root, "dangling")) {
			t.Fatal("dangling symlink should not be judged unauthorized due to stat failure")
		}
	})

	t.Run("root itself is a symlink", func(t *testing.T) {
		realRoot := filepath.Join(filepath.Dir(root), "real-root")
		if err := os.MkdirAll(realRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		// The target file must **really exist**: EvalSymlinks resolves the
		// entire path, when the last segment doesn't exist it will fail and
		// fall back to the unresolved form, making the "symlink path" look
		// like it's outside the root — that's the test case not being set up
		// properly, not a decision problem.
		if err := os.WriteFile(filepath.Join(realRoot, "a.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		linkRoot := filepath.Join(filepath.Dir(root), "link-root")
		mk(realRoot, linkRoot)
		// Both forms should point to the same place
		if !Within(linkRoot, filepath.Join(realRoot, "a.txt")) {
			t.Fatal("when root is a symlink, real path form should be allowed")
		}
		if !Within(realRoot, filepath.Join(linkRoot, "a.txt")) {
			t.Fatal("when root is a symlink, symlink path form should be allowed")
		}
	})
}

// TestTraversal_AnySymlinkRootWithinAny WithinAny allows if any one root
// matches, so "symlink escaping from root A to root B" is allowed (B is
// already a legitimate root), but "symlink escaping outside both roots" must
// be rejected. This prevents someone writing WithinAny as "all roots must
// match".
func TestTraversal_SymlinkBetweenRoots(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	tr := newTraversalRoot(t)
	second := filepath.Join(filepath.Dir(tr.root), "second-root")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tr.root, "to-second")
	if err := os.Symlink(filepath.Join(second, "x.txt"), link); err != nil {
		t.Skipf("this environment doesn't allow creating symlinks: %v", err)
	}
	if !WithinAny([]string{tr.root, second}, link) {
		t.Fatal("symlink pointing to another **legitimate** root should be allowed (don't write WithinAny as all-must-match)")
	}
	if WithinAny([]string{tr.root, second}, tr.secret) {
		t.Fatal("paths outside both roots must be rejected")
	}
}

// TestTraversal_ClassicPayloads Classic bypass forms that have appeared in the
// real world.
// The list comes from commonly used payloads in historical path traversal CVEs,
// routed by GOOS for cross-platform.
func TestTraversal_ClassicPayloads(t *testing.T) {
	tr := newTraversalRoot(t)
	root := tr.root
	sep := string(filepath.Separator)

	// Both sides must be blocked
	both := []struct{ name, path string }{
		{"proc self root symlink", filepath.Join("/proc", "self", "root", "etc", "passwd")},
		{"proc self cwd symlink", filepath.Join("/proc", "self", "cwd", "..", "outside", "secret.txt")},
		{"dot dot followed by NUL separator", ".." + sep + "\x00" + sep + "outside"),
		{"dot dot followed by newline", "..\n" + sep + "outside"),
		{"empty byte ending inside root name", filepath.Join(root, "ok") + "\x00"),
	}
	if runtime.GOOS != "windows" {
		for _, c := range both {
			t.Run(c.name, func(t *testing.T) {
				if Within(root, c.path) {
					t.Fatalf("classic payload was allowed: %q", c.path)
				}
			})
		}
	}

	if runtime.GOOS == "windows" {
		// Windows-specific: drive-relative paths, ADS, reserved device names,
		// trailing dots/spaces, 8.3 short names
		win := []struct{ name, path string }{
			{"drive-relative upward", `C:..\..\Windows\win.ini`},
			{"UNC upward", `\\server\share\..\..\secret.txt`},
			{"extended length prefix", `\\?\C:\Windows\win.ini`},
			{"device namespace", `\\.\C:\Windows\win.ini`},
			{"ADS data stream", filepath.Join(root, "a.txt") + ":evil"},
			{"ADS DATA stream", filepath.Join(root, "a.txt") + "::$DATA"},
			{"reserved device name", filepath.Join(root, "CON")},
			{"reserved device name with suffix", filepath.Join(root, "COM1.txt")},
			{"8.3 short name escape", filepath.Join(root, "ABOUT~1", "..", "..", "outside")},
		}
		for _, c := range win {
			t.Run(c.name, func(t *testing.T) {
				// ADS/reserved device names still point to things inside the
				// root, allowing them isn't traversal; what must truly be
				// blocked are the ones with .. / UNC / special prefixes.
				if strings.ContainsAny(c.path, ":\\") && strings.Contains(c.path, "..") ||
					strings.HasPrefix(c.path, `\\`) {
					if Within(root, c.path) {
						t.Fatalf("Windows classic payload was allowed: %q", c.path)
					}
				}
			})
		}
		// Trailing dots/spaces: Windows strips them, ultimately may land on
		// **another** file
		tricky := filepath.Join(root, "a.txt.")
		_ = tricky
	}
}

// TestTraversal_RootIsFilesystemRoot Extreme configuration: the root is set
// to the filesystem root. Here "allow everything" is the intent of the
// configuration, Within shouldn't be too clever to reject; but this test case
// pins the behavior so nobody thinks Within can guard against this
// configuration (it can't — startup configuration validation is needed).
func TestTraversal_RootIsFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not have a single filesystem root")
	}
	if !Within("/", "/etc/passwd") {
		t.Fatal("when root is configured as /, should allow all (this is configuration intent, not traversal)")
	}
	if !Within("/", "/tmp/anything") {
		t.Fatal("when root is configured as /, should allow all")
	}
}

// TestTraversal_SplitListTraversal The configuration splitting itself can be a
// traversal entry point: a directory name with spaces gets split in half, or
// an empty item is treated as "current directory".
func TestTraversal_SplitListTraversal(t *testing.T) {
	got := SplitList("/data/a, ../etc, ,/data/b")
	if len(got) != 3 {
		t.Fatalf("split result is wrong: %#v", got)
	}
	if got[1] != "../etc" {
		t.Fatalf("relative path should be preserved as-is (caller should Abs), got=%q", got[1])
	}
	// Empty items must be dropped: an empty item in "a,,b" if kept would
	// become Abs("")=cwd → allow everything
	if len(SplitList("a,,b")) != 2 {
		t.Fatalf("empty items must be dropped: %#v", SplitList("a,,b"))
	}
}

// TestTraversal_SymlinkedRootMissingLeaf When the root itself is a symlink and
// the last segment of path doesn't exist yet, both sides must resolve to the
// same tree.
//
// This is the shape that actually went red on the darwin CI once: macOS's
// t.TempDir() is /var/folders/..., and /var is a symlink to /private/var.
// root (exists) gets EvalSymlinks-resolved to /private/var/..., path (leaf
// doesn't exist) fails resolution and stays /var/... → two names for the same
// directory are treated as two trees, and a file clearly inside the root is
// judged unauthorized. Fix is to fall back to the longest existing prefix for
// resolution (resolveBestEffort).
func TestTraversal_SymlinkedRootMissingLeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	// Leaf doesn't exist yet: this is what a write target/symlink target looks
	// like
	missing := filepath.Join(link, "a.txt")
	if !Within(link, missing) {
		t.Fatalf("when root is symlink + leaf doesn't exist, should be judged inside root: root=%q path=%q", link, missing)
	}
	if !Within(real, missing) {
		t.Fatalf("root configured with real directory name should also recognize this path: root=%q path=%q", real, missing)
	}

	// Control: defenses must not loosen because of this, things outside the
	// root still must be rejected
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if Within(link, filepath.Join(outside, "x.txt")) {
		t.Fatal("file outside root must not be judged as inside root")
	}
}
