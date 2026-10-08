package twimg

// module.go: the public entry point for the optional ech-proxy module.
//
// Wiring (see internal/serverapp/app.go): when PEERDRIVE_ECH_PROXY_ENABLE=true the
// server calls New(...) then Start(ctx) once, hands Client() to source.NewURLSource,
// and registers Stop(ctx) as a shutdown hook. When the flag is false none of this code
// runs, so the default build and runtime behaviour are byte-for-byte unchanged.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// Config is the module's settings. Zero-value fields fall back to the documented
// defaults in defaultConfig.
type Config struct {
	// Entry describes the pbs.twimg.com → twimg-pbs.l.moonchan.xyz:8443 rewrite.
	// A zero Entry selects the full documented default (including SkipTLS=true).
	Entry Entry

	// Release channel.
	Repo           string // default DefaultRepo
	Version        string // default DefaultVersion
	Asset          string // default derived from runtime.GOOS/GOARCH
	ExpectedSHA256 string

	// InstallDir holds the downloaded binary and its .sha256 sidecar. Required.
	InstallDir string

	// IPMode is passed to ech-proxy as -ip-mode. Default DefaultIPMode ("v4").
	IPMode string

	// Start retry policy.
	Attempts     int           // default 3
	Backoff      time.Duration // default 1s (linear)
	ReadyTimeout time.Duration // default 5s per attempt
	// StartTimeout bounds the whole Start call (download + retries). Default 60s.
	StartTimeout time.Duration

	// HTTPBase is the base client whose Timeout/CheckRedirect are inherited for
	// non-rewritten traffic. nil → a fresh client (no timeout).
	HTTPBase *http.Client
}

// Module owns one ech-proxy subprocess and the http.Client that routes through it.
//
// Concurrency contract: Start and Stop are serialized by startMu, so a Stop that races an
// in-flight Start waits for it instead of tearing down a half-built client. Read-only
// accessors (Client/Alive/Entry/Pid/BinaryPath/Addr) may run concurrently with both.
type Module struct {
	cfg  Config
	dl   *Downloader
	proc *Process

	mu      sync.Mutex // guards client and started
	startMu sync.Mutex // serializes Start/Stop
	client  *http.Client
	started bool
}

func defaultConfig(cfg Config) Config {
	zeroEntry := cfg.Entry == (Entry{})
	if cfg.Entry.SrcHost == "" {
		cfg.Entry.SrcHost = DefaultSrcHost
	}
	if cfg.Entry.EntryHost == "" {
		cfg.Entry.EntryHost = DefaultEntryHost
	}
	if cfg.Entry.Port == "" {
		cfg.Entry.Port = DefaultPort
	}
	if cfg.Entry.ListenAddr == "" {
		cfg.Entry.ListenAddr = DefaultListenAddr
	}
	if zeroEntry {
		// A caller who left Entry empty gets the documented default, which is
		// SkipTLS=true (local self-signed cert). A caller who set SrcHost
		// explicitly is responsible for SkipTLS.
		cfg.Entry.SkipTLS = true
	}
	if cfg.Repo == "" {
		cfg.Repo = DefaultRepo
	}
	if cfg.Version == "" {
		cfg.Version = DefaultVersion
	}
	if cfg.IPMode == "" {
		cfg.IPMode = DefaultIPMode
	}
	if cfg.Attempts < 1 {
		cfg.Attempts = 3
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = time.Second
	}
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 5 * time.Second
	}
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = 60 * time.Second
	}
	return cfg
}

// New validates cfg, fills defaults and builds the downloader and process manager.
// Nothing is downloaded and nothing is spawned yet — call Start for that.
func New(cfg Config) (*Module, error) {
	cfg = defaultConfig(cfg)
	if err := cfg.Entry.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.InstallDir) == "" {
		return nil, errors.New("echproxy: Config.InstallDir is required")
	}
	if strings.TrimSpace(cfg.Asset) == "" {
		asset, err := DefaultAssetForCurrent()
		if err != nil {
			return nil, err
		}
		cfg.Asset = asset
	}

	dl := &Downloader{
		Repo:           cfg.Repo,
		Version:        cfg.Version,
		Asset:          cfg.Asset,
		InstallDir:     cfg.InstallDir,
		ExpectedSHA256: cfg.ExpectedSHA256,
		Client:         &http.Client{Timeout: 5 * time.Minute},
	}
	proc := &Process{
		Addr:         cfg.Entry.ListenAddr,
		IPMode:       cfg.IPMode,
		Attempts:     cfg.Attempts,
		Backoff:      cfg.Backoff,
		ReadyTimeout: cfg.ReadyTimeout,
		Logf:         func(format string, args ...interface{}) { log.LogInfo(format, args...) },
		Runner:       execRunner{},
	}
	return &Module{cfg: cfg, dl: dl, proc: proc}, nil
}

// Start downloads the binary if needed, spawns the subprocess and returns when it is
// listening on Entry.ListenAddr. After this call Client() is non-nil.
//
// Every failure is an explicit error — there is no silent fallback to direct pbs.twimg.com
// traffic, because a half-working configuration is worse than a loud startup failure.
func (m *Module) Start(ctx context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	log.LogInfo("echproxy: ensuring %s %s", m.cfg.Repo, m.cfg.Version)
	bin, err := m.dl.Ensure(ctx)
	if err != nil {
		return fmt.Errorf("echproxy: ensure binary: %w", err)
	}
	m.proc.BinPath = bin
	// Run from the install dir so any file the proxy writes lands in our managed
	// directory instead of the node's working directory.
	m.proc.WorkDir = filepath.Dir(bin)

	cctx, cancel := context.WithTimeout(ctx, m.cfg.StartTimeout)
	defer cancel()
	if err := m.proc.Start(cctx); err != nil {
		return fmt.Errorf("echproxy: start subprocess: %w", err)
	}

	client, err := NewClient(m.cfg.Entry, m.cfg.HTTPBase)
	if err != nil {
		_ = m.proc.Stop(context.Background())
		return fmt.Errorf("echproxy: build client: %w", err)
	}

	m.mu.Lock()
	m.client = client
	m.started = true
	m.mu.Unlock()
	log.LogInfo("echproxy: enabled — %s → https://%s:%s via %s (pid %d)",
		m.cfg.Entry.SrcHost, m.cfg.Entry.EntryHost, m.cfg.Entry.Port,
		m.cfg.Entry.ListenAddr, m.proc.Pid())
	return nil
}

// Stop terminates the subprocess. The downloaded binary is kept for the next start.
// Safe to call when the module was never started or already stopped.
func (m *Module) Stop(ctx context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = false
	m.client = nil
	m.mu.Unlock()

	if err := m.proc.Stop(ctx); err != nil {
		return fmt.Errorf("echproxy: stop subprocess: %w", err)
	}
	log.LogInfo("echproxy: stopped")
	return nil
}

// Client returns the routing http.Client, or nil until Start succeeds.
func (m *Module) Client() *http.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// Alive reports whether the module is started and its child is still running.
// A false value here with started=true means the subprocess died on its own.
func (m *Module) Alive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started && m.proc.Alive()
}

// Entry returns the resolved entry configuration.
func (m *Module) Entry() Entry { return m.cfg.Entry }

// BinaryPath returns where the verified binary is installed.
func (m *Module) BinaryPath() string { return m.dl.BinaryPath() }

// Addr returns the listen address of the managed subprocess.
func (m *Module) Addr() string { return m.cfg.Entry.ListenAddr }

// Pid returns the subprocess pid, or 0 when it is not running.
func (m *Module) Pid() int { return m.proc.Pid() }
