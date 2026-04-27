// ResumeManager 处理断点续传，通过 SQLite 持久化下载进度，支持暂停/恢复/取消操作。
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/repository"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// DownloadProgress tracks a file download session in SQLite.
type DownloadProgress struct {
	Hash         string   `json:"hash"`
	TotalSize    int64    `json:"total_size"`
	ReceivedSize int64    `json:"received_size"`
	LastChunk    int      `json:"last_chunk"`
	ChunksTotal  int      `json:"chunks_total"`
	ChunksDone   int      `json:"chunks_done"`
	PeersUsed    []string `json:"peers_used"`
	StartedAt    string   `json:"started_at"`
	UpdatedAt    string   `json:"updated_at"`
}

// ResumeManager handles resume-able downloads with SQLite-backed progress tracking.
type ResumeManager struct {
	downloadDir string
	maxPeers    int
	p2p         *P2PService
	btSvc       BTDHTProvider
	dualSvc     *DualP2PService
	mu          sync.RWMutex
	active      map[string]context.CancelFunc
}

// BTDHTProvider is the subset of BTDHTService used by ResumeManager.
type BTDHTProvider interface {
	FindProviders(hash string) ([]string, error)
}

// NewResumeManager 创建断点续传管理器实例。
func NewResumeManager(cfg *config.Config, p2p *P2PService, btSvc BTDHTProvider, dualSvc *DualP2PService) *ResumeManager {
	dir := cfg.DownloadDir
	if dir == "" {
		dir = "./downloads"
	}
	maxPeers := cfg.MaxPeers
	if maxPeers < 1 {
		maxPeers = 8
	}
	return &ResumeManager{
		downloadDir: dir,
		maxPeers:    maxPeers,
		p2p:         p2p,
		btSvc:       btSvc,
		dualSvc:     dualSvc,
		active:      make(map[string]context.CancelFunc),
	}
}

// ResumeDownload resumes a download for the given hash. Checks partial file,
// reads saved progress from SQLite, and downloads missing chunks. Returns the
// path to the completed file.
// ResumeDownload 恢复或新启一个断点续传下载任务，从各 P2P 源获取文件分片。
func (rm *ResumeManager) ResumeDownload(ctx context.Context, hash, targetPath string) (string, error) {
	defer log.LogDuration("ResumeManager.ResumeDownload")()
	log.LogDebug("p2p-resume: ResumeDownload hash=%s target=%s", hash, targetPath)

	rctx, cancel := context.WithCancel(ctx)
	rm.mu.Lock()
	if oldCancel, exists := rm.active[hash]; exists {
		oldCancel()
	}
	rm.active[hash] = cancel
	rm.mu.Unlock()
	defer func() {
		rm.mu.Lock()
		delete(rm.active, hash)
		rm.mu.Unlock()
	}()

	if err := os.MkdirAll(rm.downloadDir, 0755); err != nil {
		return "", fmt.Errorf("create download dir: %w", err)
	}
	if targetPath == "" {
		targetPath = filepath.Join(rm.downloadDir, hash)
	}
	os.MkdirAll(filepath.Dir(targetPath), 0755)

	// Load saved progress.
	progress := rm.loadProgress(hash)

	// Check if file already complete.
	if progress != nil && progress.ReceivedSize >= progress.TotalSize && progress.TotalSize > 0 {
		if rm.verifyFileHash(targetPath, hash) {
			log.LogInfo("p2p-resume: %s already complete at %s", hash, targetPath)
			return targetPath, nil
		}
		log.LogWarn("p2p-resume: %s hash mismatch, re-downloading", hash)
		progress = nil
	}

	if progress == nil || progress.TotalSize == 0 {
		totalSize, err := rm.discoverTotalSize(rctx, hash)
		if err != nil {
			log.LogWarn("p2p-resume: cannot get total size, falling back: %v", err)
			return rm.fullFetchFallback(rctx, hash, targetPath)
		}
		progress = &DownloadProgress{
			Hash:        hash,
			TotalSize:   totalSize,
			ChunksTotal: int((totalSize + ChunkSize - 1) / ChunkSize),
			StartedAt:   time.Now().UTC().Format(time.RFC3339),
		}
		rm.saveProgress(progress)
	}

	partialFile, err := os.OpenFile(targetPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return "", fmt.Errorf("open partial file: %w", err)
	}
	defer partialFile.Close()

	currentStat, _ := partialFile.Stat()
	if currentStat.Size() != progress.TotalSize {
		if err := partialFile.Truncate(progress.TotalSize); err != nil {
			return "", fmt.Errorf("truncate: %w", err)
		}
	}

	// Determine which chunks remain.
	chunksNeeded := make([]int, 0)
	for i := progress.ChunksDone; i < progress.ChunksTotal; i++ {
		chunksNeeded = append(chunksNeeded, i)
	}
	log.LogInfo("p2p-resume: %s needs %d/%d chunks", hash, len(chunksNeeded), progress.ChunksTotal)

	if len(chunksNeeded) == 0 {
		if rm.verifyFileHash(targetPath, hash) {
			rm.removeProgress(hash)
			return targetPath, nil
		}
		return "", fmt.Errorf("hash verification failed after resume")
	}

	peers := rm.discoverPeers(rctx, hash)
	if len(peers) == 0 {
		return "", fmt.Errorf("no peers found for %s", hash)
	}

	if err := rm.downloadChunks(rctx, partialFile, hash, chunksNeeded, progress, peers); err != nil {
		return "", err
	}

	if !rm.verifyFileHash(targetPath, hash) {
		os.Remove(targetPath)
		rm.removeProgress(hash)
		return "", fmt.Errorf("hash verification failed for %s", hash)
	}

	rm.removeProgress(hash)
	log.LogInfo("p2p-resume: %s completed to %s", hash, targetPath)
	return targetPath, nil
}

