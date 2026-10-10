// FileService handles file upload, URL registration, local file registration/bulk registration, file verification/deletion, and directory browsing.
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
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

// ImportGatewayData writes data fetched from IPFS public gateways to disk and registers metadata/provider.
// M2 convergence: the original logic was inlined in the download controller DownloadByCID gateway fallback branch
// (write to disk + InsertFileMeta + InsertFileProvider trio). Returns the content hash.
func (s *FileService) ImportGatewayData(cid string, data []byte) (string, error) {
	h := sha256.Sum256(data)
	hashStr := hex.EncodeToString(h[:])
	relPath := filepath.Join(hashStr[:2], hashStr)
	fullPath := filepath.Join(s.storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", err
	}
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hashStr,
		Size:     int64(len(data)),
		Filename: cid,
		Type:     model.FileTypeBlob,
	})
	_ = repository.InsertFileProvider(hashStr, "local", relPath)
	return hashStr, nil
}

// RegisterBTFile registers a file completed by BT download: writes to storage directory + registers metadata/provider.
// M2 convergence: the original logic was inlined in the router.go BT onComplete callback (InsertFileMeta +
// InsertFileProvider + file copy trio). Returns an error (the original inline version ignored all errors, here
// at least storage failures are exposed).
func (s *FileService) RegisterBTFile(sha256hex string, size int64, srcPath string) error {
	if !isValidHash(sha256hex) {
		return fmt.Errorf("invalid sha256 %q", sha256hex)
	}
	relPath := filepath.Join(sha256hex[:2], sha256hex)
	destPath := filepath.Join(s.storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	input, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := repository.InsertFileMeta(&model.FileMeta{
		Hash:     sha256hex,
		Size:     size,
		Filename: filepath.Base(srcPath),
		Type:     model.FileTypeBlob,
	}); err != nil {
		log.LogWarn("file-svc: RegisterBTFile InsertFileMeta: %v", err)
	}
	if err := repository.InsertFileProvider(sha256hex, "local", relPath); err != nil {
		log.LogWarn("file-svc: RegisterBTFile InsertFileProvider: %v", err)
	}
	return nil
}

// ListAll lists all blob files (M2 convergence: the file controller ListFiles previously called repository.ListAllFiles directly).
func (s *FileService) ListAll(sortBy string) ([]model.FileListItem, error) {
	items, err := repository.ListAllFiles(sortBy)
	if err != nil {
		return nil, err
	}
	// Issue #280: detect file_index vs file_meta divergence and log a warning.
	// file_index (P2P path index) and file_meta (content-addressed) are
	// maintained independently and can silently diverge.
	if onlyIdx, onlyMeta, cerr := repository.CheckFileIndexConsistency(); cerr == nil && (onlyIdx > 0 || onlyMeta > 0) {
		log.LogWarn("file_index/file_meta divergence: %d hash(es) only in file_index, %d only in file_meta — consider migration (Issue #280)", onlyIdx, onlyMeta)
	}
	return items, nil
}

// isPathAllowed validates whether absPath falls within the **operator-acknowledged root directories**:
// storage root ∪ PEERDRIVE_SHARE_DIRS declared directories ∪ download directory.
//
// Defense: register_local/register_folder/browse/copy all accept caller-supplied paths; without anchoring
// to root directories, any absolute path (e.g. /etc/shadow) could be read back via LocalFetcher / os.Remove
// to constitute arbitrary file read/write.
//
// Why not only check storage root: the operator placing shared directories outside storage (e.g. a drive
// mounted at `/mnt/media`) is a **completely legitimate** usage, and the old storage-root-only check would
// reject it outright, forcing PEERDRIVE_SHARE_DIRS to be configured inside storage -- the documentation was
// forced to say "shared directories must be under downloads", which was caused by this restriction. The relaxed
// boundary is "directories the operator themselves declared", not "arbitrary paths": to share from /etc,
// you must configure /etc into SHARE_DIRS yourself.
// allowedRoots is all root directories acknowledged by the operator: storage root ∪ SHARE_DIRS ∪ download root.
// The check (isPathAllowed) and open (openAllowed) must share the same source; otherwise a gap appears where
// "the check says it's fine but the open uses a different one."
func (s *FileService) allowedRoots() []string {
	if s.storageDir == "" {
		return nil
	}
	roots := []string{s.storageDir}
	if s.cfg != nil {
		roots = append(roots, pathutil.SplitList(s.cfg.ShareDirs)...)
		if s.cfg.DownloadDir != "" {
			roots = append(roots, s.cfg.DownloadDir)
		}
	}
	// Unify by real paths: if an allowed root is a symlink (e.g. ~/Downloads → /mnt/c/...), Eval
	// to the real path, otherwise RegisterFolder(Eval'd real path) and root(symlink) would never match.
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		if real, err := filepath.EvalSymlinks(r); err == nil {
			r = real
		}
		out = append(out, r)
	}
	return out
}

