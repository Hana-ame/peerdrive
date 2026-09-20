package pathutil

// os.Root 建立失败时的处理（doc/NETDISK.md §11.5 第 4 条）。
//
// 要保证的不是"永远不会失败"，而是三件事：
//  1. 失败**能分辨出病因**（不支持 vs 不存在 vs 没权限）——方向错了会白查半天；
//  2. 默认 fail closed，不悄悄放行；
//  3. 运营者显式开阀时，降级是一条明确、可观测、且真的能用的路径。

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeRootSupport_NormalDir(t *testing.T) {
	assert.NoError(t, ProbeRootSupport(t.TempDir()), "普通目录必须支持 Root")
}

func TestProbeRootSupport_MissingDirIsNotUnsupported(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created")
	err := ProbeRootSupport(missing)
	require.Error(t, err)
	assert.False(t, RootUnavailable(err), "目录不存在 ≠ 文件系统不支持：排查方向不能指错")
	assert.Contains(t, ExplainRootFailure(missing, err), "不存在")
}

func TestProbeRootSupport_FileNotRoot(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o600))
	err := ProbeRootSupport(f)
	require.Error(t, err, "把普通文件当根目录配是个配置错误，必须有反馈")
	assert.False(t, RootUnavailable(err), "这是配置错误，不是文件系统不支持")
}

func TestExplainRootFailure_Permission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 用户看不到权限拒绝")
	}
	if runtime.GOOS == "windows" {
		// Windows 上 os.Chmod 只改 FILE_ATTRIBUTE_READONLY 那一位，owner 照样能
		// 打开这个目录，造不出 EACCES（要造得真的去改 ACL）。所以这个具体分支
		// 不在 Windows 上验证——Windows 侧"不该被误判成不支持"由
		// TestProbeRootSupport_* 那几个反向用例覆盖。
		t.Skip("Windows 的 chmod 造不出 EACCES")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := ProbeRootSupport(dir)
	require.Error(t, err)
	assert.Contains(t, ExplainRootFailure(dir, err), "权限", "perm 错误要指向 ACL/owner，而不是笼统报错")
}

// TestRootFallback_OffByDefault 默认必须 fail closed：这是这条防线存在的意义。
// 开了阀才降级，而且阀是显式环境变量，不是"内部悄悄判断一下"。
func TestRootFallback_OffByDefault(t *testing.T) {
	assert.False(t, RootFallbackEnabled(), "默认不能降级")
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "1")
	assert.True(t, RootFallbackEnabled())
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "")
	assert.False(t, RootFallbackEnabled())
}
