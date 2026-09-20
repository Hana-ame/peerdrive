package transport

// create / upload / write-file 这三个 verb 是**对端可控**的路径入口：
// path 字段由对端整个塞进来，name 字段直接参与落盘路径拼接。
// pathutil.Within 已经挡住字符串层面的穿透，这里验的是"挡住之后各 verb 真的
// 没有绕过它另开一条路"——历史上出过事的就是这种"校验在一处、落盘在另一处"。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/pathutil"
)

// newTraversalIndex 根目录 + 根外的诱饵文件。
func newTraversalIndex(t *testing.T) (*FileIndexService, string, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))
	svc := NewFileIndexService(root)
	t.Cleanup(svc.Close) // Windows：不关会话句柄，TempDir 清不掉
	return svc, root, secret
}

func TestTraversal_CreateRejectsEscape(t *testing.T) {
	initTestDB(t)
	svc, root, secret := newTraversalIndex(t)
	parent := filepath.Dir(root)

	cases := []struct {
		name string
		path string
	}{
		{"向上逃逸", filepath.Join(root, "..", filepath.Base(parent), "secret.txt")},
		{"多级向上逃逸", filepath.Join(root, "a", "b", "..", "..", "..", "..", "etc", "passwd")},
		{"根外绝对路径", secret},
		{"系统绝对路径", "/etc/passwd"},
		{"NUL 夹带（否则在根内）", filepath.Join(root, "ok.txt") + "\x00"},
		{"点号原地逃逸", filepath.Join(root, ".", "..", filepath.Base(parent), "secret.txt")},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Create(c.path)
			assert.Error(t, err, "create 必须拒绝：%q", c.path)
		})
	}
}

// TestTraversal_CreateAllowsInside 防御不能过当：根内的正常路径必须能登记，
// 否则"共享目录放外面就拉不到"那类伪约束又会回来。
func TestTraversal_CreateAllowsInside(t *testing.T) {
	initTestDB(t)
	svc, root, _ := newTraversalIndex(t)

	inRoot := filepath.Join(root, "sub", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(inRoot), 0o755))
	require.NoError(t, os.WriteFile(inRoot, []byte("ok"), 0o644))

	fi, err := svc.Create(inRoot)
	require.NoError(t, err)
	assert.Equal(t, "a.txt", fi.Name)

	// 冗余写法（重复分隔符 / 中间夹 "." ）不应被误判
	redundant := root + string(filepath.Separator) + string(filepath.Separator) + "sub" +
		string(filepath.Separator) + "." + string(filepath.Separator) + "a.txt"
	_, err = svc.Create(redundant)
	assert.NoError(t, err, "冗余但仍在根内的写法应放行")
}

// TestTraversal_CreateSymlink 根内软链指向根外 → 拒绝。目录级软链尤其阴：
// "root/alias/secret.txt" 看着在根下两层。
func TestTraversal_CreateSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	initTestDB(t)
	svc, root, secret := newTraversalIndex(t)
	outside := filepath.Dir(secret)

	link := filepath.Join(root, "link.txt")
	require.NoError(t, os.Symlink(secret, link))
	_, err := svc.Create(link)
	assert.Error(t, err, "指向根外的文件软链必须拒绝")

	dirLink := filepath.Join(root, "alias")
	require.NoError(t, os.Symlink(outside, dirLink))
	_, err = svc.Create(filepath.Join(dirLink, "secret.txt"))
	assert.Error(t, err, "指向根外的目录软链必须拒绝")
}

// TestTraversal_WriteFileNameEscape upload/write-file 的 name 直接进
// filepath.Join(uploadDir, sanitizeName(name))，是落盘路径的最后一道拼接。
func TestTraversal_WriteFileNameEscape(t *testing.T) {
	initTestDB(t)
	svc, root, _ := newTraversalIndex(t)

	cases := []struct {
		name string
		in   string
	}{
		{"点点向上", "../../evil.txt"},
		{"反斜杠点点向上", `..\..\evil.txt`},
		{"纯点点", ".."},
		{"多级点点", "../a/../../evil.txt"},
		{"绝对路径", "/etc/passwd"},
		{"带子目录", "sub/evil.txt"},
		{"空名", ""},
		{"点", "."},
		{"根", "/"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fi, err := svc.WriteFile(c.in, strings.NewReader("payload"))
			require.NoError(t, err, "WriteFile 不应报错，但要落在根目录内")
			// 落盘位置必须还在 uploadDir 之下——这是唯一的硬断言
			assert.Contains(t, filepath.Clean(fi.Path), filepath.Clean(root),
				"name=%q 逃出了根目录：%s", c.in, fi.Path)
			assert.True(t, svc.IsPathAllowed(fi.Path), "name=%q 落到了不允许的位置：%s", c.in, fi.Path)
		})
	}
}

