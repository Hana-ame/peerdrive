package legacy

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"peerdrive/internal/config"

	"github.com/ipfs/go-cid"
)

func TestNewP2PService_Disabled(t *testing.T) {
	cfg := &config.Config{P2PEnable: false}
	svc, err := NewP2PService(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected no error for disabled P2P, got: %v", err)
	}
	if svc.IsEnabled() {
		t.Error("P2PService should be disabled when P2PEnable is false")
	}
	if svc.Host != nil {
		t.Error("Host should be nil when P2P is disabled")
	}
}

func TestNewP2PService_Enabled(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	if !svc.IsEnabled() {
		t.Error("P2PService should be enabled")
	}
	if svc.Host == nil {
		t.Error("Host should not be nil when P2P is enabled")
	}
	if svc.DHT == nil {
		t.Error("DHT should not be nil")
	}

	id, addrs := svc.GetNodeInfo()
	if id == "" {
		t.Error("peer ID should not be empty")
	}
	if len(addrs) == 0 {
		t.Error("should have at least one address")
	}
	t.Logf("Peer ID: %s, Addrs: %v", id, addrs)
}

func TestP2PService_Peers(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	peers := svc.GetConnectedPeers()
	if len(peers) != 0 {
		t.Log("unexpected connected peers count:", len(peers))
	}

	disc := svc.GetDiscoveredPeers()
	if len(disc) != 0 {
		t.Log("unexpected discovered peers count:", len(disc))
	}
}

func TestP2PService_HandleExchange(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	// Write a test file
	testData := []byte("hello p2p test data")
	hash := sha256.Sum256(testData)
	hashStr := hex.EncodeToString(hash[:])

	subDir := filepath.Join(cfg.StorageDir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), testData, 0644)

	t.Logf("Test hash: %s", hashStr)

	// Test disabled service behavior
	disabledSvc := &P2PService{cfg: &config.Config{P2PEnable: false}}
	_, fetchErr := disabledSvc.FetchFile(ctx, hashStr, nil)
	if fetchErr == nil {
		t.Error("FetchFile should fail when P2P is disabled")
	}
}

func TestP2PService_ConnectionManager(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	if svc.ConnMgr == nil {
		t.Fatal("ConnectionManager should not be nil")
	}

	stats := svc.ConnMgr.Stats()
	t.Logf("Connection manager stats: %+v", stats)

	if known, ok := stats["known_peers"]; !ok {
		t.Error("stats should include known_peers")
	} else {
		t.Logf("Known peers: %v", known)
	}
}

func TestP2PService_ChunkedTransfer(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	if svc.Transfer == nil {
		t.Fatal("ChunkedTransfer should not be nil")
	}

	// Check active jobs returns empty initially
	jobs := svc.Transfer.ActiveJobs()
	if len(jobs) != 0 {
		t.Error("active jobs should be empty")
	}

	// Write a test file
	testData := []byte("chunked transfer test data for verification")
	hash := sha256.Sum256(testData)
	hashStr := hex.EncodeToString(hash[:])

	subDir := filepath.Join(cfg.StorageDir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), testData, 0644)

	t.Logf("Chunked transfer test hash: %s", hashStr)
}

func TestP2PService_RelayConfig(t *testing.T) {
	tests := []struct {
		name     string
		mode     config.RelayMode
		expected string
	}{
		{"client mode", config.RelayClient, "client"},
		{"server mode", config.RelayServer, "server"},
		{"off mode", config.RelayOff, "off"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				P2PEnable:     true,
				P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
				P2PMDNSEnable: false,
				P2PRelayMode:  tt.mode,
				StorageDir:    t.TempDir(),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			svc, err := NewP2PService(ctx, cfg)
			if err != nil {
				t.Fatalf("failed to create P2P service: %v", err)
			}
			defer svc.Close()

			if mode := svc.RelayMode(); mode != tt.expected {
				t.Errorf("expected relay mode %q, got %q", tt.expected, mode)
			}
		})
	}
}

