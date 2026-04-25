package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type SyncService struct {
	syncRepo   *repository.SyncRepository
	downloader *Downloader
}

func NewSyncService(syncRepo *repository.SyncRepository, downloader *Downloader) *SyncService {
	return &SyncService{
		syncRepo:   syncRepo,
		downloader: downloader,
	}
}

func (s *SyncService) SaveToDisk(req model.SaveLocalRequest) error {
	// 1. Path Traversal Prevention
	if strings.Contains(req.LocalPath, "..") {
		return fmt.Errorf("path traversal detected: '..' is not allowed in local path")
	}
	
	absTargetDir, err := filepath.Abs(req.LocalPath)
	if err != nil {
		return fmt.Errorf("invalid target path: %w", err)
	}

	// 2. Get Collection Content
	storageDir := s.downloader.storageDir
	anonColl, err := repository.GetAnonCollectionByHash(req.CollectionHash, storageDir)
	if err != nil {
		return fmt.Errorf("failed to get collection metadata: %w", err)
	}

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
		if err := s.saveFile(req.CollectionHash, absTargetDir, entry.Path, entry.Hash); err != nil {
			// Log error but continue with other files
			fmt.Printf("Error saving file %s: %v\n", entry.Path, err)
			s.syncRepo.UpsertFileSyncState(req.CollectionHash, entry.Path, false)
		} else {
			s.syncRepo.UpsertFileSyncState(req.CollectionHash, entry.Path, true)
		}
	}

	return nil
}

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
		LocalPath:    state.LocalPath,
		TotalFiles:   len(files),
		SavedFiles:   savedCount,
		MissingFiles: missing,
		LastSynced:   state.SyncedAt,
	}, nil
}

func (s *SyncService) saveFile(hash, targetDir, relPath, fileHash string) error {
	// Path Traversal Prevention (again, for the file path)
	if strings.Contains(relPath, "..") {
		return fmt.Errorf("path traversal detected in file path: %s", relPath)
	}

	fullPath := filepath.Join(targetDir, relPath)
	
	// Final Root Validation
	if !strings.HasPrefix(fullPath, targetDir) {
		return fmt.Errorf("path traversal attempt: %s is outside %s", fullPath, targetDir)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return err
	}

	// Download from CAS
	reader, _, _, err := s.downloader.GetFileStream(fileHash)
	if err != nil {
		return err
	}
	defer reader.Close()

	// Write to disk
	f, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, reader)
	return err
}

func (s *SyncService) filterFiles(entries []model.AnonEntry, include, exclude []string) []model.AnonEntry {
	var result []model.AnonEntry
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

func (s *SyncService) isExcluded(path string, exclude []string) bool {
	for _, pattern := range exclude {
		if s.matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

func (s *SyncService) isIncluded(path string, include []string) bool {
	for _, pattern := range include {
		if s.matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

func (s *SyncService) matchPattern(path, pattern string) bool {
	if pattern == "*" {
		return true
	}
	// Basic wildcard support: *.ext or prefix*
	if strings.Contains(pattern, "*") {
		// This is a very simple wildcard implementation
		// For production, use filepath.Match or a regex
		// simplified: check if path contains the parts of the pattern
		// we'll use a simple contains check for now as a placeholder for real globbing
		return strings.Contains(path, strings.ReplaceAll(pattern, "*", ""))
	}
	return path == pattern
}
