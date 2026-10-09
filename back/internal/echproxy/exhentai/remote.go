// remote.go: pulls the routing document from a URL, caches it, refreshes it in
// the background and degrades to the last-known-good (or the embedded default)
// on failure.
//
// Contract summary — see the package comment in config.go for the full
// what/where/how-often/on-failure text:
//
//   - A Store ALWAYS holds a usable RemoteConfig. The zero Store state is the
//     embedded default, so a node can never end up with an unrouteable table.
//   - Failures are logged and counted, never returned upward in a way that
//     stops the node. Only a Store built with an empty URL has nothing to fetch,
//     and that is a valid configuration meaning "use the built-in table".
//   - Refreshes honour ETag / Last-Modified. A 304 renews the freshness window
//     without touching the cached document, so an unchanged document costs one
//     round trip per cache period and never invalidates in-flight requests.
//   - The current snapshot is read under an RLock on every request, which is
//     what makes a pushed change apply to the next request with no restart.

package exhentai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// maxResponseBytes caps a config document. The document is a small JSON file;
// anything larger is almost certainly a misconfigured URL (an HTML error page,
// a CDN redirect list, ...) and must be rejected rather than buffered.
const maxResponseBytes = 1 << 20 // 1 MiB

// isZero reports whether a RemoteConfig was never populated. RemoteConfig holds
// slices, maps and pointers, so it cannot be compared with ==; this is the
// field-by-field test used to decide whether to substitute the built-in table.
func (c RemoteConfig) isZero() bool {
	return c.Schema == 0 && c.Version == "" && c.UpdatedAt == "" && c.Enabled == nil &&
		c.CacheTTL == 0 && c.RefreshJitter == 0 && c.FetchTimeout == 0 &&
		c.RequestTimeout == 0 && c.Rules == nil
}

// FetchResult describes what one fetch attempt did. It is exposed so tests and
// operators can distinguish "document unchanged" from "nothing cached yet".
type FetchResult int

const (
	// FetchUpdated means a new document was parsed and installed.
	FetchUpdated FetchResult = iota
	// FetchUnchanged means the backend answered 304 and the cache was only
	// revalidated.
	FetchUnchanged
	// FetchFailed means the attempt failed and the previous snapshot is still
	// in force.
	FetchFailed
	// FetchSkipped means there was nothing to fetch (no URL configured).
	FetchSkipped
)

// Snapshot is an immutable view of the current config plus the metadata that
// says where it came from and how fresh it is. Snapshots are never mutated
// after publication; a refresh publishes a whole new one.
type Snapshot struct {
	// Config is the normalised document.
	Config RemoteConfig
	// Version is Config.Version (denormalised so the hot path does not have
	// to dereference a struct field just to log a tag).
	Version string
	// Source is "remote", "remote-unchanged" or "builtin".
	Source string
	// At is when this snapshot was installed locally.
	At time.Time
	// Expires is when the snapshot is next eligible for a refresh. It is the
	// cache freshness boundary, not a hard expiry.
	Expires time.Time
}

// StoreOptions configures a Store.
type StoreOptions struct {
	// URL is the config document URL. Empty = built-in table only, never fetch.
	URL string
	// Defaults is the document used before the first successful fetch.
	// nil = DefaultRemoteConfig().
	Defaults RemoteConfig
	// DefaultsVersioned labels the embedded default so the live version can be
	// told apart from a fetched one.
	DefaultsVersion string
	// Client is the HTTP client used for fetches. nil = a fresh client with
	// FetchTimeout. Inject one to share a proxy or to test without the network.
	Client *http.Client
	// MinCacheTTL overrides the fleet-wide lower clamp on cache_ttl. Leave it
	// zero for the default. It exists so a Store can hold a tighter refresh
	// window than MinCacheTTL without mutating process-global state.
	MinCacheTTL time.Duration
	// AllowInsecureHTTP permits http:// URLs. Default false: the document
	// contains a login cookie for a third-party site, so fetching it over
	// plaintext is a credential leak.
	AllowInsecureHTTP bool
	// AuthHeaders are added to every fetch request (e.g. a bearer token for a
	// private config endpoint).
	AuthHeaders map[string]string
	// Now overrides the clock (tests).
	Now func() time.Time
	// Logf receives fetch and version-change events. nil = silent.
	Logf func(format string, args ...interface{})
}

// Store holds the current config snapshot and the bookkeeping around it.
//
// Concurrency: cfg is guarded by mu; everything else a Store owns is either
// immutable after NewStore or atomic. Load is safe to call from any goroutine,
// including the request path.
type Store struct {
	url         string
	defaults    RemoteConfig
	defaultsVer string
	client      *http.Client
	now         func() time.Time
	logf        func(format string, args ...interface{})

	mu          sync.RWMutex
	cfg         Snapshot
	etag        string
	modified    time.Time
	minInterval time.Duration

	// Cache TTL clamps applied when a document is normalised. Zero values fall
	// back to the fleet-wide defaults.
	minCacheTTL time.Duration
	maxCacheTTL time.Duration

	authHeaders map[string]string

	fetches          int64
	failedFetches    int64
	unchangedFetches int64
}

