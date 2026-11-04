package metadata

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	// Registers the pure-Go SQLite driver under the name "sqlite".
	_ "modernc.org/sqlite"

	"github.com/Hana-ame/peerdrive/h2f/interface"
)

// EnvDatabase names the environment variable that overrides the location of
// the default store used by the package-level Stat/Index/List.
const EnvDatabase = "PEERDRIVE_METADATA_DB"

// 错误的本体在 interface 包，这里只是别名：只依赖 metadata 的调用方不必再引
// 一个包，判断方式不变（errors.Is 认的是同一个值）。
var (
	ErrNotFound        = iface.ErrNotFound
	ErrInvalidHash     = iface.ErrInvalidHash
	ErrInvalidPath     = iface.ErrInvalidPath
	ErrUnsupportedType = iface.ErrUnsupportedType
)

// schema is applied on every Open, so opening an older database upgrades it.
//
// full_path is the primary key: one path holds one content at a time. Rows are
// inserted and updated in place, which is what makes Index idempotent. parent
// is denormalised so List is a single indexed lookup instead of a path scan.
const schema = `
CREATE TABLE IF NOT EXISTS files (
	full_path TEXT PRIMARY KEY,
	sha256    TEXT NOT NULL,
	parent    TEXT NOT NULL,
	filename  TEXT NOT NULL,
	mime_type TEXT NOT NULL,
	size      INTEGER NOT NULL,
	ctime     INTEGER NOT NULL,
	mtime     INTEGER NOT NULL,
	btime     INTEGER NOT NULL,
	atime     INTEGER NOT NULL,
	is_dir    INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS files_sha256 ON files(sha256);
CREATE INDEX IF NOT EXISTS files_parent ON files(parent);
`

// selectColumns is the fixed column order every read below shares.
const selectColumns = `full_path, sha256, filename, mime_type, size, ctime, mtime, btime, atime, is_dir`

// Store is an open metadata database.
//
// It is safe for concurrent use; the pool is capped at one connection because
// SQLite allows a single writer anyway and this is a local cache, not a
// network service.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: empty database path", iface.ErrInvalidPath)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("metadata: resolve database path %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("metadata: create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", abs)
	if err != nil {
		return nil, fmt.Errorf("metadata: open database %q: %w", abs, err)
	}
	db.SetMaxOpenConns(1)

	// Pragma handshake instead of a URI DSN: DSN options are parsed
	// differently per driver and per platform, plain SQL is not.
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("metadata: %s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("metadata: apply schema: %w", err)
	}
	return &Store{db: db, path: abs}, nil
}

// OpenDefault opens the store at $PEERDRIVE_METADATA_DB, or one under the
// user cache directory when the variable is unset.
func OpenDefault() (*Store, error) {
	path, err := defaultDatabasePath()
	if err != nil {
		return nil, err
	}
	return Open(path)
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path reports the absolute path of the database file.
func (s *Store) Path() string { return s.path }

func defaultDatabasePath() (string, error) {
	if p := strings.TrimSpace(os.Getenv(EnvDatabase)); p != "" {
		return p, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("metadata: locate user cache dir: %w", err)
	}
	return filepath.Join(dir, "peerdrive", "metadata.db"), nil
}

// defaultStore backs the package-level Stat/Index/List. It is opened lazily so
// importing the package never touches the filesystem.
var (
	defaultMu    sync.Mutex
	defaultStore *Store
)

// SetDefault replaces the store used by the package-level calls. Passing nil
// resets it, so the next call re-opens from the environment. The store is not
// closed: the caller owns it.
func SetDefault(s *Store) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultStore = s
}

// Default returns the store used by the package-level calls, opening it on
// first use.
func Default() (*Store, error) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultStore != nil {
		return defaultStore, nil
	}
	s, err := OpenDefault()
	if err != nil {
		return nil, err
	}
	defaultStore = s
	return s, nil
}

