package transport

// outbound.go: Outbound role = this side initiates verbs and collects responses ("I ask others").
// Owned by: FetchFromPeer/requestFile (initiate req frame), routeResponse (route meta/data/
// done/err by reqId), stateFor (connection state lookup). Contrasting with inbound.go
// (respond to peer), both paths share the same connection full-duplex concurrently —
// connection close cleanup is done uniformly in conn.go's bindConn OnClose; this file
// only handles initiation and collection.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	hashutil "peerdrive/pkg/hashutil"
)

// verbWaitTimeout waiting limit for one-shot verbs (small JSON response types like
// share/info).
// Responses themselves are millisecond-level; 15s covers "signaling just jittering and
// reconnecting at this exact moment" — too short would falsely report failure during
// network jitter, too long would make users wait when opening node details.
const verbWaitTimeout = 15 * time.Second

// requestVerb initiates a "request-response" verb to a direct peer and waits for a single
// JSON response returned as-is.
// Only for small JSON responses (share etc.); file content must use OpenStream (streaming,
// doesn't consume memory).
//
// Why return raw JSON instead of dcResp: share-resp has fields (collections/files) that
// dcResp doesn't have; parsing to dcResp then marshaling would lose them.
//
// Failure paths: peer returns err frame → return that error message; timeout/connection
// closed/service closed → corresponding error. Connection close is detected via st.binDone
// (cleanupConn closes it), no need for an additional done channel in the waiting slot.
func (s *PeerJSService) requestVerb(peerID, reqType string, timeout time.Duration) ([]byte, error) {
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("peerjs: no connection to %s", peerID)
	}
	st := s.stateFor(conn)
	if st == nil {
		return nil, fmt.Errorf("peerjs: connection not bound")
	}
	reqID := uuid.NewString()
	ch := make(chan []byte, 1)
	st.mu.Lock()
	st.verbWaits[reqID] = ch
	st.mu.Unlock()
	cleanup := func() {
		st.mu.Lock()
		delete(st.verbWaits, reqID)
		st.mu.Unlock()
	}
	if err := conn.SendJSON(dcReq{Type: reqType, ReqID: reqID}); err != nil {
		cleanup()
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case raw := <-ch:
		var r dcResp
		if err := json.Unmarshal(raw, &r); err == nil && r.Type == "err" {
			return nil, fmt.Errorf("peerjs: %s", r.Msg)
		}
		return raw, nil
	case <-timer.C:
		cleanup()
		return nil, fmt.Errorf("peerjs: %s request to %s timed out", reqType, peerID)
	case <-st.binDone:
		cleanup()
		return nil, fmt.Errorf("peerjs: connection closed")
	case <-s.ctx.Done():
		cleanup()
		return nil, fmt.Errorf("peerjs: service closed")
	}
}

