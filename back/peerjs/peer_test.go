package peerjs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── message.go / utility functions ────────────────────────────────────────────────

// Discovery background: defensive test -- utility function basic behavior:
// frame construction/serialization correctness, depended upon by the protocol layer above
func TestNewMessage(t *testing.T) {
	m := NewMessage(MsgOffer, "dst-node", OfferPayload{ConnectionID: "c1"})
	assert.Equal(t, MsgOffer, m.Type)
	assert.Equal(t, "dst-node", m.Dst)
	var p OfferPayload
	require.NoError(t, json.Unmarshal(m.Payload, &p))
	assert.Equal(t, "c1", p.ConnectionID)

	// When payload is nil, Payload is empty
	m2 := NewMessage(MsgHeartbeat, "", nil)
	assert.Empty(t, m2.Payload)
}

// Discovery background: defensive test -- connectionId extraction is the signaling
// routing key; extraction errors cause message misrouting
func TestPayloadConnectionID(t *testing.T) {
	m := NewMessage(MsgCandidate, "dst", CandidatePayload{ConnectionID: "conn-42"})
	assert.Equal(t, "conn-42", payloadConnectionID(m))
	assert.Empty(t, payloadConnectionID(NewMessage(MsgAnswer, "dst", nil)))
}

// ── Peer: signaling routing (fakeSignaller driven) ─────────────────────────────────

// offer constructs a valid data OFFER message.
// Uses a real pion-generated offer SDP (SetRemoteDescription can succeed -- a fake SDP
// would cause handleOffer to fail, the connection would close, and the test would not
// reach the real code path).
func offer(t *testing.T, src, connID string) Message {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	defer func() { _ = pc.Close() }()
	_, err = pc.CreateDataChannel("test", nil)
	require.NoError(t, err)
	offer, err := pc.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, pc.SetLocalDescription(offer))

	m := NewMessage(MsgOffer, "", OfferPayload{
		SDP:          &offer,
		Type:         ConnData,
		ConnectionID: connID,
		Label:        "test",
	})
	m.Src = src
	return m
}

// TestRouteOffer_AnswererUsesOffererConnectionID Regression: the answerer must reuse
// the offerer's connectionId (a previously generated new ID caused ANSWER routing
// failure and ICE stuck at checking).
//
// Discovery background: dual-node E2E (real 0.peerjs.com signaling) -- after B
// initiates a connection, it receives an ANSWER but ICE stays at checking forever
// and DataChannel never opens. Debugging logs located the issue: A's ANSWER used
// a newly generated connectionId, and B couldn't find the connection in conns by that
// id, so the message was dropped.
func TestRouteOffer_AnswererUsesOffererConnectionID(t *testing.T) {
	p, f := newTestPeer()
	_ = f

	p.OnConnection(func(c *Connection) {})

	f.inject(offer(t, "remote-node", "conn-from-offerer"))

	p.mu.Lock()
	conn := p.conns["conn-from-offerer"]
	p.mu.Unlock()
	require.NotNil(t, conn, "connection must be registered with the offerer-provided connectionId")
	assert.Equal(t, "conn-from-offerer", conn.ID)
	assert.Equal(t, "remote-node", conn.PeerID)

	// The answer must be ANSWER with the same connectionId
	var found bool
	for _, m := range f.sentSnapshot() {
		if m.Type == MsgAnswer {
			var p AnswerPayload
			require.NoError(t, json.Unmarshal(m.Payload, &p))
			assert.Equal(t, "conn-from-offerer", p.ConnectionID)
			found = true
		}
	}
	assert.True(t, found, "must send an ANSWER reply")
}

