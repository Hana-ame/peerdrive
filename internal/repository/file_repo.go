// 文件仓库 — files 表的 CRUD 操作。
// 函数列表：
//   GetFileByHash(hash)     — 按哈希查询单个文件元数据，未找到返回 (nil, nil)
//   GetLocations(hash)      — 按哈希查询所有位置（同一文件可能多地存储）
//   InsertFile(meta)        — 插入新文件记录，哈希冲突返回错误
//   DeleteFile(hash)        — 按哈希删除文件记录
//   GetFileByHashTx(tx,hash)— 在事务内按哈希查询
//
// metadata 列：TEXT DEFAULT '{}'，存储 JSON 扩展属性。
//   例如 {"is_gzip": true, "mime_type": "application/gzip"}
//   所有文件级扩展属性均放在 metadata 中，不新增专用列。

package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

func GetFileByHash(hash string) (*model.FileMetadata, error) {
	row := DB.QueryRow(`SELECT id, hash, provider_type, path, filename, metadata FROM files WHERE hash = ?`, hash)
	var m model.FileMetadata
	err := row.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename, &m.Metadata)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func GetLocations(hash string) ([]model.FileMetadata, error) {
	rows, err := DB.Query(`SELECT id, hash, provider_type, path, filename, metadata FROM files WHERE hash = ?`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []model.FileMetadata
	for rows.Next() {
		var m model.FileMetadata
		if err := rows.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename, &m.Metadata); err != nil {
			return nil, err
		}
		locations = append(locations, m)
	}
	return locations, nil
}

func InsertFile(meta *model.FileMetadata) error {
	_, err := DB.Exec(`INSERT INTO files (hash, provider_type, path, filename, metadata) VALUES (?, ?, ?, ?, ?)`,
		meta.Hash, meta.ProviderType, meta.Path, meta.Filename, meta.Metadata)
	return err
}

func DeleteFile(hash string) error {
	_, err := DB.Exec(`DELETE FROM files WHERE hash = ?`, hash)
	return err
}

func GetFileByHashTx(tx *sql.Tx, hash string) (*model.FileMetadata, error) {
	row := tx.QueryRow(`SELECT id, hash, provider_type, path, filename, metadata FROM files WHERE hash = ?`, hash)
	var m model.FileMetadata
	err := row.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename, &m.Metadata)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
