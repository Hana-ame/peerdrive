package source

// peer_test.go: PeerSource racing pull tests (2026-08-19 concurrent-race redesign).
// Uses a real PeerJSService + BindLocal to inject any number of in-memory fake Session peers,
// verifying: multiple peers issue req concurrently (real concurrency) -> the first responder wins
// -> all losing streams are reaped and closed (fetch state cleaned up, peer stream mutex released,
// late frames silently ignored). Coverage: two-peer/three-peer/partial failure/busy peer skipped/
// multi-block reassembly/winner lock reuse/err frame. Discovery background: trying peers serially
// let the first slow peer stall the whole origin fetch; it was redesigned as concurrent racing.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// fakePeerSess an in-memory Session (for source package tests; equivalent to transport's fakeSession).
type fakePeerSess struct {
	id string

	mu        sync.Mutex
	failSend  bool // when true, SendJSON errors (simulates a peer frame-send failure / disconnect)
	sentReq   string
	onMessage func(peerjs.Frame)
	onClose   func()
}

func newFakePeerSess(id string) *fakePeerSess { return &fakePeerSess{id: id} }

func (f *fakePeerSess) ID() string { return f.id }
func (f *fakePeerSess) SendJSON(v any) error {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if t, _ := m["type"].(string); t == "req" {
		r, _ := m["reqId"].(string)
		f.mu.Lock()
		f.sentReq = r
		fail := f.failSend
		f.mu.Unlock()
		if fail {
			return errSendFail
		}
	}
	return nil
}
func (f *fakePeerSess) SendFrame(header any, body []byte) error { return f.SendJSON(header) }
func (f *fakePeerSess) OnMessage(fn func(peerjs.Frame)) {
	f.mu.Lock()
	f.onMessage = fn
	f.mu.Unlock()
}
func (f *fakePeerSess) OnClose(fn func()) {
	f.mu.Lock()
	f.onClose = fn
	f.mu.Unlock()
}

// Close simulates a peer disconnect: it triggers bindConn's OnClose cleanup (errCh delivery +
// fetch state cleared + binDone closed) -- the same semantics as a real WSSession.Close.
func (f *fakePeerSess) Close() {
	f.mu.Lock()
	fn := f.onClose
	f.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// feed injects one frame into bindConn's pump (the real routeResponse path).
func (f *fakePeerSess) feed(frame peerjs.Frame) {
	f.mu.Lock()
	fn := f.onMessage
	f.mu.Unlock()
	if fn != nil {
		fn(frame)
	}
}

func (f *fakePeerSess) reqID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sentReq
}

