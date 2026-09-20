package pathutil

// os.Root 打不开时怎么办。
//
// 现状（2026-09-20 之前的写法）：OpenRoot 失败就是返回一个 error 算了。表现是
// "那个目录共享不了"，而日志里只有一句 "open root ...: <errno>"，运维看不出是
// 文件系统不支持、还是目录不存在、还是权限不够——于是要么认为产品坏了，要么
// 去 chmod/chown 一通，白忙。
//
// 这里做两件事：
//  1. 分类 + 可操作的解释：unsupported vs missing vs permission，各自给出下一步；
//  2. 一个**显式**、默认关闭的逃生阀 PEERDRIVE_ROOT_FALLBACK=1：真到了不支持的
//     文件系统上又必须用这个目录时，运营者可以选择退回"按路径判定 + 按路径打开"
//     的老路，并在日志里持续收到告警。默认不开——宁可少给，不能默认把 TOCTOU
//     窗口开回来。

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"peerdrive/internal/log"
)

// openRootOrFallback 打开允许根；os.Root 在本文件系统上不可用时，按逃生阀决定
// 是否退回"按路径操作"。返回的 *os.Root 为 nil 表示**已降级**（此时用 base 拼
// 出完整路径走老的 os.* 函数）。
func openRootOrFallback(root string) (*os.Root, error) {
	var (
		dir *os.Root
		err error
	)
	// 根自己还没建出来（首次运行时 storageDir / uploadDir 都不存在）：建目录本来
	// 就是预期行为——边界是"不能越过这个根"，而不是"这个根得先存在"。
	if root != "" {
		if dir, err = openRootFn(root); err != nil && errors.Is(err, os.ErrNotExist) {
			if mkErr := os.MkdirAll(root, 0o755); mkErr == nil {
				dir, err = openRootFn(root)
			}
		}
	}
	if err == nil {
		return dir, nil
	}
	if RootUnavailable(err) && RootFallbackEnabled() {
		if WarnOnce("root-fallback:" + root) {
			log.LogWarn("pathutil: %s", RootFallbackWhy)
			log.LogWarn("pathutil: 降级目录=%s 底层原因=%v", root, err)
		}
		// 降级模式下没有 Root 兜着，目录得自己先建出来：后面的写都以它为父
		_ = os.MkdirAll(root, 0o755)
		return nil, nil
	}
	return nil, fmt.Errorf("%s: %w", ExplainRootFailure(root, err), err)
}

// WarnOnce 返回 true 表示这是第一次撞上这个 key（应当打日志）；重复调用不再打扰。
//
// 安全降级/不支持这类消息刷屏会把真正的问题淹没掉，也不该让每个请求都白白做
// 一次字符串格式化。
func WarnOnce(key string) bool {
	warnMu.Lock()
	defer warnMu.Unlock()
	if warned[key] {
		return false
	}
	warned[key] = true
	warnLines = append(warnLines, key)
	return true
}

// RootFallbackEnabled 运营者是否显式要求"Root 用不了就退回按路径操作"。
//
// 为什么要有：某些文件系统（部分网络文件系统、9P、老的 overlay/SMB 挂载、WSL
// 的 DrvFs 在某些内核上）做不了打开目录当根目录再 tree-scoped 解析那套；
// 那时 fail closed 的直接后果是"这个目录完全共享不了"，产品等于不可用。
// 给一个**明确知道自己在做什么**的人留个开关，比让他在暗处猜要好。
func RootFallbackEnabled() bool {
	return os.Getenv("PEERDRIVE_ROOT_FALLBACK") == "1"
}

// RootUnavailable 这个错误是不是"平台/文件系统干不了这件事"（而不是"目录不存在"
// 或"没权限"）。分清楚很重要：排查方向完全不同。
func RootUnavailable(err error) bool {
	if err == nil {
		return false
	}
	// os.Root 内部会把 errno 包成 *PathError 再往上丢，errors.Is 能穿透
	for _, target := range []error{os.ErrInvalid, errNotSupported} {
		if errors.Is(err, target) {
			return true
		}
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return RootUnavailable(pe.Err)
	}
	// openat2 不被识别时内核返回 EINVAL；完全没实现则是 ENOSYS/ENOTSUP。
	// 这里也认 Windows 的 ERROR_NOT_SUPPORTED（syscall 侧 errno 1150）。
	msg := err.Error()
	return strings.Contains(msg, "not supported") ||
		strings.Contains(msg, "operation not supported") ||
		strings.Contains(msg, "not implemented")
}

// errNotSupported 用一个可比较的哨兵承接 syscall.Errno 之外的判定，
// 免得在 RootUnavailable 里散布 magic number。
var errNotSupported = errors.New("root operations unsupported")

// ExplainRootFailure 把 OpenRoot 的失败翻译成人话 + 下一步该干嘛。
func ExplainRootFailure(root string, err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("目录 %s 不存在：创建它（或修正配置）后重启", root)
	case errors.Is(err, os.ErrPermission):
		return fmt.Sprintf("进程没有访问 %s 的权限：检查目录的 owner/ACL，不要用 chmod 777 敷衍", root)
	case RootUnavailable(err):
		extra := ""
		if !RootFallbackEnabled() {
			extra = "；若确认要在这种文件系统上用这个目录，可显式设 PEERDRIVE_ROOT_FALLBACK=1（会退回按路径判定，存在 TOCTOU 风险）"
		}
		return fmt.Sprintf("文件系统不支持目录句柄约束（os.Root），安全边界无法在该目录上强制执行%v", extra)
	default:
		return fmt.Sprintf("%v", err)
	}
}

// ProbeRootSupport 试着在一个目录上建立 os.Root；返回 nil 表示这条路走得通。
//
// 什么时候用：**启动自检**。别等到有人来拉文件才发现这个目录其实提供不出去。
func ProbeRootSupport(root string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("empty root")
	}
	r, err := openRootFn(root)
	if err != nil {
		return err
	}
	defer r.Close()
	// 打开成功还不够：有的挂载点上 openat2 能开、但后续 *_at 调用会失败。
	if _, err := r.Stat("."); err != nil {
		return err
	}
	return nil
}

// warnOnce 同一个目录只告警一次：安全降级/不支持这类消息刷屏会把真正的
// 问题淹没掉，也不该让每个请求都走一遍字符串格式化。
var (
	warnMu    sync.Mutex
	warned    = map[string]bool{}
	warnLines = []string{}
)

// openRootFn 可被单测替换的接缝。
//
// 为什么需要它：os.Root 支不支持取决于**运行机器的文件系统**，而一台支持得很
// 好的机器上根本造不出"不支持"的环境——降级这条分支就永远没被执行过，而我们
// 不该寄希望于它"看起来应该没问题"。
var openRootFn = os.OpenRoot

// RootFallbackWhy 为什么走成了降级路径（日志/错误信息里要说清楚：这是被允许的
// 降级，不是"我们没做防护"）。
const RootFallbackWhy = "PEERDRIVE_ROOT_FALLBACK=1：本文件系统无法使用 os.Root，已退回按路径判定（存在 TOCTOU 窗口，仅限信任该目录下用户的场景使用）"
