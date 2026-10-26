package repository

import (
	"database/sql"
	"fmt"
	"time"

	"peerdrive/internal/model"
	"peerdrive/pkg/hashutil"
)

// FileIndex local file index: sha256 → absolute path mapping (persisted in SQLite).
// Since Issue #405 it is the **authoritative** index for "which files this node has
// and where they live"; file_meta/file_providers are kept only as a compatibility
// mirror (see RegisterFile) and are backfilled into this table on upgrade.
//
// The extra metadata columns (kind / mime_type / gziped / cid / origin) were added
// by the same Issue #405 convergence so reads no longer need the legacy tables.
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
	Kind           string // model.FileTypeBlob (default) or model.FileTypeAnonCollection
	MimeType       string
	Gziped         bool
	CID            string
	Origin         string // how the row got here: unified | legacy | legacy_backfill
	RemoteURL      string // Issue #405 Step 2: first registered remote URL (replaces file_providers provider_type='http')
}

// Origin markers for file_index rows (Issue #405).
//
// Why a persisted marker instead of inferring: during the deprecation window the
// same row can be written by two code paths. The marker lets tests and operators
// tell "written through the unified path" (mirrored to file_meta) from "written by
// a legacy/direct path" (may need the startup backfill) without guessing.
const (
	FileOriginUnified  = "unified"         // written via RegisterFile: index authoritative + legacy mirror
	FileOriginLegacy   = "legacy"          // direct index-only write (sync/Create) or row from an old DB
	FileOriginBackfill = "legacy_backfill" // imported from file_meta/file_providers by BackfillFileIndexFromLegacy
)

// FileRecord is the single write entry for a locally owned file (Issue #405).
//
// Why a struct: before this, every writer repeated a meta+provider+index triplet
// and it was easy to forget one leg (anon collections, IPFS pins and the
// universal downloader all wrote only the legacy tables). RegisterFile atomically
// writes the authoritative index row and the compatibility mirror, so a new
// caller cannot reintroduce the dual-track skew by forgetting a statement.
type FileRecord struct {
	Hash           string
	Path           string // absolute path (file_index.path is matched against share dirs)
	Name           string
	Size           int64
	Kind           string // empty → model.FileTypeBlob
	MimeType       string
	Gziped         bool
	CID            string // empty → derived from Hash (same value InsertFileMeta stores)
	UploaderPeerID string
	IsInbox        bool
	ProviderPath   string // legacy file_providers.path (relative to storage or absolute); empty → no mirror row
	RemoteURL      string // remote URL for URL-only files (no local path); converges the http provider
}

// legacyMetaMirror gates RegisterFile's file_meta/file_providers compatibility
// writes (Issue #405 Step 2, PEERDRIVE_LEGACY_META_MIRROR).
//
// Default true: pre-#405 readers and operator scripts still read the legacy
// tables, so the mirror stays on by default. The server sets it from config at
// startup (SetLegacyMetaMirror); it is never read directly by callers. When it is
// false the legacy tables stop growing, which is the observation window that
// authorizes Step 3 (DROP TABLE).
var legacyMetaMirror = true

// SetLegacyMetaMirror switches the RegisterFile legacy mirror on/off (see var).
func SetLegacyMetaMirror(v bool) { legacyMetaMirror = v }

// LegacyMetaMirrorEnabled reports the current mirror state (tests + ops).
func LegacyMetaMirrorEnabled() bool { return legacyMetaMirror }

// createFileIndexTable creates the table. seq increments on each upsert/delete;
// incremental sync between nodes fetches by seq.
func createFileIndexTable() {
	createFileIndexTableOn(db)
}

