//go:build linux

package metadata

import (
	"io/fs"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Linux 侧两档，按内核能力退，不改语义：
//
//  1. statx                                 4.11+   四个时间全有（唯一能拿到 Btime 的）
//  2. syscall.Stat_t（fstatat 已经做过了）   一直有   三个时间，缺 Btime
//
// Btime 只走 statx：syscall.Stat_t 里根本没有出生时间这个字段，4.11 之前的内核
// 在用户态拿不到它（debugfs 之类的后门不算）。老内核上 statx 返回 ENOSYS，
// 退到 Stat_t 保住三个时间，比编一个假的出生时间诚实。
func platformTimes(path string, fi fs.FileInfo) timestamps {
	var stx unix.Statx_t
	err := unix.Statx(
		unix.AT_FDCWD,
		path,
		// 不触发同步、不跟随符号链接：拿到的是这个路径自己。
		unix.AT_STATX_SYNC_AS_STAT|unix.AT_SYMLINK_NOFOLLOW,
		unix.STATX_BTIME|unix.STATX_CTIME|unix.STATX_MTIME|unix.STATX_ATIME,
		&stx,
	)
	if err == nil {
		return timestampsFromStatx(stx)
	}

	if fi == nil {
		return timestamps{}
	}
	// 老内核（< 4.11）走这里；32 位下 Sec/Nsec 是 int32，统一转 int64 再算。
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return timestamps{
			Ctime: time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec)),
			Mtime: time.Unix(int64(st.Mtim.Sec), int64(st.Mtim.Nsec)),
			Atime: time.Unix(int64(st.Atim.Sec), int64(st.Atim.Nsec)),
		}
	}
	return timestamps{Mtime: fi.ModTime()}
}

// timestampsFromStatx 只认 mask 里声明有效的字段。
// 不看 mask 直接取，等于把"没记录"读成 1970-01-01。
func timestampsFromStatx(stx unix.Statx_t) timestamps {
	var ts timestamps
	if stx.Mask&unix.STATX_CTIME != 0 {
		ts.Ctime = statxTime(stx.Ctime)
	}
	if stx.Mask&unix.STATX_MTIME != 0 {
		ts.Mtime = statxTime(stx.Mtime)
	}
	if stx.Mask&unix.STATX_ATIME != 0 {
		ts.Atime = statxTime(stx.Atime)
	}
	if stx.Mask&unix.STATX_BTIME != 0 {
		ts.Btime = statxTime(stx.Btime)
	}
	return ts
}

func statxTime(t unix.StatxTimestamp) time.Time {
	return time.Unix(t.Sec, int64(t.Nsec))
}
