package transport

// conn.go: Shared core for a connection (mechanisms shared by both roles, no duplication).
//
// Role division — inbound/outbound are "frame roles", not connection direction. WebRTC
// connections are full-duplex symmetric; the same Session simultaneously carries both
// roles (can serve a peer's req while collecting responses for its own outgoing requests),
// so:
//   - inbound.go: inbound role = respond to verbs sent by peers (req/create/upload/
//     list/info/delete/sync)
//   - outbound.go: outbound role = this side initiates verbs (req) and collects responses
//     (meta/data/done/err)
//   - conn.go: connection-level mechanisms shared by both roles exist only here — frame
//     types, reqId state machine, binary chunk routing, flow control. Duplicating these
//     when splitting roles would directly break protocol consistency (see bindConn comments).
//
// Frame protocol (JSON text frames + binary chunks on DataChannel, shared by go↔go and
// go↔web):
//
//	Request: {"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<optional>"}
//	Response: {"type":"meta","hash","total","reqId"}          file info
//	          {"type":"data","hash","offset","size","reqId"}  + immediately followed by size bytes of raw data
//	          {"type":"done","hash","offset","size","reqId"}  transfer complete
//	          {"type":"err","msg","reqId"}                    failure
//
// Critical constraints (protocol correctness depends on these, do not break):
//  1. data header must be a text frame, data chunk must be a binary frame (pion dc.Send
//     sends binary, SendText sends text — reversed, the peer would swallow the JSON header
//     as a data chunk)
//  2. data header and data chunk must be contiguous (Connection.SendFrame atomic send);
//     receiver uses "connection-level expect" state machine to attach binary chunks to
//     the most recent data header's request
//  3. reqId routing: browser side can omit reqId (backward compatibility); Go side always
//     carries it

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"

	"peerdrive/internal/log"
)

// pskAuthTimeout how long to wait for a peer to present psk-auth after bindConn.
// Connections that don't authenticate hold connState + 2 goroutines — without a timeout
// an attacker can exhaust resources by repeatedly handshaking without authenticating.
// Override in tests for faster execution.
var pskAuthTimeout = 30 * time.Second

// dcReq file fetch request frame (initiated by outbound role, responded to by inbound role).
// Trace is the fallback chain (2026-08-18, anti-loop): when serveFile routes fallback to
// other nodes via multi-source routing, it carries "already passed node chain"; nodes on
// the forwarding path find themselves in the chain and reject (A←→B mutual interconnect:
// B requests a file A doesn't have → A falls back to B → B falls back to A infinite loop).
// Root requests (HTTP download / browser fetch) have empty trace; old peers ignore the
// field (omitempty compatible). Propagation: serveFile carries it via context (TraceKey);
// PeerSource.Open retrieves from ctx and appends to OpenStreamFrom's request frame.
type dcReq struct {
	Type   string   `json:"type"`
	Hash   string   `json:"hash"`
	Offset int64    `json:"offset"`
	Size   int64    `json:"size"`
	ReqID  string   `json:"reqId,omitempty"`
	Trace  []string `json:"trace,omitempty"`
	Token  string   `json:"token,omitempty"` // Phase 7: optional auth credential/token
	// search 动词的查询字段（出站 RequestSearch 用；入站由 dispatchFrame
	// Unmarshal 进 dcResp，两边 json tag 逐字对齐即可）。
	// dcReq 是**只发不收**的结构（服务端不拿它解析任何东西），所以在这里
	// 挂搜索专用字段不会和 dcResp 的同名 tag 打架。Size 复用为 limit
	// ——与 serveSearch 的读法一致。
	Query   string `json:"q,omitempty"`
	MinSize *int64 `json:"minSize,omitempty"`
	MaxSize *int64 `json:"maxSize,omitempty"`
}

// traceCtxKey context key (exported as TraceKey for source package to read; type is
// private to prevent misuse).
type traceCtxKey struct{}

// TraceKey fallback chain context key: value is []string (already-passed node id chain,
// excluding current node — current node is appended by caller before passing downstream).
var TraceKey = traceCtxKey{}

