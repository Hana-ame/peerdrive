package service

// The traversal matrix on the HTTP API side. All four entry points accept a caller-supplied
// path, and each leads to a different system call:
//   register_local  → os.Open (arbitrary file read)
//   register_folder → os.ReadDir (arbitrary directory listing)
//   browse          → os.ReadDir (arbitrary directory listing)
//   copy            → os.MkdirAll + os.WriteFile (arbitrary file write, the most dangerous)
//
// All four share one FileService.isPathAllowed judgement, so instead of testing each one
// separately, we run **the same payload table** through every entry point -- when a fifth
// entry point is added later, just add a line to the entry list and the payloads need no rewrite.

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traversalFixture the storage root plus a same-level "must never be touched" directory.
// Deliberately made same-level with storage (rather than far away), because a wrong prefix
// match lets through exactly this kind of "next-door" path first.
type traversalFixture struct {
	base    string
	storage string
	outside string
	secret  string
	svc     *FileService
}

func newTraversalFixture(t *testing.T) traversalFixture {
	t.Helper()
	require.NoError(t, repository.InitDB(":memory:"))
	base := t.TempDir()
	// Resolve symlinks (macOS: /var -> /private/var). The service checks candidate paths against
	// the storage root with isPathAllowed, so an unresolved root makes it reject its own
	// directory — this suite passed on Linux and failed on darwin/arm64 in CI.
	if resolved, rerr := filepath.EvalSymlinks(base); rerr == nil {
		base = resolved
	}
	storage := filepath.Join(base, "storage")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(storage, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))

	cfg := &config.Config{StorageDir: storage, StorageEnable: true}
	return traversalFixture{
		base:    base,
		storage: storage,
		outside: outside,
		secret:  secret,
		svc:     NewFileService(cfg),
	}
}

// traversalPayloads one payload set, fed to every entry point.
func (f traversalFixture) payloads() []struct {
	name string
	path string
} {
	return []struct {
		name string
		path string
	}{
		{"single-level up", filepath.Join(f.storage, "..", "outside", "secret.txt")},
		{"multi-level up", filepath.Join(f.storage, "sub", "..", "..", "..", "outside", "secret.txt")},
		{"relative path up", filepath.Join("..", "outside", "secret.txt")},
		{"relative path multi-level", filepath.Join("..", "..", "..", "etc", "passwd")},
		{"absolute path outside root", f.secret},
		{"system absolute path", "/etc/passwd"},
		{"NUL injection", filepath.Join(f.storage, "ok.txt") + "\x00"},
		{"in-place dots", filepath.Join(f.storage, ".", "..", "outside")},
	}
}

// TestTraversal_RegisterLocalRejects arbitrary file read: after registration the provider path gets read back.
func TestTraversal_RegisterLocalRejects(t *testing.T) {
	f := newTraversalFixture(t)
	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			h, err := f.svc.RegisterLocal(c.path, "x.txt")
			assert.Error(t, err, "register_local must reject: %q", c.path)
			assert.Empty(t, h)
		})
	}
}

// TestTraversal_RegisterFolderRejects arbitrary directory listing + batch arbitrary file read.
func TestTraversal_RegisterFolderRejects(t *testing.T) {
	f := newTraversalFixture(t)
	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			res, err := f.svc.RegisterFolder(c.path)
			assert.Error(t, err, "register_folder must reject: %q", c.path)
			assert.Empty(t, res)
		})
	}
}

// TestTraversal_BrowseDirRejects arbitrary directory listing (spits out file names and sizes).
func TestTraversal_BrowseDirRejects(t *testing.T) {
	f := newTraversalFixture(t)
	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			entries, err := f.svc.BrowseDir(c.path)
			assert.Error(t, err, "browse must reject: %q", c.path)
			assert.Nil(t, entries)
		})
	}
}

