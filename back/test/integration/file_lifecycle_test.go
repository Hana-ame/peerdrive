//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Hana-ame/go-peerserver"
	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

// TestFileLifecycleEndToEnd File lifecycle dual-node closed loop (self-hosted signaling + static interconnect):
//
//	① A(WS): create registers local file fileA
//	② B: WebRTC pulls fileA from A (content matches)
//	③ B(WS): upload chunked upload of fileB
//	④ A: WebRTC pulls fileB from B (content matches)
//	⑤ Both sides sync return their respective file_index (A=fileA, B=fileB)
//
// Discovery background: functional acceptance -- create/req/upload/download/sync complete cross-node closed loop.
func TestFileLifecycleEndToEnd(t *testing.T) {
	// Self-hosted signaling
	ss := signalserver.NewServer("testkey")
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/peerjs") {
			ss.HandleWS(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	defer hs.Close()

	idA, idB := randID("fl-a"), randID("fl-b")
	newNode := func(id, storage string, peers []string) *transport.PeerJSService {
		if err := repository.InitDB(":memory:"); err != nil {
			t.Fatal(err)
		}
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(hs.URL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.PeerJSPeers = join(peers)
		// H2: create only allows files within the DownloadDir root; test root = storage
		cfg.DownloadDir = storage
		svc := transport.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	// Each node gets its own local WS session (file verb entry point)
	bindWS := func(svc *transport.PeerJSService) *wsVerbClient {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			svc.BindLocal(transport.NewWSSession("local", conn))
		}))
		t.Cleanup(srv.Close)
		return newWSVerbClient(t, srv.URL)
	}

	storageA := t.TempDir()
	svcA := newNode(idA, storageA, []string{idB})
	svcB := newNode(idB, t.TempDir(), []string{idA})
	wsA := bindWS(svcA)
	wsB := bindWS(svcB)

	// ① A create fileA (H2: create only allows files within the DownloadDir root -> source file goes in storageA)
	fileA := []byte("file-A-e2e-content")
	srcA := filepath.Join(storageA, "file-a.bin")
	requireWrite(t, srcA, fileA)
	created := wsA.call(map[string]any{"type": "create", "path": srcA, "reqId": "a1"})
	if created["type"] != "created" {
		t.Fatalf("A create failed: %v", created)
	}
	hashA := created["hash"].(string)

	// ② B pulls fileA from A (WebRTC)
	waitConnections(t, svcB, map[string]bool{idA: true}, 60*time.Second)
	data, err := svcB.FetchFromPeer(idA, hashA, 0, -1)
	if err != nil {
		t.Fatalf("B pull from A failed: %v", err)
	}
	if !bytes.Equal(data, fileA) {
		t.Fatalf("B pulled content mismatch: got %q", data)
	}

	// ③ B upload fileB (chunked upload)
	fileB := make([]byte, 2*transport.UploadChunkSizeForTest()+7777)
	for i := range fileB {
		fileB[i] = byte(i * 31)
	}
	up := wsB.uploadStream("file-b.bin", fileB)
	if up["type"] != "uploaded" {
		t.Fatalf("B upload failed: %v", up)
	}
	hashB := up["hash"].(string)

	// ④ A pulls fileB from B (WebRTC)
	waitConnections(t, svcA, map[string]bool{idB: true}, 60*time.Second)
	data2, err := svcA.FetchFromPeer(idB, hashB, 0, -1)
	if err != nil {
		t.Fatalf("A pull from B failed: %v", err)
	}
	if !bytes.Equal(data2, fileB) {
		t.Fatalf("A pulled content mismatch: got %d bytes", len(data2))
	}

	// ⑤ Both sides sync should contain their respective files
	// Note: repository.DB is a process-level global singleton; two nodes within the test share an in-memory DB --
	// assert "contains" rather than exact count (in real deployments each node has its own process/DB, no such sharing).
	syncA := wsA.call(map[string]any{"type": "sync", "seq": 0, "reqId": "a2"})
	hasA := false
	for _, f := range syncA["files"].([]any) {
		if f.(map[string]any)["hash"] == hashA {
			hasA = true
		}
	}
	if !hasA {
		t.Fatalf("A sync should contain fileA: %v", syncA)
	}
	syncB := wsB.call(map[string]any{"type": "sync", "seq": 0, "reqId": "b2"})
	hasB := false
	for _, f := range syncB["files"].([]any) {
		if f.(map[string]any)["hash"] == hashB {
			hasB = true
		}
	}
	if !hasB {
		t.Fatalf("B sync should contain fileB: %v", syncB)
	}
}

