// BTClient wraps the anacrolix/torrent library, managing BitTorrent download tasks.
// DHT peer discovery, wire protocol, tracker communication, and seeding are all handled internally by the library.
package p2p_bt

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
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

// NewBTClientWithListenAddr creates a BT client bound to listenAddr
// (":0" = random port, "" = library default).
//
// Exported for btservice: when the runtime runs inside the DLL, the host must be
// able to pin the peer port (firewall rules, multiple instances) instead of
// inheriting the library default.
func NewBTClientWithListenAddr(dataDir, listenAddr string) *BTClient {
	return newBTClient(dataDir, listenAddr)
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

// ---- Lifecycle ----

func (c *BTClient) Close() {
	LogInfo("bt-client: closing")
	c.cl.Close()
}
