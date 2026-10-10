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

		// PRAGMA user_version must match latest migration version (8)
		ver, err := GetUserVersion(handle)
		require.NoError(t, err)
		require.Equal(t, 8, ver, "fresh DB must have user_version = 8")

		// schema_migrations table must contain all 8 migrations
		var count int
		err = handle.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 8, count, "fresh DB must record all 8 applied migrations")
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
		require.Equal(t, 8, ver)

		var count int
		err = handle2.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 8, count)
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
