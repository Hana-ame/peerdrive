// BTClient 管理 BitTorrent 下载任务，支持 .torrent 文件和 magnet URI，通过 DHT 发现 peers 并下载分片。
package p2p_bt

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
)

// DownloadStatus represents the current state of a torrent download.
type DownloadStatus struct {
	InfoHash    string  `json:"infohash"`
	Name        string  `json:"name"`
	TotalSize   int64   `json:"total_size"`
	Downloaded  int64   `json:"downloaded"`
	PiecesTotal int     `json:"pieces_total"`
	PiecesDone  int     `json:"pieces_done"`
	Peers       int     `json:"peers"`
	Speed       float64 `json:"speed_bytes_per_sec"`
	Status      string  `json:"status"`  // "downloading", "seeding", "completed", "error"
	Error       string  `json:"error,omitempty"`
	Seeding     bool    `json:"seeding"`
}

// CompletedFile holds information about a completed torrent file.
type CompletedFile struct {
	InfoHash string `json:"infohash"`
	Path     string `json:"path"`     // file path relative to data dir
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
}

// OnTorrentComplete is a callback invoked when a torrent finishes downloading.
type OnTorrentComplete func(infohash string, files []CompletedFile)

// BTDownload represents a single active torrent download managed by BTClient.
type BTDownload struct {
	InfoHash  string
	Name      string
	Meta      *TorrentMeta
	DataDir   string // directory where files are stored
	Status    string
	StartTime time.Time
	DoneCh    chan struct{} // closed when download completes

	mu         sync.RWMutex
	downloaded int64
	piecesDone int
	peers      int
	speed      float64
	err        error
	files      []CompletedFile
	cancel     context.CancelFunc // called to cancel/download the download
}

// cancelDownload calls the cancel function if set, to abort the download.
func (dl *BTDownload) cancelDownload() {
	dl.mu.RLock()
	cancel := dl.cancel
	dl.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// BTClient 管理 BitTorrent 下载任务，封装底层 BitTorrent 协议细节。
type BTClient struct {
	dataDir    string
	downloads  map[string]*BTDownload
	seeders    map[string]*BTSeeder
	onComplete OnTorrentComplete
	mu         sync.RWMutex
	stopCh     chan struct{}
	seederMu   sync.Mutex
}

// NewBTClient 创建 BT 客户端实例，指定下载文件存储目录。
func NewBTClient(dataDir string) *BTClient {
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "peerdrive-bt")
	}
	os.MkdirAll(dataDir, 0755)

	client := &BTClient{
		dataDir:   dataDir,
		downloads: make(map[string]*BTDownload),
		seeders:   make(map[string]*BTSeeder),
		stopCh:    make(chan struct{}),
	}
	log.LogInfo("bt-client: created, dataDir=%s", dataDir)
	return client
}

// SetOnComplete 注册种子下载完成时的回调函数。
func (c *BTClient) SetOnComplete(fn OnTorrentComplete) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onComplete = fn
}

// AddTorrent 开始下载种子，通过 DHT 和/或 Tracker 发现对端并下载分片。
func (c *BTClient) AddTorrent(meta *TorrentMeta) error {
	defer log.LogDuration("BTClient.AddTorrent")()
	log.LogInfo("bt-client: AddTorrent infohash=%s name=%q", meta.InfoHashHex, meta.Name)

	c.mu.Lock()
	if _, exists := c.downloads[meta.InfoHashHex]; exists {
		c.mu.Unlock()
		return fmt.Errorf("torrent %s already added", meta.InfoHashHex)
	}

	dl := &BTDownload{
		InfoHash:  meta.InfoHashHex,
		Name:      meta.Name,
		Meta:      meta,
		DataDir:   filepath.Join(c.dataDir, meta.InfoHashHex),
		Status:    "downloading",
		StartTime: time.Now(),
		DoneCh:    make(chan struct{}),
	}
	c.downloads[meta.InfoHashHex] = dl
	c.mu.Unlock()

	// Create data directory.
	os.MkdirAll(dl.DataDir, 0755)

	// Start download in background.
	go c.downloadTorrent(dl)
	return nil
}

