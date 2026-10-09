package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/pkg/hashutil"
)

var anonStorageDir string

// SetAnonStorageDir sets the default storage directory path for anonymous collections.
func SetAnonStorageDir(dir string) {
	anonStorageDir = dir
}

// SaveCollection serializes an anonymous collection to JSON, writes it to content-addressed storage, and returns the SHA256 hash.
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

// GetAnonCollectionByHash reads and deserializes an anonymous collection from content-addressed storage.
func GetAnonCollectionByHash(hash, storageDir string) (*model.AnonCollection, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	// Defense: hash may come from URL/request body/remote sync; without validation
	// hash[:2] panics on out-of-bounds, and ".."-like values escape the storage dir.
	// Same root cause as anon_service.GetCollectionByHash.
	if !hashutil.IsValidSHA256(hash) {
		return nil, fmt.Errorf("collection not found locally: invalid hash")
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

// ListAnonCollections returns summaries of all registered anonymous collections (including name preview and tags), ordered by creation time descending.
// M11: No LIMIT → full-table materialization with os.ReadFile per row (file I/O × N) at thousands of collections.
// Frontend only shows recent collections; a 1000-row cap is sufficient.
func ListAnonCollections(storageDir string) ([]model.AnonCollectionSummary, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	rows, err := db.Query(
		`SELECT hash, created_at FROM file_meta WHERE type = ? ORDER BY created_at DESC LIMIT 1000`,
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
				summary.Tags = coll.Tags
				summary.EntryCount = len(coll.Entries)
				// name preview: first 3 filenames
				preview := make([]string, 0, 3)
				for i, e := range coll.Entries {
					if i >= 3 {
						break
					}
					preview = append(preview, e.Path)
				}
				summary.NamePreview = strings.Join(preview, ", ")
				// Visibility backfill: list page shows Public/Specific/Self-only directly,
				// no need to fetch details one by one.
				// Gotcha: historical collections lack this field → EffectiveVisibility() falls back to "public".
				// Do not pass an empty string to the frontend, or the three-option UI has no default highlight.
				summary.Visibility = coll.EffectiveVisibility()
				summary.Owner = coll.Owner
			}
		}
		results = append(results, summary)
	}
	if results == nil {
		results = []model.AnonCollectionSummary{}
	}
	return results, nil
}