func testPeerHash(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

// waitReqs waits until every peer has received a req frame (a race must be issued concurrently to all candidate peers).
func waitReqs(t *testing.T, sess ...*fakePeerSess) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, s := range sess {
			if s.reqID() == "" {
				all = false
				break
			}
		}
		if all {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("not all peers received req within deadline")
}

// feedFullResponse injects a complete meta/data/done response into a peer (single block, payload is content).
func feedFullResponse(t *testing.T, s *fakePeerSess, hash string, content []byte) {
	t.Helper()
	feedFullResponseBlocks(t, s, hash, [][]byte{content})
}

// feedFullResponseBlocks injects a complete response into a peer (multiple blocks: each block is
// one data header + a binary chunk, simulating real 64KB chunked transfer; done wraps it up).
func feedFullResponseBlocks(t *testing.T, s *fakePeerSess, hash string, blocks [][]byte) {
	t.Helper()
	rid := s.reqID()
	require.NotEmpty(t, rid, "peer must have received req frame")
	total := 0
	for _, b := range blocks {
		total += len(b)
	}
	size := strconv.Itoa(total)
	s.feed(peerjsFrameText(`{"type":"meta","total":` + size + `,"reqId":"` + rid + `"}`))
	for _, b := range blocks {
		s.feed(peerjsFrameText(`{"type":"data","size":` + strconv.Itoa(len(b)) + `,"reqId":"` + rid + `"}`))
		// the data chunk takes the pump's binary branch (st.expect was already set by the data header)
		s.feed(peerjs.Frame{IsText: false, Data: b})
	}
	s.feed(peerjsFrameText(`{"type":"done","size":` + size + `,"reqId":"` + rid + `"}`))
}

// feedErrResponse injects an err frame into a peer (a read-time failure: a race only guarantees the
// stream is established, and data failures surface at Read time).
func feedErrResponse(t *testing.T, s *fakePeerSess, msg string) {
	t.Helper()
	rid := s.reqID()
	require.NotEmpty(t, rid, "peer must have received req frame")
	s.feed(peerjsFrameText(`{"type":"err","msg":"` + msg + `","reqId":"` + rid + `"}`))
}

// setFailSends makes the given peers fail to send frames (simulates a disconnect / frame-send error).
func setFailSends(sess ...*fakePeerSess) {
	for _, s := range sess {
		s.mu.Lock()
		s.failSend = true
		s.mu.Unlock()
	}
}

// waitPeersIdle waits until at least want peers' race locks are idle (they may enter a race again).
//
// Why we must wait: raceOpen returns as soon as one wins, and **a losing candidate goroutine may
// still be in flight** (its stream is closed and its lock released by a background reaping
// goroutine; see the raceOpen comment in peer.go). Starting the next race right after the return
// makes the losing peers fail TryLock and get skipped by collectPeers -> only one candidate
// remains -> the single-peer serial path -> if that peer happens to be set to failSend, Open
// reports "peer <id>: ...", which is entirely off from the "is the lock released" premise.
//
// Discovery background: this case failed intermittently (locally with -count=400 about 1%~2%, so
// about a 1/4 chance of red on CI macOS/arm64) -- a third race started directly after the second
// returned, and the second was a race: the third collectPeers saw peerB still BUSY -> single path
// to peerA (already failSend) -> assert.AnError. The first round had been masked by a fixed
// sleep(50ms); the second and third had no such mask.
//
// Why probe with TryLock instead of a fixed sleep: acquire and release immediately, which is both
// deterministic and does not slow the test down; a real lock leak would never be satisfied within
// the deadline -> an explicit Fatal (exactly the semantic this test wants to guard: after a race,
// every peer slot must eventually be released).
func waitPeersIdle(t *testing.T, ps *PeerSource, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		pids, locks := ps.collectPeers()
		for _, l := range locks {
			l.Unlock() // return it immediately: this call only probes for idleness, it does not occupy a slot
		}
		if len(pids) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("peers not idle within deadline: %d idle, want %d (suspected peer slot leak)", len(pids), want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// newRaceSvc creates the service and a set of peers for race tests.
func newRaceSvc(t *testing.T, ids ...string) (*transport.PeerJSService, []*fakePeerSess, *PeerSource) {
	t.Helper()
	svc := transport.NewPeerJSService(&config.Config{}, t.TempDir())
	sess := make([]*fakePeerSess, 0, len(ids))
	for _, id := range ids {
		s := newFakePeerSess(id)
		svc.BindLocal(s)
		sess = append(sess, s)
	}
	return svc, sess, NewPeerSource(svc)
}

func peerjsFrameText(s string) peerjs.Frame { return peerjs.Frame{IsText: true, Data: []byte(s)} }

// errSendFail the fakeSession send-failure error (used by failure-path tests).
var errSendFail = assert.AnError

// TestPeerSource_RaceWinsFastest two-peer race: data is complete, the losing stream is reaped
// (fetch state cleaned up), late frames are silently ignored. Race outcomes are uncertain
// (Connections map iteration order is random and goroutine scheduling is not fixed) -- so the
// response is fed to every candidate peer: whoever wins can read it, and the losers' late frames
// are ignored.
func TestPeerSource_RaceWinsFastest(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]

	content := bytes.Repeat([]byte("race-data-"), 64)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, a, b)

	// feed every candidate peer: the winner gets the data, the losers' late frames are silently ignored
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "winner stream data must be complete")

	// the loser has been reaped: give the reaping goroutine time, its fetch state must already be
	// cleaned up; extra late frames fed afterwards (a repeated feed after done) must not re-register state
	time.Sleep(50 * time.Millisecond)
	for _, s := range sess {
		require.Empty(t, svc.PendingFetchesForTest(s), "loser %s should not have residual fetch state", s.id)
		feedFullResponse(t, s, hash, content) // late response: passing means no panic
		time.Sleep(20 * time.Millisecond)
		require.Empty(t, svc.PendingFetchesForTest(s), "late frames must not re-register fetch state", s.id)
	}
}

