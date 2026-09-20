//go:build !unix

package pathutil

import (
	"os"
	"syscall"
)

// Nlink 这条入口在 Windows 上给不出答案：os.FileInfo.Sys() 到这里只有
// Win32FileAttributeData，里面没有链接数。留着只是为了跨平台调用点一致，
// 真正生效的是下面的 NlinkOf。
func Nlink(os.FileInfo) uint64 { return 0 }

// NlinkOf 从**已打开的句柄**取硬链接数。
//
// 这是 Windows 上唯一常规的取 nlink 的办法：GetFileInformationByHandle 返回的
// BY_HANDLE_FILE_INFORMATION.NumberOfLinks，语义等价于 Unix fstat 的 st_nlink。
// 之前这条防线在 Windows 上是空的（= 硬链接完全没拦），现在补上了。
//
// 注意句柄必须是已经打开的那个（就是随后要读内容做 sha256 的那个 fd）——中途
// 再按路径 stat 一次等于重新解析一次路径，又开一个 TOCTOU 窗口。
func NlinkOf(f *os.File) uint64 {
	if f == nil {
		return 0
	}
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		// 取不到（句柄类型不支持等 exotic 情况）按"未知=放行"处理，
		// 与 Unix 侧 Nlink 拿不到 *syscall.Stat_t 时的策略一致。
		return 0
	}
	return uint64(d.NumberOfLinks)
}

// NlinkSupported Windows 现在能拿到链接数了（走句柄）。
func NlinkSupported() bool { return true }
