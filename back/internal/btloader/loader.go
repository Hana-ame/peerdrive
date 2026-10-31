package btloader

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// completionPollInterval is how often the host drains the runtime's
// download-complete queue.
//
// Why polling instead of a callback: the runtime lives behind a C ABI, and
// handing a Go func value across that boundary means a C function pointer called
// back from the DLL's runtime into the host's — the one shape that reliably
// breaks when two Go runtimes share a process. A bounded queue plus a drain op
// is boring and cannot deadlock. 2s is invisible next to a torrent finishing.
const completionPollInterval = 2 * time.Second

// Config configures the runtime that Open starts.
type Config struct {
	// DataDir is the torrent data directory (PEERDRIVE_DOWNLOAD_DIR).
	DataDir string

	// ListenAddr pins the BT peer listen port (":0" = random, "" = default).
	ListenAddr string

	// StorageDir is the peerdrive storage root; required only for DHT bridging.
	StorageDir string

	// DHTEnabled starts the mainline DHT server. Off by default.
	DHTEnabled bool

	// DHTListenAddr is the DHT UDP listen address (":6881").
	DHTListenAddr string
}

// wireConfig is what the runtime parses (btservice.Config).
type wireConfig struct {
	DataDir       string `json:"data_dir"`
	ListenAddr    string `json:"listen_addr,omitempty"`
	StorageDir    string `json:"storage_dir,omitempty"`
	DHTEnabled    bool   `json:"dht_enabled"`
	DHTListenAddr string `json:"dht_listen_addr,omitempty"`
}

// Client is a handle to the BT runtime. All methods are safe for concurrent use
// and return ErrUnavailable-derived errors when the runtime is not loaded.
type Client struct {
	tr      transport
	status  Status
	dataDir string

	mu         sync.Mutex
	onComplete func(infohash string, files []CompletedFile)

	stopCh    chan struct{}
	pumpOnce  sync.Once
	closeOnce sync.Once
}

// Open loads the runtime and performs a handshake. It blocks while the runtime
// starts (torrent client construction fetches the public tracker list; DHT
// bootstraps and sleeps), so callers should open it lazily, never on a request
// path.
func Open(cfg Config) (*Client, error) {
	tr, err := openTransport(wireConfig{
		DataDir:       cfg.DataDir,
		ListenAddr:    cfg.ListenAddr,
		StorageDir:    cfg.StorageDir,
		DHTEnabled:    cfg.DHTEnabled,
		DHTListenAddr: cfg.DHTListenAddr,
	})
	if err != nil {
		return nil, err
	}

	c := &Client{tr: tr, stopCh: make(chan struct{})}
	var st Status
	if err := c.decode("ping", nil, &st); err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("bt runtime handshake failed: %w", err)
	}
	c.status = st
	c.dataDir = st.DataDir

	desc := "bt runtime"
	if d, ok := tr.(interface{ Describe() string }); ok {
		desc = d.Describe()
	}
	log.LogInfo("btloader: %s ready (data_dir=%s dht=%v)", desc, st.DataDir, st.DHTAvailable)
	if st.DHTError != "" {
		log.LogWarn("btloader: DHT unavailable: %s", st.DHTError)
	}
	return c, nil
}

// Status reports what the runtime said about itself at Open time.
func (c *Client) Status() Status { return c.status }

// Close unloads the runtime. Safe to call more than once.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.stopCh)
		if c.tr != nil {
			_ = c.tr.Close()
		}
	})
}

// OnComplete registers the download-complete handler. Passing nil disables it.
// Completions that arrived before registration are delivered on the next poll.
func (c *Client) OnComplete(fn func(infohash string, files []CompletedFile)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onComplete = fn
	c.mu.Unlock()
	if fn == nil {
		return
	}
	c.pumpOnce.Do(func() { go c.pumpCompletions() })
}

// ---- torrent downloads ----

