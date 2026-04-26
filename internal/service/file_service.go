package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

var (
	ErrStorageDisabled  = errors.New("storage is disabled")
	ErrFileAlreadyExists = errors.New("file already exists")
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

func (s *FileService) RegisterLocal(path, filename string) (string, error) {
	if !s.storageEnable {
		return "", ErrStorageDisabled
	}

	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(s.storageDir, path)
	}

	f, err := os.Open(absPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	hash := hex.EncodeToString(h.Sum(nil))

	f.Seek(0, io.SeekStart)
	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)
	mimeType := http.DetectContentType(buf[:n])
	if mimeType == "application/octet-stream" {
		if t := mime.TypeByExtension(filepath.Ext(absPath)); t != "" {
			mimeType = t
		}
	}

	existing, _ := repository.GetFileMeta(hash)
	if existing == nil {
		_ = repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Size:     size,
			MimeType: mimeType,
			Gziped:   false,
			Filename: filename,
			Type:     repository.FileTypeBlob,
		})
	}

	_ = repository.InsertFileProvider(hash, "local", absPath)

	return hash, nil
}

func (s *FileService) RegisterFolder(folderPath string) ([]map[string]string, error) {
	if !s.storageEnable {
		return nil, ErrStorageDisabled
	}

	absDir := folderPath
	if !filepath.IsAbs(folderPath) {
		absDir = filepath.Join(s.storageDir, folderPath)
	}

	var results []map[string]string
	err := filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		hash, err := s.RegisterLocal(path, info.Name())
		if err != nil {
			return err
		}
		results = append(results, map[string]string{
			"filename": info.Name(),
			"hash":     hash,
		})
		return nil
	})
	return results, err
}

func (s *FileService) Upload(reader io.Reader, filename string) (*model.FileMeta, error) {
	if !s.storageEnable {
		return nil, ErrStorageDisabled
	}

	tmpFile, err := os.CreateTemp("", "peerdrive-upload-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	hasher := sha256.New()
	tee := io.TeeReader(reader, hasher)
	size, err := io.Copy(tmpFile, tee)
	if err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("write temp file: %w", err)
	}
	hash := hex.EncodeToString(hasher.Sum(nil))

	tmpFile.Seek(0, io.SeekStart)
	buf := make([]byte, 512)
	n, _ := io.ReadFull(tmpFile, buf)
	mimeType := http.DetectContentType(buf[:n])
	if ext := filepath.Ext(filename); ext != "" && mimeType == "application/octet-stream" {
		if t := mime.TypeByExtension(ext); t != "" {
			mimeType = t
		}
	}
	tmpFile.Close()

	if existing, _ := repository.GetFileMeta(hash); existing != nil {
		return existing, ErrFileAlreadyExists
	}

	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(s.storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	if err := os.Rename(tmpName, fullPath); err != nil {
		// Fallback: cross-device link, use copy instead
		if err := copyFile(tmpName, fullPath); err != nil {
			return nil, fmt.Errorf("move to storage: %w", err)
		}
	}
	tmpName = ""

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     size,
		MimeType: mimeType,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	}
	if err := repository.InsertFileMeta(meta); err != nil {
		return nil, fmt.Errorf("insert meta: %w", err)
	}

	if err := repository.InsertFileProvider(hash, "local", relPath); err != nil {
		return nil, fmt.Errorf("insert provider: %w", err)
	}

	return meta, nil
}

func (s *FileService) Verify(hash string) (*model.FileMeta, error) {
	return repository.GetFileMeta(hash)
}

func (s *FileService) Delete(hash string) error {
	if !s.storageEnable {
		return ErrStorageDisabled
	}
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			os.Remove(p.Path)
		}
	}
	repository.DB.Exec(`DELETE FROM file_providers WHERE hash = ?`, hash)
	repository.DB.Exec(`DELETE FROM file_meta WHERE hash = ?`, hash)
	return nil
}

func (s *FileService) BrowseDir(dirPath string) ([]model.DirEntry, error) {
	if !s.storageEnable {
		return nil, ErrStorageDisabled
	}

	absDir := dirPath
	if !filepath.IsAbs(dirPath) {
		absDir = filepath.Join(s.storageDir, dirPath)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}

	var result []model.DirEntry
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(absDir, e.Name())
		entry := model.DirEntry{
			Name:    e.Name(),
			Path:    fullPath,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}
		result = append(result, entry)
	}
	return result, nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()

	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return d.Sync()
}