// GetProgress returns saved download progress from SQLite.
// GetProgress 返回指定 hash 的断点续传下载进度。
func (rm *ResumeManager) GetProgress(hash string) *DownloadProgress {
	defer log.LogDuration("ResumeManager.GetProgress")()
	progress := rm.loadProgress(hash)
	if progress == nil {
		return nil
	}
	partialPath := filepath.Join(rm.downloadDir, hash)
	if fi, err := os.Stat(partialPath); err == nil && fi.Size() > progress.ReceivedSize {
		progress.ReceivedSize = fi.Size()
		rm.saveProgress(progress)
	}
	return progress
}

// CancelDownload stops an active download and cleans up the partial file.
// CancelDownload 取消指定 hash 的下载任务并清理临时文件。
func (rm *ResumeManager) CancelDownload(hash string) error {
	defer log.LogDuration("ResumeManager.CancelDownload")()
	rm.mu.Lock()
	if cancel, exists := rm.active[hash]; exists {
		cancel()
		delete(rm.active, hash)
	}
	rm.mu.Unlock()
	partialPath := filepath.Join(rm.downloadDir, hash)
	os.Remove(partialPath)
	rm.removeProgress(hash)
	log.LogInfo("p2p-resume: cancelled %s", hash)
	return nil
}

// --- SQLite persistence ---

func (rm *ResumeManager) loadProgress(hash string) *DownloadProgress {
	if repository.DB == nil {
		return nil
	}
	row := repository.DB.QueryRow(
		`SELECT total_size, received_size, last_chunk, chunks_total, chunks_done, peers_used, started_at, updated_at
		 FROM download_progress WHERE hash = ?`, hash)
	p := &DownloadProgress{Hash: hash}
	var peersStr string
	if err := row.Scan(&p.TotalSize, &p.ReceivedSize, &p.LastChunk,
		&p.ChunksTotal, &p.ChunksDone, &peersStr, &p.StartedAt, &p.UpdatedAt); err != nil {
		return nil
	}
	if peersStr != "" {
		p.PeersUsed = strings.Split(peersStr, ",")
	}
	return p
}

