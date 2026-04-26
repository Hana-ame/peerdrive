// 集合仓库 — collections、collection_entries、collection_versions、
// version_entries 四张表的 CRUD 和业务操作。
// 函数列表：
//   CreateCollection / GetOrCreateCollection — 创建/获取集合 ID（INSERT 含 current_hash）
//   ListCollections / GetCollection          — 查询集合列表/详情（SELECT 含 current_hash）
//   UpdateCurrentHash                      — 更新集合的 current_hash（Commit/Rollback 后调用）
//   AddCollectionEntry / RemoveCollectionEntry — 增删条目（upsert 语义）
//   GetCollectionEntry / ListCollectionEntries — 查询条目
//   CreateVersion / SnapshotVersionEntries   — 创建版本快照
//   GetVersionLog / GetVersionEntries        — 查询版本历史/快照内容
//   RestoreVersionEntries                   — 事务内回滚（先删后插）
//
// current_hash 迁移：已有数据库需执行 ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL

package repository

import (
	"database/sql"
	"peerdrive/internal/model"
)

func CreateCollection(username, collectionName string) (int, error) {
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, tags) VALUES (?, ?, '')`,
		username, collectionName)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func CreateCollectionWithVisibility(username, collectionName, visibility string) (int, error) {
	if visibility == "" {
		visibility = "public"
	}
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, visibility, tags) VALUES (?, ?, ?, '')`,
		username, collectionName, visibility)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func CreateCollectionWithTags(username, collectionName, visibility string, tags []string) (int, error) {
	if visibility == "" {
		visibility = "public"
	}
	tagsJSON := model.MarshalTags(tags)
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, visibility, tags) VALUES (?, ?, ?, ?)`,
		username, collectionName, visibility, tagsJSON)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

func UpdateCollectionTags(username, collectionName string, tags []string) error {
	tagsJSON := model.MarshalTags(tags)
	_, err := DB.Exec(`UPDATE collections SET tags = ? WHERE username = ? AND collection_name = ?`,
		tagsJSON, username, collectionName)
	return err
}

func SetCollectionVisibility(username, collectionName, visibility string) error {
	_, err := DB.Exec(`UPDATE collections SET visibility = ? WHERE username = ? AND collection_name = ?`,
		visibility, username, collectionName)
	return err
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

func UpdateCurrentHash(collectionID int, hash string) error {
	_, err := DB.Exec(`UPDATE collections SET current_hash = ? WHERE id = ?`, hash, collectionID)
	return err
}

func ListCollections(username string) ([]model.Collection, error) {
	rows, err := DB.Query(`SELECT id, username, collection_name, current_hash, visibility, tags, created_at FROM collections WHERE username = ? ORDER BY created_at DESC`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []model.Collection
	for rows.Next() {
		c, err := model.ScanCollection(rows)
		if err != nil {
			return nil, err
		}
		cols = append(cols, *c)
	}
	return cols, nil
}

func GetCollection(username, collectionName string) (*model.Collection, error) {
	c, err := model.ScanCollection(DB.QueryRow(`SELECT id, username, collection_name, current_hash, visibility, tags, created_at FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

func SearchCollections(query string) ([]model.Collection, error) {
	rows, err := DB.Query(`SELECT id, username, collection_name, current_hash, visibility, tags, created_at FROM collections WHERE (username LIKE ? OR collection_name LIKE ?) AND visibility = 'public' ORDER BY created_at DESC`,
		"%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []model.Collection
	for rows.Next() {
		c, err := model.ScanCollection(rows)
		if err != nil {
			return nil, err
		}
		cols = append(cols, *c)
	}
	return cols, nil
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
