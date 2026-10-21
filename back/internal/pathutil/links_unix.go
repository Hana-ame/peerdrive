//go:build unix

package pathutil

import (
	"os"
	"syscall"
)

// Nlink returns the number of hard links for a file; returns 0 if unavailable (the caller treats this as "unknown").
//
// Why it's needed: hard links have no direction, and EvalSymlinks doesn't catch them either -- the same
// inode may have one name inside the shared directory and another outside, and there is **no** way to
// determine from the path alone whether it is exposed. The only possible policy is "reject if it has
// more than one name" (less is more).
func Nlink(fi os.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	// Nlink has different bit widths across Unix platforms (Linux uint64 / darwin uint16); normalize to uint64
	return uint64(st.Nlink)
}

// NlinkOf obtains the link count from an **already-opened handle** (internally does fstat on that fd).
//
// Why the handle version: unified cross-platform argument type. On Windows, only
// GetFileInformationByHandle can provide NumberOfLinks, and it requires a handle -- call sites pass *os.File
// uniformly so both platforms work.
func NlinkOf(f *os.File) uint64 {
	if f == nil {
		return 0
	}
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return Nlink(fi)
}

// NlinkSupported returns whether this platform can obtain hard link counts.
func NlinkSupported() bool { return true }
