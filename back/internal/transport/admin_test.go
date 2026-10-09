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
	content := []byte("upload-preserve-sample-payload")

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

// TestAdminRejectedOnSpoofedLocalID 发现背景：审计 A-9（2026-10-06）。
// 旧实现用 `c.ID() != "local"` 判本机；但 rtcSession.ID() 返回的是信令里对端自报的 id
// （rtc_session.go:17-21 缓存 c.PeerID），信令侧只判空不校验保留名（signalserver.go:367），
// 攻击者注册 ?id=local 即可同时绕过 admin 与 PSK 门禁。
// 修法：serveAdmin 改用 isSelfSession 类型断言——只有 WSSession 实现了 IsLocal()，
// rtcSession 没有实现，所以 IsLocal() 断言失败自动落空。
// 本用例构造一个 id="local" 但 local=false 的 fakeSession（模拟 WebRTC 对端自报 local），
// 断言 admin verb 被拒绝且 handler 未被触发。
func TestAdminRejectedOnSpoofedLocalID(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		t.Error("spoofed local-id session must not trigger admin handler")
		return http.StatusInternalServerError, nil, "", nil
	})
	// id="local" 模拟信令自报，但 local=false 表示非本地 WS 会话（即 WebRTC 对端）
	sess := &fakeSession{id: "local", local: false}
	svc.BindLocal(sess)
	raw, _ := json.Marshal(map[string]any{"type": "admin", "method": "GET", "path": "/files", "reqId": "x"})
	svc.serveAdmin(sess, svc.pending[sess], raw)
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("spoofed local-id session should be rejected, got %v", types)
	}
}

// TestRemoteControl_DisabledByDefaultRejects 发现背景：Issue #234。
// 遥控功能必须默认关闭（RemoteControlEnable=false）。当对端试图发起远程控制 admin 帧时，
// 必须严格拒绝，返回 UNAUTHORIZED 错误帧，防止未授权管理面暴露。
func TestRemoteControl_DisabledByDefaultRejects(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		t.Error("remote admin frame must not trigger handler when remote control is disabled")
		return http.StatusInternalServerError, nil, "", nil
	})
	svc.cfg.RemoteControlEnable = false
	svc.cfg.RemoteControlToken = "secret-token"

	sess := &fakeSession{id: "remote-peer-1"}
	svc.BindLocal(sess)
	raw, _ := json.Marshal(map[string]any{
		"type":   "admin",
		"method": "GET",
		"path":   "/api/status",
		"token":  "secret-token",
		"reqId":  "rc-disabled",
	})
	svc.serveAdmin(sess, svc.pending[sess], raw)

	frames := sess.sentFrames()
	if len(frames) != 1 || frames[0].header["type"] != "err" {
		t.Fatalf("should return err frame, got %v", sess.sentTypes())
	}
	if code, _ := frames[0].header["code"].(string); code != "UNAUTHORIZED" {
		t.Fatalf("want code UNAUTHORIZED, got %q", code)
	}
}

// TestRemoteControl_AuthorizedWithToken 发现背景：Issue #234。
// 当配置 RemoteControlEnable=true 并且提供了正确的 RemoteControlToken 时，
// 远程 WebRTC 对端发送的 admin 帧应被成功鉴权并转发给内部 adminHandler，
// 且请求附带 Authorization: Bearer <token> 请求头。
func TestRemoteControl_AuthorizedWithToken(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		gotAuth = req.Header.Get("Authorization")
		gotMethod = req.Method
		gotPath = req.URL.Path
		return http.StatusOK, []byte(`{"status":"running"}`), "application/json", nil
	})
	svc.cfg.RemoteControlEnable = true
	svc.cfg.RemoteControlToken = "valid-secret-token"

	sess := &fakeSession{id: "authorized-peer"}
	svc.BindLocal(sess)
	raw, _ := json.Marshal(map[string]any{
		"type":   "admin",
		"method": "POST",
		"path":   "/api/command",
		"token":  "valid-secret-token",
		"reqId":  "rc-ok",
	})
	svc.serveAdmin(sess, svc.pending[sess], raw)

	// Wait briefly for goroutine dispatchAdmin to complete
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sess.sentFrames()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	frames := sess.sentFrames()
	if len(frames) != 1 || frames[0].header["type"] != "admin-resp" {
		t.Fatalf("should return admin-resp frame, got %v", sess.sentTypes())
	}
	if status, _ := frames[0].header["status"].(float64); int(status) != http.StatusOK {
		t.Fatalf("want status 200, got %v", status)
	}
	if gotAuth != "Bearer valid-secret-token" {
		t.Fatalf("want Authorization Bearer valid-secret-token, got %q", gotAuth)
	}
	if gotMethod != "POST" || gotPath != "/api/command" {
		t.Fatalf("unexpected forwarded req: %s %s", gotMethod, gotPath)
	}
}

