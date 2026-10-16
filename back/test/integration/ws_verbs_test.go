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

// wsVerbClient Frame protocol client (simulating browser): sends JSON frames + collects binary, pairs by reqId.
// Discovery context: feature test — create/upload/list/info/download/sync verb full chain verification.
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

// call Sends request and waits for response with same reqId (ignoring intermediate binary chunks, returns last text frame).
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
			continue // Binary chunk, skip
		}
		if resp["reqId"] == reqID {
			return resp
		}
	}
	c.t.Fatal("call timeout, reqId=" + reqID)
	return nil
}

// uploadStream Chunked upload: each 64KB is one upload request + one data chunk (protocol aligned),
// waits for ack (resume) or uploaded (overall complete) before sending next chunk.
// Discovery context: After protocol changed to chunked semantics, old streaming helper (one request with multiple data frames)
// is incompatible with server (server only accepts one chunk then acks, remaining frames are discarded).
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
			c.t.Fatalf("upload should start with meta: %v", resp)
		}
		if err := c.conn.WriteJSON(map[string]any{"type": "data", "size": end - off, "reqId": reqID}); err != nil {
			c.t.Fatalf("upload hdr: %v", err)
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			c.t.Fatalf("upload body: %v", err)
		}
		// Wait for ack (chunk written) or uploaded (overall complete)
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
			break // ack → next chunk
		}
	}
	if last == nil {
		c.t.Fatal("upload did not receive completion frame")
	}
	return last
}

// TestFrameVerbs_CreateListInfoDownload create/list/info/download full chain
// (via local WS session, frame protocol identical to remote DataChannel).
//
// Discovery context: feature test — create/list/info/download verb chain
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

	// create: register files within root directory (DownloadDir=storage, H2)
	src := filepath.Join(storage, "ext.bin")
	content := []byte("frame-verbs-create-upload-download")
	requireWrite(t, src, content)

	created := c.call(map[string]any{"type": "create", "path": src, "reqId": "c1"})
	if created["type"] != "created" {
		t.Fatalf("create failed: %v", created)
	}
	hash := created["hash"].(string)

	// list: confirm in list
	list := c.call(map[string]any{"type": "list", "reqId": "c2"})
	if list["type"] != "list-resp" {
		t.Fatalf("list failed: %v", list)
	}
	files := list["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("list should have 1 entry: %v", files)
	}

	// info: get file info
	info := c.call(map[string]any{"type": "info", "hash": hash, "reqId": "c3"})
	if info["type"] != "info-resp" {
		t.Fatalf("info failed: %v", info)
	}
	if info["name"] != "ext.bin" {
		t.Fatalf("info name mismatch: %v", info)
	}

	// download (req): content matches
	got := c.download(hash, content)
	if !bytes.Equal(got, content) {
		t.Fatalf("download content mismatch: got %d bytes", len(got))
	}
}

// TestFrameVerbs_UploadAndSync Streaming upload → download verify → another node sync gets metadata.
//
// Discovery context: feature test — upload + metadata sync chain
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

	content := make([]byte, 150*1024) // Cross multiple chunks
	for i := range content {
		content[i] = byte(i * 11)
	}
	up := c.uploadStream("big.bin", content)
	if up["type"] != "uploaded" {
		t.Fatalf("upload failed: %v", up)
	}
	hash := up["hash"].(string)
	if sha256Hex(content) != hash {
		t.Fatalf("upload hash mismatch: %s", hash)
	}

	// download verify
	if got := c.download(hash, content); !bytes.Equal(got, content) {
		t.Fatalf("download after upload mismatch")
	}

	// metadata sync: another node sync(0) should get this file (including path)
	sync := c.call(map[string]any{"type": "sync", "seq": 0, "reqId": "s1"})
	if sync["type"] != "sync-resp" {
		t.Fatalf("sync failed: %v", sync)
	}
	sfiles := sync["files"].([]any)
	if len(sfiles) != 1 {
		t.Fatalf("sync should have 1 change: %v", sfiles)
	}
	first := sfiles[0].(map[string]any)
	if first["hash"] != hash {
		t.Fatalf("sync hash mismatch: %v", first)
	}
	lastSeq := int64(sync["lastSeq"].(float64))
	if lastSeq <= 0 {
		t.Fatalf("lastSeq should increase: %d", lastSeq)
	}

	// Incremental sync: seq=lastSeq has no new changes
	sync2 := c.call(map[string]any{"type": "sync", "seq": lastSeq, "reqId": "s2"})
	if len(sync2["files"].([]any)) != 0 {
		t.Fatalf("incremental sync should have no changes: %v", sync2)
	}
}