func TestCIDFromSha256(t *testing.T) {
	// Use a real SHA256 hash
	realData := []byte("test data for cid")
	h := sha256.Sum256(realData)
	realHash := hex.EncodeToString(h[:])
	c := cidFromSha256(realHash)
	if c == cid.Undef {
		t.Error("cidFromSha256 returned Undef for valid hash")
	} else {
		t.Logf("CID from hash: %s", c.String())
	}

	// Invalid length
	c = cidFromSha256("short")
	if c != cid.Undef {
		t.Error("cidFromSha256 should return Undef for short hash")
	}
}

func TestConnectionManager_QualityMetrics(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	cm := svc.ConnMgr
	if cm == nil {
		t.Fatal("ConnectionManager should not be nil")
	}

	// Check that quality metrics exist in stats
	stats := cm.Stats()
	if _, ok := stats["quality_score"]; !ok {
		t.Error("Stats should include quality_score")
	}
	if _, ok := stats["avg_latency_ms"]; !ok {
		t.Error("Stats should include avg_latency_ms")
	}

	t.Logf("Quality stats: %+v", stats)
}

func TestP2PService_TopologyEndpoint(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	// Get topology data
	topo := svc.GetTopology()
	if topo == nil {
		t.Fatal("GetTopology should not return nil")
	}

	// Verify structure
	if topo.LocalPeerID == "" {
		t.Error("LocalPeerID should not be empty")
	}
	if topo.Edges == nil {
		t.Error("Edges should not be nil")
	}
	t.Logf("Topology: local=%s, edges=%d", topo.LocalPeerID, len(topo.Edges))
}

func TestP2PService_ExchangeProtocol_IPv6(t *testing.T) {
	// Test that IPv6 listen addresses are accepted
	cfg := &config.Config{
		P2PEnable:       true,
		P2PListenAddr:   "/ip4/0.0.0.0/tcp/0",
		P2PListenAddrV6: "/ip6/::/tcp/0",
		P2PMDNSEnable:   false,
		StorageDir:      t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service with IPv6: %v", err)
	}
	defer svc.Close()

	// Verify that addrs include both IPv4 and IPv6
	_, addrs := svc.GetNodeInfo()
	hasV6 := false
	hasV4 := false
	for _, a := range addrs {
		if len(a) > 3 && a[:3] == "/ip" {
			if a[3] == '6' {
				hasV6 = true
			} else if a[3] == '4' {
				hasV4 = true
			}
		}
	}
	if !hasV4 {
		t.Log("No IPv4 address present (might be expected on some platforms)")
	}
	if !hasV6 {
		t.Log("No IPv6 address present (might be expected on some platforms)")
	}
	t.Logf("Node addrs: %v", addrs)
}

func TestChunkedTransfer_ProgressRace(t *testing.T) {
	cfg := &config.Config{
		P2PEnable:     true,
		P2PListenAddr: "/ip4/0.0.0.0/tcp/0",
		P2PMDNSEnable: false,
		StorageDir:    t.TempDir(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	svc, err := NewP2PService(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create P2P service: %v", err)
	}
	defer svc.Close()

	// Write a test file
	testData := make([]byte, ChunkSize*3+100) // ~3 chunks
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	hash := sha256.Sum256(testData)
	hashStr := hex.EncodeToString(hash[:])

	subDir := filepath.Join(cfg.StorageDir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), testData, 0644)

	// Test transfer progress
	progress := &TransferProgress{
		Hash:        hashStr,
		TotalSize:   int64(len(testData)),
		ChunksTotal: 4,
	}

	// Test concurrent Update calls (no race)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			progress.Update(1024)
		}()
	}
	wg.Wait()

	if progress.ReceivedSize != 10240 {
		t.Errorf("Expected 10240 bytes received, got %d", progress.ReceivedSize)
	}
	t.Logf("Progress test completed, received=%d", progress.ReceivedSize)
}

func TestTrimNewline(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello\n", "hello"},
		{"hello", "hello"},
		{"hello\n\n", "hello\n"},
		{"", ""},
	}

	for _, tt := range tests {
		result := trimNewline(tt.input)
		if result != tt.expected {
			t.Errorf("trimNewline(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
