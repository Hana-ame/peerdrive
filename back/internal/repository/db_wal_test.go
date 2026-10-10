package repository

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJournalModeWAL verifies that OpenDB enables WAL journal mode on
// file-backed databases and that synchronous is NORMAL (SQLite's recommended
// companion for WAL).
//
// 发现背景：Issue #277 指出 SQLite 默认 DELETE journal mode 在同一时刻只允许
// 一个写操作，peerdrive 的多节点访问 / 频繁上传下载同步场景锁竞争是瓶颈。WAL
// 让读写不再互相阻塞，写吞吐显著提升。本测试锁定 WAL 已生效，防止后续误改回
// DELETE（journal_mode 是持久化在 DB 文件里的，一旦回退到 DELETE 会让已经
// 在 WAL 侧生成的 -wal 侧车文件变成孤立文件）。
func TestJournalModeWAL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal.db")
	handle, err := OpenDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseHandle(handle) })

	var mode string
	require.NoError(t, handle.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode, "file-backed DB must be opened in WAL mode (issue #277)")
}

// TestSynchronousNormalOnWAL verifies synchronous=NORMAL is applied per
// connection. synchronous is NOT persistent (unlike journal_mode), so it must
// live in the DSN suffix — this test pins that behavior.
//
// 发现背景：同上（Issue #277）。SQLite 官方文档明确建议 WAL 搭配
// synchronous=NORMAL：FULL 只换来 ~50% 写吞吐的下降，而 WAL 在 NORMAL 下
// 已经不会因为断电丢失提交（WAL 自身的 fsync 保证了这一点）。锁定 NORMAL
// （数值 1）而非 FULL （2），避免后续被"更安全"的名义回退。
func TestSynchronousNormalOnWAL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sync.db")
	handle, err := OpenDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseHandle(handle) })

	var syncMode int
	require.NoError(t, handle.QueryRow(`PRAGMA synchronous`).Scan(&syncMode))
	require.Equal(t, 1, syncMode, "synchronous must be NORMAL (1) when WAL is enabled (issue #277)")
}

// TestJournalModePersistedAcrossOpen verifies WAL survives across process
// restarts (re-opening the same file-backed DB).
//
// 发现背景：journal_mode=WAL 是持久化在 DB 文件头部的，所以即使我们没在 DSN
// 里写 _journal_mode=WAL，重开也应该保持 WAL。本测试同时验证两件事：
//   ① DSN 后缀确实设了 WAL（首次 open 就生效）；
//   ② WAL 在关闭后依然持久（第二次 open 依然是 wal）。
// 如果未来有人把 DSN 后缀删掉，这个测试在第二次 open 仍然应该通过——
// 这正是"持久化"的语义，也是为什么我们把 journal_mode 放在 DSN 里当作
// "belt and suspenders" 而不是唯一机制。
func TestJournalModePersistedAcrossOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "persist.db")

	handle1, err := OpenDB(dbPath)
	require.NoError(t, err)
	var mode1 string
	require.NoError(t, handle1.QueryRow(`PRAGMA journal_mode`).Scan(&mode1))
	require.Equal(t, "wal", mode1)
	require.NoError(t, CloseHandle(handle1))

	handle2, err := OpenDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseHandle(handle2) })
	var mode2 string
	require.NoError(t, handle2.QueryRow(`PRAGMA journal_mode`).Scan(&mode2))
	require.Equal(t, "wal", mode2, "WAL must persist across process restarts")
}

// TestMemoryDBNotForcedWAL verifies in-memory databases are NOT forced into
// WAL mode (the DSN suffix is skipped for :memory: per dsn()'s guard).
//
// 发现背景：:memory: 数据库是"每连接一个独立空库"，WAL 的 -wal / -shm 侧车
// 文件语义对它无意义（SQLite 内部给 :memory: 用的是 memory journal，不是
// WAL）。dsn() 对 :memory: 跳过 DSN 后缀，故这里断言"不崩溃 + 未误开 WAL"。
// 如果未来有人改坏了 dsn() 的 isMemoryDB 分支，本测试会让所有内存 DB 测试
// 一起爆炸——是一个早预警。
func TestMemoryDBNotForcedWAL(t *testing.T) {
	handle, err := OpenDB(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseHandle(handle) })

	var mode string
	require.NoError(t, handle.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	// SQLite 给 :memory: 报告的默认是 "memory"（不是 "wal"），确认没被误开。
	require.NotEqual(t, "wal", mode, ":memory: DB must NOT be forced into WAL mode")
}

// TestLegacyDeleteDBMigratedToWAL verifies that an existing DB file created
// under the legacy DELETE journal mode is migrated to WAL on the next OpenDB.
//
// 发现背景：升级 peerdrive 时，已部署节点上的 peerdrive.db 仍处 DELETE 模式。
// 如果只在"新建 DB"路径上设 WAL，老库会被永久困在 DELETE 模式——这是本次
// 改动最容易漏掉的生产路径。DSN 后缀在每次连接打开时都会执行
// PRAGMA journal_mode=WAL，SQLite 会原地把 DELETE 转成 WAL（需要短暂写锁，
// busy_timeout=5000 兜底）。本测试模拟"先用无后缀驱动开一个 DELETE 库，再
// 用 OpenDB 重开"的升级场景。
func TestLegacyDeleteDBMigratedToWAL(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	// 用原生驱动开一个 DELETE 模式的库（绕过 dsn()，直接给驱动原始路径）。
	legacy, err := sql.Open(sqliteDriver, dbPath)
	require.NoError(t, err)
	_, err = legacy.Exec(`CREATE TABLE legacy_probe (id INTEGER PRIMARY KEY)`)
	require.NoError(t, err)
	var mode string
	require.NoError(t, legacy.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "delete", mode, "sanity: legacy DB must start in DELETE mode")
	require.NoError(t, legacy.Close())

	// 用 OpenDB 重开同一文件：DSN 后缀里的 _journal_mode=WAL 触发原地迁移。
	handle, err := OpenDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseHandle(handle) })

	require.NoError(t, handle.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", mode, "legacy DELETE-mode DB must be migrated to WAL on OpenDB")

	// 迁移不能破坏已有数据，也不能破坏写入路径。
	_, err = handle.Exec(`INSERT INTO legacy_probe (id) VALUES (42)`)
	require.NoError(t, err)
	var id int
	require.NoError(t, handle.QueryRow(`SELECT id FROM legacy_probe WHERE id=42`).Scan(&id))
	require.Equal(t, 42, id)
}
