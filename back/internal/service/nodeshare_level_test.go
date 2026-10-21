package service

// nodeshare_level_test.go: three-tier sharing levels (doc/NETDISK.md §12.6).
//
// One-sentence semantics: **public = listed and given; unlisted = not listed but
// given; private = only to known people**. The part easy to get wrong is not
// "can it be stored", but the boundaries between the three tiers:
//   · unlisted must be **not listed but still downloadable** (dropping the second
//     half and link sharing is broken)
//   · private must **block strangers** (drop it and private is no different from unlisted)
//   · when the same content is hit by both a directory rule and a single-file
//     rule, take the **loosest** (taking the strictest silently voids "I
//     deliberately loosened this one")
//   · a misspelled level ("pubilc") must reject the whole batch; never silently
//     fall back to public (that publishes content meant to be restricted)

import (
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/model"
)

// TestNodeSharePublicListedAndDownloadable public: listed, anyone can fetch.
func TestNodeSharePublicListedAndDownloadable(t *testing.T) {
	base := t.TempDir()
	h := sha("a1")
	s := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1},
	})
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelPublic); err != nil {
		t.Fatalf("share: %v", err)
	}
	snap := s.SnapshotFor("stranger-peer")
	if len(snap.Files) != 1 {
		t.Fatalf("public file must be listed, got %d", len(snap.Files))
	}
	if !s.AllowsDownload("stranger-peer", h, false) {
		t.Fatal("public file must be downloadable by anyone")
	}
}

// TestNodeShareUnlistedHiddenButDownloadable unlisted: not listed, but fetchable by hash.
//
// This one is easiest to write as "not listed = not given" -- then unlisted
// degrades into "not shared", and link sharing, the one thing it exists to
// support, is gone.
func TestNodeShareUnlistedHiddenButDownloadable(t *testing.T) {
	base := t.TempDir()
	h := sha("b1")
	s := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1},
	})
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelUnlisted); err != nil {
		t.Fatalf("share: %v", err)
	}
	if got := len(s.SnapshotFor("stranger-peer").Files); got != 0 {
		t.Fatalf("unlisted must NOT be listed, got %d", got)
	}
	if !s.AllowsDownload("stranger-peer", h, false) {
		t.Fatal("unlisted must still be downloadable by hash")
	}
	// The summary (count reported by announce) must not count it as "shared N externally"
	if got := s.Summary().Files; got != 0 {
		t.Fatalf("unlisted must not be counted in summary, got %d", got)
	}
}

// TestNodeSharePrivateOnlyFriendsAndSelf private: friends and self only.
func TestNodeSharePrivateOnlyFriendsAndSelf(t *testing.T) {
	base := t.TempDir()
	h := sha("c1")
	s := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1},
	})
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelPrivate); err != nil {
		t.Fatalf("share: %v", err)
	}
	if s.AllowsDownload("stranger-peer", h, false) {
		t.Fatal("private must be denied to strangers")
	}
	if !s.AllowsDownload("stranger-peer", h, true) {
		t.Fatal("private must be allowed to self (local channel)")
	}
	// empty friends list => nobody is a friend
	if got := len(s.SnapshotFor("stranger-peer").Files); got != 0 {
		t.Fatalf("private must not be listed to strangers, got %d", got)
	}
	if _, err := s.Update(ScopePatch{Friends: &[]string{"Friend-Node"}}); err != nil {
		t.Fatalf("set friends: %v", err)
	}
	if !s.AllowsDownload("friend-node", h, false) {
		t.Fatal("friend must be allowed (id match is case-insensitive)")
	}
	// friends must see the private listing: granting permission without a directory listing grants nothing
	if got := len(s.SnapshotFor("Friend-Node").Files); got != 1 {
		t.Fatalf("private must be listed to friends, got %d", got)
	}
}

