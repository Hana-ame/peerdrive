// BTClient wraps the anacrolix/torrent library, managing BitTorrent download tasks.
// DHT peer discovery, wire protocol, tracker communication, and seeding are all handled internally by the library.
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

)

// globalDHT is retained for BEP 44/51 and GetGlobalStats().DHTNodes usage.
var globalDHT *BTDHTService

// SetGlobalDHT sets the global DHT service reference.
func SetGlobalDHT(dht *BTDHTService) {
	globalDHT = dht
}

// ---- Internal state ----

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

// BTClient manages BitTorrent download tasks.
type BTClient struct {
	cl          *torrent.Client
	dataDir     string
	onComplete  OnTorrentComplete
	mu          sync.RWMutex
	downloads   map[string]*downloadState
	customPeers map[string][]string
	torrentData map[string][]byte // infohash -> raw .torrent bytes
	autoSeed    map[string]bool   // infohashes to auto-start seeding on completion
}

// NewBTClient creates a BT client instance (default listen port).
func NewBTClient(dataDir string) *BTClient {
	return newBTClient(dataDir, "")
}

// newBTClient creates a BT client instance with an optional listen address (":0" = random port).
func newBTClient(dataDir string, listenAddr string) *BTClient {
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "peerdrive-bt")
	}
	os.MkdirAll(dataDir, 0755)

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	if listenAddr != "" {
		cfg.SetListenAddr(listenAddr)
	}
	cfg.Seed = false
	cfg.NoUpload = true
	cfg.DisableUTP = true

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		LogError("bt-client: failed to create torrent client: %v", err)
		return nil
	}

	client := &BTClient{
		cl:          cl,
		dataDir:     dataDir,
		downloads:   make(map[string]*downloadState),
		customPeers: make(map[string][]string),
		torrentData: make(map[string][]byte),
		autoSeed:    make(map[string]bool),
	}
	LogInfo("bt-client: created, dataDir=%s", dataDir)
	return client
}

// SetOnComplete registers a download-complete callback.
func (c *BTClient) SetOnComplete(fn OnTorrentComplete) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onComplete = fn
}

// ---- Adding downloads ----

// AddTorrentBytes starts a download from raw .torrent file bytes, returning the parsed metadata.
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

	// Wait for metadata to be available (.torrent files are typically available immediately)
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
	c.torrentData[ih] = data
	c.mu.Unlock()

	t.DownloadAll()
	go c.watchDownload(ds)

	LogInfo("bt-client: AddTorrentBytes infohash=%s name=%q", ih, meta.Name)
	return meta, nil
}

// AddMagnetURI starts a download from a magnet link URI.
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
			LogWarn("bt-client: magnet metadata timeout for %s", ih)
			return
		}
	}()

	go c.watchDownload(ds)

	LogInfo("bt-client: AddMagnetURI infohash=%s name=%q", ih, name)

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

// AddTorrent is a backward-compatible method — adds a download from a TorrentMeta.
// Since the old TorrentMeta does not contain the raw .torrent bytes, this method only reconstructs a magnet link and adds it.
// New code should use AddTorrentBytes.
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

// AddMagnet is a backward-compatible method — adds a download from a MagnetInfo.
// New code should use AddMagnetURI.
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

// ---- Download completion monitoring ----

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
			LogWarn("bt-client: stat completed file %q: %v", absPath, err)
			continue
		}

		data, err := os.ReadFile(absPath)
		if err != nil {
			LogWarn("bt-client: read completed file %q: %v", absPath, err)
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

		// Check the auto-seed flag
		c.mu.RLock()
		shouldAutoSeed := c.autoSeed[ds.infoHashHex]
		c.mu.RUnlock()
		if shouldAutoSeed {
			LogInfo("bt-client: auto-seeding %s", ds.infoHashHex)
			ds.t.AllowDataUpload()
			ds.seeding = true
			ds.status = "seeding"
		}
		LogInfo("bt-client: firing onComplete for %s (%d files)", ds.infoHashHex, len(completedFiles))
		c.onComplete(ds.infoHashHex, completedFiles)
	}

	LogInfo("bt-client: download completed infohash=%s name=%q files=%d",
		ds.infoHashHex, ds.name, len(completedFiles))
}

// ---- Progress queries ----

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

// ---- Pause/Resume ----

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
	LogInfo("bt-client: paused download %s (%s)", infohash, ds.name)
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
	LogInfo("bt-client: resumed download %s (%s)", infohash, ds.name)
	return nil
}

