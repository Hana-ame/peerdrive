package service

// HTTP API 侧的穿透矩阵。四个入口都接受调用方路径，且各自通向不同的系统调用：
//   register_local  → os.Open（任意文件读）
//   register_folder → os.ReadDir（任意目录列举）
//   browse          → os.ReadDir（任意目录列举）
//   copy            → os.MkdirAll + os.WriteFile（任意文件写，最危险）
//
// 四个共用 FileService.isPathAllowed 一道判定，所以这里不是各测一遍，而是
// 用**同一份 payload 表**逐个入口跑一遍——将来加第五个入口时，
// 只要往入口列表里加一行，payload 不用重写。

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// traversalFixture storage 根 + 同级的"绝不能碰到"的目录。
// 故意让 outside 与 storage **同级**（而不是远在天边），因为前缀匹配写错时
// 最先误放行的就是这种"就在隔壁"的路径。
type traversalFixture struct {
	base    string
	storage string
	outside string
	secret  string
	svc     *FileService
}

func newTraversalFixture(t *testing.T) traversalFixture {
	t.Helper()
	require.NoError(t, repository.InitDB(":memory:"))
	base := t.TempDir()
	storage := filepath.Join(base, "storage")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(storage, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))

	cfg := &config.Config{StorageDir: storage, StorageEnable: true}
	return traversalFixture{
		base:    base,
		storage: storage,
		outside: outside,
		secret:  secret,
		svc:     NewFileService(cfg),
	}
}

// traversalPayloads 一份 payload，喂给所有入口。
func (f traversalFixture) payloads() []struct {
	name string
	path string
} {
	return []struct {
		name string
		path string
	}{
		{"单点向上", filepath.Join(f.storage, "..", "outside", "secret.txt")},
		{"多级向上", filepath.Join(f.storage, "sub", "..", "..", "..", "outside", "secret.txt")},
		{"相对路径向上", filepath.Join("..", "outside", "secret.txt")},
		{"相对路径多级", filepath.Join("..", "..", "..", "etc", "passwd")},
		{"根外绝对路径", f.secret},
		{"系统绝对路径", "/etc/passwd"},
		{"NUL 夹带", filepath.Join(f.storage, "ok.txt") + "\x00"},
		{"点号原地", filepath.Join(f.storage, ".", "..", "outside")},
	}
}

// TestTraversal_RegisterLocalRejects 任意文件读：登记后 provider 路径会被回读。
func TestTraversal_RegisterLocalRejects(t *testing.T) {
	f := newTraversalFixture(t)
	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			h, err := f.svc.RegisterLocal(c.path, "x.txt")
			assert.Error(t, err, "register_local 必须拒绝：%q", c.path)
			assert.Empty(t, h)
		})
	}
}

// TestTraversal_RegisterFolderRejects 任意目录列举 + 批量任意文件读。
func TestTraversal_RegisterFolderRejects(t *testing.T) {
	f := newTraversalFixture(t)
	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			res, err := f.svc.RegisterFolder(c.path)
			assert.Error(t, err, "register_folder 必须拒绝：%q", c.path)
			assert.Empty(t, res)
		})
	}
}

// TestTraversal_BrowseDirRejects 任意目录列举（连文件名带大小都吐出去）。
func TestTraversal_BrowseDirRejects(t *testing.T) {
	f := newTraversalFixture(t)
	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			entries, err := f.svc.BrowseDir(c.path)
			assert.Error(t, err, "browse 必须拒绝：%q", c.path)
			assert.Nil(t, entries)
		})
	}
}

