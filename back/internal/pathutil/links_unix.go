//go:build unix

package pathutil

import (
	"os"
	"syscall"
)

// Nlink 返回文件的硬链接数；取不到时返回 0（调用方按"未知"处理）。
//
// 为什么需要它：硬链接没有方向，`EvalSymlinks` 也认不出来——同一个 inode 在
// 共享目录内有一个名字、在外面还有另一个名字，从路径上**无法**判断它有没有
// 暴露出去。唯一能做的策略就是"有多个名字就拒绝"（宁可少给）。
func Nlink(fi os.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	// Nlink 在各 Unix 上位宽不同（Linux uint64 / darwin uint16），统一收成 uint64
	return uint64(st.Nlink)
}

// NlinkOf 从**已打开的句柄**取链接数（内部就是对那个 fd 做 fstat）。
//
// 为什么要有句柄版：跨平台统一的入参。Windows 上只有 GetFileInformationByHandle
// 能给 NumberOfLinks，而它要的是句柄——调用点统一传 *os.File 才能让两边都生效。
func NlinkOf(f *os.File) uint64 {
	if f == nil {
		return 0
	}
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return Nlink(fi)
}

// NlinkSupported 本平台能否拿到硬链接数。
func NlinkSupported() bool { return true }