// NewStore builds a Store. It never performs network I/O; call Sync for that.
func NewStore(opts StoreOptions) (*Store, error) {
	minTTL := opts.MinCacheTTL
	if minTTL <= 0 {
		minTTL = MinCacheTTL
	}
	maxTTL := MaxCacheTTL

	def := opts.Defaults
	if def.isZero() {
		def = DefaultRemoteConfig()
	}
	nd, err := def.normalizeBounds(minTTL, maxTTL)
	if err != nil {
		return nil, fmt.Errorf("exhentai: default config: %w", err)
	}

	if opts.URL != "" && !opts.AllowInsecureHTTP {
		u, err := url.Parse(opts.URL)
		if err != nil {
			return nil, fmt.Errorf("exhentai: config URL %q is not a valid URL: %w", opts.URL, err)
		}
		if u.Scheme == "http" {
			return nil, errors.New("exhentai: config URL uses http:// — refusing (the document may carry a login cookie); set AllowInsecureHTTP to accept it")
		}
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: nd.FetchTimeout.Duration()}
	}

	snap := Snapshot{
		Config:  nd,
		Version: opts.DefaultsVersion,
		Source:  "builtin",
		At:      now(),
		Expires: now().Add(nd.CacheTTL.Duration()),
	}
	if snap.Version == "" {
		snap.Version = "builtin"
	}

	return &Store{
		url:         opts.URL,
		defaults:    nd,
		defaultsVer: snap.Version,
		client:      client,
		now:         now,
		logf:        logf,
		authHeaders: opts.AuthHeaders,
		cfg:         snap,
		minInterval: minRefreshInterval,
		minCacheTTL: minTTL,
		maxCacheTTL: maxTTL,
	}, nil
}

// SnapshotStore is the interface the RoundTripper depends on. It is narrow on
// purpose so a test can supply a fixed config without standing up an httptest
// server, and so the module can be driven by any other config source later.
type SnapshotStore interface {
	// Load returns the current snapshot. Never nil.
	Load() Snapshot
}

// Load returns the current snapshot. It never returns a zero Snapshot.
func (s *Store) Load() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Version returns the version tag of the live document.
func (s *Store) Version() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg.Version
}

// Stats reports fetch counters for operators and tests.
func (s *Store) Stats() (fetches, unchanged, failed int64) {
	return atomic.LoadInt64(&s.fetches), atomic.LoadInt64(&s.unchangedFetches), atomic.LoadInt64(&s.failedFetches)
}