// TestNodeShareLoosestLevelWins dir unlisted + single file public => that file is public.
func TestNodeShareLoosestLevelWins(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "media")
	h := sha("d1")
	s := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(dir, "a.txt"), Size: 1},
	})
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: dir, Level: model.LevelUnlisted}}}); err != nil {
		t.Fatalf("dirs: %v", err)
	}
	if got := len(s.Snapshot().Files); got != 0 {
		t.Fatalf("dir unlisted ⇒ not listed, got %d", got)
	}
	// loosen just this file to public
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelPublic); err != nil {
		t.Fatalf("share: %v", err)
	}
	snap := s.Snapshot()
	if len(snap.Files) != 1 {
		t.Fatalf("loosest must win (public), listed=%d", len(snap.Files))
	}
	// the reverse: dir public + file private, still public (the dir is looser)
	s2 := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(dir, "a.txt"), Size: 1},
	})
	if _, err := s2.Update(ScopePatch{Dirs: &[]ShareItem{{ID: dir, Level: model.LevelPublic}}}); err != nil {
		t.Fatalf("dirs: %v", err)
	}
	if _, err := s2.SetFilesShared([]string{h}, true, model.LevelPrivate); err != nil {
		t.Fatalf("share: %v", err)
	}
	if got := len(s2.Snapshot().Files); got != 1 {
		t.Fatalf("dir public must stay public, listed=%d", got)
	}
	if !s2.AllowsDownload("stranger", h, false) {
		t.Fatal("dir public ⇒ downloadable even if the single pick says private")
	}
}

// TestNodeShareInvalidLevelRejected a misspelled level rejects the whole batch (no public fallback).
func TestNodeShareInvalidLevelRejected(t *testing.T) {
	base := t.TempDir()
	h := sha("e1")
	s := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1},
	})
	if _, err := s.SetFilesShared([]string{h}, true, "pubilc"); err == nil {
		t.Fatal("typo level must be rejected")
	}
	if got := len(s.Scope().Files); got != 0 {
		t.Fatalf("rejected update must not modify scope, files=%v", got)
	}
	dir := filepath.Join(base, "d")
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: dir, Level: "secret"}}}); err == nil {
		t.Fatal("invalid dir level must be rejected")
	}
	// an invalid friend ID (containing whitespace) is rejected the same way
	if _, err := s.Update(ScopePatch{Friends: &[]string{"node id"}}); err == nil {
		t.Fatal("friend id with whitespace must be rejected")
	}
}

// TestNodeShareSetLevelOverrides an explicit level change must be able to tighten (public → private).
//
// Pitfall: the first draft merged by "take the loosest", so a file already made
// public could never be tightened to private -- the UI showed "I selected private,
// but after a refresh it's public again". Merging happens only at resolution time
// (dir ∪ single file); the user's dropdown selection is an **explicit override**.
func TestNodeShareSetLevelOverrides(t *testing.T) {
	base := t.TempDir()
	h := sha("a3")
	s := newScopeShare(t, base, true, []model.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1},
	})
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelPublic); err != nil {
		t.Fatalf("share: %v", err)
	}
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelPrivate); err != nil {
		t.Fatalf("tighten: %v", err)
	}
	if got := s.LevelOf(h); got != model.LevelPrivate {
		t.Fatalf("level = %q, want private", got)
	}
	if s.AllowsDownload("stranger", h, false) {
		t.Fatal("tightened level must actually deny strangers")
	}
}

// TestNodeShareLevelPersistedAcrossRestart the level persists to disk together with the scope.
func TestNodeShareLevelPersistedAcrossRestart(t *testing.T) {
	base := t.TempDir()
	h := sha("a2")
	files := []model.FileInfo{{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1}}
	s1 := newScopeShare(t, base, true, files)
	if _, err := s1.Update(ScopePatch{Friends: &[]string{"buddy"}}); err != nil {
		t.Fatalf("friends: %v", err)
	}
	if _, err := s1.SetFilesShared([]string{h}, true, model.LevelPrivate); err != nil {
		t.Fatalf("share: %v", err)
	}
	s2 := newScopeShare(t, base, true, files)
	if got := s2.LevelOf(h); got != model.LevelPrivate {
		t.Fatalf("level after restart = %q, want private", got)
	}
	if s2.AllowsDownload("stranger", h, false) {
		t.Fatal("private must survive restart")
	}
	if !s2.AllowsDownload("buddy", h, false) {
		t.Fatal("friends must survive restart")
	}
}

