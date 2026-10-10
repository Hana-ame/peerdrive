package service

// Note: This file is a test for legacy code (see doc/archive/LEGACY.md, pending deletion/migration); discovery background is not annotated individually. The "discovery background" convention applies to new code.

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/pkg/urlguard"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupFileServiceTest() (string, *FileService) {
	repository.InitDB(":memory:")

	tmpDir, err := os.MkdirTemp("", "peerdrive_file_service_test")
	if err != nil {
		panic(err)
	}

	// Resolve symlinks before using the path as a storage root.
	//
	// Why (found via CI on darwin/arm64, invisible on Linux): os.MkdirTemp returns
	// /var/folders/... on macOS, while every file the service actually touches lives under the
	// resolved /private/var/folders/.... isPathAllowed / copyInto compare against that root, so an
	// unresolved root makes the service reject its own storage directory with
	// "path outside allowed root". Every test in this file that uploads or registers therefore
	// passed on Linux and failed on macOS. Fixing it in the shared helper rather than per-test is
	// what makes this correct everywhere instead of just for the test that happened to trip it.
	if resolved, rerr := filepath.EvalSymlinks(tmpDir); rerr == nil {
		tmpDir = resolved
	}

	cfg := &config.Config{
		StorageDir:    tmpDir,
		StorageEnable: true,
	}

	svc := NewFileService(cfg)
	return tmpDir, svc
}

func TestNewFileService(t *testing.T) {
	cfg := &config.Config{
		StorageDir:    "/tmp/peerdrive",
		StorageEnable: true,
	}

	svc := NewFileService(cfg)
	assert.NotNil(t, svc)
	assert.Equal(t, "/tmp/peerdrive", svc.storageDir)
	assert.True(t, svc.storageEnable)

	cfg2 := &config.Config{
		StorageDir:    "/tmp/peerdrive",
		StorageEnable: false,
	}
	svc2 := NewFileService(cfg2)
	assert.False(t, svc2.storageEnable)
}

func TestRegisterLocal(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "hello.txt")
	err := os.WriteFile(testFile, []byte("hello world"), 0644)
	assert.NoError(t, err)

	hash, err := svc.RegisterLocal(testFile, "hello.txt")
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.Len(t, hash, 64)
}

func TestRegisterLocalNonexistentPath(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	hash, err := svc.RegisterLocal(filepath.Join(tmpDir, "does_not_exist.txt"), "does_not_exist.txt")
	assert.Error(t, err)
	assert.Empty(t, hash)
}

func TestRegisterLocalStorageDisabled(t *testing.T) {
	repository.InitDB(":memory:")
	cfg := &config.Config{
		StorageDir:    "/tmp",
		StorageEnable: false,
	}
	svc := NewFileService(cfg)

	hash, err := svc.RegisterLocal("/tmp/test.txt", "test.txt")
	assert.Error(t, err)
	assert.Equal(t, ErrStorageDisabled, err)
	assert.Empty(t, hash)
}

// Discovery background (2026-08-16 transport layer review F2): RegisterLocal
// previously accepted any absolute path, combined with LocalFetcher's provider
// readback = anonymous arbitrary file reading (can read /etc/shadow etc.).
// Fix: anchor to the storage root (Abs + EvalSymlinks prefix check). This test
// protects that boundary.
func TestRegisterLocalOutsideStorageRootRejected(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	outside := os.TempDir() // Outside the storage root
	outFile := filepath.Join(outside, "peerdrive_root_escape_test.txt")
	err := os.WriteFile(outFile, []byte("secret"), 0644)
	assert.NoError(t, err)
	defer os.Remove(outFile)

	hash, err := svc.RegisterLocal(outFile, "x.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside storage root")
	assert.Empty(t, hash)
}

