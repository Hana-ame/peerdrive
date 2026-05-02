// BTClient 包装 anacrolix/torrent 库，管理 BitTorrent 下载任务。
// DHT 对等发现、线协议、Tracker 通信、做种均由库内部处理。
package p2p_bt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"peerdrive/internal/log"
)

// globalDHT 保留给 BEP 44/51 及 GetGlobalStats().DHTNodes 使用。
var globalDHT *BTDHTService

// SetGlobalDHT 设置全局 DHT 服务引用。
func SetGlobalDHT(dht *BTDHTService) {
	globalDHT = dht
}

// ---- 内部状态 ----

type downloadState struct {
	infoHashHex string
	name        string
	t           *torrent.Torrent
	status      string // downloading / paused / completed / error / seeding
	err         error
	files       []CompletedFile
	doneCh      chan struct{}
	completed   bool
	startTime   time.Time
	seeding     bool
}

// ---- BTClient ----

// BTClient 管理 BitTorrent 下载任务。
type BTClient struct {
	cl          *torrent.Client
	dataDir     string
	onComplete  OnTorrentComplete
	mu          sync.RWMutex
	downloads   map[string]*downloadState
	customPeers map[string][]string
}

// NewBTClient 创建 BT 客户端实例。
func NewBTClient(dataDir string) *BTClient {
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "peerdrive-bt")
	}
	os.MkdirAll(dataDir, 0755)

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = false
	cfg.NoUpload = true
	cfg.DisableUTP = true

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		log.LogError("bt-client: failed to create torrent client: %v", err)
		return nil
	}

	client := &BTClient{
		cl:          cl,
		dataDir:     dataDir,
		downloads:   make(map[string]*downloadState),
		customPeers: make(map[string][]string),
	}
	log.LogInfo("bt-client: created, dataDir=%s", dataDir)
	return client
}

// SetOnComplete 注册下载完成回调。
func (c *BTClient) SetOnComplete(fn OnTorrentComplete) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onComplete = fn
}

// ---- 添加下载 ----

// AddTorrentBytes 从原始 .torrent 文件字节启动下载，返回解析后的元信息。
func (c *BTClient) AddTorrentBytes(data []byte) (*TorrentMeta, error) {
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid torrent file: %w", err)
	}

	infoHash := mi.HashInfoBytes()
	ih := infoHash.HexString()

	c.mu.Lock()
	if _, exists := c.downloads[ih]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("torrent %s already added", ih)
	}
	c.mu.Unlock()

	t, err := c.cl.AddTorrent(mi)
	if err != nil {
		return nil, fmt.Errorf("add torrent: %w", err)
	}

	// 等待元信息可用（.torrent 文件通常立即可用）
	<-t.GotInfo()

	meta := torrentMetaFromLibrary(mi, t)

	ds := &downloadState{
		infoHashHex: ih,
		name:        meta.Name,
		t:           t,
		status:      "downloading",
		startTime:   time.Now(),
		doneCh:      make(chan struct{}),
	}

	c.mu.Lock()
	c.downloads[ih] = ds
	c.mu.Unlock()

	t.DownloadAll()
	go c.watchDownload(ds)

	log.LogInfo("bt-client: AddTorrentBytes infohash=%s name=%q", ih, meta.Name)
	return meta, nil
}

// AddMagnetURI 从磁力链接 URI 启动下载。
func (c *BTClient) AddMagnetURI(uri string) (*TorrentMeta, error) {
	spec, err := torrent.TorrentSpecFromMagnetUri(uri)
	if err != nil {
		return nil, fmt.Errorf("invalid magnet URI: %w", err)
	}

	ih := spec.InfoHash.HexString()

	c.mu.Lock()
	if _, exists := c.downloads[ih]; exists {
		c.mu.Unlock()
		return nil, fmt.Errorf("torrent %s already added", ih)
	}
	c.mu.Unlock()

	t, _, err := c.cl.AddTorrentSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("add magnet: %w", err)
	}

	name := spec.DisplayName
	if name == "" {
		name = ih
	}

	ds := &downloadState{
		infoHashHex: ih,
		name:        name,
		t:           t,
		status:      "downloading",
		startTime:   time.Now(),
		doneCh:      make(chan struct{}),
	}

	c.mu.Lock()
	c.downloads[ih] = ds
	c.mu.Unlock()

	// For magnet links, download only after metadata is available.
	go func() {
		select {
		case <-t.GotInfo():
			t.DownloadAll()
		case <-time.After(5 * time.Minute):
			log.LogWarn("bt-client: magnet metadata timeout for %s", ih)
			return
		}
	}()

	go c.watchDownload(ds)

	log.LogInfo("bt-client: AddMagnetURI infohash=%s name=%q", ih, name)

	meta := &TorrentMeta{
		InfoHashHex: ih,
		Name:        name,
	}
	if len(spec.Trackers) > 0 {
		for _, tier := range spec.Trackers {
			meta.AnnounceList = append(meta.AnnounceList, tier...)
		}
	}

	return meta, nil
}

