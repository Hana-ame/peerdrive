package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
)

// allowedRoots returns all root directories acknowledged by the operator:
// storage root ∪ PEERDRIVE_SHARE_DIRS declared directories ∪ download directory.
//
// Defense: register_local/register_folder/browse/copy all accept caller-supplied paths; without anchoring
// to root directories, any absolute path (e.g. /etc/shadow) could be read back via LocalFetcher / os.Remove
// to constitute arbitrary file read/write.
//
// Why not only check storage root: the operator placing shared directories outside storage (e.g. a drive
// mounted at `/mnt/media`) is a completely legitimate usage, and the old storage-root-only check would
// reject it outright, forcing PEERDRIVE_SHARE_DIRS to be configured inside storage. The relaxed boundary
// is "directories the operator themselves declared", not "arbitrary paths": to share from /etc, you must
// configure /etc into SHARE_DIRS yourself.
//
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
	// Unify by real paths: if an allowed root is a symlink (e.g. ~/Downloads → /mnt/c/..., or macOS
	// /var → /private/var), retain both the configured path and the resolved real path.
	// Otherwise, paths constructed directly from s.storageDir (e.g. upload to /var/folders/...)
	// would fail pickRoot against an allowed root that was rewritten to /private/var/folders/...
	out := make([]string, 0, len(roots)*2)
	for _, r := range roots {
		out = append(out, r)
		if real, err := filepath.EvalSymlinks(r); err == nil && real != r {
			out = append(out, real)
		}
	}
	return out
}

// IsPathAllowed validates whether absPath falls within the operator-acknowledged root directories.
func (s *FileService) IsPathAllowed(absPath string) bool {
	return s.isPathAllowed(absPath)
}

func (s *FileService) isPathAllowed(absPath string) bool {
	if s.storageDir == "" {
		return false
	}
	return pathutil.WithinAny(s.allowedRoots(), absPath)
}

// OpenAllowed safely opens absPath within allowed roots (os.Root, resolution and open in one step).
// Do not fall back to os.Open: that leaves a TOCTOU window where the file could be replaced with a symlink
// between the check and the open.
func (s *FileService) OpenAllowed(absPath string) (*os.File, error) {
	if s.storageDir == "" {
		return nil, fmt.Errorf("path outside storage root")
	}
	return pathutil.SafeOpenAny(s.allowedRoots(), absPath)
}

func (s *FileService) openAllowed(absPath string) (*os.File, error) {
	return s.OpenAllowed(absPath)
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
// whether a symlink exists was checked earlier. This is the write-path version of the same TOCTOU.
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
