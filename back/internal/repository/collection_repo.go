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
	"encoding/json"

	"peerdrive/internal/model"
)

// CreateCollection 为指定用户创建一个新集合，返回集合 ID。
func CreateCollection(username, collectionName string) (int, error) {
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, tags) VALUES (?, ?, '')`,
		username, collectionName)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// CreateCollectionWithVisibility 创建集合并指定可见性（public/unlisted/private）。
func CreateCollectionWithVisibility(username, collectionName, visibility string) (int, error) {
	if visibility == "" {
		visibility = "public"
	}
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, visibility, tags, follow_redirects) VALUES (?, ?, ?, '', 1)`,
		username, collectionName, visibility)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// CreateCollectionWithTags 创建集合并设置标签和可见性。
func CreateCollectionWithTags(username, collectionName, visibility string, tags []string) (int, error) {
	if visibility == "" {
		visibility = "public"
	}
	tagsJSON := model.MarshalTags(tags)
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, visibility, tags, follow_redirects) VALUES (?, ?, ?, ?, 1)`,
		username, collectionName, visibility, tagsJSON)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// CreateCollectionWithFull 创建集合并指定全部属性（可见性、重定向跟随、标签）。
func CreateCollectionWithFull(username, collectionName, visibility string, followRedirects bool, tags []string) (int, error) {
	if visibility == "" {
		visibility = "public"
	}
	fr := 0
	if followRedirects {
		fr = 1
	}
	tagsJSON := model.MarshalTags(tags)
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, visibility, tags, follow_redirects) VALUES (?, ?, ?, ?, ?)`,
		username, collectionName, visibility, tagsJSON, fr)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// UpdateCollectionTags 替换集合的标签列表。
func UpdateCollectionTags(username, collectionName string, tags []string) error {
	tagsJSON := model.MarshalTags(tags)
	_, err := DB.Exec(`UPDATE collections SET tags = ? WHERE username = ? AND collection_name = ?`,
		tagsJSON, username, collectionName)
	return err
}

// SetCollectionVisibility 更新集合的可见性属性。
func SetCollectionVisibility(username, collectionName, visibility string) error {
	_, err := DB.Exec(`UPDATE collections SET visibility = ? WHERE username = ? AND collection_name = ?`,
		visibility, username, collectionName)
	return err
}