// TestNodeShareLegacyStringItemsStillPublic legacy persisted state (string arrays) reads as public.
//
// share_scope.json written before the upgrade carries only ids. If it cannot be
// parsed and we fall back to the initial value, the scope the operator picked is
// silently dropped -- and dropped quietly (after a restart the listing is empty,
// so he thinks the files are gone).
func TestNodeShareLegacyStringItemsStillPublic(t *testing.T) {
	base := t.TempDir()
	h := sha("b2")
	files := []model.FileInfo{{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1}}
	writeScopeFile(t, base, `{"enable":true,"dirs":[],"files":["`+h+`"],"collections":[]}`)
	s := newScopeShare(t, base, false, files)
	if got := len(s.Scope().Files); got != 1 {
		t.Fatalf("legacy string items must load, got %v", s.Scope().Files)
	}
	if got := s.LevelOf(h); got != model.LevelPublic {
		t.Fatalf("legacy item level = %q, want public", got)
	}
	if got := len(s.Snapshot().Files); got != 1 {
		t.Fatalf("legacy item must stay listed, got %d", got)
	}
}

// writeScopeFile writes persisted state directly (simulating a legacy version / a hand-edited share_scope.json).
func writeScopeFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, shareScopeFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestNodeShareCollectionVisibilityDowngradesToPrivate a collection that is itself not public ⇒ downgrade.
//
// The AccessList of a restricted/private collection is a list of accounts, which
// cannot be verified without an identity; serving it as public is the same as a
// share frame bypassing the account gate. Downgrading it to private keeps it at
// least friends-only.
func TestNodeShareCollectionVisibilityDowngradesToPrivate(t *testing.T) {
	base := t.TempDir()
	h := sha("c1")
	colls := map[string]*model.AnonCollection{
		sha("cc"): {FriendlyName: "restricted", Visibility: model.VisibilityRestricted,
			Entries: []model.AnonCollectionEntry{{Path: "a.txt", Hash: h}}},
	}
	for k := range colls {
		for i := range colls[k].Entries {
			colls[k].Entries[i].Normalize()
		}
	}
	s := newScopeShare(t, base, true, nil)
	s.SetAnonAccess(func(hash string) (*model.AnonCollection, error) { return colls[hash], nil },
		func() ([]model.AnonCollectionSummary, error) { return nil, nil })
	cid := sha("cc")
	if _, err := s.Update(ScopePatch{Collections: &[]ShareItem{{ID: cid, Level: model.LevelPublic}}}); err != nil {
		t.Fatalf("collections: %v", err)
	}
	if got := len(s.SnapshotFor("stranger").Files); got != 0 {
		t.Fatalf("restricted collection must not be listed publicly, got %d", got)
	}
	if s.AllowsDownload("stranger", h, false) {
		t.Fatal("restricted collection entry must be denied to strangers")
	}
	if _, err := s.Update(ScopePatch{Friends: &[]string{"buddy"}}); err != nil {
		t.Fatalf("friends: %v", err)
	}
	if !s.AllowsDownload("buddy", h, false) {
		t.Fatal("restricted collection entry must be allowed to friends (private fallback)")
	}
}

