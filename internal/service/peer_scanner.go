// PeerScanner 后台主动扫描并连接 P2P 对端，支持 DHT 扫描、注册服务器扫描、LAN（mDNS）扫描和引导节点维护。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	dhtScanInterval       = 60 * time.Second
	regScanInterval       = 120 * time.Second
	lanScanInterval       = 30 * time.Second
	bootstrapScanInterval = 300 * time.Second
	scanConnectTimeout    = 15 * time.Second
)

// PeerScanner proactively discovers and connects to peers via DHT, a
// registration server, LAN (mDNS), and bootstrap reconnection.
type PeerScanner struct {
	svc     *P2PService
	tracker *PeerTracker
	regURL  string

	mu           sync.Mutex
	active       bool
	stopCh       chan struct{}
	wg           sync.WaitGroup
	lastScanTime map[string]time.Time
}

// NewPeerScanner creates a new PeerScanner.
func NewPeerScanner(svc *P2PService, tracker *PeerTracker, regURL string) *PeerScanner {
	return &PeerScanner{
		svc:          svc,
		tracker:      tracker,
		regURL:       regURL,
		lastScanTime: make(map[string]time.Time),
	}
}

// Start launches the background scanner goroutines.
func (ps *PeerScanner) Start() {
	ps.mu.Lock()
	if ps.active {
		ps.mu.Unlock()
		return
	}
	ps.active = true
	ps.stopCh = make(chan struct{})
	ps.mu.Unlock()

	log.LogInfo("peer-scanner: starting scanners (dht=%s, reg=%s, lan=%s, bootstrap=%s)",
		dhtScanInterval, regScanInterval, lanScanInterval, bootstrapScanInterval)

	ps.wg.Add(4)
	go ps.runDHTScanner()
	go ps.runRegServerScanner()
	go ps.runLANScanner()
	go ps.runBootstrapMaintainer()
}

// Stop signals all scanner goroutines to shut down and waits for them.
func (ps *PeerScanner) Stop() {
	ps.mu.Lock()
	if !ps.active {
		ps.mu.Unlock()
		return
	}
	ps.active = false
	close(ps.stopCh)
	ps.mu.Unlock()

	ps.wg.Wait()
	log.LogInfo("peer-scanner: all scanners stopped")
}

// ActiveScanners returns the list of active scanner names.
func (ps *PeerScanner) ActiveScanners() []string {
	return []string{"dht", "reg_server", "lan", "bootstrap"}
}

// LastScanTimes returns a map of scanner name to last scan time (RFC3339).
func (ps *PeerScanner) LastScanTimes() map[string]string {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	result := make(map[string]string, len(ps.lastScanTime))
	for k, v := range ps.lastScanTime {
		if !v.IsZero() {
			result[k] = v.Format(time.RFC3339)
		} else {
			result[k] = ""
		}
	}
	return result
}

func (ps *PeerScanner) markScanTime(name string) {
	ps.mu.Lock()
	ps.lastScanTime[name] = time.Now()
	ps.mu.Unlock()
}

func (ps *PeerScanner) shouldStop() bool {
	select {
	case <-ps.stopCh:
		return true
	default:
		return false
	}
}

func (ps *PeerScanner) isPeerConnected(id peer.ID) bool {
	if ps.svc.Host == nil {
		return false
	}
	return ps.svc.Host.Network().Connectedness(id) == network.Connected
}

func (ps *PeerScanner) connectToPeer(info peer.AddrInfo) {
	if info.ID == ps.svc.Host.ID() {
		return
	}
	if ps.isPeerConnected(info.ID) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), scanConnectTimeout)
	defer cancel()

	if err := ps.svc.Host.Connect(ctx, info); err != nil {
		log.LogWarn("peer-scanner: connect to %s failed: %v", info.ID.String(), err)
		return
	}

	addrStrs := make([]string, len(info.Addrs))
	for i, a := range info.Addrs {
		addrStrs[i] = a.String()
	}
	ps.tracker.RecordConnection(info.ID.String(), addrStrs, "")
	ps.tracker.SetDirection(info.ID.String(), "outbound")
	log.LogInfo("peer-scanner: connected to %s (outbound)", info.ID.String())
}

