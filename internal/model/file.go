// Package model 定义 Peerdrive 系统的核心数据结构和 SQLite 表映射。
// 文件结构 FileMetadata：对应 files 表。
// Metadata 字段为 TEXT 类型，存储 JSON 格式的扩展属性，例如：
//   {"is_gzip": true, "mime_type": "application/gzip"}
// 下载时根据 Metadata 中的 is_gzip 决定是否设置 Content-Encoding: gzip。
// 新增任何属性都只需修改 JSON 内容，无需改表结构。
// 使用 db 标签标记数据库列名，用于 repository 层的 Scan 绑定。

package model

type FileMetadata struct {
	ID           int    `db:"id"`
	Hash         string `db:"hash"`
	ProviderType string `db:"provider_type"`
	Path         string `db:"path"`
	Filename     string `db:"filename"`
	Metadata     string `db:"metadata"`
}
