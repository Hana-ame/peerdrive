package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type FileService struct {
	storageDir    string
	storageEnable bool
}

func NewFileService(cfg *config.Config) *FileService {
	return &FileService{
		storageDir:    cfg.StorageDir,
		storageEnable: cfg.StorageEnable,
	}
}

// RegisterLocal handles the logic of registering a local file.
func (s *FileService) RegisterLocal(path, filename string) (string, error) {
	if !s.storageEnable {
		return "", fmt.Errorf("storage is disabled")
	}
	fullPath := path
	// If path is relative, it's assumed to be relative to storageDir
	if !filepath.IsAbs(path) {
		fullPath = filepath.Join(s.storageDir, path)
	}

	f, err := os.Open(fullPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))

	// Meta (Idempotent)
	existing, _ := repository.GetFileMeta(hash)
	if existing == nil {
		_ = repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Gziped:   false,
			Filename: filename,
			Type:     repository.FileTypeBlob,
		})
	}

	// Provider
	_ = repository.InsertFileProvider(hash, "local", path)

	return hash, nil
}

// RegisterFolder handles batch registration of files in a folder.
func (s *FileService) RegisterFolder(folderPath string) ([]map[string]string, error) {
	if !s.storageEnable {
		return nil, fmt.Errorf("storage is disabled")
	}
	fullDir := folderPath
	if !filepath.IsAbs(folderPath) {
		fullDir = filepath.Join(s.storageDir, folderPath)
	}

	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return nil, err
	}

	var results []map[string]string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		// Note: We pass the relative path as requested by the user in the original logic
		relPath := filepath.Join(folderPath, entry.Name())
		hash, err := s.RegisterLocal(relPath, entry.Name())
		if err != nil {
			continue
		}

		results = append(results, map[string]string{
			"filename": entry.Name(),
			"hash":     hash,
		})
	}
	return results, nil
}

// Upload handles uploading a file from a stream.
func (s *FileService) Upload(reader io.Reader, filename string) (string, error) {
	if !s.storageEnable {
		return "", fmt.Errorf("storage is disabled")
	}
	// Use a temporary file to calculate hash
	tempFile, err := os.CreateTemp("", "peerdrive-upload-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	h := sha256.New()
	mw := io.MultiWriter(tempFile, h)
	if _, err := io.Copy(mw, reader); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))

	// Move to permanent storage
	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(s.storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	if err := os.Rename(tempFile.Name(), fullPath); err != nil {
		return "", err
	}

	// Meta
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	})

	// Provider
	_ = repository.InsertFileProvider(hash, "local", relPath)

	return hash, nil
}

func (s *FileService) Verify(hash string) (*model.FileMeta, error) {
	return repository.GetFileMeta(hash)
}

func (s *FileService) Delete(hash string) error {
	if !s.storageEnable {
		return fmt.Errorf("storage is disabled")
	}
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			fullPath := p.Path
			if !filepath.IsAbs(p.Path) {
				fullPath = filepath.Join(s.storageDir, p.Path)
			}
			os.Remove(fullPath)
		}
	}
	repository.DB.Exec(`DELETE FROM file_providers WHERE hash = ?`, hash)
	repository.DB.Exec(`DELETE FROM file_meta WHERE hash = ?`, hash)
	return nil
}