// TestRouteOffer_DuplicateConnectionID_ClosesOld Regression: with a duplicate
// connectionId, the old connection must be fully Closed (previously only pc.Close:
// leftover conns map, done never closed, onClose not triggered -> upper layer hangs).
//
// Discovery background:
//  1. Code review found the "duplicate OFFER only closes pc without cleaning up the registry" leak path
//  2. While writing this test, another chained bug was exposed: changing to old.Close() while
//     holding p.mu -> Close->forgetConnection needs the same lock -> Go mutex is non-reentrant
//     deadlock -> test hangs. Fix: only extract old inside the lock, then unlock and Close.
func TestRouteOffer_DuplicateConnectionID_ClosesOld(t *testing.T) {
	p, f := newTestPeer()
	p.OnConnection(func(c *Connection) {})

	f.inject(offer(t, "remote-node", "conn-x"))

	p.mu.Lock()
	first := p.conns["conn-x"]
	p.mu.Unlock()
	require.NotNil(t, first)

	// Second OFFER with the same connectionId
	f.inject(offer(t, "remote-node", "conn-x"))

	// The old connection's done channel must be closed (connectLoop relies on it to exit reconnect)
	select {
	case <-first.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("old connection's Done not closed: leak (pre-fix behavior)")
	}
	// Old connection must be removed from conns, new connection takes over
	p.mu.Lock()
	cur := p.conns["conn-x"]
	p.mu.Unlock()
	assert.NotNil(t, cur)
	assert.NotSame(t, first, cur, "must be the new connection")
}

// TestRouteExpire_ClosesConnection Regression: EXPIRE (OFFER queued and expired) must
// close the connection and trigger Done (connectLoop relies on it to reconnect; pre-fix
// it waited forever for OnOpen).
//
// Discovery background: dual-node E2E -- B starts before A (A not yet online), B's
// OFFER expires in the signaling server queue, server replies EXPIRE; B's connectLoop
// still waits for OnOpen, times out after 20s without retry, and A still can't connect
// even after coming online. Fix: EXPIRE -> Close (trigger Done) -> loop reconnect.
func TestRouteExpire_ClosesConnection(t *testing.T) {
	p, f := newTestPeer()
	c, _ := newTestConn(p, "conn-e") // register directly, not relying on OFFER flow

	exp := NewMessage(MsgExpire, "", struct {
		ConnectionID string `json:"connectionId"`
	}{ConnectionID: "conn-e"})
	exp.Src = "remote-node"
	f.inject(exp)

	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection not closed after EXPIRE")
	}
	p.mu.Lock()
	_, still := p.conns["conn-e"]
	p.mu.Unlock()
	assert.False(t, still, "connection should be removed from the registry")
}

// TestRouteLeave_ClosesAllFromPeer LEAVE closes all connections from that remote peer.
//
// Discovery background: code review found handleLeave called c.Close() one by one while
// holding p.mu -- Close->forgetConnection needs the same lock -> Go mutex non-reentrant
// deadlock. This test reproduces it directly under -race (hangs with 3 nodes / multiple
// connections); fix: collect inside the lock, close after unlocking.
func TestRouteLeave_ClosesAllFromPeer(t *testing.T) {
	p, f := newTestPeer()
	_, _ = newTestConn(p, "c-a")
	_, _ = newTestConn(p, "c-b")
	_, _ = newTestConn(p, "c-c")

	// c-a/c-b belong to remote-node, c-c belongs to other-node
	p.mu.Lock()
	p.conns["c-a"].PeerID = "remote-node"
	p.conns["c-b"].PeerID = "remote-node"
	p.conns["c-c"].PeerID = "other-node"
	p.mu.Unlock()

	leave := NewMessage(MsgLeave, "", nil)
	leave.Src = "remote-node"
	f.inject(leave)

	p.mu.Lock()
	_, hasA := p.conns["c-a"]
	_, hasB := p.conns["c-b"]
	_, hasC := p.conns["c-c"]
	p.mu.Unlock()
	assert.False(t, hasA, "all connections from remote-node should be closed")
	assert.False(t, hasB)
	assert.True(t, hasC, "connections from other nodes should be unaffected")
}

// TestRouteCandidate_UnknownConnID_Ignored A candidate with an unknown connectionId
// does not panic.
//
// Discovery background: defensive test -- the peer may send out-of-order or expired
// ICE candidates (or malicious injection); route must safely drop unknown connectionIds
// instead of panicking (nil conn dereference was previously considered).
func TestRouteCandidate_UnknownConnID_Ignored(t *testing.T) {
	p, f := newTestPeer()
	cand := NewMessage(MsgCandidate, "", CandidatePayload{ConnectionID: "nope"})
	assert.NotPanics(t, func() { f.inject(cand) })
	_ = p
}

