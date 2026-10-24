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
	Hash           string
	Path           string
	Name           string
	Size           int64
	Deleted        bool
	Seq            int64 // monotonically increasing sync cursor (for metadata incremental sync)
	CreatedAt      string
	UpdatedAt      string
	UploaderPeerID string // remote peer ID who uploaded the file, or empty if local owner
	IsInbox        bool   // true if uploaded by remote peer and pending quarantine review
}

// createFileIndexTable creates the table. seq increments on each upsert/delete;
// incremental sync between nodes fetches by seq.
func createFileIndexTable() {
	createFileIndexTableOn(db)
}

func createFileIndexTableOn(d *sql.DB) {
	if d == nil {
		return
	}
	d.Exec(`CREATE TABLE IF NOT EXISTS file_index (
		hash TEXT PRIMARY KEY,
		path TEXT NOT NULL,
		name TEXT DEFAULT '',
		size INTEGER DEFAULT 0,
		deleted INTEGER DEFAULT 0,
		seq INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		uploader_peer_id TEXT DEFAULT '',
		is_inbox INTEGER DEFAULT 0
	)`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN uploader_peer_id TEXT DEFAULT ''`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN is_inbox INTEGER DEFAULT 0`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_seq ON file_index(seq)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_name ON file_index(name)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_inbox ON file_index(is_inbox)`)
}

// UpsertFileIndex registers/updates a mapping (called after create/upload succeeds), returns new seq.
func UpsertFileIndex(hash, path, name string, size int64, deleted bool) (int64, error) {
	return UpsertFileIndexWithMeta(hash, path, name, size, deleted, "", false)
}

// UpsertFileIndexWithMeta registers/updates a mapping with uploader provenance and inbox quarantine metadata (Issue #267).
func UpsertFileIndexWithMeta(hash, path, name string, size int64, deleted bool, uploaderPeerID string, isInbox bool) (int64, error) {
	tx, err := db.Begin()
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
	_, err = tx.Exec(`INSERT INTO file_index (hash, path, name, size, deleted, seq, uploader_peer_id, is_inbox)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			path=excluded.path, name=excluded.name, size=excluded.size,
			deleted=excluded.deleted, seq=excluded.seq,
			uploader_peer_id=CASE WHEN excluded.uploader_peer_id != '' THEN excluded.uploader_peer_id ELSE file_index.uploader_peer_id END,
			is_inbox=excluded.is_inbox,
			updated_at=CURRENT_TIMESTAMP`,
		hash, path, name, size, boolToInt(deleted), seq, uploaderPeerID, boolToInt(isInbox))
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
	if db == nil {
		return nil, sql.ErrNoRows
	}
	row := db.QueryRow(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox
		FROM file_index WHERE hash = ? AND deleted = 0`, hash)
	return scanFileIndex(row)
}

// ListFileIndex lists all non-deleted mappings (ordered by seq ascending, supports pagination).
func ListFileIndex(offset, limit int) ([]FileIndex, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox
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

// ListInboxFiles lists all quarantined files uploaded by external peers (Issue #267).
func ListInboxFiles(offset, limit int) ([]FileIndex, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox
		FROM file_index WHERE deleted = 0 AND is_inbox = 1 ORDER BY seq DESC LIMIT ? OFFSET ?`, limit, offset)
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

// ApproveInboxFile approves a quarantined file, integrating it into host storage index (Issue #267).
func ApproveInboxFile(hash string) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`UPDATE file_index SET is_inbox = 0, updated_at = CURRENT_TIMESTAMP WHERE hash = ?`, hash)
	return err
}

// ListFileIndexSince incremental sync: returns all changes with seq greater than since (including delete markers).
// Defense: since comes from remote sync verb; unbounded change records cause full-table scan + materialization.
// Add LIMIT as backstop (discard extreme values when peer's cursor is far behind).
func ListFileIndexSince(since int64) ([]FileIndex, error) {
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox
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
	var isInbox int
	if err := r.Scan(&f.Hash, &f.Path, &f.Name, &f.Size, &deleted, &f.Seq, &f.CreatedAt, &f.UpdatedAt, &f.UploaderPeerID, &isInbox); err != nil {
		return nil, err
	}
	f.Deleted = deleted != 0
	f.IsInbox = isInbox != 0
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