// CredentialFunc provides an authentication credential/token for requests over a connection (Phase 7 identity placeholder).
// Returns an empty string by default in Phase 6; populated in Phase 7.
type CredentialFunc func() string

// dcResp general response frame: shared by fetch responses (meta/data/done/err) and file
// index verb responses (created/uploaded/ack/list-resp/search-resp/info-resp/deleted/sync-resp).
type dcResp struct {
	Type    string     `json:"type"`
	Hash    string     `json:"hash,omitempty"`
	Total   int64      `json:"total,omitempty"`
	Offset  int64      `json:"offset"`
	Size    int64      `json:"size,omitempty"`
	Msg     string     `json:"msg,omitempty"`
	ReqID   string     `json:"reqId,omitempty"`
	Path    string     `json:"path,omitempty"`
	Name    string     `json:"name,omitempty"`
	Seq     int64      `json:"seq,omitempty"`
	Files   []FileInfo `json:"files"`
	LastSeq int64      `json:"lastSeq,omitempty"`
	Nonce   string     `json:"nonce,omitempty"` // fwd-challenge: one-time challenge (forward.go)
	Hmac    string     `json:"hmac,omitempty"`  // fwd-auth: HMAC-SHA256(key, nonce)
	Port    int        `json:"port,omitempty"`  // fwd-open: client-declared target port
	URL     string     `json:"url,omitempty"`   // pull: address for this node to fetch (pull.go)
	Psk     string     `json:"psk,omitempty"`   // psk-auth: peer's presented pre-shared key (psk.go)
	Code    string     `json:"code,omitempty"`  // machine-readable error code in err frames (consumers branch on code)
	Token   string     `json:"token,omitempty"` // Phase 7: optional requester/responder identity token
	// search 动词的查询字段（file_index_search.go）。**不放进独立结构体**是有意的：
	// dispatchFrame 把每个入站文本帧统一 Unmarshal 成 dcResp，搜索请求得走同一条路；
	// 拆成第二个结构体意味着要在 dispatch 里为它再开一次 Unmarshal 分支。
	//
	// MinSize/MaxSize 用 **指针** 而非 int64：size=0 是合法值（空文件在索引里
	// 就是 0），omitempty 的 int64 分不出「没传」和「传了 0」，指针则天然分得开，
	// 也才能表达「只限下界不加上界」。null / 缺省 → nil → 不施加该条件。
	Query   string `json:"q,omitempty"`        // search: 子串（name 或 path）
	MinSize *int64 `json:"minSize,omitempty"` // search: size 下界（含）
	MaxSize *int64 `json:"maxSize,omitempty"` // search: size 上界（含）
}

// connState records the request state machine and response routing for a connection.
// State ownership annotations (same connection full-duplex concurrent reuse, flat shared,
// not split into two copies):
//   - fetches/expect: outbound role (outbound.go requestFile/routeResponse)
//   - pendingUpload/binCh/binDone: inbound role (inbound.go serveUploadBegin/uploadWorker)
type connState struct {
	mu             sync.Mutex
	expect         *fetchState            // current expected binary data chunk download request
	fetches        map[string]*fetchState // reqId → download request
	pendingUpload  *uploadState           // current receiving streaming upload (only one at a time per connection)
	credentialFunc CredentialFunc        // connection-level credential callback (Phase 7)

	// verbWaits one-shot JSON response waiting slots (share-type "request-response" verbs):
	// reqId → raw response frame bytes. Separate from fetches because file fetching is
	// **streaming** (data goes through bounded queue + expect state machine), while
	// share/info etc. just need one JSON and finish; mixing into fetchState would add
	// phantom "no data chunks" branches. Value is raw JSON not parsed struct: share-resp
	// fields (collections/files) aren't in dcResp; re-marshaling would lose them.
	verbWaits map[string]chan []byte

	// H5 fix: binary data chunks (upload slices) delivered to connection-level worker
	// (binCh/binDone); WriteAt/Complete (fsync + full file hashFile) moved out of pion
	// message pump — previously, the moment an 8GB upload completed, all other frames on
	// this connection froze until Complete finished (head-of-line blocking; slow disk
	// would deadlock the entire connection). Routing decision (who owns it) happens in
	// the message pump (cheap); worker only does IO; ordering guaranteed by single worker.
	binCh   chan binaryChunk
	binDone chan struct{}

	// adminUp management-plane binary upload collection slot (single slot, admin.go):
	// after browser sends admin frame with binary=true declaration, in-pump routes
	// subsequent binary frames here (writing to temp file); on completion triggers
	// serveAdminUploadComplete (multipart internal forwarding). Mutually exclusive with
	// pendingUpload (file index upload): same connection has at most one upload collector
	// at a time.
	adminUp *adminUploadState

	// forward forwarding tunnel (single slot, forward.go): same connection has one active
	// forwarding stream at a time. fwdHandshake is handshake waiting state (fwd-open sent
	// → placeholder before ok/err arrives).
	fwd   *fwdStream
	fwdHs *fwdHandshake
	fwdCh chan fwdChunk // fwd chunks → connection-level worker writes tunnel (bounded backpressure, same as binCh)

	// pskOK whether the peer has passed this node's pre-shared key verification (psk.go gate).
	// Only affects "whether this node serves it", not its responses to our own requests
	// (responses go through routeResponse; we have no reason to block our own data).
	pskOK bool
}

