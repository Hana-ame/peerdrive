package repository

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSchemaMigrationVersionTracking verifies that InitDB records schema_migrations
// and updates PRAGMA user_version.
// 发现背景：Issue #151 提出 SQLite schema 缺少版本跟踪，8 条 ALTER 依赖字符串匹配静默吞错。
func TestSchemaMigrationVersionTracking(t *testing.T) {
	t.Run("FreshDatabaseAppliesAllMigrationsAndSetsUserVersion", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "fresh.db")
		handle, err := OpenDB(dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(handle) })

		// PRAGMA user_version must match latest migration version (7)
		ver, err := GetUserVersion(handle)
		require.NoError(t, err)
		require.Equal(t, 7, ver, "fresh DB must have user_version = 7")

		// schema_migrations table must contain all 7 migrations
		var count int
		err = handle.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 7, count, "fresh DB must record all 7 applied migrations")
	})

	t.Run("RepeatedOpenIsIdempotent", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "idempotent.db")
		handle1, err := OpenDB(dbPath)
		require.NoError(t, err)
		require.NoError(t, CloseHandle(handle1))

		// Re-open existing DB
		handle2, err := OpenDB(dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(handle2) })

		ver, err := GetUserVersion(handle2)
		require.NoError(t, err)
		require.Equal(t, 7, ver)

		var count int
		err = handle2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 7, count)
	})

	t.Run("MigrationFailureReturnsErrorNotSwallowed", func(t *testing.T) {
		// Test migrationExec fails on bad statement
		dbPath := filepath.Join(t.TempDir(), "fail.db")
		handle, err := OpenDB(dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(handle) })

		err = migrationExec(handle, `ALTER TABLE nonexistent_table ADD COLUMN foo TEXT`)
		require.Error(t, err, "migration on nonexistent table must return an error and not be swallowed")
	})
}

// TestWALModeEnabledForFileDB verifies that WAL journal mode is enabled for
// file-backed databases (Issue #277).
// 发现背景：Issue #277 指出 SQLite 默认 DELETE journal mode 下读写并发阻塞，
// WAL 模式允许单写者 + 多读者无锁并发。
func TestWALModeEnabledForFileDB(t *testing.T) {
	t.Run("FileDBUsesWALMode", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "wal.db")
		handle, err := OpenDB(dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(handle) })

		var mode string
		err = handle.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
		require.NoError(t, err)
		require.Equal(t, "wal", strings.ToLower(mode), "file-backed DB must use WAL journal mode")
	})

	t.Run("MemoryDBSkipsWAL", func(t *testing.T) {
		// In-memory DBs do not support WAL — OpenDB must not attempt to set it.
		handle, err := OpenDB(":memory:")
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(handle) })

		var mode string
		err = handle.QueryRow(`PRAGMA journal_mode`).Scan(&mode)
		require.NoError(t, err)
		// In-memory DBs default to MEMORY journal mode, which is fine.
		// The key assertion is that OpenDB did not error trying to set WAL.
		require.NotEqual(t, "", mode, "in-memory DB must have a valid journal mode")
	})
}