// TestRouteHeartbeat_Ignored Heartbeat messages have no side effects.
//
// Discovery background: peerjs-server sends HEARTBEAT to clients (server-side keepalive
// probe); the client should not reply and should not produce any side effects
// (peerjs-client behavior alignment).
func TestRouteHeartbeat_Ignored(t *testing.T) {
	_, f := newTestPeer()
	assert.NotPanics(t, func() {
		f.inject(NewMessage(MsgHeartbeat, "", nil))
	})
	assert.Empty(t, f.sent, "heartbeat should not produce any sends")
}

// ── Peer: Close idempotency and lifecycle ─────────────────────────────────────────

// TestPeerClose_Idempotent Regression: repeated Close calls do not panic (previously
// worried about closing a closed channel).
//
// Discovery background: code review -- Peer.Close / Connection.Close / signaller.Close
// all have "close(channel)" logic; repeated calls would panic (close of closed channel),
// and the service layer startLoop/Close may trigger multiple closes concurrently.
func TestPeerClose_Idempotent(t *testing.T) {
	p, _ := newTestPeer()
	p.Close()
	assert.NotPanics(t, p.Close)
	assert.NotPanics(t, p.Close)
}

// TestPeerClose_ClosesAllConnections Peer.Close closes all connections and triggers Done.
//
// Discovery background: closeOnce guarantees each Connection is cleaned up only once;
// this test verifies that Peer-level close cascades to all connections (no connection
// left behind in the conns map waiting to leak).
func TestPeerClose_ClosesAllConnections(t *testing.T) {
	p, _ := newTestPeer()
	c1, _ := newTestConn(p, "c1")
	c2, _ := newTestConn(p, "c2")

	p.Close()

	select {
	case <-c1.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("c1 not closed")
	}
	select {
	case <-c2.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("c2 not closed")
	}
}

// TestSetICEServers The NewPeerWithSignaller path must have an ICE configuration entry
// point (previously always nil).
//
// Discovery background: code review -- under the NewPeerWithSignaller (custom signaling)
// path, p.iceServers was always nil; WebRTC only had LAN host candidates and couldn't
// hole-punch across public networks. Options.ICEServers only covered the PeerJS cloud
// signaling path.
func TestSetICEServers(t *testing.T) {
	p, _ := newTestPeer()
	servers := []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}}
	p.SetICEServers(servers)

	// This configuration is used when establishing connections via Connect (real pion PC creation, no network)
	conn, err := p.Connect(context.Background(), "remote-node", "t")
	require.NoError(t, err)
	assert.NotNil(t, conn)
	conn.Close()
}

// ── Connection: frame protocol ────────────────────────────────────────────────────

// TestConnection_SendJSON_UsesTextFrame Regression: JSON headers must be text frames
// (previously binary frames caused the peer to swallow control headers as data chunks).
//
// Discovery background: dual-node E2E -- the Go side sent JSON headers with
// dc.Send([]byte) (binary frames, PPID 53), and the peerjs browser side collected all
// messages as data chunks; the done frame never fired, pulls timed out; adding logs
// revealed the peer received binary frames. Fix: headers use SendText (PPID 51).
func TestConnection_SendJSON_UsesTextFrame(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	require.NoError(t, c.SendJSON(map[string]any{"type": "hello"}))
	require.Len(t, dc.events, 1)
	assert.Contains(t, dc.events[0], "text:")
	assert.Contains(t, dc.events[0], `"hello"`)
}

// TestConnection_Send_BinaryFrame Data chunks must be binary frames.
//
// Discovery background: same E2E as TestConnection_SendJSON_UsesTextFrame -- the protocol
// distinguishes "text frame = control header / binary frame = data chunk"; Send must
// maintain binary semantics.
func TestConnection_Send_BinaryFrame(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	require.NoError(t, c.Send([]byte{1, 2, 3}))
	require.Len(t, dc.events, 1)
	assert.Equal(t, "bin:\x01\x02\x03", dc.events[0])
}