// TestPeerSource_RaceAllFail all peers fail -> aggregated error, peer stream mutexes released
// (a later Open still works and is not stuck on TryLock).
func TestPeerSource_RaceAllFail(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]
	setFailSends(a, b)

	hash := testPeerHash([]byte("x"))

	_, err := ps.Open(context.Background(), hash, 0, -1)
	require.Error(t, err, "all failure must report error")
	// both peers should have received req (issued concurrently)
	require.NotEmpty(t, a.reqID())
	require.NotEmpty(t, b.reqID())
	require.Empty(t, svc.PendingFetchesForTest(a), "failure path must not have residual fetch state")
	require.Empty(t, svc.PendingFetchesForTest(b))
}

// TestPeerSource_RaceThreePeers three-peer race: all peers receive req (real concurrency), the
// winner stream's data is complete; all losers are reaped (fetch state cleaned up), late frames
// silently ignored; another Open after reaping succeeds (no peer lock leaked).
func TestPeerSource_RaceThreePeers(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")

	content := bytes.Repeat([]byte("three-race-"), 100)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// feed the response to all candidate peers: the winner reads the data, the losers' late frames are ignored
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "winner stream data must be complete")

	// all losers have been reaped: fetch state cleaned up + late frames silently ignored
	time.Sleep(50 * time.Millisecond)
	for _, s := range sess {
		require.Empty(t, svc.PendingFetchesForTest(s), "loser %s should not have residual fetch state", s.id)
		feedFullResponse(t, s, hash, content) // late response: passing means no panic
		time.Sleep(20 * time.Millisecond)
		require.Empty(t, svc.PendingFetchesForTest(s), "late frames must not re-register fetch state", s.id)
	}

	// no lock leaked: start another race (all three peers still online), feed the response to all -> success
	r2, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r2.Close()
	waitReqs(t, sess...)
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	got2, err := readAllTimeout(r2)
	require.NoError(t, err)
	assert.Equal(t, content, got2, "second round race winner stream data must be complete")
}

