package service

// nodeshare_scope_test.go: runtime sharing scope (doc/NETDISK.md M2.6).
//
// Background: the sharing scope used to be fixed at startup via environment
// variables only; changing it once meant restarting the node. After making it
// checkable at runtime, the three things most likely to go wrong are:
//  1. the choice is lost after a restart (or the reverse: an env var resurrects a
//     share item the operator had cancelled);
//  2. relaxed validation -- putting `/` in the HTTP request body shares the whole disk;
//  3. directory sharing and single-file sharing conflict (a file is checked but
//     cannot be unchecked because it sits inside a shared directory; or the listing
//     shows it but the peer cannot fetch it).
// The cases below pin down these three classes respectively.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// newScopeShare builds a share service with only fake file-index data, persisting to storageDir.
// Pass an empty storageDir for in-memory mode.
func newScopeShare(t *testing.T, storageDir string, enable bool, files []transport.FileInfo) *NodeShare {
	t.Helper()
	s := NewNodeShare(testShareCfg(enable, "", ""), storageDir)
	s.SetFileLister(func() ([]transport.FileInfo, error) { return files, nil })
	return s
}

// TestNodeShareRuntimeChoiceBeatsEnvAfterRestart a runtime choice beats the env var.
//
// Discovery background: if env vars overwrote persisted state on every startup,
// share items the operator had cancelled in the admin console would come back to
// life after a restart -- "I clearly cancelled the share" is the hardest kind of
// feedback to self-diagnose (he would not suspect the config file had taken
// effect again). Env vars only seed on the **first** run (no persisted file).
func TestNodeShareRuntimeChoiceBeatsEnvAfterRestart(t *testing.T) {
	dir := t.TempDir()
	h := sha("f1")
	files := []transport.FileInfo{{Hash: h, Name: "a.txt", Path: filepath.Join(dir, "a.txt"), Size: 3}}

	// first start: config says enable=false
	s1 := newScopeShare(t, dir, false, files)
	if s1.Scope().Enable {
		t.Fatal("seed from config must keep enable=false")
	}
	// the operator enables sharing in the admin console and checks a file
	if _, err := s1.Update(ScopePatch{Enable: boolPtr(true)}); err != nil {
		t.Fatalf("update enable: %v", err)
	}
	if _, err := s1.SetFilesShared([]string{h}, true, ""); err != nil {
		t.Fatalf("share file: %v", err)
	}

	// restart: same config (enable=false), but the persisted state must win
	s2 := newScopeShare(t, dir, false, files)
	if !s2.Scope().Enable {
		t.Fatal("runtime choice must survive restart (enable lost)")
	}
	if got := s2.Scope().Files; len(got) != 1 || got[0].ID != h {
		t.Fatalf("runtime files = %v, want [%s]", got, h)
	}
	if got := len(s2.Snapshot().Files); got != 1 {
		t.Fatalf("snapshot files = %d, want 1", got)
	}
}

// TestNodeShareUpdateRejectsVolumeRoot the volume root must be rejected (and must not corrupt an existing scope).
//
// Why a unit test pins this: the value comes from the HTTP request body, so one
// typo means "share the whole disk". pathutil.Within calls `/etc/passwd` "inside
// the root" -- the judgement is correct, the config intent is wrong, so this can
// only be stopped at the entry point.
func TestNodeShareUpdateRejectsVolumeRoot(t *testing.T) {
	dir := t.TempDir()
	s := newScopeShare(t, dir, true, nil)
	root := config.DefaultRootPath() // Unix "/" / Windows "C:\"
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: root}}}); err == nil {
		t.Fatalf("volume root %q must be rejected", root)
	}
	if got := s.Scope().Dirs; len(got) != 0 {
		t.Fatalf("rejected update must not modify scope, dirs=%v", got)
	}
}

// TestNodeShareUpdateRejectsInvalidHash an invalid hash rejects the whole batch (no half-written scope).
func TestNodeShareUpdateRejectsInvalidHash(t *testing.T) {
	dir := t.TempDir()
	s := newScopeShare(t, dir, true, nil)
	bad := []ShareItem{{ID: sha("aa")}, {ID: "not-a-hash"}}
	if _, err := s.Update(ScopePatch{Files: &bad}); err == nil {
		t.Fatal("invalid hash must be rejected")
	}
	if got := s.Scope().Files; len(got) != 0 {
		t.Fatalf("rejected update must not modify scope, files=%v", got)
	}
}