// TestConnection_SendFrame_Atomic Regression: during concurrent sends, the data header
// and binary body must be atomic and contiguous (previously unlocked interleaving caused
// the receiver's expect state machine to attach chunks to the wrong request).
//
// Discovery background: code review -- when multiple goroutines concurrently run
// serveFile, if the data header and binary body are not contiguous on the wire, the
// receiver attaches binary chunks to the wrong request (reqId state machine confusion).
// SendFrame's sendMu guarantees one frame is atomic; this test brute-forces with
// 8 goroutines x 50 rounds.
func TestConnection_SendFrame_Atomic(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	const workers = 8
	const perWorker = 50
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				header := map[string]any{"type": "data", "w": w, "i": i}
				body := []byte{byte(w), byte(i)}
				if err := c.SendFrame(header, body); err != nil {
					t.Errorf("SendFrame: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Each text: header must be immediately followed by its corresponding bin: body
	assert.GreaterOrEqual(t, len(dc.events), workers*perWorker*2)
	for i := 0; i+1 < len(dc.events); i += 2 {
		assert.True(t, isText(dc.events[i]), "event %d should be a text header: %v", i, dc.events[i])
		assert.True(t, isBin(dc.events[i+1]), "event %d should be a binary body: %v", i+1, dc.events[i+1])
	}
}

func isText(e string) bool { return len(e) > 5 && e[:5] == "text:" }
func isBin(e string) bool  { return len(e) > 4 && e[:4] == "bin:" }

// TestConnection_SendFrame_BeforeOpen_Error Sending before open returns an error.
//
// Discovery background: defensive test -- the answerer-side OnConnection callback
// fires before DataChannel open (handleOffer calls back immediately); if the upper
// layer sends at this point, it must get a clear error rather than silent loss
// or panic.
func TestConnection_SendFrame_BeforeOpen_Error(t *testing.T) {
	p, _ := newTestPeer()
	c, _ := newTestConn(p, "c1") // not openFake
	assert.Error(t, c.SendFrame(map[string]any{"type": "data"}, []byte{1}))
	assert.Error(t, c.SendJSON(map[string]any{"type": "x"}))
	assert.Error(t, c.Send([]byte{1}))
}

// TestConnection_Close_Idempotent Regression: repeated Close calls do not panic, Done
// fires only once.
//
// Discovery background: code review -- Connection.Close is called concurrently by
// multiple triggers (ICE state callback, remote dc close, upper-layer active, Peer.Close
// cascade); closeOnce must guarantee onClose fires only once (upper-layer pending requests
// rely on it for precise cleanup).
func TestConnection_Close_Idempotent(t *testing.T) {
	p, _ := newTestPeer()
	c, _ := newTestConn(p, "c1")

	closed := 0
	c.OnClose(func(*Connection) { closed++ })
	c.Close()
	c.Close()
	c.Close()

	assert.Equal(t, 1, closed, "onClose should fire only once")
	select {
	case <-c.Done():
	default:
		t.Fatal("Done should already be closed")
	}
	// Already removed from the registry
	p.mu.Lock()
	_, ok := p.conns["c1"]
	p.mu.Unlock()
	assert.False(t, ok)
}

// TestConnection_RemoteClose_CleansUp Regression: remote closing the dc must trigger
// local cleanup (previously pionChannel.OnClose was never wired; the connection hung
// until ICE timeout fallback).
//
// Discovery background: code review -- the DataChannel interface has OnClose but the
// pion adapter layer never wired it; after the peer actively closes the dc, the local
// connection hangs for seconds to minutes (waiting for ICE disconnected fallback), and
// upper-layer pending requests could only rely on a 5-minute timeout for release. Fix:
// on attach, dc.OnClose -> c.Close().
func TestConnection_RemoteClose_CleansUp(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	// Simulate remote close: trigger dc.OnClose
	require.NotNil(t, dc.onCls)
	dc.onCls()

	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("local cleanup did not happen after remote close")
	}
}

// TestConnection_OnMessage_RoutesFrames Text/binary frames are dispatched by type.
//
// Discovery background: defensive test -- frame type distinction is the protocol
// foundation (see SendJSON/Send tests); verifies the receiving side also correctly
// routes by IsText (service layer bindConn relies on this behavior).
func TestConnection_OnMessage_RoutesFrames(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	var gotText, gotBin bool
	c.OnMessage(func(f Frame) {
		if f.IsText {
			gotText = string(f.Data) == "hello"
		} else {
			gotBin = len(f.Data) == 3
		}
	})
	require.NotNil(t, dc.onMsg)
	dc.onMsg(Frame{IsText: true, Data: []byte("hello")})
	dc.onMsg(Frame{IsText: false, Data: []byte{1, 2, 3}})
	assert.True(t, gotText)
	assert.True(t, gotBin)
}

// TestConnection_CallbackRegistration_Concurrent Regression: registration of
// OnOpen/OnMessage/OnClose (business goroutine calls setter) and attach callback
// triggering (pion callback goroutine reads snapshot) execute concurrently -- handlerMu
// protects, verified under -race for no data race.
//
// Discovery background: -race integration test crashes every time it runs (self-hosted
// signaling + same-machine WebRTC timing is fast; connectLoop's OnOpen registration races
// with the DataChannel open callback read/write of c.onOpen); real networks have the
// same issue (timing is slower, harder to trigger). Fix: handlerMu protects the three
// fields, the trigger side takes a snapshot before calling back.
func TestConnection_CallbackRegistration_Concurrent(t *testing.T) {
	p, _ := newTestPeer()
	c, dc := newTestConn(p, "c1")
	openFake(dc)

	const rounds = 50
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.OnOpen(func(*Connection) {})
			c.OnMessage(func(Frame) {})
			c.OnClose(func(*Connection) {})
		}()
		go func() {
			defer wg.Done()
			// Trigger the closures registered by attach (internally reads snapshot, racing with the setters above)
			if dc.onOpen != nil {
				dc.onOpen()
			}
			if dc.onMsg != nil {
				dc.onMsg(Frame{IsText: true, Data: []byte("x")})
			}
			if dc.onCls != nil {
				dc.onCls()
			}
		}()
	}
	wg.Wait()

	// Snapshot reads don't break registration semantics: after triggering, the latest
	// registered callback is still received
	var got bool
	c.OnMessage(func(f Frame) { got = string(f.Data) == "y" })
	dc.onMsg(Frame{IsText: true, Data: []byte("y")})
	assert.True(t, got, "the callback registered after concurrency must still work")
}

