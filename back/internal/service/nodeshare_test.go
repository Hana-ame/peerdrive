package service

// Test background (doc/NETDISK.md M2 / ROADMAP phase 5 "file scope management"):
// the sharing scope is this project's only entry point that "publishes the content listing
// outward", and three things must be pinned down:
//  1. off by default (without explicit opt-in it must be completely empty);
//  2. restricted/private collections never appear in the shared listing (a share frame
//     carries no requester identity, so letting them out publishes "accounts-only" content);
//  3. file sharing honors only explicitly declared directory prefixes (guards against
//     "configured a parent dir and ended up sharing the whole disk").

import (
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
)

func sha(hex string) string {
	// fabricate a legal 64hex test value (content doesn't matter, only the format)
	out := ""
	for len(out) < 64 {
		out += hex
	}
	return out[:64]
}

func testShareCfg(enable bool, colls, dirs string) *config.Config {
	cfg := config.Load()
	cfg.ShareEnable = enable
	cfg.ShareCollections = colls
	cfg.ShareDirs = dirs
	return cfg
}

// newShareWith builds a share service wired to fake data sources.
func newShareWith(t *testing.T, cfg *config.Config, colls map[string]*model.AnonCollection, list []model.AnonCollectionSummary, files []model.FileInfo) *NodeShare {
	t.Helper()
	// empty storageDir = in-memory mode (no disk writes): unit tests only care
	// about resolution semantics; persistence is covered separately by the
	// TestNodeSharePersist* series.
	s := NewNodeShare(cfg, "")
	s.SetAnonAccess(
		func(h string) (*model.AnonCollection, error) {
			if c, ok := colls[h]; ok {
				return c, nil
			}
			return nil, errNotFound
		},
		func() ([]model.AnonCollectionSummary, error) { return list, nil },
	)
	s.SetFileLister(func() ([]model.FileInfo, error) { return files, nil })
	return s
}

var errNotFound = &notFoundErr{}

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "not found" }

// TestNodeShareDisabledByDefault off by default: without a config it must be completely empty.
// Discovery background: default full-disk sharing is a privacy incident (the user goal
// "other nodes can see my file links" is bidirectional, so the operator must first decide
// what to share).
func TestNodeShareDisabledByDefault(t *testing.T) {
	pub := sha("a")
	cfg := testShareCfg(false, pub, "/tmp")
	s := newShareWith(t, cfg, map[string]*model.AnonCollection{
		pub: {FriendlyName: "pub", Entries: []model.AnonCollectionEntry{{Path: "a.txt", Providers: []model.Provider{{Type: "sha256", Value: sha("b")}}}}},
	}, nil, []model.FileInfo{{Hash: sha("c"), Name: "c.txt", Path: "/tmp/c.txt"}})

	snap := s.Snapshot()
	if len(snap.Collections) != 0 || len(snap.Files) != 0 || len(snap.Dirs) != 0 {
		t.Fatalf("disabled share must be empty, got %+v", snap)
	}
	if got := s.Summary(); got.Collections != 0 || got.Files != 0 || got.Dirs != 0 {
		t.Fatalf("disabled summary must be zero, got %+v", got)
	}
}

// TestNodeShareExplicitPublicCollection an explicitly declared public collection enters the shared listing, including its entries.
// Discovery background: users want their "packaged collection" to show up in the peer's
// file link list, so entries (path+hash) must be sent along, otherwise the peer only sees
// the collection name and cannot click in.
func TestNodeShareExplicitPublicCollection(t *testing.T) {
	hash := sha("1")
	fileHash := sha("2")
	cfg := testShareCfg(true, hash, "")
	s := newShareWith(t, cfg, map[string]*model.AnonCollection{
		hash: {
			FriendlyName: "My Movies",
			Visibility:   model.VisibilityPublic,
			Tags:         []string{"movie"},
			Entries: []model.AnonCollectionEntry{
				{Path: "a.mp4", Providers: []model.Provider{{Type: "sha256", Value: fileHash, MimeType: "video/mp4"}}},
			},
		},
	}, nil, nil)

	snap := s.Snapshot()
	if len(snap.Collections) != 1 {
		t.Fatalf("collections = %d, want 1", len(snap.Collections))
	}
	c := snap.Collections[0]
	if c.Hash != hash || c.Name != "My Movies" || c.Size != 1 {
		t.Fatalf("collection meta wrong: %+v", c)
	}
	if len(c.Entries) != 1 || c.Entries[0].Path != "a.mp4" || c.Entries[0].Hash != fileHash || c.Entries[0].Mime != "video/mp4" {
		t.Fatalf("entries wrong: %+v", c.Entries)
	}
	if got := s.Summary().Collections; got != 1 {
		t.Fatalf("summary collections = %d, want 1", got)
	}
}

// TestNodeShareSkipsNonPublicCollections restricted/private collections must be skipped.
// Discovery background: a share frame has no requester identity (ROADMAP hard constraint:
// no account dependency before phase 7), so AccessList cannot be verified -- letting them
// out means leaking the "accounts-only" collection listing + file hashes to any connected peer.
func TestNodeShareSkipsNonPublicCollections(t *testing.T) {
	restricted := sha("3")
	private := sha("4")
	cfg := testShareCfg(true, restricted+","+private, "")
	s := newShareWith(t, cfg, map[string]*model.AnonCollection{
		restricted: {FriendlyName: "r", Visibility: model.VisibilityRestricted, AccessList: []string{"alice"}},
		private:    {FriendlyName: "p", Visibility: model.VisibilityPrivate, Owner: "bob"},
	}, nil, nil)

	snap := s.Snapshot()
	if len(snap.Collections) != 0 {
		t.Fatalf("non-public collections must be skipped, got %+v", snap.Collections)
	}
}

