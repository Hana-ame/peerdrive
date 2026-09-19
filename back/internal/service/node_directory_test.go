package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/transport"
)

// 测试统一背景：节点市场目录（doc/NETDISK.md M1）。
// 关键风险点：① joined 清单必须原子落盘（进程被 kill 不能留下半截 JSON）；
// ② 市场聚合要在"发现服务器不可用"时仍能显示已加入节点（否则用户会以为
// 加过的节点丢了）；③ 自己不能出现在市场列表里。

// newTestDir 建一个临时 storageDir 上的目录服务。
func newTestDir(t *testing.T, discoverURL string) (*NodeDirectory, string) {
	t.Helper()
	base := t.TempDir()
	return NewNodeDirectory(base, discoverURL), base
}

// TestNodeDirectoryJoinPersistsAndReloads 校验加入清单跨实例持久化。
// 发现背景：清单是运营者唯一的"我加入过谁"记录，重启丢失等于功能不可用。
func TestNodeDirectoryJoinPersistsAndReloads(t *testing.T) {
	d, base := newTestDir(t, "")
	d.SetSelfID(func() string { return "self-node" })

	if err := d.Join("peer-a"); err != nil {
		t.Fatalf("join peer-a: %v", err)
	}
	if err := d.Join("peer-b"); err != nil {
		t.Fatalf("join peer-b: %v", err)
	}
	// 重复加入必须幂等（前端可能重复点/重试）
	if err := d.Join("peer-a"); err != nil {
		t.Fatalf("re-join peer-a: %v", err)
	}
	if got := len(d.JoinedPeerIDs()); got != 2 {
		t.Fatalf("joined count = %d, want 2", got)
	}
	// 原子写的临时文件不能残留
	if _, err := os.Stat(filepath.Join(base, joinedFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}

	// 新实例（模拟进程重启）应恢复清单
	d2 := NewNodeDirectory(base, "")
	ids := d2.JoinedPeerIDs()
	if len(ids) != 2 || ids[0] != "peer-a" || ids[1] != "peer-b" {
		t.Fatalf("reloaded joined = %v, want [peer-a peer-b]", ids)
	}
}

// TestNodeDirectoryLeave 校验移出语义。
// 发现背景：移出后必须真的从磁盘消失（否则重启又回来了）；
// 移出未加入的节点要报错（前端才能提示"该节点未加入"）。
func TestNodeDirectoryLeave(t *testing.T) {
	d, base := newTestDir(t, "")
	if err := d.Join("peer-a"); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := d.Leave("peer-a"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if got := len(d.JoinedPeerIDs()); got != 0 {
		t.Fatalf("joined count = %d, want 0", got)
	}
	// 落盘也要清掉
	raw, err := os.ReadFile(filepath.Join(base, joinedFileName))
	if err != nil {
		t.Fatalf("read joined file: %v", err)
	}
	var f joinedFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(f.Peers) != 0 {
		t.Fatalf("persisted peers = %v, want empty", f.Peers)
	}
	if err := d.Leave("peer-a"); err == nil {
		t.Fatal("leave unknown peer should fail")
	}
}

// TestNodeDirectoryJoinValidation 校验 peer id 校验与自我加入拒绝。
// 发现背景：peerId 会落盘并进信令查询串，放任换行/超长会污染清单与日志。
func TestNodeDirectoryJoinValidation(t *testing.T) {
	d, _ := newTestDir(t, "")
	d.SetSelfID(func() string { return "self-node" })

	cases := []struct {
		name string
		peer string
	}{
		{"empty", ""},
		{"self", "self-node"},
		{"newline", "peer\ninjected"},
		{"space", "peer a"},
		{"too long", repeat('x', 200)},
	}
	for _, tc := range cases {
		if err := d.Join(tc.peer); err == nil {
			t.Fatalf("%s: join should fail", tc.name)
		}
	}
	if got := len(d.JoinedPeerIDs()); got != 0 {
		t.Fatalf("joined count = %d, want 0 (no invalid id persisted)", got)
	}
}

func repeat(b byte, n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return string(out)
}

// TestNodeDirectoryMarketMerge 校验市场聚合：在线 ∪ 已加入，且自己不在列表里。
// 发现背景：用户要的界面是"自己的节点 / 别人的节点"，别人节点里可能离线
// （已加入但发现服务器里没有），必须保留条目并标 offline。
func TestNodeDirectoryMarketMerge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/discover/nodes" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nodes": []map[string]any{
				{"peerId": "self-node", "nodeType": "go-persistent"},
				{"peerId": "peer-online", "nodeType": "go-persistent", "lastSeen": 1700000000,
					"loadInfo": map[string]any{"shares": map[string]any{"collections": float64(2), "files": float64(7)}}},
			},
		})
	}))
	defer srv.Close()

	d, _ := newTestDir(t, srv.URL)
	d.SetSelfID(func() string { return "self-node" })
	d.SetConnected(func() map[string]bool { return map[string]bool{"peer-offline": true} })
	if err := d.Join("peer-offline"); err != nil {
		t.Fatalf("join: %v", err)
	}

	nodes := d.Market(context.Background())
	if len(nodes) != 2 {
		t.Fatalf("market size = %d, want 2 (%+v)", len(nodes), nodes)
	}
	// 直连的已加入离线节点排第一（排序规则：直连 > 加入 > 在线）
	if nodes[0].PeerID != "peer-offline" {
		t.Fatalf("first = %s, want peer-offline", nodes[0].PeerID)
	}
	if !nodes[0].Joined || !nodes[0].Connected || nodes[0].Online {
		t.Fatalf("peer-offline flags wrong: %+v", nodes[0])
	}
	if nodes[1].PeerID != "peer-online" || !nodes[1].Online || nodes[1].Joined {
		t.Fatalf("peer-online flags wrong: %+v", nodes[1])
	}
	if nodes[1].Shares.Collections != 2 || nodes[1].Shares.Files != 7 {
		t.Fatalf("peer-online shares = %+v, want 2/7", nodes[1].Shares)
	}
	for _, n := range nodes {
		if n.PeerID == "self-node" {
			t.Fatal("self must not appear in market node list")
		}
	}
}

