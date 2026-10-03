//go:build windows

package pathutil

// 8.3 short names (`C:\PROGRA~1`, `D:\MYMEDI~1\VIDEO~1\a.mkv`).
//
// Why separate handling is required: on Windows, the same directory can have two **different strings**
// names, and Within/normalize comparison is string-based. Without expansion, two problems arise:
//
//  1. False rejection (would actually affect normal usage): the operator configures a long name
//     like `\...\Shared Media Library`, but the request comes in with a short name like
//     `\...\SHARED~1\a.txt` (paths written into the DB by other tools, or paths passed through
//     the panel may be short names), Rel won't match -> judged out of bounds. The symptom is the
//     same as the old "visible in manifest, but read failed on fetch": the file is actually inside
//     the allowed root but can't be read.
//  2. Two name systems each taken seriously: checking uses the long name, opening uses the short name
//     (or vice versa), and when they disagree, a crack appears where "check passes but actually goes elsewhere".
//
// On the other hand, remember: short names **cannot create new reachable scopes** -- it's another name
// for the same directory, it won't turn `C:\Windows` into something inside the shared directory
// `C:\data`. So this does "unify to long names", not "block short names".

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

// getPathName calls kernel32 path name conversion APIs; returns false on failure.
//
// These APIs directly return 0 when the corresponding name doesn't exist (GetLastError may not be
// meaningful), so we don't check lasterr here, only the return value.
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

// ExpandShortNames expands short name components in the path back to long names.
//
// Doesn't do speculative replacement: only replaces the segment where the **filesystem actually
// gave a short name**, everything else unchanged. Also works for new files that don't exist yet --
// shrink from the longest prefix backwards, take the first prefix that converts successfully, and
// keep the non-existent tail as-is (since they don't exist, short/long names are moot).
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

// ShortNameOf gets the 8.3 short name form of a path (for testing: constructing short-name payloads).
// Returns empty string when 8.3 generation is disabled on the volume.
func ShortNameOf(p string) string {
	if s, ok := getPathName(procGetShortNam, p); ok {
		return s
	}
	return ""
}
