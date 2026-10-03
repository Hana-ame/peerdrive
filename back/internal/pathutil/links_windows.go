//go:build !unix

package pathutil

import (
	"os"
	"syscall"
)

// Nlink cannot answer on Windows: os.FileInfo.Sys() only provides
// Win32FileAttributeData here, which has no link count. Kept only for cross-platform call-site consistency;
// NlinkOf below is the one that actually works.
func Nlink(os.FileInfo) uint64 { return 0 }

// NlinkOf obtains the hard link count from an **already-opened handle**.
//
// This is the only regular way to get nlink on Windows: BY_HANDLE_FILE_INFORMATION.NumberOfLinks
// returned by GetFileInformationByHandle, semantically equivalent to st_nlink from Unix fstat.
// Before this, the defense was empty on Windows (= hard links were not blocked at all); now it is fixed.
//
// Note the handle must be the already-opened one (the same fd used later to read content for sha256) --
// statting by path again in between would mean re-resolving the path, opening another TOCTOU window.
func NlinkOf(f *os.File) uint64 {
	if f == nil {
		return 0
	}
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		// If it can't be obtained (exotic cases such as unsupported handle type), treat as "unknown=allow",
		// consistent with the Unix-side Nlink policy when *syscall.Stat_t can't be obtained.
		return 0
	}
	return uint64(d.NumberOfLinks)
}

// NlinkSupported: Windows can now obtain link counts (via handle).
func NlinkSupported() bool { return true }
