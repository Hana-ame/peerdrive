package transport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
)

// fakeSession an in-memory Session: records the JSON frames it sends (header + body), and can capture the
// OnMessage callback so frames can be injected by hand (used by the H1/H6/M6 unit tests, the streaming
// OpenStream, and the forward tests). Close fires the OnClose callback + marks closed (simulating the
// real connection-close cleanup path -- the bindConn same-peer dedup test relies on this behaviour).
type fakeSession struct {
	id   string
	mu   sync.Mutex
	sent []map[string]any
	// frames full frame records (including SendFrame's binary body) -- used by the forward data-passthrough assertions
	frames []fakeFrame

	// local = a direct local WS connection ("ourselves"), see isSelfSession in share.go
	local bool

	onMessage func(peerjs.Frame)
	onClose   func()
	closed    bool
}

// IsLocal implements the optional interface recognised by isSelfSession (only local WS sessions have it).
func (f *fakeSession) IsLocal() bool { return f.local }

// fakeFrame a full record of one frame (JSON header + optional binary body).
type fakeFrame struct {
	header map[string]any
	body   []byte
}

func (f *fakeSession) ID() string { return f.id }
func (f *fakeSession) SendJSON(v any) error {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.frames = append(f.frames, fakeFrame{header: m})
	f.mu.Unlock()
	return nil
}
func (f *fakeSession) SendFrame(header any, body []byte) error {
	b, _ := json.Marshal(header)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	f.mu.Lock()
	f.sent = append(f.sent, m)
	f.frames = append(f.frames, fakeFrame{header: m, body: body})
	f.mu.Unlock()
	return nil
}
func (f *fakeSession) OnMessage(fn func(peerjs.Frame)) {
	f.mu.Lock()
	f.onMessage = fn
	f.mu.Unlock()
}
func (f *fakeSession) OnClose(fn func()) {
	f.mu.Lock()
	f.onClose = fn
	f.mu.Unlock()
}
func (f *fakeSession) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	fn := f.onClose
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Closed reports whether the session has been Closed (the dedup tests assert the old connection was closed).
func (f *fakeSession) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// feed manually injects one frame into the OnMessage callback (simulating a frame arriving from the peer).
func (f *fakeSession) feed(frame peerjs.Frame) {
	f.mu.Lock()
	fn := f.onMessage
	f.mu.Unlock()
	if fn != nil {
		fn(frame)
	}
}

// sentFrames returns a copy of the frames sent so far (including bodies).
func (f *fakeSession) sentFrames() []fakeFrame {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeFrame(nil), f.frames...)
}

func (f *fakeSession) sentFrameTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.frames))
	for _, fr := range f.frames {
		if t, ok := fr.header["type"].(string); ok {
			out = append(out, t)
		}
	}
	return out
}

// sentTypes returns the sequence of frame types sent so far.
func (f *fakeSession) sentTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, m := range f.sent {
		if t, ok := m["type"].(string); ok {
			out = append(out, t)
		}
	}
	return out
}

func newTestPeerJSService(t *testing.T) *PeerJSService {
	t.Helper()
	return newTestPeerJSServiceWithIndex(t, NewFileIndexService(t.TempDir()))
}

func newTestPeerJSServiceWithIndex(t *testing.T, idx *FileIndexService) *PeerJSService {
	t.Helper()
	t.Cleanup(idx.Close) // Windows: without closing the upload session's handles, TempDir cannot be cleaned up
	return &PeerJSService{
		cfg:          &config.Config{},
		storageDir:   t.TempDir(),
		conns:        map[string]Session{},
		pending:      map[Session]*connState{},
		connecting:   map[string]struct{}{},
		forwardRules: map[string][]int{},
		fwNonces:     map[string]*fwdNonce{},
		fileIndex:    idx,
		ctx:          context.Background(),
	}
}

// TestServeFile_InvalidHashNoPanic an illegal hash (empty / short) -> an err frame, no panic.
// Discovery background: the H1 remote-crash bug -- serveFile indexed req.Hash[:2] directly, so a peer
// sending {"type":"req","hash":""} or "a" caused an out-of-bounds panic and killed the process (on the
// public signaling network, any one node could crash every node with a single line of JSON). Fix:
// validate with isValidHash first.
func TestServeFile_InvalidHashNoPanic(t *testing.T) {
	svc := newTestPeerJSService(t)
	for _, bad := range []string{"", "a", "abc", "not-hex!"} {
		sess := &fakeSession{id: "remote"}
		svc.serveFile(sess, dcReq{Type: "req", Hash: bad, ReqID: "r1"})
		types := sess.sentTypes()
		require.Len(t, types, 1, "hash=%q should return exactly one err frame", bad)
		assert.Equal(t, "err", types[0], "hash=%q", bad)
	}
}

