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
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

var (
	ErrStorageDisabled   = errors.New("storage is disabled")
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
	defer log.LogDuration("FileService.RegisterLocal")()
	log.LogDebug("file-svc: RegisterLocal path=%s filename=%s", path, filename)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: RegisterLocal storage disabled")
		return "", err
	}

	absPath := path
	if !filepath.IsAbs(path) {
		absPath = filepath.Join(s.storageDir, path)
	}

	f, err := os.Open(absPath)
	if err != nil {
		log.LogError("file-svc: RegisterLocal open %s failed: %v", absPath, err)
		return "", err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		log.LogError("file-svc: RegisterLocal stat %s failed: %v", absPath, err)
		return "", err
	}
	size := info.Size()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		log.LogError("file-svc: RegisterLocal hash %s failed: %v", absPath, err)
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

	log.LogInfo("file-svc: RegisterLocal %s -> hash=%s size=%d", absPath, hash, size)
	return hash, nil
}

func (s *FileService) RegisterFolder(folderPath string) ([]map[string]string, error) {
	defer log.LogDuration("FileService.RegisterFolder")()
	log.LogDebug("file-svc: RegisterFolder folderPath=%s", folderPath)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: RegisterFolder storage disabled")
		return nil, err
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

	if err != nil {
		log.LogError("file-svc: RegisterFolder walk %s failed: %v", absDir, err)
		return results, err
	}

	log.LogInfo("file-svc: RegisterFolder %s registered %d files", folderPath, len(results))
	return results, nil
}

func (s *FileService) Upload(reader io.Reader, filename string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.Upload")()
	log.LogDebug("file-svc: Upload filename=%s", filename)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: Upload storage disabled")
		return nil, err
	}

	tmpFile, err := os.CreateTemp("", "peerdrive-upload-*")
	if err != nil {
		log.LogError("file-svc: Upload create temp file failed: %v", err)
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	hasher := sha256.New()
	tee := io.TeeReader(reader, hasher)
	size, err := io.Copy(tmpFile, tee)
	if err != nil {
		tmpFile.Close()
		log.LogError("file-svc: Upload write temp file failed: %v", err)
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
		log.LogInfo("file-svc: Upload %s already exists (hash=%s)", filename, hash)
		return existing, ErrFileAlreadyExists
	}

	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(s.storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	if err := os.Rename(tmpName, fullPath); err != nil {
		// Fallback: cross-device link, use copy instead
		if err := copyFile(tmpName, fullPath); err != nil {
			log.LogError("file-svc: Upload move to storage failed: %v", err)
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
		log.LogError("file-svc: Upload insert meta failed: %v", err)
		return nil, fmt.Errorf("insert meta: %w", err)
	}

	if err := repository.InsertFileProvider(hash, "local", relPath); err != nil {
		log.LogError("file-svc: Upload insert provider failed: %v", err)
		return nil, fmt.Errorf("insert provider: %w", err)
	}

	log.LogInfo("file-svc: Upload %s completed (hash=%s, size=%d)", filename, hash, size)
	return meta, nil
}

func (s *FileService) Verify(hash string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.Verify")()
	log.LogDebug("file-svc: Verify hash=%s", hash)

	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		log.LogError("file-svc: Verify %s failed: %v", hash, err)
		return nil, err
	}
	if meta != nil {
		log.LogInfo("file-svc: Verify %s found (size=%d)", hash, meta.Size)
	} else {
		log.LogInfo("file-svc: Verify %s not found", hash)
	}
	return meta, nil
}

func (s *FileService) Delete(hash string) error {
	defer log.LogDuration("FileService.Delete")()
	log.LogDebug("file-svc: Delete hash=%s", hash)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: Delete storage disabled")
		return err
	}
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			os.Remove(p.Path)
		}
	}
	repository.DB.Exec(`DELETE FROM file_providers WHERE hash = ?`, hash)
	repository.DB.Exec(`DELETE FROM file_meta WHERE hash = ?`, hash)
	log.LogInfo("file-svc: Delete %s completed", hash)
	return nil
}

func (s *FileService) BrowseDir(dirPath string) ([]model.DirEntry, error) {
	defer log.LogDuration("FileService.BrowseDir")()
	log.LogDebug("file-svc: BrowseDir dirPath=%s", dirPath)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: BrowseDir storage disabled")
		return nil, err
	}

	absDir := dirPath
	if !filepath.IsAbs(dirPath) {
		absDir = filepath.Join(s.storageDir, dirPath)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		log.LogError("file-svc: BrowseDir read %s failed: %v", absDir, err)
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
	log.LogInfo("file-svc: BrowseDir %s found %d entries", absDir, len(result))
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