// GetOrCreateCollection 按用户名和集合名查询集合，不存在则自动创建。
func GetOrCreateCollection(username, collectionName string) (int, error) {
	var id int
	err := DB.QueryRow(`SELECT id FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName).Scan(&id)
	if err == sql.ErrNoRows {
		return CreateCollection(username, collectionName)
	}
	return id, err
}

// UpdateCurrentHash 更新集合的 current_hash 指针（指向最新的匿名集合快照）。
func UpdateCurrentHash(collectionID int, hash string) error {
	_, err := DB.Exec(`UPDATE collections SET current_hash = ? WHERE id = ?`, hash, collectionID)
	return err
}

// ListCollections 查询指定用户的所有集合，按创建时间倒序排列。
// M11：无 LIMIT 全表物化 → LIMIT 1000。
func ListCollections(username string) ([]model.Collection, error) {
	rows, err := DB.Query(`SELECT id, username, collection_name, current_hash, visibility, follow_redirects, tags, created_at FROM collections WHERE username = ? ORDER BY created_at DESC LIMIT 1000`, username)
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

// GetCollection 按用户名和集合名查询单个集合；未找到时返回 (nil, nil)。
func GetCollection(username, collectionName string) (*model.Collection, error) {
	c, err := model.ScanCollection(DB.QueryRow(`SELECT id, username, collection_name, current_hash, visibility, follow_redirects, tags, created_at FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// SearchCollections 在公开集合中按用户名或集合名模糊搜索。
// M11：LIKE %q% 无 LIMIT → 全表扫描 + 物化；限 100 条结果（搜索场景足够）。
func SearchCollections(query string) ([]model.Collection, error) {
	rows, err := DB.Query(`SELECT id, username, collection_name, current_hash, visibility, follow_redirects, tags, created_at FROM collections WHERE (username LIKE ? OR collection_name LIKE ?) AND visibility = 'public' ORDER BY created_at DESC LIMIT 100`,
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

// AddCollectionEntry 插入或更新集合中的一条 path->fileHash 映射（upsert 语义），同时存储 providers_json。
func AddCollectionEntry(collectionID int, path, fileHash string) error {
	providers := []model.Provider{{Type: "sha256", Value: fileHash}}
	providersJSON, _ := json.Marshal(providers)
	_, err := DB.Exec(`INSERT INTO collection_entries (collection_id, path, file_hash, providers_json) VALUES (?, ?, ?, ?)
		ON CONFLICT(collection_id, path) DO UPDATE SET file_hash = excluded.file_hash, providers_json = excluded.providers_json`,
		collectionID, path, fileHash, string(providersJSON))
	return err
}

// AddProviderCollectionEntry 插入或更新集合条目，附带完整的 providers 数组。
func AddProviderCollectionEntry(collectionID int, path string, providers []model.Provider) error {
	primaryHash := ""
	for _, p := range providers {
		if p.Type == "sha256" && p.Value != "" {
			primaryHash = p.Value
			break
		}
	}
	if providers == nil {
		providers = []model.Provider{}
	}
	providersJSON, _ := json.Marshal(providers)
	_, err := DB.Exec(`INSERT INTO collection_entries (collection_id, path, file_hash, providers_json) VALUES (?, ?, ?, ?)
		ON CONFLICT(collection_id, path) DO UPDATE SET file_hash = excluded.file_hash, providers_json = excluded.providers_json`,
		collectionID, path, primaryHash, string(providersJSON))
	return err
}

// RemoveCollectionEntry 从集合中删除指定 path 的条目。
func RemoveCollectionEntry(collectionID int, path string) error {
	_, err := DB.Exec(`DELETE FROM collection_entries WHERE collection_id = ? AND path = ?`,
		collectionID, path)
	return err
}

// GetCollectionEntry 查询集合中指定 path 的条目；未找到时返回 (nil, nil)。
func GetCollectionEntry(collectionID int, path string) (*model.CollectionEntry, error) {
	var e model.CollectionEntry
	var pj sql.NullString
	err := DB.QueryRow(`SELECT id, collection_id, path, file_hash, COALESCE(providers_json, '') FROM collection_entries WHERE collection_id = ? AND path = ?`,
		collectionID, path).Scan(&e.ID, &e.CollectionID, &e.Path, &e.FileHash, &pj)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if pj.Valid {
		e.ProvidersJSON = pj.String
	}
	return &e, nil
}

// ListCollectionEntries 查询集合中的所有条目，包含 providers_json。
// M11：条目数是集合数据本体，理论上必须全量……但恶意构造大集合会全表物化。
// 限 10000（正常集合同步批次远小于此；异常大集合走分页重构而非全量爆内存）。
func ListCollectionEntries(collectionID int) ([]model.CollectionEntry, error) {
	rows, err := DB.Query(`SELECT id, collection_id, path, file_hash, COALESCE(providers_json, '') FROM collection_entries WHERE collection_id = ? LIMIT 10000`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []model.CollectionEntry
	for rows.Next() {
		var e model.CollectionEntry
		var pj string
		if err := rows.Scan(&e.ID, &e.CollectionID, &e.Path, &e.FileHash, &pj); err != nil {
			return nil, err
		}
		e.ProvidersJSON = pj
		entries = append(entries, e)
	}
	return entries, nil
}

// CreateVersion 为集合创建新版本快照记录，返回版本 ID 和版本号。
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

// SnapshotVersionEntries 将集合当前条目快照（含 providers_json）复制到 version_entries。
func SnapshotVersionEntries(versionID, collectionID int) error {
	_, err := DB.Exec(`INSERT INTO version_entries (version_id, path, file_hash, providers_json) SELECT ?, path, file_hash, COALESCE(providers_json, '') FROM collection_entries WHERE collection_id = ?`,
		versionID, collectionID)
	return err
}

// GetVersionLog 返回集合的版本历史，按版本号倒序排列。
// M11：无 LIMIT → 无限版本历史全表物化；限 1000（历史回滚 UI 展示最近版本即可）。
func GetVersionLog(collectionID int) ([]model.CollectionVersion, error) {
	rows, err := DB.Query(`SELECT id, collection_id, version_number, commit_message, created_at, parent_version_id FROM collection_versions WHERE collection_id = ? ORDER BY version_number DESC LIMIT 1000`, collectionID)
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

// GetVersionEntries 查询指定版本快照中的全部条目列表（含 providers_json）。
// M11：同 ListCollectionEntries，限 10000。
func GetVersionEntries(versionID int) ([]model.VersionEntry, error) {
	rows, err := DB.Query(`SELECT id, version_id, path, file_hash, COALESCE(providers_json, '') FROM version_entries WHERE version_id = ? LIMIT 10000`, versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []model.VersionEntry
	for rows.Next() {
		var e model.VersionEntry
		var pj string
		if err := rows.Scan(&e.ID, &e.VersionID, &e.Path, &e.FileHash, &pj); err != nil {
			return nil, err
		}
		e.ProvidersJSON = pj
		entries = append(entries, e)
	}
	return entries, nil
}

// RestoreVersionEntries 在事务内将集合条目回滚到指定版本的快照内容（先删后插，含 providers_json）。
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
		_, err = tx.Exec(`INSERT INTO collection_entries (collection_id, path, file_hash, providers_json) VALUES (?, ?, ?, ?)`,
			collectionID, e.Path, e.FileHash, e.ProvidersJSON)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
