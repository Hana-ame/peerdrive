package transport

// admin.go: Management-plane verbs (browser manages this node via local WS session).
//
// Why only local sessions (c.ID()=="local") are allowed:
//   - WebRTC/PeerJS connections may come from any node on public signaling; if
//     admin verbs were also implemented there, it would expose this node's management
//     interface (auth tokens, file read/write, collection changes) to unknown peers,
//     completely opening the permission surface. Management plane is fixed to local
//     WS sessions; WebRTC only carries file data plane (req/upload/index verbs, see
//     conn.go bindConn dispatch).
//   - Browser establishes WSSession via /ws/peer (id="local", Origin whitelist
//     validation, see peerjs_routes.go registerPeerJSRoutes), then sends admin frames
//     to manage this node.
//
// Internal forwarding design: admin frame → construct internal *http.Request → inject
// into gin engine's ServeHTTP (injected via SetAdminHandler in router.SetupRouter) →
// reuse all controller logic (files/collections/sharing/tasks/BT/IPFS etc. with zero
// duplication). Response:
//   - JSON response → {"type":"admin-resp","status":N,"body":<raw JSON>,"reqId"}
//   - Binary response (file stream) → {"type":"admin-bin","status":N,"size":N,"reqId"}
//     + immediately followed by a binary frame (sent as a whole; capped at adminBinMax
//     to prevent OOM)
//   - Failure → {"type":"err","msg":"...","reqId"}
//
// Upload: admin frame with binary=true + filename/size, subsequent binary frames are
// collected as request body into a temp file; once complete, constructs multipart/
// form-data and forwards (controller's FormFile reads it transparently). Mutually
// exclusive with old upload verb (file index / download directory session) — admin
// upload goes through storageDir content-addressed storage (/files/upload semantics).
//
// Authentication: frontend puts getAuthToken() token into admin frame's token field;
// forwarding injects it into Authorization header → gin's AuthRequired middleware
// behaves identically to HTTP.

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
)

// adminBinMax management-plane binary response limit (64MB): internal forwarding already
// keeps the entire response in memory (httptest recorder); large files should use req verb
// chunked fetch (frontend ws.js download path).
const adminBinMax = 64 << 20

// adminUploadTimeout upload collection timeout: if browser declares binary upload but
// sends no data chunks, it would permanently occupy the slot (same as M6 fix for upload
// verb). Timeout aborts and cleans up temp file.
const adminUploadTimeout = 30 * time.Second

// AdminHandler internal forwarding function: passes a pre-constructed internal request
// to the gin engine. Returns HTTP status code, response body, Content-Type. Injected by
// the router layer.
type AdminHandler func(req *http.Request) (int, []byte, string, error)

// adminReq management-plane request frame (browser → local session).
// When binary=true, subsequent binary frames serve as the request body (multipart upload).
type adminReq struct {
	Type   string `json:"type"`
	Method string `json:"method"`
	Path   string `json:"path"` // includes query string, e.g. "/files/upload"
	Body   any    `json:"body,omitempty"`
	Token  string `json:"token,omitempty"`

	// Binary upload declaration (binary=true): filename for multipart filename,
	// field for multipart field name (default "file"; BT torrent upload uses "torrent")
	Binary   bool   `json:"binary,omitempty"`
	Filename string `json:"filename,omitempty"`
	Field    string `json:"field,omitempty"`
	Size     int64  `json:"size,omitempty"`

	ReqID string `json:"reqId,omitempty"`
}

// adminResp management-plane response frame (local session → browser).
// Body is raw JSON (gin-serialized response body); frontend routes by reqId then uses
// directly.
type adminResp struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Body   any    `json:"body,omitempty"`
	ReqID  string `json:"reqId,omitempty"`
}

// ginH lightweight JSON object (avoids introducing gin dependency).
type ginH map[string]any

// adminBinResp binary response header (JSON text frame, followed by a binary data frame).
type adminBinResp struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Size   int64  `json:"size"`
	ReqID  string `json:"reqId,omitempty"`
}

