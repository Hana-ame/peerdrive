// Package metadata implements peerdrive's "hash to metadata" module: a
// persistent, content-addressed map from SHA256 to the file that currently
// holds that content.
//
// # The three calls
//
//	Stat(sha)    resolve a hash to one FileMetadata
//	Index(root)  scan root and register everything under it, idempotently
//	List(root)   read back what is persisted, without touching the filesystem
//
// # Directories are files too
//
// A directory has no byte stream of its own, so its content is defined as the
// JSON encoding of its direct children ([]iface.FileMetadata, ordered by Ctime
// descending, Filename ascending as the tie break), and its SHA256 is the hash
// of those bytes. A directory hash therefore pins the whole subtree below it,
// and List(dir) returns exactly the slice that was hashed - re-encoding it
// must reproduce the parent's hash.
//
// Because the manifest is the literal JSON of FileMetadata, everything in
// that struct is part of the identity, including FullPath and the timestamps.
// The same tree copied to another path hashes differently. That is what the
// contract in AGENTS.md asks for; it is not a portable git-tree style hash.
//
// # Identity vs. location
//
// The table is keyed by path, not by hash: a path holds one content at a time,
// while one content may live at many paths. Stat(sha) resolves the ambiguity
// by returning the newest (Ctime) copy with a deterministic path tie break.
//
// # Limits worth knowing
//
//   - Symlinks and other irregular files are skipped: they carry no content of
//     their own, and following them invites cycles.
//   - Index is a full scan of root. Rows previously registered under a
//     directory but gone from disk are removed, so List keeps matching the
//     manifest. Nothing is deleted unless the scan of that subtree succeeded.
//   - Timestamps are stored as UnixNano in UTC. Btime needs statx on Linux and
//     FILE_BASIC_INFO on Windows; where the kernel cannot tell, the field stays
//     zero rather than being faked.
package metadata
