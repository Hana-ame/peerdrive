// 文件仓库 — file_meta 和 file_providers 两表的 CRUD。
// file_meta：每个唯一文件内容一行（hash = PK）。
// file_providers：同一文件可有多份副本（不同 provider/path），用 available 标记。

package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

// ─── file_meta ──────────────────────────────────────────

// GetFileMeta 按 hash 查询文件元数据；未找到时返回 (nil, nil)。
func GetFileMeta(hash string) (*model.FileMeta, error) {
	var m model.FileMeta
	err := DB.QueryRow(
		`SELECT hash, size, created_at, mime_type, gziped, filename, type FROM file_meta WHERE hash = ?`,
		hash,
	).Scan(&m.Hash, &m.Size, &m.CreatedAt, &m.MimeType, &m.Gziped, &m.Filename, &m.Type)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// InsertFileMeta 插入一条新的文件元数据记录。
func InsertFileMeta(meta *model.FileMeta) error {
	_, err := DB.Exec(
		`INSERT INTO file_meta (hash, size, mime_type, gziped, filename, type) VALUES (?, ?, ?, ?, ?, ?)`,
		meta.Hash, meta.Size, meta.MimeType, meta.Gziped, meta.Filename, meta.Type,
	)
	return err
}

// ─── file_providers ─────────────────────────────────────

// GetFileProviders 查询指定 hash 的所有可用 provider，优先返回 local 类型。
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

// InsertFileProvider 为指定 hash 添加一个新 provider（如 local/http）。
func InsertFileProvider(hash, providerType, path string) error {
	_, err := DB.Exec(
		`INSERT INTO file_providers (hash, provider_type, path) VALUES (?, ?, ?)`,
		hash, providerType, path,
	)
	return err
}

// MarkProviderUnavailable 将指定 provider 标记为不可用。
func MarkProviderUnavailable(id int) error {
	_, err := DB.Exec(`UPDATE file_providers SET available = 0 WHERE id = ?`, id)
	return err
}

// ListAllFiles 返回所有 blob 类型文件列表，支持按 time/path/name/type/size 排序。
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
		ORDER BY ` + orderCol + ` DESC`

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
