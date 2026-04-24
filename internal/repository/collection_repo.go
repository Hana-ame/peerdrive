package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

func CreateCollection(username, collectionName string) (int, error) {
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name) VALUES (?, ?)`,
		username, collectionName)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func GetOrCreateCollection(username, collectionName string) (int, error) {
	var id int
	err := DB.QueryRow(`SELECT id FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName).Scan(&id)
	if err == sql.ErrNoRows {
		return CreateCollection(username, collectionName)
	}
	return id, err
}

func ListCollections(username string) ([]model.Collection, error) {
	rows, err := DB.Query(`SELECT id, username, collection_name, created_at FROM collections WHERE username = ? ORDER BY created_at DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []model.Collection
	for rows.Next() {
		var c model.Collection
		if err := rows.Scan(&c.ID, &c.Username, &c.CollectionName, &c.CreatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, nil
}

func GetCollection(username, collectionName string) (*model.Collection, error) {
	var c model.Collection
	err := DB.QueryRow(`SELECT id, username, collection_name, created_at FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName).Scan(&c.ID, &c.Username, &c.CollectionName, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func AddCollectionEntry(collectionID int, path, fileHash string) error {
	_, err := DB.Exec(`INSERT INTO collection_entries (collection_id, path, file_hash) VALUES (?, ?, ?)
		ON CONFLICT(collection_id, path) DO UPDATE SET file_hash = excluded.file_hash`,
		collectionID, path, fileHash)
	return err
}

func RemoveCollectionEntry(collectionID int, path string) error {
	_, err := DB.Exec(`DELETE FROM collection_entries WHERE collection_id = ? AND path = ?`,
		collectionID, path)
	return err
}

func GetCollectionEntry(collectionID int, path string) (*model.CollectionEntry, error) {
	var e model.CollectionEntry
	err := DB.QueryRow(`SELECT id, collection_id, path, file_hash FROM collection_entries WHERE collection_id = ? AND path = ?`,
		collectionID, path).Scan(&e.ID, &e.CollectionID, &e.Path, &e.FileHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func ListCollectionEntries(collectionID int) ([]model.CollectionEntry, error) {
	rows, err := DB.Query(`SELECT id, collection_id, path, file_hash FROM collection_entries WHERE collection_id = ?`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []model.CollectionEntry
	for rows.Next() {
		var e model.CollectionEntry
		if err := rows.Scan(&e.ID, &e.CollectionID, &e.Path, &e.FileHash); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func CreateVersion(collectionID int, commitMsg string, parentVersionID *int) (int, int, error) {
	var maxVer int
	err := DB.QueryRow(`SELECT COALESCE(MAX(version_number), 0) FROM collection_versions WHERE collection_id = ?`,
		collectionID).Scan(&maxVer)
	if err != nil {
		return 0, 0, err
	}
	newVer := maxVer + 1
	var res sql.Result
	if parentVersionID != nil {
		res, err = DB.Exec(`INSERT INTO collection_versions (collection_id, version_number, commit_message, parent_version_id) VALUES (?, ?, ?, ?)`,
			collectionID, newVer, commitMsg, *parentVersionID)
	} else {
		res, err = DB.Exec(`INSERT INTO collection_versions (collection_id, version_number, commit_message) VALUES (?, ?, ?)`,
			collectionID, newVer, commitMsg)
	}
	if err != nil {
		return 0, 0, err
	}
	id, _ := res.LastInsertId()
	return int(id), newVer, nil
}

func SnapshotVersionEntries(versionID, collectionID int) error {
	_, err := DB.Exec(`INSERT INTO version_entries (version_id, path, file_hash) SELECT ?, path, file_hash FROM collection_entries WHERE collection_id = ?`,
		versionID, collectionID)
	return err
}

func GetVersionLog(collectionID int) ([]model.CollectionVersion, error) {
	rows, err := DB.Query(`SELECT id, collection_id, version_number, commit_message, created_at, parent_version_id FROM collection_versions WHERE collection_id = ? ORDER BY version_number DESC`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []model.CollectionVersion
	for rows.Next() {
		var v model.CollectionVersion
		if err := rows.Scan(&v.ID, &v.CollectionID, &v.VersionNumber, &v.CommitMessage, &v.CreatedAt, &v.ParentVersionID); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, nil
}

func GetVersionEntries(versionID int) ([]model.VersionEntry, error) {
	rows, err := DB.Query(`SELECT id, version_id, path, file_hash FROM version_entries WHERE version_id = ?`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []model.VersionEntry
	for rows.Next() {
		var e model.VersionEntry
		if err := rows.Scan(&e.ID, &e.VersionID, &e.Path, &e.FileHash); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func RestoreVersionEntries(versionID, collectionID int) error {
	entries, err := GetVersionEntries(versionID)
	if err != nil {
		return err
	}
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM collection_entries WHERE collection_id = ?`, collectionID)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_, err = tx.Exec(`INSERT INTO collection_entries (collection_id, path, file_hash) VALUES (?, ?, ?)`,
			collectionID, e.Path, e.FileHash)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
