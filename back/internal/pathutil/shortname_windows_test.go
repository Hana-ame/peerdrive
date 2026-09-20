//go:build windows

package pathutil

// 8.3 短名的专项用例。
//
// 这个文件里每个用例都曾在 Linux CI 上无处可跑（8.3 是 NTFS/Windows 的概念），
// 于是"Windows 语义"长期处于没验证过的状态。现在它们是 Windows 真机测试矩阵
// 的一部分（见 doc/NETDISK.md §11.6）。

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithin_83ShortNameInsideRoot 修复的那个 bug：文件**真的在允许根内**，
// 只是路径写法用了 8.3 短名——以前会被判成越权。
//
// 这不是洁癖：共享目录由其它工具登记、路径穿过 Symlink/UNC/旧面板时都可能
// 变成短名形态，于是出现"文件在、hash 对、就是读不出来"。
func TestWithin_83ShortNameInsideRoot(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "本用例只在 Windows 上有意义")

	base := t.TempDir()
	root := filepath.Join(base, "Shared Media Library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	realFile := filepath.Join(root, "movie.mkv")
	require.NoError(t, os.WriteFile(realFile, []byte("x"), 0o644))

	shortRoot := ShortNameOf(root)
	if shortRoot == "" {
		t.Skip("本卷没有为这个目录生成 8.3 短名（8.3 生成可能被禁用了）")
	}
	t.Logf("root=%s short=%s", root, shortRoot)
	require.NotEqual(t, root, shortRoot, "前置条件：短名必须真的不同于长名")

	shortFile := filepath.Join(shortRoot, "movie.mkv")

	assert.True(t, Within(root, realFile), "长名写法必须在根内")
	assert.True(t, Within(root, shortFile), "同一个文件的 8.3 短名写法也必须在根内")

	// 走一遍真实打开：光有判定一致还不够，Open 也得认这个别名
	f, err := SafeOpen(root, shortFile)
	require.NoError(t, err, "短名写法要能真的打开")
	require.NoError(t, f.Close())
}

// TestWithin_83AliasCannotEscape 修 relax 之后必须确认没顺便开门：短名是别名，
// 它能指到同一个目录，但**不能**把根外的东西变成根内。
func TestWithin_83AliasCannotEscape(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "本用例只在 Windows 上有意义")

	base := t.TempDir()
	parent := filepath.Join(base, "Parent Dir")
	share := filepath.Join(parent, "Shared Media Library")
	private := filepath.Join(parent, "Private Vault")
	require.NoError(t, os.MkdirAll(share, 0o755))
	require.NoError(t, os.MkdirAll(private, 0o755))
	secret := filepath.Join(private, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))

	shortParent := ShortNameOf(parent)
	if shortParent == "" {
		t.Skip("本卷没有生成 8.3 短名")
	}
	// 用短名走到共享目录之外
	viaAlias := filepath.Join(shortParent, "Private Vault", "secret.txt")
	assert.False(t, Within(share, viaAlias), "短名不能让共享目录之外的文件变成可达")

	// 反过来：短名配共享目录、长名请求，也必须一致（两边混用不能产生缝隙）
	shortShare := ShortNameOf(share)
	if shortShare != "" {
		assert.True(t, Within(share, filepath.Join(shortShare, "a.txt")),
			"共享目录自己的短名别名应当仍算根内")
	}
}

// TestExpandShortNames_NewFile 即将写入的新文件最后一段不存在——还原不能就此
// 摆烂，目录那部分必须还是长名。
func TestExpandShortNames_NewFile(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "本用例只在 Windows 上有意义")

	base := t.TempDir()
	dir := filepath.Join(base, "Brand New Dir")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	shortDir := ShortNameOf(dir)
	if shortDir == "" {
		t.Skip("本卷没有生成 8.3 短名")
	}
	got := ExpandShortNames(filepath.Join(shortDir, "not-created-yet.bin"))
	// 注意基准也要还原：t.TempDir() 自身可能就是短名形态（CI 上是 RUNNER~1），
	// 直接拿它拼期望值会在那台机器上恒红，而还原逻辑其实是对的。
	assert.Equal(t, filepath.Join(ExpandShortNames(base), "Brand New Dir", "not-created-yet.bin"), got,
		"目录段要还原成长名，文件名原样保留")

	// 能写进去才算真的生效（写路径也走同一套还原）
	require.NoError(t, SafeWriteFileAny([]string{dir}, filepath.Join(shortDir, "not-created-yet.bin"), []byte("ok"), 0o644))
	b, err := os.ReadFile(filepath.Join(dir, "not-created-yet.bin"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(b))
}

// TestExpandShortNames_UnknownPath 不存在的路径不能被"还原成"别的东西：
// 拿不到长名就原样返回。
func TestExpandShortNames_UnknownPath(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "本用例只在 Windows 上有意义")
	base := t.TempDir()
	p := filepath.Join(base, "No Such Thing", "x.txt")
	// 同上：基准可能是短名形态，还原后才是可比较的长名写法
	assert.Equal(t, filepath.Join(ExpandShortNames(base), "No Such Thing", "x.txt"), ExpandShortNames(p))
}

// TestSafeWriteFile_ShortNameRootAlias 允许根本身配成 8.3 短名时，写路径也得认。
//
// 真出过事：GitHub 的 Windows runner 上 t.TempDir() 是 `C:\Users\RUNNER~1\...`，
// 而 pickRoot 只还原了 path 没还原 root → root 短名 + path 长名被算成两棵树 →
// 明明在根内的写被判 "path outside allowed root"（本机用户名没有 8.3 别名，
// 所以本地真机跑不出来，只能靠这里造形状）。
func TestSafeWriteFile_ShortNameRootAlias(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "本用例只在 Windows 上有意义")

	base := t.TempDir()
	dir := filepath.Join(base, "Shared Media Library")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	short := ShortNameOf(dir)
	if short == "" {
		t.Skip("本卷没有生成 8.3 短名")
	}
	require.NotEqual(t, short, dir, "短名必须真的与长名不同，否则这个用例测不到东西")

	// 允许根用短名、写入路径也用短名：两边混用不能产生"判不进去"的缝隙
	require.NoError(t, SafeWriteFileAny([]string{short}, filepath.Join(short, "a.bin"), []byte("ok"), 0o644))
	b, err := os.ReadFile(filepath.Join(dir, "a.bin"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(b))
}
