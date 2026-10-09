// module.go: the public entry point for the optional exhentai module.
//
// Wiring (see internal/serverapp/app.go): when PEERDRIVE_EXHENTA_ENABLE=true
// the server calls New(...) then Start(ctx), hands Client() to
// source.NewURLSource the same way the twimg client is handed, and registers
// Stop(ctx) as a shutdown hook. When the flag is false none of this runs, so
// the default build and runtime behaviour are byte-for-byte unchanged.
//
// Unlike the iwara and twimg modules there is no subprocess to manage: the
// backend is a directly reachable mirror, so Start only does one synchronous
// config fetch and launches the background refresh. See the package comment in
// config.go for the delivery contract and why a failed fetch degrades instead
// of failing startup.

package exhentai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Config is the module's settings. Zero-value fields fall back to documented
// defaults.
type Config struct {
	// ConfigURL is the routing document URL (PEERDRIVE_EXHENTA_CONFIG_URL).
	// Empty = use only the built-in table and never fetch. That is a valid,
	// quiet configuration, not an error.
	ConfigURL string
	// AllowInsecureHTTP permits an http:// ConfigURL. Default false.
	AllowInsecureHTTP bool
	// Defaults overrides the embedded fallback document.
	Defaults RemoteConfig
	// HTTPBase is inherited by the returned client for non-routed traffic
	// (Timeout, CheckRedirect, Jar). nil = a fresh client.
	HTTPBase *http.Client
	// ConfigHTTP is the client used to fetch the document. nil = a fresh
	// client using the document's fetch_timeout (default 10s).
	ConfigHTTP *http.Client
	// AuthHeaders are sent with every config fetch.
	AuthHeaders map[string]string
	// Now overrides the clock, for tests.
	Now func() time.Time
	// Logf receives lifecycle and fetch events. nil = silent.
	Logf func(format string, args ...interface{})
}

// Module owns the config Store and the http.Client that routes through it.
//
// Concurrency: Start and Stop are serialised by startMu so a Stop racing an
// in-flight Start waits for it instead of tearing down a half-built client.
// Read-only accessors may run concurrently with both.
type Module struct {
	cfg      Config
	store    *Store
	router   *Router
	mu       sync.Mutex
	startMu  sync.Mutex
	client   *http.Client
	started  bool
	stopOnce sync.Once
	ctx      context.Context
	cancel   context.CancelFunc
}

// New validates cfg, builds the Store and Router, and returns the module.
// Nothing is fetched yet: call Start.
func New(cfg Config) (*Module, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...interface{}) {}
	}

	store, err := NewStore(StoreOptions{
		URL:               cfg.ConfigURL,
		Defaults:          cfg.Defaults,
		DefaultsVersion:   "builtin",
		Client:            cfg.ConfigHTTP,
		AllowInsecureHTTP: cfg.AllowInsecureHTTP,
		AuthHeaders:       cfg.AuthHeaders,
		Now:               cfg.Now,
		Logf:              cfg.Logf,
	})
	if err != nil {
		return nil, err
	}
	router, err := NewRouter(store)
	if err != nil {
		return nil, err
	}
	return &Module{cfg: cfg, store: store, router: router}, nil
}

// Start performs one synchronous config fetch and launches the background
// refresh. After it returns, Client() is non-nil.
//
// A fetch failure is logged but not returned: the Store always holds a usable
// table (last-known-good, then the built-in default), so the module degrades
// to the built-in routing rather than refusing to start. This is deliberate
// and is the opposite of twimg, whose Start fails loudly because a half-running
// ech-proxy subprocess is worse than a loud failure — here the failure mode is
// "behaves like the disabled node", which is the safe one.
func (m *Module) Start(ctx context.Context) error {
	m.startMu.Lock()
	defer m.startMu.Unlock()

	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	client, err := NewClient(m.router, m.cfg.HTTPBase)
	if err != nil {
		return fmt.Errorf("exhentai: build client: %w", err)
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())

	switch res := m.store.Sync(ctx); res {
	case FetchSkipped:
		m.cfg.Logf("exhentai: no config URL — using built-in table (%s)", m.store.Version())
	case FetchFailed:
		m.cfg.Logf("exhentai: initial config fetch failed; using %s from %s",
			m.store.Version(), m.store.Load().Source)
	default:
		m.cfg.Logf("exhentai: config loaded (%s, %d rules)", m.store.Version(),
			len(m.store.Load().Config.Rules))
	}

	m.mu.Lock()
	m.client = client
	m.started = true
	m.mu.Unlock()

	if m.cfg.ConfigURL != "" {
		go m.store.Run(m.ctx)
	}

	snap := m.store.Load()
	m.cfg.Logf("exhentai: enabled — %d rules, version %s (%s)",
		len(snap.Config.Rules), snap.Version, snap.Source)
	return nil
}

// Stop cancels the background refresh and clears the client. It never blocks on
// in-flight requests. Safe to call when the module was never started.
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

	m.stopOnce.Do(func() { m.cancel() })
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	m.cfg.Logf("exhentai: stopped")
	return nil
}

// Client returns the routing http.Client, or nil until Start succeeds.
func (m *Module) Client() *http.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// Store returns the config Store, for tests and operators. Never nil after New.
func (m *Module) Store() *Store { return m.store }

// Router returns the live Router, for tests. Never nil after New.
func (m *Module) Router() *Router { return m.router }

// Version returns the version tag of the live routing document.
func (m *Module) Version() string { return m.store.Version() }

// Snapshot returns the live routing snapshot. Never nil after New.
func (m *Module) Snapshot() Snapshot { return m.store.Load() }

// Alive reports whether the module is started. There is no subprocess, so this
// is simply the started flag; it is provided for symmetry with twimg.Module.
func (m *Module) Alive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.started
}

// ValidateConfig is a helper for callers that want to check a Config before
// constructing a Module. It only checks the fields NewStore validates.
func ValidateConfig(cfg Config) error {
	_, err := New(cfg)
	return err
}

// ErrDisabled is returned if a caller asks for a client before Start. It is
// exported so tests can assert the zero-change contract without reflection.
var ErrDisabled = errors.New("exhentai: module is not started")