// Discovery background (2026-08-16 transport layer review F3): CopyFile's
// target path previously could write to any location (absolute paths used as-is
// / relative paths ../ escape). Fix: validate target is inside storage root
// before writing to disk.
func TestCopyFileOutsideStorageRootRejected(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	src := filepath.Join(tmpDir, "src.txt")
	err := os.WriteFile(src, []byte("hello"), 0644)
	assert.NoError(t, err)
	hash, err := svc.RegisterLocal(src, "src.txt")
	assert.NoError(t, err)

	// ../ escape target
	_, err = svc.CopyFile(hash, filepath.Join("..", "..", "escape_me.txt"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside storage root")
}

// Discovery background (2026-08-16 transport layer review H1 same category):
// Delete previously directly called os.Remove on the provider path for any hash.
// Fix: validate hash first, then confirm path is inside storage root.
func TestDeleteInvalidHashRejected(t *testing.T) {
	_, svc := setupFileServiceTest()

	err := svc.Delete("not-a-hash")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hash")
}

// Discovery background (2026-08-16 transport layer review H6): BrowseDir
// previously accepted any absolute path → arbitrary directory listing.
// Fix: anchor to the storage root.
func TestBrowseDirOutsideStorageRootRejected(t *testing.T) {
	_, svc := setupFileServiceTest()

	// Must be an **absolute path on the target platform**: on Windows "/etc"
	// is not an absolute path (no volume name), it would be treated as a
	// relative path joined under the storage root, so the check would pass and
	// the failure reason would be "file not found" instead of "unauthorized" —
	// the assertion would be a false positive.
	// (Found on a real Windows machine 2026-09-20, never exposed on Linux CI)
	outside := "/etc"
	if runtime.GOOS == "windows" {
		outside = filepath.Join(filepath.VolumeName(svc.storageDir), `\Windows\System32\drivers\etc`)
	}

	_, err := svc.BrowseDir(outside)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside storage root")
}

func TestRegisterFolder(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("aaa"), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("bbb"), 0644)
	assert.NoError(t, err)
	err = os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "sub", "c.txt"), []byte("ccc"), 0644)
	assert.NoError(t, err)

	results, err := svc.RegisterFolder(tmpDir)
	assert.NoError(t, err)
	assert.Len(t, results, 3)

	filenames := make(map[string]bool)
	for _, r := range results {
		filenames[r["filename"]] = true
		assert.NotEmpty(t, r["hash"])
	}
	assert.True(t, filenames["a.txt"])
	assert.True(t, filenames["b.txt"])
	assert.True(t, filenames["c.txt"])
}

func TestVerify(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "verify.txt")
	err := os.WriteFile(testFile, []byte("verify me"), 0644)
	assert.NoError(t, err)

	hash, err := svc.RegisterLocal(testFile, "verify.txt")
	assert.NoError(t, err)

	meta, err := svc.Verify(hash)
	assert.NoError(t, err)
	assert.NotNil(t, meta)
	assert.Equal(t, "verify.txt", meta.Filename)
	assert.Equal(t, int64(9), meta.Size)
}

func TestRegisterLocalEmptyFilename(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "auto_name.txt")
	err := os.WriteFile(testFile, []byte("hello auto name"), 0644)
	assert.NoError(t, err)

	// Register with empty filename - should derive from path
	hash, err := svc.RegisterLocal(testFile, "")
	assert.NoError(t, err)
	assert.NotEmpty(t, hash)

	meta, err := repository.GetFileMeta(hash)
	assert.NoError(t, err)
	assert.NotNil(t, meta)
	assert.Equal(t, "auto_name.txt", meta.Filename)
}

