package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"peerdrive/internal/model"
)

var anonStorageDir string

func SetAnonStorageDir(dir string) {
	anonStorageDir = dir
}

func SaveCollection(coll *model.AnonCollection, storageDir string) (string, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	sort.Slice(coll.Entries, func(i, j int) bool {
		return coll.Entries[i].Path < coll.Entries[j].Path
	})

	data, err := json.Marshal(coll)
	if err != nil {
		return "", fmt.Errorf("marshal collection: %w", err)
	}
	h := sha256.Sum256(data)
	hashStr := hex.EncodeToString(h[:])

	dir := filepath.Join(storageDir, hashStr[:2])
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	filePath := filepath.Join(dir, hashStr)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return "", err
		}
	}

	relPath := fmt.Sprintf("%s/%s", hashStr[:2], hashStr)
	_ = InsertFileMeta(&model.FileMeta{
		Hash:     hashStr,
		Gziped:   false,
		Filename: fmt.Sprintf("anon_%s.json", hashStr),
		Type:     FileTypeAnonCollection,
	})
	_ = InsertFileProvider(hashStr, "local", relPath)

	return hashStr, nil
}

func GetAnonCollectionByHash(hash, storageDir string) (*model.AnonCollection, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	filePath := filepath.Join(storageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("collection not found locally: %w", err)
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		return nil, fmt.Errorf("invalid collection json: %w", err)
	}
	if coll.Version < 1 {
		return nil, fmt.Errorf("unsupported collection version: %d", coll.Version)
	}
	return &coll, nil
}

func ListAnonCollections(storageDir string) ([]model.AnonCollectionSummary, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	rows, err := DB.Query(
		`SELECT hash, created_at FROM file_meta WHERE type = ? ORDER BY created_at DESC`,
		FileTypeAnonCollection,
	)
	if err != nil {
		return nil, fmt.Errorf("query anon collections: %w", err)
	}
	defer rows.Close()

	var results []model.AnonCollectionSummary
	for rows.Next() {
		var hash, createdAt string
		if err := rows.Scan(&hash, &createdAt); err != nil {
			continue
		}
		summary := model.AnonCollectionSummary{
			Hash:      hash,
			CreatedAt: createdAt,
		}
		// try to read json for friendly_name & version
		jsonPath := filepath.Join(storageDir, hash[:2], hash)
		if data, err := os.ReadFile(jsonPath); err == nil {
			var coll model.AnonCollection
			if json.Unmarshal(data, &coll) == nil {
				summary.FriendlyName = coll.FriendlyName
				summary.Version = coll.Version
			}
		}
		results = append(results, summary)
	}
	if results == nil {
		results = []model.AnonCollectionSummary{}
	}
	return results, nil
}
