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
	"sort"
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
//
// Issue #405 read convergence: file_index is consulted first and is authoritative;
// file_meta is now a compatibility mirror (RegisterFile) and remains only as a
// fallback for rows from a pre-#405 database that the startup backfill skipped
// (e.g. bytes already gone). Same order for every metadata consumer below.
func (s *FileService) GetMeta(hash string) (*model.FileMeta, error) {
	if fi, fiErr := repository.GetFileIndex(hash); fiErr == nil && fi != nil && fi.Path != "" {
		return metaFromIndex(fi), nil
	}
	meta, err := repository.GetFileMeta(hash)
	if err == nil && meta != nil {
		return meta, nil
	}
	return meta, err
}

// metaFromIndex projects the unified index row onto the model.FileMeta shape the
// controllers already speak — keeps the JSON contract identical while the storage
// backing it moves from file_meta to file_index.
func metaFromIndex(fi *repository.FileIndex) *model.FileMeta {
	kind := fi.Kind
	if kind == "" {
		kind = model.FileTypeBlob
	}
	return &model.FileMeta{
		Hash:      fi.Hash,
		Size:      fi.Size,
		CreatedAt: fi.CreatedAt,
		MimeType:  fi.MimeType,
		Gziped:    fi.Gziped,
		Filename:  fi.Name,
		Type:      kind,
		CID:       fi.CID,
	}
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
// Issue #405: index-first; file_meta is only the legacy fallback.
func (s *FileService) GetMetaByCID(cid string) (*model.FileMeta, error) {
	if fi, fiErr := repository.GetFileIndexByCID(cid); fiErr == nil && fi != nil {
		return metaFromIndex(fi), nil
	}
	return repository.GetFileMetaByCID(cid)
}

// ListAll lists all blob files (M2 convergence: the file controller ListFiles previously called repository.ListAllFiles directly).
//
// Issue #405 read convergence: the unified index is the primary source; file_meta
// rows are appended only when their hash is not in the index (rows the startup
// backfill could not import). Sorting is done here because the two sources use
// different SQL shapes but the API contract is a single sorted list.
func (s *FileService) ListAll(sortBy string) ([]model.FileListItem, error) {
	idxList, err := repository.ListFileIndex(0, 1000)
	if err != nil {
		return nil, err
	}
	items := make([]model.FileListItem, 0, len(idxList))
	seen := make(map[string]bool, len(idxList))
	for _, fi := range idxList {
		if fi.IsInbox || fi.Path == "" {
			continue
		}
		mimeType := fi.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		items = append(items, model.FileListItem{
			Hash:         fi.Hash,
			Filename:     fi.Name,
			Size:         fi.Size,
			MimeType:     mimeType,
			CreatedAt:    fi.CreatedAt,
			Type:         model.FileTypeBlob,
			ProviderType: "local",
			ProviderPath: fi.Path,
		})
		seen[fi.Hash] = true
	}

	// Legacy fallback for pre-#405 rows the backfill skipped (missing bytes) or for
	// databases read by a process that never ran the startup backfill (tests/CLI).
	if legacy, lerr := repository.ListAllFiles(sortBy); lerr == nil {
		for _, it := range legacy {
			if seen[it.Hash] {
				continue
			}
			items = append(items, it)
			seen[it.Hash] = true
		}
	}

	sortFileList(items, sortBy)
	return items, nil
}

// sortFileList mirrors the old SQL ORDER BY (all descending, default created_at)
// so the API's ordering contract survives the move to an in-memory merge.
func sortFileList(items []model.FileListItem, sortBy string) {
	less := func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortBy {
		case "path":
			return a.ProviderPath > b.ProviderPath
		case "name":
			return a.Filename > b.Filename
		case "type":
			return a.MimeType > b.MimeType
		case "size":
			return a.Size > b.Size
		default:
			return a.CreatedAt > b.CreatedAt
		}
	}
	sort.SliceStable(items, less)
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
		// Issue #405: the unified write keeps the index authoritative and refreshes
		// the legacy mirrors instead of writing the index leg alone.
		_, _ = repository.RegisterFile(repository.FileRecord{
			Hash:         hash,
			Path:         fullPath,
			Name:         existing.Filename,
			Size:         existing.Size,
			Kind:         model.FileTypeBlob,
			MimeType:     existing.MimeType,
			Gziped:       existing.Gziped,
			ProviderPath: relPath,
		})
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
	// Issue #405: one transaction writes file_index (authority) + file_meta/provider
	// mirrors, so an upload can no longer leave the two tracks out of sync.
	if _, err := repository.RegisterFile(repository.FileRecord{
		Hash:         hash,
		Path:         fullPath,
		Name:         filename,
		Size:         size,
		Kind:         model.FileTypeBlob,
		MimeType:     mimeType,
		ProviderPath: relPath,
	}); err != nil {
		log.LogError("file-svc: Upload register file failed: %v", err)
		return nil, fmt.Errorf("register file: %w", err)
	}

	log.LogInfo("file-svc: Upload %s completed (hash=%s, size=%d)", filename, hash, size)
	return meta, nil
}

// Verify looks up file metadata by hash, used to verify whether a file exists.
func (s *FileService) Verify(hash string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.Verify")()
	log.LogDebug("file-svc: Verify hash=%s", hash)

	meta, err := s.GetMeta(hash)
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
	_, _ = repository.DeleteFileIndex(hash)
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

	meta, err := s.GetMeta(hash)
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
	if fi, fiErr := repository.GetFileIndex(hash); fiErr == nil && fi != nil && fi.Path != "" && s.isPathAllowed(fi.Path) {
		candidates = append(candidates, fi.Path)
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p) // codeql[go/path-injection]: hash is validated by isValidHash (64-hex) and fi.Path is checked by s.isPathAllowed.
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
