// Package iface holds the cross-module contracts of peerdrive: the metadata
// shape a caller receives, and the sentinel errors it may need to classify.
//
// It is the only package a consumer of this module has to import, and it
// depends on nothing - not even the storage engine behind it.
package iface

import "time"

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
	// IsDir marks a directory. A directory has no byte stream of its own: its
	// content is the JSON manifest of its children, so SHA256 hashes that
	// manifest and Size is its length. MimeType says inode/directory as well,
	// but that is a display string - branch on this field, not on that one.
	IsDir bool
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
