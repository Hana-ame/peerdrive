package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type AnonService struct {
	config *config.Config
}

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

func (s *AnonService) CreateCollection(name string, entries []model.AnonCollectionEntry) (string, error) {
	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			return "", fmt.Errorf("invalid path: %s", e.Path)
		}
		if !isValidHash(e.Hash) {
			return "", fmt.Errorf("invalid hash: %s", e.Hash)
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})

	coll := model.NewAnonCollection(name, entries)
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

func (s *AnonService) GetCollectionByHash(hash string) (*model.AnonCollection, error) {
	filePath := filepath.Join(s.config.StorageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("collection not found")
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		return nil, fmt.Errorf("invalid collection json")
	}
	if coll.Version < 1 {
		return nil, fmt.Errorf("unsupported version: %d", coll.Version)
	}
	return &coll, nil
}

func (s *AnonService) ListCollections() ([]model.AnonCollectionSummary, error) {
	return repository.ListAnonCollections(s.config.StorageDir)
}

func (s *AnonService) CommitCollection(
	sourceHash string,
	entries []model.AnonCollectionEntry,
	commitMessage string,
) (string, error) {
	src, err := s.GetCollectionByHash(sourceHash)
	if err != nil {
		return "", fmt.Errorf("source collection not found: %w", err)
	}

	for _, e := range entries {
		if e.Path == "" || !isRelativePath(e.Path) || strings.Contains(e.Path, "..") {
			return "", fmt.Errorf("invalid path: %s", e.Path)
		}
		if !isValidHash(e.Hash) {
			return "", fmt.Errorf("invalid hash: %s", e.Hash)
		}
	}

	entryMap := map[string]string{}
	for _, e := range src.Entries {
		entryMap[e.Path] = e.Hash
	}
	for _, e := range entries {
		entryMap[e.Path] = e.Hash
	}

	sortedEntries := make([]model.AnonCollectionEntry, 0, len(entryMap))
	removeEmpty := false
	for path, hash := range entryMap {
		if hash == "" {
			removeEmpty = true
			continue
		}
		sortedEntries = append(sortedEntries, model.AnonCollectionEntry{Path: path, Hash: hash})
	}
	if removeEmpty {
		coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1)
		coll.Version = src.Version + 1
		return s.saveCollectionJSON(coll)
	}

	sort.Slice(sortedEntries, func(i, j int) bool {
		return sortedEntries[i].Path < sortedEntries[j].Path
	})

	coll := newAnonCollectionWithVersion(src.FriendlyName, sortedEntries, src.Version+1)
	return s.saveCollectionJSON(coll)
}

func newAnonCollectionWithVersion(name string, entries []model.AnonCollectionEntry, version int) *model.AnonCollection {
	if entries == nil {
		entries = []model.AnonCollectionEntry{}
	}
	return &model.AnonCollection{
		Version:      version,
		FriendlyName: name,
		Entries:      entries,
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