// TestNodeShareSingleFileOutsideDirs single-file check: a file in no shared directory can also be shared.
//
// Discovery background: directory granularity is too coarse -- to hand out one
// uploaded file immediately you had to create a dedicated shared directory for it
// (plus a restart). Checking by hash and directory sharing take the union and do
// not interfere with each other.
func TestNodeShareSingleFileOutsideDirs(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "shared")
	other := filepath.Join(base, "other")
	h1, h2 := sha("11"), sha("22")
	files := []transport.FileInfo{
		{Hash: h1, Name: "in.txt", Path: filepath.Join(shared, "in.txt"), Size: 1},
		{Hash: h2, Name: "out.txt", Path: filepath.Join(other, "out.txt"), Size: 2},
	}
	s := newScopeShare(t, base, true, files)
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: shared}}}); err != nil {
		t.Fatalf("update dirs: %v", err)
	}
	if got := len(s.Snapshot().Files); got != 1 {
		t.Fatalf("before: files = %d, want 1 (only the one under shared dir)", got)
	}
	if _, err := s.SetFilesShared([]string{h2}, true, ""); err != nil {
		t.Fatalf("share file: %v", err)
	}
	snap := s.Snapshot()
	if len(snap.Files) != 2 {
		t.Fatalf("after: files = %d, want 2 (dir + single pick): %+v", len(snap.Files), snap.Files)
	}
	// uncheck: the part coming from the directory **cannot be unchecked** (it belongs to the directory share; edit it in the directory list)
	if _, err := s.SetFilesShared([]string{h2}, false, ""); err != nil {
		t.Fatalf("unshare file: %v", err)
	}
	if got := len(s.Snapshot().Files); got != 1 {
		t.Fatalf("after unshare: files = %d, want 1", got)
	}
}

// TestNodeShareCandidateFilesFlags the shared/by_dir flags on the candidate listing must be correct.
//
// Why not let the frontend compute it: directory matching on Windows has
// drive-letter case and mixed-separator pitfalls, and re-implementing it in the
// frontend will sooner or later disagree with the backend (a wrong checkbox is
// harder to spot than a wrong share).
func TestNodeShareCandidateFilesFlags(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "shared")
	h1, h2 := sha("31"), sha("32")
	files := []transport.FileInfo{
		{Hash: h1, Name: "in.txt", Path: filepath.Join(shared, "in.txt"), Size: 1},
		{Hash: h2, Name: "out.txt", Path: filepath.Join(base, "out.txt"), Size: 2},
	}
	s := newScopeShare(t, base, true, files)
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: shared}}}); err != nil {
		t.Fatalf("update dirs: %v", err)
	}
	items := s.CandidateFiles()
	if len(items) != 1 {
		t.Fatalf("candidates = %d, want 1 (only file under shared dir)", len(items))
	}
	if !items[0].Shared || !items[0].ByDir {
		t.Fatalf("file under shared dir must be shared+by_dir: %+v", items[0])
	}
	// after manually checking a file outside the directory, it enters the candidates with the right flags (before checking it is not a candidate, see above).
	if _, err := s.SetFilesShared([]string{h2}, true, ""); err != nil {
		t.Fatalf("share file: %v", err)
	}
	items = s.CandidateFiles()
	if len(items) != 2 {
		t.Fatalf("candidates after pick = %d, want 2", len(items))
	}
	byHash := map[string]ShareFileItem{}
	for _, it := range items {
		byHash[it.Hash] = it
	}
	if !byHash[h1].Shared || !byHash[h1].ByDir {
		t.Fatalf("file under shared dir must be shared+by_dir: %+v", byHash[h1])
	}
	if !byHash[h2].Shared || byHash[h2].ByDir {
		t.Fatalf("picked file outside dir must be shared but not by_dir: %+v", byHash[h2])
	}
}

