package transport

// Test background (doc/NETDISK.md M2): the share frame is the only "what have you
// shared" contract between a "pure WebRTC client" (M5's packages/peerdrive-client)
// and the Go node. The JS side parses field names verbatim, so here we must nail
// down the **field names themselves**: once a field name changes, the client silently
// gets no data (no error, empty list) -- the worst kind of bug.
//
// So the core test in this file is the field-name contract:
//   share-resp top level: type / collections / files / dirs / total / reqId
//   collection item: hash / name / size / tags / entries
//   entry item: path / hash / mime
//   file item: hash / name / path / size / mime

import (
	"encoding/json"
	"testing"

	"peerdrive/internal/config"

	"github.com/stretchr/testify/assert"
)

// newShareTestService creates a PeerJSService without signaling (only testing frame handling).
func newShareTestService(t *testing.T) *PeerJSService {
	t.Helper()
	cfg := config.Load()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	return NewPeerJSService(cfg, t.TempDir())
}

// TestServeShareResponseContract Asserts the field names and content shape of
// share-resp. Discovery background: the JS client (M5) parses by these field names;
// also the share frame must reply with arrays, not null (the frontend list renderer
// doesn't check for null). An empty share is a valid business state (not err).
func TestServeShareResponseContract(t *testing.T) {
	svc := newShareTestService(t)
	svc.SetShareProvider(func(peerID string) ShareSnapshot {
		return ShareSnapshot{
			Collections: []ShareCollectionInfo{{
				Hash: "aa", Name: "collection", Size: 1, Tags: []string{"t"},
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
			t.Fatalf("share-resp missing field %q: %v", key, got)
		}
	}
	if got["type"] != "share-resp" {
		t.Fatalf("type = %v, want share-resp", got["type"])
	}
	if got["reqId"] != "r1" {
		t.Fatalf("reqId = %v, want r1 (request-response pairing depends on it)", got["reqId"])
	}
	// total = collection count + file count (frontend cards display this directly)
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
			t.Fatalf("collection missing field %q: %v", key, c0)
		}
	}
	e0 := c0["entries"].([]any)[0].(map[string]any)
	for _, key := range []string{"path", "hash", "mime"} {
		if _, ok := e0[key]; !ok {
			t.Fatalf("entry missing field %q: %v", key, e0)
		}
	}

	files := got["files"].([]any)
	f0 := files[0].(map[string]any)
	for _, key := range []string{"hash", "name", "path", "size", "mime"} {
		if _, ok := f0[key]; !ok {
			t.Fatalf("file missing field %q: %v", key, f0)
		}
	}
}

// TestServeShareWithoutProviderIsEmptyArrays When no share provider is wired,
// reply with empty arrays rather than null. Discovery background: JSON null would
// cause the frontend `resp.collections.map` to throw an error and white-screen;
// also "the other side has no shared content" is a normal state and should not go
// through the err branch.
func TestServeShareWithoutProviderIsEmptyArrays(t *testing.T) {
	svc := newShareTestService(t)
	sess := &fakeSession{id: "peer-y"}
	svc.serveShare(sess, dcResp{Type: "share"})

	if len(sess.sentFrames()) != 1 {
		t.Fatal("serveShare should only reply one frame")
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

// TestShareLoadInfoCountsOnly The summary only reports counts, not hashes — and only
// when there is a gate.
//
// Discovery background: announce is broadcast to all queryers via the discovery server;
// putting collection hashes into loadInfo is equivalent to publicly revealing "what this node
// has" (a leak surface explicitly named in ROADMAP).
//
// 2026-10-04 update: a PSK is now also part of the contract. With no PSK the node admits anyone,
// so the counts are withheld entirely (see TestShareLoadInfo_HidesCountsWithoutPSK). This test
// therefore sets a PSK — it keeps pinning the original intent (never leak hashes) plus the
// counts-are-published-when-gated half.
func TestShareLoadInfoCountsOnly(t *testing.T) {
	svc := newShareTestService(t)
	svc.cfg.PeerPSK = "a-real-key"
	svc.SetShareProvider(func(peerID string) ShareSnapshot {
		return ShareSnapshot{
			Collections: []ShareCollectionInfo{{Hash: "test-unlisted-hash"}},
			Files:       []ShareFileInfo{{Hash: "f1"}, {Hash: "f2"}},
			Dirs:        []string{"/data"},
		}
	})
	li := svc.shareLoadInfo()
	if li == nil {
		t.Fatal("loadInfo should not be nil")
	}
	raw, _ := json.Marshal(li)
	if contains(string(raw), "test-unlisted-hash") || contains(string(raw), "f1") {
		t.Fatalf("loadInfo leaked specific hashes: %s", raw)
	}
	shares, ok := li["shares"].(map[string]any)
	if !ok {
		t.Fatalf("loadInfo.shares missing: %v", li)
	}
	if shares["collections"] != 1 || shares["files"] != 2 || shares["dirs"] != 1 {
		t.Fatalf("shares counts mismatch: %v", shares)
	}
}

// TestShareLoadInfoNilWithoutProvider When sharing is not enabled, do not report
// loadInfo. Discovery background: reporting all zeros would make market cards show
// "shared 0 items" -- better not to report at all. The frontend treats it as "unknown"
// (sharing not enabled vs. shared an empty set are two different states).
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

// TestShareLoadInfo_HidesCountsWithoutPSK — 2026-10-04.
//
// Background: shareLoadInfo feeds the announce body, which the discovery server broadcasts
// to every queryer. With no PSK configured the node admits anyone (pskEnabled() is
// literally cfg.PeerPSK != ""), so those counts were a public description of a node with
// no admission control — measured live: GET /status handed out dir/file/collection counts.
//
// The fix hides the counts but keeps the node findable. These cases pin BOTH halves:
// dropping the counts must not accidentally drop the whole payload, and setting a PSK
// must restore them.
func TestShareLoadInfo_HidesCountsWithoutPSK(t *testing.T) {
	counted := func(t *testing.T, info map[string]any) (float64, bool) {
		t.Helper()
		if info == nil {
			return 0, false
		}
		shares, ok := info["shares"].(map[string]any)
		if !ok {
			return 0, false
		}
		v, present := shares["files"]
		if !present {
			return 0, false
		}
		n, ok := v.(int)
		assert.True(t, ok, "files count should be an int, got %T", v)
		return float64(n), true
	}

	t.Run("no PSK hides counts but keeps the payload", func(t *testing.T) {
		svc := newTestPeerJSService(t) // cfg.PeerPSK == ""
		svc.SetShareProvider(func(string) ShareSnapshot {
			return ShareSnapshot{Files: []ShareFileInfo{{Hash: "h1"}, {Hash: "h2"}}}
		})

		info := svc.shareLoadInfo()
		assert.NotNil(t, info, "payload must stay non-nil so the node is still announced")
		assert.Equal(t, "open", info["auth"], "Issue #266: open nodes report auth=open")
		assert.NotNil(t, info["caps"], "Issue #266: caps list present")
		_, hasCount := counted(t, info)
		assert.False(t, hasCount, "no files count may be published without a PSK")

		shares := info["shares"].(map[string]any)
		assert.Equal(t, true, shares["countsHidden"],
			"mark that counts were deliberately hidden, not merely absent")
	})

	t.Run("PSK restores counts", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.PeerPSK = "a-real-key"
		svc.SetShareProvider(func(string) ShareSnapshot {
			return ShareSnapshot{Files: []ShareFileInfo{{Hash: "h1"}, {Hash: "h2"}}}
		})

		info := svc.shareLoadInfo()
		assert.Equal(t, "psk", info["auth"], "Issue #266: psk nodes report auth=psk")
		assert.NotNil(t, info["caps"], "Issue #266: caps list present")
		got, hasCount := counted(t, info)
		assert.True(t, hasCount, "with a PSK the counts should be published again")
		assert.Equal(t, float64(2), got)
	})
}