func (s *FileService) isPathAllowed(absPath string) bool {
	if s.storageDir == "" {
		return false
	}
	return pathutil.WithinAny(s.allowedRoots(), absPath)
}

// openAllowed safely opens absPath within allowed roots (os.Root, resolution and open in one step).
// Do not fall back to os.Open: that leaves a TOCTOU window where the file could be replaced with a symlink
// between the check and the open.
func (s *FileService) openAllowed(absPath string) (*os.File, error) {
	if s.storageDir == "" {
		return nil, fmt.Errorf("path outside storage root")
	}
	return pathutil.SafeOpenAny(s.allowedRoots(), absPath)
}

// RegisterLocal computes the SHA256 hash of a local file and registers it in file_meta and file_providers.
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

	// Security boundary: only allow registering files within the storage root directory.
	// Pitfall: previously any absolute path was accepted, combined with LocalFetcher's provider read-back = anonymous arbitrary file read.
	if !s.isPathAllowed(absPath) {
		log.LogWarn("file-svc: RegisterLocal path outside allowed roots (storage/share/download): %s", absPath)
		return "", fmt.Errorf("path outside storage root")
	}

	f, err := s.openAllowed(absPath)
	if err != nil {
		log.LogError("file-svc: RegisterLocal open %s failed: %v", absPath, err)
		return "", err
	}
	defer f.Close()

	// Get properties on the **already-opened fd** (fstat), rather than stat by path again --
	// one fewer path resolution, and one fewer "replaced after check" window.
	info, err := f.Stat()
	if err != nil {
		log.LogError("file-svc: RegisterLocal stat %s failed: %v", absPath, err)
		return "", err
	}
	size := info.Size()

	// Hard links (shares the pathutil decision on the peer side too): if the same inode has
	// one name inside an allowed root and another outside, path checks won't catch it.
	// Checking only on the transport side leaves HTTP register_local as another open path.
	// Pass a handle, not FileInfo: on Windows only the handle can query NumberOfLinks.
	if err := pathutil.RejectHardlink(absPath, f); err != nil {
		log.LogWarn("file-svc: RegisterLocal hard link rejected: %v", err)
		return "", err
	}

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

	// Derive filename from path if not provided
	if filename == "" {
		filename = filepath.Base(absPath)
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

	// Sync-register file_index (hash -> absolute path): this index is the **only source of truth**
	// for "what files this node can serve externally":
	//   - The external share manifest (M2 share frame, service/nodeshare.go reads via
	//     transport.FileIndexService.List) filters files under ShareDirs according to it;
	//   - Cross-node pull (M3) uses Info(hash) to determine "already local" and uses its path to
	//     register after writing to disk.
	// Consequence of missing this step: files registered/uploaded by the operator are visible in their
	// own drive UI, but when peers ask the share frame they always get files:[] -- the drive chain
	// breaks at the "manifest" stage.
	// Written at the same layer as InsertFileProvider; failure only warns (does not block registration).
	if _, err := repository.UpsertFileIndex(hash, absPath, filename, size, false); err != nil {
		log.LogWarn("file-svc: RegisterLocal upsert file_index %s failed: %v", hash, err)
	}

	log.LogInfo("file-svc: RegisterLocal %s -> hash=%s size=%d", absPath, hash, size)
	return hash, nil
}

