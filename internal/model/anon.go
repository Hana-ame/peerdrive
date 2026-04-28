// Package model 定义匿集合（AnonCollection）及其条目的数据结构。
// 匿名集合通过 SHA256 内容寻址存储，支持版本递增和标签。
package model

import "time"

type AnonCollectionEntry struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

type AnonCollection struct {
	Version      int                   `json:"version"`
	FriendlyName string                `json:"friendly_name,omitempty"`
	Entries      []AnonCollectionEntry `json:"entries"`
	Tags         []string              `json:"tags,omitempty"`
	CreatedAt    string                `json:"created_at"`
}

// NewAnonCollection 创建一个新的匿名集合，初始版本为 1，创建时间为当前 UTC 时间。
func NewAnonCollection(name string, entries []AnonCollectionEntry, tags []string) *AnonCollection {
	if entries == nil {
		entries = []AnonCollectionEntry{}
	}
	if tags == nil {
		tags = []string{}
	}
	return &AnonCollection{
		Version:      1,
		FriendlyName: name,
		Entries:      entries,
		Tags:         tags,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

// AnonEntry 遗留兼容 — sync 和 collection 模块仍在使用
type AnonEntry struct {
	Path string  `json:"path"`
	Hash string  `json:"hash"`
	URL  *string `json:"url,omitempty"`
}

type AnonCollectionSummary struct {
	Hash         string   `json:"hash"`
	FriendlyName string   `json:"friendly_name,omitempty"`
	NamePreview  string   `json:"name_preview,omitempty"`
	Version      int      `json:"version"`
	Tags         []string `json:"tags,omitempty"`
	EntryCount   int      `json:"entry_count"`
	CreatedAt    string   `json:"created_at"`
}