// snapshotOutsideTree 给"storage 之外"的整棵子树拍个快照（相对路径 + 大小）。
// 越权 copy 的正确拒绝不是"返回了 error"，而是**磁盘上什么都没多出来**——
// 只断言 error 会漏掉「先写盘再改 DB 记录」那种老写法。
func (f traversalFixture) snapshotOutsideTree(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(f.base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if p == f.storage || strings.HasPrefix(p, f.storage+string(filepath.Separator)) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(f.base, p)
		if rel == "." {
			return nil
		}
		size := int64(-1)
		if !info.IsDir() {
			size = info.Size()
		}
		out = append(out, rel+":"+strconv.FormatInt(size, 10))
		return nil
	})
	require.NoError(t, err)
	sort.Strings(out)
	return out
}

// TestTraversal_CopyFileRejects 任意文件写：dest_path 直接 MkdirAll + WriteFile。
// 这是四个入口里唯一能"落地"的（配合公开 upload 可写 authorized_keys 之类），
// 所以除了越权路径，还要验它**写盘前**就拒绝，而不是写完再改 DB 记录。
func TestTraversal_CopyFileRejects(t *testing.T) {
	f := newTraversalFixture(t)
	const dummyHash = "0000000000000000000000000000000000000000000000000000000000000000"

	for _, c := range f.payloads() {
		t.Run(c.name, func(t *testing.T) {
			before := f.snapshotOutsideTree(t)

			dst, err := f.svc.CopyFile(dummyHash, c.path)
			assert.Error(t, err, "copy 必须拒绝：%q", c.path)
			assert.Empty(t, dst)

			assert.Equal(t, before, f.snapshotOutsideTree(t),
				"越权的 copy 不能在 storage 之外留下任何痕迹：%s", c.path)
		})
	}
}

// TestTraversal_SymlinkEscapeRejects 软链：根内放一个指向外部的链接。
// 目录级软链最容易被漏——"storage/alias/secret.txt" 看着在根下两层。
func TestTraversal_SymlinkEscapeRejects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	f := newTraversalFixture(t)

	fileLink := filepath.Join(f.storage, "link.txt")
	require.NoError(t, os.Symlink(f.secret, fileLink))
	_, err := f.svc.RegisterLocal(fileLink, "link.txt")
	assert.Error(t, err, "指向外部的**文件**软链必须拒绝")
	_, err = f.svc.BrowseDir(fileLink)
	assert.Error(t, err, "软链不应被当作目录浏览")

	dirLink := filepath.Join(f.storage, "alias")
	require.NoError(t, os.Symlink(f.outside, dirLink))
	_, err = f.svc.RegisterFolder(dirLink)
	assert.Error(t, err, "指向外部的**目录**软链必须拒绝")
	_, err = f.svc.BrowseDir(dirLink)
	assert.Error(t, err, "指向外部的目录软链不应被列举")
}

// TestTraversal_DeclaredShareDirAllowed storage 之外的目录，只要运营者在
// PEERDRIVE_SHARE_DIRS 里声明过就应当放行；没声明的同级目录仍拒绝。
//
// 发现背景（真实故障，2026-09-20）：曾经只有 storage 根一个判定，逼得共享目录
// 必须放在 downloads 之下，否则"登记成功、清单列得出、一拉 read failed"。
func TestTraversal_DeclaredShareDirAllowed(t *testing.T) {
	f := newTraversalFixture(t)
	share := filepath.Join(f.base, "media-on-another-disk") // 与 storage 同级
	require.NoError(t, os.MkdirAll(share, 0o755))
	movie := filepath.Join(share, "movie.mkv")
	require.NoError(t, os.WriteFile(movie, []byte("x"), 0o644))

	undeclared := f.svc
	_, err := undeclared.RegisterLocal(movie, "movie.mkv")
	assert.Error(t, err, "未声明的目录必须拒绝（不能因为「目录存在」就放行）")

	require.NoError(t, repository.InitDB(":memory:"))
	declared := NewFileService(&config.Config{
		StorageDir:    f.storage,
		StorageEnable: true,
		ShareDirs:     share,
	})
	_, err = declared.RegisterLocal(movie, "movie.mkv")
	assert.NoError(t, err, "声明过的共享目录（在 storage 之外）必须放行")

	entries, err := declared.BrowseDir(share)
	assert.NoError(t, err, "声明过的共享目录应能浏览")
	assert.NotEmpty(t, entries)

	// 声明了 A 不代表 A 的邻居也放行
	_, err = declared.RegisterLocal(f.secret, "secret.txt")
	assert.Error(t, err, "声明 media 不应连带放行它的同级目录")
}