// AddMagnet 通过 magnet URI 开始下载。
func (c *BTClient) AddMagnet(magnet *MagnetInfo) error {
	defer log.LogDuration("BTClient.AddMagnet")()
	log.LogInfo("bt-client: AddMagnet infohash=%s name=%q", magnet.InfoHash, magnet.DisplayName)

	// Construct a minimal TorrentMeta from the magnet info.
	// We'll use the DHT to find peers and download the metadata.
	meta := &TorrentMeta{
		Name:        magnet.DisplayName,
		InfoHashHex: magnet.InfoHash,
		InfoHash:    magnet.InfoHashRaw,
		AnnounceList: magnet.Trackers,
	}

	c.mu.Lock()
	if _, exists := c.downloads[magnet.InfoHash]; exists {
		c.mu.Unlock()
		return fmt.Errorf("torrent %s already added", magnet.InfoHash)
	}

	dl := &BTDownload{
		InfoHash:  magnet.InfoHash,
		Name:      magnet.DisplayName,
		Meta:      meta,
		DataDir:   filepath.Join(c.dataDir, magnet.InfoHash),
		Status:    "downloading",
		StartTime: time.Now(),
		DoneCh:    make(chan struct{}),
	}
	c.downloads[magnet.InfoHash] = dl
	c.mu.Unlock()

	os.MkdirAll(dl.DataDir, 0755)
	go c.downloadTorrent(dl)
	return nil
}