// --------------------------------------------------------------------------
// 1. DHT Scanner — for each file hash in storage, find providers and connect.
// --------------------------------------------------------------------------

func (ps *PeerScanner) runDHTScanner() {
	defer ps.wg.Done()
	log.LogDebug("peer-scanner: DHT scanner started")

	// Run once immediately, then on tick.
	ps.scanDHT()

	ticker := time.NewTicker(dhtScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ps.stopCh:
			log.LogDebug("peer-scanner: DHT scanner stopped")
			return
		case <-ticker.C:
			ps.scanDHT()
		}
	}
}

func (ps *PeerScanner) scanDHT() {
	defer ps.markScanTime("dht")
	log.LogDebug("peer-scanner: DHT scan starting")

	if ps.svc.DHT == nil || !ps.svc.IsEnabled() {
		return
	}

	hashes := ps.collectStorageHashes()
	log.LogDebug("peer-scanner: DHT scan checking %d hashes", len(hashes))

	for _, hash := range hashes {
		if ps.shouldStop() {
			return
		}

		providers, err := ps.svc.FindProviders(hash)
		if err != nil {
			log.LogWarn("peer-scanner: DHT FindProviders for %s failed: %v", hash[:16], err)
			continue
		}

		for _, pi := range providers {
			if ps.shouldStop() {
				return
			}
			ps.connectToPeer(pi)
		}
	}

	log.LogInfo("peer-scanner: DHT scan completed (%d hashes)", len(hashes))
}

// collectStorageHashes walks the storage directory and returns all 64-char
// hex file hashes found (the layout is storageDir/{prefix 2 chars}/{hash}).
func (ps *PeerScanner) collectStorageHashes() []string {
	storageDir := ps.svc.storageDir
	entries, err := os.ReadDir(storageDir)
	if err != nil {
		log.LogWarn("peer-scanner: storage dir %s not readable: %v", storageDir, err)
		return nil
	}

	var hashes []string
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 2 {
			continue
		}
		subDir := filepath.Join(storageDir, entry.Name())
		subEntries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() && len(sub.Name()) == 64 {
				hashes = append(hashes, sub.Name())
			}
		}
	}
	return hashes
}

// --------------------------------------------------------------------------
// 2. Registration Server Scanner — GET /auth/list and connect to known peers.
// --------------------------------------------------------------------------

func (ps *PeerScanner) runRegServerScanner() {
	defer ps.wg.Done()
	log.LogDebug("peer-scanner: registration server scanner started")

	if ps.regURL == "" {
		log.LogDebug("peer-scanner: no reg server URL, scanner idle")
		return
	}

	// Run once immediately.
	ps.scanRegServer()

	ticker := time.NewTicker(regScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ps.stopCh:
			log.LogDebug("peer-scanner: registration server scanner stopped")
			return
		case <-ticker.C:
			ps.scanRegServer()
		}
	}
}

type regServerPeer struct {
	ID    string   `json:"peer_id"`
	Addrs []string `json:"addrs"`
}

type regServerListResponse struct {
	Peers []regServerPeer `json:"peers"`
}

