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

// SetAnonStorageDir 设置匿名集合的默认存储目录路径。
func SetAnonStorageDir(dir string) {
	anonStorageDir = dir
}

// SaveCollection 将匿名集合序列化为 JSON，写入 content-addressed 存储，返回 SHA256 hash。
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

// GetAnonCollectionByHash 从 content-addressed 存储中读取并反序列化匿名集合。
func GetAnonCollectionByHash(hash, storageDir string) (*model.AnonCollection, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	// 防御：hash 可能来自 URL/请求体/远端 sync，未校验时 hash[:2] 越界 panic、
	// ".." 类值逃逸 storage 目录。与 anon_service.GetCollectionByHash 同一根因。
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

// ListAnonCollections 返回所有已注册的匿名集合摘要（含名称预览和标签），按创建时间倒序。
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
			}
		}
		results = append(results, summary)
	}
	if results == nil {
		results = []model.AnonCollectionSummary{}
	}
	return results, nil
}