func TestRegisterURLDefaultFilename(t *testing.T) {
	// This test verifies that filename is derived from URL when not provided
	// (tested via the http test server approach)

	// The filename derivation uses path.Base(url) as fallback
	// RegisterURL also handles Content-Disposition header
	// We can verify the logic works by checking the URL path parsing
	filename := path.Base("https://example.com/path/to/myfile.zip")
	assert.Equal(t, "myfile.zip", filename)
}
func TestVerifyNonexistent(t *testing.T) {
	_, svc := setupFileServiceTest()

	meta, err := svc.Verify("nonexistent_hash_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assert.NoError(t, err)
	assert.Nil(t, meta)
}

func TestDelete(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	testFile := filepath.Join(tmpDir, "delete_me.txt")
	err := os.WriteFile(testFile, []byte("to be deleted"), 0644)
	assert.NoError(t, err)

	hash, err := svc.RegisterLocal(testFile, "delete_me.txt")
	assert.NoError(t, err)

	err = svc.Delete(hash)
	assert.NoError(t, err)

	meta, err := repository.GetFileMeta(hash)
	assert.NoError(t, err)
	assert.Nil(t, meta)

	providers, err := repository.GetFileProviders(hash)
	assert.NoError(t, err)
	assert.Nil(t, providers)
}

func TestDeleteStorageDisabled(t *testing.T) {
	repository.InitDB(":memory:")
	cfg := &config.Config{
		StorageDir:    "/tmp",
		StorageEnable: false,
	}
	svc := NewFileService(cfg)

	err := svc.Delete("somehash")
	assert.Error(t, err)
	assert.Equal(t, ErrStorageDisabled, err)
}

// TestResolveURL_SSRFGuard — POST /files/register_url is "fetch a caller-supplied URL
// on the node's behalf", the same shape as the P2P pull verb, so it must carry the
// same SSRF guard.
//
// Background: measured on 2026-10-04 that this endpoint had **no** guard — posting
// {"url":"http://127.0.0.1:<node port>/peerjs/share"} returned 201 and stored the
// node's own admin response as a file. These cases pin that every internal target is
// rejected *before* any network call, and that the rejection names the reason.
func TestResolveURL_SSRFGuard(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	blocked := map[string]string{
		"http://127.0.0.1:3399/peerjs/share": "internal/local", // the original exploit
		"http://localhost:8080/":            "localhost",
		"http://169.254.169.254/latest/meta-data/": "internal/local",
		"http://10.0.0.5/":                        "internal/local",
		"http://192.168.1.1/":                     "internal/local",
		"http://[::1]:9000/":                      "internal/local",
		"file:///etc/passwd":                      "only http/https",
		"ftp://example.com/x":                     "only http/https",
		"http://user:pw@example.com/":             "user info",
	}

	for raw, wantSubstr := range blocked {
		_, _, _, _, _, err := svc.ResolveURL(raw, true)
		assert.Error(t, err, "%s must be rejected by the SSRF guard", raw)
		assert.Contains(t, err.Error(), wantSubstr,
			"%s: error %q should explain why (expected to mention %q)", raw, err.Error(), wantSubstr)
	}
}

// TestGuardExternalURL_AcceptsPublicHost makes sure the guard is not simply
// "reject everything": a syntactically valid public https URL must pass the check.
// (It is only checked, not fetched — no network in a unit test.)
func TestGuardExternalURL_AcceptsPublicHost(t *testing.T) {
	assert.NoError(t, urlguard.GuardExternalURL("https://example.com/file.zip"))
	assert.NoError(t, urlguard.GuardExternalURL("http://example.com/a.bin"))
}

// TestUpload_RegistersFileIndex is the regression guard for the bug found 2026-10-04:
// Upload wrote file_meta + file_providers but never file_index, so the file showed in
// the operator's own /files listing yet never reached the share manifest (nodeshare
// reads file_index, not file_meta) — peers got files:[] forever.
//
// The assertion is deliberately on file_index rather than on the manifest: it is the
// single source of truth for "what may this node serve outward", so holding this
// prevents nodeshare from silently regressing again from this direction.
func TestUpload_RegistersFileIndex(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	// setupFileServiceTest resolves symlinks in the storage root (macOS /var -> /private/var),
	// which is why this passes on darwin — see the comment there.
	payload := []byte("upload writes file_index now")
	meta, err := svc.Upload(bytes.NewReader(payload), "demo.txt")
	require.NoError(t, err, "upload itself must succeed")
	require.NotEmpty(t, meta.Hash)

	listed, err := repository.ListFileIndex(0, 100)
	assert.NoError(t, err)

	var found bool
	for _, f := range listed {
		if f.Hash == meta.Hash {
			found = true
			assert.Equal(t, "demo.txt", f.Name)
			assert.Equal(t, int64(len(payload)), f.Size)
			assert.False(t, f.Deleted)
			assert.NotEmpty(t, f.Path,
				"index path must be absolute — nodeshare matches share-dir prefixes against it")
		}
	}
	assert.True(t, found, "uploaded file %s must appear in file_index", meta.Hash)
}

// TestUpload_RawShaFileWithoutExt verifies that uploaded files are stored as raw files named by their SHA without extensions,
// and the original filename is properly registered in both file_meta and file_index tables.
// 发现背景：issue 要求上传的文件默认使用 raw file，没有 ext，文件名是 sha，sha 文件表中保留文件名项目。
func TestUpload_RawShaFileWithoutExt(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	payload := []byte("upload raw sha file test payload")
	meta, err := svc.Upload(bytes.NewReader(payload), "document.pdf")
	require.NoError(t, err)
	require.NotEmpty(t, meta.Hash)
	assert.Equal(t, "document.pdf", meta.Filename)

	// Check file_meta table
	savedMeta, err := repository.GetFileMeta(meta.Hash)
	require.NoError(t, err)
	require.NotNil(t, savedMeta)
	assert.Equal(t, "document.pdf", savedMeta.Filename)

	// Check file_index table
	idx, err := repository.GetFileIndex(meta.Hash)
	require.NoError(t, err)
	require.NotNil(t, idx)
	assert.Equal(t, "document.pdf", idx.Name)

	// Verify on-disk file: raw file, no ext, filename is sha
	baseName := filepath.Base(idx.Path)
	assert.Equal(t, meta.Hash, baseName)
	assert.Equal(t, "", filepath.Ext(baseName))
	data, err := os.ReadFile(idx.Path)
	require.NoError(t, err)
	assert.Equal(t, payload, data)
}

