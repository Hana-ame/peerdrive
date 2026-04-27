package p2p_bt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// BTBridge connects Peerdrive's FileService with the BitTorrent DHT.
// It announces local files on the DHT and fetches files from remote peers
// discovered via the DHT.
type BTBridge struct {
	DHT        *BTDHTService
	storageDir string
	mu         sync.RWMutex
	shared     map[string]struct{}
}

// NewBTBridge creates a new bridge using the given DHT service and storage
// directory. The storage directory is used to serve announced files via HTTP.
func NewBTBridge(dhtSvc *BTDHTService, storageDir string) *BTBridge {
	return &BTBridge{
		DHT:        dhtSvc,
		storageDir: storageDir,
		shared:     make(map[string]struct{}),
	}
}

// ShareFile announces the given 64-char hex hash on the BitTorrent DHT and
// records it in the local shared set.
func (b *BTBridge) ShareFile(hash string) error {
	defer log.LogDuration("BTBridge.ShareFile")()
	log.LogDebug("bt-bridge: ShareFile hash=%s", hash)

	if b.DHT == nil {
		err := fmt.Errorf("BT DHT not available")
		log.LogError("bt-bridge: ShareFile failed: %v", err)
		return err
	}
	if err := b.DHT.Announce(hash); err != nil {
		log.LogError("bt-bridge: ShareFile announce failed: %v", err)
		return fmt.Errorf("share announce: %w", err)
	}
	b.mu.Lock()
	b.shared[hash] = struct{}{}
	b.mu.Unlock()
	log.LogInfo("bt-bridge: shared file %s on BT DHT", hash)
	return nil
}

// FetchFile looks up providers for the given hash on the BitTorrent DHT, then
// attempts to download the file via HTTP from discovered peers. It returns
// the file content from the first successful peer.
func (b *BTBridge) FetchFile(ctx context.Context, hash string) ([]byte, error) {
	defer log.LogDuration("BTBridge.FetchFile")()
	log.LogDebug("bt-bridge: FetchFile hash=%s", hash)

	if b.DHT == nil {
		err := fmt.Errorf("BT DHT not available")
		log.LogError("bt-bridge: FetchFile failed: %v", err)
		return nil, err
	}

	peers, err := b.DHT.FindProviders(hash)
	if err != nil {
		log.LogError("bt-bridge: FetchFile find providers failed: %v", err)
		return nil, fmt.Errorf("find providers: %w", err)
	}
	if len(peers) == 0 {
		log.LogInfo("bt-bridge: no BT DHT providers found for %s", hash)
		return nil, fmt.Errorf("no BT DHT providers found for %s", hash)
	}

	log.LogInfo("bt-bridge: found %d BT DHT providers for %s, attempting fetch", len(peers), hash)

	// Try each peer via HTTP. We assume peers serve files on the same port as
	// their DHT listen port (or an adjacent HTTP port).
	httpClient := &http.Client{Timeout: 15 * time.Second}

	for _, peerAddr := range peers {
		url := fmt.Sprintf("http://%s/files/%s", peerAddr, hash)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			log.LogWarn("bt-bridge: HTTP fetch from %s failed: %v", peerAddr, err)
			continue
		}

		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.LogWarn("bt-bridge: HTTP read from %s failed: %v", peerAddr, err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.LogWarn("bt-bridge: HTTP %d from %s", resp.StatusCode, peerAddr)
			continue
		}

		log.LogInfo("bt-bridge: fetched %s from %s (%d bytes)", hash, peerAddr, len(data))
		return data, nil
	}

	err = fmt.Errorf("could not fetch %s from any BT DHT peer", hash)
	log.LogError("bt-bridge: %v", err)
	return nil, err
}

// ListShared returns all currently shared hashes.
func (b *BTBridge) ListShared() ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	result := make([]string, 0, len(b.shared))
	for h := range b.shared {
		result = append(result, h)
	}
	return result, nil
}

// FilePath returns the full storage path for a given hash.
func (b *BTBridge) FilePath(hash string) string {
	return filepath.Join(b.storageDir, hash[:2], hash)
}

// EnsureFileWritten writes data to the standard peerdrive storage layout.
func (b *BTBridge) EnsureFileWritten(hash string, data []byte) error {
	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(b.storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
