// Package service — Universal multi-protocol download pipeline.
//
// UniversalDownloader tries every available protocol in priority order:
//
//	local  → reads from content-addressed storage on disk
//	ipfs   → libp2p DHT + exchange
//	btdht  → BitTorrent Mainline DHT HTTP bridge
//	webrtc → WebRTC data channel (placeholder)
//	http   → HTTP URL registered in file_providers
//
// On success the file is cached to local storage so the next request
// is served instantly by the LocalFetcher.

package service

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
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/p2p_bt"
	"peerdrive/internal/repository"
)

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
// IPFSFetcher
// ---------------------------------------------------------------------------

// IPFSFetcher uses the libp2p DHT + exchange protocol to fetch files.
type IPFSFetcher struct {
	p2pSvc *P2PService
}

func (f *IPFSFetcher) Name() string { return "ipfs" }

func (f *IPFSFetcher) IsAvailable() bool {
	return f.p2pSvc != nil && f.p2pSvc.IsEnabled()
}

func (f *IPFSFetcher) Fetch(ctx context.Context, hash string) ([]byte, error) {
	return f.p2pSvc.FetchFile(ctx, hash, nil)
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

// NewBTDHTFetcher creates a BTDHTFetcher backed by the given DHT service.
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
// WebRTCFetcher (placeholder)
// ---------------------------------------------------------------------------

// WebRTCFetcher is a placeholder for WebRTC data channel downloads.
// It always reports unavailable until the backend is implemented.
type WebRTCFetcher struct {
	enabled bool
}

// NewWebRTCFetcher creates a WebRTC fetcher.
func NewWebRTCFetcher(enabled bool) *WebRTCFetcher {
	return &WebRTCFetcher{enabled: enabled}
}

func (f *WebRTCFetcher) Name() string { return "webrtc" }

func (f *WebRTCFetcher) IsAvailable() bool { return f.enabled }

func (f *WebRTCFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	return nil, fmt.Errorf("webrtc: not yet implemented")
}

// ---------------------------------------------------------------------------
// HTTPURLFetcher
// ---------------------------------------------------------------------------

// HTTPURLFetcher checks file_providers for "http"-type entries and fetches
// from the registered URL.
type HTTPURLFetcher struct{}

func (f *HTTPURLFetcher) Name() string { return "http" }

func (f *HTTPURLFetcher) IsAvailable() bool { return true }

func (f *HTTPURLFetcher) Fetch(ctx context.Context, hash string) ([]byte, error) {
	providers, err := repository.GetFileProviders(hash)
	if err != nil {
		return nil, fmt.Errorf("http: db lookup failed: %w", err)
	}
	for _, p := range providers {
		if p.ProviderType != "http" || !p.Available {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Path, nil)
		if err != nil {
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		return data, nil
	}
	return nil, fmt.Errorf("http: no URL provider found for %s", hash)
}

// ---------------------------------------------------------------------------
// UniversalDownloader
// ---------------------------------------------------------------------------

// UniversalDownloader tries every registered protocol in priority order and
// caches successfully downloaded files to local storage.
type UniversalDownloader struct {
	storageDir string
	fetchers   []ProtocolFetcher
	timeout    time.Duration
}

// NewUniversalDownloader creates a downloader with the given protocol order.
//   - order is a comma-separated list of protocol names
//     (default: "local,ipfs,btdht,http")
//   - timeout is the per-protocol fetch deadline
func NewUniversalDownloader(
	p2pSvc *P2PService,
	btSvc *p2p_bt.BTDHTService,
	storageDir string,
	order string,
	timeout time.Duration,
) *UniversalDownloader {
	d := &UniversalDownloader{
		storageDir: storageDir,
		timeout:    timeout,
	}
	d.fetchers = d.buildFetchers(order, p2pSvc, btSvc)
	return d
}

// buildFetchers builds the ordered fetcher list from a comma-separated string.
func (d *UniversalDownloader) buildFetchers(order string, p2pSvc *P2PService, btSvc *p2p_bt.BTDHTService) []ProtocolFetcher {
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
		filtered = []string{"local", "ipfs", "btdht", "http"}
	}

	registry := map[string]func() ProtocolFetcher{
		"local": func() ProtocolFetcher {
			return &LocalFetcher{storageDir: d.storageDir}
		},
		"ipfs": func() ProtocolFetcher {
			return &IPFSFetcher{p2pSvc: p2pSvc}
		},
		"btdht": func() ProtocolFetcher {
			return NewBTDHTFetcher(btSvc, d.storageDir)
		},
		"webrtc": func() ProtocolFetcher {
			return NewWebRTCFetcher(false)
		},
		"http": func() ProtocolFetcher {
			return &HTTPURLFetcher{}
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

// Download tries each protocol in priority order.  On success the file is
// cached to local storage and (data, protocolName, nil) is returned.
func (d *UniversalDownloader) Download(ctx context.Context, hash string) ([]byte, string, error) {
	for _, fetcher := range d.fetchers {
		if !fetcher.IsAvailable() {
			log.LogDebug("downloader: %s not available, skipping", fetcher.Name())
			continue
		}

		fetchCtx, cancel := context.WithTimeout(ctx, d.timeout)
		data, err := fetcher.Fetch(fetchCtx, hash)
		cancel()

		if err == nil {
			// Verify SHA-256 hash matches.
			h := sha256.Sum256(data)
			if hex.EncodeToString(h[:]) != hash {
				log.LogWarn("downloader: %s returned hash mismatch for %s", fetcher.Name(), hash)
				continue
			}
			// Cache to local storage so subsequent requests are instant.
			d.cacheToLocal(hash, data)
			log.LogInfo("downloader: fetched %s via %s (%d bytes)", hash, fetcher.Name(), len(data))
			return data, fetcher.Name(), nil
		}
		log.LogDebug("downloader: %s failed for %s: %v", fetcher.Name(), hash, err)
	}
	return nil, "", fmt.Errorf("file not found on any protocol")
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

// CheckSources returns a map of protocol -> availability for the given hash.
// For local and http the check is accurate (disk / DB lookup).  For network
// protocols it returns whether the backend is available at all.
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

// ClearLocalCache removes the cached local copy for the given hash so the
// next download will re-fetch from the network.
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

// Fetchers returns the ordered slice of protocol fetchers (exposed for tests).
func (d *UniversalDownloader) Fetchers() []ProtocolFetcher {
	return d.fetchers
}
