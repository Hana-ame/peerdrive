// Package repository 提供 SQLite 数据库操作层。
// 使用 mattn/go-sqlite3 驱动。必须先调用 InitDB(dbPath) 初始化全局 DB 连接。
// 七张表：
//   file_meta       — 文件内容元数据（hash PK：size / mime_type / gziped / filename / type）
//   file_providers  — 文件存储位置（hash → provider_type + path，多副本可用）
//   collections     — 注册用户合集（current_hash 指向最新快照）
//   collection_entries  — 合集工作区条目
//   collection_versions — 版本快照记录
//   version_entries     — 版本快照内容
//   transfer_tasks      — 异步任务跟踪

package repository

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
)

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

	// 迁移：从旧 files 表迁移到新表（忽略错误）
	DB.Exec(`ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL`)
	return nil
}