// TestNodeShareUpdatePartialKeepsOthers partial update: items not sent keep their values.
//
// Why not "full replacement": the admin console changes one kind of thing at a
// time, and a full PUT would turn "I only want to flip a switch" into a full
// write that may clobber someone else's changes.
func TestNodeShareUpdatePartialKeepsOthers(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	h := sha("41")
	s := newScopeShare(t, dir, true, []transport.FileInfo{{Hash: h, Name: "a", Path: filepath.Join(shared, "a")}})
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: shared}}}); err != nil {
		t.Fatalf("update dirs: %v", err)
	}
	if _, err := s.SetFilesShared([]string{h}, true, ""); err != nil {
		t.Fatalf("share file: %v", err)
	}
	// change only enable
	if _, err := s.Update(ScopePatch{Enable: boolPtr(false)}); err != nil {
		t.Fatalf("update enable: %v", err)
	}
	sc := s.Scope()
	if sc.Enable {
		t.Fatal("enable must be false")
	}
	if len(sc.Dirs) != 1 || len(sc.Files) != 1 {
		t.Fatalf("partial update must keep dirs/files: %+v", sc)
	}
	// sharing off = empty external listing (not "no filter")
	if got := len(s.Snapshot().Files); got != 0 {
		t.Fatalf("disabled snapshot files = %d, want 0", got)
	}
}

// TestNodeShareDirHookFiresOncePerNewDir new-directory callback: notify only the newly added part.
// Background: the callback registers readable roots for file_index; missing one yields "listed but not fetchable".
func TestNodeShareDirHookFiresOncePerNewDir(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	s := newScopeShare(t, base, true, nil)
	var got []string
	s.SetDirHook(func(dirs []string) { got = append(got, dirs...) })

	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: a}}}); err != nil {
		t.Fatalf("update dirs: %v", err)
	}
	if len(got) != 1 || got[0] != a {
		t.Fatalf("hook = %v, want [%s]", got, a)
	}
	// append b: notify only b (a is already registered)
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: a}, {ID: b}}}); err != nil {
		t.Fatalf("update dirs: %v", err)
	}
	if len(got) != 2 || got[1] != b {
		t.Fatalf("hook = %v, want [%s %s]", got, a, b)
	}
}

// TestNodeShareSelectedFileBeyondIndexPage a checked item beyond the index page still takes effect.
//
// Discovery background: fileList has a 1000-entry cap (to prevent a DoS on the
// remote list verb). When the files registered by a node exceed 1000, a newly
// uploaded file is not on that page -- filtering by page only means the user
// checks it but it is neither listed nor shared ("I checked it, but nothing
// happened"). A check is looked up separately by hash and is not affected by
// pagination.
func TestNodeShareSelectedFileBeyondIndexPage(t *testing.T) {
	base := t.TempDir()
	onPage := sha("aa")
	offPage := sha("bb")
	s := newScopeShare(t, base, true, []transport.FileInfo{
		{Hash: onPage, Name: "page.txt", Path: filepath.Join(base, "page.txt"), Size: 1},
	})
	s.SetFileInfoReader(func(hash string) (*transport.FileInfo, error) {
		if hash == offPage {
			return &transport.FileInfo{Hash: offPage, Name: "offpage.txt", Path: filepath.Join(base, "offpage.txt"), Size: 7}, nil
		}
		return nil, fmt.Errorf("not found: %s", hash)
	})
	if _, err := s.SetFilesShared([]string{offPage}, true, ""); err != nil {
		t.Fatalf("share file: %v", err)
	}
	snap := s.Snapshot()
	if len(snap.Files) != 1 || snap.Files[0].Hash != offPage {
		t.Fatalf("snapshot = %+v, want only the selected off-page file", snap.Files)
	}
	// it must also be visible in the candidate listing -- otherwise the user has nowhere to uncheck it after checking
	var found bool
	for _, it := range s.CandidateFiles() {
		if it.Hash == offPage && it.Shared {
			found = true
		}
	}
	if !found {
		t.Fatalf("selected file must appear in candidates: %+v", s.CandidateFiles())
	}
}

// TestNodeShareCorruptStateFallsBackToConfig a corrupt persisted file does not block startup.
// Same orientation as node_directory: keep the bad file (for manual inspection) and treat it as "never saved".
func TestNodeShareCorruptStateFallsBackToConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := testShareCfg(true, "", dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, shareScopeFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewNodeShare(cfg, dir)
	if !s.Scope().Enable {
		t.Fatal("corrupt state must fall back to config (enable=true)")
	}
	if got := s.Scope().Dirs; len(got) != 1 {
		t.Fatalf("dirs = %v, want 1 (from config)", got)
	}
}

func boolPtr(b bool) *bool { return &b }