// fwdChunk a chunk of forwarding data to be written to the tunnel (ownership carried
// with the chunk — tunnel may have been swapped/closed; worker writing to a closed out
// gets an error and discards, matching "forwarding is best-effort stream" semantics).
type fwdChunk struct {
	fw   *fwdStream
	data []byte
}

// binaryChunk a pending-to-persist upload slice (routing decided in message pump, worker
// only does IO).
// up is fileIndex upload (inbound.go uploadWorker writes UploadSession);
// au is admin management-plane upload (admin.go writes temp file, collected before multipart forwarding).
type binaryChunk struct {
	up     *uploadState
	au     *adminUploadState // admin upload chunk (mutually exclusive with up, one frame belongs to only one)
	offset int64
	data   []byte
	last   bool // this chunk completes → trigger Complete (only when bitmap is full)
}

// uploadState slice upload receiving state (connection-level single stream: one upload
// request → one data chunk). Multi-source concurrency = multiple connections parallel
// WriteAt different slices; within same connection, slices are serial (request-response
// pairing).
type uploadState struct {
	reqID   string
	offset  int64 // start of this slice (chunk-aligned)
	size    int64 // length of this slice
	got     int64
	sess    *UploadSession
	created time.Time // M6: creation time — peer sending upload header without data chunks
	// permanently occupies the slot
}

// fetchState streaming collection state for one file fetch (outbound role).
// Data chunks don't reside in state (streaming: delivered to bounded queue q for
// fetchReader consumption); received only does byte counting — for done frame integrity
// verification.
type fetchState struct {
	reqID    string
	size     int64         // expected data chunk size (upper limit verification in maxPeerFetchSize)
	received int64         // bytes delivered to queue
	// total: file total size declared by peer's meta frame (-1 = unknown, see fetchReader.Total).
	// Uses atomic instead of plain field: write happens in message pump (routeResponse,
	// holds st.mu); read happens in consumer goroutine (reader.Total()); plain field
	// would be a data race.
	total    atomic.Int64
	q        chan []byte   // data chunk queue (bounded 8, message pump delivers / fetchReader consumes)
	done     chan struct{} // close → peer's done frame (transfer complete; remaining chunks in q still consumable)
	errCh    chan error    // error (including connection close)
	closed   chan struct{} // local cancellation (reader.Close): pump stops delivering, chunks discarded
}

