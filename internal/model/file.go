// Package model defines data structures for the Peerdrive system.
// Includes FileMetadata (content-addressed file records),
// Collection/CollectionEntry (user-managed file sets),
// CollectionVersion/VersionEntry (version snapshots),
// and TransferTask (async operation tracking).

package model

type FileMetadata struct {
	ID           int    `db:"id"`
	Hash         string `db:"hash"`
	ProviderType string `db:"provider_type"`
	Path         string `db:"path"`
	Filename     string `db:"filename"`
}
