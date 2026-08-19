package source

// control.go：Source 控制面（可选能力）。
//
// 读取面（Source 接口）只回答“给我 hash 的字节流”；控制面回答“如何把文件
// 加入/写入这个 source”。控制面按能力拆分，不是每个 source 都必须实现。
// 发现背景：2026-08-19 控制面设计（doc/source-control.md）——用户需要
// local 能添加本地文件/直接写文件，BT 能下载 torrent，IPFS 能 serve/pin。

import (
	"errors"
	"io"
)

// ErrControlUnsupported 表示当前 source 没有实现对应控制能力。
var ErrControlUnsupported = errors.New("source does not support this control operation")

// LocalControl 本地文件源的控制能力。
type LocalControl interface {
	// AddLocalFile 把本地已有文件加入 source：计算 hash、登记索引。
	AddLocalFile(path string) (*FileMeta, error)

	// WriteFile 直接写文件：从 reader 写入，完成后计算 hash 并登记。
	WriteFile(name string, r io.Reader) (*FileMeta, error)
}

// LocalControlOf 返回 source 的 LocalControl 实现；不支持时 ok=false。
func LocalControlOf(s Source) (LocalControl, bool) {
	lc, ok := s.(LocalControl)
	return lc, ok
}
