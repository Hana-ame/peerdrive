package pathutil

// SafeOpen —— 把「校验路径」和「打开文件」合成一步，消掉 TOCTOU 窗口。
//
// 之前所有读取点都是两步走：
//
//	Within(root, path) → 通过 → os.Open(path)
//
// 中间隔着一个 EvalSymlinks，攻击者可以在校验之后、打开之前把路径里的某个
// 成分换成软链（共享目录里能写文件的人就能这么干）。要堵住它，不能靠"再检查
// 一遍"，只能让**解析与打开由内核一次完成**——这就是 Go 1.24 的 os.Root：
// 它在 Linux 上走 openat2(RESOLVE_BENEATH)，其它平台走等价的目录句柄 + 逐段
// O_NOFOLLOW，任何会逃出根目录的软链在 open 时就失败，没有窗口。
//
// 为什么不直接 Root.Open(rel) 就完事：实测（WSL2 / kernel 5.15）os.Root 对
// **目标写成绝对路径的根内软链**也会拒绝（Go 无法在不逃逸的前提下验证绝对
// 目标）。那样会误伤"共享目录里用绝对软链组织媒体库"的正常用法。所以这里
// 先用 normalize（Abs+Clean+EvalSymlinks）把路径化成不含软链的规范形式，
// 再交给 os.Root —— 规范化后的 rel 已无软链成分，Root 能接受；而规范化本身
// 已经确认过它落在根内。
//
// 这样剩下的唯一"窗口"是：攻击者在我们 normalize 之后、Root.Open 之前把
// 某个目录成分换成软链。但 Root.Open 自己还会逐段再确认一次，换掉就打不开，
// 打不开我们就返回错误——不会打开到别的文件上。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot 路径不在允许的根目录之内。
var ErrOutsideRoot = errors.New("path outside allowed root")

// SafeOpen 以只读方式打开 root 之内的 path。
//
// 返回的 *os.File 可以直接用：os.Root 关掉之后，已经打开的 fd 依然有效
// （实测可读），所以调用方不必持有 Root。
func SafeOpen(root, path string) (*os.File, error) {
	cr, ok := normalize(root)
	if !ok {
		return nil, fmt.Errorf("%w: invalid root %q", ErrOutsideRoot, root)
	}
	cp, ok := normalize(path)
	if !ok {
		return nil, fmt.Errorf("%w: invalid path %q", ErrOutsideRoot, path)
	}
	rel, err := filepath.Rel(cr, cp)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	if rel == "" {
		rel = "."
	}

	ops, err := openScoped(cr)
	if err != nil {
		return nil, err
	}
	defer ops.Close()

	f, err := ops.open(rel)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// SafeOpenAny 在 roots 中**第一个**包含 path 的根内打开它。
// 与 WithinAny 的语义一致（命中任意一个即算通过）。
func SafeOpenAny(roots []string, path string) (*os.File, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: no allowed root configured", ErrOutsideRoot)
	}
	var last error
	for _, r := range roots {
		f, err := SafeOpen(r, path)
		if err == nil {
			return f, nil
		}
		if last == nil {
			last = err
		}
	}
	if last == nil {
		last = ErrOutsideRoot
	}
	return nil, fmt.Errorf("%w: %s (checked %d roots)", ErrOutsideRoot, path, len(roots))
}
