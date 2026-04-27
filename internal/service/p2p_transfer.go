// ChunkedTransfer 实现 P2P 分片传输协议，支持并行分片下载、进度回调和服务端分片请求处理。
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	ChunkSize            = 256 * 1024 // 256KB chunks
	MaxParallelChunks    = 8
	TransferTimeout      = 5 * time.Minute
	ChunkRequestTimeout  = 30 * time.Second
	ProtocolChunk        = "/peerdrive/chunk/1.0.0"
)

// TransferProgress tracks the progress of a file transfer.
type TransferProgress struct {
	Hash         string
	TotalSize    int64
	ReceivedSize int64
	ChunksTotal  int
	ChunksDone   int
	Peers        []peer.ID
	StartTime    time.Time
	Done         bool
	Error        error
	mu           sync.Mutex
	onUpdate     func(progress float64)
}

// Update 更新已接收字节数并触发进度回调（如有设置）。
func (tp *TransferProgress) Update(bytesReceived int64) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.ReceivedSize += bytesReceived
	if tp.onUpdate != nil && tp.TotalSize > 0 {
		pct := float64(tp.ReceivedSize) / float64(tp.TotalSize) * 100
		tp.onUpdate(pct)
	}
}

// Progress 返回当前下载进度百分比（0-100）。
func (tp *TransferProgress) Progress() float64 {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	if tp.TotalSize == 0 {
		return 0
	}
	return float64(tp.ReceivedSize) / float64(tp.TotalSize) * 100
}

// ChunkedTransfer handles chunked file transfers with progress tracking.
type ChunkedTransfer struct {
	svc        *P2PService
	activeJobs map[string]*TransferProgress
	jobsMu     sync.RWMutex
}

// NewChunkedTransfer creates a new chunked transfer handler.
// NewChunkedTransfer 创建分片传输服务实例，注册 libp2p 流处理协议。
func NewChunkedTransfer(svc *P2PService) *ChunkedTransfer {
	ct := &ChunkedTransfer{
		svc:        svc,
		activeJobs: make(map[string]*TransferProgress),
	}
	if svc.IsEnabled() {
		svc.Host.SetStreamHandler(protocol.ID(ProtocolChunk), ct.handleChunkRequest)
	}
	return ct
}

// ActiveJobs returns all active transfer jobs.
// ActiveJobs 返回所有正在进行的传输任务。
func (ct *ChunkedTransfer) ActiveJobs() map[string]*TransferProgress {
	ct.jobsMu.RLock()
	defer ct.jobsMu.RUnlock()
	result := make(map[string]*TransferProgress)
	for k, v := range ct.activeJobs {
		result[k] = v
	}
	return result
}

// GetProgress returns the progress for a transfer job.
// GetProgress 返回指定 hash 的传输进度。
func (ct *ChunkedTransfer) GetProgress(hash string) *TransferProgress {
	ct.jobsMu.RLock()
	defer ct.jobsMu.RUnlock()
	return ct.activeJobs[hash]
}

