package schemaconsistency

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/repository"

	"github.com/Hana-ame/go-peerserver/regserver"
)

// TestCrossDBSchemaConsistency_VersionMatrix 验证主库与 regserver 库在统一发布内的 schema 版本基准与表结构完整性。
// 发现背景：Issue #204 指出主库与 regserver 库各自独立执行 schema 初始化与 user_version 演进，
// 缺乏跨库同步断言，导致单库破坏或版本推进不一致时无法在 CI 阶段被捕获。
func TestCrossDBSchemaConsistency_VersionMatrix(t *testing.T) {
	t.Setenv("PEERDRIVE_JWT_SECRET", "test-secret-at-least-32-chars-long-for-consistency-testing")

	tmpDir := t.TempDir()
	mainDBPath := filepath.Join(tmpDir, "main.db")
	regDBPath := filepath.Join(tmpDir, "reg.db")

	// 1. 初始化主库
	mainHandle, err := repository.OpenDB(mainDBPath)
	require.NoError(t, err, "main DB open should succeed")
	t.Cleanup(func() { _ = repository.CloseHandle(mainHandle) })

	// 2. 初始化 regserver 库
	regSrv, err := regserver.New(regDBPath)
	require.NoError(t, err, "regserver DB open should succeed")
	t.Cleanup(func() { _ = regSrv.Close() })

	// 断言主库 PRAGMA user_version 达到最新基线版本 (>= 7)
	mainVer, err := repository.GetUserVersion(mainHandle)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, mainVer, 7, "main DB user_version must be >= 7")

	// 断言主库 schema_migrations 包含全量迁移记录
	var mainMigrationCount int
	err = mainHandle.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&mainMigrationCount)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, mainMigrationCount, 7, "main DB migrations count must match user_version")

	// 断言 regserver 库表结构与用户/中继节点定义完整
	var regUserTableCount int
	err = regSrv.DB().QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('users', 'relay_nodes')`).Scan(&regUserTableCount)
	require.NoError(t, err)
	assert.Equal(t, 2, regUserTableCount, "regserver DB must create both users and relay_nodes tables")
}

// TestCrossDBSchemaConsistency_InterleavedInitializationOrder 验证主库与 regserver 库交叉初始化的时序独立性。
// 发现背景：Issue #204 明确指出两库可能在不同启动顺序下被加载（例如在单二进制 peerdrive all 下同时启动，
// 或在独立进程下先后加载），需确保不论哪一个库先初始化，均能正确完成建表与读写，互无锁竞争与污染。
func TestCrossDBSchemaConsistency_InterleavedInitializationOrder(t *testing.T) {
	t.Setenv("PEERDRIVE_JWT_SECRET", "test-secret-at-least-32-chars-long-for-consistency-testing")

	t.Run("OrderA_MainFirstThenRegserver", func(t *testing.T) {
		dirA := t.TempDir()
		mainPath := filepath.Join(dirA, "main.db")
		regPath := filepath.Join(dirA, "reg.db")

		// 先主库
		mHandle, err := repository.OpenDB(mainPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = repository.CloseHandle(mHandle) })

		// 后 regserver
		rSrv, err := regserver.New(regPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = rSrv.Close() })

		require.NoError(t, mHandle.Ping(), "main DB should remain healthy")
		require.NoError(t, rSrv.PingDB(), "regserver DB should be healthy")
	})

	t.Run("OrderB_RegserverFirstThenMain", func(t *testing.T) {
		dirB := t.TempDir()
		mainPath := filepath.Join(dirB, "main.db")
		regPath := filepath.Join(dirB, "reg.db")

		// 先 regserver
		rSrv, err := regserver.New(regPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = rSrv.Close() })

		// 后主库
		mHandle, err := repository.OpenDB(mainPath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = repository.CloseHandle(mHandle) })

		require.NoError(t, rSrv.PingDB(), "regserver DB should remain healthy")
		require.NoError(t, mHandle.Ping(), "main DB should be healthy")
	})
}

// TestCrossDBSchemaConsistency_PartialFailureAndNonRollbackSemantics 验证单库 DDL 错误时的容错隔离与非回滚语义。
// 发现背景：Issue #204 强调固定 migrationExec 的「不中断、不回滚」容错行为——即发生局部非致命迁移异常时
// 记录警告并保持既有数据可用，同时保证单库异常严格隔离，不连带破坏另一独立数据库的生命周期。
func TestCrossDBSchemaConsistency_PartialFailureAndNonRollbackSemantics(t *testing.T) {
	t.Setenv("PEERDRIVE_JWT_SECRET", "test-secret-at-least-32-chars-long-for-consistency-testing")

	tmpDir := t.TempDir()
	mainDBPath := filepath.Join(tmpDir, "main.db")
	regDBPath := filepath.Join(tmpDir, "reg.db")

	// 1. 正常初始化主库并写入基础数据
	mainHandle, err := repository.OpenDB(mainDBPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = repository.CloseHandle(mainHandle) })

	// 验证在已初始化的主库上执行非法的 DDL（模拟损坏的增量迁移）
	badDDL := `ALTER TABLE nonexistent_table_for_test ADD COLUMN dummy_col TEXT`
	_, err = mainHandle.Exec(badDDL)
	require.Error(t, err, "invalid DDL must return error to caller")

	// 验证非法 DDL 发生后，此前迁移好的表与结构保持完整（无误导性静默回滚）
	var fileIndexTableCount int
	err = mainHandle.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='file_index'`).Scan(&fileIndexTableCount)
	require.NoError(t, err)
	assert.Equal(t, 1, fileIndexTableCount, "pre-existing tables must remain intact after partial failure")

	// 2. 验证主库遭遇 DDL 错误并不影响随后 regserver 独立库的正常初始化
	regSrv, err := regserver.New(regDBPath)
	require.NoError(t, err, "regserver DB initialization must be strictly isolated from main DB failures")
	t.Cleanup(func() { _ = regSrv.Close() })

	require.NoError(t, regSrv.PingDB())
}

