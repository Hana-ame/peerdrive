package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

func GetFileByHash(hash string) (*model.FileMetadata, error) {
	row := DB.QueryRow(`SELECT id, hash, provider_type, path, filename FROM files WHERE hash = ?`, hash)
	var m model.FileMetadata
	err := row.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func GetLocations(hash string) ([]model.FileMetadata, error) {
	rows, err := DB.Query(`SELECT id, hash, provider_type, path, filename FROM files WHERE hash = ?`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var locations []model.FileMetadata
	for rows.Next() {
		var m model.FileMetadata
		if err := rows.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename); err != nil {
			return nil, err
		}
		locations = append(locations, m)
	}
	return locations, nil
}

func InsertFile(meta *model.FileMetadata) error {
	_, err := DB.Exec(`INSERT INTO files (hash, provider_type, path, filename) VALUES (?, ?, ?, ?)`,
		meta.Hash, meta.ProviderType, meta.Path, meta.Filename)
	return err
}

func DeleteFile(hash string) error {
	_, err := DB.Exec(`DELETE FROM files WHERE hash = ?`, hash)
	return err
}

func GetFileByHashTx(tx *sql.Tx, hash string) (*model.FileMetadata, error) {
	row := tx.QueryRow(`SELECT id, hash, provider_type, path, filename FROM files WHERE hash = ?`, hash)
	var m model.FileMetadata
	err := row.Scan(&m.ID, &m.Hash, &m.ProviderType, &m.Path, &m.Filename)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}