// ---- Removal ----

func (c *BTClient) RemoveDownload(infohash string) error {
	c.mu.Lock()
	ds, ok := c.downloads[infohash]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("download %s not found", infohash)
	}
	delete(c.downloads, infohash)
	delete(c.torrentData, infohash)
	delete(c.autoSeed, infohash)
	c.mu.Unlock()

	ds.t.Drop()

	// Remove the data directory
	dataDir := filepath.Join(c.dataDir, ds.name)
	if dataDir != "" && dataDir != c.dataDir {
		if err := os.RemoveAll(dataDir); err != nil {
			LogWarn("bt-client: remove data dir %s: %v", dataDir, err)
		}
	}

	LogInfo("bt-client: removed download %s (%s)", infohash, ds.name)
	return nil
}

// ---- Torrent data and Magnet ----

// GetTorrentBytes returns the raw .torrent file bytes. For downloads added from a magnet link, exports from the library if metadata is available.
func (c *BTClient) GetTorrentBytes(infohash string) ([]byte, error) {
	c.mu.RLock()
	data, ok := c.torrentData[infohash]
	c.mu.RUnlock()
	if ok {
		return data, nil
	}

	c.mu.RLock()
	ds, dsOk := c.downloads[infohash]
	c.mu.RUnlock()
	if !dsOk {
		return nil, fmt.Errorf("download not found: %s", infohash)
	}

	mi := ds.t.Metainfo()
	if len(mi.InfoBytes) == 0 {
		return nil, fmt.Errorf("metadata not yet available for %s", infohash)
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		return nil, fmt.Errorf("failed to encode torrent: %w", err)
	}
	return buf.Bytes(), nil
}

// GetMagnetURI generates a magnet link URI for the specified download.
func (c *BTClient) GetMagnetURI(infohash string) string {
	c.mu.RLock()
	ds, ok := c.downloads[infohash]
	c.mu.RUnlock()
	if !ok {
		return ""
	}

	uri := fmt.Sprintf("magnet:?xt=urn:btih:%s", ds.infoHashHex)
	if ds.name != "" && ds.name != ds.infoHashHex {
		uri += "&dn=" + ds.name
	}
	mi := ds.t.Metainfo()
	if len(mi.InfoBytes) > 0 {
		for _, tier := range mi.UpvertedAnnounceList() {
			for _, tr := range tier {
				uri += "&tr=" + tr
			}
		}
	}
	return uri
}

// SetAutoSeed marks the specified infohash to auto-start seeding after download completion.
func (c *BTClient) SetAutoSeed(infohash string) {
	c.mu.Lock()
	c.autoSeed[infohash] = true
	c.mu.Unlock()
}

// HasTorrentBytes checks whether torrent bytes are stored for the specified infohash.
func (c *BTClient) HasTorrentBytes(infohash string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.torrentData[infohash]
	return ok
}

// ---- Seeding ----

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

	LogInfo("bt-client: started seeding %s (%s)", infohash, ds.name)
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

	LogInfo("bt-client: stopped seeding %s", infohash)
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

// ---- Peer manual management ----

func (c *BTClient) AddPeer(infohash, addr string) {
	c.mu.Lock()
	c.customPeers[infohash] = append(c.customPeers[infohash], addr)
	c.mu.Unlock()

	LogInfo("bt-client: AddPeer infohash=%s addr=%s", infohash, addr)
}

func (c *BTClient) GetCustomPeers(infohash string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	peers := c.customPeers[infohash]
	result := make([]string, len(peers))
	copy(result, peers)
	return result
}

// ---- Global statistics ----

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

// ---- Lifecycle ----

func (c *BTClient) Close() {
	LogInfo("bt-client: closing")
	c.cl.Close()
}

// ---- File access ----

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

// ---- Helper functions ----

// torrentMetaFromLibrary extracts TorrentMeta from library types (used for HTTP API responses).
func torrentMetaFromLibrary(mi *metainfo.MetaInfo, t *torrent.Torrent) *TorrentMeta {
	info := t.Info()
	if info == nil {
		meta := &TorrentMeta{
			InfoHashHex: mi.HashInfoBytes().HexString(),
		}
		// Try to parse Info to get the name
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

	// Extract Announce URLs
	for _, tier := range mi.UpvertedAnnounceList() {
		meta.AnnounceList = append(meta.AnnounceList, tier...)
	}

	// Extract piece hashes (one SHA1 per 20 bytes)
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

// metaFromTorrent extracts TorrentMeta from a completed Torrent object.
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
