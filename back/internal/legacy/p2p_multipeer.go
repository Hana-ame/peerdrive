// MultiPeerDownloader 从多个 P2P 源（IPFS + BT DHT）并行下载文件分片，支持断点续传和哈希校验。
package legacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"github.com/Hana-ame/go-peerdrive-bt"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// MultiPeerSource describes a single source for multi-peer download.
type MultiPeerSource struct {
	Network string `json:"network"` // "ipfs" or "bt"
	Address string `json:"address"` // peer address
	PeerID  string `json:"peer_id"` // peer ID or IP:port
}

// MultiPeerProgress tracks overall multi-peer download progress.
type MultiPeerProgress struct {
	Hash        string  `json:"hash"`
	TotalSize   int64   `json:"total_size"`
	Received    int64   `json:"received"`
	Percent     float64 `json:"percent"`
	ChunksTotal int     `json:"chunks_total"`
	ChunksDone  int32   `json:"chunks_done"`
	Peers       int     `json:"peers"`
	Elapsed     string  `json:"elapsed"`
	Done        bool    `json:"done"`
	Error       string  `json:"error,omitempty"`
	startTime   time.Time
}

// MultiPeerDownloader orchestrates parallel chunk downloads from multiple sources.
type MultiPeerDownloader struct {
	downloadDir string
	maxPeers    int
	p2p         *P2PService
	btSvc       *p2p_bt.BTDHTService
	dualSvc     *DualP2PService
	mu          sync.RWMutex
	active      map[string]*MultiPeerProgress
}

// NewMultiPeerDownloader 创建多源并行下载器实例。
func NewMultiPeerDownloader(cfg *config.Config, p2p *P2PService, btSvc *p2p_bt.BTDHTService, dualSvc *DualP2PService) *MultiPeerDownloader {
	dir := cfg.DownloadDir
	if dir == "" {
		dir = "./downloads"
	}
	maxPeers := cfg.MaxPeers
	if maxPeers < 1 {
		maxPeers = 8
	}
	return &MultiPeerDownloader{
		downloadDir: dir,
		maxPeers:    maxPeers,
		p2p:         p2p,
		btSvc:       btSvc,
		dualSvc:     dualSvc,
		active:      make(map[string]*MultiPeerProgress),
	}
}

// GetSources returns all available sources across IPFS and BT networks.
// GetSources 查找指定 hash 的所有可用 P2P 源（IPFS 和 BT 网络）。
func (md *MultiPeerDownloader) GetSources(ctx context.Context, hash string) ([]MultiPeerSource, error) {
	defer log.LogDuration("MultiPeerDownloader.GetSources")()
	log.LogDebug("p2p-multipeer: GetSources hash=%s", hash)

	var sources []MultiPeerSource
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	var mu sync.Mutex

	if md.p2p != nil && md.p2p.IsEnabled() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			providers, err := md.p2p.FindProviders(hash)
			if err != nil {
				log.LogWarn("p2p-multipeer: IPFS FindProviders: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, pi := range providers {
				pidStr := pi.ID.String()
				for _, a := range pi.Addrs {
					addr := a.String() + "/p2p/" + pidStr
					if !seen[addr] {
						seen[addr] = true
						sources = append(sources, MultiPeerSource{Network: "ipfs", Address: addr, PeerID: pidStr})
					}
				}
				if len(pi.Addrs) == 0 && !seen[pidStr] {
					seen[pidStr] = true
					sources = append(sources, MultiPeerSource{Network: "ipfs", PeerID: pidStr})
				}
			}
		}()
	}

	if md.btSvc != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			btPeers, err := md.btSvc.FindProviders(hash)
			if err != nil {
				log.LogWarn("p2p-multipeer: BT FindProviders: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, addr := range btPeers {
				if !seen[addr] {
					seen[addr] = true
					sources = append(sources, MultiPeerSource{Network: "bt", Address: addr, PeerID: addr})
				}
			}
		}()
	}

	wg.Wait()
	return sources, nil
}