// RegisterFolder recursively registers all files in a folder, returning each file's filename and hash.
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
	// Follow symlinks (~/Downloads -> /mnt/c/...): otherwise WalkDir treats symlinks as regular files and traversal is empty.
	if real, err := filepath.EvalSymlinks(absDir); err == nil {
		absDir = real
	}

	// Security boundary: folders must also be anchored within storage root (otherwise bulk read of arbitrary directories).
	if !s.isPathAllowed(absDir) {
		log.LogWarn("file-svc: RegisterFolder outside allowed roots (storage/share/download): %s", absDir)
		return nil, fmt.Errorf("path outside storage root")
	}

	// Depth limit: only enabled when PEERDRIVE_FOLDER_MAX_DEPTH>0 is explicitly configured (default 0 = keep
	// full recursion, compatible with existing recursive semantics in TestRegisterFolder etc.). On 2026-09-26,
	// default of 1 caused CI failures, so reverted to explicit-only (set to 1 explicitly when deploying ~/Downloads).
	maxDepth := 0
	if s.cfg != nil && s.cfg.FolderMaxDepth > 0 {
		maxDepth = s.cfg.FolderMaxDepth
	}

	var results []map[string]string
	err := filepath.WalkDir(absDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == absDir {
			return nil // Root directory itself: no need to descend further
		}
		rel, relErr := filepath.Rel(absDir, p)
		if relErr != nil {
			return relErr
		}
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if d.IsDir() {
			if maxDepth > 0 && depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if maxDepth > 0 && depth > maxDepth {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		hash, err := s.RegisterLocal(p, info.Name())
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

// ResolveURL fetches a file from a URL, computes SHA256 and detects MIME type, without writing to storage/DB.
// When followRedirects=false, 301/302 redirects are rejected.
func (s *FileService) ResolveURL(rawURL string, followRedirects bool) (hash string, mimeType string, size int64, body []byte, filename string, err error) {
	defer log.LogDuration("FileService.ResolveURL")()
	log.LogDebug("file-svc: ResolveURL url=%s followRedirects=%v", rawURL, followRedirects)

	// SSRF guard (2026-10-04): this function fetches a **caller-supplied** URL on the node's
	// behalf, exactly like the P2P pull verb does — but until now it had no guard at all.
	// Measured before the fix: POST /files/register_url with
	// {"url":"http://127.0.0.1:<node port>/peerjs/share"} returned 201 and stored the node's
	// own admin response as a file. Shared guard so the two surfaces cannot drift again.
	if err := urlguard.GuardExternalURL(rawURL); err != nil {
		log.LogWarn("file-svc: ResolveURL rejected %s: %v", rawURL, err)
		return "", "", 0, nil, "", fmt.Errorf("URL rejected by SSRF guard: %w", err)
	}

	safeTransport := urlguard.NewSafeTransport()
	client := &http.Client{
		Timeout:   5 * time.Minute,
		Transport: safeTransport,
	}
	if !followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else {
		// Per-hop redirect validation: checking only the first hop lets a public URL 302 to
		// 127.0.0.1 straight back into the internal network. Same reasoning as pull.go.
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("redirect exceeds 10 hops, aborting")
			}
			if err := urlguard.GuardExternalURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect target rejected: %w", err)
			}
			return nil
		}
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		log.LogError("file-svc: ResolveURL GET %s failed: %v", rawURL, err)
		return "", "", 0, nil, "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.LogWarn("file-svc: ResolveURL %s returned status %d", rawURL, resp.StatusCode)
		return "", "", 0, nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	maxLimit := s.cfg.MaxUploadBytes
	if maxLimit <= 0 {
		maxLimit = 100 * 1024 * 1024 // 100MB default fallback limit
	}
	if cl := resp.ContentLength; cl > maxLimit {
		return "", "", 0, nil, "", fmt.Errorf("content length %d exceeds maximum allowed limit of %d bytes", cl, maxLimit)
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxLimit+1))
	if err != nil {
		log.LogError("file-svc: ResolveURL read body failed: %v", err)
		return "", "", 0, nil, "", fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxLimit {
		return "", "", 0, nil, "", fmt.Errorf("response body exceeds maximum allowed limit of %d bytes", maxLimit)
	}

	h := sha256.Sum256(body)
	hash = hex.EncodeToString(h[:])
	size = int64(len(body))

	// MIME detection: magic number sniffing first, Content-Type fallback
	mimeType = http.DetectContentType(body[:min(len(body), 512)])
	if ct := resp.Header.Get("Content-Type"); ct != "" && mimeType == "application/octet-stream" {
		mimeType = ct
	}

	// Filename extraction: Content-Disposition -> URL basename
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, encoded, ok := strings.Cut(cd, "filename*="); ok {
			if idx := strings.Index(encoded, "''"); idx > 0 && idx+2 < len(encoded) {
				part := encoded[idx+2:]
				if end := strings.IndexByte(part, ';'); end > 0 {
					part = part[:end]
				}
				decoded, decErr := percentUnescape(strings.TrimSpace(part))
				if decErr == nil && decoded != "" {
					filename = decoded
				}
			}
		}
		if filename == "" {
			if _, f, ok := strings.Cut(cd, "filename="); ok {
				filename = strings.Trim(f, "\" ")
			}
		}
	}
	if filename == "" {
		filename = path.Base(rawURL)
	}

	log.LogInfo("file-svc: ResolveURL %s -> hash=%s mime=%s size=%d", rawURL, hash, mimeType, size)
	return hash, mimeType, size, body, filename, nil
}

