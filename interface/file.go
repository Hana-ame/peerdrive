// Package iface holds the cross-module contracts of peerdrive.
package iface

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Store.Stat when no entry exists for the hash.
var ErrNotFound = errors.New("iface: hash not found")

// FileMetadata describes one file resolved from a content hash.
//
// FullPath is where the content currently lives, not a handle: opening it is
// the caller's concern. Size is the byte count of the content, so a caller can
// size a read buffer without stat-ing again.
type FileMetadata struct {
	SHA256   string
	Filename string
	FullPath string
	MimeType string
	Size     int64
	// Ctime is the only timestamp that carries evidence: user space has no call
	// to set it, so a content change followed by restoring Mtime still pushes
	// Ctime forward.
	Ctime time.Time
	// Mtime is writable by anyone (touch -m, utimes); rsync and make read it as
	// "rebuild needed", not as proof of anything.
	Mtime time.Time
	// Btime is inode birth time: written once, not settable from user space on
	// Linux (debugfs aside), settable on NTFS, only exposed via statx.
	Btime time.Time
	// Atime is unreliable: relatime updates it rarely, noatime never.
	Atime time.Time
}

// Store is the hash-to-file model: a content hash is the identity, paths are
// where that content currently lives.
type Store interface {
	// Stat returns the metadata for one content hash. The argument is a hash,
	// not a path; a missing entry returns ErrNotFound.
	Stat(ctx context.Context, hash string) (FileMetadata, error)

	// Scan walks root and reports every file it finds without recording
	// anything. Returning a non-nil error from fn stops the walk.
	Scan(ctx context.Context, root string, fn func(FileMetadata) error) error

	// Index walks root and records what it finds. Repeating it on an unchanged
	// tree creates no new entries.
	Index(ctx context.Context, root string) (IndexResult, error)
}

// IndexResult counts what one Index call did.
type IndexResult struct {
	Added   int
	Updated int
	Removed int
	Skipped int
	Failed  int
}
