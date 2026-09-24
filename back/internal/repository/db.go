// Package repository 提供 SQLite 数据库操作层。
// 驱动按 cgo 是否可用二选一（见 db_driver_cgo.go / db_driver_pure.go）：
// 有 cgo 用 mattn/go-sqlite3，没有则退回纯 Go 的 modernc.org/sqlite——
// 否则 CGO_ENABLED=0 构建出来的二进制（含全部发布包）会退化成 stub，一启动就挂。
// 必须先调用 InitDB(dbPath) 初始化全局 DB 连接。
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
	"errors"
	"strings"
	"time"

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

// CloseDB 关闭全局连接并把 DB 置空（幂等）。
//
// 为什么要有它：**打开着的库文件在 Windows 上删不掉**。测试用 t.TempDir()
// 建库、结束时 testing 会 RemoveAll 整个目录，只要连接没关就报 "The process
// cannot access the file because it is being used by another process"。
// Linux 上 unlink 一个打开的文件是允许的，所以这条只有 Windows 能暴露
// （2026-09-20 之前 Windows 那格 CI 只 build 不 test，一直没发现）。
// 生产路径（进程退出）不调用它——进程一走句柄自然回收。
func CloseDB() error {
	if DB == nil {
		return nil
	}
	err := DB.Close()
	DB = nil
	return err
}

// Ping 检查元数据库是否可连通（供 /ready 探针使用）。
//
// 为什么包一层而不是把 DB 暴露给 controller：DB 是包级变量，直接放出去等于
// 让上层拿到整个数据库句柄，分层的口子一开就合不上。探针只需要一个是/否。
func Ping() error {
	if DB == nil {
		return errors.New("database not initialized")
	}
	return DB.Ping()
}

// isMemoryDB 判断是否为进程内内存库（":memory:"）。
//
// 为什么单独判：内存库下**每个连接都是一份独立的空库**。一旦连接池开出第二条
// 连接，或者旧连接被回收后重开，前面的表就"消失"了——表现为随机报
// "no such table"。所以内存库必须强制单连接且连接永不过期。这条只对测试
// 路径生效（测试用 :memory: 隔离），但它决定了下面两个开关怎么设。
func isMemoryDB(dbPath string) bool {
	return dbPath == ":memory:" || dbPath == "file::memory:"
}

// dsn 拼出最终连接串：普通文件路径追加驱动特定的 PRAGMA 参数。
//
// 为什么内存库 / 已是 URI（file: 前缀）的不追加：":memory:?_pragma=..." 会被
// 当成一个普通文件名处理，测试直接连到一份文件库，表互相污染且清不掉。
func dsn(dbPath string) string {
	if dbPath == "" || isMemoryDB(dbPath) || strings.HasPrefix(dbPath, "file:") {
		return dbPath
	}
	return dbPath + dsnSuffix()
}

// InitDB 初始化 SQLite 数据库连接并执行全部建表 DDL，包括 file_meta、file_providers、collections 等表。
func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open(sqliteDriver, dsn(dbPath))
	if err != nil {
		return err
	}

	// 连接池：默认（无限制）在 SQLite 上是不可用的——每个写事务都要独占库，
	// 连接越多并发写冲突越频繁（症状是偶发 "database is locked"）。
	// 这里显式收口：内存库 1 条（见 isMemoryDB），文件库 8 条（够并发读，
	// 写冲突交给 busy_timeout 排队而不是直接报错）。
	if isMemoryDB(dbPath) {
		DB.SetMaxOpenConns(1)
		DB.SetMaxIdleConns(1)
		DB.SetConnMaxLifetime(0) // 连接一旦回收，内存库里的表就没了
	} else {
		DB.SetMaxOpenConns(8)
		DB.SetMaxIdleConns(4)
		DB.SetConnMaxLifetime(30 * time.Minute)
	}

	// 先 Ping 再建表：sql.Open 是惰性的（连错了也不报错），真正的失败发生在
	// 第一次 Exec。启动期就把它变成明确的错误，否则运维看到的是"建表失败"
	// 这类把病因藏在别处的报错（路径不可写 / 目录不存在 / 驱动退化成 stub）。
	if err := DB.Ping(); err != nil {
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
