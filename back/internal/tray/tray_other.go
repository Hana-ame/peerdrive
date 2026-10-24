//go:build !windows

package tray

// 非 Windows 平台：没有系统托盘，全部入口退化为零依赖空实现。
//
// 这不是「以后再说」的占位——是刻意的设计：Linux/macOS 入口（cmd/peerdrive）
// 不该为一个用不上的托盘把任何 Win32 代码、CGO 或 GTK 拖进链接图。
// cli 层用 Available() 把托盘路径整个短路，Run 永远走不到。

// Available 在非 Windows 平台恒假。
func Available() bool { return false }

// Run 在非 Windows 平台不会被调用（调用方先查 Available）。
// 直接返回 nil 而不是挂起：万一有调用方漏查，行为降级为「没有托盘」，
// 而不是把用户的服务挂死在一个永远等不到退出的循环上。
func Run(cfg Config) error { return nil }

// Quit 无事可做。
func Quit() {}

// Balloon 无事可做（调用方在非 Windows 平台不应弹托盘提示）。
func Balloon(title, body string) {}