// bindConn binds the message dispatch for a connection: parses JSON header, routes by reqId,
// appends binary chunks to expect state.
// Trap: this connection is "full-duplex reused" — it both serves the peer's req (serveFile)
// and receives responses for our own outgoing requests (routeResponse). They are
// distinguished by frame type + reqId:
//   - Text frame with type being a verb (req/create/upload/list/info/delete/sync) →
//     inbound role responds
//   - Text frame with other types → outbound role responses, routed by reqId
//   - Binary frame → data chunk, routing decision (belongs to upload or expect) done in
//     pump, disk IO delegated to connection-level worker (H5, see inbound.go uploadWorker)
//
// connRanker connection-level UUID (implemented by rtcSession; WSSession/fakeSession
// don't have it, returning "" means incomparable).
type connRanker interface{ ConnID() string }

func sessionRank(s Session) string {
	if r, ok := s.(connRanker); ok {
		return r.ConnID()
	}
	return ""
}

// bindConn binds the message dispatch for a connection: parses JSON header, routes by reqId,
// appends binary chunks to expect state.
// Trap: this connection is "full-duplex reused" — it both serves the peer's req (serveFile)
// and receives responses for our own outgoing requests (routeResponse). They are
// distinguished by frame type + reqId:
//   - Text frame with type being a verb (req/create/upload/list/info/delete/sync) →
//     inbound role responds
//   - Text frame with other types → outbound role responses, routed by reqId
//   - Binary frame → data chunk, routing decision (belongs to upload or expect) done in
//     pump, disk IO delegated to connection-level worker (H5, see inbound.go uploadWorker)
//
// connRanker connection-level UUID (implemented by rtcSession; WSSession/fakeSession
// don't have it, returning "" means incomparable).
//
// Dedup policy (2026-08-19, inlined from former dedupConn): same-peer dual-connection
// dedup — mutual dialing (A↔B dialing each other simultaneously) or reconnection races
// leave two connections under the same peerID. Retention policy must be consistent on
// both ends (connection-level UUID lexicographic comparison, smaller wins): with mutual
// dialing, both ends see two connections {own dial, peer's incoming}; if each keeps its
// own outgoing connection, the retained one is exactly the broken link the other side
// already closed (integration test TestSelfHostedSignalAndDiscover occasionally fails due
// to this — discovery background: after dedup batch went live, integration test failed
// 1/4 of the time, fetch timeout 10.5s). Connection UUID is visible on both ends with the
// same value → both take the lexicographically smaller → both retain the same physical
// connection. fakeSession has no connection UUID (equal rank) → retains the new connection
// (test double semantics). Exception: local WS sessions are not deduped — multiple browser
// tabs each have a local session; actively closing old tab's connection would interrupt
// its in-progress inbound service.
//
// ⚠️ State-before-conns ordering: the connState is created in s.pending BEFORE the
// connection is registered in s.conns. This eliminates the race where OpenStream or
// requestVerbPayload reads a connection from s.conns before its state is registered in
// s.pending, which would return "peerjs: connection not bound" (CI discovery:
// TestPeerPullSavesToLocalDrive fails ~1/4 with "open stream: peerjs: connection not
// bound" — dedup window race).
// bindConn creates connState, registers OnMessage/OnClose, and calls bindConnPrepared.
// Kept for backward compatibility with test callers that do not pre-create state.
// Production callers (onIncomingConnection, connectLoop) use bindConnPrepared directly
// with handlers registered before OnOpen to close the pre-bindConn frame-drop race.
func (s *PeerJSService) bindConn(c Session) {
	if s.IsPeerBlocked(c.ID()) {
		log.LogWarn("peerjs: bindConn for blocked peer %s rejected", c.ID())
		c.Close()
		return
	}

	s.mu.Lock()
	old := s.conns[c.ID()]
	s.mu.Unlock()

	// Dedup decision (before touching conns map; eliminates "not bound" race).
	keepOld := old != nil && old != c && c.ID() != "local" &&
		sessionRank(old) < sessionRank(c)
	if keepOld {
		log.LogInfo("peerjs: dedup connection to %s, closing newer", c.ID())
		c.Close()
		return
	}

	st := &connState{
		fetches:   make(map[string]*fetchState),
		verbWaits: make(map[string]chan []byte),
		binCh:     make(chan binaryChunk, 16),
		binDone:   make(chan struct{}),
		fwdCh:     make(chan fwdChunk, 16),
	}

	// ⚠️ OnMessage MUST be registered **before doing anything that may yield**.
	// Discovery background: CI panel E2E occasionally shows "psk: this node requires
	// a pre-shared key" (~1/5), node logs show only our own outgoing psk-auth with no
	// peer's psk ok/mismatch — i.e. the peer's auth frame was never seen. Root cause
	// is ordering: in the peerjs library, dc.OnMessage **silently drops** frames when
	// onMessage is nil, and dc.OnOpen (which runs bindConn) and dc.OnMessage are two
	// concurrently dispatchable callbacks; the send below may yield; the peer sending
	// psk-auth at the moment of open could easily arrive before registration and be
	// silently dropped — the gate then waits forever, all subsequent verbs are rejected,
	// with no error at all.
	// Early registration only affects inbound, not the "psk-auth is our first frame"
	// outbound semantics.
	c.OnMessage(func(msg peerjs.Frame) { s.dispatchFrame(c, st, msg) })
	c.OnClose(func() { s.cleanupConn(c, st) })

	s.bindConnPrepared(c, st, old)
}

