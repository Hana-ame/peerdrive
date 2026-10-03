//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

// Live deployment verification: full test flow using peersignal.moonchan.xyz (cloudcone self-hosted signaling + discovery) as
// the signaling server. Run: PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v
// Note: cloudcone is directly reachable (bypasses host proxy); do not set HTTPS_PROXY to a proxy when running tests.
const (
	liveSignalHost = "peersignal.moonchan.xyz"
	liveSignalKey  = "pd-signal-b9447b406828e500"
	liveDiscover   = "https://peersignal.moonchan.xyz"
)

// TestLiveSignal_DiscoveryAndInterop Full chain: live signaling + HTTP discovery + WebRTC file pull.
// Discovery context: deployment verification — after self-hosted server goes online, nodes require zero changes (only change host/key/discover)
// to interconnect via live signaling and pull files.
func TestLiveSignal_DiscoveryAndInterop(t *testing.T) {
	if os.Getenv("PEERDRIVE_LIVE_TEST") != "1" {
		t.Skip("Live test requires PEERDRIVE_LIVE_TEST=1 (registers nodes to peersignal.moonchan.xyz)")
	}

	storageA := t.TempDir()
	content := []byte("live-signal-test-payload")
	hash := writeTestFile(t, storageA, content)

	idA := randID("live-a")
	idB := randID("live-b")

	newLive := func(id, storage string) *transport.PeerJSService {
		if err := repository.InitDB(":memory:"); err != nil {
			t.Fatal(err)
		}
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost = liveSignalHost
		cfg.PeerJSPort = "443"
		cfg.PeerJSSecure = true
		cfg.PeerJSKey = liveSignalKey
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = liveDiscover
		cfg.MQTTCollections = hash
		svc := transport.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	newLive(idA, storageA) // File source
	svcB := newLive(idB, t.TempDir())

	// B has no static PEERS — relies on live discovery to interconnect with A
	waitConnections(t, svcB, map[string]bool{idA: true}, 90*time.Second)

	data, err := svcB.FetchFromPeer(idA, hash, 0, -1)
	if err != nil {
		t.Fatalf("Live signaling fetch failed: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("Content mismatch: got %q", data)
	}
}

// TestLiveSignal_ProtocolCompat Live signaling protocol compatibility: peerjs client module
// directly connects to live to achieve WebRTC data plane interoperability (verifies wss deployment + zero protocol difference).
//
// Discovery context: feature acceptance — live deployment protocol compatibility: peerjs client directly connects to live wss data plane
func TestLiveSignal_ProtocolCompat(t *testing.T) {
	if os.Getenv("PEERDRIVE_LIVE_TEST") != "1" {
		t.Skip("Live test requires PEERDRIVE_LIVE_TEST=1")
	}

	pA := peerjs.NewPeer("live-pc-a-"+randSuffix(), liveOptions())
	pB := peerjs.NewPeer("live-pc-b-"+randSuffix(), liveOptions())

	done := make(chan string, 1)
	pB.OnConnection(func(c *peerjs.Connection) {
		c.OnMessage(func(f peerjs.Frame) {
			done <- string(f.Data)
		})
	})
	if err := pA.Dial(context.Background()); err != nil {
		t.Fatalf("A dial: %v", err)
	}
	if err := pB.Dial(context.Background()); err != nil {
		t.Fatalf("B dial: %v", err)
	}
	defer pA.Close()
	defer pB.Close()

	conn, err := pA.Connect(context.Background(), pB.ID(), "live")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	conn.OnOpen(func(c *peerjs.Connection) {
		_ = c.SendText("hello-live")
	})
	select {
	case got := <-done:
		if got != "hello-live" {
			t.Fatalf("Content mismatch: %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Live signaling data plane not connected")
	}
}

// liveOptions Live signaling client configuration.
func liveOptions() peerjs.Options {
	opts := peerjs.DefaultOptions()
	opts.Host = liveSignalHost
	opts.Port = "443"
	opts.Secure = true
	opts.Key = liveSignalKey
	return opts
}