// snapshotOutsideTree snapshots the whole subtree "outside storage" (relative path + size).
// A correct rejection of an unauthorized copy is not "an error was returned", but **nothing
// extra ended up on disk** -- asserting only the error would miss the old
// "write to disk first, then fix the DB record" pattern.
func (f traversalFixture) snapshotOutsideTree(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(f.base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if p == f.storage || strings.HasPrefix(p, f.storage+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(f.base, p)
		if rel == "." {
			return nil
		}
		size := int64(-1)
		if !info.IsDir() {
			size = info.Size()
		}
		out = append(out, rel+":"+strconv.FormatInt(size, 10))
		return nil
	})
	require.NoError(t, err)
	sort.Strings(out)
	return out
}

// TestTraversal_CopyFileRejects arbitrary file write: dest_path goes straight to MkdirAll + WriteFile.
// This is the only one of the four entry points that can "land" (combined with a public upload
// it could write authorized_keys and the like), so besides the unauthorized path, it must also
// reject **before** writing to disk rather than fixing up the DB record afterwards.
func TestTraversal_CopyFileRejects(t *testing.T) {
	f := newTraversalFixture(t)
	const dummyHash = "0000000000000000000000000000000000000000000000000000000000000000"

	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			before := f.snapshotOutsideTree(t)

			dst, err := f.svc.CopyFile(dummyHash, c.path)
			assert.Error(t, err, "copy must reject: %q", c.path)
			assert.Empty(t, dst)

			assert.Equal(t, before, f.snapshotOutsideTree(t),
				"unauthorized copy must leave no trace outside storage: %s", c.path)
		})
	}
}

// TestTraversal_SymlinkEscapeRejects symlinks: put a link inside the root pointing outside.
// Directory-level symlinks are the easiest to miss -- "storage/alias/secret.txt" looks two
// levels under the root.
func TestTraversal_SymlinkEscapeRejects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipped")
	}
	f := newTraversalFixture(t)

	fileLink := filepath.Join(f.storage, "link.txt")
	require.NoError(t, os.Symlink(f.secret, fileLink))
	_, err := f.svc.RegisterLocal(fileLink, "link.txt")
	assert.Error(t, err, "outward-pointing **file** symlink must be rejected")
	_, err = f.svc.BrowseDir(fileLink)
	assert.Error(t, err, "symlink should not be browsed as directory")

	dirLink := filepath.Join(f.storage, "alias")
	require.NoError(t, os.Symlink(f.outside, dirLink))
	_, err = f.svc.RegisterFolder(dirLink)
	assert.Error(t, err, "outward-pointing **directory** symlink must be rejected")
	_, err = f.svc.BrowseDir(dirLink)
	assert.Error(t, err, "outward-pointing directory symlink should not be listed")
}

// TestTraversal_DeclaredShareDirAllowed a directory outside storage must be allowed as soon as the
// operator has declared it in PEERDRIVE_SHARE_DIRS; undeclared same-level directories are still
// rejected.
//
// Discovery background (real incident, 2026-09-20): there used to be only one judgement against
// the storage root, which forced shared directories to live under downloads, or else
// "registration succeeded, the listing shows it, but pulling gives read failed".
func TestTraversal_DeclaredShareDirAllowed(t *testing.T) {
	f := newTraversalFixture(t)
	share := filepath.Join(f.base, "media-on-another-disk") // same level as storage
	require.NoError(t, os.MkdirAll(share, 0o755))
	movie := filepath.Join(share, "movie.mkv")
	require.NoError(t, os.WriteFile(movie, []byte("x"), 0o644))

	undeclared := f.svc
	_, err := undeclared.RegisterLocal(movie, "movie.mkv")
	assert.Error(t, err, "undeclared directory must be rejected (cannot let it pass just because 'the directory exists')")

	require.NoError(t, repository.InitDB(":memory:"))
	declared := NewFileService(&config.Config{
		StorageDir:    f.storage,
		StorageEnable: true,
		ShareDirs:     share,
	})
	_, err = declared.RegisterLocal(movie, "movie.mkv")
	assert.NoError(t, err, "declared shared directory (outside storage) must be allowed")

	entries, err := declared.BrowseDir(share)
	assert.NoError(t, err, "declared shared directory should be browsable")
	assert.NotEmpty(t, entries)

	// declaring A does not mean A's neighbours are allowed
	_, err = declared.RegisterLocal(f.secret, "secret.txt")
	assert.Error(t, err, "declaring media should not also allow its sibling directory")
}