// TestTraversal_CreateHardlinkRejected 硬链接：没有方向，EvalSymlinks 认不出来。
// 同一个 inode 在根内有一个名字、根外还有另一个，从路径上无法判断它有没有被
// 暴露出去，所以只能"有多个名字就拒绝"。
func TestTraversal_CreateHardlinkRejected(t *testing.T) {
	// 不再按 GOOS 跳过：以前就是因为 Windows 上没人验证，那条防线悄悄地是空的。
	// 现在 Windows 走 GetFileInformationByHandle 也能拿到 NumberOfLinks，
	// 这条用例在两个平台上都必须真的跑一遍。
	if !pathutil.NlinkSupported() {
		t.Skip("本平台拿不到硬链接数，这条防线为空（见 pathutil/links_*.go）")
	}
	initTestDB(t)
	svc, root, _ := newTraversalIndex(t)

	outside := t.TempDir()
	original := filepath.Join(outside, "linked.txt")
	require.NoError(t, os.WriteFile(original, []byte("shared inode"), 0o644))
	alias := filepath.Join(root, "alias.txt")
	if err := os.Link(original, alias); err != nil {
		t.Skipf("本环境不能跨目录建硬链接：%v", err)
	}

	// 路径判定是**放行**的——它看不出这是硬链接，这正是软链之外的另一个口子
	assert.True(t, svc.IsPathAllowed(alias), "前置条件：纯路径判定看不出硬链接")

	_, err := svc.Create(alias)
	require.Error(t, err, "有多个名字的文件必须拒绝登记")
	assert.Contains(t, err.Error(), "hard link", "错误要说明是硬链接：%v", err)

	// 开关打开后放行（pnpm node_modules / git alternates 这类目录需要）。
	// 判定每次读环境变量，所以 t.Setenv 就能验——不必为了测试改包级变量。
	t.Setenv("PEERDRIVE_ALLOW_HARDLINKS", "1")
	_, err = svc.Create(alias)
	assert.NoError(t, err, "PEERDRIVE_ALLOW_HARDLINKS=1 时应放行")
}

// TestTraversal_ReadRootSymlinkEscape 声明了共享根目录之后，读取边界放宽了，
// 但**软链逃逸不能跟着放宽**：共享目录里放一个指向 /etc 的软链，仍要判越权。
func TestTraversal_ReadRootSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	initTestDB(t)
	svc, root, secret := newTraversalIndex(t)
	share := t.TempDir() // 模拟 PEERDRIVE_SHARE_DIRS，在下载根之外

	svc.AddReadRoot(share)

	// 声明过 → 可读（这是上一轮修的产品约束）
	inShare := filepath.Join(share, "movie.mkv")
	require.NoError(t, os.WriteFile(inShare, []byte("x"), 0o644))
	assert.True(t, svc.IsPathReadable(inShare), "声明过的共享目录必须可读")

	// 共享目录里的软链指向别处 → 越权
	require.NoError(t, os.Symlink(secret, filepath.Join(share, "link.txt")))
	assert.False(t, svc.IsPathReadable(filepath.Join(share, "link.txt")),
		"共享目录内指向外部的软链必须判越权")

	// 但登记/写入边界不能因为 AddReadRoot 被带开
	_, err := svc.Create(inShare)
	assert.Error(t, err, "可读 ≠ 可登记：写边界不许被 AddReadRoot 带开")
	_ = root
}

// TestTraversal_RedactDisallowedPath 历史库/旧版本可能残留根目录外的 Path，
// list/info/sync 对外回显时必须脱敏，不能把绝对路径泄给对端。
func TestTraversal_RedactDisallowedPath(t *testing.T) {
	svc, root, secret := newTraversalIndex(t)
	p := &PeerJSService{fileIndex: svc}

	got := p.redactDisallowedPath(FileInfo{Hash: "h", Path: secret, Name: "secret.txt"})
	assert.Equal(t, "", got.Path, "根目录外的 Path 必须脱敏")
	assert.Equal(t, "secret.txt", got.Name, "Name 可以留")

	inside := filepath.Join(root, "a.txt")
	got = p.redactDisallowedPath(FileInfo{Hash: "h", Path: inside})
	assert.Equal(t, inside, got.Path, "根目录内的 Path 应保留")
}
