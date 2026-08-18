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

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
	"github.com/Hana-ame/go-peerserver"
)

// TestFileLifecycleEndToEnd 文件生命周期双节点闭环（自托管信令 + 静态互联）：
//
//	① A(WS)：create 登记本地文件 fileA
//	② B：WebRTC 从 A 拉 fileA（内容一致）
//	③ B(WS)：upload 分片上传 fileB
//	④ A：WebRTC 从 B 拉 fileB（内容一致）
//	⑤ 两侧 sync 分别返回各自 file_index（A=fileA，B=fileB）
//
// 发现背景：功能验收——create/req/upload/download/sync 跨节点完整闭环。
func TestFileLifecycleEndToEnd(t *testing.T) {
	// 自托管信令
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
		// H2：create 只允许 DownloadDir 根内文件；测试根 = storage
		cfg.DownloadDir = storage
		svc := transport.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	// 每个节点挂自己的本地 WS 会话（文件 verb 入口）
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

	// ① A create fileA（H2：create 只允许 DownloadDir 根内文件 → 源文件放 storageA）
	fileA := []byte("file-A-e2e-content")
	srcA := filepath.Join(storageA, "file-a.bin")
	requireWrite(t, srcA, fileA)
	created := wsA.call(map[string]any{"type": "create", "path": srcA, "reqId": "a1"})
	if created["type"] != "created" {
		t.Fatalf("A create 失败: %v", created)
	}
	hashA := created["hash"].(string)

	// ② B 从 A 拉 fileA（WebRTC）
	waitConnections(t, svcB, map[string]bool{idA: true}, 60*time.Second)
	data, err := svcB.FetchFromPeer(idA, hashA, 0, -1)
	if err != nil {
		t.Fatalf("B 从 A 拉取失败: %v", err)
	}
	if !bytes.Equal(data, fileA) {
		t.Fatalf("B 拉取内容不一致: got %q", data)
	}

	// ③ B upload fileB（分片上传）
	fileB := make([]byte, 2*transport.UploadChunkSizeForTest()+7777)
	for i := range fileB {
		fileB[i] = byte(i * 31)
	}
	up := wsB.uploadStream("file-b.bin", fileB)
	if up["type"] != "uploaded" {
		t.Fatalf("B upload 失败: %v", up)
	}
	hashB := up["hash"].(string)

	// ④ A 从 B 拉 fileB（WebRTC）
	waitConnections(t, svcA, map[string]bool{idB: true}, 60*time.Second)
	data2, err := svcA.FetchFromPeer(idB, hashB, 0, -1)
	if err != nil {
		t.Fatalf("A 从 B 拉取失败: %v", err)
	}
	if !bytes.Equal(data2, fileB) {
		t.Fatalf("A 拉取内容不一致: got %d bytes", len(data2))
	}

	// ⑤ 两侧 sync 应包含各自产生的文件
	// 注意：repository.DB 是进程级全局单例，测试内两节点共享内存 DB——
	// 断言"包含"而非精确条数（真实部署每节点独立进程/DB，无此共享）。
	syncA := wsA.call(map[string]any{"type": "sync", "seq": 0, "reqId": "a2"})
	hasA := false
	for _, f := range syncA["files"].([]any) {
		if f.(map[string]any)["hash"] == hashA {
			hasA = true
		}
	}
	if !hasA {
		t.Fatalf("A sync 应包含 fileA: %v", syncA)
	}
	syncB := wsB.call(map[string]any{"type": "sync", "seq": 0, "reqId": "b2"})
	hasB := false
	for _, f := range syncB["files"].([]any) {
		if f.(map[string]any)["hash"] == hashB {
			hasB = true
		}
	}
	if !hasB {
		t.Fatalf("B sync 应包含 fileB: %v", syncB)
	}
}

// TestFileLifecycleWS 单节点全 verb 闭环（create/info/download/upload/list/sync/delete）：
// 发现背景：功能验收——本地文件索引完整生命周期。
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

	// ① create（H2：源文件必须在 DownloadDir 根内 = storage）
	src := filepath.Join(storage, "lifecycle.bin")
	contentA := []byte("lifecycle-create-content")
	requireWrite(t, src, contentA)
	created := c.call(map[string]any{"type": "create", "path": src, "reqId": "c1"})
	if created["type"] != "created" {
		t.Fatalf("create 失败: %v", created)
	}
	hashA := created["hash"].(string)

	// ② info
	info := c.call(map[string]any{"type": "info", "hash": hashA, "reqId": "c2"})
	if info["type"] != "info-resp" || info["name"] != "lifecycle.bin" {
		t.Fatalf("info 失败: %v", info)
	}

	// ③ download
	if got := c.download(hashA, contentA); !bytes.Equal(got, contentA) {
		t.Fatalf("create 后下载不一致")
	}

	// ④ upload
	contentB := make([]byte, 3*transport.UploadChunkSizeForTest())
	for i := range contentB {
		contentB[i] = byte(i * 29)
	}
	up := c.uploadStream("up.bin", contentB)
	if up["type"] != "uploaded" {
		t.Fatalf("upload 失败: %v", up)
	}
	hashB := up["hash"].(string)
	if sha256Hex(contentB) != hashB {
		t.Fatalf("upload hash 不符")
	}

	// ⑤ download 回验
	if got := c.download(hashB, contentB); !bytes.Equal(got, contentB) {
		t.Fatalf("upload 后下载不一致")
	}

	// ⑥ list 两条
	list := c.call(map[string]any{"type": "list", "reqId": "c3"})
	if n := len(list["files"].([]any)); n != 2 {
		t.Fatalf("list 应有 2 条: %d", n)
	}

	// ⑦ sync 全量两条 + 增量空
	sync := c.call(map[string]any{"type": "sync", "seq": 0, "reqId": "c4"})
	if n := len(sync["files"].([]any)); n != 2 {
		t.Fatalf("sync 应有 2 条: %d", n)
	}
	lastSeq := int64(sync["lastSeq"].(float64))
	sync2 := c.call(map[string]any{"type": "sync", "seq": lastSeq, "reqId": "c5"})
	if n := len(sync2["files"].([]any)); n != 0 {
		t.Fatalf("增量 sync 应无变更: %v", sync2)
	}

	// ⑧ delete → 增量 sync 出现 tombstone
	del := c.call(map[string]any{"type": "delete", "hash": hashA, "reqId": "c6"})
	if del["type"] != "deleted" {
		t.Fatalf("delete 失败: %v", del)
	}
	sync3 := c.call(map[string]any{"type": "sync", "seq": lastSeq, "reqId": "c7"})
	s3 := sync3["files"].([]any)
	if len(s3) != 1 {
		t.Fatalf("delete 后 sync 应有 1 条 tombstone: %d", len(s3))
	}
	if first := s3[0].(map[string]any); first["delete"] != true {
		t.Fatalf("tombstone 标记缺失: %v", first)
	}
}
