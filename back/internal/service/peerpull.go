package service

// peerpull.go: Cross-node pull and save (doc/NETDISK.md M3 / ROADMAP Phase 6).
//
// Target flow: user sees a "file link" on another node (collection entry or single file from
// the share frame) -> clicks save -> content streams from the remote node to this node and is
// written to disk -> appears in "My Files".
//
// Shape references BT/PT download tasks (user analogy "Kuake/PT approach"):
//   - Task list + progress + cancel;
//   - Content already local is **skipped directly** (content-addressing naturally deduplicates,
//     no need to download again).
//
// Division of labor with existing capabilities:
//   - transport.OpenStream: streams remote content (not memory-resident, 8GB is fine);
//   - This service: writes the stream to a file + verifies sha256 + registers into file_index
//     (visible in "My Files");
//   - source.Manager's peer source is "on-demand fallback" (not persisted to disk), which is a
//     different semantic from this service's "save" -- do not substitute one for the other.
//
// Disk strategy (**single write**, no CAS copy):
//   Stream -> <DownloadDir>/pulled/<relative path>.part -> verify sha256 -> rename to final name
//   -> file_index.Create to register.
//   Why DownloadDir instead of CAS: file_index allows the root directory to be DownloadDir
//   (see transport.NewFileIndexService H2 security boundary). After registration, the file can
//   both be served to other nodes via this node's serveFile and appear directly in the "My Files"
//   list -- one copy of data satisfies both "save" and "share". Writing a CAS copy would just
//   store the same content twice.
//   Deduplication relies on the "check file_index first" step: if the same hash is already
//   registered, it won't be downloaded again.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// PullStatus is the pull task status.
type PullStatus string

const (
	PullRunning   PullStatus = "running"
	PullDone      PullStatus = "done"
	PullFailed    PullStatus = "failed"
	PullCancelled PullStatus = "cancelled"
)

// pullConcurrency is the maximum number of concurrent pulls.
// Each pull occupies one WebRTC connection's bandwidth; too many concurrent pulls slow down
// each one (and the peer's serveFile is per-connection serial). 3 is a reasonable trade-off
// for "batch saving a collection".
const pullConcurrency = 3

// pullMaxJobs is the retention limit for the job table (oldest completed jobs are discarded when exceeded).
// Why: the job table is in memory, long-running + frequent pulls would grow unboundedly.
const pullMaxJobs = 200

// PullJob is a pull task (each row in the frontend "transfers" list).
type PullJob struct {
	ID       string     `json:"id"`
	Peer     string     `json:"peer"`
	Hash     string     `json:"hash"`
	Name     string     `json:"name,omitempty"`
	Path     string     `json:"path,omitempty"` // relative save path (path within a collection)
	Coll     string     `json:"collection,omitempty"`
	Total    int64      `json:"total"`    // -1 = peer did not declare size
	Received int64      `json:"received"`
	Status   PullStatus `json:"status"`
	Error    string     `json:"error,omitempty"`
	Skipped  bool       `json:"skipped,omitempty"` // local already has the same hash content
	SavedTo  string     `json:"saved_to,omitempty"`
	Started  time.Time  `json:"started_at"`
	Ended    *time.Time `json:"ended_at,omitempty"`
}

// Done returns whether the task has reached a terminal state.
func (j PullJob) Done() bool {
	return j.Status == PullDone || j.Status == PullFailed || j.Status == PullCancelled
}

// PullSource is the pull data plane (*transport.PeerJSService satisfies this).
// Only depends on "able to stream by hash" for easy injection of fake implementations in tests.
type PullSource interface {
	OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error)
}

// totalReporter is a reader that can report the peer's declared size (*transport.fetchReader satisfies this).
// Uses an optional interface rather than a hard dependency: fake/future implementations need not provide
// size, and progress renders as unknown.
type totalReporter interface{ Total() int64 }

// PeerPuller is the cross-node pull and save service.
type PeerPuller struct {
	downloadRoot string // save root directory (= file_index's allowed root)
	source       PullSource

	// isLocal checks whether a hash is already local (file_index hit).
	// register adds the written file to file_index, returning the actual hash and size.
	isLocal  func(hash string) bool
	register func(path string) (string, int64, error)

	mu   sync.Mutex
	jobs map[string]*PullJob
	// cancels jobID -> cancel (cancel closes the reader, immediately interrupting the transfer)
	cancels map[string]context.CancelFunc
	// closers jobID -> current reader (Close on Cancel to wake a blocked Read)
	closers map[string]io.Closer
	sem     chan struct{}
}