// TestTraversal_RegisterLocalHardlinkRejected hard links: the hole on the HTTP registration side.
//
// Unlike symlinks, a hard link has **no direction** -- the same inode is called alias.txt
// inside storage and linked.txt outside it; EvalSymlinks cannot see it, and the path
// judgement will necessarily allow it (see the precondition assertion below). So only the
// link count helps. This test must exist alongside the one in transport.Create: judging on
// one side only lets the other side register the same inode and go around it.
func TestTraversal_RegisterLocalHardlinkRejected(t *testing.T) {
	// Windows is no longer skipped: it can now report NumberOfLinks too (the handle-based NlinkOf).
	if !pathutil.NlinkSupported() {
		t.Skip("this platform cannot obtain hardlink count, this defense is empty (see pathutil/links_*.go)")
	}
	f := newTraversalFixture(t)

	original := filepath.Join(f.outside, "linked.txt")
	require.NoError(t, os.WriteFile(original, []byte("shared inode"), 0o644))
	alias := filepath.Join(f.storage, "alias.txt")
	if err := os.Link(original, alias); err != nil {
		t.Skipf("this environment cannot create hardlinks: %v", err)
	}

	assert.True(t, f.svc.isPathAllowed(alias), "precondition: pure path check cannot detect hardlinks")

	_, err := f.svc.RegisterLocal(alias, "alias.txt")
	require.Error(t, err, "files with multiple names must be rejected")
	assert.Contains(t, err.Error(), "hard link", "error should indicate hardlink: %v", err)

	// escape valve for false positives (pnpm node_modules / cp -l backup directories)
	t.Setenv("PEERDRIVE_ALLOW_HARDLINKS", "1")
	h, err := f.svc.RegisterLocal(alias, "alias.txt")
	assert.NoError(t, err, "setting PEERDRIVE_ALLOW_HARDLINKS=1 should allow")
	assert.Len(t, h, 64)
}

// TestTraversal_InsideStillWorks the defense must not be over-eager: normal usage must not be killed.
func TestTraversal_InsideStillWorks(t *testing.T) {
	f := newTraversalFixture(t)

	inRoot := filepath.Join(f.storage, "sub", "a.txt")
	require.NoError(t, os.WriteFile(inRoot, []byte("hello"), 0o644))

	h, err := f.svc.RegisterLocal(inRoot, "a.txt")
	assert.NoError(t, err)
	assert.Len(t, h, 64)

	entries, err := f.svc.BrowseDir(f.storage)
	assert.NoError(t, err, "storage root itself should be browsable")
	assert.NotEmpty(t, entries)

	// the relative-path spelling (the frontend often sends it this way) must not be rejected just for not being absolute
	_, err = f.svc.BrowseDir("sub")
	assert.NoError(t, err, "relative paths inside root should be allowed")

	// a copy "detour" inside the root (contains .. but still lands inside the root) must also be allowed
	dst := filepath.Join(f.storage, "sub", "..", "copied.txt")
	const dummyHash = "0000000000000000000000000000000000000000000000000000000000000000"
	_, _ = f.svc.CopyFile(dummyHash, dst) // a missing source hash fails, but must NOT fail on the path
	// only assert it is not an "outside the root" error -- a missing source is a different matter
	_, err = f.svc.CopyFile("short", dst)
	assert.NotContains(t, err.Error(), "outside", "detour but still inside root should not be judged as unauthorized")
}
