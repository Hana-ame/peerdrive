package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Apply 把资产应用到当前二进制：下载到同目录临时文件 → 校验 sha256 →
// 两步原子替换（旧版留 .bak）。任何一步失败都不动原二进制。
//
// 安全链（顺序固定，缺一不可）：
//
//	下载到临时文件（与当前二进制同目录，保证 rename 同文件系统）
//	→ sha256 校验（与 latest.json / checksums.txt 同源的值）
//	→ 替换前备份：current → current.bak
//	→ 替换：tmp → current
//	→ 替换后复验 sha256；失败则回滚（current → current.failed，bak → current）
//
// 说明：不在进程内重启自己——替换完成后由调用方提示操作者重启
// （服务器二进制在 systemd/docker 监督下，进程内 re-exec 会跟监督者打架）。
func Apply(ctx context.Context, asset *Asset) error {
	current, err := executablePath()
	if err != nil {
		return err
	}
	return applyTo(ctx, asset, current)
}

// executablePath 返回当前二进制的真实路径（跟随符号链接）。
// 为什么跟随：/usr/local/bin/peerdrive 常是指向实际安装位置的软链，
// 不解析的话 rename 会替换软链本身而不是目标文件。
func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// applyTo 是 Apply 的可测内芯：current 显式传入（测试用临时文件）。
func applyTo(ctx context.Context, asset *Asset, current string) error {
	dir := filepath.Dir(current)

	// 1. 下载到同目录临时文件。
	tmp, err := os.CreateTemp(dir, ".peerdrive-update-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { tmp.Close(); os.Remove(tmpPath) }

	if err := downloadAsset(ctx, asset.URL, tmp); err != nil {
		cleanup()
		return fmt.Errorf("download %s: %w", asset.URL, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	// 2. 替换前校验：与清单 sha256 不一致 → 丢弃临时文件，原二进制不动。
	if err := VerifySHA256(tmpPath, asset.SHA256); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("verify downloaded asset: %w", err)
	}

	// 3. 保留原文件的权限位（下载的临时文件是 0600，替换后权限不能缩水）。
	if st, err := os.Stat(current); err == nil {
		_ = os.Chmod(tmpPath, st.Mode().Perm())
	}

	// 4. 两步原子替换 + 失败回滚（swap 内部完成）。
	if err := swap(tmpPath, current); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// 5. 替换后复验：rename 完成后文件内容应与清单一致（防御性兜底）。
	if err := VerifySHA256(current, asset.SHA256); err != nil {
		_ = rollbackAfterSwap(current, asset.SHA256)
		return fmt.Errorf("post-swap verify failed, rolled back: %w", err)
	}
	return nil
}

// rename 是 os.Rename 的注入点（测试模拟失败分支用）。
var rename = os.Rename

// swap 把 tmp 原子替换到 current：
//
//	current → current.bak （备份旧版；Windows 上运行中二进制允许 rename、
//	不允许覆盖，所以必须先挪走再放新文件）
//	tmp → current
//
// 第二步失败时把 bak 挪回 current（回滚），保证任何时刻磁盘上都有一份
// 可用的二进制。
func swap(tmp, current string) error {
	bak := current + ".bak"
	if err := rename(current, bak); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	if err := rename(tmp, current); err != nil {
		// 回滚：把备份挪回原位，丢弃临时文件。
		if rbErr := rename(bak, current); rbErr != nil {
			return fmt.Errorf("replace binary failed (%v) and rollback failed (%v); backup kept at %s",
				err, rbErr, bak)
		}
		os.Remove(tmp)
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

// rollbackAfterSwap 在替换后复验失败时把现场恢复：当前损坏文件挪到
// current.failed 留证，备份挪回 current。
func rollbackAfterSwap(current, wantSHA256 string) error {
	failed := current + ".failed"
	if err := rename(current, failed); err != nil {
		return err
	}
	return rename(current+".bak", current)
}
