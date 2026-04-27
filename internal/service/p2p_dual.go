package service

import (
	"context"
	"fmt"
	"sync"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/p2p_bt"
)

// DualFindResult holds merged results from both the IPFS/libp2p and
// BitTorrent DHT networks.
type DualFindResult struct {
	IPFSPeers []string `json:"ipfs_peers"`
	BTPeers   []string `json:"bt_peers"`
}

// DualP2PService wraps both the IPFS/libp2p P2PService and the BitTorrent
// Mainline DHT service, providing unified operations across both networks.
type DualP2PService struct {
	IPFS *P2PService
	BT   *p2p_bt.BTDHTService
	cfg  *config.Config
	mu   sync.Mutex
}

// NewDualP2PService creates a DualP2PService that wraps both the existing
// IPFS/libp2p service and the BitTorrent DHT service.
func NewDualP2PService(cfg *config.Config, ipfsSvc *P2PService, btSvc *p2p_bt.BTDHTService) *DualP2PService {
	return &DualP2PService{
		IPFS: ipfsSvc,
		BT:   btSvc,
		cfg:  cfg,
	}
}

// Announce announces the given hash on both the IPFS/libp2p DHT and the
// BitTorrent DHT (if available).
func (d *DualP2PService) Announce(hash string) error {
	defer log.LogDuration("DualP2PService.Announce")()
	log.LogDebug("p2p-dual: Announce hash=%s", hash)

	d.mu.Lock()
	defer d.mu.Unlock()

	var lastErr error
	hasAny := false

	if d.IPFS != nil && d.IPFS.IsEnabled() {
		if err := d.IPFS.AnnounceHash(hash); err != nil {
			log.LogWarn("p2p-dual: announce on IPFS failed: %v", err)
			lastErr = err
		} else {
			hasAny = true
		}
	}

	if d.BT != nil {
		if err := d.BT.Announce(hash); err != nil {
			log.LogWarn("p2p-dual: announce on BT DHT failed: %v", err)
			lastErr = err
		} else {
			hasAny = true
		}
	}

	if !hasAny {
		if lastErr != nil {
			err := fmt.Errorf("dual announce failed on all networks: %w", lastErr)
			log.LogError("p2p-dual: %v", err)
			return err
		}
		err := fmt.Errorf("dual announce: no network available")
		log.LogError("p2p-dual: %v", err)
		return err
	}

	log.LogInfo("p2p-dual: announced %s on IPFS + BT DHT", hash)
	return nil
}

// FindProviders searches both the IPFS/libp2p DHT and the BitTorrent DHT for
// providers of the given hash and returns merged results.
func (d *DualP2PService) FindProviders(hash string) (*DualFindResult, error) {
	defer log.LogDuration("DualP2PService.FindProviders")()
	log.LogDebug("p2p-dual: FindProviders hash=%s", hash)

	result := &DualFindResult{}

	var wg sync.WaitGroup

	// Search IPFS DHT.
	if d.IPFS != nil && d.IPFS.IsEnabled() && d.IPFS.DHT != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			providers, err := d.IPFS.FindProviders(hash)
			if err != nil {
				log.LogWarn("p2p-dual: find on IPFS for %s: %v", hash, err)
				return
			}
			for _, pi := range providers {
				for _, a := range pi.Addrs {
					addr := a.String() + "/p2p/" + pi.ID.String()
					result.IPFSPeers = append(result.IPFSPeers, addr)
				}
				if len(pi.Addrs) == 0 {
					result.IPFSPeers = append(result.IPFSPeers, pi.ID.String())
				}
			}
		}()
	}

	// Search BT DHT.
	if d.BT != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			btPeers, err := d.BT.FindProviders(hash)
			if err != nil {
				log.LogWarn("p2p-dual: find on BT DHT for %s: %v", hash, err)
				return
			}
			result.BTPeers = btPeers
		}()
	}

	wg.Wait()

	log.LogInfo("p2p-dual: FindProviders for %s: %d IPFS peers, %d BT peers", hash, len(result.IPFSPeers), len(result.BTPeers))
	return result, nil
}

// FetchFile tries to download a file using the IPFS/libp2p network first
// (with all its peer discovery), and falls back to the BitTorrent DHT if
// IPFS did not yield a result.
func (d *DualP2PService) FetchFile(ctx context.Context, hash string) ([]byte, error) {
	defer log.LogDuration("DualP2PService.FetchFile")()
	log.LogDebug("p2p-dual: FetchFile hash=%s", hash)

	// Try IPFS first.
	if d.IPFS != nil && d.IPFS.IsEnabled() {
		data, err := d.IPFS.FetchFile(ctx, hash, nil)
		if err == nil {
			log.LogInfo("p2p-dual: got %s from IPFS", hash)
			return data, nil
		}
		log.LogWarn("p2p-dual: IPFS failed for %s: %v", hash, err)
	}

	// Fall back to BT DHT (HTTP-based fetch).
	if d.BT != nil {
		bridge := p2p_bt.NewBTBridge(d.BT, d.cfg.StorageDir)
		data, err := bridge.FetchFile(ctx, hash)
		if err == nil {
			log.LogInfo("p2p-dual: got %s from BT DHT fallback", hash)
			return data, nil
		}
		log.LogWarn("p2p-dual: BT DHT fallback failed for %s: %v", hash, err)
	}

	err := fmt.Errorf("could not fetch %s from any network", hash)
	log.LogError("p2p-dual: %v", err)
	return nil, err
}