// TestConnectedPeers Verifies ConnectedPeers only returns open connections and deduplicates.
func TestConnectedPeers(t *testing.T) {
	p, _ := newTestPeer()

	// Non-open connections are not counted
	c1, f1 := newTestConn(p, "conn-1")
	c2, f2 := newTestConn(p, "conn-2")
	assert.Empty(t, p.ConnectedPeers())

	// After open, returns remote ids
	openFake(f1)
	openFake(f2)
	got := p.ConnectedPeers()
	assert.ElementsMatch(t, []string{"remote-conn-1", "remote-conn-2"}, got)

	// Multiple connections from the same remote peer should be deduplicated
	dup := &Connection{
		ID:       "conn-dup",
		PeerID:   "remote-conn-1",
		peer:     p,
		done:     make(chan struct{}),
		lowWater: make(chan struct{}, 1),
	}
	fdup := newFakeDC()
	dup.attach(fdup)
	openFake(fdup)
	p.registerConnection(dup)

	got = p.ConnectedPeers()
	assert.ElementsMatch(t, []string{"remote-conn-1", "remote-conn-2"}, got, "same remote peer should appear only once")

	// After closing c1, remote-conn-1 still has the dup connection open, so it should still appear (dedup by remote ID)
	c1.Close()
	got = p.ConnectedPeers()
	assert.ElementsMatch(t, []string{"remote-conn-1", "remote-conn-2"}, got)

	// After closing c2, only remote-conn-1 remains (dup still open)
	c2.Close()
	got = p.ConnectedPeers()
	assert.ElementsMatch(t, []string{"remote-conn-1"}, got)

	// After closing all, empty
	dup.Close()
	assert.Empty(t, p.ConnectedPeers())
}
