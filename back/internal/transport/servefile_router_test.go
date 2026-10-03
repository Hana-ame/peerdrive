package transport

// servefile_router_test.go: serveFile multi-source routing + upstream loop prevention
// tests (optimization item 3, 2026-08-18).
//
// Background: after serveFile was upgraded from local semantics (fileIndex + CAS)
// to multi-source routing (source.Manager: local -> peer -> URL template), a peer
// req may reach back to another node -- when A<->B are interconnected, B requesting
// a file A doesn't have triggers an infinite A->B->A recursion.
// Loop prevention: the req frame carries a trace (chain of nodes traversed); a node
// on the forwarding path that finds itself in the chain refuses (dcReq.Trace; serveFile
// propagates via context, OpenStreamFrom carries it).
//
// Note: this file does not import the source package (transport <-> source have a
// dependency, and importing source from internal tests would create an import cycle)
// -- router is implemented with a local fakeRouter that satisfies the FileRouter
// interface, aligned with source.Manager/LocalSource semantics.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRouter in-memory FileRouter: behavior aligned with LocalSource (clamp + length limit).
type fakeRouter struct {
	content []byte
	hash    string
}

func (f *fakeRouter) OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if hash != f.hash {
		return nil, fmt.Errorf("not found")
	}
	off := offset
	if off < 0 {
		off = 0
	}
	if off > int64(len(f.content)) {
		off = int64(len(f.content))
	}
	length := size
	if length < 0 || off+length > int64(len(f.content)) {
		length = int64(len(f.content)) - off
	}
	return io.NopCloser(bytes.NewReader(f.content[off : off+length])), nil
}

func (f *fakeRouter) InfoSize(ctx context.Context, hash string) (int64, error) {
	if hash != f.hash {
		return 0, fmt.Errorf("not found")
	}
	return int64(len(f.content)), nil
}

// TestServeFile_LoopDetected Trace contains this node's ID -> refuse, no upstream
// call (prevents infinite loop).
//
// Discovery background: code review 2026-08-18 -- serveFile must prevent upstream
// loops after multi-source routing is introduced; trace is a new optional field
// in the frame protocol (omitempty for backward compatibility with old peers).
func TestServeFile_LoopDetected(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.id = "node-a"
	sess := &fakeSession{id: "node-b"}
	svc.serveFile(sess, dcReq{
		Type: "req", Hash: hashOf("loop"), ReqID: "r1",
		Trace: []string{"node-c", "node-a"}, // this node is already in the chain
	})
	types := sess.sentTypes()
	require.Len(t, types, 1, "should reply exactly one err frame")
	assert.Equal(t, "err", types[0])
	assert.Equal(t, "loop detected", sess.sent[0]["msg"])
}

// TestServeFile_RouterMultiSource After wiring the router: on hit, use multi-source
// routing (meta.total from InfoSize + data streamed, including range requests); on
// miss, err not found.
//
// Discovery background: code review 2026-08-18 -- serveFile was upgraded from
// hardcoded openFile to the FileRouter interface (implemented by source.Manager);
// this test locks down "routing hit + total semantics + range + miss error" behavior.
func TestServeFile_RouterMultiSource(t *testing.T) {
	svc := newTestPeerJSService(t)
	content := []byte("hello multi-source router")
	hash := hashOf(string(content))
	svc.SetFileRouter(&fakeRouter{content: content, hash: hash})

	// Full request (size=-1): meta(total) -> data(full content) -> done
	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes(), "on hit must be meta+data+done")
	assert.Equal(t, float64(len(content)), frames[0].header["total"], "meta.total comes from InfoSize")
	assert.Equal(t, content, frames[1].body, "data block must match the original content")
	assert.Equal(t, float64(len(content)), frames[2].header["size"], "done.size is the actual amount sent")

	// Range request: offset=2 size=4 -> data only returns [2:6]
	sess2 := &fakeSession{id: "remote2"}
	svc.serveFile(sess2, dcReq{Type: "req", Hash: hash, Offset: 2, Size: 4, ReqID: "r2"})
	frames2 := sess2.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess2.sentFrameTypes())
	assert.Equal(t, content[2:6], frames2[1].body, "range request must return the corresponding range")
	assert.Equal(t, float64(len(content)), frames2[0].header["total"], "meta.total is still the full file size")

	// Miss: err not found
	sess3 := &fakeSession{id: "remote3"}
	svc.serveFile(sess3, dcReq{Type: "req", Hash: hashOf("nope"), ReqID: "r3"})
	assert.Equal(t, "err", sess3.sentTypes()[0])
	assert.Equal(t, "not found", sess3.sent[0]["msg"])
}

