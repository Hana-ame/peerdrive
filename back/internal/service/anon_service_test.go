package service

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/stretchr/testify/assert"
)

func setupAnonServiceTest(t *testing.T) (string, *AnonService) {
	t.Helper()
	repository.InitDB(":memory:")

	tmpDir, err := os.MkdirTemp("", "peerdrive_anon_test")
	assert.NoError(t, err)

	cfg := &config.Config{
		StorageDir:    tmpDir,
		StorageEnable: true,
	}
	svc := NewAnonService(cfg)
	return tmpDir, svc
}

func TestCreateCollection_ValidEntries(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	entries := []model.AnonCollectionEntry{
		{Path: "README.md", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		{Path: "src/main.go", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b"},
	}

	hash, err := svc.CreateCollection("test-coll", entries, nil)
	assert.NoError(t, err)
	assert.Len(t, hash, 64)
	assert.Regexp(t, `^[a-f0-9]{64}$`, hash)

	_, err = os.Stat(filepath.Join(tmpDir, hash[:2], hash))
	assert.NoError(t, err)
}

func TestCreateCollection_PathTraversal(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	entries := []model.AnonCollectionEntry{
		{Path: "../etc/passwd", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}

	hash, err := svc.CreateCollection("traversal-test", entries, nil)
	assert.Error(t, err)
	assert.Empty(t, hash)
	assert.Contains(t, err.Error(), "invalid path")
}

func TestCreateCollection_InvalidHash(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	entries := []model.AnonCollectionEntry{
		{Path: "valid.txt", Hash: "not-a-valid-hash"},
	}

	hash, err := svc.CreateCollection("bad-hash", entries, nil)
	assert.Error(t, err)
	assert.Empty(t, hash)
	assert.Contains(t, err.Error(), "invalid providers")
}

func TestCreateCollection_EmptyEntries(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	hash, err := svc.CreateCollection("empty-coll", nil, nil)
	assert.NoError(t, err)
	assert.Len(t, hash, 64)

	coll, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.NotNil(t, coll)
	assert.Len(t, coll.Entries, 0)
}

func TestGetCollection_ByHash(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	entries := []model.AnonCollectionEntry{
		{Path: "docs/readme.md", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}

	hash, err := svc.CreateCollection("my-collection", entries, nil)
	assert.NoError(t, err)

	coll, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.NotNil(t, coll)
	assert.Equal(t, "my-collection", coll.FriendlyName)
	assert.Equal(t, 2, coll.Version)
	assert.Len(t, coll.Entries, 1)
	assert.Equal(t, "docs/readme.md", coll.Entries[0].Path)
}

func TestGetCollection_NonexistentHash(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	coll, err := svc.GetCollectionByHash("a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a")
	assert.Error(t, err)
	assert.Nil(t, coll)
	assert.Contains(t, err.Error(), "collection not found")
}

// 发现背景（2026-08-16 传输层审阅 H1）：GetCollectionByHash 未校验 hash 就做 hash[:2] 切片，
// 短 hash（0/1 字符）→ 越界 panic 杀进程；".." 类值还逃逸 storage 目录。
// 修复：入口校验 isValidHash。此前无此测试，非法 hash 直接 panic。
func TestGetCollection_InvalidHashNoPanic(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	for _, bad := range []string{"", "a", "..", "A7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"} {
		coll, err := svc.GetCollectionByHash(bad)
		assert.Error(t, err, "hash %q should be rejected", bad)
		assert.Nil(t, coll)
		assert.Contains(t, err.Error(), "collection not found")
	}
}

func TestForkCollection(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	srcEntries := []model.AnonCollectionEntry{
		{Path: "file1.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		{Path: "file2.txt", Hash: "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b"},
	}

	srcHash, err := svc.CreateCollection("source", srcEntries, nil)
	assert.NoError(t, err)

	srcColl, err := svc.GetCollectionByHash(srcHash)
	assert.NoError(t, err)

	forkEntries := make([]model.AnonCollectionEntry, len(srcColl.Entries))
	copy(forkEntries, srcColl.Entries)
	forkEntries = append(forkEntries, model.AnonCollectionEntry{
		Path: "file3.txt",
		Hash: "c7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434c",
	})

	forkHash, err := svc.CreateCollection("forked", forkEntries, nil)
	assert.NoError(t, err)
	assert.NotEqual(t, srcHash, forkHash)

	forkColl, err := svc.GetCollectionByHash(forkHash)
	assert.NoError(t, err)
	assert.Equal(t, "forked", forkColl.FriendlyName)
	assert.Len(t, forkColl.Entries, 3)
}

func TestDownloadFile_FromCollectionEntry(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	fileContent := []byte("hello world from anon collection")
	fileHash := "c7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434d"

	targetDir := filepath.Join(tmpDir, fileHash[:2])
	err := os.MkdirAll(targetDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(targetDir, fileHash), fileContent, 0644)
	assert.NoError(t, err)

	repository.InsertFileMeta(&model.FileMeta{
		Hash:     fileHash,
		Size:     int64(len(fileContent)),
		MimeType: "text/plain",
		Filename: "testfile.txt",
		Gziped:   false,
		Type:     repository.FileTypeBlob,
	})
	repository.InsertFileProvider(fileHash, "local", filepath.Join(fileHash[:2], fileHash))

	entries := []model.AnonCollectionEntry{
		{Path: "testfile.txt", Hash: fileHash},
	}

	hash, err := svc.CreateCollection("download-test", entries, nil)
	assert.NoError(t, err)

	coll, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.Len(t, coll.Entries, 1)

	entryHash := coll.Entries[0].GetPrimaryHash()
	assert.Equal(t, fileHash, entryHash)

	data, err := os.ReadFile(filepath.Join(tmpDir, entryHash[:2], entryHash))
	assert.NoError(t, err)
	assert.Equal(t, fileContent, data)
}

func TestCreateCollection_EmptyPath(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	entries := []model.AnonCollectionEntry{
		{Path: "", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}

	hash, err := svc.CreateCollection("empty-path", entries, nil)
	assert.Error(t, err)
	assert.Empty(t, hash)
}

func TestCreateCollection_DirEntryWithoutProvider(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	// Directory entry (path ends with /) should not require providers
	entries := []model.AnonCollectionEntry{
		{Path: "images/"},
		{Path: "docs/readme.md", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}

	hash, err := svc.CreateCollection("dir-entry", entries, nil)
	assert.NoError(t, err)
	assert.Len(t, hash, 64)

	coll, err := svc.GetCollectionByHash(hash)
	assert.NoError(t, err)
	assert.Len(t, coll.Entries, 2)

	// Verify dir entry is stored without hash
	for _, e := range coll.Entries {
		if e.Path == "images/" {
			assert.Empty(t, e.Hash)
			assert.Empty(t, e.Providers)
		}
	}
}

func TestCreateCollection_FileWithoutProvider(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	// File entry (path without /) without providers should still fail
	entries := []model.AnonCollectionEntry{
		{Path: "file.txt"},
	}

	hash, err := svc.CreateCollection("no-provider", entries, nil)
	assert.Error(t, err)
	assert.Empty(t, hash)
	assert.Contains(t, err.Error(), "invalid providers")
}

func TestCreateCollection_AbsolutePath(t *testing.T) {
	tmpDir, svc := setupAnonServiceTest(t)
	defer os.RemoveAll(tmpDir)

	entries := []model.AnonCollectionEntry{
		{Path: "/etc/hosts", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}

	hash, err := svc.CreateCollection("abs-path", entries, nil)
	assert.Error(t, err)
	assert.Empty(t, hash)
	assert.Contains(t, err.Error(), "invalid path")
}
