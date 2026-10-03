// Collection repository — CRUD and business operations for collections,
// collection_entries, collection_versions, and version_entries tables.
// Function list:
//   CreateCollection / GetOrCreateCollection — create/get collection ID (INSERT with current_hash)
//   ListCollections / GetCollection          — query collection list/details (SELECT with current_hash)
//   UpdateCurrentHash                      — update collection's current_hash (called after Commit/Rollback)
//   AddCollectionEntry / RemoveCollectionEntry — add/remove entries (upsert semantics)
//   GetCollectionEntry / ListCollectionEntries — query entries
//   CreateVersion / SnapshotVersionEntries   — create version snapshot
//   GetVersionLog / GetVersionEntries        — query version history/snapshot content
//   RestoreVersionEntries                   — rollback within transaction (delete then insert)
//
// current_hash migration: existing databases need ALTER TABLE collections ADD COLUMN current_hash TEXT DEFAULT NULL

package repository

import (
	"database/sql"
	"encoding/json"

	"peerdrive/internal/model"
)

// CreateCollection creates a new collection for the given user, returns the collection ID.
func CreateCollection(username, collectionName string) (int, error) {
	res, err := DB.Exec(`INSERT INTO collections (username, collection_name, tags) VALUES (?, ?, '')`,
		username, collectionName)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return int(id), err
}

// CreateCollectionWithVisibility creates a collection with specified visibility (public/unlisted/private).
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

// CreateCollectionWithTags creates a collection with tags and visibility.
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

// CreateCollectionWithFull creates a collection with all attributes specified (visibility, redirect following, tags).
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

// UpdateCollectionTags replaces the collection's tag list.
func UpdateCollectionTags(username, collectionName string, tags []string) error {
	tagsJSON := model.MarshalTags(tags)
	_, err := DB.Exec(`UPDATE collections SET tags = ? WHERE username = ? AND collection_name = ?`,
		tagsJSON, username, collectionName)
	return err
}

// SetCollectionVisibility updates the collection's visibility attribute.
func SetCollectionVisibility(username, collectionName, visibility string) error {
	_, err := DB.Exec(`UPDATE collections SET visibility = ? WHERE username = ? AND collection_name = ?`,
		visibility, username, collectionName)
	return err
}

// GetOrCreateCollection queries a collection by username and name; auto-creates if not found.
func GetOrCreateCollection(username, collectionName string) (int, error) {
	var id int
	err := DB.QueryRow(`SELECT id FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName).Scan(&id)
	if err == sql.ErrNoRows {
		return CreateCollection(username, collectionName)
	}
	return id, err
}

// UpdateCurrentHash updates the collection's current_hash pointer (pointing to the latest anonymous collection snapshot).
func UpdateCurrentHash(collectionID int, hash string) error {
	_, err := DB.Exec(`UPDATE collections SET current_hash = ? WHERE id = ?`, hash, collectionID)
	return err
}

// ListCollections queries all collections for a given user, ordered by creation time descending.
// M11: No LIMIT → full-table materialization; LIMIT 1000.
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

// ListPublicCollections lists all public collections; q filters by username/collection name when non-empty.
// M2 layering: original logic lived in controller (collection.go ListPublicCollections ran raw SQL),
// consolidated into repository; controller only depends on service.
func ListPublicCollections(q string) ([]model.Collection, error) {
	var rows *sql.Rows
	var err error
	if q != "" {
		rows, err = DB.Query(`SELECT id, username, collection_name, current_hash, visibility, follow_redirects, tags, created_at FROM collections WHERE visibility = 'public' AND (username LIKE ? OR collection_name LIKE ?) ORDER BY created_at DESC LIMIT 1000`, "%"+q+"%", "%"+q+"%")
	} else {
		rows, err = DB.Query(`SELECT id, username, collection_name, current_hash, visibility, follow_redirects, tags, created_at FROM collections WHERE visibility = 'public' ORDER BY created_at DESC LIMIT 1000`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []model.Collection
	for rows.Next() {
		col, err := model.ScanCollection(rows)
		if err != nil {
			continue
		}
		cols = append(cols, *col)
	}
	return cols, rows.Err()
}

// GetCollection queries a single collection by username and name; returns (nil, nil) if not found.
func GetCollection(username, collectionName string) (*model.Collection, error) {
	c, err := model.ScanCollection(DB.QueryRow(`SELECT id, username, collection_name, current_hash, visibility, follow_redirects, tags, created_at FROM collections WHERE username = ? AND collection_name = ?`,
		username, collectionName))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// SearchCollections fuzzy searches public collections by username or collection name.
// M11: LIKE %q% with no LIMIT → full-table scan + materialization; limit to 100 results (sufficient for search).
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

// AddCollectionEntry inserts or updates a path->fileHash mapping in a collection (upsert semantics), also storing providers_json.
func AddCollectionEntry(collectionID int, path, fileHash string) error {
	providers := []model.Provider{{Type: "sha256", Value: fileHash}}
	providersJSON, _ := json.Marshal(providers)
	_, err := DB.Exec(`INSERT INTO collection_entries (collection_id, path, file_hash, providers_json) VALUES (?, ?, ?, ?)
		ON CONFLICT(collection_id, path) DO UPDATE SET file_hash = excluded.file_hash, providers_json = excluded.providers_json`,
		collectionID, path, fileHash, string(providersJSON))
	return err
}

// AddProviderCollectionEntry inserts or updates a collection entry with the full providers array.
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

// RemoveCollectionEntry removes the entry for a given path from a collection.
func RemoveCollectionEntry(collectionID int, path string) error {
	_, err := DB.Exec(`DELETE FROM collection_entries WHERE collection_id = ? AND path = ?`,
		collectionID, path)
	return err
}

// GetCollectionEntry queries the entry for a given path in a collection; returns (nil, nil) if not found.
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

// ListCollectionEntries queries all entries in a collection, including providers_json.
// M11: Entry count is the collection's data body, theoretically must be full... but maliciously constructed large collections cause full-table materialization.
// Limit to 10000 (normal collection sync batches are far smaller; abnormally large collections need pagination refactoring, not full memory blowup).
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

// CreateVersion creates a new version snapshot record for a collection, returns version ID and version number.
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

// SnapshotVersionEntries copies the collection's current entries (including providers_json) into version_entries.
func SnapshotVersionEntries(versionID, collectionID int) error {
	_, err := DB.Exec(`INSERT INTO version_entries (version_id, path, file_hash, providers_json) SELECT ?, path, file_hash, COALESCE(providers_json, '') FROM collection_entries WHERE collection_id = ?`,
		versionID, collectionID)
	return err
}

// GetVersionLog returns the collection's version history, ordered by version number descending.
// M11: No LIMIT → unlimited version history full-table materialization; limit to 1000 (UI only shows recent versions for rollback).
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

// GetVersionEntries queries all entries in a specified version snapshot (including providers_json).
// M11: Same as ListCollectionEntries, limit to 10000.
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

// RestoreVersionEntries rolls back collection entries to a specified version's snapshot within a transaction (delete then insert, including providers_json).
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