// Stat resolves sha to metadata using the default store.
func Stat(sha string) (iface.FileMetadata, error) {
	s, err := Default()
	if err != nil {
		return iface.FileMetadata{}, err
	}
	return s.Stat(sha)
}

// Index scans root using the default store.
func Index(root string) ([]iface.FileMetadata, error) {
	s, err := Default()
	if err != nil {
		return nil, err
	}
	return s.Index(root)
}

// List lists root using the default store.
func List(root string) ([]iface.FileMetadata, error) {
	s, err := Default()
	if err != nil {
		return nil, err
	}
	return s.List(root)
}

// ---------------------------------------------------------------- encoding --

// noTime is the stored representation of an unknown timestamp. The zero time
// predates the Unix epoch far enough that UnixNano would overflow int64, so it
// cannot simply be stored as a number.
const noTime = math.MinInt64

// encodeTime stores a timestamp as UnixNano in UTC.
func encodeTime(t time.Time) int64 {
	if t.IsZero() {
		return noTime
	}
	return t.UTC().UnixNano()
}

func decodeTime(n int64) time.Time {
	if n == noTime {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

// insertSQL upserts by path. Re-indexing an unchanged tree rewrites identical
// values, which is what "idempotent" buys: no duplicate rows, no drift.
const insertSQL = `
INSERT INTO files (full_path, sha256, filename, mime_type, size, ctime, mtime, btime, atime, is_dir, parent)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(full_path) DO UPDATE SET
	sha256    = excluded.sha256,
	filename  = excluded.filename,
	mime_type = excluded.mime_type,
	size      = excluded.size,
	ctime     = excluded.ctime,
	mtime     = excluded.mtime,
	btime     = excluded.btime,
	atime     = excluded.atime,
	is_dir    = excluded.is_dir,
	parent    = excluded.parent
`

func encodeMeta(m iface.FileMetadata) []any {
	return []any{
		m.FullPath, m.SHA256, m.Filename,
		m.MimeType, m.Size,
		encodeTime(m.Ctime), encodeTime(m.Mtime), encodeTime(m.Btime), encodeTime(m.Atime),
		boolInt(m.IsDir),
		filepath.Dir(m.FullPath),
	}
}

func scanMeta(scan func(dest ...any) error) (iface.FileMetadata, error) {
	var (
		m                          iface.FileMetadata
		fullPath, sha, filename    string
		mimeType                   string
		size                       int64
		ctime, mtime, btime, atime int64
		isDir                      int64
	)
	if err := scan(&fullPath, &sha, &filename, &mimeType, &size, &ctime, &mtime, &btime, &atime, &isDir); err != nil {
		return iface.FileMetadata{}, err
	}
	m = iface.FileMetadata{
		SHA256:   sha,
		Filename: filename,
		FullPath: fullPath,
		MimeType: mimeType,
		Size:     size,
		IsDir:    isDir != 0,
		Ctime:    decodeTime(ctime),
		Mtime:    decodeTime(mtime),
		Btime:    decodeTime(btime),
		Atime:    decodeTime(atime),
	}
	return m, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// normalizePath turns any caller-supplied path into the form stored in the
// database: absolute and cleaned. Relative paths are rejected by the schema's
// promise that FullPath is usable without knowing the caller's working
// directory.
func normalizePath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("%w: empty path", iface.ErrInvalidPath)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("metadata: resolve path %q: %w", p, err)
	}
	return filepath.Clean(abs), nil
}

// normalizeHash accepts upper or lower case hex and returns lowercase.
func normalizeHash(sha string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(sha))
	if len(h) != 64 {
		return "", fmt.Errorf("%w: %q is %d characters, want 64", ErrInvalidHash, sha, len(h))
	}
	for _, c := range h {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			return "", fmt.Errorf("%w: %q", ErrInvalidHash, sha)
		}
	}
	return h, nil
}
