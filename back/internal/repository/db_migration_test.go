package repository

import (
	"path/filepath"
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

		// PRAGMA user_version must match latest migration version (9)
		ver, err := GetUserVersion(handle)
		require.NoError(t, err)
		require.Equal(t, 9, ver, "fresh DB must have user_version = 9")

		// schema_migrations table must contain all 9 migrations
		var count int
		err = handle.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 9, count, "fresh DB must record all 9 applied migrations")
	})

	t.Run("VersionTablesHaveIndexes", func(t *testing.T) {
		// 发现背景：Issue #275 指出 collection_versions.collection_id 与
		// version_entries.version_id 缺索引，GetVersionLog/GetVersionEntries 退化为全表扫描。
		// 迁移 v8/v9 补上两条 CREATE INDEX IF NOT EXISTS，本用例锁定索引存在。
		dbPath := filepath.Join(t.TempDir(), "indexed.db")
		handle, err := OpenDB(dbPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(handle) })

		// Verify idx_cv_collection_id exists on collection_versions
		var idxCount int
		err = handle.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master
			WHERE type='index' AND name='idx_cv_collection_id' AND tbl_name='collection_versions'
		`).Scan(&idxCount)
		require.NoError(t, err)
		require.Equal(t, 1, idxCount, "idx_cv_collection_id must exist on collection_versions")

		// Verify idx_ve_version_id exists on version_entries
		idxCount = 0
		err = handle.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master
			WHERE type='index' AND name='idx_ve_version_id' AND tbl_name='version_entries'
		`).Scan(&idxCount)
		require.NoError(t, err)
		require.Equal(t, 1, idxCount, "idx_ve_version_id must exist on version_entries")
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
		require.Equal(t, 9, ver)

		var count int
		err = handle2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 9, count)
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