// TestServeFile_NoRouterFallback No router wired -> local semantics (fileIndex + CAS).
// Maintains old behavior without regression (test/standalone mode).
func TestServeFile_NoRouterFallback(t *testing.T) {
	svc := newTestPeerJSService(t)
	content := []byte("fallback-local")
	hash := hashOf(string(content))
	// Write content-addressed storage
	dir := filepath.Join(svc.storageDir, hash[:2])
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, hash), content, 0o644))

	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes())
	assert.Equal(t, float64(len(content)), frames[0].header["total"])
	assert.Equal(t, content, frames[1].body)
}

// TestOpenStreamFrom_TracePropagation OpenStreamFrom propagates trace to the peer's
// req frame (loop prevention chain propagation point); root request (trace=nil) does
// not carry that field.
//
// Discovery background: code review 2026-08-18 -- if serveFile upstream drops
// trace, downstream nodes cannot detect loops; ctx propagation makes the "root
// request (no trace) -> forward (with trace) -> reject loop" chain complete.
// PeerSource's ctx->trace reading is in source/peer.go (a few lines, covered by
// this test for frame-side propagation + conn.go TraceKey comment constraints).
func TestOpenStreamFrom_TracePropagation(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.id = "node-a"
	sess := bindFakeConn(t, svc, "peerB")

	// With trace: the sent req frame must carry it (forwarding chain node-b -> node-a -> downstream)
	r, err := svc.OpenStreamFrom("peerB", hashOf("x"), 0, -1, []string{"node-b"})
	require.NoError(t, err)
	var reqHeader map[string]any
	for _, fr := range sess.sentFrames() {
		if fr.header["type"] == "req" {
			reqHeader = fr.header
		}
	}
	require.NotNil(t, reqHeader, "OpenStreamFrom should send a req frame")
	trace, ok := reqHeader["trace"].([]any)
	require.True(t, ok, "a request with trace must serialize the trace field")
	assert.Equal(t, []any{"node-b"}, trace, "trace is passed through as-is to downstream")
	r.Close()

	// Root request (trace=nil): omitempty does not serialize the trace field
	sess2 := bindFakeConn(t, svc, "peerC")
	r2, err := svc.OpenStreamFrom("peerC", hashOf("y"), 0, -1, nil)
	require.NoError(t, err)
	var rootHeader map[string]any
	for _, fr := range sess2.sentFrames() {
		if fr.header["type"] == "req" {
			rootHeader = fr.header
		}
	}
	require.NotNil(t, rootHeader)
	_, hasTrace := rootHeader["trace"]
	assert.False(t, hasTrace, "root request must not carry trace")
	r2.Close()
}

// TestServeFile_RouterInfoSizeFail InfoSize failure (peer/url source has no
// metadata) -> meta.total=-1 (protocol convention: fetchReader only checks the
// cap when total>0), but the data stream is sent normally.
//
// Discovery background: code review 2026-08-18 -- under multi-source routing,
// peer/url sources can't get the file size; if InfoSize failure directly errs, it
// makes a source with data unavailable. The convention total=-1 means "unknown
// size, stream sending".
func TestServeFile_RouterInfoSizeFail(t *testing.T) {
	svc := newTestPeerJSService(t)
	content := []byte("no-info-size stream")
	hash := hashOf(string(content))
	svc.SetFileRouter(&infoFailRouter{content: content, hash: hash})

	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes())
	assert.Equal(t, float64(-1), frames[0].header["total"], "InfoSize failure must give total=-1")
	assert.Equal(t, content, frames[1].body, "data stream is not affected by InfoSize failure")
}

// infoFailRouter OpenRange works but InfoSize errors (aligned with PeerSource/URLSource:
// no metadata cache, stream only).
type infoFailRouter struct {
	content []byte
	hash    string
}

func (f *infoFailRouter) OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if hash != f.hash {
		return nil, fmt.Errorf("not found")
	}
	off := offset
	if off < 0 {
		off = 0
	}
	if off > int64(len(f.content)) {
		off = int64(len(f.content))
	}
	length := size
	if length < 0 || off+length > int64(len(f.content)) {
		length = int64(len(f.content)) - off
	}
	return io.NopCloser(bytes.NewReader(f.content[off : off+length])), nil
}

func (f *infoFailRouter) InfoSize(ctx context.Context, hash string) (int64, error) {
	return 0, fmt.Errorf("no info size for %s", hash)
}

// Test utility os wrappers (to avoid noise from top-of-file os/path imports).
func osMkdirAll(t *testing.T, dir string) error {
	t.Helper()
	return mkdirAllForTest(dir)
}

func osWriteFile(t *testing.T, path string, content []byte) error {
	t.Helper()
	return writeFileForTest(path, content)
}

// mkdirAllForTest/writeFileForTest thin wrappers (used by TestServeFile_NoRouterFallback).
func mkdirAllForTest(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func writeFileForTest(path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}

var _ = json.Marshal // retain json import (fakeSession.feed uses peerjs.Frame construction)
