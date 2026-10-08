//go:build integration

package integration

// ws_verbs_search_test.go: search verb end-to-end (local WS session, same frame protocol as remote DataChannel).
//
// Discovery context: feature test — the search verb's full chain create → search → list unchanged.
// Why E2E rather than relying only on the transport unit tests: the unit tests call
// serveSearch directly, bypassing dispatchFrame's switch + JSON unmarshal layer. This test
// covers the two most error-prone links — "switch registration" and "field name correspondence" —
// either of which, once broken, surfaces as "the client hangs waiting for a response" rather than
// an explicit error.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/websocket"

	"peerdrive/internal/transport"
)

// TestFrameVerbs_Search create multiple files → search hits → non-matches miss → empty query lists all
// → size interval filter → list unaffected by the new verb.
func TestFrameVerbs_Search(t *testing.T) {
	storage := t.TempDir()
	svc := newService(t, randID("it-vsearch"), storage, false, nil)

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

	// create: two album files + one video (different directories and sizes, to distinguish size filtering later)
	mediaDir := filepath.Join(storage, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	register := func(rel string, content []byte) {
		p := filepath.Join(mediaDir, rel)
		requireWrite(t, p, content)
		resp := c.call(map[string]any{"type": "create", "path": p, "reqId": "c-" + rel})
		if resp["type"] != "created" {
			t.Fatalf("create %s failed: %v", rel, resp)
		}
	}
	register("album01.mp3", bytes.Repeat([]byte("a"), 100))
	register("album02.mp3", bytes.Repeat([]byte("b"), 200))
	register("film.mov", bytes.Repeat([]byte("c"), 1000))

	// search: substring hit
	res := c.call(map[string]any{"type": "search", "q": "album", "reqId": "s1"})
	if res["type"] != "search-resp" {
		t.Fatalf("search failed: %v", res)
	}
	if got := res["total"].(float64); got != 2 {
		t.Fatalf("search album total = %v, want 2: %v", got, res)
	}
	files := res["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("search album files = %d, want 2: %v", len(files), files)
	}
	// File fields complete (client fetches by hash for subsequent downloads)
	f0 := files[0].(map[string]any)
	for _, k := range []string{"hash", "path", "name", "size", "seq"} {
		if _, ok := f0[k]; !ok {
			t.Fatalf("search-resp file missing field %q: %v", k, f0)
		}
	}

	// search: case-insensitive (SQLite LIKE default behavior)
	res = c.call(map[string]any{"type": "search", "q": "ALBUM", "reqId": "s2"})
	if res["total"].(float64) != 2 {
		t.Fatalf("search ALBUM should be case-insensitive: %v", res)
	}

	// search: keyword only in path (directory level)
	res = c.call(map[string]any{"type": "search", "q": "media", "reqId": "s3"})
	if res["total"].(float64) != 3 {
		t.Fatalf("search by directory should hit all 3: %v", res)
	}

	// search: no match → total=0 must be present (not missing) or the client can't tell "no results" from "old node"
	res = c.call(map[string]any{"type": "search", "q": "notexist-zzz", "reqId": "s4"})
	if res["type"] != "search-resp" {
		t.Fatalf("search no-match failed: %v", res)
	}
	if _, ok := res["total"]; !ok {
		t.Fatalf("zero-hit search-resp must carry total:0: %v", res)
	}
	if res["total"].(float64) != 0 {
		t.Fatalf("no-match total = %v, want 0: %v", res["total"], res)
	}
	if nf, ok := res["files"].([]any); !ok || len(nf) != 0 {
		t.Fatalf("no-match files should be empty array: %v", res["files"])
	}

	// search: size interval (lower bound 500 excludes album*.mp3 at 100/200, film.mov at 1000 stays)
	res = c.call(map[string]any{"type": "search", "minSize": 500, "reqId": "s5"})
	if res["total"].(float64) != 1 {
		t.Fatalf("size-filtered search total = %v, want 1: %v", res["total"], res)
	}
	sf := res["files"].([]any)[0].(map[string]any)
	if sf["name"] != "film.mov" {
		t.Fatalf("size-filtered search should hit film.mov: %v", sf)
	}

	// search: % must be treated as a literal (otherwise one click matches the whole table)
	res = c.call(map[string]any{"type": "search", "q": "%", "reqId": "s6"})
	if res["total"].(float64) != 0 {
		t.Fatalf("%% must be a literal, not a wildcard: %v", res)
	}

	// search: pagination limit=1 → this page has 1 entry, but total is still the full hit count
	res = c.call(map[string]any{"type": "search", "q": "album", "size": 1, "reqId": "s7"})
	pg := res["files"].([]any)
	if len(pg) != 1 {
		t.Fatalf("limit=1 should return 1 entry: %v", res)
	}
	if res["total"].(float64) != 2 {
		t.Fatalf("total must be the full hit count, not the page length: %v", res)
	}
	if res["offset"].(float64) != 0 {
		t.Fatalf("offset should be echoed back as 0: %v", res)
	}

	// search: second page offset=1
	res = c.call(map[string]any{"type": "search", "q": "album", "size": 1, "offset": 1, "reqId": "s8"})
	pg2 := res["files"].([]any)
	if len(pg2) != 1 {
		t.Fatalf("second page should return 1 entry: %v", res)
	}
	if pg2[0].(map[string]any)["hash"] == pg[0].(map[string]any)["hash"] {
		t.Fatalf("two pages must not overlap: %v vs %v", pg[0], pg2[0])
	}
	if res["offset"].(float64) != 1 {
		t.Fatalf("offset should be echoed back as 1: %v", res)
	}

	// delete: after removing one album file, search total drops to 1 (deleted lines must not appear in search)
	hash := sf["hash"] // film.mov's hash, for the delete below
	res = c.call(map[string]any{"type": "search", "q": "film", "reqId": "s9"})
	film := res["files"].([]any)[0].(map[string]any)
	del := c.call(map[string]any{"type": "delete", "hash": film["hash"], "reqId": "d1"})
	if del["type"] != "deleted" {
		t.Fatalf("delete failed: %v", del)
	}
	res = c.call(map[string]any{"type": "search", "q": "film", "reqId": "s10"})
	if res["total"].(float64) != 0 {
		t.Fatalf("deleted file must not appear in search: %v", res)
	}

	// list is unaffected (the new verb must not change existing protocol behavior)
	list := c.call(map[string]any{"type": "list", "reqId": "l1"})
	if list["type"] != "list-resp" {
		t.Fatalf("list failed: %v", list)
	}
	if n := len(list["files"].([]any)); n != 2 {
		t.Fatalf("list should have 2 entries after delete (album01/album02): %v", list)
	}
	_ = hash
}
