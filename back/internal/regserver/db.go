package regserver

// db.go — SQLite 打开与 schema。
//
// 数据兼容是「搬进主仓」的前提（见 README「迁移兼容性」）：
//   - CREATE TABLE IF NOT EXISTS 不动已有表，旧数据直接复用。
//   - 表名、列名、bcrypt cost 10 一律与原独立仓一致。
//   - 两个表 users / relay_nodes 分别对应 auth 与 relay 两条业务线，
//     拆到 db.go 是因为它们共享同一个 *sql.DB（Server.db），不分开。
//
// 驱动注册在 driver_cgo.go / driver_pure.go（按 cgo 切分，与
// internal/repository/db_driver_*.go 口径一致）。本文件只用 sql.Open(sqliteDriver, ...)
// 不硬编码驱动名。
//
// 注：v0.2.0 前本包有包级 `var db *sql.DB`，2026-10-05 并入主仓时改成
// Server.db 实例字段（同进程多实例的可行性要求）。那行包级声明已是死代码，
// 本文件不再保留它——若架构再次引入全局 db，需一并评估「两个实例互相覆盖」
// 的回归风险。

import "database/sql"

// openDB 打开（必要时创建）库并建表。
//
// 为什么 CREATE TABLE 和 sql.Open 放同一个函数：schema 是 Server 的生命周期
// 一部分，分散会让「库没建好表就返回 Server」成为可能。New 调用本函数，
// 失败即整体失败。
//
// 注意变量名：循环变量不能叫 s，否则遮住 receiver，末尾的 s.db = d 会编译
// 不过或写到错误的接收者上。
func (s *Server) openDB(path string) error {
	d, err := sql.Open(sqliteDriver, path+dsnSuffix())
	if err != nil {
		return err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS relay_nodes (
			peer_id TEXT PRIMARY KEY,
			addrs TEXT NOT NULL,
			storage_mb INTEGER DEFAULT 0,
			load_pct REAL DEFAULT 0,
			version TEXT DEFAULT '',
			registered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	for _, stmt := range stmts {
		if _, err := d.Exec(stmt); err != nil {
			d.Close()
			return err
		}
	}
	s.db = d
	return nil
}