// RegisterURL fetches a file from a URL, computes SHA256 and registers it (provider_type="http"), automatically following 301/302 redirects.
func (s *FileService) RegisterURL(rawURL string, filename string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.RegisterURL")()
	log.LogDebug("file-svc: RegisterURL url=%s filename=%s", rawURL, filename)

	hash, mimeType, size, body, autoFilename, err := s.ResolveURL(rawURL, true)
	if err != nil {
		return nil, err
	}
	if filename == "" {
		filename = autoFilename
	}

	// 3. Insert into file_meta (skip if already exists)
	existing, _ := repository.GetFileMeta(hash)
	if existing == nil {
		err = repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Size:     size,
			MimeType: mimeType,
			Gziped:   false,
			Filename: filename,
			Type:     repository.FileTypeBlob,
		})
		if err != nil {
			log.LogError("file-svc: RegisterURL insert meta failed: %v", err)
			return nil, fmt.Errorf("insert meta: %w", err)
		}
	}

	// Insert file_provider (type "http", path = url)
	err = repository.InsertFileProvider(hash, "http", rawURL)
	if err != nil {
		log.LogError("file-svc: RegisterURL insert provider failed: %v", err)
		return nil, fmt.Errorf("insert provider: %w", err)
	}

	// 4. If storage enabled, save to content-addressed storage
	if s.storageEnable {
		relPath := hash[:2] + "/" + hash
		fullPath := filepath.Join(s.storageDir, relPath)
		if err := pathutil.SafeWriteFileAny(s.allowedRoots(), fullPath, body, 0644); err != nil {
			log.LogWarn("file-svc: RegisterURL save to storage failed (non-fatal): %v", err)
		}
	}

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     size,
		MimeType: mimeType,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	}

	log.LogInfo("file-svc: RegisterURL %s -> hash=%s size=%d", rawURL, hash, size)
	return meta, nil
}

// percentUnescape decodes percent-encoded sequences (e.g. %20 -> space).
func percentUnescape(s string) (string, error) {
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, err1 := hexDecodeNibble(s[i+1])
			lo, err2 := hexDecodeNibble(s[i+2])
			if err1 == nil && err2 == nil {
				buf.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		buf.WriteByte(s[i])
	}
	return buf.String(), nil
}

func hexDecodeNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex nibble: %c", c)
	}
}

// Upload uploads a file to content-addressed storage, computes SHA256 and registers metadata and provider.
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
	// No longer using os.Rename(tmpName, fullPath): the source is in the system temp directory, which is
	// outside the allowed roots. The rename step cannot be Root-ified (it would follow symlinks on the
	// dst parent directory). Changed to "open target within allowed roots + copy", which also eliminates
	// the need for a cross-device fallback branch.
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

	// Sync-register file_index, same step RegisterLocal already takes (file_service.go:276).
	//
	// Why this was missing (found 2026-10-04): file_index is the **only source of truth** for
	// "which files may this node serve outward" — service/nodeshare.go builds the share manifest
	// from it via transport.FileIndexService.List. Upload wrote file_meta + file_providers only,
	// so an uploaded file showed up in the operator's own /files listing yet **never** appeared in
	// the share manifest: a peer connected successfully and saw files:[] forever. The comment on
	// RegisterLocal already spelled out precisely this trap; Upload simply never took the step.
	//
	// Measured before the fix: POST /files/upload → GET /peerjs/share returns files:[]. Issuing
	// one POST /files/register_local for the same file made the manifest list it immediately.
	// The admin console's "+ Upload File" button goes through this endpoint
	// (front/src/pages/Drive.jsx → ws.upload(..., '/files/upload')), so the primary operator
	// workflow could not share anything it uploaded.
	//
	// Failure only warns: the bytes are already stored and the meta row is committed, so a
	// bookkeeping problem must not turn a successful upload into an error.
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
	// Defense: an unvalidated hash goes into the provider path, combined with LocalFetcher read-back = arbitrary file delete.
	// Since RegisterLocal is now anchored to the storage root, this adds a fallback to prevent historical data from having provider paths outside the root.
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

