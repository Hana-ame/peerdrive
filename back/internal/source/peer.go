package source

// peer.go: PeerSource — p2p passthrough source (fetches from online peers via PeerJS/WebRTC).
// Semantics:
//   - Open enumerates online peers (PeerJSService.Connections), races multiple peers concurrently,
//     returns immediately on the first successful stream — passthrough semantics: if this node
//     doesn't have it, fetch from peers, and pick the fastest path (2026-08-19 race batch,
//     see REFACTOR §3.15).
//   - Only one stream allowed per peer at a time: connection-level expect single-slot (frame
//     protocol constraint, see transport/conn.go bindConn comments) — concurrent streams to
//     the same peer will interleave data. TryLock skips busy peers instead of waiting (large
//     file streams would block for a long time).
//   - sha256 verification of full requests is done by transport's fetchReader (H5 fallback).
//   - Available = has online peers.

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"peerdrive/internal/transport"
)

// PeerSource p2p passthrough file source.
type PeerSource struct {
	name string
	svc  *transport.PeerJSService

	mu       sync.RWMutex
	priority int

	// peerLocks per-peer stream mutex: only one OpenStream per peer at a time.
	// TryLock semantics: skip busy peers (go to the next), don't wait — waiting for a
	// large file stream to finish would block the entire routing.
	// Synchronously cleaned up on peer disconnect with MAX_PEERS guard (Issue #214).
	peerLocks sync.Map // peerID → *sync.Mutex
	lockCount atomic.Int64
}

// NewPeerSource creates a p2p passthrough source. name defaults to "peer".
func NewPeerSource(svc *transport.PeerJSService) *PeerSource {
	s := &PeerSource{name: "peer", svc: svc}
	if svc != nil {
		svc.OnPeerDisconnect(func(peerID string) {
			s.RemoveLock(peerID)
		})
	}
	return s
}

func (s *PeerSource) Name() string { return s.name }
func (s *PeerSource) Type() string { return "peer" }

// Capabilities peer streaming chunks (req frames support offset/size range).
func (s *PeerSource) Capabilities() Capability { return CapStream }

