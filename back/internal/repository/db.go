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

var DB *sql.DB

// CloseDB closes the global connection and nils out DB (idempotent).
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
	if DB == nil {
		return nil
	}
	err := DB.Close()
	DB = nil
	return err
}

// Ping checks whether the metadata database is reachable (used by /ready probe).
//
// Why wrap instead of exposing DB to controller: DB is a package-level variable;
// passing it out equals giving upper layers the entire database handle. Once the
// layering boundary is breached, it's impossible to close it again. The probe only
// needs a yes/no.
func Ping() error {
	if DB == nil {
		return errors.New("database not initialized")
	}
	return DB.Ping()
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

// InitDB initializes the SQLite database connection and executes all table-creation DDL,
// including file_meta, file_providers, collections, and other tables.
func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open(sqliteDriver, dsn(dbPath))
	if err != nil {
		return err
	}

	// Connection pool: the default (unlimited) is unusable with SQLite — each write
	// transaction needs exclusive database access, and more connections mean more
	// frequent concurrent write conflicts (symptom: sporadic "database is locked").
	// Explicitly constrained here: 1 connection for in-memory DBs (see isMemoryDB),
	// 8 for file DBs (enough for concurrent reads; write conflicts are queued by
	// busy_timeout instead of erroring immediately).
	if isMemoryDB(dbPath) {
		DB.SetMaxOpenConns(1)
		DB.SetMaxIdleConns(1)
		DB.SetConnMaxLifetime(0) // once a connection is recycled, in-memory DB tables are gone
	} else {
		DB.SetMaxOpenConns(8)
		DB.SetMaxIdleConns(4)
		DB.SetConnMaxLifetime(30 * time.Minute)
	}

	// Ping before table creation: sql.Open is lazy (doesn't error on wrong connection),
	// real failure happens at first Exec. Make it an explicit error at startup,
	// otherwise ops sees "table creation failed" — a symptom that hides the root cause
	// elsewhere (unwritable path / nonexistent directory / driver degraded to stub).
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

	// Migration: migrate from old files table to new tables.
	// L9: original implementation silently ignored all ALTER errors — duplicate column
	// on repeated migration is expected idempotent behavior, but real errors (missing
	// table, disk failure) were also swallowed, making failed migrations untraceable.
	// Now all go through migrationExec: duplicate column logs debug only, other errors
	// log warn for traceability.
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
	// File index table: sha256 → absolute path mapping + sync cursor (independent of old file_meta)
	createFileIndexTable()
	// SHA tags table: sha256 → tag mapping (Issue #91)
	createShaTagsTable()
	return nil
}

// migrationExec executes idempotent migration statements. Duplicate column name
// is the expected result of re-running migrations — only logs debug. Other errors
// (missing table, I/O failure, etc.) log warn for traceability (L9).
func migrationExec(stmt string) {
	if _, err := DB.Exec(stmt); err != nil {
		if strings.Contains(err.Error(), "duplicate column") {
			log.LogDebug("db: migration skipped (already applied): %s", err)
		} else {
			log.LogWarn("db: migration failed: %v (stmt: %s)", err, stmt)
		}
	}
}