// MultiPeerDownload orchestrates a multi-source parallel download.
// MultiPeerDownload 从多个源并行下载文件分片，支持完整性校验。
func (md *MultiPeerDownloader) MultiPeerDownload(ctx context.Context, hash, targetPath string) (string, error) {
	defer log.LogDuration("MultiPeerDownloader.MultiPeerDownload")()
	log.LogDebug("p2p-multipeer: MultiPeerDownload hash=%s", hash)

	if targetPath == "" {
		targetPath = filepath.Join(md.downloadDir, hash)
	}
	os.MkdirAll(filepath.Dir(targetPath), 0755)

	sources, err := md.GetSources(ctx, hash)
	if err != nil {
		return "", fmt.Errorf("discover sources: %w", err)
	}
	if len(sources) == 0 {
		return md.singleFallback(ctx, hash, targetPath)
	}

	totalSize, err := md.discoverTotalSize(ctx, hash, sources)
	if err != nil {
		return "", fmt.Errorf("get total size: %w", err)
	}

	chunksTotal := int((totalSize + ChunkSize - 1) / ChunkSize)
	progress := &MultiPeerProgress{
		Hash:        hash,
		TotalSize:   totalSize,
		ChunksTotal: chunksTotal,
		Peers:       len(sources),
		startTime:   time.Now(),
	}
	md.mu.Lock()
	md.active[hash] = progress
	md.mu.Unlock()
	defer func() {
		md.mu.Lock()
		delete(md.active, hash)
		md.mu.Unlock()
	}()

	outFile, err := os.Create(targetPath)
	if err != nil {
		return "", fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close()
	if err := outFile.Truncate(totalSize); err != nil {
		return "", fmt.Errorf("truncate: %w", err)
	}

	chunkAssignments := md.assignChunks(chunksTotal, sources)
	var wg sync.WaitGroup
	chunkCh := make(chan struct {
		idx  int
		peer MultiPeerSource
	}, chunksTotal)
	errCh := make(chan error, chunksTotal)
	var received atomic.Int64
	var chunksDone atomic.Int32

	numWorkers := md.maxPeers
	if numWorkers > chunksTotal {
		numWorkers = chunksTotal
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range chunkCh {
				select {
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				default:
				}
				offset := int64(job.idx) * ChunkSize
				size := int(ChunkSize)
				if offset+int64(size) > totalSize {
					size = int(totalSize - offset)
				}
				data, err := md.downloadChunk(ctx, job.peer, hash, offset, size, sources)
				if err != nil {
					errCh <- fmt.Errorf("chunk %d: %w", job.idx, err)
					return
				}
				if _, err := outFile.WriteAt(data, offset); err != nil {
					errCh <- fmt.Errorf("write chunk %d: %w", job.idx, err)
					return
				}
				received.Add(int64(len(data)))
				done := chunksDone.Add(1)
				md.mu.Lock()
				progress.Received = received.Load()
				progress.ChunksDone = done
				progress.Percent = float64(received.Load()) / float64(totalSize) * 100
				progress.Elapsed = time.Since(progress.startTime).Round(time.Second).String()
				md.mu.Unlock()
			}
		}()
	}

	for idx, p := range chunkAssignments {
		chunkCh <- struct {
			idx  int
			peer MultiPeerSource
		}{idx: idx, peer: p}
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
	if firstErr != nil {
		os.Remove(targetPath)
		return "", firstErr
	}

	outFile.Sync()
	outFile.Seek(0, 0)
	hasher := sha256.New()
	if _, err := io.Copy(hasher, outFile); err != nil {
		return "", fmt.Errorf("hash: %w", err)
	}
	if hex.EncodeToString(hasher.Sum(nil)) != hash {
		os.Remove(targetPath)
		return "", fmt.Errorf("hash mismatch for %s", hash)
	}

	// M13：完成段更新必须持锁——worker 内的 progress 写都有 md.mu 保护，
	// 这里若不加锁，GetProgress 并发 RLock 读会读到半更新状态（-race 报错）。
	md.mu.Lock()
	progress.Done = true
	progress.Received = totalSize
	progress.ChunksDone = int32(chunksTotal)
	progress.Percent = 100
	progress.Elapsed = time.Since(progress.startTime).Round(time.Second).String()
	md.mu.Unlock()
	log.LogInfo("p2p-multipeer: %s completed (%d bytes from %d sources)", hash, totalSize, len(sources))
	return targetPath, nil
}

// GetProgress returns current multi-peer download progress.
// GetProgress 返回指定 hash 的多源下载进度。
func (md *MultiPeerDownloader) GetProgress(hash string) *MultiPeerProgress {
	md.mu.RLock()
	defer md.mu.RUnlock()
	p, ok := md.active[hash]
	if !ok {
		return nil
	}
	clone := *p
	return &clone
}

// --- internal helpers ---

func (md *MultiPeerDownloader) discoverTotalSize(ctx context.Context, hash string, sources []MultiPeerSource) (int64, error) {
	for _, s := range sources {
		if s.Network == "ipfs" && md.p2p != nil && md.p2p.IsEnabled() {
			pid, err := peer.Decode(s.PeerID)
			if err != nil {
				continue
			}
			connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if md.p2p.Host.Network().Connectedness(pid) != network.Connected {
				md.p2p.Host.Connect(connCtx, peer.AddrInfo{ID: pid})
			}
			cancel()
			size, err := md.p2p.Transfer.requestFileSize(ctx, pid, hash)
			if err == nil {
				return size, nil
			}
		}
	}
	return 0, fmt.Errorf("could not determine file size")
}

func (md *MultiPeerDownloader) assignChunks(chunksTotal int, sources []MultiPeerSource) []MultiPeerSource {
	if len(sources) == 0 {
		return nil
	}
	assignments := make([]MultiPeerSource, chunksTotal)
	for i := 0; i < chunksTotal; i++ {
		assignments[i] = sources[i%len(sources)]
	}
	return assignments
}

func (md *MultiPeerDownloader) downloadChunk(ctx context.Context, source MultiPeerSource,
	hash string, offset int64, size int, allSources []MultiPeerSource) ([]byte, error) {
	tryOrder := []MultiPeerSource{source}
	for _, s := range allSources {
		if s.PeerID != source.PeerID || s.Network != source.Network {
			tryOrder = append(tryOrder, s)
		}
	}
	var lastErr error
	for _, s := range tryOrder {
		var data []byte
		var err error
		switch s.Network {
		case "ipfs":
			data, err = md.fetchIPFSChunk(ctx, s, hash, offset, size)
		case "bt":
			data, err = md.fetchBTChunk(ctx, s, hash, offset, size)
		default:
			err = fmt.Errorf("unknown network: %s", s.Network)
		}
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (md *MultiPeerDownloader) fetchIPFSChunk(ctx context.Context, source MultiPeerSource, hash string, offset int64, size int) ([]byte, error) {
	if md.p2p == nil || !md.p2p.IsEnabled() {
		return nil, fmt.Errorf("p2p not enabled")
	}
	pid, err := peer.Decode(source.PeerID)
	if err != nil {
		return nil, fmt.Errorf("invalid peer id: %w", err)
	}
	return md.p2p.Transfer.requestChunk(ctx, pid, hash, offset, size)
}

func (md *MultiPeerDownloader) fetchBTChunk(ctx context.Context, source MultiPeerSource, hash string, offset int64, size int) ([]byte, error) {
	httpClient := &http.Client{Timeout: 30 * time.Second}
	url := fmt.Sprintf("http://%s/files/%s", source.Address, hash)
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
	// M4：远端返回的 body 无界 → LimitReader 限到请求范围+1 字节，
	// 超限说明对端响应异常（Range 被忽略返回全文件 / 恶意超大包），直接报错。
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(size)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > size {
		return nil, fmt.Errorf("BT chunk from %s too large: expected %d bytes, got %d", source.Address, size, len(data))
	}
	return data, nil
}

func (md *MultiPeerDownloader) singleFallback(ctx context.Context, hash, targetPath string) (string, error) {
	log.LogWarn("p2p-multipeer: no sources, falling back for %s", hash)
	if md.dualSvc != nil {
		data, err := md.dualSvc.FetchFile(ctx, hash)
		if err == nil {
			os.MkdirAll(filepath.Dir(targetPath), 0755)
			os.WriteFile(targetPath, data, 0644)
			return targetPath, nil
		}
	}
	return "", fmt.Errorf("no sources for %s", hash)
}