// TestServeFile_PrivateDeniedToStrangers private content: strangers cannot fetch it, friends / ourselves can
// (doc/NETDISK.md §12.6).
//
// Why pin this in the transport layer rather than testing the service only: the gate wiring lives inside
// serveFile (a nil gate lets everything through), and "installing a gate but never calling it on a req" is
// exactly the kind of mistake that shows all green in the service alone while leaking everything in
// production.
func TestServeFile_PrivateDeniedToStrangers(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	content := []byte("private-content")
	inRoot := filepath.Join(svc.fileIndex.uploadDir, "secret.bin")
	require.NoError(t, os.MkdirAll(svc.fileIndex.uploadDir, 0o755))
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	// fake gate: only hash==fi.Hash counts as private, and the friend list contains only "buddy"
	svc.SetShareGate(fakeShareGate{private: fi.Hash, friends: []string{"buddy"}})

	stranger := &fakeSession{id: "stranger"}
	svc.serveFile(stranger, dcReq{Type: "req", Hash: fi.Hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"err"}, stranger.sentTypes(), "private must not be sent to strangers")

	friend := &fakeSession{id: "buddy"}
	svc.serveFile(friend, dcReq{Type: "req", Hash: fi.Hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"meta", "data", "done"}, friend.sentFrameTypes(), "friends must be able to fetch")

	self := &fakeSession{id: "whatever", local: true}
	svc.serveFile(self, dcReq{Type: "req", Hash: fi.Hash, Size: -1, ReqID: "r1"})
	require.Equal(t, []string{"meta", "data", "done"}, self.sentFrameTypes(), "self (local channel) must be able to fetch")
}

// fakeShareGate a fake download gate (one private hash + one friend list).
type fakeShareGate struct {
	private string
	friends []string
}

func (g fakeShareGate) AllowsDownload(peerID, hash string, self bool) bool {
	if self || hash != g.private {
		return true
	}
	for _, f := range g.friends {
		if f == peerID {
			return true
		}
	}
	return false
}

// TestServeFile_IndexPathOutsideRoot the index matches but the path is out of bounds -> err frame, no file
// outside the root is served (H2). Discovery background: the H2 arbitrary-file-read vulnerability -- a peer
// creates an arbitrary absolute path and then req-reads it. Defensive test: simulating historical dirty data
// (an out-of-root path already upserted into the index), serveFile must refuse.
func TestServeFile_IndexPathOutsideRoot(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)

	// put a file inside the root and register it normally
	inRoot := filepath.Join(svc.fileIndex.uploadDir, "in.bin")
	require.NoError(t, os.MkdirAll(svc.fileIndex.uploadDir, 0o755))
	content := []byte("inside-root")
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	// simulate historical dirty data: rewrite the indexed path to an out-of-root file
	outside := filepath.Join(t.TempDir(), "shadow.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	_, err = repository.UpsertFileIndex(fi.Hash, outside, "shadow.txt", 6, false)
	require.NoError(t, err)

	// request that hash: must be refused, no out-of-root content may be returned
	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: fi.Hash, ReqID: "r1"})
	types := sess.sentTypes()
	require.Len(t, types, 1)
	assert.Equal(t, "err", types[0], "out-of-root paths must not be returned")
}

// TestServeFile_IndexPathAllowed the fallback branch: a file_index hit on a legal path -> that path's file
// is served (content addressing is only a backstop; a created file has just an absolute path + the index).
// Discovery background: during the 2026-08-18 serveFile multi-source routing rewrite the file_index branch
// was lost (a routing miss only queried CAS), and the integration test TestFrameVerbs_CreateListInfoDownload
// reported not found; once restored, this test pins down "an index hit on a legal path must be served".
func TestServeFile_IndexPathAllowed(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	content := []byte("indexed-path-content")
	inRoot := filepath.Join(svc.fileIndex.uploadDir, "in.bin")
	require.NoError(t, os.MkdirAll(svc.fileIndex.uploadDir, 0o755))
	require.NoError(t, os.WriteFile(inRoot, content, 0o644))
	fi, err := svc.fileIndex.Create(inRoot)
	require.NoError(t, err)

	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: fi.Hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes())
	assert.Equal(t, float64(len(content)), frames[0].header["total"])
	assert.Equal(t, content, frames[1].body)
}

// TestRouteResponse_DataSizeCap a malicious data frame declaring a huge size -> errCh (H6).
// Discovery background: H6 -- f.size = r.Size had no cap, so a malicious peer declaring 1<<62 and
// streaming data frames made f.got grow without bound until OOM. Fix: an 8GB cap.
func TestRouteResponse_DataSizeCap(t *testing.T) {
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	f := newTestFetchState("r1")
	st.fetches["r1"] = f

	svc.routeResponse(st, dcResp{Type: "data", ReqID: "r1", Size: 1 << 62}, nil)
	select {
	case err := <-f.errCh:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("data frame exceeding limit must error")
	}
}

