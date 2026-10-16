package user_invariants

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"peerdrive/internal/repository"
)

// TestFileIndexMappingGroup 用户测试组：sha256 -> path 映射数据库
//
// 包含 3 项测试：
// 1. 创建 sha256 -> path 的映射 db，如果不存在自动创建。
// 2. 指定一个 path，计算/插入 sha256、path 及必要信息。
// 3. 通过 sha256 获取文件 path。
//
// 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS:
// This test group is formulated exclusively by the human user as an immutable invariant.
// AI agents are STRICTLY FORBIDDEN from modifying, commenting out, or loosening these assertions.
func TestFileIndexMappingGroup(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "user_test_file_index.db")

	// 确保测试结束关闭 DB，防止 Windows 文件句柄占用
	t.Cleanup(func() {
		_ = repository.CloseDB()
	})

	var (
		testFilePath = filepath.Join(tempDir, "sample_data.txt")
		testContent  = []byte("peerdrive user invariant: sha256 to path mapping verification")
		testHash     string
		testFileSize int64
		testFileName = "sample_data.txt"
	)

	// 准备测试文件并计算其真实的 SHA-256
	err := os.WriteFile(testFilePath, testContent, 0644)
	require.NoError(t, err, "failed to create test file")

	hasher := sha256.New()
	hasher.Write(testContent)
	testHash = hex.EncodeToString(hasher.Sum(nil))
	testFileSize = int64(len(testContent))

	// -------------------------------------------------------------
	// 测试 1: 创建 sha256 -> path 的映射 db，如果 db 不存在需要自动创建
	// -------------------------------------------------------------
	t.Run("1_CreateDB_AutoCreateIfNotExists", func(t *testing.T) {
		// 验证初始状态：数据库文件尚不存在
		_, err := os.Stat(dbPath)
		require.True(t, os.IsNotExist(err), "DB file must NOT exist before InitDB")

		// 调用 InitDB 初始化数据库
		err = repository.InitDB(dbPath)
		require.NoError(t, err, "InitDB should succeed and auto-create the database")

		// 验证数据库文件已被自动创建在磁盘上
		info, err := os.Stat(dbPath)
		require.NoError(t, err, "DB file must exist after InitDB")
		assert.False(t, info.IsDir(), "DB path must be a regular file")

		// 验证数据库健康探针通过
		err = repository.Ping()
		assert.NoError(t, err, "repository.Ping() must succeed")
	})

	// -------------------------------------------------------------
	// 测试 2: 添加一个 path，指定 path 然后插入 sha256、path 以及必要信息
	// -------------------------------------------------------------
	t.Run("2_InsertPath_WithSHA256AndMetadata", func(t *testing.T) {
		// 插入 sha256, path, name, size, deleted=false
		seq, err := repository.UpsertFileIndex(testHash, testFilePath, testFileName, testFileSize, false)
		require.NoError(t, err, "UpsertFileIndex must succeed when inserting valid file mapping")
		assert.Greater(t, seq, int64(0), "returned sync seq must be greater than 0")
	})

	// -------------------------------------------------------------
	// 测试 3: 通过 sha256 获取文件 path
	// -------------------------------------------------------------
	t.Run("3_GetPath_BySHA256", func(t *testing.T) {
		// 根据 sha256 获取记录
		record, err := repository.GetFileIndex(testHash)
		require.NoError(t, err, "GetFileIndex should find the record by sha256")
		require.NotNil(t, record, "returned FileIndex must not be nil")

		// 验证根据 sha256 获取到的文件 path 正确无误
		assert.Equal(t, testFilePath, record.Path, "retrieved path must match the inserted file path")
		assert.Equal(t, testHash, record.Hash, "retrieved hash must match the query sha256")
		assert.Equal(t, testFileName, record.Name, "retrieved name must match the inserted name")
		assert.Equal(t, testFileSize, record.Size, "retrieved size must match the inserted size")
		assert.False(t, record.Deleted, "retrieved deleted flag must be false")

		// 反向验证：查询不存在的 sha256 应返回 sql.ErrNoRows 错误
		nonExistentHash := "0000000000000000000000000000000000000000000000000000000000000000"
		notFoundRecord, err := repository.GetFileIndex(nonExistentHash)
		assert.Error(t, err, "querying non-existent sha256 must return an error")
		assert.True(t, err == sql.ErrNoRows, "error must be sql.ErrNoRows")
		assert.Nil(t, notFoundRecord, "record must be nil for non-existent hash")
	})
}
