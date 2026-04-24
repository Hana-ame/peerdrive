package model

type Collection struct {
	ID             int    `db:"id"`
	Username       string `db:"username"`
	CollectionName string `db:"collection_name"`
	CreatedAt      string `db:"created_at"`
}

type CollectionEntry struct {
	ID           int    `db:"id"`
	CollectionID int    `db:"collection_id"`
	Path         string `db:"path"`
	FileHash     string `db:"file_hash"`
}

type CollectionVersion struct {
	ID              int    `db:"id"`
	CollectionID    int    `db:"collection_id"`
	VersionNumber   int    `db:"version_number"`
	CommitMessage   string `db:"commit_message"`
	CreatedAt       string `db:"created_at"`
	ParentVersionID *int   `db:"parent_version_id"`
}

type VersionEntry struct {
	ID        int    `db:"id"`
	VersionID int    `db:"version_id"`
	Path      string `db:"path"`
	FileHash  string `db:"file_hash"`
}