// AddTorrent 向后兼容方法——从 TorrentMeta 添加下载。
// 由于旧 TorrentMeta 不含原始 .torrent 字节，此方法仅重建磁力链接再添加。
// 新代码应使用 AddTorrentBytes。
func (c *BTClient) AddTorrent(meta *TorrentMeta) error {
	uri := fmt.Sprintf("magnet:?xt=urn:btih:%s", meta.InfoHashHex)
	if meta.Name != "" {
		uri += "&dn=" + meta.Name
	}
	for _, tr := range meta.AnnounceList {
		uri += "&tr=" + tr
	}
	_, err := c.AddMagnetURI(uri)
	return err
}

// AddMagnet 向后兼容方法——从 MagnetInfo 添加下载。
// 新代码应使用 AddMagnetURI。
func (c *BTClient) AddMagnet(magnet *MagnetInfo) error {
	uri := fmt.Sprintf("magnet:?xt=urn:btih:%s", magnet.InfoHash)
	if magnet.DisplayName != "" {
		uri += "&dn=" + magnet.DisplayName
	}
	for _, tr := range magnet.Trackers {
		uri += "&tr=" + tr
	}
	_, err := c.AddMagnetURI(uri)
	return err
}

// ---- 下载完成监控 ----

func (c *BTClient) watchDownload(ds *downloadState) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.RLock()
		_, exists := c.downloads[ds.infoHashHex]
		c.mu.RUnlock()
		if !exists {
			return
		}

		t := ds.t
		select {
		case <-t.Closed():
			return
		default:
		}

		length := t.Length()
		if length > 0 && t.BytesCompleted() >= length && ds.status == "downloading" {
			c.finalizeDownload(ds)
			return
		}
	}
}

func (c *BTClient) finalizeDownload(ds *downloadState) {
	ds.completed = true
	ds.status = "completed"

	t := ds.t
	dataRoot := filepath.Join(c.dataDir, t.Name())

	var completedFiles []CompletedFile
	for _, f := range t.Files() {
		var absPath string
		if info := t.Info(); info != nil && !info.IsDir() {
			// Single-file torrent: the file IS the torrent name.
			absPath = dataRoot
		} else {
			// Multi-file torrent: files are under the torrent directory.
			absPath = filepath.Join(dataRoot, f.Path())
		}

		fi, err := os.Stat(absPath)
		if err != nil {
			log.LogWarn("bt-client: stat completed file %q: %v", absPath, err)
			continue
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			log.LogWarn("bt-client: read completed file %q: %v", absPath, err)
			continue
		}

		hash := sha256.Sum256(data)
		completedFiles = append(completedFiles, CompletedFile{
			InfoHash: ds.infoHashHex,
			Path:     absPath,
			Size:     fi.Size(),
			SHA256:   hex.EncodeToString(hash[:]),
		})
	}

	ds.files = completedFiles

	if ds.doneCh != nil {
		close(ds.doneCh)
	}

	if c.onComplete != nil && len(completedFiles) > 0 {
		log.LogInfo("bt-client: firing onComplete for %s (%d files)", ds.infoHashHex, len(completedFiles))
		c.onComplete(ds.infoHashHex, completedFiles)
	}

	log.LogInfo("bt-client: download completed infohash=%s name=%q files=%d",
		ds.infoHashHex, ds.name, len(completedFiles))
}

