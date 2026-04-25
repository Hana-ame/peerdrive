// 文件仓库 — files 表的 CRUD 操作。
// 函数列表：
//   GetFileByHash(hash)    — 取第一个可用位置（优先 local），未找到返回 (nil, nil)
//   GetLocations(hash)     — 按哈希查询所有记录（含不可用）
//   InsertFile(meta)       — 插入新文件记录（hash 可重复，同一文件多个存储位置）
//   DeleteFile(hash)       — 按哈希删除全部记录
//   MarkFileUnavailable(id) — 将指定记录标记为不可用
//   GetFileByHashTx(tx,hash)— 在事务内取第一个可用位置
//
// 同一 hash 可对应多行（同一内容在不同 provider/path 各有一份副本）。
// available=1 表示可用，available=0 表示已失效（文件被删除、provider 不可达等）。

package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

func GetFileByHash(hash string) (*model.FileMetadata, error) {
	// 优先取 local 可用记录，否则取第一个可用
	row := DB.QueryRow(`SELECT id, hash, provider_type, path, filename, metadata, available FROM files WHERE hash = ? AND available = 1 ORDER BY CASE provider_type WHEN 'local' THEN 0 ELSE 1 END ASC, id ASC LIMIT 1`, hash)
	var m model.FileMetadata
	err := row.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename, &m.Metadata, &m.Available)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func GetLocations(hash string) ([]model.FileMetadata, error) {
	rows, err := DB.Query(`SELECT id, hash, provider_type, path, filename, metadata, available FROM files WHERE hash = ?`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []model.FileMetadata
	for rows.Next() {
		var m model.FileMetadata
		if err := rows.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename, &m.Metadata, &m.Available); err != nil {
			return nil, err
		}
		locations = append(locations, m)
	}
	return locations, nil
}

func InsertFile(meta *model.FileMetadata) error {
	_, err := DB.Exec(`INSERT INTO files (hash, provider_type, path, filename, metadata, available) VALUES (?, ?, ?, ?, ?, ?)`,
		meta.Hash, meta.ProviderType, meta.Path, meta.Filename, meta.Metadata, true)
	return err
}

func DeleteFile(hash string) error {
	_, err := DB.Exec(`DELETE FROM files WHERE hash = ?`, hash)
	return err
}

func MarkFileUnavailable(id int) error {
	_, err := DB.Exec(`UPDATE files SET available = 0 WHERE id = ?`, id)
	return err
}

func GetFileByHashTx(tx *sql.Tx, hash string) (*model.FileMetadata, error) {
	row := tx.QueryRow(`SELECT id, hash, provider_type, path, filename, metadata, available FROM files WHERE hash = ? AND available = 1 ORDER BY CASE provider_type WHEN 'local' THEN 0 ELSE 1 END ASC, id ASC LIMIT 1`, hash)
	var m model.FileMetadata
	err := row.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename, &m.Metadata, &m.Available)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