// adminUploadState management-plane binary upload collection state (connection-level
// single slot, same as pendingUpload).
type adminUploadState struct {
	reqID    string
	filename string
	field    string // multipart field name (default "file"; BT torrent uses "torrent")
	reqPath  string // forwarding target path (default /files/upload; BT torrent uses /bt/torrent)
	token    string // Bearer token from declaration frame; forwarded with multipart internal request after upload completes
	got      int64
	size     int64
	f        *os.File // temp file (body collection), forwarded as multipart after completion
	path     string
	created  time.Time
	aborted  bool // in-pump abort marker (size exceeded / timeout) → worker cleans up and returns err
}

// cleanupTemp closes the handle THEN deletes the temp file (idempotent).
//
// Order matters: on Windows, open files can't be deleted ("The process cannot access
// the file because it is being used by another process"), os.Remove silently fails,
// and temp files remain on disk permanently (each aborted management-plane upload
// leaves one behind). On Linux, order doesn't matter — open files can still be unlinked.
// So this bug is only visible on real Windows machines (discovered 2026-09-20; Linux
// CI was all green before). Consolidated into one function to avoid each site needing
// to remember the correct order.
func (au *adminUploadState) cleanupTemp() {
	if au == nil {
		return
	}
	if au.f != nil {
		_ = au.f.Close()
		au.f = nil
	}
	if au.path != "" {
		_ = os.Remove(au.path)
		au.path = ""
	}
}

// serveAdmin handles management-plane admin frames (local sessions only; dispatch in
// bindConn).
// Note: the adminUp slot setting for binary upload declaration frames MUST happen
// inside the message pump (synchronously), otherwise if the pump has already routed
// subsequent binary frames and adminUp is still nil → data chunks are lost (discovery
// background: initial version had all case "admin" go async, upload chunks arrived
// before adminUp was set, upload never completed).
func (s *PeerJSService) serveAdmin(c Session, st *connState, raw []byte) {
	if c.ID() != "local" {
		// Management plane not open to WebRTC connections (file header comment: prevent
		// permission surface exposure)
		c.SendJSON(dcResp{Type: "err", Msg: "admin verb is only allowed on the local session"})
		return
	}
	var ar adminReq
	if err := json.Unmarshal(raw, &ar); err != nil || ar.Method == "" || ar.Path == "" {
		c.SendJSON(dcResp{Type: "err", Msg: "invalid admin request"})
		return
	}
	if s.adminHandler == nil {
		c.SendJSON(dcResp{Type: "err", Msg: "admin handler not configured", ReqID: ar.ReqID})
		return
	}

	// Binary upload: synchronous slot claim (in-pump); collection driven by binary frame
	// routing (conn.go); on completion, pump triggers serveAdminUploadComplete.
	if ar.Binary {
		// Allow size==0 (empty file upload), symmetric with fileIndex upload verb's
		// empty file handling; only negative or over-limit is rejected.
		if ar.Size < 0 || ar.Size > adminBinMax {
			c.SendJSON(dcResp{Type: "err", Msg: "invalid admin upload size", ReqID: ar.ReqID})
			return
		}
		f, err := os.CreateTemp("", "peerdrive-admin-upload-*")
		if err != nil {
			c.SendJSON(dcResp{Type: "err", Msg: "upload temp file failed: " + err.Error(), ReqID: ar.ReqID})
			return
		}
		st.mu.Lock()
		// Defense: previous slot not completed (browser abandoned/crashed) — replace
		// directly and clean old file.
		// Browser declares uploads serially; two concurrent declarations won't happen;
		// malicious repeat declarations only clean temp files.
		// MUST return err to old reqId: otherwise its browser Promise hangs forever
		// (pending entry cleared only on disconnect), and old upload's late chunks mix
		// into new au's got count causing new upload to be falsely aborted for size
		// exceeded, with err pointing to the new reqId (misleading troubleshooting).
		// Discovery background: code review 2026-08-18.
		if old := st.adminUp; old != nil {
			old.cleanupTemp()
			st.mu.Unlock()
			_ = c.SendJSON(dcResp{Type: "err", Msg: "admin upload replaced by new declaration", ReqID: old.reqID})
			st.mu.Lock()
		}
		au := &adminUploadState{
			reqID:    ar.ReqID,
			filename: ar.Filename,
			field:    ar.Field,
			// Forwarding target path: default /files/upload (storage semantics); BT torrent
			// upload passes path=/bt/torrent. Old implementation hardcoded /files/upload,
			// causing torrent upload to hit /files/upload with field=torrent not accepted
			// (discovery background: frontend btTorrentUpload migration checked admin frame
			// vs backend forwarding path).
			reqPath: ar.Path,
			token:   ar.Token,
			size:    ar.Size,
			f:       f,
			path:    f.Name(),
			created: time.Now(),
		}
		st.adminUp = au
		st.mu.Unlock()
		if ar.Size == 0 {
			// Empty file has no subsequent binary frames; immediately remove from single
			// slot and trigger multipart forwarding, otherwise adminUp would hold the slot
			// (connection-level single slot) until timeout cleanup.
			st.mu.Lock()
			if st.adminUp == au {
				st.adminUp = nil
			}
			st.mu.Unlock()
			go s.serveAdminUploadComplete(c, au)
			return
		}
		log.LogInfo("peerjs-admin: upload begin %s size=%d", ar.Filename, ar.Size)
		return // Non-empty: pump triggers serveAdminUploadComplete after completion
	}

	// Normal request: JSON body → internal HTTP forwarding.
	// Async dispatch: internal HTTP forwarding may be slow (large lists/slow clients);
	// done outside the pump to avoid blocking other frames on the same connection
	// (download/upload chunk routing happens in-pump, see conn.go).
	var body []byte
	if ar.Body != nil {
		body, _ = json.Marshal(ar.Body)
	}
	req, err := s.buildAdminRequest(ar.Method, ar.Path, bytes.NewReader(body), "application/json", ar.Token)
	if err != nil {
		c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: ar.ReqID})
		return
	}
	go s.dispatchAdmin(c, req, ar.ReqID)
}

