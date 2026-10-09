package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/model"
)

// Unified test background: node market directory (doc/NETDISK.md M1).
// Key risk points: ① the joined list must be written atomically (a killed
// process must not leave half-written JSON); ② market aggregation must still
// show joined nodes when "the discovery server is unavailable" (otherwise
// users think joined nodes are lost); ③ self must not appear in the market
// list.

// newTestDir Creates a directory service on a temporary storageDir.
func newTestDir(t *testing.T, discoverURL string) (*NodeDirectory, string) {
	t.Helper()
	base := t.TempDir()
	return NewNodeDirectory(base, discoverURL), base
}

// TestNodeDirectoryJoinPersistsAndReloads Verifies that the join list persists
// across instances.
// Discovery background: the list is the operator's only record of "who I've
// joined"; losing it on restart equals the feature being unavailable.
func TestNodeDirectoryJoinPersistsAndReloads(t *testing.T) {
	d, base := newTestDir(t, "")
	d.SetSelfID(func() string { return "self-node" })

	if err := d.Join("peer-a"); err != nil {
		t.Fatalf("join peer-a: %v", err)
	}
	if err := d.Join("peer-b"); err != nil {
		t.Fatalf("join peer-b: %v", err)
	}
	// Repeated joins must be idempotent (the frontend may double-click/retry)
	if err := d.Join("peer-a"); err != nil {
		t.Fatalf("re-join peer-a: %v", err)
	}
	if got := len(d.JoinedPeerIDs()); got != 2 {
		t.Fatalf("joined count = %d, want 2", got)
	}
	// The atomic-write temp file must not be left behind
	if _, err := os.Stat(filepath.Join(base, joinedFileName+".tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}

	// A new instance (simulating process restart) should restore the list
	d2 := NewNodeDirectory(base, "")
	ids := d2.JoinedPeerIDs()
	if len(ids) != 2 || ids[0] != "peer-a" || ids[1] != "peer-b" {
		t.Fatalf("reloaded joined = %v, want [peer-a peer-b]", ids)
	}
}

// TestNodeDirectoryLeave Verifies leave semantics.
// Discovery background: after leaving, the entry must really disappear from
// disk (otherwise it comes back on restart); leaving an unjoined node must
// error (so the frontend can show "this node is not joined").
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
	// The persisted file must also be cleared
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

// TestNodeDirectoryJoinValidation Verifies peer ID validation and self-join
// rejection.
// Discovery background: peerId gets persisted and goes into signaling query
// strings; allowing newlines/excessive length would pollute the list and
// logs.
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

// TestNodeDirectoryMarketMerge Verifies market aggregation: online ∪ joined,
// and self is not in the list.
// Discovery background: the UI users want is "my nodes / other people's nodes",
// where other people's nodes may be offline (joined but not in the discovery
// server); the entry must be retained and marked offline.
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
	// Directly connected joined offline node comes first (sorting rule:
	// direct > joined > online)
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

// TestNodeDirectoryMarketFallsBackToPresenceRoom Verifies fallback to the
// presence room query when an empty coll query returns no nodes.
// Discovery background: the online signaling service is externally maintained;
// we can't assume it supports "return all nodes without coll" (when
// unsupported, it returns an empty list rather than an error, failing silently).
func TestNodeDirectoryMarketFallsBackToPresenceRoom(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coll := r.URL.Query().Get("coll")
		calls = append(calls, coll)
		nodes := []map[string]any{}
		if coll == model.PresenceRoom {
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
	if len(calls) != 2 || calls[0] != "" || calls[1] != model.PresenceRoom {
		t.Fatalf("discover calls = %v, want [\"\" presenceRoom]", calls)
	}
}

// TestNodeDirectoryMarketWithoutDiscoverURL Verifies no error when there's no
// discovery server.
// Discovery background: pure local/offline deployments (no DiscoverURL) must
// still be able to use the market page (only showing joined nodes).
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

// TestSharesFromLoadInfo Verifies share summary parsing tolerance.
// Discovery background: loadInfo comes from external nodes, fields may be
// missing/have different types (JSON numbers are float64).
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
