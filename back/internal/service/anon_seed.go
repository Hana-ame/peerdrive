package service

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

// SeedSampleCollectionIfEmpty checks if any anonymous collections exist.
// If none exist, it inspects the user home directory (or storageDir) to construct
// a sample collection and register its files into the repository (Issue #341).
func (s *AnonService) SeedSampleCollectionIfEmpty(storageDir string, addReadRoot func(dir string)) (string, error) {
	if storageDir == "" && s.config != nil {
		storageDir = s.config.StorageDir
	}
	existing, err := s.ListCollections()
	if err == nil && len(existing) > 0 {
		return "", nil // Already has collections, no need to seed
	}

	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		homeDir = storageDir
	}

	entries := make([]model.AnonCollectionEntry, 0)
	readDirs := make(map[string]struct{})

	// Scan home directory for suitable sample files (skip directories, special files, and huge files)
	if dirEntries, err := os.ReadDir(homeDir); err == nil {
		for _, de := range dirEntries {
			if len(entries) >= 5 {
				break
			}
			if de.IsDir() {
				continue
			}
			name := de.Name()
			info, err := de.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			// Skip zero-byte or files over 50MB
			if info.Size() <= 0 || info.Size() > 50*1024*1024 {
				continue
			}

			fullPath := filepath.Join(homeDir, name)
			hash, err := hashFile(fullPath)
			if err != nil {
				continue
			}

			// Register through the unified write (Issue #405).
			_, _ = repository.RegisterFile(repository.FileRecord{
				Hash: hash, Path: fullPath, Name: name, Size: info.Size(),
				Kind: model.FileTypeBlob, ProviderPath: fullPath,
			})
			readDirs[homeDir] = struct{}{}

			entries = append(entries, model.AnonCollectionEntry{
				Path: name,
				Providers: []model.Provider{
					{Type: "sha256", Value: hash},
				},
			})
		}
	}

	// Fallback: if homeDir had no suitable regular files, generate a default sample welcome note
	if len(entries) == 0 {
		samplePath := filepath.Join(storageDir, "welcome_peerdrive.txt")
		content := []byte("Welcome to Peerdrive! This is an auto-generated sample collection entry.\n")
		_ = os.WriteFile(samplePath, content, 0644)
		h := sha256.Sum256(content)
		hashStr := hex.EncodeToString(h[:])
		_, _ = repository.RegisterFile(repository.FileRecord{
			Hash: hashStr, Path: samplePath, Name: "welcome_peerdrive.txt", Size: int64(len(content)),
			Kind: model.FileTypeBlob, ProviderPath: samplePath,
		})
		readDirs[storageDir] = struct{}{}

		entries = append(entries, model.AnonCollectionEntry{
			Path: "welcome_peerdrive.txt",
			Providers: []model.Provider{
				{Type: "sha256", Value: hashStr},
			},
		})
	}

	// Register readable roots so P2P download and req verbs can serve these files
	if addReadRoot != nil {
		for d := range readDirs {
			addReadRoot(d)
		}
	}

	coll := &model.AnonCollection{
		FriendlyName: "Home Sample Collection",
		Version:      1,
		Entries:      entries,
		Tags:         []string{"sample", "home"},
		AccessPolicy: model.AccessPolicyPublic,
		Visibility:   model.VisibilityPublic,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}

	hash, err := repository.SaveCollection(coll, storageDir)
	if err != nil {
		log.LogWarn("anon-svc: failed to seed sample collection: %v", err)
		return "", err
	}
	log.LogInfo("anon-svc: seeded sample collection %s (%s) with %d entries", hash, coll.FriendlyName, len(entries))
	return hash, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
