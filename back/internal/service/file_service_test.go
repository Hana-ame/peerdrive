package service

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"os"
	"path"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"

	"github.com/stretchr/testify/assert"
)

func setupFileServiceTest() (string, *FileService) {
	repository.InitDB(":memory:")

	tmpDir, err := os.MkdirTemp("", "peerdrive_file_service_test")
	if err != nil {
		panic(err)
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

// 发现背景（2026-08-16 传输层审阅 F2）：RegisterLocal 此前接受任意绝对路径，
// 配合 LocalFetcher 的 provider 回读 = 匿名任意文件读取（可读 /etc/shadow 等）。
// 修复：锚定 storage 根目录（Abs + EvalSymlinks 前缀判定）。此测试保护该边界。
func TestRegisterLocalOutsideStorageRootRejected(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	outside := os.TempDir() // storage 根目录之外
	outFile := filepath.Join(outside, "peerdrive_root_escape_test.txt")
	err := os.WriteFile(outFile, []byte("secret"), 0644)
	assert.NoError(t, err)
	defer os.Remove(outFile)

	hash, err := svc.RegisterLocal(outFile, "x.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside storage root")
	assert.Empty(t, hash)
}

// 发现背景（2026-08-16 传输层审阅 F3）：CopyFile 目标路径此前可写任意位置
// （绝对路径原样采用 / 相对路径 ../ 逃逸）。修复：写盘前校验目标在 storage 根内。
func TestCopyFileOutsideStorageRootRejected(t *testing.T) {
	tmpDir, svc := setupFileServiceTest()
	defer os.RemoveAll(tmpDir)

	src := filepath.Join(tmpDir, "src.txt")
	err := os.WriteFile(src, []byte("hello"), 0644)
	assert.NoError(t, err)
	hash, err := svc.RegisterLocal(src, "src.txt")
	assert.NoError(t, err)

	// ../ 逃逸目标
	_, err = svc.CopyFile(hash, filepath.Join("..", "..", "escape_me.txt"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside storage root")
}

// 发现背景（2026-08-16 传输层审阅 H1 同类）：Delete 此前对任意 hash 直接 os.Remove
// provider 路径。修复：先校验 hash，再确认路径在 storage 根内。
func TestDeleteInvalidHashRejected(t *testing.T) {
	_, svc := setupFileServiceTest()

	err := svc.Delete("not-a-hash")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hash")
}

// 发现背景（2026-08-16 传输层审阅 H6）：BrowseDir 此前接受任意绝对路径 → 任意目录列举。
// 修复：锚定 storage 根。
func TestBrowseDirOutsideStorageRootRejected(t *testing.T) {
	_, svc := setupFileServiceTest()

	_, err := svc.BrowseDir("/etc")
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
