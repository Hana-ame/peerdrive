package metadata

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	peererr "github.com/Hana-ame/peerdrive/errors"
	"github.com/Hana-ame/peerdrive/interface"
)

// DirMimeType is what a directory advertises as its content type. It is the
// IANA-registered type for a directory, which keeps readers from having to
// special-case an empty string. Branch on FileMetadata.IsDir, not on this.
const DirMimeType = "inode/directory"

// Index scans root and registers every file and directory below it.
//
// The scan is read whole before anything is written: either the tree is read
// successfully and the database is updated in one transaction, or nothing
// changes. That matters because a directory's hash is a function of its
// children, so a half-indexed tree would publish hashes that never existed on
// disk.
//
// Re-running Index on an unchanged tree yields the same hashes. A directory's
// hash is the sorted list of its children's paths, so neither content nor
// timestamps feed it: reading a file (which bumps its atime) cannot move the
// identity of the directory above it.
//
// Files whose persisted ctime, mtime and size still match the ones on disk are
// not read again. That is now purely about not re-hashing unchanged gigabytes;
// ctime decides, because user space cannot set it back.
//
// Entries previously registered under root but no longer present on disk are
// dropped, so List keeps showing the same set that was hashed into the
// parent's manifest.
//
// It returns every registered entry, root included, ordered by path.
func (s *Store) Index(root string) ([]iface.FileMetadata, error) {
	root, err := normalizePath(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("metadata: index %s: %w", root, err)
	}
	cache, err := s.entriesUnder(root)
	if err != nil {
		return nil, err
	}

	sc := &scan{cache: cache}
	var (
		entries []iface.FileMetadata
		prune   string
	)
	switch {
	case fi.IsDir():
		if entries, err = sc.walk(root, fi); err != nil {
			return nil, err
		}
		prune = root
	case fi.Mode().IsRegular():
		m, err := sc.file(root, fi)
		if err != nil {
			return nil, err
		}
		entries = []iface.FileMetadata{m}
	default:
		return nil, fmt.Errorf("%w: %s (%s)", peererr.ErrUnsupportedType, root, fi.Mode().Type())
	}

	if err := s.commit(entries, prune); err != nil {
		return nil, err
	}
	return sortedByPath(entries), nil
}

// scan carries the rows Index already has, so an unchanged file does not have
// to be read (and does not have its atime bumped) to be re-registered.
type scan struct {
	cache map[string]iface.FileMetadata
}

// walk registers dir and everything below it. The returned slice is in
// post-order: a directory always comes after its children, because its hash is
// computed from theirs.
func (sc *scan) walk(dir string, fi fs.FileInfo) ([]iface.FileMetadata, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("metadata: read dir %s: %w", dir, err)
	}

	children := make([]iface.FileMetadata, 0, len(des))
	out := make([]iface.FileMetadata, 0, 2*len(des)+1)
	for _, de := range des {
		path := filepath.Join(dir, de.Name())
		info, err := de.Info()
		if err != nil {
			return nil, fmt.Errorf("metadata: stat %s: %w", path, err)
		}
		switch {
		case info.IsDir():
			sub, err := sc.walk(path, info)
			if err != nil {
				return nil, err
			}
			// walk appends the directory itself last.
			self := sub[len(sub)-1]
			out = append(out, sub...)
			children = append(children, self)
		case info.Mode().IsRegular():
			m, err := sc.file(path, info)
			if err != nil {
				return nil, err
			}
			out = append(out, m)
			children = append(children, m)
		default:
			// Symlinks, sockets, devices: no content of their own to address,
			// and following links invites cycles.
		}
	}

	self, err := dirMeta(dir, fi, children)
	if err != nil {
		return nil, err
	}
	return append(out, self), nil
}

