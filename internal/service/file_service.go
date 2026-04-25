package service

import (
	"crypto/sha256"
	"encoding/hex"
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
		return "", fmt.Errorf("storage is disabled")
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
		return nil, fmt.Errorf("storage is disabled")
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

func (s *FileService) Upload(reader io.Reader, filename string) (string, error) {
	if !s.storageEnable {
		return "", fmt.Errorf("storage is disabled")
	}

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

	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(s.storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	if _, err := os.Stat(fullPath); err == nil {
		return hash, nil // File already exists, return success
	}

	if err := os.Rename(tempFile.Name(), fullPath); err != nil {
		return "", err
	}

	info, err := os.Stat(fullPath)
	size := int64(0)
	if err == nil {
		size = info.Size()
	}

	f, _ := os.Open(fullPath)
	mimeType := "application/octet-stream"
	if f != nil {
		buf := make([]byte, 512)
		n, _ := f.Read(buf)
		mimeType = http.DetectContentType(buf[:n])
		if mimeType == "application/octet-stream" {
			if t := mime.TypeByExtension(filepath.Ext(filename)); t != "" {
				mimeType = t
			}
		}
		f.Close()
	}

	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     size,
		MimeType: mimeType,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	})

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
			os.Remove(p.Path)
		}
	}
	repository.DB.Exec(`DELETE FROM file_providers WHERE hash = ?`, hash)
	repository.DB.Exec(`DELETE FROM file_meta WHERE hash = ?`, hash)
	return nil
}