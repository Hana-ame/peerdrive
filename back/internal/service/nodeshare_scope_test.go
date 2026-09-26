package service

// nodeshare_scope_test.go：运行时共享范围（doc/NETDISK.md M2.6）。
//
// 背景：共享范围原本只能靠环境变量在启动时定死，改一次要重启节点。改成运行时
// 可勾选之后，最容易出事的三件事是：
//  ① 重启后选择丢失（或者反过来：环境变量把运营者取消掉的共享项又播回来）；
//  ② 校验放宽——HTTP 请求体里填个 `/` 就把整个盘共享出去；
//  ③ 目录共享与单文件共享两条来源互相打架（勾了个文件，取消时因为它在
//     共享目录里而勾不掉；或者清单列得出、对端拉不到）。
// 下面的用例分别钉死这三类。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// newScopeShare 构造一个只带文件索引假数据、落盘到 storageDir 的共享服务。
// storageDir 传空即内存模式。
func newScopeShare(t *testing.T, storageDir string, enable bool, files []transport.FileInfo) *NodeShare {
	t.Helper()
	s := NewNodeShare(testShareCfg(enable, "", ""), storageDir)
	s.SetFileLister(func() ([]transport.FileInfo, error) { return files, nil })
	return s
}

// TestNodeShareRuntimeChoiceBeatsEnvAfterRestart 运行时选择优先于环境变量。
//
// 发现背景：环境变量如果每次启动都覆盖落盘状态，运营者在管理台上取消掉的共享
// 项会在重启后复活——"我明明取消了共享"是最难自查的一类反馈（他不会怀疑是
// 配置文件又生效了）。环境变量只在**首次**（无落盘文件）时播种。
func TestNodeShareRuntimeChoiceBeatsEnvAfterRestart(t *testing.T) {
	dir := t.TempDir()
	h := sha("f1")
	files := []transport.FileInfo{{Hash: h, Name: "a.txt", Path: filepath.Join(dir, "a.txt"), Size: 3}}

	// 首次启动：配置说 enable=false
	s1 := newScopeShare(t, dir, false, files)
	if s1.Scope().Enable {
		t.Fatal("seed from config must keep enable=false")
	}
	// 运营者在管理台上开启共享并勾了一个文件
	if _, err := s1.Update(ScopePatch{Enable: boolPtr(true)}); err != nil {
		t.Fatalf("update enable: %v", err)
	}
	if _, err := s1.SetFilesShared([]string{h}, true, ""); err != nil {
		t.Fatalf("share file: %v", err)
	}

	// 重启：同一个配置（enable=false），必须以落盘状态为准
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

// TestNodeShareUpdateRejectsVolumeRoot 卷根目录必须被拒（且不能改坏已有范围）。
//
// 为什么单测钉这条：值来自 HTTP 请求体，一次误填就是"共享整个盘"。
// pathutil.Within 会把 `/etc/passwd` 判成"在根内"——判定没错，是配置意图错了，
// 所以只能在入口拦。
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

// TestNodeShareUpdateRejectsInvalidHash 非法 hash 整批拒绝（不写半份范围）。
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

// TestNodeShareSingleFileOutsideDirs 单文件勾选：不在任何共享目录里的文件也能共享。
//
// 发现背景：目录粒度太粗——上传一个文件想立刻给出去，就得为它单独建一个共享
// 目录（还得重启）。按 hash 勾选与目录共享取并集，互不干扰。
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
	// 取消勾选：目录带来的那份**勾不掉**（它属于目录共享，得去目录列表里改）
	if _, err := s.SetFilesShared([]string{h2}, false, ""); err != nil {
		t.Fatalf("unshare file: %v", err)
	}
	if got := len(s.Snapshot().Files); got != 1 {
		t.Fatalf("after unshare: files = %d, want 1", got)
	}
}

// TestNodeShareCandidateFilesFlags 候选清单的 shared/by_dir 标记必须正确。
//
// 为什么不让前端自己算：目录匹配在 Windows 上有盘符大小写与分隔符混写的坑，
// 前端再实现一遍迟早和后端不一致（勾选框显示错比共享错更难发现）。
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
	// 手动勾选目录外文件后，它进入候选并正确标记（未勾选前不作为候选，见上）。
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

// TestNodeShareUpdatePartialKeepsOthers 局部更新：没传的项保持原样。
//
// 为什么不用"整体替换"：管理台一次只改一类东西，全量 PUT 会把"我只想开个
// 开关"变成一次可能覆盖别人改动的全量写。
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
	// 只改 enable
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
	// 关掉共享 = 对外空清单（不是"不过滤"）
	if got := len(s.Snapshot().Files); got != 0 {
		t.Fatalf("disabled snapshot files = %d, want 0", got)
	}
}

// TestNodeShareDirHookFiresOncePerNewDir 新增目录回调：只通知新增的那部分。
// 背景：回调是注册 file_index 可读根用的，漏一个就出现"清单列得出、拉不到"。
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
	// 追加 b：只通知 b（a 已经注册过）
	if _, err := s.Update(ScopePatch{Dirs: &[]ShareItem{{ID: a}, {ID: b}}}); err != nil {
		t.Fatalf("update dirs: %v", err)
	}
	if len(got) != 2 || got[1] != b {
		t.Fatalf("hook = %v, want [%s %s]", got, a, b)
	}
}

// TestNodeShareSelectedFileBeyondIndexPage 索引分页之外的勾选项仍然生效。
//
// 发现背景：fileList 有 1000 条上限（防远端 list verb 的 DoS）。节点登记的文件
// 超过 1000 条时，新上传的文件不在那一页里——只按页过滤的话，用户勾了它却既
// 列不出来也共享不出去（"我勾了，但什么都没发生"）。勾选按 hash 单独查，不受
// 分页影响。
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
	// 候选清单里也要看得见它——否则用户勾完找不到地方取消
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

// TestNodeShareCorruptStateFallsBackToConfig 落盘文件损坏不阻塞启动。
// 与 node_directory 同一取向：坏文件保留（人工可查），按"没保存过"处理。
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