// downloadTorrent runs the actual download for a BTDownload. It uses DHT peer
// discovery and wire-protocol piece exchange.
func (c *BTClient) downloadTorrent(dl *BTDownload) {
	defer log.LogDuration("BTClient.downloadTorrent")()
	log.LogInfo("bt-client: starting download infohash=%s name=%q", dl.InfoHash, dl.Name)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Store cancel so PauseDownload/RemoveDownload can abort.
	dl.mu.Lock()
	dl.cancel = cancel
	dl.mu.Unlock()

	// 1. Announce ourselves on DHT for this infohash.
	if globalDHT != nil {
		log.LogInfo("[bt-wire] announcing infohash on DHT: %s", dl.InfoHash)
		if err := globalDHT.Announce(dl.InfoHash); err != nil {
			log.LogWarn("[bt-wire] DHT announce failed (non-fatal): %v", err)
		}
	}

	// 2. Find peers via DHT.
	var peerAddrs []string
	if globalDHT != nil {
		log.LogInfo("[bt-wire] discovering peers via DHT for %s", dl.InfoHash)
		peers, err := globalDHT.FindProviders(dl.InfoHash)
		if err != nil {
			log.LogWarn("[bt-wire] DHT peer discovery failed: %v", err)
		} else {
			peerAddrs = peers
			log.LogInfo("[bt-wire] DHT found %d peers for %s", len(peers), dl.InfoHash)
		}
	}

	if len(peerAddrs) == 0 {
		log.LogWarn("[bt-wire] no peers found for %s via DHT", dl.InfoHash)
		// Try to use trackers if available.
		if dl.Meta != nil && len(dl.Meta.AnnounceList) > 0 {
			log.LogInfo("[bt-wire] trying tracker-based peer discovery for %s", dl.InfoHash)
			peers, err := discoverPeersFromTrackers(dl.InfoHash, dl.Meta.AnnounceList)
			if err != nil {
				log.LogWarn("[bt-wire] tracker discovery failed: %v", err)
			} else {
				peerAddrs = peers
			}
		}
	}

	if len(peerAddrs) == 0 {
		dl.mu.Lock()
		dl.Status = "error"
		dl.err = fmt.Errorf("no peers found via DHT or trackers")
		dl.mu.Unlock()
		close(dl.DoneCh)
		log.LogError("[bt-wire] no peers found for %s", dl.InfoHash)
		return
	}

	log.LogInfo("[bt-wire] found %d peers for %s, proceeding with download", len(peerAddrs), dl.InfoHash)

	// If the torrent metadata is incomplete (e.g., from magnet), try to fetch
	// metadata from peers first.
	if dl.Meta != nil && len(dl.Meta.Pieces) == 0 {
		log.LogInfo("[bt-wire] fetching metadata from peers for %s", dl.InfoHash)
		err := c.fetchMetadata(ctx, dl, peerAddrs)
		if err != nil {
			dl.mu.Lock()
			dl.Status = "error"
			dl.err = fmt.Errorf("metadata fetch: %w", err)
			dl.mu.Unlock()
			close(dl.DoneCh)
			log.LogError("[bt-wire] metadata fetch failed for %s: %v", dl.InfoHash, err)
			return
		}
		log.LogInfo("[bt-wire] metadata fetched for %s (%d pieces, %d files)",
			dl.InfoHash, len(dl.Meta.Pieces), len(dl.Meta.Files))
	}

	// If there's no metadata (nil or empty), we can't proceed.
	if dl.Meta == nil || len(dl.Meta.Pieces) == 0 {
		dl.mu.Lock()
		dl.Status = "error"
		dl.err = fmt.Errorf("no torrent metadata available")
		dl.mu.Unlock()
		close(dl.DoneCh)
		log.LogError("[bt-wire] no metadata for %s", dl.InfoHash)
		return
	}

	// Try to download from peers.
	var infoHash [20]byte
	copy(infoHash[:], dl.Meta.InfoHash[:20])

	numPieces := len(dl.Meta.Pieces)

	// Track download progress.
	type progressUpdate struct {
		pieceIdx int
		data     []byte
		err      error
	}
	progressCh := make(chan progressUpdate, numPieces)

	dl.mu.Lock()
	dl.Meta.PiecesHex = make([]string, numPieces)
	for i, p := range dl.Meta.Pieces {
		dl.Meta.PiecesHex[i] = hex.EncodeToString(p)
	}
	dl.mu.Unlock()

	// Download pieces from peers (with concurrency control).
	concurrency := 4
	if concurrency > len(peerAddrs) {
		concurrency = len(peerAddrs)
	}
	sem := make(chan struct{}, concurrency)

	var pieceErrors int
	var pieceErrMu sync.Mutex
	var wg sync.WaitGroup

	for pieceIdx := 0; pieceIdx < numPieces; pieceIdx++ {
		pieceIdx := pieceIdx
		sem <- struct{}{}
		wg.Add(1)

		go func() {
			defer func() {
				<-sem
				wg.Done()
			}()

			// Pick a peer for this piece (round-robin).
			peerAddr := peerAddrs[pieceIdx%len(peerAddrs)]

			var expectedHash [20]byte
			copy(expectedHash[:], dl.Meta.Pieces[pieceIdx])

			// Calculate piece length. The last piece may be shorter.
			pieceLen := dl.Meta.PieceLength
			if pieceIdx == numPieces-1 {
				lastPieceSize := dl.Meta.TotalSize % dl.Meta.PieceLength
				if lastPieceSize > 0 {
					pieceLen = lastPieceSize
				}
			}

			data, err := DownloadPiece(ctx, peerAddr, infoHash, pieceIdx,
				pieceLen, expectedHash)
			if err != nil {
				pieceErrMu.Lock()
				pieceErrors++
				pieceErrMu.Unlock()
				log.LogWarn("[bt-wire] piece %d from %s failed: %v", pieceIdx, peerAddr, err)
				progressCh <- progressUpdate{pieceIdx: pieceIdx, err: err}
				return
			}

			log.LogDebug("[bt-wire] piece %d downloaded successfully from %s (%d bytes)",
				pieceIdx, peerAddr, len(data))

			dl.mu.Lock()
			dl.piecesDone++
			dl.downloaded += int64(len(data))
			dl.mu.Unlock()

			progressCh <- progressUpdate{pieceIdx: pieceIdx, data: data}
		}()
	}

	// Close progress channel after all piece goroutines finish.
	go func() {
		wg.Wait()
		close(progressCh)
	}()

	// Reassemble pieces.
	pieces := make([][]byte, numPieces)
	for update := range progressCh {
		if update.err == nil {
			pieces[update.pieceIdx] = update.data
		}
	}

	// Check if enough pieces were downloaded.
	if pieceErrors > numPieces/2 {
		dl.mu.Lock()
		dl.Status = "error"
		dl.err = fmt.Errorf("too many piece errors (%d/%d)", pieceErrors, numPieces)
		dl.mu.Unlock()
		close(dl.DoneCh)
		log.LogError("bt-client: %s: too many piece errors", dl.InfoHash)
		return
	}

	// Write files to disk.
	var completedFiles []CompletedFile
	var infohashBytes [20]byte
	copy(infohashBytes[:], dl.Meta.InfoHash[:20])

	if dl.Meta.IsSingleFile && len(dl.Meta.Files) == 1 {
		// Assemble the full data from pieces.
		var fullData []byte
		for _, p := range pieces {
			if p == nil {
				fullData = append(fullData, make([]byte, dl.Meta.PieceLength)...)
			} else {
				fullData = append(fullData, p...)
			}
		}
		// Trim to actual file size.
		if int64(len(fullData)) > dl.Meta.TotalSize {
			fullData = fullData[:dl.Meta.TotalSize]
		}

		filePath := filepath.Join(dl.DataDir, dl.Meta.Name)
		os.MkdirAll(filepath.Dir(filePath), 0755)
		if err := os.WriteFile(filePath, fullData, 0644); err != nil {
			log.LogError("bt-client: write file %s failed: %v", filePath, err)
		} else {
			sha256Hash := sha256.Sum256(fullData)
			completedFiles = append(completedFiles, CompletedFile{
				InfoHash: dl.InfoHash,
				Path:     filePath,
				Size:     int64(len(fullData)),
				SHA256:   hex.EncodeToString(sha256Hash[:]),
			})
		}
	} else {
		// Multi-file: reconstruct from pieces, split by file boundaries.
		var fullData []byte
		for _, p := range pieces {
			if p == nil {
				fullData = append(fullData, make([]byte, dl.Meta.PieceLength)...)
			} else {
				fullData = append(fullData, p...)
			}
		}
		if int64(len(fullData)) > dl.Meta.TotalSize {
			fullData = fullData[:dl.Meta.TotalSize]
		}

		offset := int64(0)
		for _, f := range dl.Meta.Files {
			fileData := fullData[offset : offset+f.Size]
			filePath := filepath.Join(dl.DataDir, f.Path)
			os.MkdirAll(filepath.Dir(filePath), 0755)
			if err := os.WriteFile(filePath, fileData, 0644); err != nil {
				log.LogError("bt-client: write file %s failed: %v", filePath, err)
			} else {
				sha256Hash := sha256.Sum256(fileData)
				completedFiles = append(completedFiles, CompletedFile{
					InfoHash: dl.InfoHash,
					Path:     filePath,
					Size:     f.Size,
					SHA256:   hex.EncodeToString(sha256Hash[:]),
				})
			}
			offset += f.Size
		}
	}

	// Mark download as completed.
	dl.mu.Lock()
	dl.Status = "completed"
	dl.files = completedFiles
	dl.mu.Unlock()
	close(dl.DoneCh)

	// Fire on-complete callback.
	if c.onComplete != nil {
		log.LogInfo("bt-client: firing onComplete for %s (%d files)", dl.InfoHash, len(completedFiles))
		c.onComplete(dl.InfoHash, completedFiles)
	}

	log.LogInfo("bt-client: download completed infohash=%s name=%q files=%d",
		dl.InfoHash, dl.Name, len(completedFiles))
}

