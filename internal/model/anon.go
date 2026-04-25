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
	CreatedAt    string                `json:"created_at"`
}

func NewAnonCollection(name string, entries []AnonCollectionEntry) *AnonCollection {
	if entries == nil {
		entries = []AnonCollectionEntry{}
	}
	return &AnonCollection{
		Version:      1,
		FriendlyName: name,
		Entries:      entries,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

// AnonEntry 遗留兼容 — sync 和 collection 模块仍在使用
type AnonEntry struct {
	Path string  `json:"path"`
	Hash string  `json:"hash"`
	URL  *string `json:"url,omitempty"`
}
