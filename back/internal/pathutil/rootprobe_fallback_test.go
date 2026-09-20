package pathutil

// 降级路径本身的测试。
//
// 为什么要单独摆一个文件：这段装配了 openRootFn 接缝，动了它就必须整机重建，
// 放在主测试文件里会让"读的人分不清哪些用例是纯逻辑、哪些动过全局状态"。

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRootUnavailable_Classification 分类器本身：语义不同的 errno 不能被归成一类。
func TestRootUnavailable_Classification(t *testing.T) {
	assert.False(t, RootUnavailable(nil))
	assert.False(t, RootUnavailable(os.ErrNotExist))
	assert.False(t, RootUnavailable(os.ErrPermission))
	assert.False(t, RootUnavailable(os.ErrClosed))
	assert.True(t, RootUnavailable(os.ErrInvalid), "EINVAL 在 Linux 上就是 openat2 flag 不被识别的返回")
	assert.True(t, RootUnavailable(os.NewSyscallError("openat2", os.ErrInvalid)), "套了 SyscallError 也要认出来")
	assert.True(t, RootUnavailable(&os.PathError{Op: "openat2", Path: "/mnt/x", Err: os.ErrInvalid}))
}

// TestRootFallback_FailClosedThenWorks 降级路径必须真的能用——不是"记下来了"就行。
//
// 用 openRootFn 接缝模拟"文件系统不支持"（本机上真造不出这种环境），验证三点：
// 默认拦住并给出下一步、显式开阀后操作真的完成、降级状态可观测。
func TestRootFallback_FailClosedThenWorks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a", "b.txt")

	orig := openRootFn
	t.Cleanup(func() { openRootFn = orig })
	// Linux 上两种真实形态：内核 <5.6 没有 openat2 → ENOSYS；flag 不识别 → EINVAL。
	// 这里用 EINVAL，因为它能通过 errors.Is(err, os.ErrInvalid) 严格判定。
	unsupported := os.NewSyscallError("openat2", os.ErrInvalid)
	openRootFn = func(string) (*os.Root, error) { return nil, unsupported }
	require.True(t, RootUnavailable(unsupported), "前置条件：这个 errno 要被认成'不支持'")

	// 1) 默认 fail closed，且错误信息指明下一步
	err := SafeWriteFileAny([]string{root}, target, []byte("x"), 0o644)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "文件系统不支持")
	assert.Contains(t, err.Error(), "PEERDRIVE_ROOT_FALLBACK=1", "要告诉运维怎么接着往下走")
	_, sterr := os.Stat(target)
	assert.True(t, os.IsNotExist(sterr), "fail closed 时不能写出任何东西")

	// 2) 显式开阀后操作应当真的完成（父目录也要补出来）
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "1")
	require.NoError(t, SafeWriteFileAny([]string{root}, target, []byte("payload"), 0o644))
	b, rerr := os.ReadFile(target)
	require.NoError(t, rerr)
	assert.Equal(t, "payload", string(b))

	// 读回去也要能读（降级的另一端）
	f, oerr := SafeOpen(root, target)
	require.NoError(t, oerr)
	require.NoError(t, f.Close())

	// 删除同样可用
	require.NoError(t, SafeRemoveAny([]string{root}, target))
	_, sterr2 := os.Stat(target)
	assert.True(t, os.IsNotExist(sterr2))
}

// TestRootFallback_MissingRootIsCreatedNotDegraded 目录还不存在 ≠ 文件系统不支持：
// 首次运行不该被降级（更不该被误报成不安全），而是把目录建出来、继续用 Root。
func TestRootFallback_MissingRootIsCreatedNotDegraded(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created-yet")
	t.Setenv("PEERDRIVE_ROOT_FALLBACK", "1") // 就算开着阀也不能顺手降级

	require.NoError(t, SafeWriteFileAny([]string{root}, filepath.Join(root, "x.txt"), []byte("ok"), 0o644))
	b, err := os.ReadFile(filepath.Join(root, "x.txt"))
	require.NoError(t, err)
	assert.Equal(t, "ok", string(b))
}
