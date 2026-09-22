package service

// 测试背景（doc/NETDISK.md M2 / ROADMAP 阶段 5「文件范围管理」）：
// 共享范围是这个项目里唯一"对外发布内容清单"的入口，三件事必须钉死：
//  ① 默认关（不显式开启就必须完全空）；
//  ② 受限/私有合集一律不出现在共享清单（share 帧不带请求者身份，
//     放出去等于把"仅限指定账号"的内容公开）；
//  ③ 文件共享只认显式声明的目录前缀（防"配了个父目录结果整盘都共享"）。

import (
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/transport"
)

func sha(hex string) string {
	// 造合法 64hex 测试值（内容无所谓，只要格式合法）
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

// newShareWith 构造带假数据源的共享服务。
func newShareWith(t *testing.T, cfg *config.Config, colls map[string]*model.AnonCollection, list []model.AnonCollectionSummary, files []transport.FileInfo) *NodeShare {
	t.Helper()
	// storageDir 传空 = 内存模式（不落盘）：单测只关心解析语义，落盘由
	// TestNodeSharePersist* 系列单独覆盖。
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
	s.SetFileLister(func() ([]transport.FileInfo, error) { return files, nil })
	return s
}

var errNotFound = &notFoundErr{}

type notFoundErr struct{}

func (e *notFoundErr) Error() string { return "not found" }

// TestNodeShareDisabledByDefault 默认关：不发配置就必须完全空。
// 发现背景：默认全盘分享是隐私事故（用户目标里"别人的节点能看到我的
// 文件链接"是双向的，得先由运营者决定共享什么）。
func TestNodeShareDisabledByDefault(t *testing.T) {
	pub := sha("a")
	cfg := testShareCfg(false, pub, "/tmp")
	s := newShareWith(t, cfg, map[string]*model.AnonCollection{
		pub: {FriendlyName: "pub", Entries: []model.AnonCollectionEntry{{Path: "a.txt", Providers: []model.Provider{{Type: "sha256", Value: sha("b")}}}}},
	}, nil, []transport.FileInfo{{Hash: sha("c"), Name: "c.txt", Path: "/tmp/c.txt"}})

	snap := s.Snapshot()
	if len(snap.Collections) != 0 || len(snap.Files) != 0 || len(snap.Dirs) != 0 {
		t.Fatalf("disabled share must be empty, got %+v", snap)
	}
	if got := s.Summary(); got.Collections != 0 || got.Files != 0 || got.Dirs != 0 {
		t.Fatalf("disabled summary must be zero, got %+v", got)
	}
}

// TestNodeShareExplicitPublicCollection 显式声明的 public 合集进共享清单，含条目。
// 发现背景：用户要「打包好的 collection」出现在对方的文件链接列表里，
// 因此 entries（path+hash）必须一起下发，否则对方只能看到合集名点不进去。
func TestNodeShareExplicitPublicCollection(t *testing.T) {
	hash := sha("1")
	fileHash := sha("2")
	cfg := testShareCfg(true, hash, "")
	s := newShareWith(t, cfg, map[string]*model.AnonCollection{
		hash: {
			FriendlyName: "我的电影",
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
	if c.Hash != hash || c.Name != "我的电影" || c.Size != 1 {
		t.Fatalf("collection meta wrong: %+v", c)
	}
	if len(c.Entries) != 1 || c.Entries[0].Path != "a.mp4" || c.Entries[0].Hash != fileHash || c.Entries[0].Mime != "video/mp4" {
		t.Fatalf("entries wrong: %+v", c.Entries)
	}
	if got := s.Summary().Collections; got != 1 {
		t.Fatalf("summary collections = %d, want 1", got)
	}
}

// TestNodeShareSkipsNonPublicCollections 受限/私有合集必须被跳过。
// 发现背景：share 帧没有请求者身份（ROADMAP 硬约束：第 7 阶段前不引入账号
// 依赖），因此无法校验 AccessList——放出去等于把"仅限指定账号"的合集
// 清单+文件 hash 泄给任何连上的对端。
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

// TestNodeShareAllTokenOnlyPublic 配置 "all" 时只带 public 合集。
// 发现背景：运营者想"把公开的都共享出去"，且不希望以后新增 public 合集
// 还要改配置；但 all 绝不能把受限/私有也捎带上。
func TestNodeShareAllTokenOnlyPublic(t *testing.T) {
	pub, res, pri := sha("5"), sha("6"), sha("7")
	// 空 visibility 视为 public（与 model.EffectiveVisibility 一致）
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

// TestNodeShareIgnoresInvalidCollectionHash 配置里写错的 hash 被忽略。
// 发现背景：写成名字/短 hash 会在共享清单里留一个"永远查不到的幽灵项"，
// 前端点进去必然失败；宁可启动时告警并忽略。
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

// TestNodeShareDirPrefixFilter 文件共享按目录前缀过滤。
// 发现背景：前缀匹配必须按路径分隔符边界判定——否则 /data/share2 会被
// /data/share 的前缀命中（经典越界共享）。
func TestNodeShareDirPrefixFilter(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "inside.txt")
	nested := filepath.Join(dir, "sub", "nested.txt")
	outside := filepath.Join(dir+"2", "outside.txt")
	deleted := filepath.Join(dir, "gone.txt")

	cfg := testShareCfg(true, "", dir)
	s := newShareWith(t, cfg, nil, nil, []transport.FileInfo{
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
		// 只回 basename：不回本机绝对路径（对外最小信息原则）
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

// TestNodeShareNoDirsMeansNoFiles 未配置目录时不共享任何文件（而不是共享全部）。
// 发现背景：这是"默认关"在文件维度的体现——空 dirs 若被当成"无过滤"，
// 等于 PEERDRIVE_SHARE_ENABLE=true 就泄露整个 file_index。
func TestNodeShareNoDirsMeansNoFiles(t *testing.T) {
	cfg := testShareCfg(true, "", "")
	s := newShareWith(t, cfg, nil, nil, []transport.FileInfo{
		{Hash: sha("a"), Name: "a.txt", Path: "/whatever/a.txt"},
	})
	if got := len(s.Snapshot().Files); got != 0 {
		t.Fatalf("files = %d, want 0 when no dirs configured", got)
	}
}
