package pathutil

// 写路径（copy/delete/upload 落盘）上的软链逃逸。
//
// 为什么单开一个文件：读取侧（safeopen_test.go）早就用 os.Root 消掉了 TOCTOU，
// 但写/落盘侧一直还是"先 Within 判一把，再 os.WriteFile / os.Remove"的两步走。
// 这两步之间隔着一次路径解析——共享目录里能写东西的人可以在中间把某个**目录
// 成分**换成软链，把写/删引导到根之外。下面的用例就是那个形状。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWriteFixture 允许根 root + 根外的诱饵区 outside（里面有 victim.txt）。
type writeFixture struct {
	root    string
	outside string
	victim  string
}

func newWriteFixture(t *testing.T) writeFixture {
	t.Helper()
	return writeFixture{
		root:    t.TempDir(),
		outside: t.TempDir(),
		victim:  filepath.Join(t.TempDir(), "victim.txt"), // 单独位置，避免误伤
	}
}

// TestSafeWriteFile_FinalComponentSymlink 最经典的形状：根内放一个指向受害文件
// 的软链，然后按"正常路径"去写。**在读看来路径完全合法**（Within 必然放行），
// 只有跟ague（实际确实走过 WritFile）才暴露。
//
// TestSafeWriteFile_FinalComponentSymlinkRace TOCTOU 的标准形状：
// 先判路径（此刻一切正常）→ 攻击者把成分换成软链 → 再落盘。
//
// 为什么必须**先判再换**：静态摆着的软链 Within 也挡得住（normalize 里有
// EvalSymlinks），真正没人管的正是"判定与落盘之间那段时间"。所以要模拟的是
// 竞态，而不是放一个静静等着看的软链——后者证明不了新代码干了什么。
//
// os.WriteFile 会跟着这个软链把内容写到外面去；走 Root 之后必须失败。
func TestSafeWriteFile_FinalComponentSymlinkRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接需要开发者模式/管理员")
	}
	f := newWriteFixture(t)
	require.NoError(t, os.WriteFile(f.victim, []byte("ORIGINAL"), 0o600))

	target := filepath.Join(f.root, "report.txt")

	// 1) 校验通过：此刻它还不是软链
	assert.True(t, WithinAny([]string{f.root}, target), "前置条件：此刻路径判定应当放行")

	// 2) 攻击者抢在这两步之间把它换成了指向受害文件的软链
	require.NoError(t, os.Symlink(f.victim, target))

	// 3) 落盘：旧的 os.WriteFile 会一路跟到根外；新的必须拒绝
	err := SafeWriteFileAny([]string{f.root}, target, []byte("PWNED"), 0o644)
	assert.Error(t, err, "判定之后被换成指向根外的软链时，写必须失败")

	got, rerr := os.ReadFile(f.victim)
	require.NoError(t, rerr)
	assert.Equal(t, "ORIGINAL", string(got), "受害文件必须一字未改")

	// 对照实验：同一套竞态，旧的 os.WriteFile 确实会把内容写到根外去。
	// 有这一条，上面的失败才不能被解释成"环境里软链本来就不可用"之类的偶然。
	legacyVictim := filepath.Join(f.outside, "legacy-victim.txt")
	require.NoError(t, os.WriteFile(legacyVictim, []byte("ORIGINAL"), 0o600))
	legacyLink := filepath.Join(f.root, "legacy.txt")
	require.NoError(t, os.Symlink(legacyVictim, legacyLink))
	require.NoError(t, os.WriteFile(legacyLink, []byte("PWNED"), 0o644))
	gotLegacy, lerr := os.ReadFile(legacyVictim)
	require.NoError(t, lerr)
	assert.Equal(t, "PWNED", string(gotLegacy), "对照：os.WriteFile 确实写穿了软链——这正是要堵的洞")
}

// TestSafeWriteFile_ParentDirSymlinkRace 更隐蔽的一支：换掉的是**父目录**
// （真目录 → 指向根外的软链）。目标文件名是全新的，它自己永远不可能是软链，
// 所以"最终 component 不能是软链"这类检查根本看不到问题上哪儿去了。
func TestSafeWriteFile_ParentDirSymlinkRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接需要开发者模式/管理员")
	}
	f := newWriteFixture(t)
	outsideDir := filepath.Join(f.outside, "dir")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))

	sub := filepath.Join(f.root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "new.txt")
	assert.True(t, WithinAny([]string{f.root}, target), "前置条件：此刻 sub 还是真目录")

	// 换掉中间那一层
	require.NoError(t, os.Remove(sub))
	require.NoError(t, os.Symlink(outsideDir, sub))

	err := SafeWriteFileAny([]string{f.root}, target, []byte("PWNED"), 0o644)
	assert.Error(t, err, "父目录被换成逃向根外的软链时不能写")

	_, sterr := os.Stat(filepath.Join(outsideDir, "new.txt"))
	assert.True(t, os.IsNotExist(sterr), "根外不能出现新文件")
}