// createFileIndexTableOn creates the file_index table and applies the additive
// column migrations for pre-Issue-#405 databases.
//
// Why the ALTERs ignore their error: SQLite has no "ADD COLUMN IF NOT EXISTS",
// and `CREATE TABLE IF NOT EXISTS` does not touch an existing table. On a fresh
// DB the CREATE already has every column, so each ALTER fails with "duplicate
// column" — expected. This mirrors the existing uploader_peer_id/is_inbox
// migration style (file_index predates schema_migrations and is created after
// some migrations run, so version-numbered migrations cannot be used here).
func createFileIndexTableOn(d *sql.DB) {
	if d == nil {
		return
	}
	_, _ = d.Exec(`CREATE TABLE IF NOT EXISTS file_index (
		hash TEXT PRIMARY KEY,
		path TEXT NOT NULL,
		name TEXT DEFAULT '',
		size INTEGER DEFAULT 0,
		deleted INTEGER DEFAULT 0,
		seq INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		uploader_peer_id TEXT DEFAULT '',
		is_inbox INTEGER DEFAULT 0,
		kind TEXT DEFAULT 'blob',
		mime_type TEXT DEFAULT '',
		gziped INTEGER DEFAULT 0,
		cid TEXT DEFAULT '',
		origin TEXT DEFAULT 'legacy',
		remote_url TEXT DEFAULT ''
	)`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN uploader_peer_id TEXT DEFAULT ''`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN is_inbox INTEGER DEFAULT 0`)
	// Issue #405: metadata converged from file_meta so reads can be index-first.
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN kind TEXT DEFAULT 'blob'`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN mime_type TEXT DEFAULT ''`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN gziped INTEGER DEFAULT 0`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN cid TEXT DEFAULT ''`)
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN origin TEXT DEFAULT 'legacy'`)
	// Issue #405 Step 2: URL providers converged from file_providers so the table
	// can stop being read (and then be dropped in Step 3).
	_, _ = d.Exec(`ALTER TABLE file_index ADD COLUMN remote_url TEXT DEFAULT ''`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_seq ON file_index(seq)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_name ON file_index(name)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_inbox ON file_index(is_inbox)`)
	d.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_kind ON file_index(kind)`)
}

// UpsertFileIndex registers/updates a mapping (called after create/upload succeeds), returns new seq.
func UpsertFileIndex(hash, path, name string, size int64, deleted bool) (int64, error) {
	return UpsertFileIndexWithMeta(hash, path, name, size, deleted, "", false)
}

// UpsertFileIndexWithMeta registers/updates a mapping with uploader provenance and inbox quarantine metadata (Issue #267).
//
// This is the index-only path (P2P sync / Create): it deliberately does not touch
// the metadata columns or the legacy mirror. Local files that have metadata must
// go through RegisterFile instead (Issue #405).
func UpsertFileIndexWithMeta(hash, path, name string, size int64, deleted bool, uploaderPeerID string, isInbox bool) (int64, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin file_index tx: %w", err)
	}
	defer tx.Rollback()
	seq, err := upsertFileIndexOnlyTx(tx, hash, path, name, size, deleted, uploaderPeerID, isInbox)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit file_index tx: %w", err)
	}
	return seq, nil
}

func upsertFileIndexOnlyTx(tx *sql.Tx, hash, path, name string, size int64, deleted bool, uploaderPeerID string, isInbox bool) (int64, error) {
	var last sql.NullInt64
	// Note: seq monotonicity depends on transaction serialization; safe under SQLite single-writer
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM file_index`).Scan(&last); err != nil {
		return 0, fmt.Errorf("read file_index max seq: %w", err)
	}
	seq := last.Int64 + 1
	_, err := tx.Exec(`INSERT INTO file_index (hash, path, name, size, deleted, seq, uploader_peer_id, is_inbox)
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
	return seq, nil
}

// RegisterFile is the single write entry for a locally owned file (Issue #405).
//
// One transaction writes:
//  1. the authoritative file_index row (path/name/size/kind/mime/cid/remote_url
//     + origin='unified') — always,
//  2. the legacy file_meta mirror (metadata columns only; never overwrites an
//     existing non-empty filename, same rule as the old UpdateFileMetaFilename),
//  3. the legacy file_providers local row when ProviderPath is set.
//
// Legs 2 and 3 are gated by legacyMetaMirror (PEERDRIVE_LEGACY_META_MIRROR,
// default on, Issue #405 Step 2): the mirror exists only for one deprecation
// window, since pre-#405 readers and operator scripts still read
// file_meta/file_providers. New reads in this tree are index-first (see
// service.FileService.GetMeta / ListAll), so turning the mirror off proves the
// legacy tables can be dropped in Step 3 — see doc/TODO-SIMPLIFY.md M2.
//
// remote_url is first-writer-wins: a hash is content-addressed, so a second URL
// for the same content is not more correct than the first. Keeping the first URL
// avoids losing a reachable URL if a later registration points at a dead host.
// Multiple distinct URLs per hash are no longer tracked (file_providers used to
// keep every one); that is the accepted Step 2 tradeoff and the mirror above
// still records them for the remainder of the deprecation window.
func RegisterFile(rec FileRecord) (int64, error) {
	if rec.Hash == "" {
		return 0, fmt.Errorf("register file: empty hash")
	}
	kind := rec.Kind
	if kind == "" {
		kind = model.FileTypeBlob
	}
	cid := rec.CID
	if cid == "" {
		cid = hashutil.SHA256ToCID(rec.Hash)
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin register file tx: %w", err)
	}
	defer tx.Rollback()

	var last sql.NullInt64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM file_index`).Scan(&last); err != nil {
		return 0, fmt.Errorf("read file_index max seq: %w", err)
	}
	seq := last.Int64 + 1

	// The authoritative row. gziped is only ever promoted to 1 (no writer sets it
	// to true today, but a re-registration must not silently clear it).
	if _, err := tx.Exec(`INSERT INTO file_index
		(hash, path, name, size, deleted, seq, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			path=excluded.path, name=excluded.name, size=excluded.size,
			deleted=0, seq=excluded.seq,
			uploader_peer_id=CASE WHEN excluded.uploader_peer_id != '' THEN excluded.uploader_peer_id ELSE file_index.uploader_peer_id END,
			is_inbox=excluded.is_inbox,
			kind=CASE WHEN excluded.kind != '' THEN excluded.kind ELSE file_index.kind END,
			mime_type=CASE WHEN excluded.mime_type != '' THEN excluded.mime_type ELSE file_index.mime_type END,
			gziped=CASE WHEN excluded.gziped = 1 THEN 1 ELSE file_index.gziped END,
			cid=CASE WHEN excluded.cid != '' THEN excluded.cid ELSE file_index.cid END,
			remote_url=CASE WHEN file_index.remote_url != '' THEN file_index.remote_url ELSE excluded.remote_url END,
			origin=excluded.origin,
			updated_at=CURRENT_TIMESTAMP`,
		rec.Hash, rec.Path, rec.Name, rec.Size, seq,
		rec.UploaderPeerID, boolToInt(rec.IsInbox),
		kind, rec.MimeType, boolToInt(rec.Gziped), cid, FileOriginUnified, rec.RemoteURL); err != nil {
		return 0, fmt.Errorf("register file_index: %w", err)
	}

	// Legacy mirrors (gated, Issue #405 Step 2). Same column set/merge rules as
	// InsertFileMeta + UpdateFileMetaFilename.
	if legacyMetaMirror {
		if _, err := tx.Exec(`INSERT INTO file_meta (hash, size, mime_type, gziped, filename, type, cid)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(hash) DO UPDATE SET
				size=excluded.size,
				mime_type=CASE WHEN excluded.mime_type != '' THEN excluded.mime_type ELSE file_meta.mime_type END,
				gziped=CASE WHEN excluded.gziped = 1 THEN 1 ELSE file_meta.gziped END,
				filename=CASE WHEN file_meta.filename IS NULL OR file_meta.filename = '' THEN excluded.filename ELSE file_meta.filename END,
				type=CASE WHEN excluded.type != '' THEN excluded.type ELSE file_meta.type END,
				cid=CASE WHEN excluded.cid != '' THEN excluded.cid ELSE file_meta.cid END`,
			rec.Hash, rec.Size, rec.MimeType, boolToInt(rec.Gziped), rec.Name, kind, cid); err != nil {
			return 0, fmt.Errorf("mirror file_meta: %w", err)
		}

		if rec.ProviderPath != "" {
			if _, err := tx.Exec(`INSERT INTO file_providers (hash, provider_type, path)
				SELECT ?, 'local', ?
				WHERE NOT EXISTS (
					SELECT 1 FROM file_providers WHERE hash = ? AND provider_type = 'local' AND path = ?
				)`,
				rec.Hash, rec.ProviderPath, rec.Hash, rec.ProviderPath); err != nil {
				return 0, fmt.Errorf("mirror file_providers: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit register file tx: %w", err)
	}
	return seq, nil
}

// GetFileIndex queries mapping by hash (not deleted — tombstones are only exposed through SyncSince).
func GetFileIndex(hash string) (*FileIndex, error) {
	if db == nil {
		return nil, sql.ErrNoRows
	}
	row := db.QueryRow(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url
		FROM file_index WHERE hash = ? AND deleted = 0`, hash)
	return scanFileIndex(row)
}

// GetFileIndexByCID queries a non-deleted mapping by IPFS CID (Issue #405 read convergence).
func GetFileIndexByCID(cid string) (*FileIndex, error) {
	if db == nil || cid == "" {
		return nil, sql.ErrNoRows
	}
	row := db.QueryRow(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url
		FROM file_index WHERE cid = ? AND deleted = 0`, cid)
	return scanFileIndex(row)
}

// ListFileIndex lists all non-deleted blob mappings (ordered by seq descending, supports pagination).
//
// kind='blob' matters: anon-collection JSON now lives in file_index too (Issue #405),
// but it must not leak into the P2P list/share surface, which advertises files.
func ListFileIndex(offset, limit int) ([]FileIndex, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url
		FROM file_index WHERE deleted = 0 AND kind = 'blob' ORDER BY seq DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFileIndex(rows)
}

// ListAnonCollectionIndex lists registered anonymous-collection hashes (Issue #405:
// replaces the old `SELECT ... FROM file_meta WHERE type='anon_collection'` read).
func ListAnonCollectionIndex(offset, limit int) ([]FileIndex, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url
		FROM file_index WHERE deleted = 0 AND kind = ? ORDER BY seq DESC LIMIT ? OFFSET ?`,
		model.FileTypeAnonCollection, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFileIndex(rows)
}

// ListInboxFiles lists all quarantined files uploaded by external peers (Issue #267).
func ListInboxFiles(offset, limit int) ([]FileIndex, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url
		FROM file_index WHERE deleted = 0 AND is_inbox = 1 AND kind = 'blob' ORDER BY seq DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFileIndex(rows)
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
// kind='blob': collection JSON is node-local state, not part of the file-sync surface.
func ListFileIndexSince(since int64) ([]FileIndex, error) {
	rows, err := db.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin, remote_url
		FROM file_index WHERE seq > ? AND kind = 'blob' ORDER BY seq ASC LIMIT 1000`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFileIndex(rows)
}

// DeleteFileIndex logical delete (sync tombstone), returns new seq.
func DeleteFileIndex(hash string) (int64, error) {
	return UpsertFileIndex(hash, "", "", 0, true)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func collectFileIndex(rows *sql.Rows) ([]FileIndex, error) {
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

func scanFileIndex(r rowScanner) (*FileIndex, error) {
	var f FileIndex
	var deleted, isInbox, gziped int
	if err := r.Scan(&f.Hash, &f.Path, &f.Name, &f.Size, &deleted, &f.Seq, &f.CreatedAt, &f.UpdatedAt, &f.UploaderPeerID, &isInbox, &f.Kind, &f.MimeType, &gziped, &f.CID, &f.Origin, &f.RemoteURL); err != nil {
		return nil, err
	}
	f.Deleted = deleted != 0
	f.IsInbox = isInbox != 0
	f.Gziped = gziped != 0
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
