// Local sync repository — CRUD for local_collection_sync and local_sync_files tables, used to track sync status of collection files to disk.
package repository

import (
	"database/sql"
	"encoding/json"
	"peerdrive/internal/model"
)

type SyncRepository struct{}

// NewSyncRepository creates a new sync repository instance.
func NewSyncRepository() *SyncRepository {
	return &SyncRepository{}
}

// UpsertSyncState inserts or updates the local sync state record for a collection (with include/exclude filters).
func (r *SyncRepository) UpsertSyncState(sync *model.LocalCollectionSync) error {
	includeJSON, _ := json.Marshal(sync.IncludeFilter)
	excludeJSON, _ := json.Marshal(sync.ExcludeFilter)

	_, err := db.Exec(`
		INSERT INTO local_collection_sync (collection_hash, local_path, include_filter, exclude_filter, synced_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(collection_hash) DO UPDATE SET
			local_path = excluded.local_path,
			include_filter = excluded.include_filter,
			exclude_filter = excluded.exclude_filter,
			synced_at = CURRENT_TIMESTAMP`,
		sync.CollectionHash, sync.LocalPath, string(includeJSON), string(excludeJSON))
	return err
}

// GetSyncState queries local sync state by collection hash; returns (nil, nil) if not found.
func (r *SyncRepository) GetSyncState(hash string) (*model.LocalCollectionSync, error) {
	var s model.LocalCollectionSync
	var includeStr, excludeStr string
	err := db.QueryRow(`SELECT collection_hash, local_path, include_filter, exclude_filter, synced_at FROM local_collection_sync WHERE collection_hash = ?`, hash).
		Scan(&s.CollectionHash, &s.LocalPath, &includeStr, &excludeStr, &s.SyncedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(includeStr), &s.IncludeFilter)
	json.Unmarshal([]byte(excludeStr), &s.ExcludeFilter)
	return &s, nil
}

// UpsertFileSyncState inserts or updates the sync state for a file in a collection (isSaved marks whether save to disk succeeded).
func (r *SyncRepository) UpsertFileSyncState(hash, path string, isSaved bool) error {
	_, err := db.Exec(`
		INSERT INTO local_sync_files (collection_hash, file_path, is_saved, last_modified)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(collection_hash, file_path) DO UPDATE SET
			is_saved = excluded.is_saved,
			last_modified = CURRENT_TIMESTAMP`,
		hash, path, isSaved)
	return err
}

// GetSyncFiles queries all file sync state records for a specified collection.
// M11: No LIMIT → full-table materialization; limit to 1000 (local sync state records are usually far fewer).
func (r *SyncRepository) GetSyncFiles(hash string) ([]model.LocalSyncFile, error) {
	rows, err := db.Query(`SELECT id, collection_hash, file_path, is_saved, last_modified FROM local_sync_files WHERE collection_hash = ? LIMIT 1000`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []model.LocalSyncFile
	for rows.Next() {
		var f model.LocalSyncFile
		var isSavedInt int
		if err := rows.Scan(&f.ID, &f.CollectionHash, &f.FilePath, &isSavedInt, &f.LastModified); err != nil {
			return nil, err
		}
		f.IsSaved = isSavedInt == 1
		files = append(files, f)
	}
	return files, nil
}

// ClearSyncFiles deletes all file sync records for a specified collection (called before re-sync).
func (r *SyncRepository) ClearSyncFiles(hash string) error {
	_, err := db.Exec(`DELETE FROM local_sync_files WHERE collection_hash = ?`, hash)
	return err
}