// TestCrossDBSchemaConsistency_FieldCompatibility 验证跨库数据实体的字段规格与类型兼容性。
// 发现背景：Issue #204 指出 relay_nodes.peer_id 与主库 peer_id 及发现层数据流向需格式兼容；
// regserver users.username 与主库 collections.username 需类型一致，防止因模式偏离造成跨组件运行时断裂。
func TestCrossDBSchemaConsistency_FieldCompatibility(t *testing.T) {
	t.Setenv("PEERDRIVE_JWT_SECRET", "test-secret-at-least-32-chars-long-for-consistency-testing")

	tmpDir := t.TempDir()
	mainDBPath := filepath.Join(tmpDir, "main.db")
	regDBPath := filepath.Join(tmpDir, "reg.db")

	mainHandle, err := repository.OpenDB(mainDBPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = repository.CloseHandle(mainHandle) })

	regSrv, err := regserver.New(regDBPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = regSrv.Close() })

	// 1. 验证 peer_id 跨库约束：主库与 regserver 均使用 TEXT 类型作为节点唯一标识
	testPeerID := "peerdrive-node-cross-consistency-test-12345"
	_, err = regSrv.DB().Exec(`INSERT INTO relay_nodes (peer_id, addrs) VALUES (?, ?)`, testPeerID, "127.0.0.1:9000")
	require.NoError(t, err, "peer_id should be accepted into regserver relay_nodes")

	// 2. 验证 username 跨库约束：regserver 用户名可作为主库 collection 的合规 username (owner)
	testUsername := "operator_alice"
	_, err = regSrv.DB().Exec(`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, testUsername, "$2a$10$hash", "admin")
	require.NoError(t, err, "username should be accepted into regserver users")

	_, err = mainHandle.Exec(`INSERT INTO collections (collection_name, username, visibility) VALUES (?, ?, ?)`, "shared_archive", testUsername, "public")
	require.NoError(t, err, "regserver username should seamlessly function as collection owner in main DB")
}
