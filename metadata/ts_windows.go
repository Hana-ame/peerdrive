//go:build windows

package metadata

import (
	"io/fs"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// FILE_INFO_BY_HANDLE_CLASS::FileBasicInfo
const fileBasicInfoClass = 0

// fileBasicInfo mirrors FILE_BASIC_INFO: four FILETIMEs (100ns ticks since
// 1601-01-01 UTC) plus the attribute word. FileAttributes is not used here,
// but it is part of the struct: leaving it out makes the call fail with
// ERROR_BAD_LENGTH instead of returning short data.
type fileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
}

var (
	kernel32                         = syscall.NewLazyDLL("kernel32.dll")
	procGetFileInformationByHandleEx = kernel32.NewProc("GetFileInformationByHandleEx")
)

// platformTimes asks the kernel for FILE_BASIC_INFO, which is the only way to
// get ChangeTime on Windows: Win32FileAttributeData and syscall.Stat_t both
// omit it, and NTFS does maintain it (it moves on any metadata write, so it
// plays the role Ctime plays on Linux).
func platformTimes(path string, fi fs.FileInfo) timestamps {
	// os.Open opens directories too (FILE_FLAG_BACKUP_SEMANTICS), so a single
	// path works for both file and directory entries.
	if f, err := os.Open(path); err == nil {
		var info fileBasicInfo
		ok, _, _ := procGetFileInformationByHandleEx.Call(
			f.Fd(),
			fileBasicInfoClass,
			uintptr(unsafe.Pointer(&info)),
			unsafe.Sizeof(info),
		)
		f.Close()
		if ok != 0 {
			return timestamps{
				Ctime: filetime(info.ChangeTime),
				Mtime: filetime(info.LastWriteTime),
				Btime: filetime(info.CreationTime),
				Atime: filetime(info.LastAccessTime),
			}
		}
	}

	// Fallback for handles the API refuses (some network shares): what
	// os.FileInfo already carries, minus ChangeTime, which stays zero.
	ts := timestamps{Mtime: fi.ModTime()}
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		ts.Atime = filetime(unpackFiletime(d.LastAccessTime))
		ts.Btime = filetime(unpackFiletime(d.CreationTime))
	}
	return ts
}

func unpackFiletime(ft syscall.Filetime) int64 {
	return int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
}

// windowsEpochOffset is the number of 100ns ticks between 1601-01-01 and
// 1970-01-01, the two epochs involved.
const windowsEpochOffset = 116444736000000000

func filetime(t int64) time.Time {
	if t <= 0 {
		return time.Time{}
	}
	return time.Unix(0, (t-windowsEpochOffset)*100).UTC()
}
