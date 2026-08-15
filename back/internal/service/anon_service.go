// Package service 提供 Peerdrive 业务逻辑层，包括匿名集合管理、下载、P2P 传输、文件注册等功能。
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type AnonService struct {
	config *config.Config
}

// NewAnonService 创建一个新的匿名集合服务实例。
func NewAnonService(cfg *config.Config) *AnonService {
	return &AnonService{config: cfg}
}

func isRelativePath(p string) bool {
	return !filepath.IsAbs(p) && !strings.HasPrefix(p, "/")
}

var hashRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

func isValidHash(h string) bool {
	return hashRe.MatchString(h)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func isValidProviders(providers []model.Provider) bool {
	if len(providers) == 0 {
		return false
	}
	hasValid := false
	for _, p := range providers {
		if p.Type == "" && p.Value == "" {
			continue
		}
		hasValid = true
		switch p.Type {
		case "sha256":
			if !isValidHash(p.Value) {
				return false
			}
		case "url":
			u, err := url.ParseRequestURI(p.Value)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return false
			}
		default:
			return false
		}
	}
	return hasValid
}

// CreateCollection 创建匿名集合，验证条目路径和 providers，写入 content-addressed 存储并返回 SHA256。
func (s *AnonService) CreateCollection(name string, entries []model.AnonCollectionEntry, tags []string) (string, error) {
	defer log.LogDuration("AnonService.CreateCollection")()
	log.LogDebug("anon-svc: CreateCollection name=%s entries=%d", name, len(entries))

	// 规范化：struct literal 可能直接设置 Hash 但没设 Providers
	for i := range entries {
		entries[i].Normalize()
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			err := fmt.Errorf("invalid path: %s", e.Path)
			log.LogError("anon-svc: CreateCollection invalid path: %s", e.Path)
			return "", err
		}
		if !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
			err := fmt.Errorf("invalid providers for path: %s", e.Path)
			log.LogError("anon-svc: CreateCollection invalid providers for path: %s", e.Path)
			return "", err
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	coll := model.NewAnonCollection(name, entries, tags)
	jsonBytes, err := json.MarshalIndent(coll, "", "  ")
	if err != nil {
		log.LogError("anon-svc: CreateCollection marshal failed: %v", err)
		return "", err
	}

	hash := sha256Hex(jsonBytes)

	targetDir := filepath.Join(s.config.StorageDir, hash[:2])
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.LogError("anon-svc: CreateCollection mkdir failed: %v", err)
		return "", err
	}
	targetPath := filepath.Join(targetDir, hash)
	if err := os.WriteFile(targetPath, jsonBytes, 0644); err != nil {
		log.LogError("anon-svc: CreateCollection write failed: %v", err)
		return "", err
	}

	relPath := filepath.Join(hash[:2], hash)
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(jsonBytes)),
		MimeType: "application/json",
		Filename: "anon_collection.json",
		Gziped:   false,
		Type:     repository.FileTypeAnonCollection,
	})
	_ = repository.InsertFileProvider(hash, "local", relPath)

	log.LogInfo("anon-svc: CreateCollection %s -> hash=%s", name, hash)
	return hash, nil
}

// GetCollectionByHash 通过 SHA256 hash 从 content-addressed 存储中读取匿名集合。
func (s *AnonService) GetCollectionByHash(hash string) (*model.AnonCollection, error) {
	defer log.LogDuration("AnonService.GetCollectionByHash")()
	log.LogDebug("anon-svc: GetCollectionByHash hash=%s", hash)

	// 防御：hash 来自 URL 路径/请求体/远端 P2P sync，无长度校验时 hash[:2] 会切片越界 panic；
	// 非 64 位 hex（如 ".."）还会让 filepath.Join 逃逸 storage 目录。非法直接返回 not-found。
	if !isValidHash(hash) {
		log.LogWarn("anon-svc: GetCollectionByHash invalid hash=%q", hash)
		return nil, fmt.Errorf("collection not found")
	}

	filePath := filepath.Join(s.config.StorageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s not found: %v", hash, err)
		return nil, fmt.Errorf("collection not found")
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s invalid JSON: %v", hash, err)
		return nil, fmt.Errorf("invalid collection json")
	}
	if coll.Version < 1 {
		log.LogError("anon-svc: GetCollectionByHash %s unsupported version: %d", hash, coll.Version)
		return nil, fmt.Errorf("unsupported version: %d", coll.Version)
	}
	coll.NormalizeEntries()
	log.LogInfo("anon-svc: GetCollectionByHash %s found (version=%d, entries=%d)", hash, coll.Version, len(coll.Entries))
	return &coll, nil
}

