package transport

// 测试背景（doc/NETDISK.md M2）：share 帧是「纯 WebRTC 客户端」（M5 的
// packages/peerdrive-client）与 Go 节点之间唯一的"你共享了什么"契约。
// JS 侧按字段名逐字解析，因此这里必须把**字段名本身**钉死：
// 一旦改字段名，客户端静默拿不到数据（不报错、列表空）——最坏的一类 bug。
//
// 因此本文件的核心测试是 field-name contract：
//   share-resp 顶层：type / collections / files / dirs / total / reqId
//   collection 项：hash / name / size / tags / entries
//   entry 项：path / hash / mime
//   file 项：hash / name / path / size / mime

import (
	"encoding/json"
	"testing"

	"peerdrive/internal/config"
)

// newShareTestService 造一个不连信令的 PeerJSService（只测帧处理）。
func newShareTestService(t *testing.T) *PeerJSService {
	t.Helper()
	cfg := config.Load()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	return NewPeerJSService(cfg, t.TempDir())
}

// TestServeShareResponseContract 断言 share-resp 的字段名与内容形态。
// 发现背景：JS 客户端（M5）按这些字段名解析；同时 share 帧必须回数组而不是
// null（前端列表渲染不判空），空共享是合法业务状态（不是 err）。
func TestServeShareResponseContract(t *testing.T) {
	svc := newShareTestService(t)
	svc.SetShareProvider(func(peerID string) ShareSnapshot {
		return ShareSnapshot{
			Collections: []ShareCollectionInfo{{
				Hash: "aa", Name: "合集", Size: 1, Tags: []string{"t"},
				Entries: []ShareEntryInfo{{Path: "a.txt", Hash: "bb", Mime: "text/plain"}},
			}},
			Files: []ShareFileInfo{{Hash: "cc", Name: "c.txt", Path: "c.txt", Size: 12, Mime: "text/plain"}},
			Dirs:  []string{"/data"},
		}
	})
	sess := &fakeSession{id: "peer-x"}
	svc.serveShare(sess, dcResp{Type: "share", ReqID: "r1"})

	frames := sess.sentFrames()
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	got := frames[0].header

	for _, key := range []string{"type", "collections", "files", "dirs", "total", "reqId"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("share-resp 缺字段 %q: %v", key, got)
		}
	}
	if got["type"] != "share-resp" {
		t.Fatalf("type = %v, want share-resp", got["type"])
	}
	if got["reqId"] != "r1" {
		t.Fatalf("reqId = %v, want r1（请求-响应配对依赖它）", got["reqId"])
	}
	// total = 合集数 + 文件数（前端卡片直接显示）
	if got["total"].(float64) != 2 {
		t.Fatalf("total = %v, want 2", got["total"])
	}

	colls := got["collections"].([]any)
	if len(colls) != 1 {
		t.Fatalf("collections = %v", colls)
	}
	c0 := colls[0].(map[string]any)
	for _, key := range []string{"hash", "name", "size", "tags", "entries"} {
		if _, ok := c0[key]; !ok {
			t.Fatalf("collection 缺字段 %q: %v", key, c0)
		}
	}
	e0 := c0["entries"].([]any)[0].(map[string]any)
	for _, key := range []string{"path", "hash", "mime"} {
		if _, ok := e0[key]; !ok {
			t.Fatalf("entry 缺字段 %q: %v", key, e0)
		}
	}

	files := got["files"].([]any)
	f0 := files[0].(map[string]any)
	for _, key := range []string{"hash", "name", "path", "size", "mime"} {
		if _, ok := f0[key]; !ok {
			t.Fatalf("file 缺字段 %q: %v", key, f0)
		}
	}
}

// TestServeShareWithoutProviderIsEmptyArrays 未装配共享提供者时回空数组而非 null。
// 发现背景：JSON null 会让前端 `resp.collections.map` 直接抛错白屏；
// 且"对方没共享内容"是常规状态，不该走 err 分支。
func TestServeShareWithoutProviderIsEmptyArrays(t *testing.T) {
	svc := newShareTestService(t)
	sess := &fakeSession{id: "peer-y"}
	svc.serveShare(sess, dcResp{Type: "share"})

	if len(sess.sentFrames()) != 1 {
		t.Fatal("serveShare 应只回一帧")
	}
	raw, _ := json.Marshal(sess.sentFrames()[0].header)
	var parsed struct {
		Type        string            `json:"type"`
		Collections []json.RawMessage `json:"collections"`
		Files       []json.RawMessage `json:"files"`
		Total       int               `json:"total"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Type != "share-resp" || parsed.Collections == nil || parsed.Files == nil {
		t.Fatalf("empty share must be [] not null: %s", raw)
	}
	if parsed.Total != 0 {
		t.Fatalf("total = %d, want 0", parsed.Total)
	}
}

// TestShareLoadInfoCountsOnly 摘要只报数量、不报 hash。
// 发现背景：announce 经发现服务器广播给所有查询者；一旦把合集 hash 放进
// loadInfo 就等于公开"本节点持有什么"（ROADMAP 里明确点名的泄露面）。
func TestShareLoadInfoCountsOnly(t *testing.T) {
	svc := newShareTestService(t)
	svc.SetShareProvider(func(peerID string) ShareSnapshot {
		return ShareSnapshot{
			Collections: []ShareCollectionInfo{{Hash: "secret-hash"}},
			Files:       []ShareFileInfo{{Hash: "f1"}, {Hash: "f2"}},
			Dirs:        []string{"/data"},
		}
	})
	li := svc.shareLoadInfo()
	if li == nil {
		t.Fatal("loadInfo 不应为 nil")
	}
	raw, _ := json.Marshal(li)
	if contains(string(raw), "secret-hash") || contains(string(raw), "f1") {
		t.Fatalf("loadInfo 泄露了具体 hash: %s", raw)
	}
	shares, ok := li["shares"].(map[string]any)
	if !ok {
		t.Fatalf("loadInfo.shares 缺失: %v", li)
	}
	if shares["collections"] != 1 || shares["files"] != 2 || shares["dirs"] != 1 {
		t.Fatalf("shares 计数不符: %v", shares)
	}
}

// TestShareLoadInfoNilWithoutProvider 未启用共享时不上报 loadInfo。
// 发现背景：上报了但全是 0 会让市场卡片显示"共享了 0 个"——不如不报，
// 前端按"未知"处理（未开启共享 vs 共享了空集，是两种不同状态）。
func TestShareLoadInfoNilWithoutProvider(t *testing.T) {
	svc := newShareTestService(t)
	if got := svc.shareLoadInfo(); got != nil {
		t.Fatalf("want nil loadInfo, got %v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