// AddTorrentBytes adds a .torrent payload and starts downloading.
func (c *Client) AddTorrentBytes(data []byte) (*TorrentMeta, error) {
	var meta TorrentMeta
	if err := c.decode("add_torrent", map[string]string{"data_b64": b64enc(data)}, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// AddMagnetURI resolves a magnet URI and starts downloading.
func (c *Client) AddMagnetURI(uri string) (*TorrentMeta, error) {
	var meta TorrentMeta
	if err := c.decode("add_magnet", map[string]string{"uri": uri}, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// GetDownload returns one download's status, or nil when unknown.
func (c *Client) GetDownload(infohash string) *DownloadStatus {
	var status *DownloadStatus
	if err := c.decode("get_download", map[string]string{"infohash": infohash}, &status); err != nil {
		log.LogWarn("btloader: get_download %s: %v", infohash, err)
		return nil
	}
	return status
}

// ListDownloads returns every known download.
func (c *Client) ListDownloads() []DownloadStatus {
	var out []DownloadStatus
	if err := c.decode("list_downloads", nil, &out); err != nil {
		log.LogWarn("btloader: list_downloads: %v", err)
		return nil
	}
	return out
}

// PauseDownload pauses a download.
func (c *Client) PauseDownload(infohash string) error {
	return c.call1("pause", map[string]string{"infohash": infohash})
}

// ResumeDownload resumes a paused download.
func (c *Client) ResumeDownload(infohash string) error {
	return c.call1("resume", map[string]string{"infohash": infohash})
}

// RemoveDownload removes a download and its files.
func (c *Client) RemoveDownload(infohash string) error {
	return c.call1("remove", map[string]string{"infohash": infohash})
}

// StartSeed starts seeding a completed download.
func (c *Client) StartSeed(infohash string) error {
	return c.call1("start_seed", map[string]string{"infohash": infohash})
}

// StopSeed stops seeding a download.
func (c *Client) StopSeed(infohash string) error {
	return c.call1("stop_seed", map[string]string{"infohash": infohash})
}

// SetAutoSeed marks a download to seed as soon as it completes.
func (c *Client) SetAutoSeed(infohash string) {
	if err := c.call1("set_auto_seed", map[string]string{"infohash": infohash}); err != nil {
		log.LogWarn("btloader: set_auto_seed %s: %v", infohash, err)
	}
}

// ListSeeders returns the infohashes currently seeding.
func (c *Client) ListSeeders() []string {
	var out []string
	if err := c.decode("list_seeders", nil, &out); err != nil {
		log.LogWarn("btloader: list_seeders: %v", err)
		return nil
	}
	return out
}

// GetGlobalStats returns runtime-wide counters.
func (c *Client) GetGlobalStats() GlobalStats {
	var stats GlobalStats
	if err := c.decode("global_stats", nil, &stats); err != nil {
		log.LogWarn("btloader: global_stats: %v", err)
	}
	return stats
}

// GetTorrentBytes returns the raw .torrent for a known download.
func (c *Client) GetTorrentBytes(infohash string) ([]byte, error) {
	var out struct {
		DataB64 string `json:"data_b64"`
	}
	if err := c.decode("torrent_bytes", map[string]string{"infohash": infohash}, &out); err != nil {
		return nil, err
	}
	return b64dec(out.DataB64)
}

// GetMagnetURI returns the magnet link for a known download ("" if unknown).
func (c *Client) GetMagnetURI(infohash string) string {
	var out struct {
		URI string `json:"uri"`
	}
	if err := c.decode("magnet_uri", map[string]string{"infohash": infohash}, &out); err != nil {
		log.LogWarn("btloader: magnet_uri %s: %v", infohash, err)
		return ""
	}
	return out.URI
}

// GetDownloadDir returns the runtime's torrent data directory.
func (c *Client) GetDownloadDir() string {
	if c == nil {
		return ""
	}
	return c.dataDir
}

// CreateAndSeed builds a torrent from a local path, copies the data into the
// runtime's data dir, and starts seeding. The metainfo never crosses the
// boundary: building it requires torrent's metainfo package, which lives on the
// runtime side.
func (c *Client) CreateAndSeed(opts CreateAndSeedOptions) (*TorrentMeta, error) {
	var meta TorrentMeta
	err := c.decode("create_and_seed", map[string]any{
		"path":         opts.Path,
		"name":         opts.Name,
		"piece_length": opts.PieceLength,
		"announce":     opts.Announce,
	}, &meta)
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

// ---- mainline DHT ----

// DHTStatus reports the DHT server state.
func (c *Client) DHTStatus() DHTStatus {
	var st DHTStatus
	if err := c.decode("dht_status", nil, &st); err != nil {
		return DHTStatus{Error: err.Error()}
	}
	return st
}

// DHTAnnounce publishes a hash's provider record on the DHT.
func (c *Client) DHTAnnounce(hash string) error {
	return c.call1("dht_announce", map[string]string{"hash": hash})
}

// DHTFindProviders looks up providers for a hash.
func (c *Client) DHTFindProviders(hash string) ([]string, error) {
	var peers []string
	if err := c.decode("dht_find_providers", map[string]string{"hash": hash}, &peers); err != nil {
		return nil, err
	}
	return peers, nil
}

// BEP44Put stores immutable data on the DHT and returns the 40-char hex target.
func (c *Client) BEP44Put(data []byte) (string, error) {
	var out struct {
		Target string `json:"target"`
	}
	if err := c.decode("bep44_put", map[string]string{"data_b64": b64enc(data)}, &out); err != nil {
		return "", err
	}
	return out.Target, nil
}

// BEP44Get fetches immutable data by 40-char hex target.
func (c *Client) BEP44Get(targetHex string) ([]byte, error) {
	var out struct {
		DataB64 string `json:"data_b64"`
	}
	if err := c.decode("bep44_get", map[string]string{"target": targetHex}, &out); err != nil {
		return nil, err
	}
	return b64dec(out.DataB64)
}

// BEP51Sample samples infohashes from the DHT.
func (c *Client) BEP51Sample(max int) ([]string, error) {
	var out []string
	if err := c.decode("bep51_sample", map[string]int{"max": max}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- DHT HTTP bridge ----

// ShareFile announces a stored file's hash on the DHT.
func (c *Client) ShareFile(hash string) error {
	return c.call1("bridge_share", map[string]string{"hash": hash})
}

// ListShared returns the hashes this node announced.
func (c *Client) ListShared() ([]string, error) {
	var out []string
	if err := c.decode("bridge_list_shared", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// BridgeFilePath maps a hash to its storage path ("" when the hash is invalid).
func (c *Client) BridgeFilePath(hash string) string {
	var out struct {
		Path string `json:"path"`
	}
	if err := c.decode("bridge_path", map[string]string{"hash": hash}, &out); err != nil {
		return ""
	}
	return out.Path
}

// FetchFile pulls a file from DHT peers into storage and returns its bytes.
//
// The runtime writes the verified file (SHA-256 checked on its side) and only
// the path crosses the boundary; reading it back here keeps the existing
// ProtocolFetcher contract ([]byte) without shipping up to 512 MiB of base64
// through JSON in both directions.
func (c *Client) FetchFile(hash string) ([]byte, error) {
	var out struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := c.decode("bridge_fetch", map[string]string{"hash": hash}, &out); err != nil {
		return nil, err
	}
	if out.Path == "" {
		return nil, fmt.Errorf("bridge fetch of %s returned no path", hash)
	}
	return readFileCapped(out.Path, out.Size)
}

// ---- plumbing ----

// call1 executes an op without caring about its payload.
func (c *Client) call1(op string, args any) error {
	_, err := c.call(op, args)
	return err
}

// decode executes an op and unmarshals its data into dst.
func (c *Client) decode(op string, args any, dst any) error {
	raw, err := c.call(op, args)
	if err != nil {
		return err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return fmt.Errorf("%s: decode reply: %w", op, err)
	}
	return nil
}

func (c *Client) call(op string, args any) (json.RawMessage, error) {
	if c == nil || c.tr == nil {
		return nil, ErrUnavailable
	}
	return c.tr.Call(op, args)
}

func (c *Client) pumpCompletions() {
	ticker := time.NewTicker(completionPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			var events []Completion
			if err := c.decode("drain_completions", nil, &events); err != nil {
				// The runtime is gone (DLL unloaded / process shutting down):
				// stop polling instead of logging once per tick forever.
				if len(events) == 0 && isUnavailable(err) {
					return
				}
				log.LogWarn("btloader: drain_completions: %v", err)
				continue
			}
			for _, ev := range events {
				c.mu.Lock()
				fn := c.onComplete
				c.mu.Unlock()
				if fn != nil {
					fn(ev.InfoHash, ev.Files)
				}
			}
		}
	}
}