// NewPeerPuller creates the pull service. Falls back to ./downloads when downloadRoot is empty.
func NewPeerPuller(downloadRoot string) *PeerPuller {
	if downloadRoot == "" {
		downloadRoot = "./downloads"
	}
	return &PeerPuller{
		downloadRoot: downloadRoot,
		jobs:         make(map[string]*PullJob),
		cancels:      make(map[string]context.CancelFunc),
		closers:      make(map[string]io.Closer),
		sem:          make(chan struct{}, pullConcurrency),
	}
}

// SetSource sets the pull data plane.
func (p *PeerPuller) SetSource(s PullSource) { p.source = s }

// SetLocalChecker sets the local content checker (deduplication).
func (p *PeerPuller) SetLocalChecker(fn func(hash string) bool) { p.isLocal = fn }

// SetRegister sets the file registration function (write to file_index after download).
func (p *PeerPuller) SetRegister(fn func(path string) (string, int64, error)) { p.register = fn }

// ListJobs returns all tasks, sorted by start time descending.
func (p *PeerPuller) ListJobs() []PullJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PullJob, 0, len(p.jobs))
	for _, j := range p.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Started.After(out[j].Started)
	})
	return out
}

// Cancel cancels a task: closes the reader to interrupt the transfer and marks it cancelled.
func (p *PeerPuller) Cancel(id string) bool {
	p.mu.Lock()
	cancel, ok := p.cancels[id]
	j := p.jobs[id]
	closer := p.closers[id]
	p.mu.Unlock()
	if !ok {
		return false
	}
	if closer != nil {
		closer.Close()
	}
	cancel()
	p.mu.Lock()
	if j != nil && !j.Done() {
		now := time.Now()
		j.Status = PullCancelled
		j.Ended = &now
		j.Error = "cancelled by user"
	}
	p.mu.Unlock()
	return true
}

// Pull starts a single pull task: stream remote content, write to disk, verify, register.
// Deduplication: if file_index already has the same hash, mark as skipped (skipped=true).
func (p *PeerPuller) Pull(peerID, hash, name, relPath, coll string, total int64) (*PullJob, error) {
	if p.source == nil {
		return nil, errors.New("pull source not configured")
	}
	if p.register == nil {
		return nil, errors.New("register function not configured")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	// Deduplication: same jobID (peer+hash) still running -> reuse
	for _, j := range p.jobs {
		if j.Peer == peerID && j.Hash == hash && !j.Done() {
			return j, nil
		}
	}
	// Local dedup: file_index already has this hash -> skipped
	if p.isLocal != nil && p.isLocal(hash) {
		now := time.Now()
		j := &PullJob{
			ID:       p.nextID(),
			Peer:     peerID,
			Hash:     hash,
			Name:     name,
			Path:     relPath,
			Coll:     coll,
			Total:    total,
			Received: total,
			Status:   PullDone,
			Skipped:  true,
			SavedTo:  relPath,
			Started:  now,
			Ended:    &now,
		}
		p.jobs[j.ID] = j
		return j, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	j := &PullJob{
		ID:      p.nextID(),
		Peer:    peerID,
		Hash:    hash,
		Name:    name,
		Path:    relPath,
		Coll:    coll,
		Total:   total,
		Status:  PullRunning,
		Started: now,
	}
	p.jobs[j.ID] = j
	p.cancels[j.ID] = cancel
	// Concurrency limiting + task table retention
	go p.run(ctx, cancel, j, peerID, hash, name, relPath, coll, total)
	p.evictLocked()
	return j, nil
}

// run executes a single pull task (runs in a goroutine).
func (p *PeerPuller) run(ctx context.Context, cancel context.CancelFunc, j *PullJob, peerID, hash, name, relPath, coll string, total int64) {
	defer func() {
		p.mu.Lock()
		delete(p.cancels, j.ID)
		delete(p.closers, j.ID)
		p.mu.Unlock()
		now := time.Now()
		p.mu.Lock()
		if j.Ended == nil {
			j.Ended = &now
		}
		p.mu.Unlock()
	}()

	// Acquire concurrency slot
	p.sem <- struct{}{}
	defer func() { <-p.sem }()

	if err := ctx.Err(); err != nil {
		p.finishJob(j, PullCancelled, "cancelled")
		return
	}

	// Stream from peer
	r, err := p.source.OpenStream(peerID, hash, 0, -1)
	if err != nil {
		p.finishJob(j, PullFailed, fmt.Sprintf("open stream: %v", err))
		return
	}
	p.mu.Lock()
	p.closers[j.ID] = r
	p.mu.Unlock()

	// Write to .part file under DownloadDir/pulled
	safeRel := sanitizeRelPath(relPath)
	if safeRel == "" {
		safeRel = filepath.Base(name)
		if safeRel == "" || safeRel == "." {
			safeRel = hash
		}
	}
	partial := filepath.Join(p.downloadRoot, "pulled", safeRel+".part")
	if err := os.MkdirAll(filepath.Dir(partial), 0755); err != nil {
		r.Close()
		p.finishJob(j, PullFailed, fmt.Sprintf("mkdir: %v", err))
		return
	}
	out, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		r.Close()
		p.finishJob(j, PullFailed, fmt.Sprintf("create partial: %v", err))
		return
	}

	// Check if the source reports total size
	if tr, ok := r.(totalReporter); ok {
		t := tr.Total()
		if t > 0 && total < 0 {
			p.mu.Lock()
			j.Total = t
			p.mu.Unlock()
		}
	}

	// Stream copy with SHA256 verification
	h := sha256.New()
	w := io.MultiWriter(out, h)
	var copied int64
	var copyErr error
	buf := make([]byte, 32*1024)
	for {
		n, rerr := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				copyErr = werr
				break
			}
			copied += int64(n)
			p.mu.Lock()
			j.Received = copied
			p.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			copyErr = rerr
			break
		}
		if cerr := ctx.Err(); cerr != nil {
			p.finishJob(j, PullCancelled, "cancelled")
			r.Close()
			out.Close()
			os.Remove(partial)
			return
		}
	}
	r.Close()

	if copyErr != nil {
		out.Close()
		os.Remove(partial)
		p.finishJob(j, PullFailed, fmt.Sprintf("copy: %v", copyErr))
		return
	}

	computed := hex.EncodeToString(h.Sum(nil))
	if computed != hash {
		out.Close()
		os.Remove(partial)
		p.finishJob(j, PullFailed, fmt.Sprintf("hash mismatch: expected %s, got %s", hash, computed))
		return
	}
	if err := out.Close(); err != nil {
		os.Remove(partial)
		p.finishJob(j, PullFailed, fmt.Sprintf("close partial: %v", err))
		return
	}

	// Register into file_index
	registeredPath := partial
	if p.register != nil {
		regPath, regSize, err := p.register(partial)
		if err != nil {
			p.finishJob(j, PullFailed, fmt.Sprintf("register: %v", err))
			return
		}
		_ = regSize
		if regPath != "" {
			registeredPath = regPath
		}
	}

	finalPath := strings.TrimSuffix(partial, ".part")
	if err := os.Rename(partial, finalPath); err != nil {
		// Rename failure is non-fatal: file_index has already registered the partial path
		log.LogWarn("peerpull: rename %s -> %s failed: %v", partial, finalPath, err)
	}

	p.finishJob(j, PullDone, "")
	j.SavedTo = finalPath
	log.LogInfo("peerpull: done peer=%s hash=%s path=%s size=%d", peerID, hash, finalPath, copied)
}

