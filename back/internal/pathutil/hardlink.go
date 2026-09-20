package pathutil

// 硬链接策略：一份判定，两处共用（transport.FileIndexService.Create 与
// service.FileService.RegisterLocal）。
//
// 为什么两处都要有：登记入口不止一个。对端经 WebRTC 调 create 会让本节点登记
// 下载根下的文件，运营者经 HTTP 调 register_local 会登记 storage/共享目录下的
// 文件——两条路都能把一个"在允许根内、但在外面还有别的名字"的文件登记进来。
// 之前只在 transport 侧判，HTTP 侧留了口子。

import (
	"fmt"
	"os"
)

// HardlinkCheckEnabled 本平台是否真的检查硬链接。
//
// 两个条件缺一不可：平台得给得出链接数，且运营者没有显式放开。
// Windows 原先恒 false（os.FileInfo 里没 nlink），改成句柄取数后与 Unix 一致。
func HardlinkCheckEnabled() bool {
	return NlinkSupported() && os.Getenv("PEERDRIVE_ALLOW_HARDLINKS") != "1"
}

// RejectHardlink 拒绝"有多个名字"的普通文件。
//
// 硬链接没有方向：同一个 inode 在允许根内有一个名字、在外面还有另一个，从
// 路径上**无法**判断它有没有被暴露出去（EvalSymlinks 也认不出来）。唯一能做
// 的就是"链接数 > 1 就拒绝"——宁可少给。
//
// 误伤场景：pnpm 的 node_modules、git alternates、`cp -l` 备份这类目录大量使用
// 硬链接，要登记它们就设 PEERDRIVE_ALLOW_HARDLINKS=1。
//
// 入参必须是**已经打开的那个 *os.File**，不是 FileInfo：Windows 上要对着句柄调
// GetFileInformationByHandle 才拿得到 NumberOfLinks，FileInfo 那条路永远给不出
// 答案（2026-09-20 之前 Windows 这条防线因此是空的）。顺带也避免了"stat 之后、
// 打开之前被换掉"的窗口。
func RejectHardlink(path string, f *os.File) error {
	if !HardlinkCheckEnabled() || f == nil {
		return nil
	}
	fi, err := f.Stat()
	if err != nil {
		return nil // 属性都取不到就谈不了链接数，按"未知"处理
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	if n := NlinkOf(f); n > 1 {
		return fmt.Errorf("%s has %d hard links: refusing to register a file that may have a name outside the allowed root (set PEERDRIVE_ALLOW_HARDLINKS=1 to allow)", path, n)
	}
	return nil
}
