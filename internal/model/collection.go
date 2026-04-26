// Package model 定义集合管理与版本控制的数据结构。
// Collection（collections 表）：用户拥有的一组命名的 path→hash 映射。
//   CurrentHash 字段：指向当前 commit 生成的 AnonCollection JSON 的 SHA256。
//   每次 Commit 时生成新的 hash 并更新此字段；回滚时也会更新。
//   GetCollection 优先通过 CurrentHash 返回内容，fallback 到 collection_entries 表。
// CollectionEntry（collection_entries 表）：集合中的单个路径映射（工作区）。
// CollectionVersion（collection_versions 表）：commit 时的条目快照，
//   通过 parent_version_id 形成版本链。
// VersionEntry（version_entries 表）：版本快照中的单个条目记录。

package model

import (
	"database/sql"
	"encoding/json"
)

type Collection struct {
	ID             int      `db:"id" json:"id"`
	Username       string   `db:"username" json:"username"`
	CollectionName string   `db:"collection_name" json:"collection_name"`
	CurrentHash    *string  `db:"current_hash" json:"current_hash"`
	Visibility     string   `db:"visibility" json:"visibility"`
	Tags           []string `db:"tags" json:"tags"`
	CreatedAt      string   `db:"created_at" json:"created_at"`
}

type collectionRow struct {
	ID             int
	Username       string
	CollectionName string
	CurrentHash    *string
	Visibility     string
	Tags           sql.NullString
	CreatedAt      string
}

func (c *Collection) ScanRow(s Scanner, columns ...string) error {
	var r collectionRow
	v := &r
	v.ID = c.ID
	v.Username = c.Username
	v.CurrentHash = c.CurrentHash
	v.Visibility = c.Visibility
	v.CreatedAt = c.CreatedAt
	return nil
}

type Scanner interface {
	Scan(...interface{}) error
}

func ScanCollection(scanner interface{ Scan(...interface{}) error }) (*Collection, error) {
	var c Collection
	var tagsStr sql.NullString
	err := scanner.Scan(&c.ID, &c.Username, &c.CollectionName, &c.CurrentHash, &c.Visibility, &tagsStr, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	if tagsStr.Valid && tagsStr.String != "" {
		json.Unmarshal([]byte(tagsStr.String), &c.Tags)
	}
	if c.Tags == nil {
		c.Tags = []string{}
	}
	return &c, nil
}

func MarshalTags(tags []string) string {
	if tags == nil {
		return ""
	}
	data, _ := json.Marshal(tags)
	return string(data)
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
