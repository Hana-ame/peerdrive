//go:build windows

package tray

// Windows 托盘实现：隐藏窗口 + Shell_NotifyIconW + 弹出菜单。
//
// 为什么不用现成的 systray 库：x/sys/windows 早已不包装 user32 的
// 窗口/菜单/托盘 API（v0.49 起只剩 LazyDLL/NewCallback/StringToUTF16 这些
// 基元），而第三方 systray 库要么拖 CGO、要么拖 GTK——与「linux 入口零托盘
// 零依赖」的诉求正面冲突。这里用 LazyDLL 直调，全部代码不到 400 行，
// 无新增模块依赖（x/sys 本就是传递依赖，见 go.mod）。
//
// 线程模型（重要）：Run 必须占住**创建了窗口的那个 goroutine**——消息循环
// 与窗口同线程，菜单动作、TrackPopupMenuEx 的返回都在这个线程上跑。
// 其他 goroutine 只能通过 Balloon/Quit 这两个线程安全入口递消息进来。

import (
	_ "embed" // 仅为 //go:embed assets/icon.png 服务（[]byte 需匿名导入）
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

//go:embed assets/icon.png
var defaultIconPNG []byte

// ---------- Win32 绑定（LazyDLL 直调） ----------

var (
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW     = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procPostMessageW        = modUser32.NewProc("PostMessageW")
	procPostThreadMessageW  = modUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId  = modKernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandleW    = modKernel32.NewProc("GetModuleHandleW")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW         = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenuEx    = modUser32.NewProc("TrackPopupMenuEx")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procDestroyMenu         = modUser32.NewProc("DestroyMenu")
	procDestroyWindow       = modUser32.NewProc("DestroyWindow")
	procLoadImageW          = modUser32.NewProc("LoadImageW")

	procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
)

// ---------- 常量（值取自 Win32 SDK，均为稳定 ABI） ----------

const (
	wmNull      = 0x0000
	wmDestroy   = 0x0002
	wmQuit      = 0x0012
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmApp       = 0x8000

	// msgTray 是托盘图标回调消息；msgBalloon 是跨线程气球提示的搬运消息。
	msgTray    = wmApp + 1
	msgBalloon = wmApp + 2

	nimAdd        = 0x00000000
	nimModify     = 0x00000001
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	// NOTIFYICON_VERSION_4：Win7 起支持的气球行为（不抢焦点、可超时自动消失）。
	notifyiconVersion = 4

	imageIcon      = 1
	lrLoadFromFile = 0x00000010
	lrDefaultSize  = 0x00000040

	mfString    = 0x00000000
	mfSeparator = 0x00000800

	tpmReturnCmd   = 0x0100
	tpmRightButton = 0x0002

	niifInfo = 0x00000001 // 气球用小信息图标

	idiApplication = 32512 // MAKEINTRESOURCE(IDI_APPLICATION)，PNG 失败时的兜底

	trayWindowClass = "PeerdriveTrayWindow"
	trayIconID      = 1
)

// ---------- 最小 Win32 结构体（布局按 x64 C ABI 手工对齐） ----------

// wndClassExW 对应 WNDCLASSEXW（x64 下 80 字节）。
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// winMsg 对应 MSG（x64 下 48 字节）。显式补两个 pad 对齐 C 布局。
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	Pad0    uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pad1    uint32
	Pt      winPoint
}

type winPoint struct {
	X, Y int32
}

