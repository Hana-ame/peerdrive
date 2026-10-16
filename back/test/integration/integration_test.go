//go:build integration

// Package integration Integration tests (optimization item 6, 2026-08-18, now offline by default):
//   - Signaling: TestMain starts a global self-hosted signalserver (httptest in-memory service);
//     all nodes point to it -- no longer depends on the public cloud 0.peerjs.com (no proxy/internet needed)
//   - Data plane: real WebRTC (same-machine dual pion host candidate direct connection, no STUN)
//   - Discovery: self-hosted /announce + /nodes API (replacing MQTT)
//   - External network tests separately gated:
//     - PEERDRIVE_MQTT_TEST=1 -> MQTT public broker discovery tests (mqtt_test.go)
//     - PEERDRIVE_LIVE_TEST=1  -> full online chain (live_test.go)
//
// Run (no external network needed, but -p 1 serial is mandatory: multiple test groups
// share the global signaling, and running in parallel causes interference):
//
//	cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1
package integration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hana-ame/go-peerserver"
	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

// selfHostedURL global self-hosted signaling + discovery server (started by TestMain, offline).
// Optimization item 6: integration tests no longer depend on public cloud signaling -- public
// signaling causes mutual interference when run in parallel, and requires proxy/internet.
// Self-hosted httptest is stable for local serial execution.
var selfHostedURL string

// TestMain starts the global self-hosted signaling server (PeerJS protocol + discovery API).
func TestMain(m *testing.M) {
	ss := signalserver.NewServer("testkey")
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/peerjs"):
			ss.HandleWS(w, r)
		case strings.HasSuffix(r.URL.Path, "/announce"):
			ss.HandleAnnounce(w, r)
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			ss.HandleNodes(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	selfHostedURL = hs.URL
	code := m.Run()
	hs.Close()
	os.Exit(code)
}

// splitHostPort splits host/port from an httptest URL.
func splitHostPort(url string) (string, string) {
	trimmed := strings.TrimPrefix(url, "http://")
	i := strings.LastIndex(trimmed, ":")
	return trimmed[:i], trimmed[i+1:]
}

// randID generates a unique node ID (avoids ID conflicts on public signaling).
func randID(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, randSuffix())
}

// randSuffix 4-byte random hex suffix.
func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeTestFile writes a content-addressed test file (storage/{h[:2]}/{h}), returns hash.
func writeTestFile(t *testing.T, storageDir string, content []byte) string {
	t.Helper()
	h := sha256Hex(content)
	dir := filepath.Join(storageDir, h[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, h), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newService constructs a service with PeerJS enabled (P2P/BT disabled for speed).
// peers: static peer list (if not set, only passive receive/discovery).
// Optimization item 6: signaling defaults to the global self-hosted server (selfHostedURL), offline;
// mqtt=true uses the public broker for discovery (requires PEERDRIVE_MQTT_TEST=1, see mqtt_test.go).
func newService(t *testing.T, id, storageDir string, mqtt bool, peers []string, collections ...string) *transport.PeerJSService {
	t.Helper()
	// File index etc. need repository.DB; each service in integration tests uses an independent in-memory DB
	// (InitDB re-Opens the global singleton -- prevents file_index rows left by previous tests
	// from polluting this test's sync/list assertions. Discovery background: verb tests ran in sequence and
	// sync had extra files).
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	cfg := config.Load()
	cfg.PeerJSEnable = true
	cfg.PeerJSID = id
	cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
	cfg.PeerJSSecure = false
	cfg.PeerJSKey = "testkey"
	cfg.BTDHTEnabled = false
	cfg.PeerJSPeers = join(peers)
	cfg.MQTTEnable = mqtt
	if mqtt {
		cfg.MQTTBroker = "tcp://broker.emqx.io:1883"
		cfg.MQTTCollections = join(collections)
	}
	// H2: create only allows files within the DownloadDir root; tests uniformly point the root to storageDir,
	// tests that need create write the source file into storageDir.
	cfg.DownloadDir = storageDir
	svc := transport.NewPeerJSService(cfg, storageDir)
	svc.Start()
	t.Cleanup(svc.Close)
	return svc
}

// waitConnections polls until connections to specified nodes are established.
// PEERDRIVE_SKIP_RTC=1 (no UDP sandbox, like docker default) skips interconnect tests --
// data plane WebRTC requires UDP; local WS/admin tests don't go through this function and are unaffected.
func waitConnections(t *testing.T, svc *transport.PeerJSService, want map[string]bool, timeout time.Duration) {
	t.Helper()
	if os.Getenv("PEERDRIVE_SKIP_RTC") == "1" {
		t.Skip("PEERDRIVE_SKIP_RTC=1: no UDP environment, skipping WebRTC interconnect tests")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conns := svc.Connections()
		ok := true
		for id, wantConn := range want {
			_, has := conns[id]
			if has != wantConn {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	got := svc.Connections()
	t.Fatalf("connection state not as expected want=%v got=%v", want, keys(got))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
