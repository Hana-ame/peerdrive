package transport

// inbound.go: Inbound role = respond to the full set of verbs sent by peers ("others ask me, I answer").
// Owned by: req (serveFile), create/upload/list/info/delete/sync (serve* index verbs),
// upload chunk persistence worker (uploadWorker). Shared connection mechanisms are in
// conn.go; this file only contains peer-driven handling logic — contrasting with outbound.go
// (this side initiates). Both paths share the same connection full-duplex concurrently,
// sharing no mutable state (except their own slots within connState).

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	hashutil "peerdrive/pkg/hashutil"
)

// chunkSize DataChannel single chunk transfer size (pion SCTP single message limit ~256KB,
// 64KB balances flow control granularity).
// Write buffer flow control has been pushed down to peerjs.Connection.SendFrame
// (connection-level global callback); here we only define chunk size.
const chunkSize = 64 * 1024

// chunkPool 64KB chunk buffer pool: concurrent serveFile each calling make would
// redundantly allocate 64KB (GC pressure + memory peaks); pool reuse (one per request,
// returned after SendFrame synchronous copy — SendFrame internally doesn't hold buf during
// marshal and wire transmission).
var chunkPool = sync.Pool{
	New: func() any {
		b := make([]byte, chunkSize)
		return &b
	},
}

func getChunk() []byte  { return *chunkPool.Get().(*[]byte) }
func putChunk(b []byte) { chunkPool.Put(&b) }

// ---- File serving (peer requests this node's file) ----

// FileRouter file routing interface (implemented by source.Manager; transport layer only
// depends on the interface to avoid import cycle — source package imports transport,
// transport can't import source).
// serveFile routes through this for "local → peer → URL template" multi-source fetch
// (3rd optimization, 2026-08-18); when not configured (nil), falls back to local semantics
// (openFile).
type FileRouter interface {
	// OpenRange streaming open a hash's chunk (offset<0→0; size<0→to end of file).
	OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error)
	// InfoSize query file total size (for meta frame; sources without metadata return err).
	InfoSize(ctx context.Context, hash string) (int64, error)
}

// serveFile opens a file (content-addressed/file_index) and sends chunks as requested, with
// write buffer flow control.
// Security (H1/H2 fix):
//   - hash must be 64 hex before slicing — previously directly req.Hash[:2], peer sending
//     empty/short hash would out-of-bounds panic, serveFile panicking in goroutine would
//     kill the entire process (any node on public signaling could crash the entire node with
//     one line of JSON)
//   - file_index matched paths must be within allowed root directory — previously directly
//     os.Open(fi.Path), peer creating any absolute path could then req to read it
//     (/etc/shadow attack chain)
//
// Prefer file_index mapping (externally registered/uploaded files), then content-addressed
// storage.
// 2026-08-18 (3rd optimization): open changed to multi-source routing (when FileRouter is
// configured) — local authority first, fall back to peer/URL template on miss (original
// openFile semantics and source.LocalSource.resolvePath are consistent, converged to one
// place). Fallback loops are prevented by Trace (this node's ID already in the chain →
// reject, see dcReq.Trace comments).
func (s *PeerJSService) serveFile(c Session, req dcReq) {
	if !hashutil.IsStrictSHA256(req.Hash) {
		_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "invalid hash", ReqID: req.ReqID})
		return
	}
	// Sharing level gate (doc/NETDISK.md §12.6): private content only for friends and self.
	// Only blocks private — public / unlisted / undeclared all pass through (see ShareGate
	// comments).
	if g := s.currentShareGate(); g != nil && !g.AllowsDownload(c.ID(), req.Hash, isSelfSession(c)) {
		log.LogInfo("peerjs: deny private download hash=%s peer=%s", req.Hash, c.ID())
		_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "private", ReqID: req.ReqID})
		return
	}
	// Trace anti-loop: this node already in the request chain → reject (prevents A←→B mutual
	// interconnect fallback infinite loop)
	if s.ID() != "" {
		for _, id := range req.Trace {
			if id == s.ID() {
				_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "loop detected", ReqID: req.ReqID})
				return
			}
		}
	}
	// Forwarded chain: append this node (only reached when this node is not in the chain)
	fwdTrace := append(append([]string{}, req.Trace...), s.ID())
	ctx := context.WithValue(context.Background(), TraceKey, fwdTrace)

	total := int64(-1) // -1 = unknown (peer fetchReader's total>0 upper limit check would skip)
	var r io.Reader
	if s.router != nil {
		// Metadata: local source can query (file_index/CAS stat); peer/url sources have no
		// Info → -1
		if size, err := s.router.InfoSize(ctx, req.Hash); err == nil && size >= 0 {
			total = size
		}
		f, err := s.router.OpenRange(ctx, req.Hash, req.Offset, req.Size)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "not found", ReqID: req.ReqID})
			return
		}
		defer f.Close()
		r = f
	} else {
		// Router not configured (test/standalone mode): local semantics (file_index priority
		// + CAS fallback)
		var f *os.File
		var err error
		f, err = s.openFile(req.Hash, req.Offset)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Hash: req.Hash, Msg: "not found", ReqID: req.ReqID})
			return
		}
		defer f.Close()
		r = f
	}
	// Meta frame: file total size
	_ = c.SendJSON(dcResp{Type: "meta", Hash: req.Hash, Total: total, ReqID: req.ReqID})
	buf := getChunk()
	defer putChunk(buf)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			_ = c.SendFrame(dcResp{Type: "data", Hash: req.Hash, Offset: req.Offset, Size: int64(n), ReqID: req.ReqID}, buf[:n])
		}
		if err != nil {
			break
		}
	}
	_ = c.SendJSON(dcResp{Type: "done", Hash: req.Hash, Offset: req.Offset, ReqID: req.ReqID})
}