// notifyIconDataW 对应 NOTIFYICONDATAW（x64 下 976 字节）。
// 字段顺序与 pad 必须与 C 结构逐一对应——cbSize 用 unsafe.Sizeof 自证。
type notifyIconDataW struct {
	cbSize           uint32
	pad0             uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	pad1             uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	pad2             uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

// ---------- 进程级托盘状态（单托盘，一个进程一个实例） ----------

var (
	stateMu       sync.Mutex
	gHwnd         uintptr
	gLoopThreadID uint32
	gCfg          *Config

	// 跨线程递进来的气球内容；由消息循环线程取出并展示。
	pendingTitle, pendingBody string
	pendingBalloon            bool

	// wndProcPtr 必须被包级变量持有：windows.NewCallback 返回的是
	// 函数指针，Go 侧不保留引用会被 GC 回收，届时窗口回调直接野指针。
	wndProcPtr uintptr
)

// Available 在 Windows 上恒真。
func Available() bool { return true }

// Quit 让托盘消息循环退出（从任意 goroutine 调用都安全）。
// 用 PostThreadMessage 投 WM_QUIT：菜单动作里也可以直接调，走的同一条路。
func Quit() {
	if gLoopThreadID == 0 {
		return
	}
	procPostThreadMessageW.Call(uintptr(gLoopThreadID), wmQuit, 0, 0)
}

// Balloon 弹一条系统气球提示。可从任意 goroutine 调用：
// 非循环线程的调用会被搬进消息队列，由循环线程执行 Shell_NotifyIcon。
func Balloon(title, body string) {
	stateMu.Lock()
	pendingTitle, pendingBody, pendingBalloon = title, body, true
	stateMu.Unlock()
	if hwnd := currentHwnd(); hwnd != 0 {
		procPostMessageW.Call(hwnd, msgBalloon, 0, 0)
	}
}

// Run 创建托盘并占住当前 goroutine 跑消息循环，直到用户从菜单退出。
func Run(cfg Config) error {
	if len(cfg.Icon) == 0 {
		cfg.Icon = defaultIconPNG
	}
	stateMu.Lock()
	gCfg = &cfg
	stateMu.Unlock()

	hInst, _, _ := procGetModuleHandleW.Call(0)

	clsName := utf16Z(trayWindowClass)
	wndProcPtr = windows.NewCallback(trayWndProc)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   wndProcPtr,
		hInstance:     hInst,
		lpszClassName: clsName,
	}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("tray: RegisterClassExW: %w", windows.GetLastError())
	}

	// 隐藏的普通窗口（不是 HWND_MESSAGE 消息窗）：TrackPopupMenuEx 需要
	// 一个能设前台的窗口才能正确收起菜单，消息-only 窗口在这上面有已知怪癖。
	gHwnd, _, _ = procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(clsName)),
		0, // WS_OVERLAPPED，无 WS_VISIBLE —— 从不显示
		0, 0, 0, 0,
		0, 0, hInst, 0,
	)
	if gHwnd == 0 {
		return fmt.Errorf("tray: CreateWindowExW: %w", windows.GetLastError())
	}
	// GetCurrentThreadId 返回 uintptr，先接住再收窄；线程 id 在 32 位内，
	// 且 Quit/Balloon 依赖它跨线程递消息，必须拿到真实值。
	if r, _, _ := procGetCurrentThreadId.Call(); r != 0 {
		gLoopThreadID = uint32(r)
	}

	hIcon := iconFromPNG(cfg.Icon)
	if !addIcon(gHwnd, hIcon, cfg.Tooltip) {
		procDestroyWindow.Call(gHwnd)
		gHwnd = 0
		return fmt.Errorf("tray: Shell_NotifyIconW(NIM_ADD): %w", windows.GetLastError())
	}
	setIconVersion(gHwnd)

	// 消息循环：GetMessageW 返回 0 = 收到 WM_QUIT（用户从托盘退出）。
	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 {
			break
		}
		if uint32(r) == 0xFFFFFFFF { // GetMessageW 出错（罕见：无效窗口句柄）
			return fmt.Errorf("tray: GetMessageW: %w", windows.GetLastError())
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	deleteIcon(gHwnd)
	return nil
}

// trayWndProc 是隐藏窗口的消息处理。
func trayWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case msgTray:
		// 托盘回调的鼠标事件码在 lParam 的低 16 位（LOWORD）。
		switch uint16(lParam) {
		case wmRButtonUp:
			showMenu(hwnd)
		case wmLButtonUp:
			stateMu.Lock()
			cfg := gCfg
			stateMu.Unlock()
			if cfg != nil && cfg.OnOpen != nil {
				cfg.OnOpen()
			}
		}
		return 0
	case msgBalloon:
		stateMu.Lock()
		title, body := pendingTitle, pendingBody
		pendingBalloon = false
		stateMu.Unlock()
		showBalloon(hwnd, title, body)
		return 0
	case wmDestroy:
		deleteIcon(hwnd)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// showMenu 在光标处弹右键菜单并同步执行选中项。
// 用 TPM_RETURNCMD：菜单是模态的，返回即得到选中 id，省掉 WM_COMMAND 中转。
func showMenu(hwnd uintptr) {
	stateMu.Lock()
	cfg := gCfg
	stateMu.Unlock()
	if cfg == nil {
		return
	}

	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)

	actions := make(map[uint16]func(), len(cfg.Items))
	var id uint16 = 1
	for _, it := range cfg.Items {
		if it.Separator {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		label := utf16Z(it.Label)
		procAppendMenuW.Call(menu, mfString, uintptr(id), uintptr(unsafe.Pointer(label)))
		actions[id] = it.Action
		id++
	}

	var pt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// MSDN 配方：弹菜单前把托盘窗口设为前台，否则菜单在部分系统上点外面不消失。
	procSetForegroundWindow.Call(hwnd)
	sel, _, _ := procTrackPopupMenuEx.Call(
		menu,
		tpmRightButton|tpmReturnCmd,
		uintptr(uint32(pt.X)), uintptr(uint32(pt.Y)),
		hwnd, 0,
	)
	// 配方第二步：补一条 WM_NULL，让上述 SetForegroundWindow 的激活状态被消费掉。
	procPostMessageW.Call(hwnd, wmNull, 0, 0)

	if sel != 0 {
		if act, ok := actions[uint16(sel)]; ok && act != nil {
			act()
		}
	}
}