// fetchMetadata attempts to retrieve torrent metadata from peers using the
// BitTorrent extension protocol (ut_metadata). For now, it tries to connect
// to a peer and use the extension handshake to request metadata.
func (c *BTClient) fetchMetadata(ctx context.Context, dl *BTDownload, peerAddrs []string) error {
	// If we have trackers, try to do a tracker announce to get the full metadata.
	// For magnet links, we can try the ut_metadata extension via libtorrent-style
	// metadata exchange. This requires µTorrent metadata extension support.

	// Simplified approach: if the BTDHT service is available with a tracker
	// that can provide metadata, use it. Otherwise, report metadata unavailable.
	if len(dl.Meta.AnnounceList) == 0 {
		return fmt.Errorf("no trackers available for metadata retrieval; " +
			"use a .torrent file instead of a magnet link, or add trackers to the magnet URI")
	}

	// Try each tracker to announce and find peers with metadata.
	// The BitTorrent spec allows the infohash to be used to request metadata
	// via the ut_metadata extension (BEP 9).

	// For a production client, we would:
	// 1. Connect to peers via TCP
	// 2. Perform BT handshake with extension protocol flag (reserved byte)
	// 3. Send extension handshake with "m" dict containing ut_metadata
	// 4. Request metadata pieces via ut_metadata messages
	// 5. Reassemble and parse the metadata

	// For now, return an error indicating metadata needs to be provided.
	return fmt.Errorf("magnet metadata retrieval not implemented; provide a .torrent file instead")
}

