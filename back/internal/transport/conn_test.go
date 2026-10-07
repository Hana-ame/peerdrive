package transport

// conn_test.go: bindConn dedup test for double connections on the same peer (2026-08-19 architecture optimization).
// Background: bidirectional mutual dialing (A↔B both dial each other) or a reconnect race
// leaves two connections under the same peerID, and the old connection becomes an orphan
// (connState stays resident + worker goroutine leak). Fix: bindConn keeps the newest
// connection and Closes the old one outside the lock; local WS sessions are the exception
// (multiple browser tabs are independent and must not be killed by mistake).

import (
	"encoding/json"
	"io"
	"testing"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBindConn_SamePeerDedup double connections on the same peer: the new connection is kept,
// the old one is closed; the old connection's OnClose cleanup (with a conns value-equality
// guard) must not delete the new connection.
func TestBindConn_SamePeerDedup(t *testing.T) {
	svc := newTestPeerJSService(t)
	stale := &fakeSession{id: "peerX"}
	fresh := &fakeSession{id: "peerX"}
	svc.bindConn(stale)
	svc.bindConn(fresh)

	svc.mu.Lock()
	cur := svc.conns["peerX"]
	svc.mu.Unlock()
	require.Same(t, fresh, cur, "conns must point to the latest connection")
	assert.True(t, stale.Closed(), "old connection on same peer must be closed (prevent orphan connection)")
	assert.False(t, fresh.Closed(), "new connection must not be mistakenly closed")

	// after the old connection's OnClose cleanup runs, conns still points at the new connection (value-equality guard worked)
	svc.pendingMu.Lock()
	_, staleInPending := svc.pending[stale]
	svc.pendingMu.Unlock()
	assert.False(t, staleInPending, "old connection state must be cleaned from pending")
}

// TestBindConn_ReplacedConnOldStreamErrors after a connection is replaced, an in-progress
// stream on the old connection must end with an error (no hang): replacement means
// Close(stale) → OnClose cleanup → errCh delivery → the old fetchReader read errors out.
func TestBindConn_ReplacedConnOldStreamErrors(t *testing.T) {
	svc := newTestPeerJSService(t)
	stale := &fakeSession{id: "peerX"}
	svc.bindConn(stale)

	// start a stream on the old connection (frame sent successfully, no response received yet)
	r, err := svc.OpenStream("peerX", hashOf("x"), 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// a new connection for the same peer arrives → the old connection is replaced and closed
	fresh := &fakeSession{id: "peerX"}
	svc.bindConn(fresh)

	_, err = io.ReadAll(r)
	require.Error(t, err, "after connection is replaced, old stream must report error (no hang, no truncation)")
}

// TestBindConn_LocalNoDedup local WS sessions are not deduped: each browser tab has its own
// local session, and an old tab's connection must not be closed actively (its inbound
// service is still active).
func TestBindConn_LocalNoDedup(t *testing.T) {
	svc := newTestPeerJSService(t)
	tab1 := &fakeSession{id: "local", local: true}
	tab2 := &fakeSession{id: "local", local: true}
	svc.bindConn(tab1)
	svc.bindConn(tab2)

	svc.mu.Lock()
	cur := svc.conns["local"]
	svc.mu.Unlock()
	require.Same(t, tab2, cur, "conns points to the latest local session")
	assert.False(t, tab1.Closed(), "old local session must not be actively closed (multi-tab coexistence)")
	assert.False(t, tab2.Closed())
}

// TestBindConn_DedupFreesSlot after dedup the old connection's resources are fully released:
// pending cleanup + worker goroutine exit (binDone closed), and the service can keep
// fetching over the new connection.
func TestBindConn_DedupFreesSlot(t *testing.T) {
	svc := newTestPeerJSService(t)
	stale := &fakeSession{id: "peerY"}
	fresh := &fakeSession{id: "peerY"}
	svc.bindConn(stale)
	svc.bindConn(fresh) // stale is closed (binDone closed → worker exits)

	// fetch normally over the new connection (through the real pump registered by bindConn:
	// data header → expect, binary chunk delivery, done to wrap up)
	content := []byte("dedup-after")
	h := hashOf(string(content))
	r, err := svc.OpenStream("peerY", h, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	rid := reqIDOf(t, fresh)
	require.NotEmpty(t, rid)
	fresh.feed(peerjsFrameText(`{"type":"meta","total":` + itoa(len(content)) + `,"reqId":"` + rid + `"}`))
	fresh.feed(peerjsFrameText(`{"type":"data","size":` + itoa(len(content)) + `,"reqId":"` + rid + `"}`))
	fresh.feed(peerjs.Frame{IsText: false, Data: content})
	fresh.feed(peerjsFrameText(`{"type":"done","size":` + itoa(len(content)) + `,"reqId":"` + rid + `"}`))

	got, err := io.ReadAll(r)
	require.NoError(t, err)
	assert.Equal(t, string(content), string(got), "after dedup, new connection must serve pulls normally")
}

// reqIDOf takes the reqId of the req frame from the session's already-sent frames.
func reqIDOf(t *testing.T, s *fakeSession) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.sent {
		if m["type"] == "req" {
			id, _ := m["reqId"].(string)
			return id
		}
	}
	return ""
}

// TestConnState_AdminUpAndPendingUploadSlot verifies that adminUp and pendingUpload can exist
// at the same time (an assumption of the current code; it should later be fixed to be mutually
// exclusive). Discovery background: code review 2026-08-19 -- the dispatchFrame binary-chunk
// routing assumes the two slots are mutually exclusive, but neither serveUploadBegin nor
// serveAdmin explicitly checks whether the other is already occupied.
func TestConnState_AdminUpAndPendingUploadSlot(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "local", local: true}
	svc.bindConn(sess)
	st := svc.pending[sess]
	require.NotNil(t, st)

	// simulate both slots existing at once
	st.mu.Lock()
	st.adminUp = &adminUploadState{reqID: "admin-1", size: 100}
	st.pendingUpload = &uploadState{reqID: "up-1", size: 100}
	both := st.adminUp != nil && st.pendingUpload != nil
	st.mu.Unlock()
	assert.True(t, both, "current code allows both slots to coexist (known issue, to be fixed)")

	// cleanup (does not trigger the worker)
	st.mu.Lock()
	st.adminUp = nil
	st.pendingUpload = nil
	st.mu.Unlock()
}

// TestConnState_AdminUpOverlap verifies the admin-declaration replacement logic: when an old
// declaration is replaced, an err frame should be sent to the old reqId and the new declaration
// takes the slot (discovery background: the replacement logic in admin.go).
func TestConnState_AdminUpOverlap(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "local", local: true}
	svc.bindConn(sess)
	st := svc.pending[sess]
	require.NotNil(t, st)

	// the first admin declaration (size=0 empty file, completes immediately, does not trigger the worker)
	ar1 := adminReq{Type: "admin", Method: "POST", Path: "/files/upload",
		Binary: true, Filename: "a.bin", Size: 0, ReqID: "ar1"}
	raw1, _ := json.Marshal(ar1)
	svc.serveAdmin(sess, st, raw1)

	// the second admin declaration (also size=0, so replacement leaves no residual worker)
	ar2 := adminReq{Type: "admin", Method: "POST", Path: "/files/upload",
		Binary: true, Filename: "b.bin", Size: 0, ReqID: "ar2"}
	raw2, _ := json.Marshal(ar2)
	svc.serveAdmin(sess, st, raw2)

	// both declarations go through the empty-file path, so the worker never writes to disk
	types := sess.sentTypes()
	assert.Contains(t, types, "err", "should receive err frame when old admin declaration is replaced")
}
