// SyncService syncs collection files to local disk, supporting path filtering (include/exclude pattern matching) and sync state tracking.
package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"peerdrive/internal/downloader"
	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
)

type SyncService struct {
	syncRepo            *repository.SyncRepository
	universalDownloader *downloader.UniversalDownloader
	storageDir          string
}

func NewSyncService(syncRepo *repository.SyncRepository, uniDl *downloader.UniversalDownloader, storageDir string) *SyncService {
	return &SyncService{
		syncRepo:            syncRepo,
		universalDownloader: uniDl,
		storageDir:          storageDir,
	}
}

// SaveToDisk syncs collection files to local disk, supporting path filtering (include/exclude) and sync state tracking.
func (s *SyncService) SaveToDisk(req model.SaveLocalRequest) error {
	// 1. Path Traversal & Volume Root Prevention
	if strings.TrimSpace(req.LocalPath) == "" || strings.Contains(req.LocalPath, "..") {
		return fmt.Errorf("path traversal detected: '..' is not allowed in local path")
	}

	absTargetDir, err := filepath.Abs(req.LocalPath)
	if err != nil {
		return fmt.Errorf("invalid target path: %w", err)
	}
	if pathutil.IsUnsafeRoot(absTargetDir) {
		return fmt.Errorf("refusing to sync into unsafe volume root: %s", absTargetDir)
	}

	// 2. Get Collection Content
	anonColl, err := repository.GetAnonCollectionByHash(req.CollectionHash, s.storageDir)
	if err != nil {
		return fmt.Errorf("failed to get collection metadata: %w", err)
	}
	anonColl.NormalizeEntries()

	// 3. Filter Files
	filesToSave := s.filterFiles(anonColl.Entries, req.Include, req.Exclude)

	// 4. Update Sync State in DB
	syncState := &model.LocalCollectionSync{
		CollectionHash: req.CollectionHash,
		LocalPath:      absTargetDir,
		IncludeFilter:  req.Include,
		ExcludeFilter:  req.Exclude,
	}
	if err := s.syncRepo.UpsertSyncState(syncState); err != nil {
		return fmt.Errorf("failed to update sync state: %w", err)
	}
	s.syncRepo.ClearSyncFiles(req.CollectionHash)

	// 5. Save Files to Disk
	for _, entry := range filesToSave {
		fileHash := entry.GetPrimaryHash()
		if err := s.saveFile(req.CollectionHash, absTargetDir, entry.Path, fileHash); err != nil {
			// Log error but continue with other files
			fmt.Printf("Error saving file %s: %v\n", entry.Path, err)
			s.syncRepo.UpsertFileSyncState(req.CollectionHash, entry.Path, false)
		} else {
			s.syncRepo.UpsertFileSyncState(req.CollectionHash, entry.Path, true)
		}
	}

	return nil
}

// GetStatus queries the local sync state of a collection, returning the list of saved/missing files.
func (s *SyncService) GetStatus(hash string) (*model.SyncStatusResponse, error) {
	state, err := s.syncRepo.GetSyncState(hash)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("collection not saved locally")
	}

	files, err := s.syncRepo.GetSyncFiles(hash)
	if err != nil {
		return nil, err
	}

	var missing []model.LocalSyncFile
	savedCount := 0
	for _, f := range files {
		if f.IsSaved {
			savedCount++
		} else {
			missing = append(missing, f)
		}
	}

	return &model.SyncStatusResponse{
		CollectionHash: state.CollectionHash,
		LocalPath:      state.LocalPath,
		TotalFiles:     len(files),
		SavedFiles:     savedCount,
		MissingFiles:   missing,
		LastSynced:     state.SyncedAt,
	}, nil
}

func (s *SyncService) saveFile(hash, targetDir, relPath, fileHash string) error {
	// Path Traversal Prevention (again, for the file path)
	if relPath == "" || !isRelativePath(relPath) || strings.Contains(relPath, "..") {
		return fmt.Errorf("path traversal detected in file path: %s", relPath)
	}

	fullPath := filepath.Join(targetDir, relPath)

	// Authoritative Root Validation via pathutil.Within
	if !pathutil.Within(targetDir, fullPath) {
		return fmt.Errorf("path traversal attempt: %s is outside %s", fullPath, targetDir)
	}

	// Download from CAS via downloader.UniversalDownloader
	ctx := context.Background()
	data, _, err := s.universalDownloader.Download(ctx, fileHash)
	if err != nil {
		return err
	}

	// Write to disk atomically within targetDir root via pathutil.SafeWriteFileAny
	return pathutil.SafeWriteFileAny([]string{targetDir}, fullPath, data, 0o644)
}

func (s *SyncService) filterFiles(entries []model.AnonCollectionEntry, include, exclude []string) []model.AnonCollectionEntry {
	var result []model.AnonCollectionEntry
	for _, e := range entries {
		if s.isExcluded(e.Path, exclude) {
			continue
		}
		if len(include) > 0 && !s.isIncluded(e.Path, include) {
			continue
		}
		result = append(result, e)
	}
	return result
}

// isExcluded checks whether the path matches any exclusion pattern.
func (s *SyncService) isExcluded(path string, exclude []string) bool {
	for _, pattern := range exclude {
		if s.matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

// isIncluded checks whether the path matches any inclusion pattern.
func (s *SyncService) isIncluded(path string, include []string) bool {
	for _, pattern := range include {
		if s.matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

// matchPattern determines whether path matches pattern (shell-style + directory prefix + substring fallback).
// L2: the original fallback strings.Contains had no path boundary -- excluding "tmp/foo" would also
// exclude "tmp/foobar" (the user wants to exclude a single directory but accidentally hits adjacent files).
// The substring fallback now requires boundaries: either the pattern ends with "/" (explicitly matching
// a whole directory prefix), or the substring must be bounded by a path separator or file boundary.
func (s *SyncService) matchPattern(path, pattern string) bool {
	if pattern == "*" {
		return true
	}
	// Use filepath.Match for proper shell-style pattern matching
	matched, err := filepath.Match(pattern, filepath.Base(path))
	if err == nil && matched {
		return true
	}
	// Also support directory-based matching (e.g., "docs/*")
	if strings.Contains(pattern, "/") {
		matched, err := filepath.Match(pattern, path)
		if err == nil && matched {
			return true
		}
	}
	// Fallback: substring matching requires path boundaries (pattern ending with / = directory prefix;
	// otherwise boundaries must be '/' or string ends) to prevent /tmp/foo from matching /tmp/foobar
	if strings.HasSuffix(pattern, "/") {
		return strings.HasPrefix(path, pattern)
	}
	idx := strings.Index(path, pattern)
	if idx < 0 {
		return false
	}
	leftOK := idx == 0 || path[idx-1] == '/'
	right := idx + len(pattern)
	rightOK := right >= len(path) || path[right] == '/'
	return leftOK && rightOK
}