// TestRouteResponse_DoneSizeMismatch the peer sends done early (a truncated file counted as success) -> errCh (H6).
// Discovery background: H6 -- done did not check the bytes actually received, so a peer sending only
// meta+done returned empty or truncated data as success -> silent data corruption. Fix: compare done.Size
// against the bytes actually received.
func TestRouteResponse_DoneSizeMismatch(t *testing.T) {
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	f := newTestFetchState("r1")
	st.fetches["r1"] = f

	// declares 100 bytes sent, but 0 actually received (no data frame) -> must error
	svc.routeResponse(st, dcResp{Type: "done", ReqID: "r1", Size: 100}, nil)
	select {
	case err := <-f.errCh:
		assert.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("truncated done must error")
	}
	select {
	case <-f.done:
		t.Fatal("must not treat truncated data as success")
	default:
	}
}

// TestRouteResponse_DoneSizeMatch a normal done (Size matches what was received) -> success.
func TestRouteResponse_DoneSizeMatch(t *testing.T) {
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	f := newTestFetchState("r1")
	st.fetches["r1"] = f

	svc.routeResponse(st, dcResp{Type: "data", ReqID: "r1", Size: 3}, nil)
	f.received = 3
	svc.routeResponse(st, dcResp{Type: "done", ReqID: "r1", Size: 3}, nil)
	select {
	case <-f.done:
		// done closed = the transfer is complete (streaming semantics: data is consumed through f.q)
	case <-time.After(time.Second):
		t.Fatal("matching done should return success")
	}
}

// newTestFetchState builds a fetchState for tests (every streaming field initialised).
func newTestFetchState(reqID string) *fetchState {
	return &fetchState{
		reqID:  reqID,
		q:      make(chan []byte, 8),
		done:   make(chan struct{}),
		errCh:  make(chan error, 1),
		closed: make(chan struct{}),
	}
}

// TestServeUploadBegin_StalePendingCleared a stale upload-header placeholder times out -> cleared automatically (M6).
// Discovery background: M6 -- when a peer sent an upload header and never sent the data blocks,
// pendingUpload was held forever, so every later upload on that connection got "already in progress" (a
// connection-level DoS that only a reconnect cleared).
func TestServeUploadBegin_StalePendingCleared(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	st := &connState{fetches: make(map[string]*fetchState)}
	sess := &fakeSession{id: "remote"}

	// a stale placeholder (created 31s ago)
	st.mu.Lock()
	st.pendingUpload = &uploadState{reqID: "stale", created: time.Now().Add(-31 * time.Second)}
	st.mu.Unlock()

	svc.serveUploadBegin(sess, st, dcResp{Type: "upload", Name: "new.bin", Size: 10, ReqID: "r2"})
	types := sess.sentTypes()
	require.Len(t, types, 1)
	assert.Equal(t, "meta", types[0], "stale placeholder should be cleared and upload starts normally")

	st.mu.Lock()
	assert.NotNil(t, st.pendingUpload, "new upload should occupy the slot")
	st.mu.Unlock()
}

// TestUploadWorker_WriteThenComplete the upload worker writes to disk + replies with the completion frame (the H5 chain).
// Discovery background: H5 -- the WriteAt/Complete for binary frames moved out of the message pump into a
// connection-level worker; this test drives the worker directly to verify the whole chain: chunk delivery ->
// disk write -> the uploaded reply frame.
func TestUploadWorker_WriteThenComplete(t *testing.T) {
	initTestDB(t)
	svc := newTestPeerJSService(t)
	st := &connState{
		fetches: make(map[string]*fetchState),
		binCh:   make(chan binaryChunk, 1),
		binDone: make(chan struct{}),
	}
	sess := &fakeSession{id: "remote"}

	content := make([]byte, 10)
	for i := range content {
		content[i] = byte(i)
	}
	us, err := svc.fileIndex.BeginUpload("w.bin", 10)
	require.NoError(t, err)
	up := &uploadState{reqID: "r1", offset: 0, size: 10, sess: us}

	// one worker handles a single chunk (last=true -> Complete)
	done := make(chan struct{})
	go func() {
		svc.uploadWorker(sess, st)
		close(done)
	}()
	st.binCh <- binaryChunk{up: up, offset: 0, data: content, last: true}

	// wait for the worker to reply with the uploaded frame (polling + timeout; binDone must not be
	// closed immediately -- the select's random branch choice would drop an unprocessed chunk outright)
	var types []string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		types = sess.sentTypes()
		if len(types) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(st.binDone)
	<-done

	require.Len(t, types, 1)
	assert.Equal(t, "uploaded", types[0], "all chunks collected should reply with uploaded")
}

// TestHashMatchesSHA256_AllowsEmptyFile an empty file also passes the content-addressed check.
// Discovery background: code review -- the original implementation did `len(data)==0` and returned false
// straight away, so a legitimate empty file (sha256 of empty = e3b0c442...) could never be pulled from a
// peer. Fix: remove the empty-data special case; an empty file is checked only against its real sha256.
func TestHashMatchesSHA256_AllowsEmptyFile(t *testing.T) {
	emptyHash := sha256.Sum256([]byte{})
	assert.True(t, hashMatchesSHA256(hex.EncodeToString(emptyHash[:]), []byte{}), "sha256 of empty file should be accepted")
}