func (ps *PeerScanner) scanRegServer() {
	defer ps.markScanTime("reg_server")
	log.LogDebug("peer-scanner: registration server scan starting for %s", ps.regURL)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(ps.regURL + "/auth/list")
	if err != nil {
		log.LogWarn("peer-scanner: reg server list request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.LogWarn("peer-scanner: reg server list read failed: %v", err)
		return
	}

	var listResp regServerListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		log.LogWarn("peer-scanner: reg server list parse failed: %v", err)
		return
	}

	for _, p := range listResp.Peers {
		if ps.shouldStop() {
			return
		}

		pid, err := peer.Decode(p.ID)
		if err != nil {
			log.LogWarn("peer-scanner: reg server invalid peer id %s: %v", p.ID, err)
			continue
		}

		if pid == ps.svc.Host.ID() {
			continue
		}

		for _, addrStr := range p.Addrs {
			if ps.shouldStop() {
				return
			}
			maddr, err := multiaddrFromString(addrStr)
			if err != nil {
				continue
			}
			info, err := peer.AddrInfoFromP2pAddr(maddr)
			if err != nil {
				continue
			}
			ps.connectToPeer(*info)
		}
	}

	log.LogInfo("peer-scanner: registration server scan completed (%d peers from reg server)", len(listResp.Peers))
}

// --------------------------------------------------------------------------
// 3. LAN Scanner — connect to mDNS-discovered peers that aren't connected.
// --------------------------------------------------------------------------

func (ps *PeerScanner) runLANScanner() {
	defer ps.wg.Done()
	log.LogDebug("peer-scanner: LAN scanner started")

	if ps.svc.cfg == nil || !ps.svc.cfg.P2PMDNSEnable {
		log.LogDebug("peer-scanner: mDNS not enabled, LAN scanner idle")
		return
	}

	// Run once immediately.
	ps.scanLAN()

	ticker := time.NewTicker(lanScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ps.stopCh:
			log.LogDebug("peer-scanner: LAN scanner stopped")
			return
		case <-ticker.C:
			ps.scanLAN()
		}
	}
}

func (ps *PeerScanner) scanLAN() {
	defer ps.markScanTime("lan")
	log.LogDebug("peer-scanner: LAN scan starting")

	discovered := ps.svc.GetDiscoveredPeers()
	connected := 0

	for _, info := range discovered {
		if ps.shouldStop() {
			return
		}

		if info.ID == ps.svc.Host.ID() {
			continue
		}
		if ps.isPeerConnected(info.ID) {
			connected++
			continue
		}

		ps.connectToPeer(info)
	}

	log.LogDebug("peer-scanner: LAN scan completed (%d discovered, %d already connected)", len(discovered), connected)
}

// --------------------------------------------------------------------------
// 4. Bootstrap Maintainer — re-connect to bootstrap peers if disconnected.
// --------------------------------------------------------------------------

func (ps *PeerScanner) runBootstrapMaintainer() {
	defer ps.wg.Done()
	log.LogDebug("peer-scanner: bootstrap maintainer started")

	// Run once immediately.
	ps.maintainBootstrap()

	ticker := time.NewTicker(bootstrapScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ps.stopCh:
			log.LogDebug("peer-scanner: bootstrap maintainer stopped")
			return
		case <-ticker.C:
			ps.maintainBootstrap()
		}
	}
}

func (ps *PeerScanner) maintainBootstrap() {
	defer ps.markScanTime("bootstrap")
	log.LogDebug("peer-scanner: bootstrap maintenance starting")

	if !ps.svc.IsEnabled() {
		return
	}

	bootstrapAddr := ps.svc.cfg.P2PBootstrapPeer
	if bootstrapAddr == "" {
		return
	}

	if ps.shouldStop() {
		return
	}

	info, err := parsePeerAddr(bootstrapAddr)
	if err != nil {
		log.LogWarn("peer-scanner: bootstrap parse failed: %v", err)
		return
	}

	if ps.isPeerConnected(info.ID) {
		log.LogDebug("peer-scanner: bootstrap peer %s already connected", info.ID.String())
		return
	}

	log.LogInfo("peer-scanner: reconnecting to bootstrap peer %s", info.ID.String())
	ps.connectToPeer(*info)
}

// String returns a human-readable representation of the scanner state.
func (ps *PeerScanner) String() string {
	return fmt.Sprintf("PeerScanner(regURL=%q, active=%v)", ps.regURL, ps.active)
}
