package repository

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
)

// BackfillStats reports what an upgrade pass did (Issue #405).
type BackfillStats struct {
	Imported           int // file_meta rows newly added to file_index
	SkippedNoPath      int // no local provider and no CAS file → nothing servable to index
	SkippedMissingFile int // provider/CAS path points at a file that no longer exists
}

// BackfillFileIndexFromLegacy copies pre-Issue-#405 rows from file_meta/file_providers
// into file_index so an upgraded node has one authoritative index.
//
// Why this exists: before #405, several writers (anon collections, IPFS pins, the
// universal downloader) wrote only file_meta + file_providers. Those databases
// would otherwise keep a second, half-populated source of truth forever: reads are
// now index-first, so un-migrated rows would silently disappear from listings even
// though the bytes are still on disk. This pass is additive — it never deletes or
// rewrites file_meta/file_providers (no user data is dropped), so rolling back to
// the previous binary is safe.
//
// Idempotent: it only inserts hashes absent from file_index, so running it on
// every startup is harmless. Rows whose bytes are missing are skipped (indexing a
// path we cannot serve would make the node advertise broken files); they are
// counted for the startup log so operators can see stale legacy metadata.
func BackfillFileIndexFromLegacy(storageDir string) (BackfillStats, error) {
	var st BackfillStats
	if db == nil {
		return st, fmt.Errorf("backfill file_index: database not initialized")
	}

	rows, err := db.Query(`SELECT m.hash, m.size, m.filename, m.type, m.mime_type, m.gziped, m.cid,
			COALESCE((
				SELECT p.path FROM file_providers p
				WHERE p.hash = m.hash AND p.provider_type = 'local'
				ORDER BY p.available DESC, p.id ASC LIMIT 1
			), '')
		FROM file_meta m
		WHERE NOT EXISTS (SELECT 1 FROM file_index f WHERE f.hash = m.hash)`)
	if err != nil {
		return st, fmt.Errorf("query legacy file_meta: %w", err)
	}

	type legacyRow struct {
		hash, name, kind, mime, cid, providerPath string
		size                                      int64
		gziped                                    int
	}
	var pending []legacyRow
	for rows.Next() {
		var r legacyRow
		if err := rows.Scan(&r.hash, &r.size, &r.name, &r.kind, &r.mime, &r.gziped, &r.cid, &r.providerPath); err != nil {
			_ = rows.Close()
			return st, fmt.Errorf("scan legacy file_meta: %w", err)
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return st, fmt.Errorf("iterate legacy file_meta: %w", err)
	}
	_ = rows.Close()
	if len(pending) == 0 {
		return st, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return st, fmt.Errorf("begin backfill tx: %w", err)
	}
	defer tx.Rollback()

	var last sql.NullInt64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM file_index`).Scan(&last); err != nil {
		return st, fmt.Errorf("read file_index max seq: %w", err)
	}
	seq := last.Int64

	for _, r := range pending {
		absPath := r.providerPath
		if absPath == "" {
			// No local provider row: either an http/remote-only registration or a
			// legacy CAS write. Only the CAS layout can be recovered without the
			// provider path; if it is absent too there is nothing to index.
			if storageDir == "" {
				st.SkippedNoPath++
				continue
			}
			// CAS layout: <storage>/<h[:2]>/<h>
			absPath = filepath.Join(storageDir, r.hash[:2], r.hash)
			if _, statErr := os.Stat(absPath); statErr != nil {
				st.SkippedNoPath++
				continue
			}
		} else if !filepath.IsAbs(absPath) {
			if storageDir == "" {
				st.SkippedNoPath++
				continue
			}
			absPath = filepath.Join(storageDir, absPath)
		}
		if _, statErr := os.Stat(absPath); statErr != nil {
			st.SkippedMissingFile++
			continue
		}

		seq++
		kind := r.kind
		if kind == "" {
			kind = model.FileTypeBlob
		}
		if _, err := tx.Exec(`INSERT INTO file_index
			(hash, path, name, size, deleted, seq, uploader_peer_id, is_inbox, kind, mime_type, gziped, cid, origin)
			VALUES (?, ?, ?, ?, 0, ?, '', 0, ?, ?, ?, ?, ?)
			ON CONFLICT(hash) DO NOTHING`,
			r.hash, absPath, r.name, r.size, seq, kind, r.mime, r.gziped, r.cid, FileOriginBackfill); err != nil {
			return st, fmt.Errorf("backfill file_index %s: %w", r.hash, err)
		}
		st.Imported++
	}

	if err := tx.Commit(); err != nil {
		return st, fmt.Errorf("commit backfill tx: %w", err)
	}
	if st.Imported > 0 || st.SkippedMissingFile > 0 {
		log.LogInfo("db: file_index backfill: imported=%d skipped_no_path=%d skipped_missing_file=%d",
			st.Imported, st.SkippedNoPath, st.SkippedMissingFile)
	}
	return st, nil
}

// RemoteURLBackfillStats reports what the URL-provider migration pass did.
type RemoteURLBackfillStats struct {
	// Filled is the number of existing file_index rows whose remote_url was empty
	// and got populated from a legacy file_providers row.
	Filled int
	// Imported is the number of URL-only legacy rows (http provider, no local
	// bytes) that had no file_index row at all and were newly indexed with an
	// empty path and their remote_url set.
	Imported int
}

// BackfillRemoteURLsFromLegacy migrates pre-Issue-#405 `file_providers` rows with
// provider_type='http' into file_index.remote_url (Issue #405 Step 2).
//
// Why a separate pass instead of extending BackfillFileIndexFromLegacy: that pass
// deliberately refuses to index rows whose bytes it cannot serve, which excluded
// exactly the URL-only registrations this migration is about — a URL registration
// *is* servable, through the URL, just not from local disk. Kept apart so the two
// sets of skip rules stay readable.
//
// Runs after the local backfill on every startup, is idempotent, and never
// deletes or rewrites file_meta/file_providers, so rolling back to the previous
// binary is safe. First provider wins per hash (ORDER BY id ASC), matching
// RegisterFile's first-writer-wins rule for remote_url.
func BackfillRemoteURLsFromLegacy() (RemoteURLBackfillStats, error) {
	var st RemoteURLBackfillStats
	if db == nil {
		return st, fmt.Errorf("backfill remote_url: database not initialized")
	}

	type urlRow struct {
		hash, url string
	}
	rows, err := db.Query(`SELECT hash, path FROM file_providers
		WHERE provider_type = 'http' AND available = 1
		ORDER BY id ASC`)
	if err != nil {
		return st, fmt.Errorf("query legacy http providers: %w", err)
	}
	var pending []urlRow
	for rows.Next() {
		var r urlRow
		if err := rows.Scan(&r.hash, &r.url); err != nil {
			_ = rows.Close()
			return st, fmt.Errorf("scan legacy http providers: %w", err)
		}
		if r.hash == "" || r.url == "" {
			continue
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return st, fmt.Errorf("iterate legacy http providers: %w", err)
	}
	_ = rows.Close()
	if len(pending) == 0 {
		return st, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return st, fmt.Errorf("begin remote_url backfill tx: %w", err)
	}
	defer tx.Rollback()

	var last sql.NullInt64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM file_index`).Scan(&last); err != nil {
		return st, fmt.Errorf("read file_index max seq: %w", err)
	}
	seq := last.Int64
	seen := make(map[string]bool, len(pending))
	for _, r := range pending {
		if seen[r.hash] { // first provider per hash wins
			continue
		}
		seen[r.hash] = true

		var exists bool
		err := tx.QueryRow(`SELECT 1 FROM file_index WHERE hash = ?`, r.hash).Scan(&exists)
		if err != nil && err != sql.ErrNoRows {
			_ = tx.Rollback()
			return st, fmt.Errorf("check file_index %s: %w", r.hash, err)
		}
		if exists {
			res, err := tx.Exec(`UPDATE file_index SET remote_url = ?, updated_at = CURRENT_TIMESTAMP
				WHERE hash = ? AND remote_url = ''`, r.url, r.hash)
			if err != nil {
				return st, fmt.Errorf("backfill remote_url %s: %w", r.hash, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				st.Filled++
			}
			continue
		}

		seq++
		if _, err := tx.Exec(`INSERT INTO file_index
			(hash, path, name, size, deleted, seq, kind, remote_url, origin)
			VALUES (?, '', '', 0, 0, ?, ?, ?, ?)`,
			r.hash, seq, model.FileTypeBlob, r.url, FileOriginBackfill); err != nil {
			return st, fmt.Errorf("backfill remote_url row %s: %w", r.hash, err)
		}
		st.Imported++
	}

	if err := tx.Commit(); err != nil {
		return st, fmt.Errorf("commit remote_url backfill tx: %w", err)
	}
	if st.Filled > 0 || st.Imported > 0 {
		log.LogInfo("db: remote_url backfill: filled=%d imported=%d", st.Filled, st.Imported)
	}
	return st, nil
}
