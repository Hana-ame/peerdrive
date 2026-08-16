//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"peerdrive/internal/transport"
)

// wsVerbClient 帧协议客户端（模拟浏览器）：发 JSON 帧 + 收集二进制，按 reqId 配对。
// 发现背景：功能测试——create/upload/list/info/download/sync verb 全链路验证。
type wsVerbClient struct {
	t    *testing.T
	conn *websocket.Conn
	next int
}

func newWSVerbClient(t *testing.T, srvURL string) *wsVerbClient {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+srvURL[4:]+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return &wsVerbClient{t: t, conn: conn}
}

// call 发送请求并等待同 reqId 的响应（忽略中间二进制块，返回最后文本帧）。
func (c *wsVerbClient) call(req map[string]any) map[string]any {
	c.t.Helper()
	reqID := req["reqId"].(string)
	if err := c.conn.WriteJSON(req); err != nil {
		c.t.Fatalf("send: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.t.Fatalf("read: %v", err)
		}
		if bytes.Equal(data, []byte("PING")) {
			continue
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			continue // 二进制块，跳过
		}
		if resp["reqId"] == reqID {
			return resp
		}
	}
	c.t.Fatal("call 超时，reqId=" + reqID)
	return nil
}

// uploadStream 分片上传：每 64KB 一个 upload 请求 + 一个 data 块（协议对齐），
// 等 ack（续传）或 uploaded（整体完成）后发下一分片。
// 发现背景：协议改为分片语义后，旧流式 helper（一个请求多个 data 帧）与服务端
// 不兼容（服务端只收一个分片就 ack，剩余帧被丢弃）。
func (c *wsVerbClient) uploadStream(name string, content []byte) map[string]any {
	c.t.Helper()
	const chunk = 64 * 1024
	var last map[string]any
	for off := 0; off < len(content); off += chunk {
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		reqID := "up-" + randSuffix()
		resp := c.call(map[string]any{
			"type": "upload", "name": name, "size": len(content),
			"offset": off, "reqId": reqID,
		})
		if resp["type"] != "meta" {
			c.t.Fatalf("upload 应以 meta 开始: %v", resp)
		}
		if err := c.conn.WriteJSON(map[string]any{"type": "data", "size": end - off, "reqId": reqID}); err != nil {
			c.t.Fatalf("upload hdr: %v", err)
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			c.t.Fatalf("upload body: %v", err)
		}
		// 等 ack（分片已写）或 uploaded（整体完成）
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				c.t.Fatalf("upload read: %v", err)
			}
			var r map[string]any
			if err := json.Unmarshal(data, &r); err != nil || r["reqId"] != reqID {
				continue
			}
			last = r
			if r["type"] == "uploaded" {
				return r
			}
			break // ack → 下一分片
		}
	}
	if last == nil {
		c.t.Fatal("upload 未收到完成帧")
	}
	return last
}

// TestFrameVerbs_CreateListInfoDownload create/list/info/download 全链路
// （经本地 WS 会话，帧协议与远端 DataChannel 一致）。
//
// 发现背景：功能测试——create/list/info/download verb 链路
func TestFrameVerbs_CreateListInfoDownload(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-v"), storage, false, nil)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(transport.NewWSSession("local", conn))
	}))
	defer srv.Close()
	c := newWSVerbClient(t, srv.URL)

	// create：登记根目录（DownloadDir=storage，H2）内文件
	src := filepath.Join(storage, "ext.bin")
	content := []byte("frame-verbs-create-upload-download")
	requireWrite(t, src, content)

	created := c.call(map[string]any{"type": "create", "path": src, "reqId": "c1"})
	if created["type"] != "created" {
		t.Fatalf("create 失败: %v", created)
	}
	hash := created["hash"].(string)

	// list：确认在列
	list := c.call(map[string]any{"type": "list", "reqId": "c2"})
	if list["type"] != "list-resp" {
		t.Fatalf("list 失败: %v", list)
	}
	files := list["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("list 应有 1 条: %v", files)
	}

	// info：拿文件信息
	info := c.call(map[string]any{"type": "info", "hash": hash, "reqId": "c3"})
	if info["type"] != "info-resp" {
		t.Fatalf("info 失败: %v", info)
	}
	if info["name"] != "ext.bin" {
		t.Fatalf("info name 不符: %v", info)
	}

	// download（req）：内容一致
	got := c.download(hash, content)
	if !bytes.Equal(got, content) {
		t.Fatalf("下载内容不一致: got %d bytes", len(got))
	}
}