// Sync performs one fetch. It is safe to call repeatedly; it is used at Start
// and again by Run. A failing Sync never removes the current snapshot, so the
// caller can ignore the error and keep serving with what it has.
func (s *Store) Sync(ctx context.Context) FetchResult {
	if s.url == "" {
		atomic.AddInt64(&s.fetches, 1)
		return FetchSkipped
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		s.recordFailure(ctx, fmt.Sprintf("build request: %v", err))
		return FetchFailed
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "peerdrive-exhentai-config/schema-"+strconv.Itoa(SchemaVersion))
	s.mu.RLock()
	if s.etag != "" {
		req.Header.Set("If-None-Match", s.etag)
	}
	if !s.modified.IsZero() {
		req.Header.Add("If-Modified-Since", s.modified.UTC().Format(http.TimeFormat))
	}
	s.mu.RUnlock()
	for k, v := range s.authHeaders {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := s.client.Do(req)
	if err != nil {
		s.recordFailure(ctx, fmt.Sprintf("fetch %s: %v", s.url, err))
		return FetchFailed
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotModified:
		// Unchanged: renew freshness only. The document is still authoritative.
		atomic.AddInt64(&s.fetches, 1)
		atomic.AddInt64(&s.unchangedFetches, 1)
		s.mu.Lock()
		s.cfg.Expires = s.now().Add(s.cfg.Config.CacheTTL.Duration())
		s.cfg.Source = "remote-unchanged"
		snap := s.cfg
		s.mu.Unlock()
		s.logf("exhentai: config %s unchanged (%s, %s)", snap.Version, snap.Source, time.Since(start).Round(time.Millisecond))
		return FetchUnchanged

	case resp.StatusCode != http.StatusOK:
		// 404/410 mean the operator pulled the document: fall back to the
		// built-in table rather than to nothing, which is the whole point of
		// having one. Other codes are transient and keep the last-known-good.
		atomic.AddInt64(&s.fetches, 1)
		atomic.AddInt64(&s.failedFetches, 1)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		s.mu.Lock()
		switch resp.StatusCode {
		case http.StatusNotFound, http.StatusGone:
			s.cfg = s.builtinSnapshot()
			s.etag, s.modified = "", time.Time{}
		default:
			s.cfg.Expires = s.now().Add(s.cfg.Config.CacheTTL.Duration())
		}
		snap := s.cfg
		s.mu.Unlock()
		s.logf("exhentai: config fetch %s -> HTTP %d, keeping %s (%s); body: %.120q",
			s.url, resp.StatusCode, snap.Version, snap.Source, string(body))
		return FetchFailed
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		s.recordFailure(ctx, fmt.Sprintf("read body: %v", err))
		return FetchFailed
	}
	if len(raw) > maxResponseBytes {
		s.recordFailure(ctx, fmt.Sprintf("document exceeds %d bytes, refusing", maxResponseBytes))
		return FetchFailed
	}

	var doc RemoteConfig
	if err := json.Unmarshal(raw, &doc); err != nil {
		s.recordFailure(ctx, fmt.Sprintf("parse json: %v", err))
		return FetchFailed
	}
	nd, err := doc.normalizeBounds(s.minCacheTTL, s.maxCacheTTL)
	if err != nil {
		s.recordFailure(ctx, fmt.Sprintf("validate: %v", err))
		return FetchFailed
	}

	etag := resp.Header.Get("ETag")
	modified := parseHTTPDate(resp.Header.Get("Last-Modified"))

	s.mu.Lock()
	oldVer := s.cfg.Version
	s.cfg = Snapshot{
		Config:  nd,
		Version: nd.Version,
		Source:  "remote",
		At:      s.now(),
		Expires: s.now().Add(nd.CacheTTL.Duration()),
	}
	s.etag = etag
	s.modified = modified
	snap := s.cfg
	s.mu.Unlock()

	atomic.AddInt64(&s.fetches, 1)
	s.logf("exhentai: config %s -> %s (%d rules, %s, %s)", oldVer, snap.Version,
		len(nd.Rules), snap.Source, time.Since(start).Round(time.Millisecond))
	return FetchUpdated
}

// recordFailure keeps the last-known-good snapshot and logs.
func (s *Store) recordFailure(ctx context.Context, reason string) {
	atomic.AddInt64(&s.fetches, 1)
	atomic.AddInt64(&s.failedFetches, 1)
	s.mu.Lock()
	snap := s.cfg
	failed := atomic.LoadInt64(&s.failedFetches)
	s.mu.Unlock()
	s.logf("exhentai: config fetch failed (%s), keeping %s from %s (consecutive failures: %d)",
		reason, snap.Version, snap.Source, failed)
	if ctx.Err() != nil {
		s.logf("exhentai: config fetch cancelled (%v)", ctx.Err())
	}
}

// builtinSnapshot returns the embedded default as a fresh snapshot.
func (s *Store) builtinSnapshot() Snapshot {
	return Snapshot{
		Config:  s.defaults,
		Version: s.defaultsVer,
		Source:  "builtin",
		At:      s.now(),
		Expires: s.now().Add(s.defaults.CacheTTL.Duration()),
	}
}

// nextRefresh returns the delay until the next refresh attempt. It is the cache
// period minus a random jitter draw, floored at 30s so a document that asks for
// a very short TTL does not hammer the URL. Jittering to the *start* of the
// window (rather than adding it) keeps the worst case at exactly one cache
// period, so a broken document is still re-tried within the window it promised.
func (s *Store) nextRefresh() time.Duration {
	s.mu.RLock()
	ttl := s.cfg.Config.CacheTTL.Duration()
	jitter := s.cfg.Config.RefreshJitter.Duration()
	floor := s.minInterval
	s.mu.RUnlock()

	if jitter <= 0 {
		return ttl
	}
	base := ttl - jitter
	if base < floor {
		base = floor
	}
	add := time.Duration(rand.Int64N(int64(jitter.Nanoseconds())))
	return base + add
}

// minRefreshInterval is the shortest interval between fetch attempts,
// regardless of what a document asks for. It guards the process-level default;
// an individual Store may hold a shorter floor in minInterval, which is what
// makes the refresh loop testable without waiting a thirty seconds or writing
// to process-global state from a test goroutine.
const minRefreshInterval = 30 * time.Second

// Run refreshes the document until ctx is cancelled. It returns immediately
// when no URL is configured. Call it in its own goroutine after Sync.
func (s *Store) Run(ctx context.Context) {
	if s.url == "" {
		return
	}
	for {
		d := s.nextRefresh()
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
		if ctx.Err() != nil {
			return
		}
		s.Sync(ctx)
	}
}

// parseHTTPDate parses an RFC 1123 / IMF-fixdate header value.
func parseHTTPDate(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(http.TimeFormat, v)
	if err != nil {
		return time.Time{}
	}
	return t
}