// bindConnPrepared binds a connection with a pre-created connState.
// st and OnMessage/OnClose MUST be registered by the caller **before** the
// DataChannel opens — closing the race window where the peer's psk-auth frame
// arrives before bindConn registers OnMessage and is silently dropped by the
// pion-level dc.OnMessage callback (which checks c.onMessage == nil).
//
// 2026-10-06 fix (bindConn) covered only the yield window inside pskSendAuth.
// This function extends early-registration to the bindConn-call window itself,
// which is the second, uncovered race: frames arriving between DataChannel open
// and bindConn execution are now received.
func (s *PeerJSService) bindConnPrepared(c Session, st *connState, old Session) {
	// State before conns: OpenStream reads conns then looks up pending.
	s.pendingMu.Lock()
	s.pending[c] = st
	s.pendingMu.Unlock()

	s.mu.Lock()
	s.conns[c.ID()] = c
	s.mu.Unlock()

	if old != nil && old != c && c.ID() != "local" {
		log.LogInfo("peerjs: dedup connection to %s, closing stale", c.ID())
		old.Close()
	}

	go s.uploadWorker(c, st)
	// Forwarding write worker separated from upload worker (2026-08-18): large upload
	// Complete (fsync + hashFile) no longer blocks same-connection forwarding tunnel, see
	// inbound.go fwdWorker.
	go s.fwdWorker(c, st)

	// PSK auth timeout: unauthenticated connections are a resource exhaustion vector.
	// If the peer doesn't present psk-auth within pskAuthTimeout, close the connection.
	// The timer checks st.pskOK (set by servePskAuth) — if the peer authenticates first,
	// the timer callback is a no-op. If the connection closes early, c.Close() is a no-op.
	if s.pskEnabled() && !isSelfSession(c) {
		time.AfterFunc(pskAuthTimeout, func() {
			st.mu.Lock()
			ok := st.pskOK
			st.mu.Unlock()
			if !ok {
				log.LogWarn("peerjs: no psk-auth from %s within %s, closing", c.ID(), pskAuthTimeout)
				c.Close()
			}
		})
	}

	// PSK gate: if a key is configured, present it (our first frame).
	s.pskSendAuth(c)
}

