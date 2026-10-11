// FileService handles file upload, metadata lookup, verification/deletion, and file reading/copying.
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
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
	"peerdrive/pkg/urlguard"

	"github.com/gin-gonic/gin"
)

var (
	ErrStorageDisabled   = errors.New("storage is disabled")
	ErrFileAlreadyExists = errors.New("file already exists")
)

type FileService struct {
	storageDir    string
	storageEnable bool
	cfg           *config.Config
}

// NewFileService creates a new file service instance.
func NewFileService(cfg *config.Config) *FileService {
	return &FileService{
		storageDir:    cfg.StorageDir,
		storageEnable: cfg.StorageEnable,
		cfg:           cfg,
	}
}

// GetMeta looks up file metadata by hash (M2 convergence: the download controller previously called repository.GetFileMeta directly).
func (s *FileService) GetMeta(hash string) (*model.FileMeta, error) {
	meta, err := repository.GetFileMeta(hash)
	if err == nil && meta != nil {
		return meta, nil
	}
	// Fallback to file_index if not found in file_meta
	if fi, fiErr := repository.GetFileIndex(hash); fiErr == nil && fi != nil {
		return &model.FileMeta{
			Hash:     fi.Hash,
			Size:     fi.Size,
			Filename: fi.Name,
			Type:     model.FileTypeBlob,
		}, nil
	}
	return meta, err
}

// GetLocalPath returns the on-disk file path for a content hash by checking storageDir, file_index, and local providers.
func (s *FileService) GetLocalPath(hash string) (string, error) {
	if s.storageDir != "" {
		relPath := hash[:2] + "/" + hash
		fullPath := filepath.Join(s.storageDir, relPath)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath, nil
		}
	}
	if fi, err := repository.GetFileIndex(hash); err == nil && fi != nil && fi.Path != "" {
		if _, err := os.Stat(fi.Path); err == nil {
			return fi.Path, nil
		}
	}
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			candidate := p.Path
			if !filepath.IsAbs(candidate) && s.storageDir != "" {
				candidate = filepath.Join(s.storageDir, candidate)
			}
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	return "", os.ErrNotExist
}

// GetMetaByCID looks up file metadata by IPFS CID (M2 convergence: the download controller DownloadByCID).
func (s *FileService) GetMetaByCID(cid string) (*model.FileMeta, error) {
	return repository.GetFileMetaByCID(cid)
}

// ListAll lists all blob files (M2 convergence: the file controller ListFiles previously called repository.ListAllFiles directly).
func (s *FileService) ListAll(sortBy string) ([]model.FileListItem, error) {
	items, err := repository.ListAllFiles(sortBy)
	if err != nil {
		return nil, err
	}
	// Issue #280: detect file_index vs file_meta divergence and log a warning.
	if onlyIdx, onlyMeta, cerr := repository.CheckFileIndexConsistency(); cerr == nil && (onlyIdx > 0 || onlyMeta > 0) {
		log.LogWarn("file_index/file_meta divergence: %d hash(es) only in file_index, %d only in file_meta — consider migration (Issue #280)", onlyIdx, onlyMeta)
	}
	return items, nil
}

// Upload uploads a file to content-addressed storage, computes SHA256 and registers metadata and provider.
func (s *FileService) Upload(reader io.Reader, filename string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.Upload")()
	cleanName := filepath.Base(filepath.Clean(filename))
	if cleanName == "." || cleanName == "/" || cleanName == "\\" {
		cleanName = ""
	}
	filename = cleanName
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
		if existing.Filename == "" && filename != "" {
			existing.Filename = filename
			_ = repository.UpdateFileMetaFilename(hash, filename)
		}
		relPath := hash[:2] + "/" + hash
		fullPath := filepath.Join(s.storageDir, relPath)
		_, _ = repository.UpsertFileIndex(hash, fullPath, filename, existing.Size, false)
		return existing, ErrFileAlreadyExists
	}

	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(s.storageDir, relPath)
	if err := s.copyInto(s.allowedRoots(), tmpName, fullPath); err != nil {
		log.LogError("file-svc: Upload move to storage failed: %v", err)
		return nil, fmt.Errorf("move to storage: %w", err)
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

	// Sync-register file_index
	if _, err := repository.UpsertFileIndex(hash, fullPath, filename, size, false); err != nil {
		log.LogWarn("file-svc: Upload upsert file_index %s failed: %v", hash, err)
	}

	log.LogInfo("file-svc: Upload %s completed (hash=%s, size=%d)", filename, hash, size)
	return meta, nil
}

// Verify looks up file metadata by hash, used to verify whether a file exists.
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

