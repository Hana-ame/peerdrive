package pathutil

// Windows 保留设备名：`CON`、`PRN`、`AUX`、`NUL`、`COM1`…`LPT9`。
//
// 这些名字在**任何目录**下都被 Win32 解释成设备，而不是那个目录里的文件：
// `C:\data\CON` 打开的是控制台，`C:\data\NUL` 是空设备——于是"路径在根内"
// 这个判定在 Windows 上被设备名绕过去了：判定说在根内（它文本上确实在），
// 打开拿到的却根本不是那个文件。登记/服务这种路径最好的结果是报错，
// 最坏的是把请求挂住（读 `CON` 会等输入）。
//
// 带扩展名也算（`CON.txt` 同样是设备）——Win32 的比较是"主干名 + 可选扩展名"。
//
// 为什么单独一个文件：语义只在 Windows 上成立，但函数本身要在 Linux CI 上
// 也能被单测（不依赖 runtime.GOOS 分支，调用方自己决定什么时候用）。
//
// 文件名**不能**叫 xxx_windows.go：那是 Go 的隐式 GOOS 约束，编 Linux 时会被
// 整个排除，函数在 Linux CI 上就消失了（只能叫 reserved.go）。

import (
	"path/filepath"
	"strings"
)

var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// HasReservedName 路径里有没有 Windows 保留设备名（逐段看主干名，忽略扩展名）。
// 非 Windows 平台上"这只是个普通文件名"，但函数照样可用——跨平台判定交给调用方。
func HasReservedName(p string) bool {
	if p == "" {
		return false
	}
	// 分隔符两种都认：`C:\data\CON` 与 `C:/data/CON` 在 Windows 上是同一个东西
	cleaned := strings.NewReplacer("\\", "/").Replace(p)
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		// 卷名 `C:` 不是设备名；`COM1:`（带冒号）是，但要单独处理
		if len(seg) == 2 && seg[1] == ':' {
			continue
		}
		stem := seg
		if i := strings.IndexByte(seg, ':'); i >= 0 {
			stem = seg[:i] // "COM1:stream" → "COM1"
		}
		if i := strings.IndexByte(stem, '.'); i >= 0 {
			stem = stem[:i] // "CON.txt" → "CON"
		}
		if reservedDeviceNames[strings.ToUpper(stem)] {
			return true
		}
	}
	return false
}

// hasReservedNameIn 供 normalize 使用：只在 Windows 上启用。
// 判定的是 cleaned 路径（Clean 之后）。
func hasReservedNameIn(p string) bool {
	return HasReservedName(filepath.Clean(p))
}
