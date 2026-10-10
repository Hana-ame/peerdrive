// Package repository provides the SQLite database operation layer.
// Driver selection is based on cgo availability (see db_driver_cgo.go / db_driver_pure.go):
// with cgo use mattn/go-sqlite3, without use pure-Go modernc.org/sqlite —
// otherwise binaries built with CGO_ENABLED=0 (including all release packages) degrade to a stub and crash on start.
// InitDB(dbPath) must be called first to initialize the global DB connection.
// Table list:
//   file_meta       — file content metadata (hash PK: size / mime_type / gziped / filename / type)
//   file_providers  — file storage locations (hash → provider_type + path, multiple replicas allowed)
//   collections     — registered user collections (current_hash points to latest snapshot)
//   collection_entries  — collection workspace entries
//   collection_versions — version snapshot records
//   version_entries     — version snapshot content
// Note: users / transfer_tasks tables were previously used by local AuthService
// and TaskService (deleted as dead code on 2026-08-19). Old tables in existing
// databases have no read/write endpoints; retained harmlessly.

package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
)

// FileType* constants have been moved to the model package (M2 layering: domain
// constants belong to domain); aliases retained here to avoid diff explosion,
// repository internal references use constants. New code should use model.FileType* directly.
const (
	FileTypeBlob           = model.FileTypeBlob
	FileTypeAnonCollection = model.FileTypeAnonCollection
)

// db is the package-level SQLite database handle, private to this package.
// External callers should use GetDB(), OpenDB(), or SetDB() instead of accessing package globals directly.
var db *sql.DB

// GetDB returns the active database connection.
func GetDB() *sql.DB {
	return db
}

// SetDB sets the active database connection (used for testing or explicit dependency injection).
func SetDB(d *sql.DB) {
	db = d
}

// CloseDB closes the global connection and nils out db (idempotent).
//
// Why it exists: **open database files cannot be deleted on Windows**. Tests use
// t.TempDir() to create databases; at the end testing calls RemoveAll on the whole
// directory. If the connection isn't closed, it reports "The process cannot access
// the file because it is being used by another process".
// On Linux, unlinking an open file is allowed, so this only surfaces on Windows
// (before 2026-09-20, Windows CI only built, didn't test, so it was never found).
// Production path (process exit) does not call it — the process goes away and the
// handle is naturally reclaimed.
func CloseDB() error {
	if db == nil {
		return nil
	}
	err := db.Close()
	db = nil
	return err
}

// CloseHandle closes the provided database handle safely.
func CloseHandle(d *sql.DB) error {
	if d == nil {
		return nil
	}
	return d.Close()
}

// Ping checks whether the metadata database is reachable (used by /ready probe).
//
// Why wrap instead of exposing DB to controller: db is a package-level variable;
// passing it out equals giving upper layers the entire database handle. Once the
// layering boundary is breached, it's impossible to close it again. The probe only
// needs a yes/no.
func Ping() error {
	if db == nil {
		return errors.New("database not initialized")
	}
	return db.Ping()
}

// isMemoryDB checks if this is an in-process in-memory database (":memory:").
//
// Why check separately: in-memory DBs have **a separate empty database per connection**.
// Once the connection pool opens a second connection, or an old connection is recycled
// and reopened, the previous tables "vanish" — manifesting as random "no such table"
// errors. So in-memory DBs must enforce a single connection that never expires. This
// only affects the test path (tests use :memory: for isolation), but it determines
// how the two switches below are set.
func isMemoryDB(dbPath string) bool {
	return dbPath == ":memory:" || dbPath == "file::memory:"
}

// dsn assembles the final connection string: appends driver-specific PRAGMA parameters
// to normal file paths.
//
// Why not append for in-memory DBs / existing URIs (file: prefix): ":memory:?_pragma=..."
// would be treated as a normal filename, tests would connect to a file-based DB, tables
// would cross-contaminate and can't be cleaned up.
func dsn(dbPath string) string {
	if dbPath == "" || isMemoryDB(dbPath) || strings.HasPrefix(dbPath, "file:") {
		return dbPath
	}
	return dbPath + dsnSuffix()
}

// OpenDB opens and initializes a SQLite database at dbPath, creating all schema tables,
// and returns the isolated *sql.DB handle without mutating package-level state.
func OpenDB(dbPath string) (*sql.DB, error) {
	targetDB, err := sql.Open(sqliteDriver, dsn(dbPath))
	if err != nil {
		return nil, err
	}

	// Connection pool: the default (unlimited) is unusable with SQLite — each write
	// transaction needs exclusive database access, and more connections mean more
	// frequent concurrent write conflicts (symptom: sporadic "database is locked").
	// Explicitly constrained here: 1 connection for in-memory DBs (see isMemoryDB),
	// 8 for file DBs (enough for concurrent reads; write conflicts are queued by
	// busy_timeout instead of erroring immediately).
	if isMemoryDB(dbPath) {
		targetDB.SetMaxOpenConns(1)
		targetDB.SetMaxIdleConns(1)
		targetDB.SetConnMaxLifetime(0) // once a connection is recycled, in-memory DB tables are gone
	} else {
		targetDB.SetMaxOpenConns(8)
		targetDB.SetMaxIdleConns(4)
		targetDB.SetConnMaxLifetime(30 * time.Minute)
	}

	// Ping before table creation: sql.Open is lazy (doesn't error on wrong connection),
	// real failure happens at first Exec. Make it an explicit error at startup,
	// otherwise ops sees "table creation failed" — a symptom that hides the root cause
	// elsewhere (unwritable path / nonexistent directory / driver degraded to stub).
	if err := targetDB.Ping(); err != nil {
		_ = targetDB.Close()
		return nil, err
	}

	if err := initTables(targetDB); err != nil {
		_ = targetDB.Close()
		return nil, err
	}

	// Issue #277: Enable WAL mode on file databases to eliminate reader/writer blocking.
	if !isMemoryDB(dbPath) {
		var mode string
		if err := targetDB.QueryRow("PRAGMA journal_mode = WAL;").Scan(&mode); err != nil {
			log.LogWarn("db: PRAGMA journal_mode=WAL query: %v", err)
		}
	}

	return targetDB, nil
}