// TestFrameVerbs_UploadAndSync 流式上传 → 下载回验 → 另一节点 sync 拿到 metadata。
//
// 发现背景：功能测试——upload + metadata sync 链路
func TestFrameVerbs_UploadAndSync(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-vu"), storage, false, nil)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(transport.NewWSSession("local", conn))
	}))
	defer srv.Close()
	c := newWSVerbClient(t, srv.URL)

	content := make([]byte, 150*1024) // 跨多块
	for i := range content {
		content[i] = byte(i * 11)
	}
	up := c.uploadStream("big.bin", content)
	if up["type"] != "uploaded" {
		t.Fatalf("upload 失败: %v", up)
	}
	hash := up["hash"].(string)
	if sha256Hex(content) != hash {
		t.Fatalf("upload hash 不符: %s", hash)
	}

	// download 回验
	if got := c.download(hash, content); !bytes.Equal(got, content) {
		t.Fatalf("上传后下载不一致")
	}

	// metadata 同步：另一节点 sync(0) 应拿到该文件（含 path）
	sync := c.call(map[string]any{"type": "sync", "seq": 0, "reqId": "s1"})
	if sync["type"] != "sync-resp" {
		t.Fatalf("sync 失败: %v", sync)
	}
	sfiles := sync["files"].([]any)
	if len(sfiles) != 1 {
		t.Fatalf("sync 应有 1 条变更: %v", sfiles)
	}
	first := sfiles[0].(map[string]any)
	if first["hash"] != hash {
		t.Fatalf("sync hash 不符: %v", first)
	}
	lastSeq := int64(sync["lastSeq"].(float64))
	if lastSeq <= 0 {
		t.Fatalf("lastSeq 应递增: %d", lastSeq)
	}

	// 增量同步：seq=lastSeq 无新变更
	sync2 := c.call(map[string]any{"type": "sync", "seq": lastSeq, "reqId": "s2"})
	if len(sync2["files"].([]any)) != 0 {
		t.Fatalf("增量 sync 应无变更: %v", sync2)
	}
}

// download 发送 req 帧并收集数据块直到 done。
func (c *wsVerbClient) download(hash string, want []byte) []byte {
	c.t.Helper()
	reqID := "dl-" + randSuffix()
	if err := c.conn.WriteJSON(map[string]any{"type": "req", "hash": hash, "offset": 0, "size": -1, "reqId": reqID}); err != nil {
		c.t.Fatalf("download req: %v", err)
	}
	var got []byte
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		mt, data, err := c.conn.ReadMessage()
		if err != nil {
			c.t.Fatalf("download read: %v", err)
		}
		if mt == websocket.BinaryMessage {
			got = append(got, data...)
			continue
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil || resp["reqId"] != reqID {
			continue
		}
		switch resp["type"] {
		case "err":
			c.t.Fatalf("download err: %v", resp)
		case "done":
			return got
		}
	}
	c.t.Fatal("download 超时")
	return nil
}

func requireWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFrameVerbs_UploadSharded 分片上传：同一连接串行分片（offset 递增），
// 服务端位图合并，最后一片触发 uploaded。
// 发现背景：功能需求——upload 支持分片（断点续传/多 source 的基础）。
func TestFrameVerbs_UploadSharded(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-vs"), storage, false, nil)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(transport.NewWSSession("local", conn))
	}))
	defer srv.Close()
	c := newWSVerbClient(t, srv.URL)

	content := make([]byte, 3*transport.UploadChunkSizeForTest())
	for i := range content {
		content[i] = byte(i * 3)
	}

	// 分片上传：offset 递增，每片一个 upload 请求 + data 块
	const chunk = 64 * 1024
	var lastHash string
	for off := 0; off < len(content); off += chunk {
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		reqID := "sh-" + randSuffix()
		resp := c.call(map[string]any{
			"type": "upload", "name": "sharded.bin",
			"size": len(content), "offset": off, "reqId": reqID,
		})
		if resp["type"] != "meta" {
			t.Fatalf("分片 %d 应以 meta 开始: %v", off, resp)
		}
		if err := c.conn.WriteJSON(map[string]any{"type": "data", "size": end - off, "reqId": reqID}); err != nil {
			t.Fatalf("data hdr: %v", err)
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			t.Fatalf("data body: %v", err)
		}
		// 等 ack 或 uploaded
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var resp map[string]any
			if err := json.Unmarshal(data, &resp); err != nil || resp["reqId"] != reqID {
				continue
			}
			switch resp["type"] {
			case "ack":
				// 继续下一分片
			case "uploaded":
				lastHash = resp["hash"].(string)
			case "err":
				t.Fatalf("分片 %d err: %v", off, resp)
			}
			break
		}
	}
	if lastHash == "" {
		t.Fatal("未收到 uploaded")
	}
	if sha256Hex(content) != lastHash {
		t.Fatalf("hash 不符: %s", lastHash)
	}
	// 下载回验
	if got := c.download(lastHash, content); !bytes.Equal(got, content) {
		t.Fatalf("分片上传后下载不一致")
	}
}

