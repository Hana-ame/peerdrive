// Package p2p_bt implements BitTorrent Mainline DHT integration for Peerdrive.
// It wraps github.com/anacrolix/dht/v2 to announce and discover file providers
// over the BitTorrent DHT network.
package p2p_bt

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"time"

	dht "github.com/anacrolix/dht/v2"
)

// BTDHTService wraps a BitTorrent Mainline DHT server for announcing and
// discovering file hashes.
type BTDHTService struct {
	Server     *dht.Server
	listenAddr string
}

// NewBTDHT creates a UDP DHT server on the given listenAddr, bootstraps from
// public BitTorrent bootstrap nodes and returns a ready-to-use BTDHTService.
// If listenAddr is empty, a random port is used.
func NewBTDHT(listenAddr string) (*BTDHTService, error) {
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
		return nil, fmt.Errorf("dht new server: %w", err)
	}

	// Bootstrap the routing table.
	bootstrapStats, err := srv.Bootstrap()
	if err != nil {
		logf("DHT bootstrap warning: %v", err)
	} else {
		logf("DHT bootstrap complete: %d nodes contacted", bootstrapStats.NumResponses)
	}

	// Allow a moment for the routing table to populate.
	time.Sleep(1 * time.Second)

	svc := &BTDHTService{
		Server:     srv,
		listenAddr: listenAddr,
	}

	logf("BT DHT server listening on %s (%d nodes)", srv.Addr().String(), srv.NumNodes())
	return svc, nil
}

// Announce announces the given 64-char hex SHA256 hash on the BitTorrent DHT.
// The hash is truncated to the first 20 bytes for the 160-bit infohash.
// The listen port is automatically used for the announce.
func (s *BTDHTService) Announce(hash string) error {
	if s.Server == nil {
		return fmt.Errorf("DHT server not available")
	}
	ih, err := infoHashFromHex(hash)
	if err != nil {
		return err
	}
	var infoHash [20]byte
	copy(infoHash[:], ih[:20])

	// Extract our DHT port from the server's address.
	dhtPort := s.Server.Addr().(*net.UDPAddr).Port

	ann, err := s.Server.Announce(infoHash, dhtPort, false)
	if err != nil {
		return fmt.Errorf("DHT announce: %w", err)
	}
	ann.Close()

	logf("announced %s on BT DHT", hash)
	return nil
}

// FindProviders looks up providers for the given hash on the BitTorrent DHT
// and returns peer addresses as "ip:port" strings.
func (s *BTDHTService) FindProviders(hash string) ([]string, error) {
	if s.Server == nil {
		return nil, fmt.Errorf("DHT server not available")
	}
	ih, err := infoHashFromHex(hash)
	if err != nil {
		return nil, err
	}
	var infoHash [20]byte
	copy(infoHash[:], ih[:20])

	ann, err := s.Server.AnnounceTraversal(infoHash)
	if err != nil {
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
			return peers, nil
		case <-timeout:
			return peers, nil
		}
	}
}

// NumNodes returns the number of nodes in the DHT routing table.
func (s *BTDHTService) NumNodes() int {
	if s.Server == nil {
		return 0
	}
	return s.Server.NumNodes()
}

// Close shuts down the DHT server.
func (s *BTDHTService) Close() error {
	if s.Server == nil {
		return nil
	}
	logf("shutting down BT DHT server")
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

func logf(format string, args ...interface{}) {
	full := fmt.Sprintf("[bt-dht] "+format, args...)
	os.Stderr.WriteString(full + "\n")
}