// DownloadFile downloads a file from P2P network using chunked parallel transfer.
// DownloadFile 从 P2P 网络并行分片下载文件到本地路径，支持进度回调。
func (ct *ChunkedTransfer) DownloadFile(ctx context.Context, hash string, targetPath string, onProgress func(float64)) (*TransferProgress, error) {
	defer log.LogDuration("ChunkedTransfer.DownloadFile")()
	log.LogDebug("p2p-transfer: DownloadFile hash=%s target=%s", hash, targetPath)

	if !ct.svc.IsEnabled() {
		err := fmt.Errorf("p2p not enabled")
		log.LogError("p2p-transfer: DownloadFile failed: %v", err)
		return nil, err
	}

	// Find providers
	providers, err := ct.svc.FindProviders(hash)
	if err != nil || len(providers) == 0 {
		// Try connected peers
		connected := ct.svc.GetConnectedPeers()
		for _, pid := range connected {
			providers = append(providers, peer.AddrInfo{ID: pid})
		}
	}
	if len(providers) == 0 {
		err := fmt.Errorf("no providers found for %s", hash)
		log.LogError("p2p-transfer: DownloadFile: %v", err)
		return nil, err
	}

	log.LogInfo("p2p-transfer: found %d providers for %s", len(providers), hash)

	// First, get total size from any peer
	var totalSize int64
	var metaErr error
	for _, pi := range providers {
		totalSize, metaErr = ct.requestFileSize(ctx, pi.ID, hash)
		if metaErr == nil {
			break
		}
	}
	if metaErr != nil {
		log.LogError("p2p-transfer: cannot determine file size: %v", metaErr)
		return nil, fmt.Errorf("cannot determine file size: %w", metaErr)
	}

	progress := &TransferProgress{
		Hash:      hash,
		TotalSize: totalSize,
		ChunksTotal: int((totalSize + ChunkSize - 1) / ChunkSize),
		Peers:     make([]peer.ID, 0),
		StartTime: time.Now(),
		onUpdate:  onProgress,
	}

	ct.jobsMu.Lock()
	ct.activeJobs[hash] = progress
	ct.jobsMu.Unlock()

	// Download chunks in parallel
	var wg sync.WaitGroup
	chunkCh := make(chan int, progress.ChunksTotal)
	errCh := make(chan error, progress.ChunksTotal)

	// Prepare output file
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		log.LogError("p2p-transfer: create target dir failed: %v", err)
		return nil, fmt.Errorf("create target dir: %w", err)
	}
	outFile, err := os.Create(targetPath)
	if err != nil {
		log.LogError("p2p-transfer: create output file failed: %v", err)
		return nil, fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Pre-allocate file
	outFile.Truncate(totalSize)

	// Launch workers
	numWorkers := MaxParallelChunks
	if numWorkers > progress.ChunksTotal {
		numWorkers = progress.ChunksTotal
	}
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunkIdx := range chunkCh {
				offset := int64(chunkIdx) * ChunkSize
				size := ChunkSize
				if offset+int64(size) > totalSize {
					size = int(totalSize - offset)
				}

				// Try each peer for this chunk
				var chunkData []byte
				var lastErr error
				for _, pi := range providers {
					if ct.svc.Host.Network().Connectedness(pi.ID) != network.Connected {
						connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
						ct.svc.Host.Connect(connCtx, pi)
						cancel()
					}
					chunkData, lastErr = ct.requestChunk(ctx, pi.ID, hash, offset, size)
					if lastErr == nil {
						progress.Peers = append(progress.Peers, pi.ID)
						break
					}
				}
				if lastErr != nil {
					errCh <- fmt.Errorf("chunk %d: %w", chunkIdx, lastErr)
					return
				}

				// Write chunk at correct offset
				if _, err := outFile.WriteAt(chunkData, offset); err != nil {
					errCh <- fmt.Errorf("write chunk %d: %w", chunkIdx, err)
					return
				}

				progress.Update(int64(len(chunkData)))
				progress.mu.Lock()
				progress.ChunksDone++
				progress.mu.Unlock()
			}
		}()
	}

	// Queue all chunks
	for i := 0; i < progress.ChunksTotal; i++ {
		chunkCh <- i
	}
	close(chunkCh)

	wg.Wait()
	close(errCh)

	// Check for errors
	var firstErr error
	for e := range errCh {
		if firstErr == nil {
			firstErr = e
		}
	}

	if firstErr != nil {
		progress.Error = firstErr
		progress.Done = true
		os.Remove(targetPath)
		log.LogError("p2p-transfer: DownloadFile %s failed: %v", hash, firstErr)
		return progress, firstErr
	}

	// Verify hash
	outFile.Sync()
	outFile.Seek(0, 0)
	hasher := sha256.New()
	io.Copy(hasher, outFile)
	if hex.EncodeToString(hasher.Sum(nil)) != hash {
		progress.Error = fmt.Errorf("hash verification failed")
		progress.Done = true
		os.Remove(targetPath)
		log.LogError("p2p-transfer: hash verification failed for %s", hash)
		return progress, progress.Error
	}

	progress.Done = true
	log.LogInfo("p2p-transfer: DownloadFile %s completed (%d bytes, %d chunks)", hash, totalSize, progress.ChunksTotal)
	return progress, nil
}