// adminUploadChunk worker-side handling of one admin upload chunk (called by
// inbound.go uploadWorker, IO moved out of message pump — same as H5 handling for
// fileIndex upload).
// Non-last chunk: write to temp file. Last chunk: write first, then on completion
// → internal multipart forwarding with response frame; aborted → cleanup + err.
func (s *PeerJSService) adminUploadChunk(c Session, au *adminUploadState, data []byte, last bool) {
	if len(data) > 0 {
		if _, err := au.f.Write(data); err != nil {
			// Write failure: MUST clean up temp file and handle, otherwise leak (file +
			// fd persists). Same cleanup semantics as aborted branch; aborted branch is below.
			// Discovery background: code review 2026-08-18 (previously only aborted branch cleaned).
			au.cleanupTemp()
			_ = c.SendJSON(dcResp{Type: "err", Msg: "admin upload write failed: " + err.Error(), ReqID: au.reqID})
			return
		}
	}
	if !last {
		return
	}
	if au.aborted {
		au.cleanupTemp()
		_ = c.SendJSON(dcResp{Type: "err", Msg: "upload aborted: size mismatch or timeout", ReqID: au.reqID})
		return
	}
	s.serveAdminUploadComplete(c, au)
}

// serveAdminUploadComplete internal forwarding after admin upload completes.
// multipart construction + handler call both run in worker goroutine, not blocking message
// pump.
func (s *PeerJSService) serveAdminUploadComplete(c Session, au *adminUploadState) {
	defer au.cleanupTemp() // temp file must be cleaned (normal/abnormal paths both reach here)
	if au.got < au.size {
		c.SendJSON(dcResp{Type: "err", Msg: "upload aborted: incomplete", ReqID: au.reqID})
		return
	}
	// Upload forwarding path: declaration frame's path (e.g. /files/upload or /bt/torrent);
	// empty falls back to /files/upload (storage upload semantics for old clients without
	// path).
	// Trap: au.f's write offset is already at file end, must Seek(0) to read from start,
	// otherwise io.Copy reads 0 bytes (discovery background: admin upload test got 0 bytes).
	field := au.field
	if field == "" {
		field = "file"
	}
	reqPath := au.reqPath
	if reqPath == "" {
		reqPath = "/files/upload"
	}
	if _, err := au.f.Seek(0, io.SeekStart); err != nil {
		c.SendJSON(dcResp{Type: "err", Msg: "upload rewind failed: " + err.Error(), ReqID: au.reqID})
		return
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		defer mw.Close()
		fw, err := mw.CreateFormFile(field, filepath.Base(au.filename))
		if err != nil {
			return
		}
		if _, err := io.Copy(fw, au.f); err != nil {
			return
		}
	}()
	req, err := s.buildAdminRequest("POST", reqPath, pr, mw.FormDataContentType(), au.token)
	if err != nil {
		pw.Close()
		c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: au.reqID})
		return
	}
	s.dispatchAdmin(c, req, au.reqID)
}