// TestSafeRemove_ParentDirSymlinkRace os.Remove 的老问题：它只解路径不判边界，
// 会跟着路径里的软链走到任意文件上——"允许管理自己的目录"于是变成"能删任意
// 文件"。同样是先判（合法）、后换（中间成分变软链）。
func TestSafeRemove_ParentDirSymlinkRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接需要开发者模式/管理员")
	}
	f := newWriteFixture(t)

	outsideDir := filepath.Join(f.outside, "dir")
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))
	outsideTarget := filepath.Join(outsideDir, "target.txt")
	require.NoError(t, os.WriteFile(outsideTarget, []byte("do not touch"), 0o600))

	sub := filepath.Join(f.root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	target := filepath.Join(sub, "target.txt")
	assert.True(t, WithinAny([]string{f.root}, target), "前置条件：此刻路径判定应当放行")

	require.NoError(t, os.Remove(sub))
	require.NoError(t, os.Symlink(outsideDir, sub))

	err := SafeRemoveAny([]string{f.root}, target)
	assert.Error(t, err, "经软链指向根外的删除必须失败")

	_, sterr := os.Stat(outsideTarget)
	assert.NoError(t, sterr, "根外的文件必须还在")
}

// TestSafeWriteFile_RefusesOutside 路径本身就在根外：任何 ".." 写法都不该通过。
func TestSafeWriteFile_RefusesOutside(t *testing.T) {
	f := newWriteFixture(t)
	require.NoError(t, os.WriteFile(f.victim, []byte("keep"), 0o600))

	escaped := filepath.Join(f.root, "..", filepath.Base(f.outside), "..", filepath.Base(filepath.Dir(f.victim)), filepath.Base(f.victim))
	escaped = f.victim // 直接用根外真实路径更直白

	err := SafeWriteFileAny([]string{f.root}, escaped, []byte("x"), 0o644)
	assert.ErrorIs(t, err, ErrOutsideRoot, "根外路径必须报 ErrOutsideRoot：%s", escaped)

	err = SafeRemoveAny([]string{f.root}, escaped)
	assert.ErrorIs(t, err, ErrOutsideRoot, "根外路径的删除同样要挡：%s", escaped)

	_, sterr := os.Stat(f.victim)
	assert.NoError(t, sterr, "根外文件不能被写/删到")
}

// TestSafeWriteFile_InsideStillWorks 防御不能过当：正常写必须还写得进去，
// 包括"父目录还不存在"这种以前靠 os.MkdirAll 兜住的场景。
func TestSafeWriteFile_InsideStillWorks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-exist-yet") // 根自己都还没建出来

	err := SafeWriteFileAny([]string{root}, filepath.Join(root, "a", "b", "c.txt"), []byte("hello"), 0o644)
	require.NoError(t, err, "允许根内的写不应被拦（含自动建父目录）")

	got, rerr := os.ReadFile(filepath.Join(root, "a", "b", "c.txt"))
	require.NoError(t, rerr)
	assert.Equal(t, "hello", string(got))

	f, err := SafeOpenFileAny([]string{root}, filepath.Join(root, "d.txt"), os.O_CREATE|os.O_RDWR, 0o644)
	require.NoError(t, err)
	_, werr := f.WriteString("handle-ok")
	require.NoError(t, werr)
	require.NoError(t, f.Close())

	require.NoError(t, SafeRemoveAny([]string{root}, filepath.Join(root, "d.txt")))
	_, sterr := os.Stat(filepath.Join(root, "d.txt"))
	assert.True(t, os.IsNotExist(sterr), "删除应当真的生效")
}

// TestSafeWriteFile_RejectsRootItself 允许根自身不能被写/删：
// rel == "." 必须是显式拒绝，而不是"碰巧换算出来没有 deleterious 行为"。
func TestSafeWriteFile_RejectsRootItself(t *testing.T) {
	root := t.TempDir()
	assert.Error(t, SafeWriteFileAny([]string{root}, root, []byte("x"), 0o644))
	assert.Error(t, SafeRemoveAny([]string{root}, root))
	assert.Error(t, SafeRemoveAllAny([]string{root}, root))
	assert.Error(t, SafeMkdirAllAny([]string{root}, root, 0o755))
	_, sterr := os.Stat(root)
	assert.NoError(t, sterr, "拒绝操作之后根目录必须还在")
}

// TestSafeWriteFile_NULAndReserved 字符串层面的 payload 在写路径上同样要挡：
// NUL 夹带、Windows 保留设备名。
func TestSafeWriteFile_NULAndReserved(t *testing.T) {
	root := t.TempDir()
	assert.Error(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "ok.txt")+"\x00", []byte("x"), 0o644))

	// 折叠大小写那支：本来只在 Windows 生效，这里手动打开它，让 Linux CI 也能覆盖
	old := foldCase
	foldCase = true
	t.Cleanup(func() { foldCase = old })
	assert.Error(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "CON"), []byte("x"), 0o644),
		"保留设备名不能当普通文件写")
	assert.NoError(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "CONCERT.mp3"), []byte("x"), 0o644),
		"CONCERT.mp3 是正常文件名，不能被误伤")
}

// TestSafeWriteFile_OutsideRootError 错误信息要带上路径，便于运维定位——
// "outside root" 但不说是哪个路径的日志等于没有。
func TestSafeWriteFile_OutsideRootError(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "x.txt")
	err := SafeWriteFileAny([]string{root}, outside, []byte("x"), 0o644)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), filepath.Base(outside)), "错误信息应含目标路径：%v", err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}