func (ct *ChunkedTransfer) requestFileSize(ctx context.Context, peerID peer.ID, hash string) (int64, error) {
	stream, err := ct.svc.Host.NewStream(ctx, peerID, protocol.ID(ProtocolExchange))
	if err != nil {
		return 0, err
	}
	defer stream.Close()

	fmt.Fprintf(stream, "SIZE %s\n", hash)
	var status string
	var size int64
	if _, err := fmt.Fscanf(stream, "%s %d\n", &status, &size); err != nil {
		return 0, fmt.Errorf("read size response: %w", err)
	}
	if status != "OK" {
		return 0, fmt.Errorf("peer error: %s", status)
	}
	return size, nil
}

func (ct *ChunkedTransfer) requestChunk(ctx context.Context, peerID peer.ID, hash string, offset int64, size int) ([]byte, error) {
	defer log.LogDuration("ChunkedTransfer.requestChunk")()
	log.LogDebug("p2p-transfer: requestChunk peer=%s hash=%s offset=%d size=%d", peerID.String(), hash, offset, size)

	stream, err := ct.svc.Host.NewStream(ctx, peerID, protocol.ID(ProtocolChunk))
	if err != nil {
		log.LogError("p2p-transfer: requestChunk open stream failed: %v", err)
		return nil, fmt.Errorf("open chunk stream: %w", err)
	}
	defer stream.Close()

	stream.SetReadDeadline(time.Now().Add(ChunkRequestTimeout))
	fmt.Fprintf(stream, "CHUNK %s %d %d\n", hash, offset, size)

	data := make([]byte, size)
	if _, err := io.ReadFull(stream, data); err != nil {
		log.LogError("p2p-transfer: requestChunk read failed: %v", err)
		return nil, fmt.Errorf("read chunk: %w", err)
	}

	log.LogInfo("p2p-transfer: requestChunk from %s returned %d bytes", peerID.String(), len(data))
	return data, nil
}

func (ct *ChunkedTransfer) handleChunkRequest(stream network.Stream) {
	log.LogDebug("p2p-transfer: handleChunkRequest from %s", stream.Conn().RemotePeer().String())
	defer stream.Close()

	var hash string
	var offset int64
	var size int
	if _, err := fmt.Fscanf(stream, "CHUNK %s %d %d\n", &hash, &offset, &size); err != nil {
		log.LogWarn("p2p-transfer: handleChunkRequest bad request: %v", err)
		fmt.Fprintf(stream, "ERR bad request\n")
		return
	}

	if size > ChunkSize {
		log.LogWarn("p2p-transfer: chunk too large from %s: %d (max %d)", stream.Conn().RemotePeer().String(), size, ChunkSize)
		fmt.Fprintf(stream, "ERR chunk too large (max %d)\n", ChunkSize)
		return
	}

	// Look up file
	filePath := filepath.Join(ct.svc.storageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.LogWarn("p2p-transfer: handleChunkRequest file not found: %s", hash)
		fmt.Fprintf(stream, "ERR not found\n")
		return
	}

	if int(offset) >= len(data) {
		log.LogWarn("p2p-transfer: handleChunkRequest offset out of range: %d >= %d", offset, len(data))
		fmt.Fprintf(stream, "ERR offset out of range\n")
		return
	}

	end := int(offset) + size
	if end > len(data) {
		end = len(data)
	}

	log.LogInfo("p2p-transfer: sending chunk %s offset=%d size=%d to %s", hash, offset, end-int(offset), stream.Conn().RemotePeer().String())
	stream.Write(data[offset:end])
}

// handleExchange (updated) now also handles SIZE requests
// We need to update the original handleExchange to support this
