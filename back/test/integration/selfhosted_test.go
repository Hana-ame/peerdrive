//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// requireInitDB In-memory DB initialization (resets on each call, prevents cross-test contamination).
func requireInitDB(t *testing.T) {
	t.Helper()
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatal(err)
	}
}

// TestSelfHostedSignalAndDiscover Self-hosted signaling + built-in discovery full chain:
//   - Global self-hosted signaling server (PeerJS protocol + /discover API, see integration_test.go
//     TestMain, replacing 0.peerjs.com + MQTT)
//   - Two nodes point signaling to self-hosted, discovery via HTTP (no external services)
//   - B relies only on discovery to interconnect with A and pull files
//
// Discovery context: feature requirement — after self-hosting, PeerJS signaling and room discovery are fully self-managed.
func TestSelfHostedSignalAndDiscover(t *testing.T) {
	// Test file
	storageA := t.TempDir()
	content := []byte("self-hosted-signal-and-discover")
	hash := writeTestFile(t, storageA, content)

	idA := randID("sh-a")
	idB := randID("sh-b")

	// newService points to global self-hosted signaling; here we manually construct to point to self-hosted discovery API
	newSelfHosted := func(id, storage string) *transport.PeerJSService {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = selfHostedURL
		cfg.MQTTCollections = hash
		svc := transport.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	newSelfHosted(idA, storageA) // svcA: file source (no direct reference needed)
	svcB := newSelfHosted(idB, t.TempDir())

	// B has no static PEERS — relies on self-hosted discovery to interconnect with A
	waitConnections(t, svcB, map[string]bool{idA: true}, 60*time.Second)

	data, err := svcB.FetchFromPeer(idA, hash, 0, -1)
	if err != nil {
		t.Fatalf("pull failed after self-hosted discovery: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("content mismatch: got %q", data)
	}
}

// TestInterconnectViaPresenceRoom Two nodes with zero shared content rooms interconnect via "presence room".
// Discovery context (interconnect layer): Discovery was originally content-sharded — nodes only announce/query
// collection hash rooms they care about. Thus **two nodes without shared collections can never see each other**:
// under default configuration, PEERDRIVE_MQTT_COLLECTIONS is empty → neither announce nor query any rooms,
// discovery completely spins idle, only static PEERDRIVE_PEERJS_PEERS can interconnect.
// Fix: HTTP discovery additionally joins a fixed presence room (transport.PresenceRoom), making the interconnect layer
// independent of content sharding. This test deliberately sets no shared collections, only relying on presence rooms for interconnect.
func TestInterconnectViaPresenceRoom(t *testing.T) {
	idA := randID("pr-a")
	idB := randID("pr-b")

	newPresenceNode := func(id string) *transport.PeerJSService {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = selfHostedURL
		cfg.DiscoverPresence = true
		cfg.MQTTCollections = "" // Deliberately no shared content rooms: only verifying node-level interconnect
		svc := transport.NewPeerJSService(cfg, t.TempDir())
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	newPresenceNode(idA) // Only as a peer, no explicit reference needed
	svcB := newPresenceNode(idB)

	waitConnections(t, svcB, map[string]bool{idA: true}, 60*time.Second)
}

// TestSelfHostedPeerJSSignal Self-hosted signaling protocol compatibility: directly using peerjs client module
// to connect to self-hosted server and complete WebRTC data plane interoperability (protocol identical to public cloud).
// Discovery context: feature requirement — node side requires zero changes (only change host config) to switch to self-hosted.
func TestSelfHostedPeerJSSignal(t *testing.T) {
	host, port := splitHostPort(selfHostedURL)
	// Two peerjs clients connect to self-hosted (protocol compatibility verification)
	pA := peerjs.NewPeer("sp-a", peerjsOptions(host, port))
	pB := peerjs.NewPeer("sp-b", peerjsOptions(host, port))

	done := make(chan string, 1)
	pB.OnConnection(func(c *peerjs.Connection) {
		c.OnMessage(func(f peerjs.Frame) {
			done <- string(f.Data)
		})
	})
	require.NoError(t, pA.Dial(context.Background()))
	require.NoError(t, pB.Dial(context.Background()))
	defer pA.Close()
	defer pB.Close()

	conn, err := pA.Connect(context.Background(), "sp-b", "test")
	require.NoError(t, err)
	conn.OnOpen(func(c *peerjs.Connection) {
		_ = c.SendText("hello-self-hosted")
	})
	select {
	case got := <-done:
		if got != "hello-self-hosted" {
			t.Fatalf("content mismatch: %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("self-hosted signaling data plane not reachable")
	}
}

// TestNodeMarketListsDiscoveredPeer Node market (doc/NETDISK.md M1) end-to-end:
// Two nodes with zero shared collections discover each other via presence rooms,
// B's market list should show A, and A should be marked as online; after joining A, it should be in the joined list.
//
// Discovery context (netdisk target): Users want "there are other people's nodes, can join nodes from the market."
// Market data source = discovery server's /discover/nodes (empty coll = all online nodes) ∪
// local joined list. This test also covers the prerequisite that "empty coll query on self-hosted signaling actually returns nodes"
// (if not true, the market page would silently show an empty list).
func TestNodeMarketListsDiscoveredPeer(t *testing.T) {
	idA := randID("mk-a")
	idB := randID("mk-b")

	newPresenceNode := func(id string) *transport.PeerJSService {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = selfHostedURL
		cfg.DiscoverPresence = true
		cfg.MQTTCollections = ""
		svc := transport.NewPeerJSService(cfg, t.TempDir())
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	newPresenceNode(idA)
	svcB := newPresenceNode(idB)

	dir := service.NewNodeDirectory(t.TempDir(), selfHostedURL)
	dir.SetSelfID(svcB.ID)
	dir.SetConnected(svcB.ConnectedPeerIDs)

	// announce heartbeat every 30s, but HTTPDiscovery.loop immediately announces once at startup;
	// here we wait for B to discover A (interconnect success) before querying the market, avoiding waiting for heartbeat.
	waitConnections(t, svcB, map[string]bool{idA: true}, 60*time.Second)

	var found *model.NodeSummary
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range dir.Market(context.Background()) {
			if n.PeerID == idA {
				cp := n
				found = &cp
				break
			}
		}
		if found != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if found == nil {
		t.Fatalf("peer %s not found in market list", idA)
	}
	if !found.Online {
		t.Fatalf("peer %s should be online: %+v", idA, *found)
	}
	if !found.Connected {
		t.Fatalf("peer %s is directly connected but not marked connected: %+v", idA, *found)
	}
	// Self should not appear in market list
	for _, n := range dir.Market(context.Background()) {
		if n.PeerID == idB {
			t.Fatal("this node should not appear in market node list")
		}
	}
	// After joining, should be persisted and queryable
	if err := dir.Join(idA); err != nil {
		t.Fatalf("join %s: %v", idA, err)
	}
	joined := dir.Joined(context.Background())
	if len(joined) != 1 || joined[0].PeerID != idA || !joined[0].Joined {
		t.Fatalf("joined list mismatch: %+v", joined)
	}
}

// peerjsOptions Client configuration pointing to self-hosted server.
func peerjsOptions(host, port string) peerjs.Options {
	opts := peerjs.DefaultOptions()
	opts.Host = host
	opts.Port = port
	opts.Secure = false
	opts.Key = "testkey"
	return opts
}

// TestStartClose_RacePressure Loop Start/Close race: under -race verifies startLoop
// room discovery component read/write (peerMu snapshot) and Close cleanup have no race, no goroutine
// leak after Close (ctx.Err() guard).
//
// Discovery context: 2026-08-18 -race integration test continuous runs exposed — Close (holding peerMu setting nil
// httpDisc/discovery) vs startLoop lock-free snapshot read race; after fix (peerMu + ctx
// guard), this test serves as a pressure regression. sleep 50ms lets startLoop complete discovery path before Close,
// hitting the race window.
func TestStartClose_RacePressure(t *testing.T) {
	for i := 0; i < 30; i++ {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = randID("rc")
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = selfHostedURL
		svc := transport.NewPeerJSService(cfg, t.TempDir())
		svc.Start()
		time.Sleep(50 * time.Millisecond)
		svc.Close()
	}
}
