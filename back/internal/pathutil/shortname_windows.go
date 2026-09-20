//go:build windows

package pathutil

// 8.3 短名（`C:\PROGRA~1`、`D:\MYMEDI~1\VIDEO~1\a.mkv`）。
//
// 为什么必须单独处理：Windows 上同一个目录可以有两个**字符串不同**的名字，
// 而 Within/normalize 的比较就是按字符串来的。不还原会出两种问题：
//
//  1. 误拒（真会打到正常使用）：运营者配的是长名 `\…\Shared Media Library`，
//     请求用短名 `\…\SHARED~1\a.txt` 进来（其它工具写进 DB 的路径、面板透传的
//     路径都可能是短名），Rel 比对不上 → 判成越权。现象和当年的"清单看得见、
//     一拉就 read failed"同源：文件确实在允许根里，却读不到。
//  2. 两套名字各自被当回事：判定用长名、打开用短名（或反过来），两边不一致时
//     会出现"判定放行、实际打到别处"的裂缝。
//
// 反面要记住：短名**不能创造新的可达范围**——它是同一个目录的另一个名字，
// 不会把 `C:\Windows` 变成共享目录 `C:\data` 之内的东西。所以这里做的是
// "统一到长名"，而不是"拦住短名"。

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32DLL     = syscall.NewLazyDLL("kernel32.dll")
	procGetLongName = kernel32DLL.NewProc("GetLongPathNameW")
	procGetShortNam = kernel32DLL.NewProc("GetShortPathNameW")
)

// getPathName 调 kernel32 的路径名转换 API；失败返回 false。
//
// 这些 API 在没有对应名字时直接返回 0（GetLastError 未必有意义），所以这里
// 不认 lasterr，只看返回值。
func getPathName(proc *syscall.LazyProc, in string) (string, bool) {
	if proc == nil || proc.Find() != nil {
		return "", false
	}
	p, err := syscall.UTF16PtrFromString(in)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, 1024)
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r == 0 || r > uintptr(len(buf)) {
		return "", false
	}
	return syscall.UTF16ToString(buf[:r]), true
}

// ExpandShortNames 把路径里存在的短名成分还原成长名。
//
// 不做无所求的替换：只替换**文件系统真的给了短名**的那一段，其余一字不改。
// 路径还不存在的新文件也支持——从最长前缀往回缩，取第一个能成功转换的前缀，
// 后面不存在的部分原样保留（正因为它们还不存在，也无从谈长短名）。
func ExpandShortNames(p string) string {
	clean := filepath.Clean(p)
	vol := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, vol)
	rest = strings.TrimLeft(rest, `/\`)
	if rest == "" {
		return clean
	}
	parts := strings.Split(rest, string(filepath.Separator))
	for n := len(parts); n > 0; n-- {
		prefix := vol + string(filepath.Separator) + strings.Join(parts[:n], string(filepath.Separator))
		if long, ok := getPathName(procGetLongName, prefix); ok {
			if n == len(parts) {
				return long
			}
			return filepath.Join(long, strings.Join(parts[n:], string(filepath.Separator)))
		}
	}
	return clean
}

// ShortNameOf 取路径的 8.3 短名形式（测试用：构造短名 payload）。
// 卷上禁用了 8.3 生成时返回空串。
func ShortNameOf(p string) string {
	if s, ok := getPathName(procGetShortNam, p); ok {
		return s
	}
	return ""
}
