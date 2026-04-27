// Package p2p_bt implements BitTorrent Mainline DHT integration for Peerdrive.
// It wraps github.com/anacrolix/dht/v2 to announce and discover file providers
// over the BitTorrent DHT network.
package p2p_bt

import (
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"peerdrive/internal/log"

	dht "github.com/anacrolix/dht/v2"
)

// BTDHTService wraps a BitTorrent Mainline DHT server for announcing and
// discovering file hashes.
type BTDHTService struct {
	Server     *dht.Server
	listenAddr string
}

// NewBTDHT 创建 UDP DHT 服务器，从公共 BitTorrent 引导节点启动。
func NewBTDHT(listenAddr string) (*BTDHTService, error) {
	defer log.LogDuration("BTDHT.NewBTDHT")()
	log.LogDebug("bt-dht: NewBTDHT listenAddr=%s", listenAddr)

	cfg := dht.NewDefaultServerConfig()
	cfg.StartingNodes = func() ([]dht.Addr, error) {
		return dht.ResolveHostPorts([]string{
			"router.bittorrent.com:6881",
			"dht.transmissionbt.com:6881",
		})
	}

	if listenAddr != "" {
		udpAddr, err := net.ResolveUDPAddr("udp4", listenAddr)
		if err != nil {
			return nil, fmt.Errorf("resolve listen addr %s: %w", listenAddr, err)
		}
		conn, err := net.ListenUDP("udp4", udpAddr)
		if err != nil {
			return nil, fmt.Errorf("listen udp %s: %w", listenAddr, err)
		}
		cfg.Conn = conn
	}

	srv, err := dht.NewServer(cfg)
	if err != nil {
		log.LogError("bt-dht: NewBTDHT server creation failed: %v", err)
		return nil, fmt.Errorf("dht new server: %w", err)
	}

	// Bootstrap the routing table.
	bootstrapStats, err := srv.Bootstrap()
	if err != nil {
		log.LogWarn("bt-dht: bootstrap warning: %v", err)
	} else {
		log.LogInfo("bt-dht: bootstrap complete: %d nodes contacted", bootstrapStats.NumResponses)
	}

	// Allow a moment for the routing table to populate.
	time.Sleep(1 * time.Second)

	svc := &BTDHTService{
		Server:     srv,
		listenAddr: listenAddr,
	}

	log.LogInfo("bt-dht: server listening on %s (%d nodes)", srv.Addr().String(), srv.NumNodes())
	return svc, nil
}

// Announce 在 BitTorrent DHT 上 announce 指定的 SHA256 哈希（截取前 20 字节为 infohash）。
func (s *BTDHTService) Announce(hash string) error {
	defer log.LogDuration("BTDHT.Announce")()
	log.LogDebug("bt-dht: Announce hash=%s", hash)

	if s.Server == nil {
		err := fmt.Errorf("DHT server not available")
		log.LogError("bt-dht: Announce failed: %v", err)
		return err
	}
	ih, err := infoHashFromHex(hash)
	if err != nil {
		log.LogError("bt-dht: Announce invalid hash: %v", err)
		return err
	}
	var infoHash [20]byte
	copy(infoHash[:], ih[:20])

	// Extract our DHT port from the server's address.
	dhtPort := s.Server.Addr().(*net.UDPAddr).Port

	ann, err := s.Server.Announce(infoHash, dhtPort, false)
	if err != nil {
		log.LogError("bt-dht: Announce failed: %v", err)
		return fmt.Errorf("DHT announce: %w", err)
	}
	ann.Close()

	log.LogInfo("bt-dht: announced %s on BT DHT", hash)
	return nil
}

// FindProviders 在 BitTorrent DHT 上查找指定哈希的提供者，返回 "ip:port" 格式的地址列表。
func (s *BTDHTService) FindProviders(hash string) ([]string, error) {
	defer log.LogDuration("BTDHT.FindProviders")()
	log.LogDebug("bt-dht: FindProviders hash=%s", hash)

	if s.Server == nil {
		err := fmt.Errorf("DHT server not available")
		log.LogError("bt-dht: FindProviders failed: %v", err)
		return nil, err
	}
	ih, err := infoHashFromHex(hash)
	if err != nil {
		log.LogError("bt-dht: FindProviders invalid hash: %v", err)
		return nil, err
	}
	var infoHash [20]byte
	copy(infoHash[:], ih[:20])

	ann, err := s.Server.AnnounceTraversal(infoHash)
	if err != nil {
		log.LogError("bt-dht: FindProviders traversal failed: %v", err)
		return nil, fmt.Errorf("DHT find: %w", err)
	}
	defer ann.Close()

	seen := make(map[string]struct{})
	var peers []string

	timeout := time.After(15 * time.Second)

	// Collect peers until timeout or the traversal finishes.
	for {
		select {
		case pv, ok := <-ann.Peers:
			if !ok {
				log.LogInfo("bt-dht: FindProviders done for %s, found %d peers", hash, len(peers))
				return peers, nil
			}
			for _, p := range pv.Peers {
				addr := net.JoinHostPort(p.IP.String(), fmt.Sprint(p.Port))
				if _, ok := seen[addr]; !ok {
					seen[addr] = struct{}{}
					peers = append(peers, addr)
				}
			}
		case <-ann.Finished():
			log.LogInfo("bt-dht: FindProviders finished for %s, found %d peers", hash, len(peers))
			return peers, nil
		case <-timeout:
			log.LogInfo("bt-dht: FindProviders timeout for %s, found %d peers", hash, len(peers))
			return peers, nil
		}
	}
}

// NumNodes 返回 DHT 路由表中的节点数量。
func (s *BTDHTService) NumNodes() int {
	if s.Server == nil {
		return 0
	}
	return s.Server.NumNodes()
}

// Close 关闭 DHT 服务器。
func (s *BTDHTService) Close() error {
	if s.Server == nil {
		return nil
	}
	log.LogInfo("bt-dht: shutting down BT DHT server")
	s.Server.Close()
	return nil
}

// infoHashFromHex converts a 64-char hex SHA256 string into a 32-byte SHA256
// value, then returns the first 20 bytes suitable for a BitTorrent infohash.
func infoHashFromHex(hash string) ([]byte, error) {
	if len(hash) != 64 {
		return nil, fmt.Errorf("expected 64-char hex hash, got %d chars", len(hash))
	}
	raw, err := hex.DecodeString(hash)
	if err != nil {
		return nil, fmt.Errorf("decode hex: %w", err)
	}
	// SHA256 produces 32 bytes; BitTorrent uses 160-bit (20-byte) infohashes.
	return raw[:20], nil
}