// ---- 进度查询 ----

func (c *BTClient) GetDownload(infohash string) *DownloadStatus {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	return c.buildStatus(ds)
}

func (c *BTClient) ListDownloads() []DownloadStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]DownloadStatus, 0, len(c.downloads))
	for _, ds := range c.downloads {
		result = append(result, *c.buildStatus(ds))
	}
	return result
}

func (c *BTClient) buildStatus(ds *downloadState) *DownloadStatus {
	t := ds.t
	stats := t.Stats()

	totalSize := t.Length()
	piecesTotal := 0
	if info := t.Info(); info != nil {
		piecesTotal = info.NumPieces()
	}

	errStr := ""
	if ds.err != nil {
		errStr = ds.err.Error()
	}

	return &DownloadStatus{
		InfoHash:    ds.infoHashHex,
		Name:        ds.name,
		TotalSize:   totalSize,
		Downloaded:  t.BytesCompleted(),
		PiecesTotal: piecesTotal,
		PiecesDone:  stats.PiecesComplete,
		Peers:       stats.TotalPeers,
		Speed:       0,
		Status:      ds.status,
		Error:       errStr,
		Seeding:     ds.seeding,
	}
}

// ---- 暂停/恢复 ----

func (c *BTClient) PauseDownload(infohash string) error {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	if ds.status != "downloading" {
		return fmt.Errorf("download %s is not in progress (status=%s)", infohash, ds.status)
	}

	ds.t.DisallowDataDownload()
	ds.status = "paused"
	log.LogInfo("bt-client: paused download %s (%s)", infohash, ds.name)
	return nil
}

func (c *BTClient) ResumeDownload(infohash string) error {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	if ds.status != "paused" {
		return fmt.Errorf("download %s is not paused (status=%s)", infohash, ds.status)
	}

	ds.t.AllowDataDownload()
	ds.status = "downloading"
	log.LogInfo("bt-client: resumed download %s (%s)", infohash, ds.name)
	return nil
}

// ---- 删除 ----

func (c *BTClient) RemoveDownload(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}
	delete(c.downloads, infohash)
	c.mu.Unlock()

	ds.t.Drop()

	// 删除数据目录
	dataDir := filepath.Join(c.dataDir, ds.name)
	if dataDir != "" && dataDir != c.dataDir {
		if err := os.RemoveAll(dataDir); err != nil {
			log.LogWarn("bt-client: remove data dir %s: %v", dataDir, err)
		}
	}

	log.LogInfo("bt-client: removed download %s (%s)", infohash, ds.name)
	return nil
}

// ---- 做种 ----

func (c *BTClient) StartSeed(infohash string) error {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	if ds.status != "completed" {
		return fmt.Errorf("download %s has status %q, need 'completed' to seed", infohash, ds.status)
	}
	if ds.seeding {
		return fmt.Errorf("already seeding %s", infohash)
	}

	ds.t.AllowDataUpload()
	ds.seeding = true

	log.LogInfo("bt-client: started seeding %s (%s)", infohash, ds.name)
	return nil
}

func (c *BTClient) StopSeed(infohash string) error {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("download %s not found", infohash)
	}

	if !ds.seeding {
		return fmt.Errorf("not currently seeding %s", infohash)
	}

	ds.t.DisallowDataUpload()
	ds.seeding = false

	log.LogInfo("bt-client: stopped seeding %s", infohash)
	return nil
}

func (c *BTClient) IsSeeding(infohash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ds, ok := c.downloads[infohash]
	return ok && ds.seeding
}

func (c *BTClient) ListSeeders() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	keys := make([]string, 0)
	for k, ds := range c.downloads {
		if ds.seeding {
			keys = append(keys, k)
		}
	}
	return keys
}

// ---- Peer 手动管理 ----

func (c *BTClient) AddPeer(infohash, addr string) {
	c.mu.Lock()
	c.customPeers[infohash] = append(c.customPeers[infohash], addr)
	c.mu.Unlock()

	log.LogInfo("bt-client: AddPeer infohash=%s addr=%s", infohash, addr)
}

