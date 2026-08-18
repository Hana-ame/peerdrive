package transport

// admin_test.go：admin 管理面 verb 单元测试（不依赖真实网络/gin engine，
// 用 mock AdminHandler 验证帧协议、二进制上传收集、WebRTC 连接拒绝）。
//
// 发现背景：admin verb 是「前端全面迁移到 ws/peerjs」的核心通道——浏览器经
// 本地 WS 会话发 admin 帧管理本节点，内部转发 gin engine 复用全部 HTTP
// controller。测试覆盖三类关键路径：JSON 请求转发、二进制上传收集、非本地
// 会话拒绝（管理面不暴露给 WebRTC，防权限面漏洞）。

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

// testAdminSvc 构造带 mock admin handler 的 service（不上真实信令）。
func testAdminSvc(t *testing.T, h AdminHandler) *PeerJSService {
	t.Helper()
	svc := newTestPeerJSService(t)
	svc.SetAdminHandler(h)
	return svc
}

// wsPair 建立本地 WS 会话（服务端 BindLocal + 客户端 gorilla 连接）。
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
	// 等 BindLocal 注册（bindConn 同步完成，但确保 readLoop 已活跃）
	time.Sleep(100 * time.Millisecond)
	return conn, srv
}

// readAdminResp 读帧直到 type=admin-resp / admin-bin / err。
// admin-bin 时继续读后续二进制帧（头-体连续约束，同 req 拉取），
// 读到 size 字节或后续文本帧为止。返回类型、状态码、body、二进制数据。
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
			// 头已到：继续读二进制块直到 size 字节（测试中单块）
			if resp.Size == 0 {
				return resp.Type, resp.Status, resp.Body, bin
			}
			for int64(len(bin)) < resp.Size {
				mt2, d2, err := conn.ReadMessage()
				if err != nil {
					t.Fatalf("read bin: %v", err)
				}
				if mt2 != websocket.BinaryMessage {
					t.Fatalf("admin-bin 后应跟二进制帧, got %d", mt2)
				}
				bin = append(bin, d2...)
			}
			return resp.Type, resp.Status, resp.Body, bin
		case "admin-resp":
			return resp.Type, resp.Status, resp.Body, bin
		case "err":
			t.Fatalf("服务端 err: %s", resp.Msg)
		default:
			continue
		}
	}
	t.Fatal("等待 admin 响应超时")
	return
}

// TestAdminJSONRequest 本地 WS 会话发 admin JSON 请求 → mock handler 收到
// 完整方法/路径/token → 响应回浏览器（body 原样嵌入）。
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

	// 浏览器并发两个请求（reqId 路由必须正确配对）
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
			t.Fatalf("body 未透传: %s", body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethod != "POST" || gotPath != "/collections?x=1" {
		t.Fatalf("handler 收到 %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotToken, "tok-") {
		t.Fatalf("token 未注入 Authorization: %q", gotToken)
	}
}

// TestAdminJSONError 4xx 错误体（含 409 冲突清单等结构化 body）透传为
// admin-resp + status——前端据此抛 err.status/err.data（与 fetch 版一致）。
// 发现背景：api.js request() 依赖 409 返回 {conflicts} 展示合并冲突清单；
// admin 转发若把错误压成单一 msg 字符串，冲突数据丢失。
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
		t.Fatalf("冲突清单未透传: %s", body)
	}
}

// TestAdminBinaryUpload 浏览器发 binary=true 声明 + 后续二进制帧 → 服务端
// 收集成 multipart → handler 收到 FormFile("file") 完整内容 → admin-resp。
func TestAdminBinaryUpload(t *testing.T) {
	content := bytes.Repeat([]byte("admin-upload-payload-"), 1024)
	content = append(content, []byte("END")...) // 不是 64KB 倍数，测部分块

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

	// 声明帧
	decl := map[string]any{"type": "admin", "method": "POST", "path": "/files/upload", "binary": true, "filename": "hello.txt", "size": len(content), "reqId": "up-1"}
	if err := conn.WriteJSON(decl); err != nil {
		t.Fatalf("send decl: %v", err)
	}
	// 分块发二进制（64KB 对齐分片 + 尾块）
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
		t.Fatalf("上传内容不一致: got %d bytes, want %d", len(got), len(content))
	}
	if gotFilename != "hello.txt" {
		t.Fatalf("filename 错误: %q", gotFilename)
	}
}

// TestAdminBinaryResponse 二进制响应（如集合文件流）：admin-bin 头 + 单个
// 二进制帧，前端按 size 收集。
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
		t.Fatalf("二进制响应不一致: got %d bytes, want %d", len(bin), len(payload))
	}
}