// ListCollections 返回所有已注册的匿名集合摘要列表。
func (s *AnonService) ListCollections() ([]model.AnonCollectionSummary, error) {
	return repository.ListAnonCollections(s.config.StorageDir)
}

// CommitCollection 基于源集合创建新版本，支持添加/更新/删除条目，自动递增版本号。
func (s *AnonService) CommitCollection(
	sourceHash string,
	entries []model.AnonCollectionEntry,
	commitMessage string,
) (string, error) {
	defer log.LogDuration("AnonService.CommitCollection")()
	log.LogDebug("anon-svc: CommitCollection sourceHash=%s entries=%d", sourceHash, len(entries))

	src, err := s.GetCollectionByHash(sourceHash)
	if err != nil {
		log.LogError("anon-svc: CommitCollection source %s not found: %v", sourceHash, err)
		return "", fmt.Errorf("source collection not found: %w", err)
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			err := fmt.Errorf("invalid path: %s", e.Path)
			log.LogError("anon-svc: CommitCollection invalid path: %s", e.Path)
			return "", err
		}
		if !strings.HasSuffix(e.Path, "/") && !isValidProviders(e.Providers) {
			err := fmt.Errorf("invalid providers for path: %s", e.Path)
			log.LogError("anon-svc: CommitCollection invalid providers for path: %s", e.Path)
			return "", err
		}
	}

	// 用 providers 合并源条目和新条目
	entryMap := map[string][]model.Provider{}
	for _, e := range src.Entries {
		entryMap[e.Path] = e.Providers
	}
	for _, e := range entries {
		entryMap[e.Path] = e.Providers
	}

	sortedEntries := make([]model.AnonCollectionEntry, 0, len(entryMap))
	removeEmpty := false
	for path, providers := range entryMap {
		if len(providers) == 0 {
			removeEmpty = true
			continue
		}
		sortedEntries = append(sortedEntries, model.AnonCollectionEntry{Path: path, Providers: providers})
	}
	if removeEmpty {
		coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1, src.Tags)
		return s.saveCollectionJSON(coll)
	}

	sort.Slice(sortedEntries, func(i, j int) bool {
		return sortedEntries[i].Path < sortedEntries[j].Path
	})

	coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1, src.Tags)
	return s.saveCollectionJSON(coll)
}

func newAnonCollectionWithVersion(name string, entries []model.AnonCollectionEntry, version int, tags []string) *model.AnonCollection {
	if entries == nil {
		entries = []model.AnonCollectionEntry{}
	}
	if tags == nil {
		tags = []string{}
	}
	return &model.AnonCollection{
		Version:      version,
		FriendlyName: name,
		Entries:      entries,
		Tags:         tags,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

func (s *AnonService) saveCollectionJSON(coll *model.AnonCollection) (string, error) {
	sort.Slice(coll.Entries, func(i, j int) bool {
		return coll.Entries[i].Path < coll.Entries[j].Path
	})

	jsonBytes, err := json.MarshalIndent(coll, "", "  ")
	if err != nil {
		return "", err
	}

	hash := sha256Hex(jsonBytes)

	targetDir := filepath.Join(s.config.StorageDir, hash[:2])
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", err
	}
	targetPath := filepath.Join(targetDir, hash)
	if err := os.WriteFile(targetPath, jsonBytes, 0644); err != nil {
		return "", err
	}

	relPath := filepath.Join(hash[:2], hash)
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(jsonBytes)),
		MimeType: "application/json",
		Filename: "anon_collection.json",
		Gziped:   false,
		Type:     repository.FileTypeAnonCollection,
	})
	_ = repository.InsertFileProvider(hash, "local", relPath)

	return hash, nil
}