// TestRemoteControl_UnauthorizedTokenRejected 发现背景：Issue #234。
// 当 RemoteControlEnable=true 时，如果对端未提供 token 或提供了错误 token，
// 必须立即返回 UNAUTHORIZED 错误帧，不得转发到 adminHandler。
func TestRemoteControl_UnauthorizedTokenRejected(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		t.Error("adminHandler should not be invoked on wrong token")
		return http.StatusInternalServerError, nil, "", nil
	})
	svc.cfg.RemoteControlEnable = true
	svc.cfg.RemoteControlToken = "correct-token"

	sess := &fakeSession{id: "attacker-peer"}
	svc.BindLocal(sess)

	// Case 1: Wrong token
	rawWrong, _ := json.Marshal(map[string]any{
		"type":   "admin",
		"method": "GET",
		"path":   "/api/status",
		"token":  "wrong-token",
		"reqId":  "rc-wrong",
	})
	svc.serveAdmin(sess, svc.pending[sess], rawWrong)

	frames := sess.sentFrames()
	if len(frames) != 1 || frames[0].header["type"] != "err" {
		t.Fatalf("should return err frame, got %v", sess.sentTypes())
	}
	if code, _ := frames[0].header["code"].(string); code != "UNAUTHORIZED" {
		t.Fatalf("want code UNAUTHORIZED, got %q", code)
	}

	// Case 2: Empty token
	sessEmpty := &fakeSession{id: "empty-peer"}
	svc.BindLocal(sessEmpty)
	rawEmpty, _ := json.Marshal(map[string]any{
		"type":   "admin",
		"method": "GET",
		"path":   "/api/status",
		"reqId":  "rc-empty",
	})
	svc.serveAdmin(sessEmpty, svc.pending[sessEmpty], rawEmpty)

	framesEmpty := sessEmpty.sentFrames()
	if len(framesEmpty) != 1 || framesEmpty[0].header["type"] != "err" {
		t.Fatalf("should return err frame on empty token, got %v", sessEmpty.sentTypes())
	}
	if code, _ := framesEmpty[0].header["code"].(string); code != "UNAUTHORIZED" {
		t.Fatalf("want code UNAUTHORIZED on empty token, got %q", code)
	}
}

// TestRemoteControl_CustomAuthorizer 发现背景：Issue #234。
// 验证 SetRemoteControlAuthorizer 自定义鉴权钩子能够精确覆盖 peerId、token 及路径检查。
func TestRemoteControl_CustomAuthorizer(t *testing.T) {
	var handlerCalled bool
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		handlerCalled = true
		return http.StatusOK, []byte(`{"ok":true}`), "application/json", nil
	})
	svc.cfg.RemoteControlEnable = true

	// Custom authorizer only allows peer "manager-1" on path "/api/status"
	svc.SetRemoteControlAuthorizer(func(peerID, token, method, path string) bool {
		return peerID == "manager-1" && token == "m-token" && path == "/api/status"
	})

	// Allowed call
	sessOk := &fakeSession{id: "manager-1"}
	svc.BindLocal(sessOk)
	rawOk, _ := json.Marshal(map[string]any{
		"type":   "admin",
		"method": "GET",
		"path":   "/api/status",
		"token":  "m-token",
		"reqId":  "rc-custom-ok",
	})
	svc.serveAdmin(sessOk, svc.pending[sessOk], rawOk)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if handlerCalled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !handlerCalled {
		t.Fatal("handler should be called for authorized custom check")
	}

	// Disallowed call (wrong path)
	sessFail := &fakeSession{id: "manager-1"}
	svc.BindLocal(sessFail)
	rawFail, _ := json.Marshal(map[string]any{
		"type":   "admin",
		"method": "POST",
		"path":   "/api/dangerous",
		"token":  "m-token",
		"reqId":  "rc-custom-fail",
	})
	svc.serveAdmin(sessFail, svc.pending[sessFail], rawFail)

	framesFail := sessFail.sentFrames()
	if len(framesFail) != 1 || framesFail[0].header["type"] != "err" {
		t.Fatalf("should return err for disallowed path, got %v", sessFail.sentTypes())
	}
}

// TestAdminUploadChunkWriteFail a temporary file write failure (a closed file -> os.ErrClosed) must
// clean up the temp file and handle and reply with an err frame -- this path used to return without
// cleaning up, leaking the file + fd permanently (found in the 2026-08-18 code review). Defensive
// value: no residue when the disk is full or a write error occurs, matching the aborted branch's
// cleanup semantics.
func TestAdminUploadChunkWriteFail(t *testing.T) {
	svc := testAdminSvc(t, nil)
	sess := &fakeSession{id: "local", local: true}

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
	sess := &fakeSession{id: "local", local: true}

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
	sess := &fakeSession{id: "local", local: true}
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
