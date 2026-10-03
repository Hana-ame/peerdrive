package transport

// admin_test.go: unit tests for the admin management-plane verb (no real network or gin engine --
// a mock AdminHandler verifies the frame protocol, binary upload collection, and WebRTC rejection).
//
// Discovery background: the admin verb is the core channel for "moving the frontend entirely onto
// ws/peerjs" -- the browser sends admin frames over the local WS session to manage this node, and
// they are forwarded internally to the gin engine, reusing every HTTP controller. The tests cover
// three key paths: JSON request forwarding, binary upload collection, and non-local-session
// rejection (the management plane is not exposed over WebRTC, to prevent privilege-plane holes).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// testAdminSvc builds a service with a mock admin handler (no real signaling).
func testAdminSvc(t *testing.T, h AdminHandler) *PeerJSService {
	t.Helper()
	svc := newTestPeerJSService(t)
	svc.SetAdminHandler(h)
	return svc
}

// wsPair establishes a local WS session (server-side BindLocal + a client gorilla connection).
func wsPair(t *testing.T, svc *PeerJSService) (*websocket.Conn, *httptest.Server) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(NewWSSession("local", conn))
	}))
	t.Cleanup(srv.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[4:]+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	// wait for the BindLocal registration (bindConn completes synchronously, but this ensures the readLoop is active)
	time.Sleep(100 * time.Millisecond)
	return conn, srv
}