// InitDB initializes the SQLite database connection, executes all table-creation DDL,
// and sets the package-level default handle.
func InitDB(dbPath string) error {
	d, err := OpenDB(dbPath)
	if err != nil {
		return err
	}
	db = d
	return nil
}

func initTables(targetDB *sql.DB) error {
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

	if _, err := targetDB.Exec(schema); err != nil {
		return err
	}

	// Migration tracking and execution (Issue #151):
	// Tables are tracked in schema_migrations table and PRAGMA user_version.
	// Migration errors are propagated so partial/corrupt states fail fast instead of being silently swallowed.
	if err := applyMigrations(targetDB); err != nil {
		return err
	}
	InitShareTableOn(targetDB)

	// Migration: create ipfs_pins table for pinned CIDs.
	if _, err := targetDB.Exec(`CREATE TABLE IF NOT EXISTS ipfs_pins (
		cid TEXT PRIMARY KEY,
		hash TEXT NOT NULL DEFAULT '',
		size INTEGER DEFAULT 0,
		filename TEXT DEFAULT '',
		pinned_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return err
	}
	// File index table: sha256 → absolute path mapping + sync cursor (independent of old file_meta)
	createFileIndexTableOn(targetDB)
	// SHA tags table: sha256 → tag mapping (Issue #91)
	createShaTagsTableOn(targetDB)
	return nil
}

type migration struct {
	version int
	name    string
	stmt    string
}

// schemaMigrations tracks table modifications in sequential order.
var schemaMigrations = []migration{
	{version: 1, name: "collections_add_current_hash", stmt: `ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL`},
	{version: 2, name: "collections_add_visibility", stmt: `ALTER TABLE collections ADD COLUMN visibility TEXT DEFAULT 'public'`},
	{version: 3, name: "collections_add_tags", stmt: `ALTER TABLE collections ADD COLUMN tags TEXT DEFAULT ''`},
	{version: 4, name: "collections_add_follow_redirects", stmt: `ALTER TABLE collections ADD COLUMN follow_redirects INTEGER DEFAULT 1`},
	{version: 5, name: "file_meta_add_cid", stmt: `ALTER TABLE file_meta ADD COLUMN cid TEXT DEFAULT ''`},
	{version: 6, name: "collection_entries_add_providers_json", stmt: `ALTER TABLE collection_entries ADD COLUMN providers_json TEXT DEFAULT ''`},
	{version: 7, name: "version_entries_add_providers_json", stmt: `ALTER TABLE version_entries ADD COLUMN providers_json TEXT DEFAULT ''`},
	{version: 8, name: "version_tables_add_indexes", stmt: `CREATE INDEX IF NOT EXISTS idx_cv_collection_id ON collection_versions(collection_id)`},
	{version: 9, name: "version_entries_add_index", stmt: `CREATE INDEX IF NOT EXISTS idx_ve_version_id ON version_entries(version_id)`},
}

// applyMigrations applies unapplied schema migrations and updates PRAGMA user_version.
// 发现背景：Issue #151 指出原先 8 条 ALTER 依赖字符串匹配吞掉错误，缺乏版本记录。
func applyMigrations(targetDB *sql.DB) error {
	if _, err := targetDB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	applied := make(map[int]bool)
	rows, err := targetDB.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("query applied schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return err
		}
		applied[v] = true
	}

	latestVersion := 0
	for _, m := range schemaMigrations {
		if m.version > latestVersion {
			latestVersion = m.version
		}
		if applied[m.version] {
			continue
		}

		if _, err := targetDB.Exec(m.stmt); err != nil {
			// For existing legacy databases where columns were already added prior to schema_migrations table:
			if strings.Contains(err.Error(), "duplicate column") {
				log.LogDebug("db: migration %d (%s) skipped (duplicate column in legacy db): %s", m.version, m.name, err)
			} else {
				return fmt.Errorf("db: migration %d (%s) failed: %w (stmt: %s)", m.version, m.name, err, m.stmt)
			}
		}

		if _, err := targetDB.Exec(`INSERT OR IGNORE INTO schema_migrations (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
			return fmt.Errorf("record migration %d (%s): %w", m.version, m.name, err)
		}
	}

	if _, err := targetDB.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, latestVersion)); err != nil {
		return fmt.Errorf("update pragma user_version: %w", err)
	}

	return nil
}

// GetUserVersion queries SQLite PRAGMA user_version for the database.
func GetUserVersion(targetDB *sql.DB) (int, error) {
	var version int
	if err := targetDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// migrationExec executes a single migration statement with error reporting.
func migrationExec(targetDB *sql.DB, stmt string) error {
	_, err := targetDB.Exec(stmt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate column") {
			log.LogDebug("db: migration skipped (already applied): %s", err)
			return nil
		}
		return fmt.Errorf("migration failed: %w (stmt: %s)", err, stmt)
	}
	return nil
}
