package repository

import (
	"database/sql"
	"encoding/json"
	"peerdrive/internal/model"
)

type SyncRepository struct{}

func NewSyncRepository() *SyncRepository {
	return &SyncRepository{}
}

func (r *SyncRepository) UpsertSyncState(sync *model.LocalCollectionSync) error {
	includeJSON, _ := json.Marshal(sync.IncludeFilter)
	excludeJSON, _ := json.Marshal(sync.ExcludeFilter)

	_, err := DB.Exec(`
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

func (r *SyncRepository) GetSyncState(hash string) (*model.LocalCollectionSync, error) {
	var s model.LocalCollectionSync
	var includeStr, excludeStr string
	err := DB.QueryRow(`SELECT collection_hash, local_path, include_filter, exclude_filter, synced_at FROM local_collection_sync WHERE collection_hash = ?`, hash).
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

func (r *SyncRepository) UpsertFileSyncState(hash, path string, isSaved bool) error {
	_, err := DB.Exec(`
		INSERT INTO local_sync_files (collection_hash, file_path, is_saved, last_modified)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(collection_hash, file_path) DO UPDATE SET
			is_saved = excluded.is_saved,
			last_modified = CURRENT_TIMESTAMP`,
		hash, path, isSaved)
	return err
}

func (r *SyncRepository) GetSyncFiles(hash string) ([]model.LocalSyncFile, error) {
	rows, err := DB.Query(`SELECT id, collection_hash, file_path, is_saved, last_modified FROM local_sync_files WHERE collection_hash = ?`, hash)
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

func (r *SyncRepository) ClearSyncFiles(hash string) error {
	_, err := DB.Exec(`DELETE FROM local_sync_files WHERE collection_hash = ?`, hash)
	return err
}