// BrowseDir browses a local directory, returning a list of files and subdirectories (including size and modification time).
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

	// Security boundary: only allow browsing within storage root; reject arbitrary directory listing (prerequisite for arbitrary file read).
	if !s.isPathAllowed(absDir) {
		log.LogWarn("file-svc: BrowseDir outside storage root: %s", absDir)
		return nil, fmt.Errorf("path outside storage root")
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

// copyInto writes the content of src into dst within allowed roots (creation + write in the same Root session).
//
// Why not use os.Create(dst) / os.Rename: both follow symlinks on the dst parent directory, and
// whether a symlink exists was checked **earlier**. This is the write-path version of the same TOCTOU.
func (s *FileService) copyInto(roots []string, src, dst string) error {
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := pathutil.SafeOpenFileAny(roots, dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	return nil
}

// This previously had a copyFile(src, dst) function: os.Create(dst) would follow symlinks on the
// dst parent directory to escape the allowed roots. **All call sites have been migrated to copyInto**
// (2026-09-20 write-path TOCTOU cleanup), and the function was removed -- leaving a "can write to
// any file" old function in the code is just waiting for someone to pick it up again.

// CopyFile copies a file identified by hash to a destination path within storage
// and registers the copy in file_providers. Returns the destination path.
func (s *FileService) CopyFile(hash string, destPath string) (string, error) {
	defer log.LogDuration("FileService.CopyFile")()
	log.LogDebug("file-svc: CopyFile hash=%s dest=%s", hash, destPath)

	if !s.storageEnable {
		return "", ErrStorageDisabled
	}

	// Security boundary: destination must be within storage root, and hash must be valid.
	// Pitfall: previously absolute paths were used as-is / relative paths could escape with ../, combined
	// with public upload this could write to any file (e.g. authorized_keys).
	// Check before writing to disk rather than checking Rel after writing (old code at line 615 only modified DB records after writing).
	//
	// Why these two checks must come **before looking up the source meta** (discovered during 2026-09-20 penetration testing):
	// The original code did GetFileMeta first, so an unauthorized dest returned "source hash not found" --
	//   1. The rejection reason was masked, and ops investigating logs would treat it as a data problem;
	//   2. The security boundary wasn't the first gate: if someone later added "auto-fetch when source doesn't exist",
	//      data would be prepared before path validation, degrading to arbitrary file write;
	//   3. It also leaked whether a given hash exists.
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

	// Verify source exists and get its data
	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		return "", fmt.Errorf("lookup source meta: %w", err)
	}
	if meta == nil {
		return "", fmt.Errorf("source hash %s not found", hash)
	}

	// Get source data via the download pipeline
	body, err := s.ReadFile(hash)
	if err != nil {
		return "", fmt.Errorf("read source file: %w", err)
	}

	// Write to destination (only allowed within allowed roots).
	//
	// Not using os.MkdirAll + os.WriteFile: those two steps would follow symlinks on the dst parent
	// directory to escape the roots. isPathAllowed is checked **before** them, leaving a TOCTOU window.
	// SafeWriteFileAny opens os.Root on the allowed roots, completing parent directory creation and file
	// write in one step; a dst escaping via symlinks will fail at this step.
	if err := pathutil.SafeWriteFileAny(s.allowedRoots(), absDest, body, 0644); err != nil {
		log.LogWarn("file-svc: CopyFile write %s rejected: %v", absDest, err)
		return "", fmt.Errorf("write dest file: %w", err)
	}

	// Register the copy as a local provider
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

	// Try content-addressed paths first
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

	// Fallback to DB providers
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

// MaxUploadBytes returns the maximum upload bytes based on authentication status (authenticated users use cfg.MaxUploadBytes, anonymous users use cfg.MaxUploadBytesAnon).
func (s *FileService) MaxUploadBytes(c *gin.Context) int64 {
	if authed, exists := c.Get("authenticated"); exists && authed.(bool) {
		return s.cfg.MaxUploadBytes
	}
	return s.cfg.MaxUploadBytesAnon
}
