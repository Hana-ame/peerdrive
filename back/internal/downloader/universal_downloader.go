// Package service — Universal multi-protocol download pipeline.
//
// UniversalDownloader tries every available protocol in priority order:
//
//	local  → reads from content-addressed storage on disk
//	ipfsgw → IPFS HTTP gateway (via provider.IPFSProvider, public/set gateways)
//	btdht  → BitTorrent Mainline DHT HTTP bridge (independent go-peerdrive-bt)
//	http   → HTTP URL registered in file_providers
//
// Batch 2 (2026-08-16): Removed IPFSFetcher (libp2p DHT+Bitswap stack removed with the old interconnection layer;
// IPFS compatibility API/Bitswap is no longer provided, only HTTP gateway fetching is retained).
//
// On success the file is cached to local storage, so the next request
// is served instantly by the LocalFetcher.

package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"
)

// ---------------------------------------------------------------------------
// Timing metrics
// ---------------------------------------------------------------------------

// FetcherMetric records the result and duration of a single fetcher attempt.
type FetcherMetric struct {
	Name     string        `json:"name"`
	Duration time.Duration `json:"duration"`
	Success  bool          `json:"success"`
	Error    string        `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// ProtocolFetcher interface
// ---------------------------------------------------------------------------

// ProtocolFetcher is implemented by each protocol backend.  Fetch must return
// the file bytes or a non-nil error.  The caller verifies the SHA-256 hash.
type ProtocolFetcher interface {
	Name() string
	Fetch(ctx context.Context, hash string) ([]byte, error)
	IsAvailable() bool
}

// ---------------------------------------------------------------------------
// LocalFetcher
// ---------------------------------------------------------------------------

// LocalFetcher reads from the content-addressed storage directory.
type LocalFetcher struct {
	storageDir string
}

func (f *LocalFetcher) Name() string { return "local" }

func (f *LocalFetcher) IsAvailable() bool { return f.storageDir != "" }

func (f *LocalFetcher) Fetch(_ context.Context, hash string) ([]byte, error) {
	// Try standard content-addressed paths.
	candidates := []string{
		filepath.Join(f.storageDir, hash[:2], hash),
		filepath.Join(f.storageDir, "p2p", hash[:2], hash),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return data, nil
		}
	}

	// Fallback: check DB for registered local providers.
	providers, err := repository.GetFileProviders(hash)
	if err != nil {
		return nil, fmt.Errorf("local: db lookup failed: %w", err)
	}
	for _, p := range providers {
		if p.ProviderType == "local" && p.Available {
			path := p.Path
			if !filepath.IsAbs(path) {
				path = filepath.Join(f.storageDir, path)
			}
			data, err := os.ReadFile(path)
			if err == nil {
				return data, nil
			}
		}
	}
	return nil, fmt.Errorf("local: file not found")
}

// ---------------------------------------------------------------------------
// BTDHTFetcher
// ---------------------------------------------------------------------------

// BTDHTFetcher uses the BitTorrent DHT HTTP bridge to discover peers and
// fetch the file via HTTP.
type BTDHTFetcher struct {
	bridge *p2p_bt.BTBridge
	dhtSvc *p2p_bt.BTDHTService
}

// NewBTDHTFetcher creates a BitTorrent DHT-based fetcher.
func NewBTDHTFetcher(dhtSvc *p2p_bt.BTDHTService, storageDir string) *BTDHTFetcher {
	return &BTDHTFetcher{
		bridge: p2p_bt.NewBTBridge(dhtSvc, storageDir),
		dhtSvc: dhtSvc,
	}
}

func (f *BTDHTFetcher) Name() string { return "btdht" }

func (f *BTDHTFetcher) IsAvailable() bool {
	return f.dhtSvc != nil && f.dhtSvc.Server != nil
}

func (f *BTDHTFetcher) Fetch(ctx context.Context, hash string) ([]byte, error) {
	return f.bridge.FetchFile(ctx, hash)
}

// ---------------------------------------------------------------------------
// HTTPURLFetcher
// ---------------------------------------------------------------------------

// HTTPURLFetcher checks file_providers for "http"-type entries and fetches
// from the registered URL.
type HTTPURLFetcher struct {
	// httpClient with timeout (M5): the original implementation used http.DefaultClient with no timeout,
	// a slow URL provider would hang Download permanently (the download endpoint would hang too).
	// The entire Fetch is bounded by Download's per-fetcher context; this adds a second safety net.
	httpClient *http.Client
}

// maxURLFetchSize URL provider single-fetch limit (M5):
// Same as the peerjs upload limit (8GB), prevents malicious/runaway URLs from returning infinite streams.
const maxURLFetchSize = 8 * 1024 * 1024 * 1024

func (f *HTTPURLFetcher) Name() string { return "http" }

func (f *HTTPURLFetcher) IsAvailable() bool { return true }

func (f *HTTPURLFetcher) Fetch(ctx context.Context, hash string) ([]byte, error) {
	providers, err := repository.GetFileProviders(hash)
	if err != nil {
		return nil, fmt.Errorf("http: db lookup failed: %w", err)
	}
	client := f.httpClient
	if client == nil {
		client = http.DefaultClient // Defense: fallback when NewUniversalDownloader doesn't inject
	}
	for _, p := range providers {
		if p.ProviderType != "http" || !p.Available {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Path, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		// M5: LimitReader throttling; exceeding the limit is treated as an anomalous provider
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxURLFetchSize+1))
		resp.Body.Close()
		if err != nil {
			continue
		}
		if len(data) > maxURLFetchSize {
			log.LogWarn("downloader: http provider %s returned oversized payload for %s", p.Path, hash)
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("http: no URL provider found for %s", hash)
}

// ---------------------------------------------------------------------------
// IPFSGatewayFetcher
// ---------------------------------------------------------------------------

// IPFSGatewayFetcher converts a SHA-256 hash to an IPFS CID and fetches
// the content from public IPFS gateways.
type IPFSGatewayFetcher struct {
	provider *provider.IPFSProvider
}

func (f *IPFSGatewayFetcher) Name() string { return "ipfsgw" }

func (f *IPFSGatewayFetcher) IsAvailable() bool {
	return f.provider != nil && len(f.provider.Gateways) > 0
}

func (f *IPFSGatewayFetcher) Fetch(ctx context.Context, hash string) ([]byte, error) {
	cid := hashutil.SHA256ToCID(hash)
	if cid == "" {
		return nil, fmt.Errorf("ipfsgw: failed to convert hash to CID")
	}
	return f.provider.FetchByCID(ctx, cid)
}

// ---------------------------------------------------------------------------
// UniversalDownloader
// ---------------------------------------------------------------------------

// UniversalDownloader tries every registered protocol in priority order and
// caches successfully downloaded files to local storage.
type UniversalDownloader struct {
	storageDir   string
	fetchers     []ProtocolFetcher
	timeout      time.Duration
	ipfsProvider *provider.IPFSProvider

	// M5: lastMetrics read/write race (concurrent Download writes vs LastMetrics reads,
	// /download/:hash/sources endpoint called at high frequency) → mutex protection.
	metricsMu   sync.Mutex
	lastMetrics []FetcherMetric
}

// NewUniversalDownloader creates a universal downloader that tries multiple protocols in priority order.
func NewUniversalDownloader(
	btSvc *p2p_bt.BTDHTService,
	storageDir string,
	order string,
	timeout time.Duration,
	ipfsProvider *provider.IPFSProvider,
) *UniversalDownloader {
	d := &UniversalDownloader{
		storageDir:   storageDir,
		timeout:      timeout,
		ipfsProvider: ipfsProvider,
	}
	d.fetchers = d.buildFetchers(order, btSvc)
	return d
}

// buildFetchers builds the ordered fetcher list from a comma-separated string.
func (d *UniversalDownloader) buildFetchers(order string, btSvc *p2p_bt.BTDHTService) []ProtocolFetcher {
	names := strings.Split(order, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	// Filter empty strings left by trailing commas.
	filtered := names[:0]
	for _, n := range names {
		if n != "" {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		filtered = []string{"local", "ipfsgw", "btdht", "http"}
	}

	registry := map[string]func() ProtocolFetcher{
		"local": func() ProtocolFetcher {
			return &LocalFetcher{storageDir: d.storageDir}
		},
		"ipfsgw": func() ProtocolFetcher {
			return &IPFSGatewayFetcher{provider: d.ipfsProvider}
		},
		"btdht": func() ProtocolFetcher {
			return NewBTDHTFetcher(btSvc, d.storageDir)
		},
		"http": func() ProtocolFetcher {
			// M5: URL provider uses a client with timeout (Download's per-fetcher
			// context bounds overall time, client timeout prevents single-request hang)
			return &HTTPURLFetcher{httpClient: &http.Client{Timeout: d.timeout}}
		},
	}

	var fetchers []ProtocolFetcher
	for _, name := range filtered {
		if fn, ok := registry[name]; ok {
			fetchers = append(fetchers, fn())
		} else {
			log.LogWarn("downloader: unknown protocol %q in order, skipping", name)
		}
	}
	return fetchers
}

// Download tries each protocol in priority order to download the file, caching successfully to local storage.
// Each attempt records timing metrics, retrievable via LastMetrics().
func (d *UniversalDownloader) Download(ctx context.Context, hash string) (data []byte, protocol string, err error) {
	// Defense: hash comes from remote input of anon collection entries (sync/serve path),
	// without validation it would trigger a hash[:2] out-of-bounds panic in LocalFetcher. The local download endpoint
	// already validates upstream; this is the last line of defense.
	if !hashutil.IsStrictSHA256(hash) {
		return nil, "", fmt.Errorf("download: invalid hash %q", hash)
	}
	metrics := make([]FetcherMetric, 0, len(d.fetchers))
	defer func() {
		d.metricsMu.Lock()
		d.lastMetrics = metrics
		d.metricsMu.Unlock()
	}()

	for _, fetcher := range d.fetchers {
		if !fetcher.IsAvailable() {
			log.LogDebug("downloader: %s not available, skipping", fetcher.Name())
			metrics = append(metrics, FetcherMetric{
				Name:    fetcher.Name(),
				Success: false,
				Error:   "not available",
			})
			continue
		}

		start := time.Now()
		fetchCtx, cancel := context.WithTimeout(ctx, d.timeout)
		fetchData, fetchErr := fetcher.Fetch(fetchCtx, hash)
		cancel()
		elapsed := time.Since(start)

		if fetchErr == nil {
			// Verify SHA-256 hash matches.
			h := sha256.Sum256(fetchData)
			if hex.EncodeToString(h[:]) != hash {
				log.LogWarn("downloader: %s returned hash mismatch for %s", fetcher.Name(), hash)
				metrics = append(metrics, FetcherMetric{
					Name:     fetcher.Name(),
					Duration: elapsed,
					Success:  false,
					Error:    "hash mismatch",
				})
				continue
			}
			// Cache to local storage so subsequent requests are instant.
			d.cacheToLocal(hash, fetchData)
			log.LogInfo("downloader: fetched %s via %s (%d bytes, %v)", hash, fetcher.Name(), len(fetchData), elapsed)
			metrics = append(metrics, FetcherMetric{
				Name:     fetcher.Name(),
				Duration: elapsed,
				Success:  true,
			})
			return fetchData, fetcher.Name(), nil
		}
		log.LogDebug("downloader: %s failed for %s: %v (%v)", fetcher.Name(), hash, fetchErr, elapsed)
		metrics = append(metrics, FetcherMetric{
			Name:     fetcher.Name(),
			Duration: elapsed,
			Success:  false,
			Error:    fetchErr.Error(),
		})
	}
	return nil, "", fmt.Errorf("file not found on any protocol")
}

// LastMetrics returns the per-protocol attempt records (including duration) from the most recent Download call.
func (d *UniversalDownloader) LastMetrics() []FetcherMetric {
	d.metricsMu.Lock()
	defer d.metricsMu.Unlock()
	return d.lastMetrics
}

// cacheToLocal writes the data to content-addressed storage and registers it
// in the database so future lookups hit the LocalFetcher.
func (d *UniversalDownloader) cacheToLocal(hash string, data []byte) {
	relPath := filepath.Join(hash[:2], hash)
	fullPath := filepath.Join(d.storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		log.LogWarn("downloader: cache mkdir failed: %v", err)
		return
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		log.LogWarn("downloader: cache write failed: %v", err)
		return
	}
	// Insert meta (ignore conflict so re-caching is idempotent).
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Size:     int64(len(data)),
		Gziped:   false,
		Filename: hash,
		Type:     repository.FileTypeBlob,
	})
	repository.InsertFileProvider(hash, "local", relPath)
}

// ---------------------------------------------------------------------------
// Source checks
// ---------------------------------------------------------------------------

// CheckSources returns a map of protocol availability for the given hash.
func (d *UniversalDownloader) CheckSources(ctx context.Context, hash string) map[string]bool {
	result := make(map[string]bool, len(d.fetchers))
	for _, fetcher := range d.fetchers {
		if !fetcher.IsAvailable() {
			result[fetcher.Name()] = false
			continue
		}
		switch f := fetcher.(type) {
		case *LocalFetcher:
			_, err := f.Fetch(ctx, hash)
			result[f.Name()] = err == nil
		case *HTTPURLFetcher:
			_, err := f.Fetch(ctx, hash)
			result[f.Name()] = err == nil
		default:
			// Network protocols: report capability, not specific file presence.
			result[fetcher.Name()] = true
		}
	}
	return result
}

// ClearLocalCache clears the local cache for the given hash; the next download will re-fetch from the network.
func (d *UniversalDownloader) ClearLocalCache(hash string) {
	// Remove from standard content-addressed paths.
	paths := []string{
		filepath.Join(d.storageDir, hash[:2], hash),
		filepath.Join(d.storageDir, "p2p", hash[:2], hash),
	}
	for _, p := range paths {
		if err := os.Remove(p); err == nil {
			log.LogDebug("downloader: cleared local cache for %s at %s", hash, p)
		}
	}
	// Mark existing local providers unavailable so the downloader
	// won't pick them up.
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" && p.Available {
			repository.MarkProviderUnavailable(p.ID)
		}
	}
}

// Fetchers returns the protocol fetcher slice sorted by priority (exposed for testing).
func (d *UniversalDownloader) Fetchers() []ProtocolFetcher {
	return d.fetchers
}
