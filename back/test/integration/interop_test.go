//go:build integration

package integration

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"peerdrive/internal/transport"
)

// TestTwoNodesInterop Dual-node interconnect: B pulls a file from A via public cloud signaling + WebRTC direct
// connection, verifying content and sha256 match (covering the full frame protocol req/meta/data/done pipeline).
//
// Discovery background: functional acceptance -- dual-node interconnect baseline via public cloud signaling
// (WebRTC pull + sha256 verification)
func TestTwoNodesInterop(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 700*1024) // spans multiple 64KB chunks
	for i := range content {
		content[i] = byte(i * 7)
	}
	hash := writeTestFile(t, storageA, content)

	idA, idB := randID("it-a"), randID("it-b")
	svcA := newService(t, idA, storageA, false, nil)
	svcB := newService(t, idB, t.TempDir(), false, []string{idA})

	waitConnections(t, svcA, map[string]bool{svcB.ID(): true}, 60*time.Second)
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// B pulls from A
	data, err := svcB.FetchFromPeer(svcA.ID(), hash, 0, -1)
	if err != nil {
		t.Fatalf("FetchFromPeer: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(data), len(content))
	}
	if got := sha256Hex(data); got != hash {
		t.Fatalf("sha256 mismatch: got %s want %s", got, hash)
	}

	// Reverse: A pulls from B (B doesn't have the file -> should error)
	if _, err := svcA.FetchFromPeer(svcB.ID(), hash, 0, -1); err == nil {
		t.Fatal("B doesn't have the file but pull succeeded (should fail)")
	}
}

// TestTwoNodesRangeFetch Range fetch: offset+size only fetches the middle segment.
//
// Discovery background: functional test -- range (offset/size) semantics: only fetch the middle segment
func TestTwoNodesRangeFetch(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 300*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	hash := writeTestFile(t, storageA, content)

	idA, idB := randID("it-ra"), randID("it-rb")
	svcA := newService(t, idA, storageA, false, nil)
	svcB := newService(t, idB, t.TempDir(), false, []string{idA})
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	const offset = 100 * 1024
	const size = 50 * 1024
	data, err := svcB.FetchFromPeer(svcA.ID(), hash, offset, size)
	if err != nil {
		t.Fatalf("FetchFromPeer(range): %v", err)
	}
	if !bytes.Equal(data, content[offset:offset+size]) {
		t.Fatalf("range content mismatch: got %d bytes", len(data))
	}
}

// TestThreeNodesInterop 3-node interconnect: A(file source) + B + C,
// B/C both pull from A; C also verifies interconnect with B (all three visible to each other).
//
// Discovery background: functional test -- three-node pairwise interconnect and pull
func TestThreeNodesInterop(t *testing.T) {
	storageA := t.TempDir()
	content := []byte("three-nodes-interop-" + hex.EncodeToString(make([]byte, 0)) + "-payload")
	hash := writeTestFile(t, storageA, content)

	idA, idB, idC := randID("it-3a"), randID("it-3b"), randID("it-3c")
	svcA := newService(t, idA, storageA, false, []string{idB, idC})
	svcB := newService(t, idB, t.TempDir(), false, []string{idA, idC})
	svcC := newService(t, idC, t.TempDir(), false, []string{idA, idB})

	// Pairwise interconnect: B->A, C->A, C->B
	waitConnections(t, svcA, map[string]bool{svcB.ID(): true, svcC.ID(): true}, 90*time.Second)
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true, svcC.ID(): true}, 90*time.Second)
	waitConnections(t, svcC, map[string]bool{svcA.ID(): true, svcB.ID(): true}, 90*time.Second)

	for name, svc := range map[string]*transport.PeerJSService{"B": svcB, "C": svcC} {
		data, err := svc.FetchFromPeer(svcA.ID(), hash, 0, -1)
		if err != nil {
			t.Fatalf("%s pull from A failed: %v", name, err)
		}
		if !bytes.Equal(data, content) {
			t.Fatalf("%s pulled content mismatch", name)
		}
	}

	// C pulls from B (B already pulled from A but hasn't persisted -> should still fail;
	// here we just verify the C<->B connection exists; persistence forwarding is upper-layer
	// sync logic, not in scope for this protocol)
	_ = svcC.Connections()
	if _, err := svcC.FetchFromPeer(svcB.ID(), hash, 0, -1); err == nil {
		t.Log("note: C pulled successfully from B (B may have cached the file)")
	}
}

// TestFourNodesStar 4-node star topology: A provides the file, B/C/D all pull from A simultaneously (one-to-many concurrency).
//
// Discovery background: functional test -- one-to-many concurrency (same node serving multiple peers simultaneously)
func TestFourNodesStar(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 128*1024)
	for i := range content {
		content[i] = byte(i)
	}
	hash := writeTestFile(t, storageA, content)

	idA := randID("it-4a")
	svcA := newService(t, idA, storageA, false, nil)
	var others []*transport.PeerJSService
	for i := 0; i < 3; i++ {
		svc := newService(t, randID("it-4x"), t.TempDir(), false, []string{idA})
		others = append(others, svc)
	}

	want := map[string]bool{svcA.ID(): true}
	for _, s := range others {
		waitConnections(t, s, want, 90*time.Second)
	}

	// Concurrent pulls from A
	done := make(chan error, len(others))
	for _, s := range others {
		go func(s *transport.PeerJSService) {
			data, err := s.FetchFromPeer(svcA.ID(), hash, 0, -1)
			if err != nil {
				done <- err
				return
			}
			if !bytes.Equal(data, content) {
				done <- bytesErr(len(data))
				return
			}
			done <- nil
		}(s)
	}
	for range others {
		if err := <-done; err != nil {
			t.Fatalf("concurrent pull failed: %v", err)
		}
	}
}

// bytesErr simple error construction.
func bytesErr(n int) error {
	return fmt.Errorf("size mismatch: %d", n)
}


// TestConcurrentLargeFetches Concurrent large-file pulls: 4 concurrent requests x 2MB on the same node.
// Discovery background: the old flow control implementation had each serveFile register its own
// OnBufferedAmountLow (pion replacement-style callback); with concurrent requests, only one could
// receive the low-water event, and the rest dead-waited when bufferedAmount exceeded the threshold
// (this test would hang until timeout before the fix). Fix: flow control moved to SendFrame
// (connection-level global callback).
func TestConcurrentLargeFetches(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 2*1024*1024)
	for i := range content {
		content[i] = byte(i * 13)
	}
	hash := writeTestFile(t, storageA, content)

	idA := randID("it-ca")
	newService(t, idA, storageA, false, nil) // svcA: file source, no direct reference needed
	var clients []*transport.PeerJSService
	for i := 0; i < 4; i++ {
		svc := newService(t, randID("it-cx"), t.TempDir(), false, []string{idA})
		clients = append(clients, svc)
	}
	for _, s := range clients {
		waitConnections(t, s, map[string]bool{idA: true}, 60*time.Second)
	}

	done := make(chan error, len(clients))
	for _, s := range clients {
		go func(s *transport.PeerJSService) {
			data, err := s.FetchFromPeer(idA, hash, 0, -1)
			if err != nil {
				done <- err
				return
			}
			if !bytes.Equal(data, content) {
				done <- fmt.Errorf("content mismatch: got %d bytes", len(data))
				return
			}
			done <- nil
		}(s)
	}
	for range clients {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent large-file pull failed: %v", err)
			}
		case <-time.After(120 * time.Second):
			t.Fatal("concurrent large-file pull timed out (flow control deadlock?)")
		}
	}
}
