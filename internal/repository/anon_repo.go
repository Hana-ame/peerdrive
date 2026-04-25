// 匿名合集仓库 — 将 AnonCollection JSON 写入文件系统并注册到数据库。
//
// SaveCollection(coll, storageDir):
//   1. entries 按 Path 字典序排序
//   2. json.Marshal(coll) → []byte（字段顺序固定，保证确定性）
//   3. SHA256([]byte) → hashStr
//   4. 写文件到 storage/{hashStr[:2]}/{hashStr}
//   5. INSERT file_meta (hash, gziped=0, filename, type=anon_collection)
//   6. INSERT file_providers (hash, 'local', path)
//
// GetAnonCollectionByHash(hash, storageDir):
//   1. 从 storage/{hash[:2]}/{hash} 读取文件内容
//   2. json.Unmarshal 解析为 AnonCollection → 校验 Version

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

	// 写文件
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

	// 注册 file_meta（幂等）
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

func GetAnonCollectionByHash(hash string, storageDir string) (*model.AnonCollection, error) {
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
	if coll.Version != 1 {
		return nil, fmt.Errorf("unsupported collection version: %d", coll.Version)
	}
	return &coll, nil
}
