package service

// nodeshare_level_test.go：三档共享级别（doc/NETDISK.md §12.6）。
//
// 一句话语义：**public = 列出来也给；unlisted = 不列出来但给；private = 只给
// 认识的人**。容易写错的地方不是"存不存得住"，而是三档之间的边界：
//   · unlisted 必须**不进清单但仍能下载**（漏了后半句，链接分享就废了）
//   · private 必须**挡住陌生人**（漏了就等于 private 和 unlisted 没区别）
//   · 同一内容被目录 + 单文件同时命中时取**最宽松**（取最严会让"我特意放宽
//     了这一个"静默失效）
//   · 级别写错（"pubilc"）必须整批拒绝，绝不悄悄兜成 public（那是把本想限制
//     的内容公开出去）

import (
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

// TestNodeSharePublicListedAndDownloadable public：进清单，谁都能取。
func TestNodeSharePublicListedAndDownloadable(t *testing.T) {
	base := t.TempDir()
	h := sha("a1")
	s := newScopeShare(t, base, true, []transport.FileInfo{
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

// TestNodeShareUnlistedHiddenButDownloadable unlisted：不进清单，但知道 hash 能取。
//
// 这条最容易写成"不列出 = 不给"——那样 unlisted 就退化成"没共享"，链接分享
// 这件唯一想支持的事就没了。
func TestNodeShareUnlistedHiddenButDownloadable(t *testing.T) {
	base := t.TempDir()
	h := sha("b1")
	s := newScopeShare(t, base, true, []transport.FileInfo{
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
	// 摘要（announce 上报的数量）也不该把它算进"对外共享了 N 个"
	if got := s.Summary().Files; got != 0 {
		t.Fatalf("unlisted must not be counted in summary, got %d", got)
	}
}

// TestNodeSharePrivateOnlyFriendsAndSelf private：只给好友和自己。
func TestNodeSharePrivateOnlyFriendsAndSelf(t *testing.T) {
	base := t.TempDir()
	h := sha("c1")
	s := newScopeShare(t, base, true, []transport.FileInfo{
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
	// 好友名单为空 → 谁都不是好友
	if got := len(s.SnapshotFor("stranger-peer").Files); got != 0 {
		t.Fatalf("private must not be listed to strangers, got %d", got)
	}
	if _, err := s.Update(ScopePatch{Friends: &[]string{"Friend-Node"}}); err != nil {
		t.Fatalf("set friends: %v", err)
	}
	if !s.AllowsDownload("friend-node", h, false) {
		t.Fatal("friend must be allowed (id match is case-insensitive)")
	}
	// 好友要能看到 private 清单：给了权限却不给目录，等于没给
	if got := len(s.SnapshotFor("Friend-Node").Files); got != 1 {
		t.Fatalf("private must be listed to friends, got %d", got)
	}
}

// TestNodeShareLoosestLevelWins 目录 unlisted + 单文件 public → 该文件 public。
func TestNodeShareLoosestLevelWins(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "media")
	h := sha("d1")
	s := newScopeShare(t, base, true, []transport.FileInfo{
		{Hash: h, Name: "a.txt", Path: filepath.Join(dir, "a.txt"), Size: 1},
	})
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: dir, Level: model.LevelUnlisted}}}); err != nil {
		t.Fatalf("dirs: %v", err)
	}
	if got := len(s.Snapshot().Files); got != 0 {
		t.Fatalf("dir unlisted ⇒ not listed, got %d", got)
	}
	// 单独把这个文件放宽成 public
	if _, err := s.SetFilesShared([]string{h}, true, model.LevelPublic); err != nil {
		t.Fatalf("share: %v", err)
	}
	snap := s.Snapshot()
	if len(snap.Files) != 1 {
		t.Fatalf("loosest must win (public), listed=%d", len(snap.Files))
	}
	// 反过来：目录 public + 文件 private，仍是 public（目录更宽松）
	s2 := newScopeShare(t, base, true, []transport.FileInfo{
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

// TestNodeShareInvalidLevelRejected 级别写错整批拒绝（不兜成 public）。
func TestNodeShareInvalidLevelRejected(t *testing.T) {
	base := t.TempDir()
	h := sha("e1")
	s := newScopeShare(t, base, true, []transport.FileInfo{
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
	// 非法好友 ID（含空白）同样拒绝
	if _, err := s.Update(ScopePatch{Friends: &[]string{"node id"}}); err == nil {
		t.Fatal("friend id with whitespace must be rejected")
	}
}

// TestNodeShareSetLevelOverrides 显式改级别要能收紧（public → private）。
//
// 坑：一开始写成"取最宽松"合并，结果已公开的文件改成私密永远改不动——界面上
// 显示的是"我选了私密，但刷新回来还是公开"。合并只发生在解析时（目录 ∪ 单文件），
// 用户在下拉里的选择是**显式覆盖**。
func TestNodeShareSetLevelOverrides(t *testing.T) {
	base := t.TempDir()
	h := sha("a3")
	s := newScopeShare(t, base, true, []transport.FileInfo{
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

// TestNodeShareLevelPersistedAcrossRestart 级别随范围一起落盘。
func TestNodeShareLevelPersistedAcrossRestart(t *testing.T) {
	base := t.TempDir()
	h := sha("a2")
	files := []transport.FileInfo{{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1}}
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

// TestNodeShareLegacyStringItemsStillPublic 历史落盘（字符串数组）读作 public。
//
// 升级前写的 share_scope.json 只有 id。读不懂就退回初值，等于把运营者选好的
// 范围丢掉——而且丢得悄无声息（重启后清单空了，他会以为文件没了）。
func TestNodeShareLegacyStringItemsStillPublic(t *testing.T) {
	base := t.TempDir()
	h := sha("b2")
	files := []transport.FileInfo{{Hash: h, Name: "a.txt", Path: filepath.Join(base, "a.txt"), Size: 1}}
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

// writeScopeFile 直接写落盘状态（模拟历史版本/手改过的 share_scope.json）。
func writeScopeFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, shareScopeFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestNodeShareCollectionVisibilityDowngradesToPrivate 合集自身非 public → 降级。
//
// 受限/私有合集的 AccessList 是账号列表，无身份就校验不了；把它按 public 发出去
// 等于一条 share 帧绕过账号门禁。降级成 private 后至少只给好友。
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

// TestNodeShareCollectionManifestFollowsLevel 合集 manifest 自身也受级别约束。
//
// 背景：manifest 就是一份按内容寻址存的 JSON（hash 即它的 sha256），凭 hash 能
// 直接 req 回条目清单——面板的「合集整包链接」正是靠这条。如果不把合集自身的
// hash 也算进级别表，private 合集的 manifest 会被陌生人取走：文件内容仍被条目
// 级别挡着，但条目路径与 hash 全泄了（等于把目录结构交出去）。
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

	// unlisted 合集：不列出，但 manifest 凭 hash 可取（否则整包链接就没有意义）
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
