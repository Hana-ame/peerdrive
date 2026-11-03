//go:build linux

package metadata

import (
	"io/fs"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// platformTimes prefers statx, because syscall.Stat_t has no birth time and
// Btime is the one field that cannot be recovered any other way. statx is
// available on every kernel that matters; when it is not, the fall back to
// Stat_t keeps three of the four timestamps instead of none.
func platformTimes(path string, fi fs.FileInfo) timestamps {
	var stx unix.Statx_t
	err := unix.Statx(
		unix.AT_FDCWD,
		path,
		unix.AT_STATX_SYNC_AS_STAT|unix.AT_SYMLINK_NOFOLLOW,
		unix.STATX_BTIME|unix.STATX_CTIME|unix.STATX_MTIME|unix.STATX_ATIME,
		&stx,
	)
	if err == nil {
		ts := timestamps{
			Ctime: statxTime(stx.Ctime),
			Mtime: statxTime(stx.Mtime),
			Atime: statxTime(stx.Atime),
		}
		// Not every filesystem records a birth time; the mask says so, and a
		// zero StatxTimestamp would decode as 1970.
		if stx.Mask&unix.STATX_BTIME != 0 {
			ts.Btime = statxTime(stx.Btime)
		}
		return ts
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return timestamps{
			Ctime: time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)),
			Mtime: time.Unix(int64(st.Mtim.Sec), int64(st.Mtim.Nsec)),
			Atime: time.Unix(int64(st.Atim.Sec), int64(st.Atim.Nsec)),
		}
	}
	return timestamps{Mtime: fi.ModTime()}
}

func statxTime(t unix.StatxTimestamp) time.Time {
	return time.Unix(t.Sec, int64(t.Nsec))
}