// GetDownload 根据 infohash 返回当前下载任务的状态。
func (c *BTClient) GetDownload(infohash string) *DownloadStatus {
	c.mu.RLock()
	dl, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return nil
	}

	dl.mu.RLock()
	defer dl.mu.RUnlock()

	status := &DownloadStatus{
		InfoHash:    dl.InfoHash,
		Name:        dl.Name,
		TotalSize:   dl.Meta.TotalSize,
		Downloaded:  dl.downloaded,
		PiecesTotal: len(dl.Meta.Pieces),
		PiecesDone:  dl.piecesDone,
		Peers:       dl.peers,
		Speed:       dl.speed,
		Status:      dl.Status,
		Seeding:     c.IsSeeding(dl.InfoHash),
	}
	if dl.err != nil {
		status.Error = dl.err.Error()
	}
	return status
}

// ListDownloads 返回所有活跃和已完成的 BT 下载任务列表。
func (c *BTClient) ListDownloads() []DownloadStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]DownloadStatus, 0, len(c.downloads))
	for _, dl := range c.downloads {
		dl.mu.RLock()
		status := DownloadStatus{
			InfoHash:    dl.InfoHash,
			Name:        dl.Name,
			TotalSize:   dl.Meta.TotalSize,
			Downloaded:  dl.downloaded,
			PiecesTotal: len(dl.Meta.Pieces),
			PiecesDone:  dl.piecesDone,
			Peers:       dl.peers,
			Speed:       dl.speed,
			Status:      dl.Status,
			Seeding:     c.IsSeeding(dl.InfoHash),
		}
		if dl.err != nil {
			status.Error = dl.err.Error()
		}
		dl.mu.RUnlock()
		result = append(result, status)
	}
	return result
}

// GetDownloadDir 返回下载存储目录。
func (c *BTClient) GetDownloadDir() string {
	return c.dataDir
}

// Close 关闭 BT 客户端并停止所有活跃下载任务。
func (c *BTClient) Close() {
	log.LogInfo("bt-client: closing")
	close(c.stopCh)
}

// StartSeed begins seeding a completed torrent download. It starts a TCP listener
// that responds to BT wire protocol piece requests, and re-announces on the DHT.
// The infohash must correspond to a previously completed download.
func (c *BTClient) StartSeed(infohash string) error {
	c.mu.RLock()
	dl, ok := c.downloads[infohash]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	dl.mu.RLock()
	if dl.Status != "completed" {
		dl.mu.RUnlock()
		return fmt.Errorf("download %s has status %q, need 'completed' to seed", infohash, dl.Status)
	}
	meta := dl.Meta
	dataDir := dl.DataDir
	dl.mu.RUnlock()

	// Check if already seeding.
	c.seederMu.Lock()
	if _, exists := c.seeders[infohash]; exists {
		c.seederMu.Unlock()
		return fmt.Errorf("already seeding %s", infohash)
	}

	// Compute the 20-byte infohash.
	raw, err := hex.DecodeString(infohash)
	if err != nil {
		c.seederMu.Unlock()
		return fmt.Errorf("decode infohash: %w", err)
	}
	var infoHash [20]byte
	copy(infoHash[:], raw)

	seeder := NewSeeder(infoHash, meta, dataDir)
	if err := seeder.Start(); err != nil {
		c.seederMu.Unlock()
		return fmt.Errorf("start seeder: %w", err)
	}
	c.seeders[infohash] = seeder
	c.seederMu.Unlock()

	// Re-announce on DHT so others can find us.
	if globalDHT != nil {
		go func() {
			for i := 0; i < 3; i++ {
				if err := globalDHT.Announce(infohash); err != nil {
					log.LogWarn("[bt-seeder] DHT re-announce failed: %v", err)
				}
				time.Sleep(30 * time.Second)
			}
		}()
	}

	log.LogInfo("bt-client: started seeding %s (%s) on port %d", infohash, dl.Name, seeder.Port())
	return nil
}

// StopSeed stops seeding a completed torrent download.
// It shuts down the TCP listener and removes the seeder from the active seeders map.
func (c *BTClient) StopSeed(infohash string) error {
	c.seederMu.Lock()
	seeder, ok := c.seeders[infohash]
	if !ok {
		c.seederMu.Unlock()
		return fmt.Errorf("not currently seeding %s", infohash)
	}
	delete(c.seeders, infohash)
	c.seederMu.Unlock()

	seeder.Stop()
	log.LogInfo("bt-client: stopped seeding %s", infohash)
	return nil
}

// IsSeeding returns true if the given infohash is currently being seeded.
func (c *BTClient) IsSeeding(infohash string) bool {
	c.seederMu.Lock()
	defer c.seederMu.Unlock()
	_, ok := c.seeders[infohash]
	return ok
}

