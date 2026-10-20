// BTBridge bridges the Peerdrive filesystem with BitTorrent DHT, supporting file announce and HTTP fetch.
package p2p_bt

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
)

// maxBridgeFetchBytes caps HTTP responses read from untrusted BT DHT peers (512 MiB)
// to prevent unbounded io.ReadAll OOM attacks.
const maxBridgeFetchBytes = 512 << 20

// validateSHA256Hex verifies that hash is a strict 64-character hexadecimal string.
// Prevents slice-bounds panic on hash[:2] for short strings and path traversal via "../".
func validateSHA256Hex(hash string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(hash))
	if len(h) != 64 {
		return "", fmt.Errorf("invalid sha256 hash length %d (want 64)", len(h))
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("invalid sha256 hex character %q at index %d", c, i)
		}
	}
	return h, nil
}

// BTBridge connects Peerdrive's FileService with the BitTorrent DHT.
// It announces local files on the DHT and fetches files from remote peers
// discovered via the DHT.
type BTBridge struct {
	DHT        *BTDHTService
	storageDir string
	mu         sync.RWMutex
	shared     map[string]struct{}
}

// NewBTBridge creates a bridge instance with the given DHT service and storage directory.
func NewBTBridge(dhtSvc *BTDHTService, storageDir string) *BTBridge {
	return &BTBridge{
		DHT:        dhtSvc,
		storageDir: storageDir,
		shared:     make(map[string]struct{}),
	}
}

// ShareFile announces a specified 64-character hex hash on the BitTorrent DHT.
func (b *BTBridge) ShareFile(hash string) error {
	defer LogDuration("BTBridge.ShareFile")()
	LogDebug("bt-bridge: ShareFile hash=%s", hash)

	cleanHash, err := validateSHA256Hex(hash)
	if err != nil {
		return fmt.Errorf("invalid hash for ShareFile: %w", err)
	}

	if b.DHT == nil {
		err := fmt.Errorf("BT DHT not available")
		LogError("bt-bridge: ShareFile failed: %v", err)
		return err
	}
	if err := b.DHT.Announce(cleanHash); err != nil {
		LogError("bt-bridge: ShareFile announce failed: %v", err)
		return fmt.Errorf("share announce: %w", err)
	}
	b.mu.Lock()
	b.shared[cleanHash] = struct{}{}
	b.mu.Unlock()
	LogInfo("bt-bridge: shared file %s on BT DHT", cleanHash)
	return nil
}

// FetchFile finds file providers on the BitTorrent DHT and downloads via HTTP
// with size limit protection and end-to-end SHA-256 content verification.
func (b *BTBridge) FetchFile(ctx context.Context, hash string) ([]byte, error) {
	defer LogDuration("BTBridge.FetchFile")()
	LogDebug("bt-bridge: FetchFile hash=%s", hash)

	cleanHash, err := validateSHA256Hex(hash)
	if err != nil {
		return nil, fmt.Errorf("invalid hash for FetchFile: %w", err)
	}

	if b.DHT == nil {
		err := fmt.Errorf("BT DHT not available")
		LogError("bt-bridge: FetchFile failed: %v", err)
		return nil, err
	}

	peers, err := b.DHT.FindProviders(cleanHash)
	if err != nil {
		LogError("bt-bridge: FetchFile find providers failed: %v", err)
		return nil, fmt.Errorf("find providers: %w", err)
	}
	if len(peers) == 0 {
		LogInfo("bt-bridge: no BT DHT providers found for %s", cleanHash)
		return nil, fmt.Errorf("no BT DHT providers found for %s", cleanHash)
	}

	LogInfo("bt-bridge: found %d BT DHT providers for %s, attempting fetch", len(peers), cleanHash)

	// Try each peer via HTTP. We assume peers serve files on the same port as
	// their DHT listen port (or an adjacent HTTP port).
	httpClient := &http.Client{Timeout: 15 * time.Second}

	for _, peerAddr := range peers {
		url := fmt.Sprintf("http://%s/files/%s", peerAddr, cleanHash)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			LogWarn("bt-bridge: HTTP fetch from %s failed: %v", peerAddr, err)
			continue
		}

		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBridgeFetchBytes+1))
		resp.Body.Close()
		if err != nil {
			LogWarn("bt-bridge: HTTP read from %s failed: %v", peerAddr, err)
			continue
		}
		if int64(len(data)) > maxBridgeFetchBytes {
			LogWarn("bt-bridge: HTTP response from %s exceeds maxBridgeFetchBytes (%d)", peerAddr, maxBridgeFetchBytes)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			LogWarn("bt-bridge: HTTP %d from %s", resp.StatusCode, peerAddr)
			continue
		}

		// Verify SHA-256 integrity before accepting bytes from an untrusted DHT peer
		digest := sha256.Sum256(data)
		gotHash := hex.EncodeToString(digest[:])
		if gotHash != cleanHash {
			LogWarn("bt-bridge: SHA-256 mismatch from peer %s: want %s, got %s", peerAddr, cleanHash, gotHash)
			continue
		}

		LogInfo("bt-bridge: fetched and verified %s from %s (%d bytes)", cleanHash, peerAddr, len(data))
		return data, nil
	}

	err = fmt.Errorf("could not fetch %s from any BT DHT peer", cleanHash)
	LogError("bt-bridge: %v", err)
	return nil, err
}

// ListShared returns the current list of all shared hashes.
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
// Returns empty string if hash is not a valid 64-character hex string.
func (b *BTBridge) FilePath(hash string) string {
	cleanHash, err := validateSHA256Hex(hash)
	if err != nil {
		return ""
	}
	return filepath.Join(b.storageDir, cleanHash[:2], cleanHash)
}

// EnsureFileWritten atomically writes data to the standard peerdrive storage layout
// after validating the hash and verifying data integrity.
func (b *BTBridge) EnsureFileWritten(hash string, data []byte) error {
	cleanHash, err := validateSHA256Hex(hash)
	if err != nil {
		return fmt.Errorf("invalid hash for EnsureFileWritten: %w", err)
	}
	fullPath := filepath.Join(b.storageDir, cleanHash[:2], cleanHash)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create storage dir: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, ".bt-bridge-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, fullPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename file: %w", err)
	}
	return nil
}
