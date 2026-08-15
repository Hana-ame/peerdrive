// Package model 定义 Peerdrive 系统的核心数据结构和 SQLite 表映射。
// FileMeta 为每个唯一文件内容的元数据（hash 是主键）。
// FileProvider 为每份副本的存储位置（同一 hash 可多个 provider）。
// 使用 db 标签标记数据库列名，用于 repository 层的 Scan 绑定。

package model

// 文件类型常量（领域常量，原定义在 repository 包——M2 收层时上移到 domain，
// 让 repository/controller 统一依赖 model 而非互相/反向引用）。
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

// IPFSPin 表示 ipfs_pins 表中的一条 pin 记录（M2 收层：原定义在 repository，上移 domain）。
type IPFSPin struct {
	CID      string `json:"cid"`
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Filename string `json:"filename"`
	PinnedAt string `json:"pinned_at"`
}
