package metadata

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/Hana-ame/peerdrive/h2f/interface"
)

// Stat resolves sha to the file that currently holds that content.
//
// Content is not unique to a path: the same bytes may sit at several places at
// once. The freshest copy wins (Ctime descending), with the path as a
// deterministic tie break, so repeated calls return the same answer.
func (s *Store) Stat(sha string) (iface.FileMetadata, error) {
	hash, err := normalizeHash(sha)
	if err != nil {
		return iface.FileMetadata{}, err
	}
	row := s.db.QueryRow(
		`SELECT `+selectColumns+` FROM files WHERE sha256 = ? ORDER BY ctime DESC, full_path ASC LIMIT 1`,
		hash,
	)
	m, err := scanMeta(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return iface.FileMetadata{}, fmt.Errorf("%w: sha256 %s", ErrNotFound, hash)
	}
	if err != nil {
		return iface.FileMetadata{}, fmt.Errorf("metadata: stat %s: %w", hash, err)
	}
	return m, nil
}