// ---------- Shell_NotifyIcon 封装 ----------

func addIcon(hwnd, hIcon uintptr, tooltip string) bool {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = trayIconID
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = msgTray
	nid.hIcon = hIcon
	copyUTF16Z(nid.szTip[:], tooltip)
	return shellNotify(nimAdd, &nid)
}

func setIconVersion(hwnd uintptr) {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = trayIconID
	nid.uVersion = notifyiconVersion
	shellNotify(nimSetVersion, &nid)
}

func deleteIcon(hwnd uintptr) {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = trayIconID
	shellNotify(nimDelete, &nid)
}

func showBalloon(hwnd uintptr, title, body string) {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = trayIconID
	nid.uFlags = nifInfo
	copyUTF16Z(nid.szInfoTitle[:], title)
	copyUTF16Z(nid.szInfo[:], body)
	nid.dwInfoFlags = niifInfo
	shellNotify(nimModify, &nid)
}

func shellNotify(dwMessage uint32, nid *notifyIconDataW) bool {
	// LazyProc.Call 只收 uintptr；dwMessage 是常量窄化的 uint32，显式转宽。
	r, _, _ := procShellNotifyIconW.Call(uintptr(dwMessage), uintptr(unsafe.Pointer(nid)))
	return r != 0
}

// ---------- 图标：PNG → ICO → HICON ----------

// iconFromPNG 把 PNG 字节包成 ICO 落临时文件再 LoadImage。
// Vista+ 的 ICO 容器允许直接装 PNG 载荷（不用解码成位图），192px 由
// Shell 自行缩放；任何一步失败都退到 stock 应用图标——托盘不能没有图标，
// 但也不值得为它失败。
func iconFromPNG(png []byte) uintptr {
	if ico := wrapPNGInICO(png); ico != nil {
		tmp := filepath.Join(os.TempDir(), fmt.Sprintf("peerdrive-tray-%d.ico", os.Getpid()))
		if err := os.WriteFile(tmp, ico, 0o600); err == nil {
			defer os.Remove(tmp)
			path := utf16Z(tmp)
			if h, _, _ := procLoadImageW.Call(
				0, uintptr(unsafe.Pointer(path)),
				imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize,
			); h != 0 {
				return h
			}
		}
	}
	// 兜底：IDI_APPLICATION 系统图标。
	h, _, _ := procLoadImageW.Call(0, idiApplication, imageIcon, 0, 0, lrDefaultSize)
	return h
}

// wrapPNGInICO 生成单条目 ICO：6 字节 ICONDIR + 16 字节条目 + PNG 载荷。
// 只支持 ≤256px（字节存不下一律按 256 记 0）；peerdrive 图标 192px，正好。
func wrapPNGInICO(png []byte) []byte {
	if len(png) < 24 || string(png[1:4]) != "PNG" {
		return nil
	}
	w := binary.BigEndian.Uint32(png[16:20])
	h := binary.BigEndian.Uint32(png[20:24])
	if w == 0 || h == 0 || w > 256 || h > 256 {
		return nil
	}
	var dir [6]byte
	binary.LittleEndian.PutUint16(dir[0:2], 0) // reserved
	binary.LittleEndian.PutUint16(dir[2:4], 1) // type = icon
	binary.LittleEndian.PutUint16(dir[4:6], 1) // count
	var entry [16]byte
	entry[0] = byte(w) // 256 记 0；前面已挡住 >256
	entry[1] = byte(h)
	entry[2] = 0                                  // colorCount：truecolor
	entry[3] = 0                                  // reserved
	binary.LittleEndian.PutUint16(entry[4:6], 1)  // planes
	binary.LittleEndian.PutUint16(entry[6:8], 32) // bitCount
	binary.LittleEndian.PutUint32(entry[8:12], uint32(len(png)))
	binary.LittleEndian.PutUint32(entry[12:16], 22) // imageOffset = 6+16
	out := make([]byte, 0, 22+len(png))
	out = append(out, dir[:]...)
	out = append(out, entry[:]...)
	return append(out, png...)
}

// ---------- 小工具 ----------

func currentHwnd() uintptr {
	stateMu.Lock()
	defer stateMu.Unlock()
	return gHwnd
}

// utf16Z 返回以 NUL 结尾的 UTF-16 切片（给 Win32 宽字符 API）。
func utf16Z(s string) *uint16 {
	u, _ := windows.UTF16FromString(s)
	return &append(u, 0)[0]
}

// copyUTF16Z 把字符串拷进定长宽字符数组，超长截断并保留结尾 NUL。
// szTip/szInfo/szInfoTitle 都是定长数组，越界写会踩坏后续字段。
func copyUTF16Z(dst []uint16, s string) {
	u, _ := windows.UTF16FromString(s)
	if len(u) > len(dst)-1 {
		u = u[:len(dst)-1]
	}
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, u)
}
