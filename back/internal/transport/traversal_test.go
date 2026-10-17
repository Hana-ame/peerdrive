package transport

// create / upload / write-file are **peer-controlled** path entry points:
// the path field is entirely stuffed in by the peer, and the name field directly
// participates in on-disk path concatenation. pathutil.Within already blocks
// string-level traversal; here we verify that "after blocking, each verb really
// doesn't have a bypass opening another path" -- historically incidents have
// happened exactly with this "check in one place, write in another" pattern.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/pathutil"
)

// newTraversalIndex root directory + decoy file outside the root.
func newTraversalIndex(t *testing.T) (*FileIndexService, string, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))
	svc := NewFileIndexService(root)
	t.Cleanup(svc.Close) // Windows: without closing session handles, TempDir cannot be cleaned up
	return svc, root, secret
}

func TestTraversal_CreateRejectsEscape(t *testing.T) {
	initTestDB(t)
	svc, root, secret := newTraversalIndex(t)
	parent := filepath.Dir(root)

	cases := []struct {
		name string
		path string
	}{
		{"upward escape", filepath.Join(root, "..", filepath.Base(parent), "secret.txt")},
		{"multi-level upward escape", filepath.Join(root, "a", "b", "..", "..", "..", "..", "etc", "passwd")},
		{"outside-root absolute path", secret},
		{"system absolute path", "/etc/passwd"},
		{"NUL injection (otherwise inside root)", filepath.Join(root, "ok.txt") + "\x00"},
		{"dot-in-place escape", filepath.Join(root, ".", "..", filepath.Base(parent), "secret.txt")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Create(c.path)
			assert.Error(t, err, "create must reject: %q", c.path)
		})
	}
}

// TestTraversal_CreateAllowsInside Defense cannot be overzealous: normal paths inside
// the root must be registrable, otherwise the fake constraint of "share directory is
// outside so can't pull" will come back.
func TestTraversal_CreateAllowsInside(t *testing.T) {
	initTestDB(t)
	svc, root, _ := newTraversalIndex(t)

	inRoot := filepath.Join(root, "sub", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(inRoot), 0o755))
	require.NoError(t, os.WriteFile(inRoot, []byte("ok"), 0o644))

	fi, err := svc.Create(inRoot)
	require.NoError(t, err)
	assert.Equal(t, "a.txt", fi.Name)

	// Redundant notation (repeated separators / embedded "." in the middle) should not be misjudged
	redundant := root + string(filepath.Separator) + string(filepath.Separator) + "sub" +
		string(filepath.Separator) + "." + string(filepath.Separator) + "a.txt"
	_, err = svc.Create(redundant)
	assert.NoError(t, err, "redundant but still inside root notation should pass")
}

// TestTraversal_CreateSymlink Symlink inside root pointing outside root -> reject.
// Directory-level symlinks are especially tricky: "root/alias/secret.txt" looks two
// levels under root.
func TestTraversal_CreateSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links on Windows requires developer mode/admin, skipping")
	}
	initTestDB(t)
	svc, root, secret := newTraversalIndex(t)
	outside := filepath.Dir(secret)

	link := filepath.Join(root, "link.txt")
	require.NoError(t, os.Symlink(secret, link))
	_, err := svc.Create(link)
	assert.Error(t, err, "file symlink pointing outside root must be rejected")

	dirLink := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(outside, dirLink))
	_, err = svc.Create(filepath.Join(dirLink, "secret.txt"))
	assert.Error(t, err, "directory symlink pointing outside root must be rejected")
}

// TestTraversal_WriteFileNameEscape upload/write-file's name goes directly into
// filepath.Join(uploadDir, sanitizeName(name)), the last concatenation of the on-disk path.
func TestTraversal_WriteFileNameEscape(t *testing.T) {
	initTestDB(t)
	svc, root, _ := newTraversalIndex(t)

	cases := []struct {
		name string
		in   string
	}{
		{"dot-dot upward", "../../evil.txt"},
		{"backslash dot-dot upward", `..\..\evil.txt`},
		{"pure dot-dot", ".."},
		{"multi-level dot-dot", "../a/../../evil.txt"},
		{"absolute path", "/etc/passwd"},
		{"with subdirectory", "sub/evil.txt"},
		{"empty name", ""},
		{"dot", "."},
		{"root", "/"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fi, err := svc.WriteFile(c.in, strings.NewReader("payload"))
			require.NoError(t, err, "WriteFile should not error, but must land inside the root directory")
			// On-disk location must still be under uploadDir -- this is the only hard assertion
			assert.Contains(t, filepath.Clean(fi.Path), filepath.Clean(root),
				"name=%q escaped the root directory: %s", c.in, fi.Path)
			assert.True(t, svc.IsPathAllowed(fi.Path), "name=%q landed at a disallowed location: %s", c.in, fi.Path)
		})
	}
}