// dispatchFrame connection message pump: parses JSON header, routes by reqId, appends
// binary chunks to expect state. In-pump handling must maintain frame order — some cases
// are intentionally synchronous:
//   - admin (binary upload declaration): adminUp slot claim MUST happen in-pump,
//     otherwise subsequent binary frames arrive before adminUp is set → data chunks lost
//     (discovery background: initial version went async, all upload chunks lost)
//   - fwd-data header (forwarding chunk declaration): same header-chunk contiguity
//     constraint as file transfer; in-pump frame-ordered processing means no race
func (s *PeerJSService) dispatchFrame(c Session, st *connState, msg peerjs.Frame) {
	if msg.IsText {
		var r dcResp
		if err := json.Unmarshal(msg.Data, &r); err != nil || r.Type == "" {
			return
		}
		// PSK gate: handle handshake frames first, then filter "make me work" inbound
		// verbs with the gate (psk.go). Before peer presents, these verbs all return err.
		if r.Type == "psk-auth" {
			s.servePskAuth(c, st, r.Psk)
			return
		}
		if r.Type == "psk-ok" || r.Type == "psk-err" {
			return // Client-side handshake receipts: not needed here (presenter doesn't wait
			// for receipt, see psk.go)
		}
		if s.pskGate(c, st, r) {
			return
		}
		if s.anonGate(c, st, r) {
			return
		}
		switch r.Type {
		case "req":
			// Peer requests this node's file (download)
			req := dcReq{
				Type:   r.Type,
				Hash:   r.Hash,
				Offset: r.Offset,
				Size:   r.Size,
				ReqID:  r.ReqID,
				Token:  r.Token,
			}
			go s.serveFile(c, req)
		case "create":
			// Peer registers external file (sha256 → absolute path)
			go s.serveCreate(c, r)
		case "upload":
			// Peer streaming upload: start receiving (subsequent data frames write to UploadSink)
			go s.serveUploadBegin(c, st, r)
		case "pull":
			// Peer gives a URL, asks this node to fetch it (pull.go: network ingestion, with SSRF protection)
			go s.servePull(c, r)
		case "list":
			go s.serveList(c, r)
		case "search":
			// 文件索引子串检索（serveSearch）。与 list 同一道门禁、同一条
			// 响应路径，只是多一个 q 条件——单独 verb 而不是 list 的可选参数，
			// 是为了让「无 q 的全量列」与「带 q 的检索」在协议上互不影响：
			// 老对端发 list 的行为一个字都不会变。
			go s.serveSearch(c, r)
		case "share":
			// Peer asks "what did you share" (share.go, netdisk target M2): **only returns
			// explicitly shared scope**, strictly distinct from list (local management list
			// full set).
			go s.serveShare(c, r)
		case "info":
			go s.serveInfo(c, r)
		case "delete":
			go s.serveDelete(c, r)
		case "sync":
			go s.serveSync(c, r)
		case "admin":
			// Management-plane verb (admin.go): used only by local WS sessions (browser),
			// internally forwards to gin engine reusing all HTTP controllers. WebRTC
			// connections receiving admin frames are rejected in serveAdmin (ID!="local").
			// Synchronous execution: adminUp slot claim for binary upload declaration
			// MUST happen in-pump, otherwise subsequent binary frames arrive before adminUp
			// is set → data chunks lost (discovery background: initial version went async,
			// all upload chunks lost).
			s.serveAdmin(c, st, msg.Data)
		case "fwd-open":
			go s.serveForwardOpen(c, st, r)
		case "fwd-auth":
			go s.serveForwardAuth(c, st, r)
		case "fwd-data":
			// Forwarding data header (forward.go): declares "next binary chunk belongs to
			// forwarding tunnel". Header-chunk contiguity same as file transfer (SendFrame
			// atomic send); in-pump frame-ordered processing means no race; silently
			// discarded and pending cleared when illegal (no tunnel / already closed).
			st.mu.Lock()
			fw := st.fwd
			if fw != nil && !fw.closed {
				fw.pending = true
			}
			st.mu.Unlock()
		case "fwd-close":
			go s.serveForwardClose(c, st, r)
		case "fwd-challenge", "fwd-ok", "fwd-err":
			// Client-side handshake responses (OpenForward waiting) — routed to same slot
			// as file fetch responses
			s.routeForwardResponse(st, r)
		default:
			s.routeResponse(st, r, msg.Data)
		}
		return
	}
	// Binary data chunk: routing decision in pump (cheap, maintains ordering consistency
	// with text frames); disk IO (WriteAt/Complete) delegated to connection-level worker (H5)
	st.mu.Lock()
	fw := st.fwd
	if fw != nil && fw.pending && !fw.closed {
		// Forwarding chunk: deliver to fwdCh (bounded backpressure, worker writes tunnel;
		// connection close releases)
		fw.pending = false
		select {
		case st.fwdCh <- fwdChunk{fw: fw, data: msg.Data}:
		case <-st.binDone:
		}
		st.mu.Unlock()
		return
	}
	up := st.pendingUpload
	if up != nil {
		up.got += int64(len(msg.Data))
		if up.got >= up.size {
			st.pendingUpload = nil
		}
	}
	// Management-plane upload collection (admin.go): second priority after pendingUpload.
	// Chunks delivered to binCh (reuses H5 worker, writes to disk outside pump); on
	// completion (got>=size) triggers internal multipart forwarding.
	au := st.adminUp
	if au != nil {
		au.got += int64(len(msg.Data))
		// Defense: declared size mismatch / peer sent extra → abort and clean up
		abort := au.got > au.size || time.Since(au.created) > adminUploadTimeout
		last := false
		if abort {
			st.adminUp = nil
			au.aborted = true
			last = true // empty chunk also delivered: worker sees aborted → cleanup and return err
		} else if au.got >= au.size {
			st.adminUp = nil
			last = true
		}
		select {
		case st.binCh <- binaryChunk{au: au, data: msg.Data, last: last}:
		case <-st.binDone:
		}
	}
	f := st.expect
	if up == nil && f != nil {
		// Data chunk delivered to fetch queue (bounded backpressure); if locally cancelled
		// (closed), discard. Only count on successful delivery (no counting after cancel;
		// expect cleaned up by caller).
		select {
		case f.q <- msg.Data:
			f.received += int64(len(msg.Data))
			if f.received >= f.size {
				st.expect = nil
			}
		case <-f.closed:
		}
	}
	last := up != nil && up.got >= up.size
	st.mu.Unlock()
	if up != nil {
		select {
		case st.binCh <- binaryChunk{up: up, offset: up.offset, data: msg.Data, last: last}:
		case <-st.binDone: // connection closed: stop delivering
			return
		}
	}
}