// TestFrameVerbs_UploadResumeOverWS 断点续传：传一半 → 重开上传（同 name）→
// meta.offset 返回连续已写 → 从该点补齐 → uploaded。
// 发现背景：功能需求——中断后从已接收位置继续，无需重传。
func TestFrameVerbs_UploadResumeOverWS(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-vr"), storage, false, nil)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(transport.NewWSSession("local", conn))
	}))
	defer srv.Close()
	c := newWSVerbClient(t, srv.URL)

	content := make([]byte, 4*transport.UploadChunkSizeForTest())
	for i := range content {
		content[i] = byte(i * 9)
	}

	sendChunk := func(off int) int64 {
		reqID := "rs-" + randSuffix()
		resp := c.call(map[string]any{
			"type": "upload", "name": "resume.bin",
			"size": len(content), "offset": off, "reqId": reqID,
		})
		if resp["type"] != "meta" {
			t.Fatalf("meta 失败: %v", resp)
		}
		cont := int64(resp["offset"].(float64))
		chunk := 64 * 1024
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		if err := c.conn.WriteJSON(map[string]any{"type": "data", "size": end - off, "reqId": reqID}); err != nil {
			t.Fatalf("data: %v", err)
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			t.Fatalf("body: %v", err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			_, data, err := c.conn.ReadMessage()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var resp map[string]any
			if err := json.Unmarshal(data, &resp); err != nil || resp["reqId"] != reqID {
				continue
			}
			if resp["type"] == "uploaded" {
				return 0 // 完成
			}
			break
		}
		return cont
	}

	// 传第一片，然后"断线"（直接开新连接模拟重连后续传）
	_ = sendChunk(0)
	c.conn.Close()
	c2 := newWSVerbClient(t, srv.URL)

	// 从续传点补齐剩余分片：第一轮 meta.offset 即续传起点（≥1 chunk）
	chunk := 64 * 1024
	cont := int64(0)
	for off := 0; off < len(content); off += chunk {
		if off < int(cont) {
			continue
		}
		if off == 0 {
			off = int(cont) // 从续传起点开始（首轮 cont 为 0 时从 0 开始）
		}
		end := off + chunk
		if end > len(content) {
			end = len(content)
		}
		rq := "rs-fill-" + randSuffix()
		r := c2.call(map[string]any{
			"type": "upload", "name": "resume.bin",
			"size": len(content), "offset": off, "reqId": rq,
		})
		if r["type"] != "meta" {
			t.Fatalf("补齐 meta 失败: %v", r)
		}
		if cont == 0 {
			cont = int64(r["offset"].(float64))
			if cont < 64*1024 {
				t.Fatalf("续传起点应 ≥ 1 chunk: %d", cont)
			}
		}
		if err := c2.conn.WriteJSON(map[string]any{"type": "data", "size": end - off, "reqId": rq}); err != nil {
			t.Fatalf("data: %v", err)
		}
		if err := c2.conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			t.Fatalf("body: %v", err)
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			_, data, err := c2.conn.ReadMessage()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var resp map[string]any
			if err := json.Unmarshal(data, &resp); err != nil || resp["reqId"] != rq {
				continue
			}
			if resp["type"] == "uploaded" {
				if sha256Hex(content) != resp["hash"].(string) {
					t.Fatalf("续传 hash 不符")
				}
				return
			}
			if resp["type"] == "err" {
				t.Fatalf("续传 err: %v", resp)
			}
			break
		}
	}
	t.Fatal("续传未完成")
}