// buildAdminRequest constructs internal request: method/path/body + token → Authorization
// header. Empty token means no header set (anonymous request, AuthOptional allows).
func (s *PeerJSService) buildAdminRequest(method, path string, body io.Reader, contentType, token string) (*http.Request, error) {
	req, err := http.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

// dispatchAdmin internal forwarding: handler (gin engine) → response frame back to browser.
// HTTP semantics (including error status codes) carried uniformly via admin-resp: frontend
// behaves identically to fetch version (err.status + err.data distinguishes structured error
// bodies like 409 conflict list, see api.js request()). Binary responses (application/
// octet-stream etc. non-JSON) split into admin-bin header + binary frame.
func (s *PeerJSService) dispatchAdmin(c Session, req *http.Request, reqID string) {
	status, respBody, contentType, err := s.adminHandler(req)
	if err != nil {
		c.SendJSON(adminResp{Type: "admin-resp", Status: http.StatusInternalServerError, Body: ginH{"error": err.Error()}, ReqID: reqID})
		return
	}
	// Response classification: JSON → admin-resp; text → admin-resp (string body);
	// only true binary (file stream) goes admin-bin.
	// Trap: initial version checked if respBody starts with "{" — text/plain responses
	// (e.g. /ping's "pong") were misclassified as binary → frontend received Uint8Array
	// instead of string, and admin-bin occupied binaryExpect slot (discovery background:
	// E2E smoke test /ping returned Buffer).
	if len(respBody) > 0 && json.Valid(respBody) {
		var b any
		if err := json.Unmarshal(respBody, &b); err != nil {
			b = string(respBody)
		}
		c.SendJSON(adminResp{Type: "admin-resp", Status: status, Body: b, ReqID: reqID})
		return
	}
	if status >= 400 || contentType == "" || strings.HasPrefix(contentType, "text/") {
		// Non-JSON error body / text response: pass through as string body
		c.SendJSON(adminResp{Type: "admin-resp", Status: status, Body: string(respBody), ReqID: reqID})
		return
	}
	// Binary response (file stream, e.g. /collections/.../{path}): header + single binary
	// frame. adminBinMax limit enforced at caller (large files use req verb).
	if int64(len(respBody)) > adminBinMax {
		c.SendJSON(adminResp{Type: "admin-resp", Status: http.StatusRequestEntityTooLarge, Body: ginH{"error": "admin binary response exceeds 64MB limit; use req verb streaming"}, ReqID: reqID})
		return
	}
	if err := c.SendFrame(adminBinResp{Type: "admin-bin", Status: status, Size: int64(len(respBody)), ReqID: reqID}, respBody); err != nil {
		log.LogWarn("peerjs-admin: send binary resp failed: %v", err)
	}
}

// SetAdminHandler injects internal forwarding handler (called by router.SetupRouter, see
// peerjs_routes.go registerPeerJSRoutes). h wraps gin engine: constructs internal
// *http.Request → engine.ServeHTTP(recorder) → returns status/response body/Content-Type.
// Locking: adminHandler is set during SetupRouter assembly, then read-only (serveAdmin
// called concurrently).
func (s *PeerJSService) SetAdminHandler(h AdminHandler) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	s.adminHandler = h
}
