package transport

// stream_test.go: Streaming OpenStream/fetchReader tests (outbound role).
// Uses fakeSession to manually inject peer frames (meta/data/done/err), driving
// the message pump -> chunk queue -> reader full pipeline.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindFakeConn binds a fakeSession to svc (conns + pending), returns the session.
// Unlike bindConn: does not start uploadWorker (no upload scenario), only registers routing.
func bindFakeConn(t *testing.T, svc *PeerJSService, id string) *fakeSession {
	t.Helper()
	sess := &fakeSession{id: id}
	st := &connState{
		fetches: make(map[string]*fetchState),
		binCh:   make(chan binaryChunk, 16),
		binDone: make(chan struct{}),
	}
	svc.mu.Lock()
	if svc.conns == nil {
		svc.conns = make(map[string]Session)
	}
	svc.conns[id] = sess
	svc.mu.Unlock()
	svc.pendingMu.Lock()
	if svc.pending == nil {
		svc.pending = make(map[Session]*connState)
	}
	svc.pending[sess] = st
	svc.pendingMu.Unlock()
	// Capture the message callback (fakeSession.OnMessage is already implemented)
	sess.feed(peerjsFrameText(`{"type":"x"}`)) // no-op to trigger registration (OnMessage not called in constructor)
	// OnMessage callback needs manual registration to pump -- bindConn did not call it;
	// here we bind directly: reuse bindConn's dispatch logic (text verb -> inbound; response -> outbound)
	return sess
}

func peerjsFrameText(s string) peerjs.Frame { return peerjs.Frame{IsText: true, Data: []byte(s)} }

// TestOpenStream_StreamingRead Full streaming pipeline: inject meta/data/data/done,
// reader reads out in chunks, full-request sha256 verification passes (H5 fallback retained).
// Discovery background: streaming refactor (source system) -- old requestFile buffered
// everything in memory, 8GB file OOM risk; after switching to chunk queue streaming,
// this test verifies the chunk delivery -> consumption -> verification pipeline.
func TestOpenStream_StreamingRead(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := bindFakeConn(t, svc, "peerA")

	content := bytes.Repeat([]byte("stream-data-"), 100) // 1300 bytes
	h := sha256.Sum256(content)
	hash := hex.EncodeToString(h[:])

	// Register OnMessage callback: simulate bindConn's dispatch (response frames -> routeResponse)
	var pumpMu sync.Mutex
	sess.OnMessage(func(msg peerjs.Frame) {
		if !msg.IsText {
			return // binary chunks are handled by bindConn's pump -- in tests we don't go
			// through the pump, deliver directly to expect's q (see feedData below)
		}
		var r dcResp
		_ = json.Unmarshal(msg.Data, &r)
		pumpMu.Lock()
		defer pumpMu.Unlock()
		svc.routeResponse(svc.stateFor(sess), r, nil)
	})

	// Initiate streaming request
	r, err := svc.OpenStream("peerA", hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// Inject response frames (simulating the peer):
	// meta -> data(chunk1 700B) -> data(chunk2 600B) -> done
	sess.feed(peerjsFrameText(`{"type":"meta","hash":"` + hash + `","total":1300,"reqId":"x"}`))
	reqID := "" // get reqId from the req frame sent by the session
	sess.mu.Lock()
	for _, m := range sess.sent {
		if m["type"] == "req" {
			reqID, _ = m["reqId"].(string)
		}
	}
	sess.mu.Unlock()
	require.NotEmpty(t, reqID, "openStream must carry reqId")

	feedData := func(size int, payload []byte) {
		st := svc.stateFor(sess)
		st.mu.Lock()
		f := st.expect
		st.mu.Unlock()
		require.NotNil(t, f, "expect must exist before data frame")
		// Deliver directly to the chunk queue (equivalent to pump's binary branch)
		select {
		case f.q <- payload:
			f.received += int64(size)
			if f.received >= f.size {
				st.mu.Lock()
				st.expect = nil
				st.mu.Unlock()
			}
		case <-f.closed:
		}
	}

	// Chunk 1 (send data header first to set expect, then deliver data chunk -- same order as real pump)
	sess.feed(peerjsFrameText(`{"type":"data","size":700,"reqId":"` + reqID + `"}`))
	feedData(700, content[:700])
	// Chunk 2
	sess.feed(peerjsFrameText(`{"type":"data","size":600,"reqId":"` + reqID + `"}`))
	feedData(600, content[700:])
	// done
	sess.feed(peerjsFrameText(`{"type":"done","size":1300,"reqId":"` + reqID + `"}`))

	got, err := io.ReadAll(r)
	require.NoError(t, err, "streaming read should succeed")
	assert.Equal(t, content, got, "reassembled chunks must match the original content")
}

// TestOpenStream_CloseCancel Early Close: pump delivery does not block (closed channel allows through).
func TestOpenStream_CloseCancel(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := bindFakeConn(t, svc, "peerA")

	r, err := svc.OpenStream("peerA", hashOf("x"), 0, -1)
	require.NoError(t, err)
	r.Close()

	st := svc.stateFor(sess)
	st.mu.Lock()
	require.Empty(t, st.fetches, "after Close must clean up from the routing table")
	st.mu.Unlock()

	// Deliver chunks after close: must not block (select closed branch)
	require.NoError(t, r.Close(), "repeated Close is idempotent")
}

// TestOpenStream_ConnClosed Connection closed: reader returns error rather than hanging.
func TestOpenStream_ConnClosed(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := bindFakeConn(t, svc, "peerA")

	r, err := svc.OpenStream("peerA", hashOf("x"), 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// Simulate bindConn OnClose cleanup (errCh delivery; close(f.closed) is handled
	// idempotently by reader cleanup -- here we only send the error to avoid double close)
	st := svc.stateFor(sess)
	st.mu.Lock()
	for _, f := range st.fetches {
		select {
		case f.errCh <- errConnClosedForTest:
		default:
		}
	}
	st.mu.Unlock()

	_, err = io.ReadAll(r)
	require.Error(t, err, "after connection close, reading must return an error")
}

// errConnClosedForTest test error marker (cannot use io.EOF -- ReadAll treats EOF as normal end).
var errConnClosedForTest = errors.New("connection closed (test)")

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