func (c *BTClient) GetCustomPeers(infohash string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := c.customPeers[infohash]
	result := make([]string, len(peers))
	copy(result, peers)
	return result
}

// ---- 全局统计 ----

func (c *BTClient) GetGlobalStats() *GlobalStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := &GlobalStats{}
	if globalDHT != nil {
		stats.DHTNodes = globalDHT.NumNodes()
	}

	for _, ds := range c.downloads {
		switch ds.status {
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
		stats.TotalDown += ds.t.BytesCompleted()
	}

	return stats
}

// ---- 生命周期 ----

func (c *BTClient) Close() {
	log.LogInfo("bt-client: closing")
	c.cl.Close()
}

// ---- 文件访问 ----

func (c *BTClient) ListDownloadFiles(infohash string) ([]CompletedFile, bool) {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return ds.files, ds.status == "completed"
}

func (c *BTClient) ReadFileReader(filePath string) (io.ReadCloser, error) {
	return os.Open(filePath)
}

func (c *BTClient) GetDownloadDir() string {
	return c.dataDir
}

// ---- 辅助函数 ----

// torrentMetaFromLibrary 从库类型提取 TorrentMeta（用于 HTTP API 响应）。
func torrentMetaFromLibrary(mi *metainfo.MetaInfo, t *torrent.Torrent) *TorrentMeta {
	info := t.Info()
	if info == nil {
		meta := &TorrentMeta{
			InfoHashHex: mi.HashInfoBytes().HexString(),
		}
		// 尝试解析 Info 获取名称
		if parsed, err := mi.UnmarshalInfo(); err == nil {
			meta.Name = parsed.BestName()
		}
		return meta
	}

	meta := &TorrentMeta{
		Name:        info.Name,
		PieceLength: info.PieceLength,
		TotalSize:   info.TotalLength(),
		InfoHashHex: mi.HashInfoBytes().HexString(),
		InfoHash:    mi.HashInfoBytes().Bytes(),
	}

	if len(info.Files) == 0 {
		meta.IsSingleFile = true
		meta.Files = []TorrentFile{{Path: info.Name, Size: info.Length}}
	} else {
		for _, f := range info.Files {
			path := strings.Join(f.Path, "/")
			meta.Files = append(meta.Files, TorrentFile{Path: path, Size: f.Length})
		}
	}

	// 提取 Announce URL
	for _, tier := range mi.UpvertedAnnounceList() {
		meta.AnnounceList = append(meta.AnnounceList, tier...)
	}

	// 提取 piece 哈希（每 20 字节一个 SHA1）
	numPieces := len(info.Pieces) / 20
	meta.Pieces = make([][]byte, numPieces)
	meta.PiecesHex = make([]string, numPieces)
	for i := 0; i < numPieces; i++ {
		hash := info.Pieces[i*20 : i*20+20]
		meta.Pieces[i] = hash
		meta.PiecesHex[i] = hex.EncodeToString(hash)
	}

	return meta
}

// metaFromTorrent 从已完成的 Torrent 对象提取 TorrentMeta。
func metaFromTorrent(t *torrent.Torrent) *TorrentMeta {
	info := t.Info()
	if info == nil {
		return nil
	}

	meta := &TorrentMeta{
		Name:        info.Name,
		PieceLength: info.PieceLength,
		TotalSize:   info.TotalLength(),
		InfoHashHex: t.InfoHash().HexString(),
		InfoHash:    t.InfoHash().Bytes(),
	}

	if len(info.Files) == 0 {
		meta.IsSingleFile = true
		meta.Files = []TorrentFile{{Path: info.Name, Size: info.Length}}
	} else {
		for _, f := range info.Files {
			path := strings.Join(f.Path, "/")
			meta.Files = append(meta.Files, TorrentFile{Path: path, Size: f.Length})
		}
	}

	numPieces := len(info.Pieces) / 20
	meta.Pieces = make([][]byte, numPieces)
	meta.PiecesHex = make([]string, numPieces)
	for i := 0; i < numPieces; i++ {
		hash := info.Pieces[i*20 : i*20+20]
		meta.Pieces[i] = hash
		meta.PiecesHex[i] = hex.EncodeToString(hash)
	}

	return meta
}