func (rm *ResumeManager) saveProgress(p *DownloadProgress) {
	if repository.DB == nil {
		return
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if p.StartedAt == "" {
		p.StartedAt = p.UpdatedAt
	}
	peersStr := strings.Join(p.PeersUsed, ",")
	repository.DB.Exec(`INSERT OR REPLACE INTO download_progress
		(hash, total_size, received_size, last_chunk, chunks_total, chunks_done, peers_used, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Hash, p.TotalSize, p.ReceivedSize, p.LastChunk,
		p.ChunksTotal, p.ChunksDone, peersStr, p.StartedAt, p.UpdatedAt)
}

func (rm *ResumeManager) removeProgress(hash string) {
	if repository.DB == nil {
		return
	}
	repository.DB.Exec(`DELETE FROM download_progress WHERE hash = ?`, hash)
}

// --- peer discovery & chunk downloading ---

func (rm *ResumeManager) discoverTotalSize(ctx context.Context, hash string) (int64, error) {
	if rm.p2p != nil && rm.p2p.IsEnabled() {
		providers, err := rm.p2p.FindProviders(hash)
		if err == nil {
			for _, pi := range providers {
				connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				if rm.p2p.Host.Network().Connectedness(pi.ID) != network.Connected {
					rm.p2p.Host.Connect(connCtx, pi)
				}
				cancel()
				size, err := rm.p2p.Transfer.requestFileSize(ctx, pi.ID, hash)
				if err == nil {
					return size, nil
				}
			}
		}
		for _, pid := range rm.p2p.GetConnectedPeers() {
			size, err := rm.p2p.Transfer.requestFileSize(ctx, pid, hash)
			if err == nil {
				return size, nil
			}
		}
	}
	return 0, fmt.Errorf("could not determine file size")
}

func (rm *ResumeManager) discoverPeers(ctx context.Context, hash string) []peer.AddrInfo {
	var allPeers []peer.AddrInfo
	seen := make(map[string]bool)
	if rm.p2p != nil && rm.p2p.IsEnabled() {
		providers, err := rm.p2p.FindProviders(hash)
		if err == nil {
			for _, pi := range providers {
				key := pi.ID.String()
				if !seen[key] {
					seen[key] = true
					allPeers = append(allPeers, pi)
				}
			}
		}
		for _, pid := range rm.p2p.GetConnectedPeers() {
			key := pid.String()
			if !seen[key] {
				seen[key] = true
				allPeers = append(allPeers, peer.AddrInfo{ID: pid})
			}
		}
	}
	return allPeers
}

func (rm *ResumeManager) downloadChunks(ctx context.Context, file *os.File, hash string,
	chunks []int, progress *DownloadProgress, peers []peer.AddrInfo) error {
	if len(chunks) == 0 {
		return nil
	}
	numWorkers := rm.maxPeers
	if numWorkers > len(chunks) {
		numWorkers = len(chunks)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}
	chunkCh := make(chan int, len(chunks))
	errCh := make(chan error, len(chunks))
	var wg sync.WaitGroup

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunkIdx := range chunkCh {
				select {
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				default:
				}
				offset := int64(chunkIdx) * ChunkSize
				size := int(ChunkSize)
				if offset+int64(size) > progress.TotalSize {
					size = int(progress.TotalSize - offset)
				}
				var chunkData []byte
				var lastErr error
				for _, pi := range peers {
					if rm.p2p != nil && rm.p2p.IsEnabled() && pi.ID != "" {
						chunkData, lastErr = rm.p2p.Transfer.requestChunk(ctx, pi.ID, hash, offset, size)
						if lastErr == nil {
							progress.PeersUsed = append(progress.PeersUsed, pi.ID.String())
							break
						}
					}
				}
				if lastErr != nil {
					errCh <- fmt.Errorf("chunk %d: %w", chunkIdx, lastErr)
					return
				}
				if _, err := file.WriteAt(chunkData, offset); err != nil {
					errCh <- fmt.Errorf("write chunk %d: %w", chunkIdx, err)
					return
				}
				progress.ReceivedSize += int64(size)
				progress.ChunksDone++
				progress.LastChunk = chunkIdx
				rm.saveProgress(progress)
				log.LogDebug("p2p-resume: chunk %d done (offset=%d)", chunkIdx, offset)
			}
		}()
	}
	for _, ci := range chunks {
		chunkCh <- ci
	}
	close(chunkCh)
	wg.Wait()
	close(errCh)
	var firstErr error
	for e := range errCh {
		if firstErr == nil {
			firstErr = e
		}
	}
	return firstErr
}

func (rm *ResumeManager) verifyFileHash(path, expectedHash string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == expectedHash
}

func (rm *ResumeManager) fullFetchFallback(ctx context.Context, hash, targetPath string) (string, error) {
	if rm.dualSvc != nil {
		data, err := rm.dualSvc.FetchFile(ctx, hash)
		if err == nil {
			os.MkdirAll(filepath.Dir(targetPath), 0755)
			os.WriteFile(targetPath, data, 0644)
			return targetPath, nil
		}
	}
	if rm.p2p != nil && rm.p2p.IsEnabled() {
		data, err := rm.p2p.FetchFile(ctx, hash, nil)
		if err == nil {
			os.MkdirAll(filepath.Dir(targetPath), 0755)
			os.WriteFile(targetPath, data, 0644)
			return targetPath, nil
		}
	}
	return "", fmt.Errorf("all sources exhausted for %s", hash)
}

// fetchChunkBT downloads a single chunk from a BT peer using HTTP Range.
func (rm *ResumeManager) fetchChunkBT(ctx context.Context, addr, hash string, offset int64, size int) ([]byte, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("http://%s/files/%s", addr, hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+int64(size)-1))
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