// TestNodeDirectoryMarketFallsBackToPresenceRoom 校验空 coll 查询拿不到节点时
// 回退到存在房间查询。
// 发现背景：线上信令由外部维护，不能假设它支持"不带 coll 返回全部节点"
// （不支持时返回空列表而非报错，静默失效）。
func TestNodeDirectoryMarketFallsBackToPresenceRoom(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coll := r.URL.Query().Get("coll")
		calls = append(calls, coll)
		nodes := []map[string]any{}
		if coll == transport.PresenceRoom {
			nodes = append(nodes, map[string]any{"peerId": "peer-presence"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"nodes": nodes})
	}))
	defer srv.Close()

	d, _ := newTestDir(t, srv.URL)
	d.SetSelfID(func() string { return "self-node" })
	nodes := d.Market(context.Background())
	if len(nodes) != 1 || nodes[0].PeerID != "peer-presence" {
		t.Fatalf("market = %+v, want peer-presence from presence room", nodes)
	}
	if len(calls) != 2 || calls[0] != "" || calls[1] != transport.PresenceRoom {
		t.Fatalf("discover calls = %v, want [\"\" presenceRoom]", calls)
	}
}

// TestNodeDirectoryMarketWithoutDiscoverURL 校验没有发现服务器时不报错。
// 发现背景：纯本地/离线部署（无 DiscoverURL）仍要能用市场页（只显示已加入）。
func TestNodeDirectoryMarketWithoutDiscoverURL(t *testing.T) {
	d, _ := newTestDir(t, "")
	if err := d.Join("peer-a"); err != nil {
		t.Fatalf("join: %v", err)
	}
	nodes := d.Market(context.Background())
	if len(nodes) != 1 || nodes[0].PeerID != "peer-a" || nodes[0].Online {
		t.Fatalf("market = %+v, want single offline peer-a", nodes)
	}
}

// TestSharesFromLoadInfo 校验共享摘要解析容错。
// 发现背景：loadInfo 来自外部节点，字段可能缺失/类型不同（JSON 数字是 float64）。
func TestSharesFromLoadInfo(t *testing.T) {
	if got := sharesFromLoadInfo(nil); got.Collections != 0 || got.Files != 0 {
		t.Fatalf("nil loadInfo = %+v, want zero", got)
	}
	if got := sharesFromLoadInfo(map[string]any{"shares": "3"}); got.Collections != 0 {
		t.Fatalf("string shares = %+v, want zero", got)
	}
	got := sharesFromLoadInfo(map[string]any{"shares": map[string]any{"collections": 1, "files": 2, "dirs": 3}})
	if got.Collections != 1 || got.Files != 2 || got.Dirs != 3 {
		t.Fatalf("shares = %+v, want 1/2/3", got)
	}
}