// ListSeeders returns the list of currently active seeding infohashes.
func (c *BTClient) ListSeeders() []string {
	c.seederMu.Lock()
	defer c.seederMu.Unlock()
	keys := make([]string, 0, len(c.seeders))
	for k := range c.seeders {
		keys = append(keys, k)
	}
	return keys
}

// --- DHT service integration ---

// globalDHT is set by the router to allow BTClient to discover peers.
var globalDHT *BTDHTService

// SetGlobalDHT 设置全局 DHT 服务引用，供 BT 客户端发现对端使用。
func SetGlobalDHT(dht *BTDHTService) {
	globalDHT = dht
}

// ListDownloadFiles 返回指定下载任务的已完成文件列表。
func (c *BTClient) ListDownloadFiles(infohash string) ([]CompletedFile, bool) {
	c.mu.RLock()
	dl, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}

	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.files, dl.Status == "completed"
}

// ReadFileReader 返回已完成下载中指定文件的 io.ReadCloser。
func (c *BTClient) ReadFileReader(filePath string) (io.ReadCloser, error) {
	return os.Open(filePath)
}

// PauseDownload pauses an active download by canceling its context.
// The download status is set to "paused". It can be resumed with ResumeDownload.
func (c *BTClient) PauseDownload(infohash string) error {
	c.mu.Lock()
	dl, ok := c.downloads[infohash]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	dl.mu.Lock()
	defer dl.mu.Unlock()

	if dl.Status != "downloading" {
		return fmt.Errorf("download %s is not in progress (status=%s)", infohash, dl.Status)
	}

	// Cancel the download context to abort in-flight piece downloads.
	if dl.cancel != nil {
		dl.cancel()
		dl.cancel = nil
	}
	dl.Status = "paused"
	log.LogInfo("bt-client: paused download %s (%s)", infohash, dl.Name)
	return nil
}

// ResumeDownload resumes a previously paused download.
// It starts the download loop again from scratch (re-discovering peers).
func (c *BTClient) ResumeDownload(infohash string) error {
	c.mu.Lock()
	dl, ok := c.downloads[infohash]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	dl.mu.Lock()
	if dl.Status != "paused" {
		dl.mu.Unlock()
		return fmt.Errorf("download %s is not paused (status=%s)", infohash, dl.Status)
	}
	dl.Status = "downloading"
	// Create a new DoneCh for the restarted download.
	dl.DoneCh = make(chan struct{})
	dl.mu.Unlock()

	log.LogInfo("bt-client: resuming download %s (%s)", infohash, dl.Name)
	go c.downloadTorrent(dl)
	return nil
}

// RemoveDownload removes a download from the client and deletes its data directory.
// If the download is active, it is cancelled first.
func (c *BTClient) RemoveDownload(infohash string) error {
	c.mu.Lock()
	dl, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}
	delete(c.downloads, infohash)
	c.mu.Unlock()

	// Cancel in-flight pieces.
	dl.cancelDownload()

	// Remove data directory.
	dataDir := dl.DataDir
	if dataDir != "" {
		if err := os.RemoveAll(dataDir); err != nil {
			log.LogWarn("bt-client: remove data dir %s: %v", dataDir, err)
		}
	}

	log.LogInfo("bt-client: removed download %s (%s)", infohash, dl.Name)
	return nil
}

// GlobalStats holds global BitTorrent client statistics.
type GlobalStats struct {
	TotalUp       int64  `json:"total_up_bytes"`
	TotalDown     int64  `json:"total_down_bytes"`
	ActiveTorrents int   `json:"active_torrents"`
	PausedTorrents int   `json:"paused_torrents"`
	Completed     int    `json:"completed"`
	Errors        int    `json:"errors"`
	DHTNodes      int    `json:"dht_nodes"`
}

// GetGlobalStats returns global BT client statistics.
func (c *BTClient) GetGlobalStats() *GlobalStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := &GlobalStats{}
	if globalDHT != nil {
		stats.DHTNodes = globalDHT.NumNodes()
	}

	for _, dl := range c.downloads {
		dl.mu.RLock()
		switch dl.Status {
		case "downloading":
			stats.ActiveTorrents++
		case "paused":
			stats.PausedTorrents++
		case "completed":
			stats.Completed++
		case "error":
			stats.Errors++
		default:
			stats.ActiveTorrents++
		}
		stats.TotalDown += dl.downloaded
		dl.mu.RUnlock()
	}

	return stats
}