// TestNodeShareAllTokenOnlyPublic with "all" configured, only public collections are carried.
// Discovery background: the operator wants "share everything public" and does not want to
// edit the config every time a new public collection appears; but "all" must never drag
// restricted/private ones along.
func TestNodeShareAllTokenOnlyPublic(t *testing.T) {
	pub, res, pri := sha("5"), sha("6"), sha("7")
	// empty visibility is treated as public (consistent with model.EffectiveVisibility)
	legacy := sha("8")
	cfg := testShareCfg(true, "all", "")
	s := newShareWith(t, cfg, map[string]*model.AnonCollection{
		pub:    {FriendlyName: "pub", Visibility: model.VisibilityPublic},
		legacy: {FriendlyName: "legacy"},
		res:    {FriendlyName: "res", Visibility: model.VisibilityRestricted},
		pri:    {FriendlyName: "pri", Visibility: model.VisibilityPrivate},
	}, []model.AnonCollectionSummary{
		{Hash: pub, Visibility: model.VisibilityPublic},
		{Hash: legacy, Visibility: ""},
		{Hash: res, Visibility: model.VisibilityRestricted},
		{Hash: pri, Visibility: model.VisibilityPrivate},
	}, nil)

	snap := s.Snapshot()
	if len(snap.Collections) != 2 {
		t.Fatalf("all-token collections = %d, want 2 (pub+legacy): %+v", len(snap.Collections), snap.Collections)
	}
	for _, c := range snap.Collections {
		if c.Hash != pub && c.Hash != legacy {
			t.Fatalf("unexpected collection in all-token share: %s", c.Hash)
		}
	}
}

// TestNodeShareIgnoresInvalidCollectionHash a hash written wrong in config is ignored.
// Discovery background: writing a name or a short hash leaves a "phantom entry that can
// never be looked up" in the shared listing, and the frontend is guaranteed to fail on
// click; better to warn at startup and ignore it.
func TestNodeShareIgnoresInvalidCollectionHash(t *testing.T) {
	cfg := testShareCfg(true, "not-a-hash,my-collection", "")
	s := newShareWith(t, cfg, nil, nil, nil)
	if got := len(s.Scope().Collections); got != 0 {
		t.Fatalf("invalid hashes must be ignored, got %v", s.Scope().Collections)
	}
	if len(s.Snapshot().Collections) != 0 {
		t.Fatal("snapshot should be empty")
	}
}

// TestNodeShareDirPrefixFilter file sharing filters by directory prefix.
// Discovery background: prefix matching must be decided on path-separator boundaries --
// otherwise /data/share2 would be matched by the prefix of /data/share (a classic
// out-of-bounds share).
func TestNodeShareDirPrefixFilter(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "inside.txt")
	nested := filepath.Join(dir, "sub", "nested.txt")
	outside := filepath.Join(dir+"2", "outside.txt")
	deleted := filepath.Join(dir, "gone.txt")

	cfg := testShareCfg(true, "", dir)
	s := newShareWith(t, cfg, nil, nil, []model.FileInfo{
		{Hash: sha("a"), Name: "inside.txt", Path: inside, Size: 10},
		{Hash: sha("b"), Name: "nested.txt", Path: nested, Size: 20},
		{Hash: sha("c"), Name: "outside.txt", Path: outside, Size: 30},
		{Hash: sha("d"), Name: "gone.txt", Path: deleted, Size: 40, Delete: true},
		{Hash: sha("e"), Name: "nopath.txt", Path: ""},
	})

	snap := s.Snapshot()
	if len(snap.Files) != 2 {
		t.Fatalf("files = %d, want 2 (inside+nested): %+v", len(snap.Files), snap.Files)
	}
	for _, f := range snap.Files {
		// return only the basename: never the local absolute path (minimum outward information principle)
		if f.Path != "inside.txt" && f.Path != "nested.txt" {
			t.Fatalf("unexpected shared file path %q (must be basename)", f.Path)
		}
	}
	if len(snap.Dirs) != 1 || snap.Dirs[0] != dir {
		t.Fatalf("dirs = %v, want [%s]", snap.Dirs, dir)
	}
	if got := s.Summary().Files; got != 2 {
		t.Fatalf("summary files = %d, want 2", got)
	}
}

// TestNodeShareNoDirsMeansNoFiles with no directories configured, no files are shared (not "share everything").
// Discovery background: this is "off by default" on the file dimension -- if an empty dirs
// were read as "no filter", then PEERDRIVE_SHARE_ENABLE=true alone would leak the entire file_index.
func TestNodeShareNoDirsMeansNoFiles(t *testing.T) {
	cfg := testShareCfg(true, "", "")
	s := newShareWith(t, cfg, nil, nil, []model.FileInfo{
		{Hash: sha("a"), Name: "a.txt", Path: "/whatever/a.txt"},
	})
	if got := len(s.Snapshot().Files); got != 0 {
		t.Fatalf("files = %d, want 0 when no dirs configured", got)
	}
}