// file hashes one regular file. The 512 bytes needed for MIME sniffing come
// off the same stream as the hash, so a file is read once.
func (sc *scan) file(path string, fi fs.FileInfo) (iface.FileMetadata, error) {
	ts := times(path, fi)
	if prev, ok := sc.cache[path]; ok && unchanged(prev, fi, ts) {
		return prev, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return iface.FileMetadata{}, fmt.Errorf("metadata: open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return iface.FileMetadata{}, fmt.Errorf("metadata: read %s: %w", path, err)
	}
	head = head[:n]
	if _, err := h.Write(head); err != nil {
		return iface.FileMetadata{}, fmt.Errorf("metadata: hash %s: %w", path, err)
	}
	if _, err := io.Copy(h, f); err != nil {
		return iface.FileMetadata{}, fmt.Errorf("metadata: read %s: %w", path, err)
	}

	return iface.FileMetadata{
		SHA256:   hex.EncodeToString(h.Sum(nil)),
		Filename: fi.Name(),
		FullPath: path,
		MimeType: detectMime(fi.Name(), head),
		Size:     fi.Size(),
		IsDir:    false,
		Ctime:    ts.Ctime,
		Mtime:    ts.Mtime,
		Btime:    ts.Btime,
		Atime:    ts.Atime,
	}, nil
}

// unchanged reports whether the persisted row still tells the truth about this
// path. Ctime decides: writes, chmod, chown, rename and link changes all move
// it, and user space has no call to set it back.
func unchanged(prev iface.FileMetadata, fi fs.FileInfo, ts timestamps) bool {
	if prev.IsDir || prev.SHA256 == "" {
		return false
	}
	// A zero ctime means the platform could not report one. Without it there
	// is only mtime, which anyone can rewrite with touch - so hash the file.
	if prev.Ctime.IsZero() || ts.Ctime.IsZero() {
		return false
	}
	return prev.Size == fi.Size() &&
		prev.Ctime.Equal(ts.Ctime) &&
		prev.Mtime.Equal(ts.Mtime)
}

// dirMeta turns an already-registered child list into the directory's own
// metadata. Size is the length of the manifest: the directory's content is
// exactly those bytes, so this is its real size, not a child count.
func dirMeta(dir string, fi fs.FileInfo, children []iface.FileMetadata) (iface.FileMetadata, error) {
	manifest, err := encodeManifest(children)
	if err != nil {
		return iface.FileMetadata{}, err
	}
	sum := sha256.Sum256(manifest)
	ts := times(dir, fi)

	return iface.FileMetadata{
		SHA256:   hex.EncodeToString(sum[:]),
		Filename: fi.Name(),
		FullPath: dir,
		MimeType: DirMimeType,
		Size:     int64(len(manifest)),
		IsDir:    true,
		Ctime:    ts.Ctime,
		Mtime:    ts.Mtime,
		Btime:    ts.Btime,
		Atime:    ts.Atime,
	}, nil
}

// sortedByPath imposes a deterministic order, so two identical scans return
// identical slices.
func sortedByPath(metas []iface.FileMetadata) []iface.FileMetadata {
	sort.Slice(metas, func(i, j int) bool { return metas[i].FullPath < metas[j].FullPath })
	return metas
}

func (s *Store) commit(entries []iface.FileMetadata, pruneRoot string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("metadata: begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("metadata: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, m := range entries {
		if _, err := stmt.Exec(encodeMeta(m)...); err != nil {
			return fmt.Errorf("metadata: store %s: %w", m.FullPath, err)
		}
	}
	if pruneRoot != "" {
		if err := pruneStale(tx, pruneRoot, entries); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metadata: commit transaction: %w", err)
	}
	return nil
}

// entriesUnder loads every row at or below root, keyed by path.
func (s *Store) entriesUnder(root string) (map[string]iface.FileMetadata, error) {
	prefix := pathPrefix(root)
	rows, err := s.db.Query(
		`SELECT `+selectColumns+` FROM files WHERE full_path = ? OR substr(full_path, 1, length(?)) = ?`,
		root, prefix, prefix,
	)
	if err != nil {
		return nil, fmt.Errorf("metadata: load indexed rows under %s: %w", root, err)
	}
	defer rows.Close()

	out := make(map[string]iface.FileMetadata, 64)
	for rows.Next() {
		m, err := scanMeta(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("metadata: load indexed rows under %s: %w", root, err)
		}
		out[m.FullPath] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: load indexed rows under %s: %w", root, err)
	}
	return out, nil
}

// pruneStale deletes rows inside root that the scan did not produce, i.e.
// files that were removed or moved away since the last Index.
func pruneStale(tx *sql.Tx, root string, entries []iface.FileMetadata) error {
	seen := make(map[string]struct{}, len(entries))
	for _, m := range entries {
		seen[m.FullPath] = struct{}{}
	}

	// substr() rather than LIKE: paths contain characters LIKE would treat as
	// wildcards ("_" and "%" are legal in file names).
	prefix := pathPrefix(root)
	rows, err := tx.Query(
		`SELECT full_path FROM files WHERE full_path = ? OR substr(full_path, 1, length(?)) = ?`,
		root, prefix, prefix,
	)
	if err != nil {
		return fmt.Errorf("metadata: scan for stale rows: %w", err)
	}
	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return fmt.Errorf("metadata: scan for stale rows: %w", err)
		}
		if _, ok := seen[path]; !ok {
			stale = append(stale, path)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("metadata: scan for stale rows: %w", err)
	}
	rows.Close()

	for _, path := range stale {
		if _, err := tx.Exec(`DELETE FROM files WHERE full_path = ?`, path); err != nil {
			return fmt.Errorf("metadata: drop stale %s: %w", path, err)
		}
	}
	return nil
}

// pathPrefix is root with a trailing separator, so that "/a/b" does not match
// "/a/bc". A root that already ends in a separator (a volume) keeps its own.
func pathPrefix(root string) string {
	if len(root) > 0 && os.IsPathSeparator(root[len(root)-1]) {
		return root
	}
	return root + string(os.PathSeparator)
}
