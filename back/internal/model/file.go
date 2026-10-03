// Package model defines the core data structures and SQLite table mappings for the Peerdrive system.
// FileMeta holds metadata for each unique file content (hash is the primary key).
// FileProvider holds the storage location for each copy (the same hash can have multiple providers).
// Uses db tags to mark database column names for Scan binding in the repository layer.

package model

// File type constants (domain constants, originally defined in the repository package — moved to domain during M2 collection layer
// so that repository/controller uniformly depend on model instead of mutual/reverse references).
const (
	FileTypeBlob           = "blob"
	FileTypeAnonCollection = "anon_collection"
)

type FileMeta struct {
	Hash      string `db:"hash"`
	Size      int64  `db:"size"`
	CreatedAt string `db:"created_at"`
	MimeType  string `db:"mime_type"`
	Gziped    bool   `db:"gziped"`
	Filename  string `db:"filename"`
	Type      string `db:"type"`
	CID       string `db:"cid" json:"cid"`
}

type FileProvider struct {
	ID           int    `db:"id"`
	Hash         string `db:"hash"`
	ProviderType string `db:"provider_type"`
	Path         string `db:"path"`
	Available    bool   `db:"available"`
}

type FileListItem struct {
	Hash         string `json:"hash"`
	Filename     string `json:"filename"`
	Size         int64  `json:"size"`
	MimeType     string `json:"mime_type"`
	CreatedAt    string `json:"created_at"`
	Type         string `json:"type"`
	ProviderType string `json:"provider_type"`
	ProviderPath string `json:"provider_path"`
}

type DirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// IPFSPin represents a pin record in the ipfs_pins table (M2 collection layer: originally defined in repository, moved to domain).
type IPFSPin struct {
	CID      string `json:"cid"`
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
	PinnedAt string `json:"pinned_at"`
}
