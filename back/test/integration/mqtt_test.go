//go:build integration

package integration

import (
	"os"
	"testing"
	"time"

	"peerdrive/internal/transport"
)

// requireMQTTEnv External network test gate (Optimization 2 of 6, 2026-08-18): MQTT public broker
// tests require external network + proxy; default integration tests have already moved to self-hosted signaling
// — this adds an explicit PEERDRIVE_MQTT_TEST=1 gate to avoid full test suite failures in offline environments.
func requireMQTTEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("PEERDRIVE_MQTT_TEST") != "1" {
		t.Skip("MQTT public broker test requires PEERDRIVE_MQTT_TEST=1 (external network)")
	}
}

// TestMQTTDiscovery Two nodes discover each other via MQTT sharded rooms (no static configuration at all).
// Covers: announce publishing, sharded topic subscription, onPeer callback, heartbeat idempotency.
//
// Discovery context: feature test — MQTT sharded room mutual discovery (announce/subscription/heartbeat fallback)
func TestMQTTDiscovery(t *testing.T) {
	requireMQTTEnv(t)
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	gotA := make(chan string, 4)
	gotB := make(chan string, 4)
	discA := transport.NewMQTTDiscovery("tcp://broker.emqx.io:1883", "peerdrive/v1/test", "pd-disc-a-"+randSuffix(), func(id string) { gotA <- id })
	discB := transport.NewMQTTDiscovery("tcp://broker.emqx.io:1883", "peerdrive/v1/test", "pd-disc-b-"+randSuffix(), func(id string) { gotB <- id })

	idA := "mqtt-node-a-" + randSuffix()
	idB := "mqtt-node-b-" + randSuffix()

	discA.Start([]string{hash})
	discB.Start([]string{hash})
	defer discA.Stop()
	defer discB.Stop()

	discA.Announce(idA, []string{hash})
	discB.Announce(idB, []string{hash})

	// A should receive B's announce, and B should receive A's
	waitFor := func(ch chan string, want string, timeout time.Duration) {
		t.Helper()
		deadline := time.After(timeout)
		for {
			select {
			case got := <-ch:
				if got == want {
					return
				}
			case <-deadline:
				t.Fatalf("Did not receive announce from %s (MQTT discovery failed)", want)
			}
		}
	}
	waitFor(gotA, idB, 60*time.Second)
	waitFor(gotB, idA, 60*time.Second)
}

// TestMQTTDiscoverThenPeerJSInterop MQTT discovery → PeerJS interconnect → file pull:
// B knows nothing about A's peer id (no static configuration), only through MQTT sharded discovery
// then directly connects to A via public cloud signaling to pull files — covers the full chain of discovery and interconnect.
//
// Discovery context: feature test — MQTT discovery → PeerJS interconnect → file pull full chain
func TestMQTTDiscoverThenPeerJSInterop(t *testing.T) {
	requireMQTTEnv(t)
	storageA := t.TempDir()
	content := []byte("mqtt-discovered-peerjs-transfer")
	hash := writeTestFile(t, storageA, content)

	// A: MQTT enabled, announce its own peer id
	svcA := newService(t, randID("it-ma"), storageA, true, nil, hash)
	// B: MQTT enabled, no PEERS config — relies on discovery to interconnect
	svcB := newService(t, randID("it-mb"), t.TempDir(), true, nil, hash)

	// Wait for B to discover and connect to A
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 120*time.Second)

	data, err := svcB.FetchFromPeer(svcA.ID(), hash, 0, -1)
	if err != nil {
		t.Fatalf("Fetch after MQTT discovery failed: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("Content mismatch: got %q", data)
	}
}
