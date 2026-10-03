package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// FileIndex local file index: sha256 → absolute path mapping (persisted in SQLite).
// Independent of old file_meta/file_providers tables: semantically focused (mapping + sync cursor),
// does not affect old file service logic.
type FileIndex struct {
	Hash      string
	Path      string
	Name      string
	Size      int64
	Deleted   bool
	Seq       int64 // monotonically increasing sync cursor (for metadata incremental sync)
	CreatedAt string
	UpdatedAt string
}

// createFileIndexTable creates the table. seq increments on each upsert/delete;
// incremental sync between nodes fetches by seq.
func createFileIndexTable() {
	DB.Exec(`CREATE TABLE IF NOT EXISTS file_index (
		hash TEXT PRIMARY KEY,
		path TEXT NOT NULL,
		name TEXT DEFAULT '',
		size INTEGER DEFAULT 0,
		deleted INTEGER DEFAULT 0,
		seq INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_seq ON file_index(seq)`)
}

// UpsertFileIndex registers/updates a mapping (called after create/upload succeeds), returns new seq.
// M10: original implementation had nextFileIndexSeq() as SELECT MAX+1 then separate INSERT —
// with database/sql connection pool's multiple connections writing concurrently, two requests
// could read the same MAX → seq collision, sync cursor chaos.
// Merged into the same transaction: SELECT and INSERT complete atomically within one write
// transaction (SQLite's serial write guarantee ensures monotonicity).
func UpsertFileIndex(hash, path, name string, size int64, deleted bool) (int64, error) {
	tx, err := DB.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin file_index tx: %w", err)
	}
	defer tx.Rollback()
	var last sql.NullInt64
	// Note: seq monotonicity depends on transaction serialization; safe under SQLite single-writer
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM file_index`).Scan(&last); err != nil {
		return 0, fmt.Errorf("read file_index max seq: %w", err)
	}
	seq := last.Int64 + 1
	_, err = tx.Exec(`INSERT INTO file_index (hash, path, name, size, deleted, seq)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			path=excluded.path, name=excluded.name, size=excluded.size,
			deleted=excluded.deleted, seq=excluded.seq,
			updated_at=CURRENT_TIMESTAMP`,
		hash, path, name, size, boolToInt(deleted), seq)
	if err != nil {
		return 0, fmt.Errorf("upsert file_index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit file_index tx: %w", err)
	}
	return seq, nil
}

// GetFileIndex queries mapping by hash (not deleted — tombstones are only exposed through SyncSince).
func GetFileIndex(hash string) (*FileIndex, error) {
	row := DB.QueryRow(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at
		FROM file_index WHERE hash = ? AND deleted = 0`, hash)
	return scanFileIndex(row)
}

// ListFileIndex lists all non-deleted mappings (ordered by seq ascending, supports pagination).
func ListFileIndex(offset, limit int) ([]FileIndex, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := DB.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at
		FROM file_index WHERE deleted = 0 ORDER BY seq DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileIndex
	for rows.Next() {
		f, err := scanFileIndex(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// ListFileIndexSince incremental sync: returns all changes with seq greater than since (including delete markers).
// Defense: since comes from remote sync verb; unbounded change records cause full-table scan + materialization.
// Add LIMIT as backstop (discard extreme values when peer's cursor is far behind).
func ListFileIndexSince(since int64) ([]FileIndex, error) {
	rows, err := DB.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at
		FROM file_index WHERE seq > ? ORDER BY seq ASC LIMIT 1000`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileIndex
	for rows.Next() {
		f, err := scanFileIndex(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// DeleteFileIndex logical delete (sync tombstone), returns new seq.
func DeleteFileIndex(hash string) (int64, error) {
	return UpsertFileIndex(hash, "", "", 0, true)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFileIndex(r rowScanner) (*FileIndex, error) {
	var f FileIndex
	var deleted int
	if err := r.Scan(&f.Hash, &f.Path, &f.Name, &f.Size, &deleted, &f.Seq, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return nil, err
	}
	f.Deleted = deleted != 0
	return &f, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fileIndexNow timestamp (overridable in tests).
var fileIndexNow = func() string { return time.Now().UTC().Format(time.RFC3339) }
