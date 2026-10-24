// Package tray 给 peerdrive 提供系统托盘外壳（图标 + 菜单 + 消息循环）。
//
// 定位：托盘是**平台差异**，不是业务。业务侧（cli）只看到 Config/MenuItem
// 这两个结构和 Available/Run/Balloon/Quit 四个入口；「窗口怎么建、
// Shell_NotifyIcon 怎么调、菜单怎么弹」全部关在本包内，且只在 Windows
// 编译进二进制（tray_windows.go 的 build tag；其余平台是零依赖的空实现
// tray_other.go，链接期不带任何 Win32 代码）。
//
// 设计约束（对应「各个 build 的入口要最小引用」）：
//   - 不引第三方 systray 库：x/sys/windows 的 LazyDLL 直调足够，
//     不为一个托盘把 CGO/GTK 拖进依赖图（Linux 上空实现保持零依赖）；
//   - 不认识 peerdrive：菜单文字、URL、更新检查全由调用方装配，
//     本包只做「菜单项 → 回调」的机械分发；
//   - 消息循环必须待在创建窗口的线程上：Run 设计为占住主 goroutine，
//     调用方把 HTTP 服务放 goroutine（见 serverapp.RunServeOnReady）。
package tray

// MenuItem 是托盘右键菜单里的一项。Separator 为真时画一条分隔线，
// Label/Action 忽略。
type MenuItem struct {
	Label     string
	Action    func()
	Separator bool
}

// Config 描述一个托盘实例。
type Config struct {
	// Tooltip 是鼠标悬停时的提示文字（Windows 上限 127 个字符，超出截断）。
	Tooltip string
	// Icon 是 PNG 字节串；为空时用内嵌的 peerdrive 图标。
	// Windows 侧会包成 ICO 再 LoadImage——PNG-in-ICO 是 Vista+ 合法格式。
	Icon []byte
	// OnOpen 是左键单击（或「打开」默认动作）的回调，通常打开面板。
	OnOpen func()
	// Items 是右键菜单，自上而下顺序即声明顺序。
	Items []MenuItem
}
