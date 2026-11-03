package iface

import "errors"

// 契约层的哨兵错误。调用方只 import interface/ 就能判断失败类型，
// 不必 import metadata——那会把 SQLite 驱动一起链进二进制。
//
// 约定两条：判断一律用标准库的 errors.Is，实现侧一律用
// fmt.Errorf("...: %w", ErrXxx) 包装，别拼字符串给调用方猜。
var (
	// ErrNotFound 表示按内容哈希或按路径都没查到已登记的条目。
	ErrNotFound = errors.New("peerdrive: not found")

	// ErrInvalidHash 表示传入的不是一个 64 位十六进制的 SHA256。
	ErrInvalidHash = errors.New("peerdrive: invalid sha256")

	// ErrInvalidPath 表示路径本身就不成立：空串、无法转绝对路径这类。
	ErrInvalidPath = errors.New("peerdrive: invalid path")

	// ErrUnsupportedType 表示路径存在，但不是普通文件也不是目录。
	// 符号链接、socket、设备节点都落在这里——它们没有自己的内容可寻址。
	ErrUnsupportedType = errors.New("peerdrive: unsupported file type")
)