// openFile opens a file from file_index mapping or content-addressed storage.
// Security: file_index path must be within allowed root; content-addressed path is always
// safe (generated internally).
func (s *PeerJSService) openFile(hash string, offset int64) (*os.File, error) {
	if s.fileIndex != nil {
		if fi, err := s.fileIndex.GetInfo(hash); err == nil && s.fileIndex.IsPathReadable(fi.Path) {
			f, err := os.Open(fi.Path)
			if err != nil {
				return nil, err
			}
			if offset > 0 {
				if _, err := f.Seek(offset, io.SeekStart); err != nil {
					f.Close()
					return nil, err
				}
			}
			return f, nil
		}
	}
	// Content-addressed storage fallback: storageDir/<hash-prefix>/<hash>
	prefix := hash[:2]
	path := filepath.Join(s.storageDir, prefix, hash)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

// ---- Upload handling (peer streams data to this node) ----

// serveUploadBegin handles upload begin (peer declares name/size, this node creates an
// upload session).
func (s *PeerJSService) serveUploadBegin(c Session, st *connState, r dcResp) {
	if s.fileIndex == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "upload not supported", ReqID: r.ReqID})
		return
	}
	// Validate name and size
	if r.Name == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "upload name required", ReqID: r.ReqID})
		return
	}
	if r.Size < 0 {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "upload size must be non-negative", ReqID: r.ReqID})
		return
	}
	sess, err := s.fileIndex.BeginUpload(r.Name, r.Size)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "upload begin failed: " + err.Error(), ReqID: r.ReqID})
		return
	}
	// Single-slot: one upload stream per connection
	st.mu.Lock()
	st.pendingUpload = &uploadState{
		reqID:   r.ReqID,
		offset:  0,
		size:    int64(len([]byte{})), // will be updated per chunk
		sess:    sess,
		created: time.Now(),
	}
	st.mu.Unlock()
	// Send meta acknowledgment
	_ = c.SendJSON(dcResp{Type: "meta", Hash: r.Hash, Total: r.Size, ReqID: r.ReqID})
}

// ---- Index verbs ----

// serveCreate handles peer's file registration request.
func (s *PeerJSService) serveCreate(c Session, r dcResp) {
	if s.fileIndex == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "file index not available", ReqID: r.ReqID})
		return
	}
	fi, err := s.fileIndex.Create(r.Path)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "created", Hash: fi.Hash, Total: fi.Size, Name: fi.Name, Path: fi.Path, Seq: fi.Seq, ReqID: r.ReqID})
}

// serveList handles peer's list request.
func (s *PeerJSService) serveList(c Session, r dcResp) {
	if s.fileIndex == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "file index not available", ReqID: r.ReqID})
		return
	}
	files, total, err := s.fileIndex.List(int(r.Offset), int(r.Size))
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "list-resp", Files: files, Total: total, ReqID: r.ReqID})
}

// serveInfo handles peer's info query.
func (s *PeerJSService) serveInfo(c Session, r dcResp) {
	if s.fileIndex == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "file index not available", ReqID: r.ReqID})
		return
	}
	fi, err := s.fileIndex.GetInfo(r.Hash)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "info-resp", Hash: fi.Hash, Total: fi.Size, Name: fi.Name, Path: fi.Path, Seq: fi.Seq, ReqID: r.ReqID})
}