// TestTraversal_CreateHardlinkRejected Hard links: no direction, EvalSymlinks can't
// detect them. The same inode has one name inside the root and another outside,
// and from the path alone you can't tell whether it's been exposed, so the only
// option is "reject if there are multiple names".
func TestTraversal_CreateHardlinkRejected(t *testing.T) {
	// No longer skip based on GOOS: previously no one verified on Windows, so that
	// defense line was quietly empty. Now Windows uses GetFileInformationByHandle
	// to also get NumberOfLinks; this test must actually run on both platforms.
	if !pathutil.NlinkSupported() {
		t.Skip("this platform cannot get hard link count, this defense line is empty (see pathutil/links_*.go)")
	}
	initTestDB(t)
	svc, root, _ := newTraversalIndex(t)

	outside := t.TempDir()
	original := filepath.Join(outside, "linked.txt")
	require.NoError(t, os.WriteFile(original, []byte("shared inode"), 0o644))
	alias := filepath.Join(root, "alias.txt")
	if err := os.Link(original, alias); err != nil {
		t.Skipf("this environment cannot create cross-directory hard links: %v", err)
	}

	// Path check **passes** -- it can't tell this is a hard link; this is another hole
	// besides symlinks
	assert.True(t, svc.IsPathAllowed(alias), "precondition: pure path check cannot detect hard links")

	_, err := svc.Create(alias)
	require.Error(t, err, "files with multiple names must be rejected from registration")
	assert.Contains(t, err.Error(), "hard link", "error should indicate it's a hard link: %v", err)

	// When the switch is on, allow (pnpm node_modules / git alternates and similar directories need it).
	// The check reads the env var each time, so t.Setenv works for testing -- no need
	// to modify package-level variables just for the test.
	t.Setenv("PEERDRIVE_ALLOW_HARDLINKS", "1")
	_, err = svc.Create(alias)
	assert.NoError(t, err, "should pass when PEERDRIVE_ALLOW_HARDLINKS=1")
}

// TestTraversal_ReadRootSymlinkEscape After declaring a shared root directory, the
// read boundary is relaxed, but **symlink escape cannot be relaxed along with it**:
// a symlink inside the shared directory pointing to /etc must still be judged as
// privilege escalation.
func TestTraversal_ReadRootSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symbolic links on Windows requires developer mode/admin, skipping")
	}
	initTestDB(t)
	svc, root, secret := newTraversalIndex(t)
	share := t.TempDir() // simulate PEERDRIVE_SHARE_DIRS, outside the download root

	svc.AddReadRoot(share)

	// Declared -> readable (this is the product constraint fixed in the previous round)
	inShare := filepath.Join(share, "movie.mkv")
	require.NoError(t, os.WriteFile(inShare, []byte("x"), 0o644))
	assert.True(t, svc.IsPathReadable(inShare), "a declared shared directory must be readable")

	// Symlink inside the shared directory pointing elsewhere -> privilege escalation
	require.NoError(t, os.Symlink(secret, filepath.Join(share, "link.txt")))
	assert.False(t, svc.IsPathReadable(filepath.Join(share, "link.txt")),
		"symlink inside a shared directory pointing outside must be judged as privilege escalation")

	// But the registration/write boundary must not be widened by AddReadRoot
	_, err := svc.Create(inShare)
	assert.Error(t, err, "readable != registrable: write boundary must not be widened by AddReadRoot")
	_ = root
}

// TestTraversal_RedactDisallowedPath Legacy DBs / old versions may have Path values
// outside the root directory; list/info/sync must redact them when returning
// externally -- we cannot leak absolute paths to the peer.
func TestTraversal_RedactDisallowedPath(t *testing.T) {
	svc, root, secret := newTraversalIndex(t)
	p := &PeerJSService{fileIndex: svc}

	got := p.redactDisallowedPath(FileInfo{Hash: "h", Path: secret, Name: "secret.txt"})
	assert.Equal(t, "", got.Path, "Path outside the root directory must be redacted")
	assert.Equal(t, "secret.txt", got.Name, "Name can stay")

	inside := filepath.Join(root, "a.txt")
	got = p.redactDisallowedPath(FileInfo{Hash: "h", Path: inside})
	assert.Equal(t, inside, got.Path, "Path inside the root directory should be kept")
}
