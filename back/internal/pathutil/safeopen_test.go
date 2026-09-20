package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeOpen_Inside(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "sub", "a.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("hello"), 0o644))

	f, err := SafeOpen(root, p)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 8)
	n, err := f.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(buf[:n]))
}

func TestSafeOpen_RejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("top secret"), 0o600))

	for name, p := range map[string]string{
		"向上逃逸":   filepath.Join(root, "..", filepath.Base(outside), "secret.txt"),
		"根外绝对路径": secret,
		"系统文件":   "/etc/passwd",
		"NUL":    filepath.Join(root, "a.txt") + "\x00",
		"空路径":    "",
		"空根":     "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SafeOpen(root, p)
			assert.Error(t, err, "SafeOpen 必须拒绝：%q", p)
		})
	}

	t.Run("空根", func(t *testing.T) {
		_, err := SafeOpen("", filepath.Join(root, "a.txt"))
		assert.Error(t, err, "空 root 必须拒绝")
	})
}

// TestSafeOpen_AllowsInnerAbsoluteSymlink 这一条是 SafeOpen 存在的理由之一。
//
// 直接用 os.Root.Open 会把"目标写成绝对路径的根内软链"也拒掉（Go 无法在不逃逸
// 的前提下验证绝对目标），那会误伤"共享目录里用绝对软链组织媒体库"的正常用法。
// SafeOpen 先规范化成不含软链的路径再交给 os.Root，所以这种用法照常可用。
func TestSafeOpen_AllowsInnerAbsoluteSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	root := t.TempDir()
	real := filepath.Join(root, "real.txt")
	require.NoError(t, os.WriteFile(real, []byte("REAL"), 0o644))

	abs := filepath.Join(root, "abs.txt")
	require.NoError(t, os.Symlink(real, abs))
	rel := filepath.Join(root, "rel.txt")
	require.NoError(t, os.Symlink("real.txt", rel))

	for _, p := range []string{abs, rel, real} {
		f, err := SafeOpen(root, p)
		require.NoError(t, err, "根内软链必须能打开：%s", p)
		buf := make([]byte, 8)
		n, _ := f.Read(buf)
		f.Close()
		assert.Equal(t, "REAL", string(buf[:n]), "打开了但不是同一个文件：%s", p)
	}
}

// TestSafeOpen_ToctouSwap 这条用例的价值在于**先证明窗口真的存在**。
//
// 攻击形态：不是直接给一个越权路径（那种 IsPathAllowed 就拦了），而是
// 先给一个合法路径让它通过校验，然后在"校验之后、打开之前"把路径里的某个
// **目录成分**换成软链。两步走的旧写法（Within → os.Open）会中招。
func TestSafeOpen_ToctouSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "a.txt"), []byte("top secret"), 0o600))

	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "a.txt"), []byte("benign"), 0o644))
	target := filepath.Join(sub, "a.txt")

	// 1) 校验阶段：路径完全合法
	require.True(t, Within(root, target), "前置条件：换掉之前这个路径是合法的")

	// 2) 攻击者在校验之后把 sub 换成指向外部的软链
	require.NoError(t, os.RemoveAll(sub))
	require.NoError(t, os.Symlink(outside, sub))

	// 3) 证明窗口真实存在：旧的 os.Open 会读到外部文件
	if f, err := os.Open(target); err == nil {
		buf := make([]byte, 32)
		n, _ := f.Read(buf)
		f.Close()
		require.Equal(t, "top secret", string(buf[:n]),
			"前置条件：os.Open 确实会读到外部文件（这就是 TOCTOU 窗口）")
	}

	// 4) SafeOpen 必须拒绝：它在打开那一刻由内核重新判定
	_, err := SafeOpen(root, target)
	assert.Error(t, err, "成分被换成软链之后 SafeOpen 必须拒绝（os.Open 会中招）")
}

func TestSafeOpenAny(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	outside := t.TempDir()

	pb := filepath.Join(b, "x.txt")
	require.NoError(t, os.WriteFile(pb, []byte("B"), 0o644))
	po := filepath.Join(outside, "y.txt")
	require.NoError(t, os.WriteFile(po, []byte("O"), 0o644))

	f, err := SafeOpenAny([]string{a, b}, pb)
	require.NoError(t, err, "落在第二个根内应能打开")
	f.Close()

	_, err = SafeOpenAny([]string{a, b}, po)
	assert.Error(t, err, "都不在应拒绝")

	_, err = SafeOpenAny(nil, pb)
	assert.Error(t, err, "没有根时应拒绝")
}