// TestTraversal_RegisterLocalHardlinkRejected 硬链接：HTTP 登记侧的口子。
//
// 与软链不同，硬链接**没有方向**——同一个 inode 在 storage 内叫 alias.txt、
// 在外面叫 linked.txt，EvalSymlinks 认不出来，路径判定必然放行（见下面的前置
// 断言）。所以只能靠链接数。这条必须和 transport.Create 那条一起存在：
// 只在一侧判，另一侧登记同一个 inode 就绕过去了。
func TestTraversal_RegisterLocalHardlinkRejected(t *testing.T) {
	// Windows 不再跳过：它现在也能问出 NumberOfLinks（句柄版 NlinkOf）。
	if !pathutil.NlinkSupported() {
		t.Skip("本平台拿不到硬链接数，这条防线为空（见 pathutil/links_*.go）")
	}
	f := newTraversalFixture(t)

	original := filepath.Join(f.outside, "linked.txt")
	require.NoError(t, os.WriteFile(original, []byte("shared inode"), 0o644))
	alias := filepath.Join(f.storage, "alias.txt")
	if err := os.Link(original, alias); err != nil {
		t.Skipf("本环境不能建硬链接：%v", err)
	}

	assert.True(t, f.svc.isPathAllowed(alias), "前置条件：纯路径判定看不出硬链接")

	_, err := f.svc.RegisterLocal(alias, "alias.txt")
	require.Error(t, err, "有多个名字的文件必须拒绝登记")
	assert.Contains(t, err.Error(), "hard link", "错误要说明是硬链接：%v", err)

	// 误伤场景的逃生阀（pnpm node_modules / cp -l 备份目录）
	t.Setenv("PEERDRIVE_ALLOW_HARDLINKS", "1")
	h, err := f.svc.RegisterLocal(alias, "alias.txt")
	assert.NoError(t, err, "设了 PEERDRIVE_ALLOW_HARDLINKS=1 应放行")
	assert.Len(t, h, 64)
}

// TestTraversal_InsideStillWorks 防御不能过当：正常用法不能被打死。
func TestTraversal_InsideStillWorks(t *testing.T) {
	f := newTraversalFixture(t)

	inRoot := filepath.Join(f.storage, "sub", "a.txt")
	require.NoError(t, os.WriteFile(inRoot, []byte("hello"), 0o644))

	h, err := f.svc.RegisterLocal(inRoot, "a.txt")
	assert.NoError(t, err)
	assert.Len(t, h, 64)

	entries, err := f.svc.BrowseDir(f.storage)
	assert.NoError(t, err, "storage 根自身应能浏览")
	assert.NotEmpty(t, entries)

	// 相对路径写法（前端常这么传）不能因为不是绝对路径就被拒
	_, err = f.svc.BrowseDir("sub")
	assert.NoError(t, err, "根内相对路径应放行")

	// copy 到根内的"绕路"写法（含 .. 但仍落在根内）也要放行
	dst := filepath.Join(f.storage, "sub", "..", "copied.txt")
	const dummyHash = "0000000000000000000000000000000000000000000000000000000000000000"
	_, _ = f.svc.CopyFile(dummyHash, dst) // 源 hash 不存在会失败，但**不能**因路径被拒
	// 只断言它不是"路径越权"错误——源不存在是另一回事
	_, err = f.svc.CopyFile("short", dst)
	assert.NotContains(t, err.Error(), "outside", "绕路但仍在根内的写法不应判越权")
}
