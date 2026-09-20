package pathutil

// 写路径也得 Root 化——之前只有读取侧（SafeOpen）消除了 TOCTOU，
// copy/delete/ upload 落盘那些地方还是经典的两步走：
//
//	Within(root, path) → 通过 → os.WriteFile(path) / os.Remove(path)
//
// 校验与落盘之间隔着一次路径解析：共享目录里能写东西的人，可以在这两步之间把
// 路径里的某个**目录成分**换成软链（或直接预先放一个指向外面的软链），把写/
// 删引导到根之外。os.WriteFile 和 os.Remove 都会老老实实地跟着软链走。
//
// 这里用同一个办法解决：先在**允许根**上开一个 os.Root，再让它把 rel 解析并
// 落盘，一次完成。os.Root 在 Linux 上走 openat2(RESOLVE_BENEATH)，其它平台走
// 目录句柄 + 逐段确认，任何指向根外的软链在解析时就失败。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pickRoot 挑出第一个真正包含 path 的根，返回该根与 path 相对它的 rel。
//
// 与 normalize 不同，这里**故意不解析软链、也不折叠大小写**：
//   - 解析交给 os.Root 自己做才是不受 TOCTOU 影响的那一层；我们这里再 stat 一遍
//     等于重开一个窗口（且目标文件此时可能还不存在，EvalSymlinks 必然失败）；
//   - 大小写折叠会把 Upper.Foo 变成 upper.foo 落盘，Windows 上等于悄悄改名。
//
// 字符串层面的 defence in depth 仍然要有（NUL、保留设备名、.. 逃逸、绝对路径）。
func pickRoot(roots []string, path string) (string, string, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return "", "", fmt.Errorf("%w: invalid path (NUL byte)", ErrOutsideRoot)
	}
	// Windows 保留设备名（`CON`/`NUL`/`COM1`…）：写它们会直接落到设备上。
	if foldCase && hasReservedNameIn(path) {
		return "", "", fmt.Errorf("%w: reserved device name: %s", ErrOutsideRoot, path)
	}
	if len(roots) == 0 {
		return "", "", fmt.Errorf("%w: no allowed root configured", ErrOutsideRoot)
	}
	cp, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	cp = filepath.Clean(cp)
	if foldCase {
		// 与 normalize 保持一致：Windows 上先还原 8.3 短名。判定（Within）按长名
		// 比较、而这里不还原的话，两边对同一目录会给出不同的 rel。
		cp = filepath.Clean(ExpandShortNames(cp))
	}

	sep := string(filepath.Separator)
	for _, r := range roots {
		if strings.TrimSpace(r) == "" || strings.IndexByte(r, 0) >= 0 {
			continue
		}
		cr, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		cr = filepath.Clean(cr)
		if foldCase {
			// 与 cp 用同一套写法：允许根自己也可能是 8.3 短名（GitHub 的 Windows
			// runner 上 t.TempDir() 就是 `C:\Users\RUNNER~1\...`）。只还原 path
			// 不还原 root，两边会被算成两棵树 → 明明在根内的写被判越权
			//（CI 上 windows 那格第一次真跑就红了三个用例）。
			cr = filepath.Clean(ExpandShortNames(cr))
		}
		rel, err := filepath.Rel(cr, cp)
		if err != nil {
			continue // 跨盘/无法求相对路径：换下一个根
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+sep) {
			continue
		}
		if filepath.IsAbs(rel) || rel == "" {
			continue
		}
		return cr, rel, nil
	}
	return "", "", fmt.Errorf("%w: %s (checked %d roots)", ErrOutsideRoot, path, len(roots))
}

// withRoot 在一个有效的允许根之上执行 fn（rel 已保证没有 .. 逃逸）。
//
// 支持 os.Root 就用 os.Root；文件系统不支持且运营者显式开了逃生阀时退回按路径
// 操作（见 rootprobe.go）。这两种模式的每一次操作都走 scopedOps，调用点不必分支。
func withRoot(roots []string, path string, fn func(ops scopedOps, rel string) error) error {
	cr, rel, err := pickRoot(roots, path)
	if err != nil {
		return err
	}
	ops, err := openScoped(cr)
	if err != nil {
		return err
	}
	defer ops.Close()
	if err := fn(ops, rel); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// mkdirParent 补齐 rel 的父目录。
func mkdirParent(ops scopedOps, rel string, perm os.FileMode) error {
	parent := filepath.Dir(rel)
	if parent == "." || parent == string(filepath.Separator) {
		return nil
	}
	return ops.mkdirAll(parent, perm)
}

// rejectSelf 拒绝以"根目录自身"为操作对象：rel == "." 意味着目标是允许根本身，
// 删掉/覆盖掉它没有合法用途，且必须是显式的一条规则而不是靠 caller 记得。
func rejectSelf(rel string) error {
	if rel == "." {
		return fmt.Errorf("%w: refusing to operate on the root directory itself", ErrOutsideRoot)
	}
	return nil
}

// SafeWriteFileAny 在允许根内写文件（父目录自动补齐，0644 之类由调用方给）。
// 不要用 os.WriteFile 替代：它会跟着软链走到根外。
func SafeWriteFileAny(roots []string, path string, data []byte, perm os.FileMode) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		if err := mkdirParent(ops, rel, 0o755); err != nil {
			return err
		}
		return ops.writeFile(rel, data, perm)
	})
}

// SafeOpenFileAny 在允许根内按任意 flag 打开文件（写/追加/分片续传都用它）。
// 带 O_CREATE 时自动补齐父目录——省一次 MkdirAll 调用就是少一个窗口。
//
// 返回的 *os.File 在 Root 关闭之后依然有效（打开动作已经完成）。
func SafeOpenFileAny(roots []string, path string, flag int, perm os.FileMode) (*os.File, error) {
	var f *os.File
	err := withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		if flag&os.O_CREATE != 0 {
			if err := mkdirParent(ops, rel, 0o755); err != nil {
				return err
			}
		}
		var err error
		f, err = ops.openFile(rel, flag, perm)
		return err
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// SafeMkdirAllAny 在允许根内建目录。
func SafeMkdirAllAny(roots []string, path string, perm os.FileMode) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		return ops.mkdirAll(rel, perm)
	})
}

// SafeRemoveAny 在允许根内删除单个文件（或空目录）。
//
// 为什么重要：os.Remove 会跟着**父目录**上的软链走到根外，把"允许编辑自己目录"
// 变成"能删任意文件"。走 Root 之后，路径解析与 unlink 一次完成。
func SafeRemoveAny(roots []string, path string) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		return ops.remove(rel)
	})
}

// SafeRemoveAllAny 递归删除，边界同 SafeRemoveAny。
func SafeRemoveAllAny(roots []string, path string) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		return ops.removeAll(rel)
	})
}
