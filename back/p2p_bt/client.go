// BTClient wraps the anacrolix/torrent library, managing BitTorrent download tasks.
// DHT peer discovery, wire protocol, tracker communication, and seeding are all handled internally by the library.
package p2p_bt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	cl             *torrent.Client
	dataDir        string
	onComplete     OnTorrentComplete
	mu             sync.RWMutex
	downloads      map[string]*downloadState
	customPeers    map[string][]string
	torrentData    map[string][]byte // infohash -> raw .torrent bytes
	autoSeed       map[string]bool   // infohashes to auto-start seeding on completion
	publicTrackers []string          // fallback public trackers (empty = no injection)
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
	// Seeding enabled: the client will upload to peers after download completion.
	// Previously NoUpload=true + Seed=false blocked all upload, making StartSeed a no-op
	// because t.seeding() checks cl.config.NoUpload/Seed in addition to dataUploadDisallowed.
	// Per-torrent upload is still controllable via StopSeed (DisallowDataUpload).
	cfg.Seed = true
	cfg.NoUpload = false
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

	// Public tracker fallback: when a torrent has no announce list, inject
	// these public trackers so the client can still find peers.
	//
	// The list is assembled by buildPublicTrackers (tracker_list.go):
	//   - Base: PEERDRIVE_BT_PUBLIC_TRACKERS (env) if set, else curated ~50 subset
	//   - Plus: dynamic fetch from newtrackon/ngosang (if enabled, default on)
	//   - Deduplicated (trailing-slash aware)
	//
	// Dynamic fetch failure is non-fatal: falls back to the base list.
	client.publicTrackers = buildPublicTrackers(LoadTrackerConfig())
	LogInfo("bt-client: public trackers: %d", len(client.publicTrackers))

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

	// Inject public trackers if the torrent has no announce list.
	// This ensures the client can find peers even for torrents with zero
	// trackers (common in private or older torrents).
	c.injectPublicTrackers(mi)

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

	// Inject public trackers into the magnet spec if no trackers are present.
	c.injectPublicTrackersToSpec(spec)

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

// injectPublicTrackers modifies the MetaInfo's announce list in-place,
// adding public trackers if the torrent has no existing announce list.
// If the torrent already has trackers, no change is made — this avoids
// duplicating endpoints and respects private tracker configurations.
func (c *BTClient) injectPublicTrackers(mi *metainfo.MetaInfo) {
	if len(c.publicTrackers) == 0 {
		return
	}
	// Convert existing announce list to flat []string.
	var existing []string
	if mi.AnnounceList.OverridesAnnounce(mi.Announce) {
		for _, tier := range mi.AnnounceList {
			existing = append(existing, tier...)
		}
	} else if mi.Announce != "" {
		existing = []string{mi.Announce}
	}
	merged := mergePublicTrackers(existing, c.publicTrackers)
	if len(merged) == len(existing) {
		return // no change
	}
	// Put the merged list into a single tier (tier order doesn't matter
	// for anacrolix's concurrent tracker contact).
	mi.AnnounceList = metainfo.AnnounceList{merged}
	mi.Announce = "" // AnnounceList takes precedence when non-empty.
}

// injectPublicTrackersToSpec modifies a TorrentSpec's tracker list in-place,
// adding public trackers if the magnet URI has no existing trackers.
func (c *BTClient) injectPublicTrackersToSpec(spec *torrent.TorrentSpec) {
	if len(c.publicTrackers) == 0 {
		return
	}
	var existing []string
	for _, tier := range spec.Trackers {
		existing = append(existing, tier...)
	}
	merged := mergePublicTrackers(existing, c.publicTrackers)
	if len(merged) == len(existing) {
		return // no change
	}
	spec.Trackers = [][]string{merged}
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
		curStatus := ds.status // read under lock to avoid race with Pause/Resume
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
		if length > 0 && t.BytesCompleted() >= length && curStatus == "downloading" {
			c.finalizeDownload(ds)
			return
		}
	}
}

func (c *BTClient) finalizeDownload(ds *downloadState) {
	t := ds.t
	dataRoot := filepath.Join(c.dataDir, t.Name())

	// I/O work: read files and compute hashes (no lock needed).
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

	// State update under write lock to avoid races with Pause/Resume/GetDownload.
	c.mu.Lock()
	ds.completed = true
	ds.status = "completed"
	ds.files = completedFiles

	if ds.doneCh != nil {
		close(ds.doneCh)
	}

	shouldAutoSeed := false
	if c.autoSeed[ds.infoHashHex] {
		shouldAutoSeed = true
	}
	c.mu.Unlock()

	if shouldAutoSeed {
		c.mu.Lock()
		LogInfo("bt-client: auto-seeding %s", ds.infoHashHex)
		ds.t.AllowDataUpload()
		ds.seeding = true
		ds.status = "seeding"
		c.mu.Unlock()
	}

	if c.onComplete != nil && len(completedFiles) > 0 {
		LogInfo("bt-client: firing onComplete for %s (%d files)", ds.infoHashHex, len(completedFiles))
		c.onComplete(ds.infoHashHex, completedFiles)
	}

	LogInfo("bt-client: download completed infohash=%s name=%q files=%d",
		ds.infoHashHex, ds.name, len(completedFiles))
}