// TestNodeShareCollectionManifestFollowsLevel the collection manifest itself is bound by the level too.
//
// Background: a manifest is just content-addressed JSON (the hash is its sha256),
// so from the hash one can req the entry listing directly -- the panel's
// "whole-collection link" relies on exactly that. If the collection's own hash is
// not also put into the level table, a private collection's manifest gets taken
// by strangers: file contents are still blocked by the entry levels, but the entry
// paths and hashes are all leaked (i.e. the directory structure is handed over).
func TestNodeShareCollectionManifestFollowsLevel(t *testing.T) {
	base := t.TempDir()
	h := sha("a1")
	cid := sha("b2")
	colls := map[string]*model.AnonCollection{
		cid: {FriendlyName: "pkg", Entries: []model.AnonCollectionEntry{{Path: "a.txt", Hash: h}}},
	}
	for k := range colls {
		for i := range colls[k].Entries {
			colls[k].Entries[i].Normalize()
		}
	}
	s := newScopeShare(t, base, true, nil)
	s.SetAnonAccess(func(hash string) (*model.AnonCollection, error) { return colls[hash], nil },
		func() ([]model.AnonCollectionSummary, error) { return nil, nil })

	if _, err := s.Update(ScopePatch{Collections: &[]ShareItem{{ID: cid, Level: model.LevelPrivate}}}); err != nil {
		t.Fatalf("collections: %v", err)
	}
	if s.AllowsDownload("stranger", cid, false) {
		t.Fatal("private collection manifest must be denied to strangers")
	}
	if !s.AllowsDownload("stranger", cid, true) {
		t.Fatal("self must always reach its own collection manifest")
	}

	// unlisted collection: not listed, but the manifest is fetchable by hash (otherwise a whole-collection link is meaningless)
	if _, err := s.Update(ScopePatch{Collections: &[]ShareItem{{ID: cid, Level: model.LevelUnlisted}}}); err != nil {
		t.Fatalf("collections: %v", err)
	}
	if !s.AllowsDownload("stranger", cid, false) {
		t.Fatal("unlisted collection manifest must still be fetchable by hash")
	}
	if !s.AllowsDownload("stranger", h, false) {
		t.Fatal("unlisted collection entry must still be downloadable")
	}
}

// TestNodeShareProtectedCollection_PasscodeRequired verifies that protected collections
// hide entries unless matching passcode is provided.
// 发现背景：Issue #268（Collection 级独立提取码与分级可见性）。
func TestNodeShareProtectedCollection_PasscodeRequired(t *testing.T) {
	base := t.TempDir()
	h := sha("aa")
	cid := sha("bb")
	colls := map[string]*model.AnonCollection{
		cid: {
			FriendlyName: "protected package",
			AccessPolicy: model.AccessPolicyProtected,
			Passcode:     "secret123",
			Entries:      []model.AnonCollectionEntry{{Path: "secret.txt", Hash: h}},
		},
	}
	for k := range colls {
		for i := range colls[k].Entries {
			colls[k].Entries[i].Normalize()
		}
	}
	s := newScopeShare(t, base, true, nil)
	s.SetAnonAccess(func(hash string) (*model.AnonCollection, error) { return colls[hash], nil },
		func() ([]model.AnonCollectionSummary, error) { return nil, nil })

	if _, err := s.Update(ScopePatch{Collections: &[]ShareItem{{ID: cid, Level: model.LevelPublic}}}); err != nil {
		t.Fatalf("collections: %v", err)
	}

	// 1. Without passcode: collection metadata returned, but entries hidden and marked protected
	snapNoPass := s.SnapshotForToken("stranger", "")
	if len(snapNoPass.Collections) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(snapNoPass.Collections))
	}
	c0 := snapNoPass.Collections[0]
	if !c0.IsProtected {
		t.Fatal("collection must be marked as protected without passcode")
	}
	if len(c0.Entries) != 0 {
		t.Fatalf("entries must be hidden without passcode, got %d entries", len(c0.Entries))
	}

	// 2. With wrong passcode: entries still hidden
	snapWrongPass := s.SnapshotForToken("stranger", "wrong-code")
	if !snapWrongPass.Collections[0].IsProtected || len(snapWrongPass.Collections[0].Entries) != 0 {
		t.Fatal("wrong passcode must not unlock entries")
	}

	// 3. With correct passcode: entries unlocked
	snapCorrectPass := s.SnapshotForToken("stranger", "secret123")
	cUnlocked := snapCorrectPass.Collections[0]
	if cUnlocked.IsProtected {
		t.Fatal("correct passcode must mark collection as unlocked")
	}
	if len(cUnlocked.Entries) != 1 {
		t.Fatalf("expected 1 entry unlocked, got %d", len(cUnlocked.Entries))
	}
	if cUnlocked.Entries[0].Hash != h {
		t.Fatalf("unlocked entry hash mismatch: %s != %s", cUnlocked.Entries[0].Hash, h)
	}
}