// Delete removes the local file for the given hash along with its metadata and provider records.
func (s *FileService) Delete(hash string) error {
	defer log.LogDuration("FileService.Delete")()
	log.LogDebug("file-svc: Delete hash=%s", hash)

	if !s.storageEnable {
		err := ErrStorageDisabled
		log.LogError("file-svc: Delete storage disabled")
		return err
	}
	if !isValidHash(hash) {
		log.LogWarn("file-svc: Delete invalid hash %q", hash)
		return fmt.Errorf("invalid hash")
	}
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			if s.isPathAllowed(p.Path) {
				os.Remove(p.Path)
			}
		}
	}
	if err := repository.DeleteFileMetaAndProviders(hash); err != nil {
		log.LogWarn("file-svc: Delete %s failed to clean repository: %v", hash, err)
	}
	log.LogInfo("file-svc: Delete %s completed", hash)
	return nil
}

// CopyFile copies a file identified by hash to a destination path within storage
// and registers the copy in file_providers. Returns the destination path.
func (s *FileService) CopyFile(hash string, destPath string) (string, error) {
	defer log.LogDuration("FileService.CopyFile")()
	log.LogDebug("file-svc: CopyFile hash=%s dest=%s", hash, destPath)

	if !s.storageEnable {
		return "", ErrStorageDisabled
	}

	absDest := destPath
	if !filepath.IsAbs(destPath) {
		absDest = filepath.Join(s.storageDir, destPath)
	}
	if !isValidHash(hash) {
		return "", fmt.Errorf("invalid source hash")
	}
	if !s.isPathAllowed(absDest) {
		log.LogWarn("file-svc: CopyFile dest outside storage root: %s", absDest)
		return "", fmt.Errorf("destination path outside storage root")
	}

	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		return "", fmt.Errorf("lookup source meta: %w", err)
	}
	if meta == nil {
		return "", fmt.Errorf("source hash %s not found", hash)
	}

	body, err := s.ReadFile(hash)
	if err != nil {
		return "", fmt.Errorf("read source file: %w", err)
	}

	if err := pathutil.SafeWriteFileAny(s.allowedRoots(), absDest, body, 0644); err != nil {
		log.LogWarn("file-svc: CopyFile write %s rejected: %v", absDest, err)
		return "", fmt.Errorf("write dest file: %w", err)
	}

	relPath, _ := filepath.Rel(s.storageDir, absDest)
	if relPath == "" || strings.HasPrefix(relPath, "..") {
		relPath = absDest
	}
	_ = repository.InsertFileProvider(hash, "local", relPath)

	log.LogInfo("file-svc: CopyFile hash=%s -> %s (rel=%s)", hash, absDest, relPath)
	return absDest, nil
}

// ReadFile reads a file's bytes from content-addressed storage or via providers.
func (s *FileService) ReadFile(hash string) ([]byte, error) {
	if !isValidHash(hash) {
		return nil, fmt.Errorf("invalid hash %q", hash)
	}

	candidates := []string{
		filepath.Join(s.storageDir, hash[:2], hash),
		filepath.Join(s.storageDir, "p2p", hash[:2], hash),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, nil
		}
	}

	providers, err := repository.GetFileProviders(hash)
	if err != nil {
		return nil, fmt.Errorf("db lookup: %w", err)
	}
	for _, p := range providers {
		if p.ProviderType == "local" && p.Available {
			path := p.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(s.storageDir, path)
			}
			if s.isPathAllowed(path) {
				f, err := s.openAllowed(path)
				if err == nil {
					data, readErr := io.ReadAll(f)
					_ = f.Close()
					if readErr == nil {
						return data, nil
					}
				}
			}
		}
		if p.ProviderType == "http" && p.Available {
			if err := urlguard.GuardExternalURL(p.Path); err == nil {
				client := urlguard.NewSafeClient(15 * time.Second)
				resp, err := client.Get(p.Path)
				if err == nil {
					maxBytes := s.cfg.MaxUploadBytes
					if maxBytes <= 0 {
						maxBytes = 100 * 1024 * 1024
					}
					data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
					_ = resp.Body.Close()
					if readErr == nil && len(data) > 0 {
						return data, nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("file %s not found", hash)
}

// MaxUploadBytes returns the maximum upload bytes based on authentication status.
func (s *FileService) MaxUploadBytes(c *gin.Context) int64 {
	if authed, exists := c.Get("authenticated"); exists && authed.(bool) {
		return s.cfg.MaxUploadBytes
	}
	return s.cfg.MaxUploadBytesAnon
}
