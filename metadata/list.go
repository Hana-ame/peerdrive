package metadata

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hana-ame/peerdrive/interface"
)

// List returns what is persisted for root, without touching the filesystem.
//
// For a directory it returns the direct children in manifest order: the same
// slice that was hashed into the directory's own SHA256, so a caller can
// re-encode it to check the directory's identity. For a file it returns that
// one entry. An unregistered path yields an empty slice, not an error: "not
// indexed yet" and "indexed, empty" look the same from here.
func (s *Store) List(root string) ([]iface.FileMetadata, error) {
	dir, err := normalizePath(root)
	if err != nil {
		return nil, err
	}

	children, err := s.children(dir)
	if err != nil {
		return nil, err
	}
	if len(children) > 0 {
		return children, nil
	}

	self, err := s.byPath(dir)
	if errors.Is(err, ErrNotFound) {
		return []iface.FileMetadata{}, nil
	}
	if err != nil {
		return nil, err
	}
	if self.IsDir {
		return []iface.FileMetadata{}, nil
	}
	return []iface.FileMetadata{self}, nil
}

func (s *Store) children(dir string) ([]iface.FileMetadata, error) {
	rows, err := s.db.Query(
		`SELECT `+selectColumns+` FROM files WHERE parent = ? ORDER BY ctime DESC, filename ASC, full_path ASC`,
		dir,
	)
	if err != nil {
		return nil, fmt.Errorf("metadata: list %s: %w", dir, err)
	}
	defer rows.Close()

	out := make([]iface.FileMetadata, 0, 16)
	for rows.Next() {
		m, err := scanMeta(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("metadata: list %s: %w", dir, err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: list %s: %w", dir, err)
	}
	return out, nil
}

func (s *Store) byPath(path string) (iface.FileMetadata, error) {
	row := s.db.QueryRow(`SELECT `+selectColumns+` FROM files WHERE full_path = ?`, path)
	m, err := scanMeta(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return iface.FileMetadata{}, fmt.Errorf("%w: path %s", ErrNotFound, path)
	}
	if err != nil {
		return iface.FileMetadata{}, fmt.Errorf("metadata: lookup %s: %w", path, err)
	}
	return m, nil
}