func (p *PeerPuller) finishJob(j *PullJob, status PullStatus, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	j.Status = status
	if j.Ended == nil {
		j.Ended = &now
	}
	if errMsg != "" {
		j.Error = errMsg
	}
}

// evictLocked discards the oldest completed tasks when the job table exceeds the limit (caller holds the lock).
func (p *PeerPuller) evictLocked() {
	if len(p.jobs) <= pullMaxJobs {
		return
	}
	type pair struct {
		id  string
		end time.Time
	}
	var done []pair
	for id, j := range p.jobs {
		if j.Done() {
			t := time.Time{}
			if j.Ended != nil {
				t = *j.Ended
			}
			done = append(done, pair{id, t})
		}
	}
	sort.Slice(done, func(i, j int) bool { return done[i].end.Before(done[j].end) })
	for len(p.jobs) > pullMaxJobs && len(done) > 0 {
		delete(p.jobs, done[0].id)
		delete(p.cancels, done[0].id)
		delete(p.closers, done[0].id)
		done = done[1:]
	}
}

// nextID generates a task ID (caller holds the lock).
func (p *PeerPuller) nextID() string {
	n := len(p.jobs)
	return fmt.Sprintf("pull-%d-%d", time.Now().UnixNano(), n)
}

// sanitizeRelPath sanitizes a relative path from the peer into a safe relative path.
// Rules: unify separators -> discard empty segments/"."/".."/absolute prefixes -> remove control characters within segments.
// Returns empty string if it cannot be safely sanitized (caller falls back to filename/hash).
func sanitizeRelPath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		s = strings.TrimSpace(s)
		if s == "" || s == "." || s == ".." {
			continue
		}
		// Control characters and Windows reserved characters cause rename/display issues
		s = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			switch r {
			case ':', '*', '?', '"', '<', '>', '|':
				return '_'
			}
			return r
		}, s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return strings.Join(out, string(filepath.Separator))
}
