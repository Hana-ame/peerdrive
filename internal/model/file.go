// Package model 定义 Peerdrive 系统的核心数据结构和 SQLite 表映射。
// 文件结构 FileMetadata：对应 files 表。
// Metadata 字段为 TEXT 类型，存储 JSON 格式的扩展属性，例如：
//   {"is_gzip": true, "mime_type": "application/gzip"}
// Available 字段标记某份副本是否可用，不可用时 downloader 尝试另一份。
// 使用 db 标签标记数据库列名，用于 repository 层的 Scan 绑定。

package model

type FileMetadata struct {
	ID           int    `db:"id"`
	Hash         string `db:"hash"`
	ProviderType string `db:"provider_type"`
	Path         string `db:"path"`
	Filename     string `db:"filename"`
	Metadata     string `db:"metadata"`
	Available    bool   `db:"available"`
}
