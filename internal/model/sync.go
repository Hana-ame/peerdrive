package model

import "time"

type LocalCollectionSync struct {
	CollectionHash string    `json:"collection_hash"`
	LocalPath      string    `json:"local_path"`
	IncludeFilter  []string  `json:"include_filter"`
	ExcludeFilter  []string  `json:"exclude_filter"`
	SyncedAt       time.Time `json:"synced_at"`
}

type LocalSyncFile struct {
	ID            int64     `json:"id"`
	CollectionHash string    `json:"collection_hash"`
	FilePath      string    `json:"file_path"`
	IsSaved       bool      `json:"is_saved"`
	LastModified  time.Time `json:"last_modified"`
}

type SaveLocalRequest struct {
	CollectionHash string   `json:"collection_hash"`
	LocalPath      string   `json:"local_path"`
	Include        []string `json:"include"`
	Exclude        []string `json:"exclude"`
}

type SyncStatusResponse struct {
	LocalPath   string             `json:"local_path"`
	TotalFiles  int                `json:"total_files"`
	SavedFiles  int                `json:"saved_files"`
	MissingFiles []LocalSyncFile    `json:"missing_files"`
	LastSynced  time.Time          `json:"last_synced"`
}
