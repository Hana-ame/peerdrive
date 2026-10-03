// File repository — CRUD for file_meta and file_providers tables.
// file_meta: one row per unique file content (hash = PK).
// file_providers: a file can have multiple replicas (different provider/path), marked by available.

package repository

import (
	"database/sql"
	"peerdrive/internal/model"
	"peerdrive/pkg/hashutil"
)

// ─── file_meta ──────────────────────────────────────────

// GetFileMeta queries file metadata by hash; returns (nil, nil) if not found.
func GetFileMeta(hash string) (*model.FileMeta, error) {
	var m model.FileMeta
	err := DB.QueryRow(
		`SELECT hash, size, created_at, mime_type, gziped, filename, type, cid FROM file_meta WHERE hash = ?`,
		hash,
	).Scan(&m.Hash, &m.Size, &m.CreatedAt, &m.MimeType, &m.Gziped, &m.Filename, &m.Type, &m.CID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetFileMetaByCID queries file metadata by CID; returns (nil, nil) if not found.
func GetFileMetaByCID(cid string) (*model.FileMeta, error) {
	var m model.FileMeta
	err := DB.QueryRow(
		`SELECT hash, size, created_at, mime_type, gziped, filename, type, cid FROM file_meta WHERE cid = ?`,
		cid,
	).Scan(&m.Hash, &m.Size, &m.CreatedAt, &m.MimeType, &m.Gziped, &m.Filename, &m.Type, &m.CID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// InsertFileMeta inserts a new file metadata record, automatically computing and storing the CID.
func InsertFileMeta(meta *model.FileMeta) error {
	cid := hashutil.SHA256ToCID(meta.Hash)
	_, err := DB.Exec(
		`INSERT INTO file_meta (hash, size, mime_type, gziped, filename, type, cid) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		meta.Hash, meta.Size, meta.MimeType, meta.Gziped, meta.Filename, meta.Type, cid,
	)
	return err
}

// ─── file_providers ─────────────────────────────────────

// GetFileProviders queries all available providers for a given hash, prioritizing local type.
func GetFileProviders(hash string) ([]model.FileProvider, error) {
	rows, err := DB.Query(
		`SELECT id, hash, provider_type, path, available FROM file_providers WHERE hash = ? AND available = 1 ORDER BY CASE provider_type WHEN 'local' THEN 0 ELSE 1 END ASC, id ASC`,
		hash,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FileProvider
	for rows.Next() {
		var p model.FileProvider
		if err := rows.Scan(&p.ID, &p.Hash, &p.ProviderType, &p.Path, &p.Available); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// InsertFileProvider adds a new provider (e.g. local/http) for a given hash.
func InsertFileProvider(hash, providerType, path string) error {
	_, err := DB.Exec(
		`INSERT INTO file_providers (hash, provider_type, path) VALUES (?, ?, ?)`,
		hash, providerType, path,
	)
	return err
}

// MarkProviderUnavailable marks the specified provider as unavailable.
func MarkProviderUnavailable(id int) error {
	_, err := DB.Exec(`UPDATE file_providers SET available = 0 WHERE id = ?`, id)
	return err
}

// ListAllFiles returns all blob-type file list, supports sorting by time/path/name/type/size.
func ListAllFiles(sortBy string) ([]model.FileListItem, error) {
	orderCol := "m.created_at"
	switch sortBy {
	case "path":
		orderCol = "p.path"
	case "name":
		orderCol = "m.filename"
	case "type":
		orderCol = "m.mime_type"
	case "size":
		orderCol = "m.size"
	default:
		orderCol = "m.created_at"
	}

	query := `
		SELECT m.hash, m.filename, m.size, m.mime_type, m.created_at, m.type,
		       COALESCE(p.provider_type, ''), COALESCE(p.path, '')
		FROM file_meta m
		LEFT JOIN file_providers p ON p.hash = m.hash AND p.available = 1
		WHERE m.type = 'blob'
		GROUP BY m.hash
		ORDER BY ` + orderCol + ` DESC
		LIMIT 1000` // M11: No LIMIT → full-table materialization → memory DoS with many files (frontend pagination not implemented)

	rows, err := DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.FileListItem
	for rows.Next() {
		var f model.FileListItem
		if err := rows.Scan(&f.Hash, &f.Filename, &f.Size, &f.MimeType, &f.CreatedAt, &f.Type, &f.ProviderType, &f.ProviderPath); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}
