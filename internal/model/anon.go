// Package model 定义匿名集合元数据 JSON 的数据结构。
// AnonCollection 按 hash 寻址，与普通文件同一存储层。
// Version 固定为 1，序列化时字段顺序固定以保证 SHA256 一致。
// Entries 在序列化前必须按 Path 字典序排序。
// Hash = SHA256(canonical JSON)，即集合的唯一标识符。
// JSON 文件与普通 blob 文件存储在相同路径：storage/{hash[:2]}/{hash}
package model

// 文件类型常量
const (
	FileTypeBlob           = "blob"
	FileTypeAnonCollection = "anon_collection"
)

// AnonCollection 匿名集合元数据
type AnonCollection struct {
	Version     int          `json:"version"`
	Name        string       `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	CreatedAt   string       `json:"created_at"`
	Entries     []AnonEntry  `json:"entries"`
}

// AnonEntry 匿名集合中的文件条目
type AnonEntry struct {
	Path string  `json:"path"`
	Hash string  `json:"hash"`
	URL  *string `json:"url,omitempty"`
}
