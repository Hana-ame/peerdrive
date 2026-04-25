// Package repository 提供 SQLite 数据库操作层。
// 使用 mattn/go-sqlite3 驱动。必须先调用 InitDB(dbPath) 初始化全局 DB 连接。
// 自动建表（CREATE TABLE IF NOT EXISTS），包含六张表：
//   files               — 文件元数据（哈希→位置映射；metadata TEXT 存 JSON 扩展属性）
//   collections         — 集合（用户+名称唯一；current_hash 指向最新快照 hash）
//   collection_entries  — 集合条目（path→hash，基于 collection_id 级联删除）
//   collection_versions — 版本快照记录（带 parent_version_id 版本链）
//   version_entries     — 版本快照内容
//   transfer_tasks      — 异步任务跟踪
//
// Metadata 格式示例：
//   {"is_gzip": true, "mime_type": "application/gzip"}
// 所有文件级扩展属性全部放入 metadata JSON，不新增专用列。

package repository

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
)

// 文件类型常量（与 model 包保持一致）
const (
	FileTypeBlob           = "blob"
	FileTypeAnonCollection = "anon_collection"
)

var DB *sql.DB

func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	schema := `
	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT NOT NULL,
		provider_type TEXT NOT NULL,
		path TEXT NOT NULL,
		filename TEXT,
		metadata TEXT DEFAULT '{}',
		type TEXT DEFAULT '` + FileTypeBlob + `',
		available INTEGER DEFAULT 1
	);
	CREATE INDEX IF NOT EXISTS idx_hash ON files(hash);

	CREATE TABLE IF NOT EXISTS collections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL,
		collection_name TEXT NOT NULL,
		current_hash TEXT DEFAULT NULL,
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

	CREATE TABLE IF NOT EXISTS transfer_tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		params TEXT DEFAULT '',
		result TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := DB.Exec(schema); err != nil {
		return err
	}
	// 迁移：为旧数据库添加缺失列（已有则忽略）
	DB.Exec(`ALTER TABLE files ADD COLUMN metadata TEXT DEFAULT '{}'`)
	DB.Exec(`ALTER TABLE files ADD COLUMN type TEXT DEFAULT '` + FileTypeBlob + `'`)
	DB.Exec(`ALTER TABLE files ADD COLUMN available INTEGER DEFAULT 1`)
	DB.Exec(`ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL`)
	return nil
}
