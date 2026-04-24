// Package model 定义 Peerdrive 系统的核心数据结构和 SQLite 表映射。
// 文件结构 FileMetadata：对应 files 表，包含 ID、SHA256 哈希、
//   提供者类型（local/http）、存储路径和原始文件名。
// 使用 db 标签标记数据库列名，用于 repository 层的 Scan 绑定。

package model

type FileMetadata struct {
	ID           int    `db:"id"`
	Hash         string `db:"hash"`
	ProviderType string `db:"provider_type"`
	Path         string `db:"path"`
	Filename     string `db:"filename"`
}
