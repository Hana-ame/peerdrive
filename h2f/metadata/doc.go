// Package metadata implements peerdrive's "hash to metadata" module: a
// persistent, content-addressed map from SHA256 to the file that currently
// holds that content.
//
// # The calls
//
//	Stat(sha)         resolve a hash to one FileMetadata
//	Index(root)       scan root and register everything under it, idempotently
//	List(root)        read back what is persisted, without touching the filesystem
//	Collection(paths) resolve a list of registered paths to their metadata
//
// # Directories are files too
//
// A directory has no byte stream of its own, so its content is defined as the
// JSON array of its children's absolute paths, in scan order:
//
//	["/data/photos/a.jpg", "/data/photos/raw"]
//
// Its own SHA256 is the hash of those bytes. The manifest holds locations, not
// content: editing a child leaves its parent's hash alone, while adding,
// removing, renaming or moving one moves it. Identity is per file; structure is
// per directory.
//
// The array is not sorted. os.ReadDir hands children back by name, so the order
// is name order in practice and two scans agree; but the rule is "whatever the
// scan produced", not "alphabetical".
//
// List(dir) returns the children in ctime order, which is what a reader wants
// to see, not the order that was hashed. To verify a directory, take the
// FullPath out of its children, sort them, JSON-encode, and hash that.
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
//     their own, and following them invites cycles. Using one as the root of
//     Index reports errors.ErrUnsupportedType instead of guessing.
//   - Index is a full scan of root. Rows previously registered under a
//     directory but gone from disk are removed, so List keeps matching the
//     manifest. Nothing is deleted unless the scan of that subtree succeeded.
//   - Timestamps are stored as UnixNano in UTC. Btime needs statx on Linux and
//     FILE_BASIC_INFO on Windows; where the kernel cannot tell, the field stays
//     zero rather than being faked.
//
// # Errors
//
// The sentinels live in github.com/Hana-ame/peerdrive/h2f/interface, not here, so a
// caller can classify a failure without linking this package's SQLite driver.
// The names are re-exported below as aliases of the same values, which means
// errors.Is matches either spelling.
package metadata