// RequestShares queries a direct peer's sharing manifest (share frame).
// This is the data source for "market/my nodes → peer node details page showing file
// links", and also the entry point for cross-node collection fetch (M3): first get the
// manifest, then fetch content by entry hash.
func (s *PeerJSService) RequestShares(peerID string) (ShareSnapshot, error) {
	raw, err := s.requestVerb(peerID, "share", verbWaitTimeout)
	if err != nil {
		return ShareSnapshot{}, err
	}
	var resp struct {
		Type string `json:"type"`
		ShareSnapshot
		Total int `json:"total"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ShareSnapshot{}, fmt.Errorf("peerjs: bad share response: %w", err)
	}
	// Empty array fallback: peer may return null (old version/abnormal implementation);
	// frontend shouldn't crash because of this
	if resp.Collections == nil {
		resp.Collections = []ShareCollectionInfo{}
	}
	if resp.Files == nil {
		resp.Files = []ShareFileInfo{}
	}
	return resp.ShareSnapshot, nil
}

// maxPeerFetchSize remote-declared limit (H6 fix): data frame declared chunk size/file size
// has no upper limit → malicious peer declaring 1<<62 and continuously sending data frames
// → f.got grows unbounded, OOM.
// Consistent with upload limit (8GB).
const maxPeerFetchSize = 8 * 1024 * 1024 * 1024

// OpenStream fetches sha256 file content via streaming from a direct peer (chunked/streaming
// read, entry point for source system's peerSource adapter). The returned reader automatically
// verifies sha256 when the full request is read (content-addressed fallback); Close can
// cancel early (local discard, no disconnection).
func (s *PeerJSService) OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error) {
	return s.OpenStreamFrom(peerID, hash, offset, size, nil)
}

// OpenStreamFrom has the same semantics as OpenStream, additionally carrying the fallback
// chain trace (anti-loop, 2026-08-18 3rd optimization item, see dcReq.Trace comments).
func (s *PeerJSService) OpenStreamFrom(peerID, hash string, offset, size int64, trace []string) (io.ReadCloser, error) {
	if !hashutil.IsStrictSHA256(hash) {
		return nil, fmt.Errorf("peerjs: invalid hash %q", hash)
	}
	s.mu.Lock()
	conn := s.conns[peerID]
	s.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("peerjs: no connection to %s", peerID)
	}
	st := s.stateFor(conn)
	if st == nil {
		return nil, fmt.Errorf("peerjs: connection not bound")
	}
	if offset < 0 {
		offset = 0
	}
	reqID := uuid.NewString()
	f := &fetchState{
		reqID:  reqID,
		total:  atomic.Int64{},
		q:      make(chan []byte, 8),
		done:   make(chan struct{}),
		errCh:  make(chan error, 1),
		closed: make(chan struct{}),
	}
	f.total.Store(-1)
	st.mu.Lock()
	st.fetches[reqID] = f
	st.mu.Unlock()
	if err := conn.SendJSON(dcReq{
		Type:   "req",
		Hash:   hash,
		Offset: offset,
		Size:   size,
		ReqID:  reqID,
		Trace:  trace,
	}); err != nil {
		st.mu.Lock()
		delete(st.fetches, reqID)
		st.mu.Unlock()
		return nil, err
	}
	return &fetchReader{f: f, st: st, hash: hash, offset: offset, size: size}, nil
}

// fetchReader streaming reader: reads from the fetch queue in chunks, verifies sha256 on
// full read, supports Close for early cancellation.
type fetchReader struct {
	f      *fetchState
	st     *connState
	hash   string
	offset int64
	size   int64
	h      hash.Hash
	got    int64
	total  int64
	closed bool
}

func (r *fetchReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	if r.h == nil {
		r.h = sha256.New()
	}
	for {
		select {
		case chunk, ok := <-r.f.q:
			if !ok {
				return 0, io.EOF
			}
			n := copy(p, chunk)
			r.h.Write(chunk[:n])
			r.got += int64(n)
			if n < len(chunk) {
				r.f.q <- chunk[n:]
			}
			return n, nil
		case err, ok := <-r.f.errCh:
			if ok {
				return 0, err
			}
			return 0, io.EOF
		case <-r.f.done:
			// Done received: remaining chunks in queue are still consumable
			select {
			case chunk, ok := <-r.f.q:
				if !ok {
					return 0, io.EOF
				}
				n := copy(p, chunk)
				r.h.Write(chunk[:n])
				r.got += int64(n)
				if n < len(chunk) {
					r.f.q <- chunk[n:]
				}
				return n, nil
			default:
				// Verify sha256 for full requests
				if r.offset == 0 && r.size < 0 {
					sum := hex.EncodeToString(r.h.Sum(nil))
					if sum != r.hash {
						return 0, fmt.Errorf("peerjs: sha256 mismatch for %s: got %s", r.hash, sum)
					}
				}
				return 0, io.EOF
			}
		case <-r.f.closed:
			return 0, io.EOF
		}
	}
}

// Total returns the file total size declared by the peer's meta frame (-1 if unknown).
// Used by the service layer for progress denominator in cross-node fetch saves.
func (r *fetchReader) Total() int64 { return r.f.total.Load() }

// Close cancels the fetch early (local discard, doesn't disconnect the connection).
func (r *fetchReader) Close() error {
	r.closed = true
	select {
	case <-r.f.closed:
	default:
		close(r.f.closed)
	}
	r.st.mu.Lock()
	delete(r.st.fetches, r.f.reqID)
	r.st.mu.Unlock()
	return nil
}

// routeResponse routes a response frame by reqId to the appropriate fetch state.
// Handles: meta (total size), data (expect chunk), done (completion), err (failure).
func (s *PeerJSService) routeResponse(st *connState, r dcResp, raw []byte) {
	// One-shot verb responses (share/info etc.): route to verbWaits
	if r.ReqID != "" {
		st.mu.Lock()
		ch := st.verbWaits[r.ReqID]
		st.mu.Unlock()
		if ch != nil {
			select {
			case ch <- raw:
			default:
			}
			st.mu.Lock()
			delete(st.verbWaits, r.ReqID)
			st.mu.Unlock()
			return
		}
	}
	// Fetch responses
	st.mu.Lock()
	f := st.fetches[r.ReqID]
	st.mu.Unlock()
	if f == nil {
		return
	}
	switch r.Type {
	case "meta":
		// Total size declaration (H6: upper limit check to prevent OOM from malicious peers)
		if r.Total > maxPeerFetchSize {
			failFetch(f, "peerjs: declared file size %d exceeds limit", r.Total)
		}
		// Record for service layer use (progress denominator for cross-node fetch saves,
		// see fetchReader.Total)
		f.total.Store(r.Total)
	case "data":
		// H6: chunk size upper limit and must be positive — malicious peer declaring
		// oversized size → unbounded allocation
		if r.Size <= 0 || r.Size > maxPeerFetchSize {
			failFetch(f, "peerjs: invalid data size %d", r.Size)
			return
		}
		f.size = r.Size
		st.mu.Lock()
		st.expect = f
		st.mu.Unlock()
	case "done":
		// Done is idempotent: duplicate done (or done after err) not processed — duplicate
		// close(done) would panic
		select {
		case <-f.done:
			return
		default:
		}
		// H6 integrity check: peer sending done early (only meta+done) would return truncated
		// file as success → silent data corruption. done.Size = peer-declared actual bytes
		// sent; must match delivered bytes (received) to pass through
		if r.Size >= 0 && f.received != r.Size {
			failFetch(f, "peerjs: incomplete transfer: got %d bytes, peer sent %d", f.received, r.Size)
			return
		}
		close(f.done)
	case "err":
		failFetch(f, "peerjs: %s", r.Msg)
	}
}

// failFetch marks a fetch as failed and releases the state.
func failFetch(f *fetchState, format string, args ...any) {
	select {
	case f.errCh <- fmt.Errorf(format, args...):
	default:
	}
}
