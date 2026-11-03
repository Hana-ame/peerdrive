//go:build windows

package metadata

import (
	"io/fs"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Windows 侧只用 Vista 就有的 API，所以 Windows 7 也在覆盖范围内。
// 拿不到就往下退，缺哪个字段就少哪个字段，不拿别的值顶替：
//
//	1. GetFileInformationByHandleEx(FileBasicInfo)   Vista+     四个时间全有
//	2. GetFileInformationByHandle(ByHandleFileInformation)  NT4+ 三个时间，缺 ChangeTime
//	3. Win32FileAttributeData（os.FileInfo 自带）      一直有     三个时间，缺 ChangeTime
//
// 第 1 档是唯一能拿到 ChangeTime 的路子：NTFS 维持它（任何元数据写入都会顶起），
// 因此它在 Windows 上扮演 Linux Ctime 的角色。
//
// 兼容性分两层，别混：
//
//	源码层  这里只碰 Vista 就有的 API，386 下的结构体布局也钉死了（见下面的断言）。
//	        这一层归本文件管。
//	运行时层 二进制能不能在 Win7 上启动，取决于链接进去的 Go 运行时：官方 Go 1.21.5+
//	        把熵源换成 bcryptprimitives.dll!ProcessPrng（Win8+ 才有），Win7 上启动即崩
//	        （Exception 0xc0000005, PC=0x0 in asmstdcall），代码根本没机会跑。
//	        解法是 XTLS/go-win7 的同版本补丁 SDK——随机数、控制台、文件系统调用换回
//	        Win7 有实现的版本，版本号跟着官方走，不是降级。Xray 在 release-win7.yml 里
//	        直接替换 GOROOT 这么干，ech-proxy 也是。CI 的 win7 job 用同一套。

// FILE_INFO_BY_HANDLE_CLASS::FileBasicInfo
const fileBasicInfoClass = 0

// fileBasicInfo 对应 Win32 FILE_BASIC_INFO。
//
// 末尾那个下划线不是摆设：32 位下 Go 对 int64 只做 4 字节对齐，去掉填充字段
// 结构体会缩到 36 字节，而 Windows 无论 32/64 位都按 40 字节校验缓冲区长度，
// 少了直接返回 ERROR_BAD_LENGTH——不是截断数据，是整个调用失败、时间全成零。
//
// 下面两个数组长度断言把尺寸钉死在 40：偏大偏小都编译不过。
type fileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

var _ [40 - unsafe.Sizeof(fileBasicInfo{})]byte
var _ [unsafe.Sizeof(fileBasicInfo{}) - 40]byte

var (
	modkernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetFileInformationByHandleEx = modkernel32.NewProc("GetFileInformationByHandleEx")
)

// safeCallProc 先 Find 再 Call。syscall.LazyProc.Call 找不到符号会 panic，
// 而这些符号并非每台机器都保证存在——降级总比崩掉体面。
func safeCallProc(p *syscall.LazyProc, a ...uintptr) (r1, r2 uintptr, err error) {
	if p == nil {
		return 0, 0, syscall.EINVAL
	}
	if err := p.Find(); err != nil {
		return 0, 0, err
	}
	return p.Call(a...)
}

func platformTimes(path string, fi fs.FileInfo) timestamps {
	if ts, ok := timesViaBasicInfo(path); ok {
		return ts
	}
	if ts, ok := timesViaHandle(path); ok {
		return ts
	}
	return timesViaFileInfo(fi)
}

// timesViaBasicInfo 是唯一能一次拿全四个时间的调用。
func timesViaBasicInfo(path string) (timestamps, bool) {
	// os.Open 带 FILE_FLAG_BACKUP_SEMANTICS，目录也打得开，
	// 所以文件和目录共用一条路径，不必分支。
	f, err := os.Open(path)
	if err != nil {
		return timestamps{}, false
	}
	defer f.Close()

	var info fileBasicInfo
	r, _, _ := safeCallProc(
		procGetFileInformationByHandleEx,
		f.Fd(),
		uintptr(fileBasicInfoClass),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info), // == 40，见上面的断言
	)
	if r == 0 {
		return timestamps{}, false
	}
	return timestamps{
		Ctime: filetime(info.ChangeTime),
		Mtime: filetime(info.LastWriteTime),
		Btime: filetime(info.CreationTime),
		Atime: filetime(info.LastAccessTime),
	}, true
}

// timesViaHandle 是老系统与某些网络重定向器上的退路：三个时间还在，
// ChangeTime 没有——这个字段只有 FILE_BASIC_INFO 给得出。
func timesViaHandle(path string) (timestamps, bool) {
	f, err := os.Open(path)
	if err != nil {
		return timestamps{}, false
	}
	defer f.Close()

	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &data); err != nil {
		return timestamps{}, false
	}
	return timestamps{
		Mtime: filetimeFromWin32(data.LastWriteTime),
		Btime: filetimeFromWin32(data.CreationTime),
		Atime: filetimeFromWin32(data.LastAccessTime),
	}, true
}

// timesViaFileInfo 连句柄都不用了，只剩 stat 自带的那三个。
func timesViaFileInfo(fi fs.FileInfo) timestamps {
	ts := timestamps{Mtime: fi.ModTime()}
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		ts.Atime = filetimeFromWin32(d.LastAccessTime)
		ts.Btime = filetimeFromWin32(d.CreationTime)
	}
	return ts
}

// filetime 把 FILE_BASIC_INFO 里的 64 位 FILETIME 拆回 syscall.Filetime 再换算。
func filetime(v int64) time.Time {
	return filetimeFromWin32(syscall.Filetime{
		LowDateTime:  uint32(v),
		HighDateTime: uint32(uint64(v) >> 32),
	})
}

// filetimeFromWin32 用标准库的纳秒换算，不自己算 1601 到 1970 的偏移。
func filetimeFromWin32(ft syscall.Filetime) time.Time {
	if ft.HighDateTime == 0 && ft.LowDateTime == 0 {
		// 0 表示这个文件系统没记录，不是 1601 年。
		return time.Time{}
	}
	return time.Unix(0, ft.Nanoseconds()).UTC()
}