// download Sends req frame and collects data chunks until done.
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
	c.t.Fatal("download timeout")
	return nil
}

func requireWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFrameVerbs_UploadSharded Chunked upload: serial chunks on same connection (offset incrementing),
// server bitmap merges, last chunk triggers uploaded.
// Discovery context: feature requirement — upload supports chunking (foundation for resume/multi-source).
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

	// Chunked upload: offset incrementing, each chunk is one upload request + data chunk
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
			t.Fatalf("chunk %d should start with meta: %v", off, resp)
		}
		if err := c.conn.WriteJSON(map[string]any{"type": "data", "size": end - off, "reqId": reqID}); err != nil {
			t.Fatalf("data hdr: %v", err)
		}
		if err := c.conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
			t.Fatalf("data body: %v", err)
		}
		// Wait for ack or uploaded
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
				// Continue to next chunk
			case "uploaded":
				lastHash = resp["hash"].(string)
			case "err":
				t.Fatalf("chunk %d err: %v", off, resp)
			}
			break
		}
	}
	if lastHash == "" {
		t.Fatal("did not receive uploaded")
	}
	if sha256Hex(content) != lastHash {
		t.Fatalf("hash mismatch: %s", lastHash)
	}
	// download verify
	if got := c.download(lastHash, content); !bytes.Equal(got, content) {
		t.Fatalf("download after chunked upload mismatch")
	}
}

// TestFrameVerbs_UploadResumeOverWS Resume upload: send half → reopen upload (same name) →
// meta.offset returns continuous written → fill from that point → uploaded.
// Discovery context: feature requirement — continue from received position after interruption, no retransmission needed.
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
			t.Fatalf("meta failed: %v", resp)
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
				return 0 // Complete
			}
			break
		}
		return cont
	}

	// Send first chunk, then "disconnect" (directly open new connection to simulate reconnect for resume)
	_ = sendChunk(0)
	c.conn.Close()
	c2 := newWSVerbClient(t, srv.URL)

	// Fill remaining chunks from resume point: first round meta.offset is resume start (≥1 chunk)
	chunk := 64 * 1024
	cont := int64(0)
	for off := 0; off < len(content); off += chunk {
		if off < int(cont) {
			continue
		}
		if off == 0 {
			off = int(cont) // Start from resume point (start from 0 when first round cont is 0)
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
			t.Fatalf("fill-in meta failed: %v", r)
		}
		if cont == 0 {
			cont = int64(r["offset"].(float64))
			if cont < 64*1024 {
				t.Fatalf("resume start should be >= 1 chunk: %d", cont)
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
					t.Fatalf("resume hash mismatch")
				}
				return
			}
			if resp["type"] == "err" {
				t.Fatalf("resume err: %v", resp)
			}
			break
		}
	}
	t.Fatal("resume not completed")
}

// TestFrameVerbs_Delete Verb deletes file index entry: create → list(has) → delete → list(empty).
//
// Discovery context: delete verb previously had no integration test coverage (only ws_verbs_test.go comment mentioned
// five types: create/list/info/download/sync, missing delete). Adding end-to-end verification of index cleanup.
func TestFrameVerbs_Delete(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-vd"), storage, false, nil)

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

	// 1. create: register file
	src := filepath.Join(storage, "to-delete.bin")
	content := []byte("frame-verbs-delete-test")
	requireWrite(t, src, content)
	created := c.call(map[string]any{"type": "create", "path": src, "reqId": "d1"})
	if created["type"] != "created" {
		t.Fatalf("create failed: %v", created)
	}
	hash := created["hash"].(string)

	// 2. list: confirm in list
	list := c.call(map[string]any{"type": "list", "reqId": "d2"})
	files := list["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("after create, list should have 1 entry: %v", files)
	}

	// 3. delete: delete index entry
	del := c.call(map[string]any{"type": "delete", "hash": hash, "reqId": "d3"})
	if del["type"] != "deleted" {
		t.Fatalf("delete failed: %v", del)
	}

	// 4. list: confirm cleared
	list2 := c.call(map[string]any{"type": "list", "reqId": "d4"})
	files2 := list2["files"].([]any)
	if len(files2) != 0 {
		t.Fatalf("after delete, list should be empty, got %d entries: %v", len(files2), files2)
	}

	// 5. info: deleted file info should return err
	info := c.call(map[string]any{"type": "info", "hash": hash, "reqId": "d5"})
	if info["type"] != "err" {
		t.Fatalf("info on deleted file should return err: %v", info)
	}
}