// TestPeerSource_RaceMixedFailSuccess partial failure + partial success: a failed peer releases
// its lock immediately (does not block a later Open), and the succeeding peers race normally.
func TestPeerSource_RaceMixedFailSuccess(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")
	a, b, c := sess[0], sess[1], sess[2]
	setFailSends(a)

	content := bytes.Repeat([]byte("mixed-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// B and C receive req (issued concurrently), A fails to send its frame
	waitReqs(t, b, c)
	time.Sleep(20 * time.Millisecond)
	require.Empty(t, svc.PendingFetchesForTest(a), "failed peer must not have residual fetch state")

	// feed the response to the succeeding peers B and C: the winner reads the data, the loser's late frames are ignored
	for _, s := range []*fakePeerSess{b, c} {
		feedFullResponse(t, s, hash, content)
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "mixed scenario winner stream data must be complete")

	// the loser (either B or C) is reaped
	time.Sleep(50 * time.Millisecond)
	require.Empty(t, svc.PendingFetchesForTest(a), "failed peer must not have residual fetch state")

	// A's lock was released by the failure path -> after clearing failSend, A racing alone must succeed
	a.mu.Lock()
	a.failSend = false
	a.mu.Unlock()
	setFailSends(b, c)
	r2, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r2.Close()
	waitReqs(t, a)
	feedFullResponse(t, a, hash, content)
	got2, err := readAllTimeout(r2)
	require.NoError(t, err)
	assert.Equal(t, content, got2, "after failed peer lock release, must be able to race and win again")
}

// TestPeerSource_RaceBusyPeerSkipped busy peer skipped: a peer that already has a stream (its
// mutex occupied) -> receives no req and does not block the race; the other peers win normally.
func TestPeerSource_RaceBusyPeerSkipped(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]

	// simulate peerA already having a stream in progress (the connection-level expect single slot): take its mutex directly
	muI, _ := ps.peerLocks.LoadOrStore(a.id, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	content := bytes.Repeat([]byte("busy-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()

	// only B receives req; A is skipped (a TryLock failure does not wait)
	waitReqs(t, b)
	time.Sleep(20 * time.Millisecond)
	assert.Empty(t, a.reqID(), "busy peer must be skipped (no req sent)")
	assert.Empty(t, svc.PendingFetchesForTest(a), "busy peer must not have residual fetch state")

	feedFullResponse(t, b, hash, content)
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "busy peer skipped scenario winner stream data must be complete")
}

// TestPeerSource_MultiBlockTransfer multi-block reassembly on the winner stream: data over 64KB
// transferred in 3 blocks (meta + data×3 + done) must be reassembled completely after winning.
func TestPeerSource_MultiBlockTransfer(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	// 3 blocks: 64KB + 64KB + a tail block (real block granularity)
	k := 1024
	block1 := bytes.Repeat([]byte("x"), 64*k)
	block2 := bytes.Repeat([]byte("y"), 64*k)
	block3 := bytes.Repeat([]byte("z"), 1024)
	content := append(append(append([]byte{}, block1...), block2...), block3...)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// feed the multi-block response to all candidate peers (the winner is uncertain, so feeding all guarantees the winner receives it)
	for _, s := range sess {
		feedFullResponseBlocks(t, s, hash, [][]byte{block1, block2, block3})
	}
	got, err := readAllTimeout(r)
	require.NoError(t, err)
	assert.Equal(t, content, got, "multi-block transfer must be reassembled completely in order")
	assert.Equal(t, len(content), len(got))
}

// TestPeerSource_WinnerPeerLockReleased all peer locks released after a race: the first race is
// closed immediately after establishment (the winner's lock released by Close, the loser's by
// reaping, so each of the two locks goes through "occupy -> release" once); then failSend narrows
// competition to a single peer and each peer's lock release is verified (a leaked lock means
// TryLock fails -> that peer is skipped and errors out).
//
// waitPeersIdle is mandatory between rounds: when a race returns, a losing candidate goroutine may
// still be in flight holding that peer slot (see the waitPeersIdle comment and its discovery
// background there).
func TestPeerSource_WinnerPeerLockReleased(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")
	a, b := sess[0], sess[1]

	content := bytes.Repeat([]byte("lock-race-"), 32)
	hash := testPeerHash(content)

	// round 1: close right after the race is established without reading data -- both A's and B's locks go through a release
	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, a, b)
	require.NoError(t, r.Close())
	waitPeersIdle(t, ps, 2) // wait for the reaping goroutine to finish (loser's lock released)

	// round 2: A alone (B failSend) -> success means A's lock was released (A may have been the winner or the loser)
	setFailSends(b)
	r2, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, a)
	feedFullResponse(t, a, hash, content)
	got, err := readAllTimeout(r2)
	require.NoError(t, err)
	assert.Equal(t, content, got, "A's lock must be released (can race alone)")
	require.NoError(t, r2.Close())
	waitPeersIdle(t, ps, 2) // round 2 is also a race: on return the loser candidate may still be in flight

	// round 3: B alone (A failSend) -> success means B's lock was released
	setFailSends(a)
	b.mu.Lock()
	b.failSend = false
	b.mu.Unlock()
	r3, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	waitReqs(t, b)
	feedFullResponse(t, b, hash, content)
	got3, err := readAllTimeout(r3)
	require.NoError(t, err)
	assert.Equal(t, content, got3, "B's lock must be released (can race alone)")
	require.NoError(t, r3.Close())
}

// TestPeerSource_WinnerErrFrame the winner stream's peer replies with an err frame: error at read
// time (a race only guarantees the stream is established; a data failure surfaces at Read -- no
// silent return of bad data). The winner is uncertain, so the err frame is fed to every candidate
// peer, which makes the winner's err take effect for sure.
func TestPeerSource_WinnerErrFrame(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	hash := testPeerHash([]byte("err-frame"))

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	for _, s := range sess {
		feedErrResponse(t, s, "boom")
	}
	_, err = readAllTimeout(r)
	require.Error(t, err, "winner stream receiving err frame must report error")
	assert.Contains(t, err.Error(), "boom")
}

// TestPeerSource_NoConnections no online peers: a clear error (the end of the fallback chain:
// before the upper-layer Manager gets the aggregated error, PeerSource itself must give a
// diagnosable reason).
func TestPeerSource_NoConnections(t *testing.T) {
	_, _, ps := newRaceSvc(t) // bind no peers at all

	_, err := ps.Open(context.Background(), testPeerHash([]byte("x")), 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no online peer available")
}

// TestPeerSource_OpenAfterSvcClose Open after the service is closed: all connections are released
// -> no-online-peer error (must not hang or panic).
func TestPeerSource_OpenAfterSvcClose(t *testing.T) {
	svc, _, ps := newRaceSvc(t, "peerA", "peerB")
	svc.Close() // close the service: conns cleared + session Close (fake Close triggers OnClose)

	_, err := ps.Open(context.Background(), testPeerHash([]byte("x")), 0, -1)
	require.Error(t, err, "Open after service close must report error")
	assert.Contains(t, err.Error(), "no online peer available")
}

// TestPeerSource_ReadAfterSvcClose service closed after a race is established: ctx cancel at read
// time + connection cleanup -> error (no hang, no silent truncation counted as success).
func TestPeerSource_ReadAfterSvcClose(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB")

	content := bytes.Repeat([]byte("shutdown-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// feed data into q (the read-side buffer), then close the service
	for _, s := range sess {
		feedFullResponse(t, s, hash, content)
	}
	time.Sleep(30 * time.Millisecond) // let the chunks be delivered into q
	svc.Close()                       // service closed -> ctx cancel + session Close

	_, err = readAllTimeout(r)
	require.Error(t, err, "reading after service close must report error (cannot treat buffered data as complete result)")
}

// TestPeerSource_WinnerPeerDropped a peer drops mid-transfer after winning a race: error at read
// time, never return truncated data as success (content-addressed semantics: bad data must fail
// explicitly).
func TestPeerSource_WinnerPeerDropped(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	content := bytes.Repeat([]byte("drop-race-"), 32)
	hash := testPeerHash(content)

	r, err := ps.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	defer r.Close()
	waitReqs(t, sess...)

	// feed only part of the data (meta + half a file), then drop all peers -- the winner stream
	// has not received everything, so it must error rather than return partial data
	for _, s := range sess {
		rid := s.reqID()
		require.NotEmpty(t, rid)
		size := strconv.Itoa(len(content))
		s.feed(peerjsFrameText(`{"type":"meta","total":` + size + `,"reqId":"` + rid + `"}`))
		s.feed(peerjsFrameText(`{"type":"data","size":` + strconv.Itoa(len(content)) + `,"reqId":"` + rid + `"}`))
		s.feed(peerjs.Frame{IsText: false, Data: content})
		// no done frame -- simulates a peer going offline mid-transfer
	}
	time.Sleep(30 * time.Millisecond)
	for _, s := range sess {
		s.Close() // peer disconnects: bindConn OnClose -> errCh + fetch state cleanup
	}

	_, err = readAllTimeout(r)
	require.Error(t, err, "peer disconnecting mid-transfer must report error (do not treat truncated data as success)")
}

// TestPeerSource_AllFailErrorDetail all-fail error aggregation: every peer's failure reason must
// appear in the final error (so falling back to the upper-layer source still shows which peer
// died and how).
func TestPeerSource_AllFailErrorDetail(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")
	setFailSends(sess...)

	_, err := ps.Open(context.Background(), testPeerHash([]byte("x")), 0, -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all peers failed")
	// every failed peer's reason is aggregated into the error (3 peers = 3 reasons)
	assert.Equal(t, 3, strings.Count(err.Error(), errSendFail.Error()),
		"aggregated error must include every peer's failure reason")
}

// waitLockFree waits until the given peer's stream mutex can be acquired (the racing goroutines
// are asynchronous: Open returning does not mean all candidate goroutines have finished, and a
// failed peer's unlock may come after the return; over consecutive race rounds the next round's
// TryLock collides with an unreleased lock -- discovery background:
// TestPeerSource_ParallelStreamsAcrossPeers with -count=5 failed intermittently, and the error
// "peer peerA: ..." showed the previous round's loser had not released its lock yet). Only this
// round's candidate locks are probed: the winner's lock is deliberately held by the reader until
// the test ends, so we cannot wait for every lock to be idle. TryLock probe + immediate release,
// with no side effects.
func waitLockFree(t *testing.T, ps *PeerSource, s *fakePeerSess) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		muI, _ := ps.peerLocks.LoadOrStore(s.id, &sync.Mutex{})
		mu := muI.(*sync.Mutex)
		if mu.TryLock() {
			mu.Unlock()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer lock %s not released within deadline", s.id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestPeerSource_ParallelStreamsAcrossPeers parallel streams across connections: 3 peers = 3
// connections, with 3 pulls issued at the same time (each round narrows to a single peer via
// failSend) -- peer stream mutexes are isolated per peer, so parallel connections do not block
// each other; each one's data is complete.
func TestPeerSource_ParallelStreamsAcrossPeers(t *testing.T) {
	svc, sess, ps := newRaceSvc(t, "peerA", "peerB", "peerC")
	a, b, c := sess[0], sess[1], sess[2]

	contents := [][]byte{
		bytes.Repeat([]byte("parallel-a-"), 32),
		bytes.Repeat([]byte("parallel-b-"), 32),
		bytes.Repeat([]byte("parallel-c-"), 32),
	}
	hashes := []string{
		testPeerHash(contents[0]),
		testPeerHash(contents[1]),
		testPeerHash(contents[2]),
	}

	type job struct {
		sess *fakePeerSess
		hash string
		body []byte
	}
	jobs := []job{{a, hashes[0], contents[0]}, {b, hashes[1], contents[1]}, {c, hashes[2], contents[2]}}

	// each round keeps only one candidate peer (the rest failSend): the three rounds run in parallel
	// without interfering with each other. Wait for the lock to be idle before each round (the
	// previous round's racing goroutine may not have finished).
	var readers []io.ReadCloser
	for _, j := range jobs {
		waitLockFree(t, ps, j.sess)
		for _, s := range sess {
			s.mu.Lock()
			s.failSend = s != j.sess
			s.mu.Unlock()
		}
		r, err := ps.Open(context.Background(), j.hash, 0, -1)
		require.NoError(t, err, "%s pull establishment failed", j.sess.id)
		readers = append(readers, r)
		waitReqs(t, j.sess)
	}
	defer func() {
		for _, r := range readers {
			r.Close()
		}
	}()

	// inject responses into all three streams at once -> each one wins on its own
	for _, j := range jobs {
		feedFullResponse(t, j.sess, j.hash, j.body)
	}
	for i, r := range readers {
		got, err := readAllTimeout(r)
		require.NoError(t, err, "%s read failed", jobs[i].sess.id)
		assert.Equal(t, jobs[i].body, got, "%s data must be complete", jobs[i].sess.id)
	}
	// no residual state after everything finishes (locks + fetch all released)
	for _, s := range sess {
		require.Empty(t, svc.PendingFetchesForTest(s), "after parallel streams end %s must not have residual fetch state", s.id)
	}
}

// readAllTimeout io.ReadAll plus a timeout against hangs (guards racing tests against a reader that never finishes).
func readAllTimeout(r io.ReadCloser) ([]byte, error) {
	done := make(chan struct{})
	var (
		buf []byte
		err error
	)
	go func() {
		buf, err = io.ReadAll(r)
		close(done)
	}()
	select {
	case <-done:
		return buf, err
	case <-time.After(3 * time.Second):
		return nil, assert.AnError
	}
}

// TestPeerReadCloserCloseIdempotent verifies peerReadCloser.Close can be called repeatedly: the
// underlying reader is closed only once, and the peer stream mutex is not Unlocked a second time
// (which would panic). Discovery background: another review on 2026-08-19 -- callers may combine
// a deferred Close with an explicit Close; the old implementation mu.Unlock()ed on every Close,
// so the second call panicked.
func TestPeerReadCloserCloseIdempotent(t *testing.T) {
	rc := &countingReadCloser{}
	mu := &sync.Mutex{}
	mu.Lock() // simulate TryLock already held (peerReadCloser takes over the unlock)
	p := &peerReadCloser{r: rc, mu: mu}

	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
	require.Equal(t, 1, rc.closeCount, "underlying reader must be closed only once")
	require.True(t, mu.TryLock(), "after double Close, lock should be released and acquirable again")
	mu.Unlock()
}

type countingReadCloser struct {
	closeCount int
}

func (c *countingReadCloser) Read([]byte) (int, error) { return 0, io.EOF }
func (c *countingReadCloser) Close() error             { c.closeCount++; return nil }

// TestPeerSource_PeerLocksRemovedOnDisconnect verifies that when a peer disconnects,
// its entry in PeerSource.peerLocks is synchronously removed via the OnPeerDisconnect hook.
// 发现背景 (Issue #214): PeerSource.peerLocks (sync.Map) 早期只增不减，走向公网 marketplace
// 后可能累积大量陌生节点的锁导致内存压力。
func TestPeerSource_PeerLocksRemovedOnDisconnect(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA", "peerB")

	// Trigger lock initialization via collectPeers
	pids, locks := ps.collectPeers()
	require.Len(t, pids, 2)
	for _, l := range locks {
		l.Unlock()
	}

	assert.True(t, ps.HasPeerLock("peerA"))
	assert.True(t, ps.HasPeerLock("peerB"))
	assert.Equal(t, 2, ps.PeerLocksCount())

	// Close peerA session -> should synchronously trigger RemoveLock
	sess[0].Close()

	assert.False(t, ps.HasPeerLock("peerA"), "peerA lock must be removed after disconnect")
	assert.True(t, ps.HasPeerLock("peerB"), "peerB lock must be retained while still connected")
	assert.Equal(t, 1, ps.PeerLocksCount())
}

// TestPeerSource_BusyLockDeferredRemoval verifies that when a peer disconnects while its
// lock is held by an active stream, TryLock probing defers removal until the stream closes.
// 发现背景 (Issue #214): 流拉取过程中远端断开连接，若直接强删可能与正在运行的流互斥状态发生竞态。
// TryLock 探测保证进行中的任务不受影响，并在 reader.Close() 时安全清理。
func TestPeerSource_BusyLockDeferredRemoval(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerA")

	pids, locks := ps.collectPeers()
	require.Len(t, pids, 1)
	heldLock := locks[0] // heldLock is currently locked by collectPeers

	// Simulate peerA disconnecting while heldLock is busy
	sess[0].Close()

	// Direct call to RemoveLock should return false because TryLock fails
	removed := ps.RemoveLock("peerA")
	assert.False(t, removed, "RemoveLock must return false when lock is held by active stream")
	assert.True(t, ps.HasPeerLock("peerA"), "busy lock must not be deleted while in flight")

	// Wrap in peerReadCloser and Close it
	prc := &peerReadCloser{
		r:      &countingReadCloser{},
		mu:     heldLock,
		peerID: "peerA",
		ps:     ps,
	}

	require.NoError(t, prc.Close())
	assert.False(t, ps.HasPeerLock("peerA"), "lock must be removed after reader Close releases it")
}

// TestPeerSource_SweepStaleLocks_MaxPeersGuard verifies the MAX_PEERS guard fallback:
// stale entries for disconnected peers are swept when threshold is exceeded.
// 发现背景 (Issue #214): MAX_PEERS 守卫兜底机制，防止极端异常情况下（如未走正常断开路径）
// sync.Map 条目泄漏。
func TestPeerSource_SweepStaleLocks_MaxPeersGuard(t *testing.T) {
	_, sess, ps := newRaceSvc(t, "peerActive")
	_ = sess

	// Populate multiple stale locks for non-existent peers
	for i := 0; i < 20; i++ {
		staleID := "stale-" + strconv.Itoa(i)
		muI, _ := ps.peerLocks.LoadOrStore(staleID, &sync.Mutex{})
		_ = muI
		ps.lockCount.Add(1)
	}

	assert.True(t, ps.PeerLocksCount() >= 20)

	// SweepStaleLocks directly
	removed := ps.SweepStaleLocks()
	assert.Equal(t, 20, removed, "all 20 stale disconnected locks must be swept")
	assert.Equal(t, 0, ps.PeerLocksCount())

	// Re-add stale locks to test automatic trigger in collectPeers
	for i := 0; i < 20; i++ {
		staleID := "stale-auto-" + strconv.Itoa(i)
		ps.peerLocks.Store(staleID, &sync.Mutex{})
		ps.lockCount.Add(1)
	}

	// Calling collectPeers with active peer should trigger SweepStaleLocks
	pids, locks := ps.collectPeers()
	require.Len(t, pids, 1)
	require.Equal(t, "peerActive", pids[0])
	locks[0].Unlock()

	// The stale ones should have been cleaned up by collectPeers guard
	assert.False(t, ps.HasPeerLock("stale-auto-0"))
	assert.True(t, ps.HasPeerLock("peerActive"))
	assert.Equal(t, 1, ps.PeerLocksCount())
}
