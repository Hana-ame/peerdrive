// Package repository 提供 SQLite 数据库操作层。
// 使用 mattn/go-sqlite3 驱动。必须先调用 InitDB(dbPath) 初始化全局 DB 连接。
// 表清单：
//   file_meta       — 文件内容元数据（hash PK：size / mime_type / gziped / filename / type）
//   file_providers  — 文件存储位置（hash → provider_type + path，多副本可用）
//   collections     — 注册用户合集（current_hash 指向最新快照）
//   collection_entries  — 合集工作区条目
//   collection_versions — 版本快照记录
//   version_entries     — 版本快照内容
// 注：users / transfer_tasks 表曾由本地 AuthService 与 TaskService 使用
// （2026-08-19 随死代码删除）。已存在数据库中的旧表无读写端，保留无害。

package repository

import (
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
)

// FileType* 常量已上移到 model 包（M2 收层：领域常量归 domain），
// 此处保留别名避免 diff 爆炸，repository 内部引用走常量。新代码直接用 model.FileType*。
const (
	FileTypeBlob           = model.FileTypeBlob
	FileTypeAnonCollection = model.FileTypeAnonCollection
)

var DB *sql.DB

// InitDB 初始化 SQLite 数据库连接并执行全部建表 DDL，包括 file_meta、file_providers、collections 等表。
func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	schema := `
	CREATE TABLE IF NOT EXISTS local_collection_sync (
		collection_hash TEXT PRIMARY KEY,
		local_path TEXT NOT NULL,
		include_filter TEXT,
		exclude_filter TEXT,
		synced_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS local_sync_files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_hash TEXT NOT NULL REFERENCES local_collection_sync(collection_hash) ON DELETE CASCADE,
		file_path TEXT NOT NULL,
		is_saved INTEGER DEFAULT 0,
		last_modified DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(collection_hash, file_path)
	);

	CREATE TABLE IF NOT EXISTS file_meta (
		hash TEXT PRIMARY KEY,
		size INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		mime_type TEXT DEFAULT '',
		gziped INTEGER DEFAULT 0,
		filename TEXT,
		type TEXT DEFAULT '` + FileTypeBlob + `'
	);

	CREATE TABLE IF NOT EXISTS file_providers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT NOT NULL REFERENCES file_meta(hash),
		provider_type TEXT NOT NULL,
		path TEXT NOT NULL,
		available INTEGER DEFAULT 1
	);
	CREATE INDEX IF NOT EXISTS idx_provider_hash ON file_providers(hash);

	CREATE TABLE IF NOT EXISTS collections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		collection_name TEXT NOT NULL,
		current_hash TEXT DEFAULT NULL,
		tags TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(username, collection_name)
	);

	CREATE TABLE IF NOT EXISTS collection_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER NOT NULL,
		path TEXT NOT NULL,
		file_hash TEXT NOT NULL,
		FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE,
		UNIQUE(collection_id, path)
	);

	CREATE TABLE IF NOT EXISTS collection_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER NOT NULL,
		version_number INTEGER NOT NULL,
		commit_message TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		parent_version_id INTEGER,
		FOREIGN KEY (collection_id) REFERENCES collections(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS version_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		path TEXT NOT NULL,
		file_hash TEXT NOT NULL,
		FOREIGN KEY (version_id) REFERENCES collection_versions(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS download_progress (
			hash TEXT PRIMARY KEY,
			total_size INTEGER DEFAULT 0,
			received_size INTEGER DEFAULT 0,
			last_chunk INTEGER DEFAULT 0,
			chunks_total INTEGER DEFAULT 0,
			chunks_done INTEGER DEFAULT 0,
			peers_used TEXT DEFAULT '',
			started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);	`

	if _, err := DB.Exec(schema); err != nil {
		return err
	}

	// 迁移：从旧 files 表迁移到新表。
	// L9：原实现静默忽略所有 ALTER 错误——重复迁移时 duplicate column 是预期
	// 幂等行为，但真实错误（表缺失、磁盘故障）也被吞掉，迁移失败无从排查。
	// 现在统一走 migrationExec：duplicate column 仅记 debug，其他错误记 warn。
	migrationExec(`ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL`)
	migrationExec(`ALTER TABLE collections ADD COLUMN visibility TEXT DEFAULT 'public'`)
	migrationExec(`ALTER TABLE collections ADD COLUMN tags TEXT DEFAULT ''`)
	migrationExec(`ALTER TABLE collections ADD COLUMN follow_redirects INTEGER DEFAULT 1`)
	migrationExec(`ALTER TABLE file_meta ADD COLUMN cid TEXT DEFAULT ''`)
	migrationExec(`ALTER TABLE collection_entries ADD COLUMN providers_json TEXT DEFAULT ''`)
	migrationExec(`ALTER TABLE version_entries ADD COLUMN providers_json TEXT DEFAULT ''`)
	InitShareTable()

	// Migration: create ipfs_pins table for pinned CIDs.
	DB.Exec(`CREATE TABLE IF NOT EXISTS ipfs_pins (
		cid TEXT PRIMARY KEY,
		hash TEXT NOT NULL DEFAULT '',
		size INTEGER DEFAULT 0,
		filename TEXT DEFAULT '',
		pinned_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	// 文件索引表：sha256 → 绝对路径映射 + 同步游标（独立于旧 file_meta）
	createFileIndexTable()
	return nil
}

// migrationExec 执行幂等迁移语句。duplicate column name 是重跑迁移的预期结果，
// 只记 debug；其余错误（表缺失、IO 故障等真实问题）记 warn 留痕（L9）。
func migrationExec(stmt string) {
	if _, err := DB.Exec(stmt); err != nil {
		if strings.Contains(err.Error(), "duplicate column") {
			log.LogDebug("db: migration skipped (already applied): %s", err)
		} else {
			log.LogWarn("db: migration failed: %v (stmt: %s)", err, stmt)
		}
	}
}
