package repository

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDBMultiInstanceIsolation verifies that multiple DB instances initialized via OpenDB
// operate in total isolation without contaminating each other or package-level state.
// 发现背景：Issue #139 与 #143 指出 repository.DB 原先为包级全局变量，多实例/并发测试
// 会相互覆盖句柄。引入 OpenDB 并在测试中验证多实例隔离。
func TestDBMultiInstanceIsolation(t *testing.T) {
	t.Run("TwoInstancesReadWriteIsolation", func(t *testing.T) {
		t.Parallel()

		dirA := t.TempDir()
		dirB := t.TempDir()

		dbA, err := OpenDB(filepath.Join(dirA, "instance_a.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(dbA) })

		dbB, err := OpenDB(filepath.Join(dirB, "instance_b.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(dbB) })

		// Instance A writes record X
		_, err = dbA.Exec(`INSERT INTO file_meta (hash, filename, size, mime_type) VALUES (?, ?, ?, ?)`,
			"hash_instance_a_only", "file_a.txt", 100, "text/plain")
		require.NoError(t, err)

		// Instance B writes record Y
		_, err = dbB.Exec(`INSERT INTO file_meta (hash, filename, size, mime_type) VALUES (?, ?, ?, ?)`,
			"hash_instance_b_only", "file_b.txt", 200, "text/plain")
		require.NoError(t, err)

		// Assert A sees record X and cannot see record Y
		var nameA string
		err = dbA.QueryRow(`SELECT filename FROM file_meta WHERE hash = ?`, "hash_instance_a_only").Scan(&nameA)
		require.NoError(t, err)
		require.Equal(t, "file_a.txt", nameA)

		err = dbA.QueryRow(`SELECT filename FROM file_meta WHERE hash = ?`, "hash_instance_b_only").Scan(&nameA)
		require.ErrorIs(t, err, sql.ErrNoRows, "instance A must not observe writes from instance B")

		// Assert B sees record Y and cannot see record X
		var nameB string
		err = dbB.QueryRow(`SELECT filename FROM file_meta WHERE hash = ?`, "hash_instance_b_only").Scan(&nameB)
		require.NoError(t, err)
		require.Equal(t, "file_b.txt", nameB)

		err = dbB.QueryRow(`SELECT filename FROM file_meta WHERE hash = ?`, "hash_instance_a_only").Scan(&nameB)
		require.ErrorIs(t, err, sql.ErrNoRows, "instance B must not observe writes from instance A")
	})

	t.Run("CloseInstanceANotAffectingInstanceB", func(t *testing.T) {
		t.Parallel()

		dirA := t.TempDir()
		dirB := t.TempDir()

		dbA, err := OpenDB(filepath.Join(dirA, "close_a.db"))
		require.NoError(t, err)
		dbB, err := OpenDB(filepath.Join(dirB, "close_b.db"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = CloseHandle(dbB) })

		_, err = dbB.Exec(`INSERT INTO file_meta (hash, filename, size) VALUES (?, ?, ?)`, "b_alive_hash", "b.txt", 10)
		require.NoError(t, err)

		// Close A explicitly
		require.NoError(t, CloseHandle(dbA))

		// B is still alive, healthy, and queryable
		require.NoError(t, dbB.Ping(), "dbB must stay reachable after dbA is closed")

		var count int
		err = dbB.QueryRow(`SELECT COUNT(*) FROM file_meta WHERE hash = ?`, "b_alive_hash").Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count)
	})

	t.Run("ConcurrentIndependentInstances", func(t *testing.T) {
		t.Parallel()

		const concurrency = 8
		var wg sync.WaitGroup

		for i := 0; i < concurrency; i++ {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				dir := t.TempDir()
				handle, err := OpenDB(filepath.Join(dir, fmt.Sprintf("concurrent_%d.db", i)))
				if err != nil {
					t.Errorf("worker %d: OpenDB failed: %v", i, err)
					return
				}
				defer func() { _ = CloseHandle(handle) }()

				hashVal := fmt.Sprintf("sha_%d", i)
				_, err = handle.Exec(`INSERT INTO file_index (hash, path, name, size, seq) VALUES (?, ?, ?, ?, ?)`,
					hashVal, fmt.Sprintf("/path/%d", i), fmt.Sprintf("name_%d", i), 1024, i+1)
				if err != nil {
					t.Errorf("worker %d: insert failed: %v", i, err)
					return
				}

				var rowCount int
				if err := handle.QueryRow(`SELECT COUNT(*) FROM file_index`).Scan(&rowCount); err != nil {
					t.Errorf("worker %d: count failed: %v", i, err)
					return
				}
				if rowCount != 1 {
					t.Errorf("worker %d expected 1 row, got %d", i, rowCount)
				}
			}()
		}

		wg.Wait()
	})
}