// TestAdminBinaryUploadReqPath 上传声明帧带自定义 path（如 /bt/torrent）时，
// 转发必须打到该路径且 multipart 字段用声明 field。
// 发现背景：serveAdminUploadComplete 初版硬编码 POST /files/upload，BT
// torrent 上传（path=/bt/torrent + field=torrent）会打错路由；前端
// btTorrentUpload 迁移到 WS 后此缺陷被 E2E 对账时发现。
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
		t.Fatalf("转发目标错误: %s %s field=%s", gotMethod, gotPath, gotField)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("torrent 内容不一致: %d vs %d bytes", len(got), len(content))
	}
}

// TestAdminTextResponse text/plain 响应（如 /ping 的 "pong"）必须走 admin-resp
// 字符串透传，不能当二进制 admin-bin。
// 发现背景：E2E 冒烟验证 /ping 时，dispatchAdmin 初版按 respBody 是否以 "{"
// 开头分类，text/plain 被误判为二进制 → 前端收到 Uint8Array 而非字符串，
// 且 admin-bin 占用 binaryExpect 槽。改为 json.Valid + Content-Type 判断。
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
		t.Fatalf("文本 body 透传错误: %s", body)
	}
	if len(bin) != 0 {
		t.Fatalf("文本响应不应走二进制帧: %d bytes", len(bin))
	}
}

// TestAdminRejectedOnNonLocal 非本地会话（模拟 WebRTC 连接 ID）发 admin 帧
// 必须被拒绝——管理面不开放给远端节点（防权限面漏洞的设计约束）。
func TestAdminRejectedOnNonLocal(t *testing.T) {
	svc := testAdminSvc(t, func(req *http.Request) (int, []byte, string, error) {
		t.Error("非本地会话不应触发 handler")
		return http.StatusInternalServerError, nil, "", nil
	})
	// 直接构造 fake Session（ID 非 "local"），走 serveAdmin 入口
	sess := &fakeSession{id: "remote-node-123"}
	svc.BindLocal(sess)
	raw, _ := json.Marshal(map[string]any{"type": "admin", "method": "GET", "path": "/files", "reqId": "x"})
	svc.serveAdmin(sess, svc.pending[sess], raw)
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("应返回 err 拒绝帧, got %v", types)
	}
}

// TestAdminUploadChunkWriteFail 写临时文件失败（已关闭文件 → os.ErrClosed）
// 必须清理临时文件与句柄，并回 err 帧——此前该路径只 return 不清理，
// 文件 + fd 永久泄漏（代码审阅 2026-08-18 发现）。
// 防御意义：磁盘满/写入错误时不留残留，与 aborted 分支清理语义一致。
func TestAdminUploadChunkWriteFail(t *testing.T) {
	svc := testAdminSvc(t, nil)
	sess := &fakeSession{id: "local"}

	// 构造一个「已关闭」的临时文件：Write 必然返回错误（os.ErrClosed）
	f, err := os.CreateTemp("", "peerdrive-admin-upload-fail-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	path := f.Name()
	f.Close() // 关键：关闭后 Write 失败

	au := &adminUploadState{
		reqID:   "fail-1",
		size:    10, // 声明 10 字节，下面只写 4 字节
		f:       f,
		path:    path,
		created: time.Now(),
	}

	// 写失败路径：非 last 块也应清理（Write 失败立即 return 清理）
	svc.adminUploadChunk(sess, au, []byte("data"), false)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("写失败后临时文件应被清理, 仍存在: %s", path)
	}
	// 已关闭的文件句柄应被 Close 过（再次 Close 报错；此处只需验证没 panic）
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("应回 err 帧, got %v", types)
	}
}

// TestAdminUploadAbortedCleansTemp 上传中止（size 超限/超时标记 aborted）：
// 最后一帧（空块）到达时清理临时文件并回 err——上传收集失败不留残留。
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
		got:     4, // 只收到 4 字节
		f:       f,
		path:    path,
		aborted: true, // 泵内判定超限/超时后置位
		created: time.Now(),
	}

	// last=true（空块投递触发清理，见 conn.go abort 分支）
	svc.adminUploadChunk(sess, au, nil, true)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("aborted 后临时文件应被清理, 仍存在: %s", path)
	}
	types := sess.sentTypes()
	if len(types) != 1 || types[0] != "err" {
		t.Fatalf("应回 err 帧, got %v", types)
	}
}
