// 匿名合集仓库 — 将 AnonCollection JSON 写入文件系统并注册到 files 表。
//
// SaveCollection(coll, storageDir):
//   1. entries 按 Path 字典序排序
//   2. json.Marshal(coll) → []byte（字段顺序固定，保证确定性）
//   3. SHA256([]byte) → hashStr（即集合唯一标识）
//   4. 写文件到 storage/anon/{hashStr[:2]}/{hashStr}
//   5. 注册到 files 表，type='anon_collection'
//   6. 幂等：文件已存在则跳过写，UNIQUE(hash) 保证不重复插入
//
// GetAnonCollectionByHash(hash, storageDir):
//   1. 从 storage/anon/{hash[:2]}/{hash} 读取文件内容
//   2. json.Unmarshal 解析为 AnonCollection
//   3. 校验 Version 字段（目前支持 1）
//
// storageDir 通过 SetStorageDir(dir) 注入。
//
// 依赖：
//   - files 表有 type TEXT DEFAULT 'blob' 列
//   - model.AnonCollection 结构体字段顺序固定（json.Marshal 按声明顺序）

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

	dir := filepath.Join(storageDir, "anon", hashStr[:2])
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	filePath := filepath.Join(dir, hashStr)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return "", err
		}
	}

	relPath := fmt.Sprintf("anon/%s/%s", hashStr[:2], hashStr)
	_, err = DB.Exec(`
		INSERT INTO files (hash, provider_type, path, filename, type)
		VALUES (?, 'local', ?, ?, ?)`,
		hashStr, relPath, fmt.Sprintf("anon_%s.json", hashStr), FileTypeAnonCollection,
	)
	return hashStr, nil
}

func GetAnonCollectionByHash(hash string, storageDir string) (*model.AnonCollection, error) {
	if storageDir == "" {
		storageDir = anonStorageDir
	}
	filePath := filepath.Join(storageDir, "anon", hash[:2], hash)
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