// serveDelete handles peer's delete request.
func (s *PeerJSService) serveDelete(c Session, r dcResp) {
	if s.fileIndex == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "file index not available", ReqID: r.ReqID})
		return
	}
	fi, err := s.fileIndex.Delete(r.Hash, false)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "deleted", Hash: fi.Hash, ReqID: r.ReqID})
}

// serveSync handles peer's incremental sync request.
func (s *PeerJSService) serveSync(c Session, r dcResp) {
	if s.fileIndex == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "file index not available", ReqID: r.ReqID})
		return
	}
	files, lastSeq, err := s.fileIndex.Sync(r.Seq)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	_ = c.SendJSON(dcResp{Type: "sync-resp", Files: files, LastSeq: lastSeq, ReqID: r.ReqID})
}

// serveShare handles peer's share query (share.go, netdisk target M2).
func (s *PeerJSService) serveShare(c Session, r dcResp) {
	s.shareMu.RLock()
	provider := s.shareProvider
	s.shareMu.RUnlock()
	if provider == nil {
		_ = c.SendJSON(dcResp{Type: "share-resp", Collections: []ShareCollectionInfo{}, Files: []ShareFileInfo{}, ReqID: r.ReqID})
		return
	}
	snap := provider(c.ID())
	_ = c.SendJSON(dcResp{Type: "share-resp", Collections: snap.Collections, Files: snap.Files, Total: len(snap.Collections)*len(snap.Files), ReqID: r.ReqID})
}

// ---- Upload worker ----

// uploadWorker connection-level worker: persists upload chunks received in the pump.
// IO (WriteAt/Complete) happens here, not in the pion message pump — previously, an 8GB
// upload's Complete (fsync + full file hashFile) would freeze all other frames on this
// connection (head-of-line blocking; slow disk could deadlock the entire connection).
// Single worker guarantees ordering; shares binDone exit signal with fwdWorker.
func (s *PeerJSService) uploadWorker(c Session, st *connState) {
	for {
		select {
		case ch := <-st.binCh:
			if ch.au != nil {
				// Admin management-plane upload chunk (admin.go)
				s.adminUploadChunk(c, ch.au, ch.data, ch.last)
			} else if ch.up != nil {
				// File index upload chunk (inbound.go)
				if ch.up.got >= ch.up.size {
					if err := ch.up.sess.WriteAt(ch.offset, ch.data); err != nil {
						log.LogWarn("peerjs: upload write failed: %v", err)
						_ = c.SendJSON(dcResp{Type: "err", Msg: "upload write failed: " + err.Error(), ReqID: ch.up.reqID})
						ch.up.sess.Release()
						continue
					}
					if ch.last {
						fi, err := ch.up.sess.Complete()
						if err != nil {
							_ = c.SendJSON(dcResp{Type: "err", Msg: "upload complete failed: " + err.Error(), ReqID: ch.up.reqID})
						} else {
							_ = c.SendJSON(dcResp{Type: "uploaded", Hash: fi.Hash, Total: fi.Size, Path: fi.Path, ReqID: ch.up.reqID})
						}
					} else {
						// Chunk written but not complete: ack so client can continue with next chunk
						_ = c.SendJSON(dcResp{Type: "ack", Offset: ch.offset, ReqID: ch.up.reqID})
					}
				}
			}
		case <-st.binDone:
			return
		}
	}
}

// fwdWorker connection-level forwarding write worker (separated on 2026-08-18; discovery
// background: code review — forwarding chunks were originally consumed by uploadWorker's
// select; during an 8GB upload's Complete (fsync + full file hashFile, slow disks can take
// seconds), all forwarding tunnels on the same connection freeze, and interactive tunnels
// (SSH etc.) freeze entirely). Independent worker ensures forwarding IO is not affected by
// uploads.
// Single worker guarantees ordering; shares binDone exit signal with uploadWorker.
func (s *PeerJSService) fwdWorker(c Session, st *connState) {
	for {
		select {
		case ch := <-st.fwdCh:
			// Write failure (tunnel closed/peer disconnected) silently discarded: forwarding
			// is a best-effort stream.
			if _, err := ch.fw.out.Write(ch.data); err != nil {
				log.LogDebug("peerjs: fwd write drop: %v", err)
				ch.fw.out.Close()
			}
		case <-st.binDone:
			return
		}
	}
}
