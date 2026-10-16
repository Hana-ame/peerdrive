package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
)

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
	_, _ = repository.UpsertFileIndex(hashStr, fullPath, cid, int64(len(data)), false)
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
	_, _ = repository.UpsertFileIndex(sha256hex, destPath, filepath.Base(srcPath), size, false)
	return nil
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

	// Get properties on the already-opened fd (fstat), rather than stat by path again --
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
	} else {
		filename = filepath.Base(filepath.Clean(filename))
		if filename == "." || filename == "/" || filename == "\\" {
			filename = filepath.Base(absPath)
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

	// Sync-register file_index (hash -> absolute path): this index is the only source of truth
	// for "what files this node can serve externally".
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

	// Depth limit: only enabled when PEERDRIVE_FOLDER_MAX_DEPTH>0 is explicitly configured.
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
