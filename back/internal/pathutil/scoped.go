package pathutil

// scopedOps 把"在一个允许根之内做文件操作"包成一个统一的面：能用 os.Root 就用，
// 用不了（且运营者显式开了逃生阀）就退回"绝对路径 + os.*"。
//
// 为什么要有这一层：以前每个调用点自己 if-else，写 E1 处改了、E2 处忘了改。
// 降级逻辑只有一处、正确性由这一个类型负责，调用点永远写 `ops.WriteFile(rel, …)`。

import (
	"os"
	"path/filepath"
)

type scopedOps struct {
	root *os.Root // nil = 已降级到按路径操作
	base string   // 降级时用：base/rel 就是完整路径
}

// openScoped 在一个允许根上建立操作句柄。
func openScoped(root string) (scopedOps, error) {
	r, err := openRootOrFallback(root)
	if err != nil {
		return scopedOps{}, err
	}
	return scopedOps{root: r, base: root}, nil
}

func (o scopedOps) degraded() bool { return o.root == nil }

// full 降级模式下的完整路径（WriteFile/Remove 等最后一个参数是路径不是句柄）。
func (o scopedOps) full(rel string) string {
	if rel == "." {
		return o.base
	}
	return filepath.Join(o.base, rel)
}

func (o scopedOps) Close() {
	if o.root != nil {
		_ = o.root.Close()
	}
}

func (o scopedOps) open(rel string) (*os.File, error) {
	if o.root != nil {
		return o.root.Open(rel)
	}
	return os.Open(o.full(rel))
}

func (o scopedOps) openFile(rel string, flag int, perm os.FileMode) (*os.File, error) {
	if o.root != nil {
		return o.root.OpenFile(rel, flag, perm)
	}
	return os.OpenFile(o.full(rel), flag, perm)
}

func (o scopedOps) writeFile(rel string, data []byte, perm os.FileMode) error {
	if o.root != nil {
		return o.root.WriteFile(rel, data, perm)
	}
	return os.WriteFile(o.full(rel), data, perm)
}

func (o scopedOps) mkdirAll(rel string, perm os.FileMode) error {
	if o.root != nil {
		return o.root.MkdirAll(rel, perm)
	}
	return os.MkdirAll(o.full(rel), perm)
}

func (o scopedOps) remove(rel string) error {
	if o.root != nil {
		return o.root.Remove(rel)
	}
	return os.Remove(o.full(rel))
}

func (o scopedOps) removeAll(rel string) error {
	if o.root != nil {
		return o.root.RemoveAll(rel)
	}
	return os.RemoveAll(o.full(rel))
}

// Degraded 是否处在降级模式：此刻这些操作**没有** TOCTOU 保护（路径会被二次
// 解析）。调用方若要在日志里说明状态、或拒绝某些高风险动作，用它判断。
func (o scopedOps) Degraded() bool { return o.degraded() }