func (s *PeerSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *PeerSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available has online peers (excluding self).
func (s *PeerSource) Available(ctx context.Context) bool {
	conns := s.svc.Connections()
	for pid := range conns {
		if pid != s.svc.ID() {
			return true
		}
	}
	return false
}

// Open fetches content from online peers: multi-peer concurrent race, first success returns.
// Why race: with serial attempts, the first slow peer (slow remote disk / network jitter)
// would block the entire fetch fallback until failure/timeout — in passthrough scenarios
// with multiple peers online, we should pick the fastest path. Racing only goes to
// "first stream established", not waiting for remaining peers (late/failing streams are
// closed by the reaper goroutine to prevent peer stream mutex leaks and local fetch state
// hangs).
// ctx can carry the fallback chain (transport.TraceKey, injected during serveFile
// fallback) — passed through to OpenStreamFrom to prevent loops (A←→B mutual interconnect
// fallback infinite loop, 2026-08-18 3rd optimization item). Root requests (HTTP download
// etc.) ctx has no such value → trace is nil.
func (s *PeerSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	var trace []string
	if t, ok := ctx.Value(transport.TraceKey).([]string); ok {
		trace = t
	}
	pids, locks := s.collectPeers()
	if len(pids) == 0 {
		return nil, fmt.Errorf("no online peer available")
	}
	if len(pids) == 1 {
		// Single peer: take the original serial path (no concurrency overhead)
		r, err := s.svc.OpenStreamFrom(pids[0], hash, offset, size, trace)
		if err != nil {
			locks[0].Unlock()
			if s.svc != nil && !s.svc.HasConnection(pids[0]) {
				s.RemoveLock(pids[0])
			}
			return nil, fmt.Errorf("peer %s: %w", pids[0], err)
		}
		return &peerReadCloser{r: r, mu: locks[0], peerID: pids[0], ps: s}, nil
	}
	return s.raceOpen(pids, locks, hash, offset, size, trace)
}

// collectPeers enumerates available peers and TryLocks each: busy peers (already have
// an active stream, connection-level expect single-slot) are skipped without waiting —
// waiting for large file streams would block the entire routing.
// Returned locks are held by this caller; release responsibility is transferred to the
// stream lifecycle (winner reader Close / failure path immediate Unlock / race reaper Close).
func (s *PeerSource) collectPeers() (pids []string, locks []*sync.Mutex) {
	// MAX_PEERS guard fallback: if the number of tracked locks exceeds 2 * MAX_PEERS,
	// run a sweep to prune any stale entries that may have leaked due to abnormal disconnects (Issue #214).
	if s.PeerLocksCount() > 2*s.maxPeersLimit() {
		s.SweepStaleLocks()
	}

	conns := s.svc.Connections()
	for pid := range conns {
		if pid == s.svc.ID() {
			continue
		}
		muI, loaded := s.peerLocks.LoadOrStore(pid, &sync.Mutex{})
		if !loaded {
			s.lockCount.Add(1)
		}
		mu := muI.(*sync.Mutex)
		if !mu.TryLock() {
			continue
		}
		pids = append(pids, pid)
		locks = append(locks, mu)
	}
	return pids, locks
}

// raceResult race result. chan capacity = candidate count → loser goroutines never block.
type raceResult struct {
	r   io.ReadCloser
	mu  *sync.Mutex
	pid string
	err error // failure reason (aggregated into final error on all-failure, for diagnosis)
}

// raceOpen multi-peer concurrent race: each candidate peer initiates a stream concurrently,
// first success returns immediately (true race: slow peers don't block this call); remaining
// undecided results are closed by a background reaper goroutine (r leak → that peer's stream
// mutex permanently occupied + local fetch state hang, slow peers arriving seconds late
// cause leaks).
func (s *PeerSource) raceOpen(pids []string, locks []*sync.Mutex, hash string, offset, size int64, trace []string) (io.ReadCloser, error) {
	ch := make(chan raceResult, len(pids))
	for i := range pids {
		go func(pid string, mu *sync.Mutex) {
			r, err := s.svc.OpenStreamFrom(pid, hash, offset, size, trace)
			if err != nil {
				mu.Unlock() // failure: immediately release that peer's slot
				if s.svc != nil && !s.svc.HasConnection(pid) {
					s.RemoveLock(pid)
				}
				ch <- raceResult{err: err, pid: pid}
				return
			}
			ch <- raceResult{r: r, mu: mu, pid: pid} // success: unlock ownership goes to winner/reaper
		}(pids[i], locks[i])
	}
	got := 0
	var errs []string
	for got < len(pids) {
		rr := <-ch
		got++
		if rr.r == nil {
			if rr.err != nil {
				errs = append(errs, rr.err.Error())
			}
			continue
		}
		if got < len(pids) {
			go func(n int) {
				for j := n; j < len(pids); j++ {
					x := <-ch
					if x.r != nil {
						x.r.Close()
						x.mu.Unlock()
						if s.svc != nil && !s.svc.HasConnection(x.pid) {
							s.RemoveLock(x.pid)
						}
					}
				}
			}(got)
		}
		return &peerReadCloser{r: rr.r, mu: rr.mu, peerID: rr.pid, ps: s}, nil
	}
	// All failed: aggregate each peer's reason (diagnosable error when falling back to
	// upper-layer source)
	if len(errs) > 0 {
		return nil, fmt.Errorf("all peers failed: %s", strings.Join(errs, "; "))
	}
	return nil, fmt.Errorf("all peers failed")
}

// Fetch full fetch (CapStream already covers, defensive implementation).
func (s *PeerSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Info p2p source does not do metadata queries (peer info verb not implemented on the
// fetch side — not supported in the first version).
func (s *PeerSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	return nil, nil
}

// RemoveLock cleans up the peerLocks entry for peerID if it is safe to do so.
// Timing safety guarantees (Issue #214):
//  1. If the peer is still actively connected, keep the entry (no-op).
//  2. TryLock probe: if the lock is held by an active stream, defer deletion until
//     the stream closes.
//  3. Once TryLock succeeds and the peer is disconnected, delete the entry from
//     peerLocks and unlock.
func (s *PeerSource) RemoveLock(peerID string) bool {
	if peerID == "" {
		return false
	}
	if s.svc != nil && s.svc.HasConnection(peerID) {
		return false
	}
	muI, ok := s.peerLocks.Load(peerID)
	if !ok {
		return false
	}
	mu := muI.(*sync.Mutex)
	if !mu.TryLock() {
		// Active stream in flight; skip removal, will be cleaned up on reader Close
		return false
	}
	defer mu.Unlock()

	// Re-verify after acquiring the lock in case connection reconnected
	if s.svc != nil && s.svc.HasConnection(peerID) {
		return false
	}
	if _, loaded := s.peerLocks.LoadAndDelete(peerID); loaded {
		s.lockCount.Add(-1)
		return true
	}
	return false
}

// HasPeerLock reports whether an entry for peerID exists in peerLocks.
func (s *PeerSource) HasPeerLock(peerID string) bool {
	_, ok := s.peerLocks.Load(peerID)
	return ok
}

// PeerLocksCount returns the approximate count of tracked peer locks.
func (s *PeerSource) PeerLocksCount() int {
	c := s.lockCount.Load()
	if c < 0 {
		return 0
	}
	return int(c)
}

// SweepStaleLocks prunes locks for peers that are no longer connected and whose
// lock is not held by an active stream.
func (s *PeerSource) SweepStaleLocks() int {
	if s.svc == nil {
		return 0
	}
	conns := s.svc.Connections()
	removed := 0
	s.peerLocks.Range(func(key, value any) bool {
		pid, ok := key.(string)
		if !ok {
			return true
		}
		if _, connected := conns[pid]; !connected {
			if s.RemoveLock(pid) {
				removed++
			}
		}
		return true
	})
	return removed
}

func (s *PeerSource) maxPeersLimit() int {
	mp := 8
	if s.svc != nil {
		mp = s.svc.MaxPeers()
	}
	if mp <= 0 || mp > (1<<20) {
		mp = 8
	}
	if s.svc != nil {
		if n := len(s.svc.Connections()); n > mp {
			mp = n
		}
	}
	return mp
}

// peerReadCloser releases the peer stream mutex after reading is complete.
type peerReadCloser struct {
	r      io.ReadCloser
	mu     *sync.Mutex
	peerID string
	ps     *PeerSource
	// Prevent caller from calling Close twice: io.Reader usage convention allows Close
	// to be called multiple times (defer + explicit close), and Unlock on the same
	// *sync.Mutex every time would panic.
	// Discovery background: re-review 2026-08-19 — race reaping / failure path combined
	// with caller defer, double Close unlock is a potential crash point.
	once sync.Once
}

func (p *peerReadCloser) Read(b []byte) (int, error) { return p.r.Read(b) }

func (p *peerReadCloser) Close() error {
	var err error
	p.once.Do(func() {
		err = p.r.Close()
		p.mu.Unlock() // release the peer's slot when stream ends (only TryLock holders reach here)
		if p.ps != nil && p.peerID != "" {
			if p.ps.svc != nil && !p.ps.svc.HasConnection(p.peerID) {
				p.ps.RemoveLock(p.peerID)
			}
		}
	})
	return err
}