// readAdminResp reads frames until type is admin-resp / admin-bin / err.
// For admin-bin, keep reading the following binary frames (the header-body must be contiguous, as
// with a req pull) up to size bytes or the next text frame. Returns type, status code, body, and binary data.
func readAdminResp(t *testing.T, conn *websocket.Conn) (typ string, status int, body json.RawMessage, bin []byte) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if mt == websocket.BinaryMessage {
			bin = append(bin, data...)
			continue
		}
		var resp struct {
			Type   string          `json:"type"`
			Status int             `json:"status"`
			Body   json.RawMessage `json:"body"`
			Msg    string          `json:"msg"`
			Size   int64           `json:"size"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		switch resp.Type {
		case "admin-bin":
			// header received: keep reading binary blocks until size bytes (a single block in this test)
			if resp.Size == 0 {
				return resp.Type, resp.Status, resp.Body, bin
			}
			for int64(len(bin)) < resp.Size {
				mt2, d2, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("read bin: %v", err)
				}
				if mt2 != websocket.BinaryMessage {
					t.Fatalf("after admin-bin should follow binary frame, got %d", mt2)
				}
				bin = append(bin, d2...)
			}
			return resp.Type, resp.Status, resp.Body, bin
		case "admin-resp":
			return resp.Type, resp.Status, resp.Body, bin
		case "err":
			t.Fatalf("server err: %s", resp.Msg)
		default:
			continue
		}
	}
	t.Fatal("waiting for admin response timed out")
	return
}

// TestAdminJSONRequest a local WS session sends an admin JSON request -> the mock handler receives
// the complete method/path/token -> the response goes back to the browser (the body embedded verbatim).
func TestAdminJSONRequest(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath, gotToken string
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		mu.Lock()
		gotMethod, gotPath = req.Method, req.URL.RequestURI()
		gotToken = req.Header.Get("Authorization")
		mu.Unlock()
		return http.StatusOK, []byte(`{"ok":true}`), "application/json", nil
	})
	conn, _ := wsPair(t, svc)

	// the browser sends two requests concurrently (reqId routing must pair them correctly)
	send := func(reqID string, body any) {
		frame := map[string]any{"type": "admin", "method": "POST", "path": "/collections?x=1", "body": body, "token": "tok-" + reqID, "reqId": reqID}
		if err := conn.WriteJSON(frame); err != nil {
			t.Fatalf("send admin: %v", err)
		}
	}
	send("a", map[string]any{"name": "coll-a"})
	send("b", map[string]any{"name": "coll-b"})

	for range []string{"a", "b"} {
		typ, status, body, _ := readAdminResp(t, conn)
		if typ != "admin-resp" || status != http.StatusOK {
			t.Fatalf("want admin-resp 200, got %s %d", typ, status)
		}
		if !strings.Contains(string(body), `"ok":true`) {
			t.Fatalf("body not passed through: %s", body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethod != "POST" || gotPath != "/collections?x=1" {
		t.Fatalf("handler received %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotToken, "tok-") {
		t.Fatalf("token not injected into Authorization: %q", gotToken)
	}
}

// TestAdminJSONError a 4xx error body (including a structured body such as the 409 conflict list)
// is passed through as admin-resp + status -- the frontend then throws err.status/err.data, as the
// fetch version does. Discovery background: api.js request() relies on a 409 returning {conflicts}
// to display the merge conflict list; if admin forwarding collapsed the error into a single msg
// string, the conflict data would be lost.
func TestAdminJSONError(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		return http.StatusConflict, []byte(`{"error":"merge conflict","conflicts":[{"path":"a.txt"}]}`), "application/json", nil
	})
	conn, _ := wsPair(t, svc)

	if err := conn.WriteJSON(map[string]any{"type": "admin", "method": "POST", "path": "/actions/merge", "body": map[string]any{}, "reqId": "m1"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	typ, status, body, _ := readAdminResp(t, conn)
	if typ != "admin-resp" || status != http.StatusConflict {
		t.Fatalf("want admin-resp 409, got %s %d", typ, status)
	}
	if !strings.Contains(string(body), `"conflicts"`) {
		t.Fatalf("conflict list not passed through: %s", body)
	}
}

// TestAdminBinaryUpload the browser sends a binary=true declaration + following binary frames -> the
// server collects them into a multipart request -> the handler receives the full FormFile("file")
// content -> admin-resp.
func TestAdminBinaryUpload(t *testing.T) {
	content := bytes.Repeat([]byte("admin-upload-payload-"), 1024)
	content = append(content, []byte("END")...) // not a multiple of 64KB, to exercise a partial block

	var mu sync.Mutex
	var got []byte
	var gotFilename string
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		file, hdr, err := req.FormFile("file")
		if err != nil {
			return http.StatusBadRequest, []byte(`{"error":"no file"}`), "application/json", nil
		}
		defer file.Close()
		b, _ := io.ReadAll(file)
		mu.Lock()
		got, gotFilename = b, hdr.Filename
		mu.Unlock()
		return http.StatusCreated, []byte(`{"hash":"fake"}`), "application/json", nil
	})
	conn, _ := wsPair(t, svc)

	// declaration frame
	decl := map[string]any{"type": "admin", "method": "POST", "path": "/files/upload", "binary": true, "filename": "hello.txt", "size": len(content), "reqId": "up-1"}
	if err := conn.WriteJSON(decl); err != nil {
		t.Fatalf("send decl: %v", err)
	}
	// send binary in chunks (64KB-aligned slices + a tail block)
	const chunk = 64 * 1024
	for off := 0; off < len(content); off += chunk {
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			t.Fatalf("send bin: %v", err)
		}
	}

	typ, status, _, _ := readAdminResp(t, conn)
	if typ != "admin-resp" || status != http.StatusCreated {
		t.Fatalf("want admin-resp 201, got %s %d", typ, status)
	}
	mu.Lock()
	defer mu.Unlock()
	if !bytes.Equal(got, content) {
		t.Fatalf("uploaded content mismatch: got %d bytes, want %d", len(got), len(content))
	}
	if gotFilename != "hello.txt" {
		t.Fatalf("filename error: %q", gotFilename)
	}
}

// TestAdminBinaryUploadToken when a binary upload declaration frame carries a token, the internal
// forwarding must preserve the Authorization header; otherwise, once auth is enabled (a registration
// server present), every frontend upload would 401. Discovery background: another review on 2026-08 --
// the first version of adminUploadState stored only file metadata and dropped the declaration frame's
// token, so serveAdminUploadComplete built the internal request with an empty token.
func TestAdminBinaryUploadToken(t *testing.T) {
	content := []byte("token-preserving-upload")

	var mu sync.Mutex
	var gotAuth string
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		file, _, err := req.FormFile("file")
		if err != nil {
			return http.StatusBadRequest, []byte(`{"error":"no file"}`), "application/json", nil
		}
		defer file.Close()
		mu.Lock()
		gotAuth = req.Header.Get("Authorization")
		mu.Unlock()
		return http.StatusCreated, []byte(`{"ok":true}`), "application/json", nil
	})
	conn, _ := wsPair(t, svc)

	decl := map[string]any{
		"type": "admin", "method": "POST", "path": "/files/upload",
		"binary": true, "filename": "a.txt", "size": len(content),
		"token": "tok-upload-123", "reqId": "up-token",
	}
	if err := conn.WriteJSON(decl); err != nil {
		t.Fatalf("send decl: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, content); err != nil {
		t.Fatalf("send bin: %v", err)
	}
	typ, status, _, _ := readAdminResp(t, conn)
	if typ != "admin-resp" || status != http.StatusCreated {
		t.Fatalf("want admin-resp 201, got %s %d", typ, status)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer tok-upload-123" {
		t.Fatalf("token not passed through to multipart internal request: %q", gotAuth)
	}
}

// TestAdminBinaryUploadEmpty an empty file upload (size=0) must also complete the multipart forwarding.
// Discovery background: another review on 2026-08 -- the first version of serveAdmin rejected size<=0
// outright; the frontend's ws.upload declares size=0 for an empty file with no following binary frames,
// so the upload would 400. That was asymmetric with the fileIndex upload verb, which already supports
// empty files.
func TestAdminBinaryUploadEmpty(t *testing.T) {
	var mu sync.Mutex
	var got int
	var gotFilename string
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		file, hdr, err := req.FormFile("file")
		if err != nil {
			return http.StatusBadRequest, []byte(`{"error":"no file"}`), "application/json", nil
		}
		defer file.Close()
		b, _ := io.ReadAll(file)
		mu.Lock()
		got, gotFilename = len(b), hdr.Filename
		mu.Unlock()
		return http.StatusCreated, []byte(`{"hash":"empty-hash"}`), "application/json", nil
	})
	conn, _ := wsPair(t, svc)

	decl := map[string]any{
		"type": "admin", "method": "POST", "path": "/files/upload",
		"binary": true, "filename": "empty.txt", "size": 0, "reqId": "up-empty",
	}
	if err := conn.WriteJSON(decl); err != nil {
		t.Fatalf("send decl: %v", err)
	}
	// empty file: no binary frames are sent at all, so the server should complete immediately
	typ, status, _, _ := readAdminResp(t, conn)
	if typ != "admin-resp" || status != http.StatusCreated {
		t.Fatalf("want admin-resp 201, got %s %d", typ, status)
	}
	mu.Lock()
	defer mu.Unlock()
	if got != 0 {
		t.Fatalf("empty file content should be 0 bytes, got %d", got)
	}
	if gotFilename != "empty.txt" {
		t.Fatalf("filename error: %q", gotFilename)
	}
}

// TestAdminBinaryResponse a binary response (such as a collection file stream): an admin-bin header
// plus a single binary frame, which the frontend collects according to size.
func TestAdminBinaryResponse(t *testing.T) {
	payload := bytes.Repeat([]byte{0xAB}, 300*1024)
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		return http.StatusOK, payload, "application/octet-stream", nil
	})
	conn, _ := wsPair(t, svc)

	if err := conn.WriteJSON(map[string]any{"type": "admin", "method": "GET", "path": "/collections/x/y.bin", "reqId": "bin-1"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	typ, status, _, bin := readAdminResp(t, conn)
	if typ != "admin-bin" || status != http.StatusOK {
		t.Fatalf("want admin-bin 200, got %s %d", typ, status)
	}
	if !bytes.Equal(bin, payload) {
		t.Fatalf("binary response mismatch: got %d bytes, want %d", len(bin), len(payload))
	}
}

// TestAdminBinaryUploadReqPath when an upload declaration frame carries a custom path (such as /bt/torrent),
// the forwarding must go to that path and the multipart field must use the declared field.
// Discovery background: the first version of serveAdminUploadComplete hardcoded POST /files/upload, so a
// BT torrent upload (path=/bt/torrent + field=torrent) hit the wrong route; the frontend's btTorrentUpload
// exposed the defect during E2E reconciliation after moving to WS.
func TestAdminBinaryUploadReqPath(t *testing.T) {
	content := []byte("fake-torrent-bytes-0123456789")

	var mu sync.Mutex
	var gotMethod, gotPath, gotField string
	var got []byte
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		file, _, err := req.FormFile("torrent")
		if err != nil {
			return http.StatusBadRequest, []byte(`{"error":"no torrent field"}`), "application/json", nil
		}
		defer file.Close()
		b, _ := io.ReadAll(file)
		mu.Lock()
		gotMethod, gotPath, gotField = req.Method, req.URL.Path, "torrent"
		got = b
		mu.Unlock()
		return http.StatusCreated, []byte(`{"ok":true}`), "application/json", nil
	})
	conn, _ := wsPair(t, svc)

	decl := map[string]any{"type": "admin", "method": "POST", "path": "/bt/torrent", "binary": true, "filename": "x.torrent", "field": "torrent", "size": len(content), "reqId": "up-torrent"}
	if err := conn.WriteJSON(decl); err != nil {
		t.Fatalf("send decl: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, content); err != nil {
		t.Fatalf("send bin: %v", err)
	}

	typ, status, _, _ := readAdminResp(t, conn)
	if typ != "admin-resp" || status != http.StatusCreated {
		t.Fatalf("want admin-resp 201, got %s %d", typ, status)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethod != "POST" || gotPath != "/bt/torrent" || gotField != "torrent" {
		t.Fatalf("forwarding target error: %s %s field=%s", gotMethod, gotPath, gotField)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("torrent content mismatch: %d vs %d bytes", len(got), len(content))
	}
}

// TestAdminTextResponse a text/plain response (such as /ping's "pong") must go through admin-resp as
// a string passthrough, never as binary admin-bin. Discovery background: while E2E smoke-testing /ping,
// the first version of dispatchAdmin classified by whether respBody started with "{", so text/plain was
// misjudged as binary -> the frontend received a Uint8Array instead of a string, and admin-bin occupied
// the binaryExpect slot. Changed to json.Valid + Content-Type checks.
func TestAdminTextResponse(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		return http.StatusOK, []byte("pong"), "text/plain; charset=utf-8", nil
	})
	conn, _ := wsPair(t, svc)

	if err := conn.WriteJSON(map[string]any{"type": "admin", "method": "GET", "path": "/ping", "reqId": "t1"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	typ, status, body, bin := readAdminResp(t, conn)
	if typ != "admin-resp" || status != http.StatusOK {
		t.Fatalf("want admin-resp 200, got %s %d", typ, status)
	}
	if string(body) != `"pong"` {
		t.Fatalf("text body passthrough error: %s", body)
	}
	if len(bin) != 0 {
		t.Fatalf("text response should not go via binary frame: %d bytes", len(bin))
	}
}

// TestAdminRejectedOnNonLocal an admin frame from a non-local session (a simulated WebRTC connection
// ID) must be rejected -- the management plane is not opened to remote nodes (a design constraint
// against privilege-plane holes).
func TestAdminRejectedOnNonLocal(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		t.Error("non-local session should not trigger handler")
		return http.StatusInternalServerError, nil, "", nil
	})
	// build a fake Session directly (ID not "local") and go through the serveAdmin entry point
	sess := &fakeSession{id: "remote-node-123"}
	svc.BindLocal(sess)
	raw, _ := json.Marshal(map[string]any{"type": "admin", "method": "GET", "path": "/files", "reqId": "x"})
	svc.serveAdmin(sess, svc.pending[sess], raw)
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("should return err rejection frame, got %v", types)
	}
}

// TestAdminUploadChunkWriteFail a temporary file write failure (a closed file -> os.ErrClosed) must
// clean up the temp file and handle and reply with an err frame -- this path used to return without
// cleaning up, leaking the file + fd permanently (found in the 2026-08-18 code review). Defensive
// value: no residue when the disk is full or a write error occurs, matching the aborted branch's
// cleanup semantics.
func TestAdminUploadChunkWriteFail(t *testing.T) {
	svc := testAdminSvc(t, nil)
	sess := &fakeSession{id: "local"}

	// build a "closed" temp file: Write must fail (os.ErrClosed)
	f, err := os.CreateTemp("", "peerdrive-admin-upload-fail-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	f.Close() // the key step: after closing, Write fails

	au := &adminUploadState{
		reqID:   "fail-1",
		size:    10, // declared 10 bytes, but only 4 bytes are written below
		f:       f,
		path:    path,
		created: time.Now(),
	}

	// the write-failure path: a non-last block must also be cleaned up (a Write failure returns and cleans up immediately)
	svc.adminUploadChunk(sess, au, []byte("data"), false)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file should be cleaned after write failure, still exists: %s", path)
	}
	// the closed file handle should have been Closed (a second Close errors; here we only need to confirm there is no panic)
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("should return err frame, got %v", types)
	}
}

// TestAdminUploadAbortedCleansTemp an aborted upload (size over the limit / marked aborted on timeout):
// when the final frame (an empty block) arrives, clean up the temp file and reply with an err -- a
// failed upload collection leaves no residue.
func TestAdminUploadAbortedCleansTemp(t *testing.T) {
	svc := testAdminSvc(t, nil)
	sess := &fakeSession{id: "local"}

	f, err := os.CreateTemp("", "peerdrive-admin-upload-abort-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()

	au := &adminUploadState{
		reqID:   "abort-1",
		size:    10,
		got:     4, // only 4 bytes received
		f:       f,
		path:    path,
		aborted: true, // set by the pump after detecting an over-limit / timeout
		created: time.Now(),
	}

	// last=true (delivering an empty block triggers cleanup; see the abort branch in conn.go)
	svc.adminUploadChunk(sess, au, nil, true)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file should be cleaned after aborted, still exists: %s", path)
	}
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("should return err frame, got %v", types)
	}
}

// TestAdminUploadReplacedErrsOld when an old upload slot is replaced by a new declaration, the old
// reqId must receive an err frame and the old temp file must be cleaned up -- replacement used to
// clear the file silently without a frame, so the browser's Promise for the old upload hung forever
// (its pending entry survived until the connection dropped), and late blocks from the old upload were
// mixed into the new au's got count, making the new upload be misjudged as over the size limit and
// aborted with the err pointing at the new reqId (which misleads debugging). Discovery background:
// 2026-08-18 code review (the duplicate-declaration upload path in serveAdmin).
func TestAdminUploadReplacedErrsOld(t *testing.T) {
	// a non-nil handler is required: serveAdmin has an adminHandler==nil check before the binary branch
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		return http.StatusOK, []byte(`{"ok":true}`), "application/json", nil
	})
	sess := &fakeSession{id: "local"}
	svc.BindLocal(sess)
	st := svc.pending[sess]
	if st == nil {
		t.Fatal("should have connState after BindLocal")
	}

	decl := func(reqID string) []byte {
		raw, _ := json.Marshal(map[string]any{
			"type": "admin", "method": "POST", "path": "/files/upload",
			"binary": true, "filename": "a.bin", "size": 10, "reqId": reqID,
		})
		return raw
	}

	// first declaration: occupies the slot (creates the temp file)
	svc.serveAdmin(sess, st, decl("up-old"))
	st.mu.Lock()
	old := st.adminUp
	st.mu.Unlock()
	if old == nil {
		t.Fatal("first declaration should occupy the slot")
	}

	// second declaration: replacement -> the old reqId gets an err + the old temp file is cleaned up, and the new declaration occupies the slot
	svc.serveAdmin(sess, st, decl("up-new"))
	frames := sess.sentFrames()
	if len(frames) != 1 || frames[0].header["type"] != "err" {
		t.Fatalf("should return err frame for old reqId, got %v", sess.sentTypes())
	}
	if reqID, _ := frames[0].header["reqId"].(string); reqID != "up-old" {
		t.Fatalf("err frame should carry old reqId up-old, got %q", reqID)
	}
	if _, err := os.Stat(old.path); !os.IsNotExist(err) {
		t.Fatalf("old temp file should be cleaned, still exists: %s", old.path)
	}
	st.mu.Lock()
	if st.adminUp == nil || st.adminUp.reqID != "up-new" {
		t.Fatalf("new declaration should occupy the slot")
	}
	au := st.adminUp
	st.mu.Unlock()
	// cleanup: remove the new slot's temp file (the test leaves no residue)
	os.Remove(au.path)
	au.f.Close()
}
