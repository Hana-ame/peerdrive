// SyncService 将集合文件同步到本地磁盘，支持路径过滤（include/exclude 模式匹配）和同步状态跟踪。
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"peerdrive/internal/downloader"
	"peerdrive/internal/model"
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

// SaveToDisk 将集合文件同步到本地磁盘，支持路径过滤（include/exclude）和同步状态跟踪。
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
	anonColl, err := repository.GetAnonCollectionByHash(req.CollectionHash, s.storageDir)
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

// GetStatus 查询集合的本地同步状态，返回已保存/缺失的文件列表。
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

	// Download from CAS via downloader.UniversalDownloader
	ctx := context.Background()
	data, _, err := s.universalDownloader.Download(ctx, fileHash)
	if err != nil {
		return err
	}

	// Write to disk
	return os.WriteFile(fullPath, data, 0644)
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

// isExcluded 检查路径是否匹配任一排除模式。
func (s *SyncService) isExcluded(path string, exclude []string) bool {
	for _, pattern := range exclude {
		if s.matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

// isIncluded 检查路径是否匹配任一包含模式。
func (s *SyncService) isIncluded(path string, include []string) bool {
	for _, pattern := range include {
		if s.matchPattern(path, pattern) {
			return true
		}
	}
	return false
}

// matchPattern 判断 path 是否匹配 pattern（shell 风格 + 目录前缀 + 子串回退）。
// L2：原 fallback strings.Contains 无路径边界——排除 "tmp/foo" 会把
// "tmp/foobar" 也排除（用户想排除单个目录却误伤相邻文件）。
// 子串回退加边界：要么 pattern 以 "/" 结尾（明确想匹配整个目录前缀），
// 要么子串前后必须是路径分隔符或文件边界。
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
	// Fallback：子串匹配必须有路径边界（pattern 以 / 结尾 = 目录前缀；
	// 否则前后边界必须是 '/' 或字符串端），防止 /tmp/foo 误匹配 /tmp/foobar
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
