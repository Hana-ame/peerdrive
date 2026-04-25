// Package model 定义集合管理与版本控制的数据结构。
// Collection（collections 表）：用户拥有的一组命名的 path→hash 映射。
// CollectionEntry（collection_entries 表）：集合中的单个路径映射。
// CollectionVersion（collection_versions 表）：commit 时的条目快照，
//   通过 parent_version_id 形成版本链。
// VersionEntry（version_entries 表）：版本快照中的单个条目记录。

package model

type Collection struct {
	ID             int    `db:"id" json:"id"`
	Username       string `db:"username" json:"username"`
	CollectionName string `db:"collection_name" json:"collection_name"`
	CreatedAt      string `db:"created_at" json:"created_at"`
}

type CollectionEntry struct {
	ID           int    `db:"id" json:"id"`
	CollectionID int    `db:"collection_id" json:"collection_id"`
	Path         string `db:"path" json:"path"`
	FileHash     string `db:"file_hash" json:"file_hash"`
}

type CollectionVersion struct {
	ID              int    `db:"id" json:"id"`
	CollectionID    int    `db:"collection_id" json:"collection_id"`
	VersionNumber   int    `db:"version_number" json:"version_number"`
	CommitMessage   string `db:"commit_message" json:"commit_message"`
	CreatedAt       string `db:"created_at" json:"created_at"`
	ParentVersionID *int   `db:"parent_version_id" json:"parent_version_id"`
}

type VersionEntry struct {
	ID        int    `db:"id" json:"id"`
	VersionID int    `db:"version_id" json:"version_id"`
	Path      string `db:"path" json:"path"`
	FileHash  string `db:"file_hash" json:"file_hash"`
}