// TestFileLifecycleWS Single-node full verb closed loop (create/info/download/upload/list/sync/delete):
// Discovery background: functional acceptance -- local file index complete lifecycle.
func TestFileLifecycleWS(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-fl"), storage, false, nil)

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

	// ① create (H2: source file must be within the DownloadDir root = storage)
	src := filepath.Join(storage, "lifecycle.bin")
	contentA := []byte("lifecycle-create-content")
	requireWrite(t, src, contentA)
	created := c.call(map[string]any{"type": "create", "path": src, "reqId": "c1"})
	if created["type"] != "created" {
		t.Fatalf("create failed: %v", created)
	}
	hashA := created["hash"].(string)

	// ② info
	info := c.call(map[string]any{"type": "info", "hash": hashA, "reqId": "c2"})
	if info["type"] != "info-resp" || info["name"] != "lifecycle.bin" {
		t.Fatalf("info failed: %v", info)
	}

	// ③ download
	if got := c.download(hashA, contentA); !bytes.Equal(got, contentA) {
		t.Fatalf("download after create mismatch")
	}

	// ④ upload
	contentB := make([]byte, 3*transport.UploadChunkSizeForTest())
	for i := range contentB {
		contentB[i] = byte(i * 29)
	}
	up := c.uploadStream("up.bin", contentB)
	if up["type"] != "uploaded" {
		t.Fatalf("upload failed: %v", up)
	}
	hashB := up["hash"].(string)
	if sha256Hex(contentB) != hashB {
		t.Fatalf("upload hash mismatch")
	}

	// ⑤ download verification
	if got := c.download(hashB, contentB); !bytes.Equal(got, contentB) {
		t.Fatalf("download after upload mismatch")
	}

	// ⑥ list two entries
	list := c.call(map[string]any{"type": "list", "reqId": "c3"})
	if n := len(list["files"].([]any)); n != 2 {
		t.Fatalf("list should have 2 entries: %d", n)
	}

	// ⑦ sync full two entries + incremental empty
	sync := c.call(map[string]any{"type": "sync", "seq": 0, "reqId": "c4"})
	if n := len(sync["files"].([]any)); n != 2 {
		t.Fatalf("sync should have 2 entries: %d", n)
	}
	lastSeq := int64(sync["lastSeq"].(float64))
	sync2 := c.call(map[string]any{"type": "sync", "seq": lastSeq, "reqId": "c5"})
	if n := len(sync2["files"].([]any)); n != 0 {
		t.Fatalf("incremental sync should have no changes: %v", sync2)
	}

	// ⑧ delete -> incremental sync shows a tombstone
	del := c.call(map[string]any{"type": "delete", "hash": hashA, "reqId": "c6"})
	if del["type"] != "deleted" {
		t.Fatalf("delete failed: %v", del)
	}
	sync3 := c.call(map[string]any{"type": "sync", "seq": lastSeq, "reqId": "c7"})
	s3 := sync3["files"].([]any)
	if len(s3) != 1 {
		t.Fatalf("after delete, sync should have 1 tombstone: %d", len(s3))
	}
	if first := s3[0].(map[string]any); first["delete"] != true {
		t.Fatalf("tombstone marker missing: %v", first)
	}
}