// cleanupConn connection close cleanup: deregister connection, release fetch/forward/upload
// states.
// Order-sensitive: fwdOut taken out inside lock, Close outside lock (closing out triggers
// read-side return, can't be done while holding st.mu — forwardPump might be waiting on
// the lock in a goroutine reading from out).
func (s *PeerJSService) cleanupConn(c Session, st *connState) {
	s.mu.Lock()
	// Value-equality guard: dedup-eliminated connection cleanup won't mistakenly delete
	// retained connection (conns[c.ID()] already overwritten by retained connection during dedup).
	if s.conns[c.ID()] == c {
		delete(s.conns, c.ID())
	}
	s.mu.Unlock()
	s.pendingMu.Lock()
	delete(s.pending, c)
	s.pendingMu.Unlock()
	if st == nil {
		return
	}
	var fwdOut net.Conn
	st.mu.Lock()
	// Management-plane upload collection (admin.go): connection closed → abort and clean up
	// temp file
	if au := st.adminUp; au != nil {
		st.adminUp = nil
		au.cleanupTemp() // order: close handle first, then delete file (reversed on Windows = can't delete)
	}
	for _, f := range st.fetches {
		// Connection closed: notify fetch reader to exit (errCh), and release pump
		// delivery blockage (close(f.closed) idempotent check — reader may have already
		// self-cleaned)
		select {
		case f.errCh <- fmt.Errorf("peerjs: connection closed"):
		default:
		}
		select {
		case <-f.closed:
		default:
			close(f.closed)
		}
	}
	// Forwarding: tunnel dies with it — close out to release read side (forwardPump exits),
	// caller (OpenForward's returned net.Conn) read side immediately EOFs
	if st.fwd != nil {
		fwdOut = st.fwd.out
	}
	st.mu.Unlock()
	// H5: notify upload worker to exit (unconsumed chunks discarded — connection dead,
	// session residue cleaned by file_index's 10-minute reap)
	close(st.binDone)
	if fwdOut != nil {
		fwdOut.Close()
	}
	log.LogInfo("peerjs: connection closed from %s", c.ID())
}
