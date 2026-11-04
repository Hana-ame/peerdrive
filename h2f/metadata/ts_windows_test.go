//go:build windows

package metadata

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"
)

// The Windows port has to deliver ChangeTime (ctime evidence) and creation time
// (btime) from FILE_BASIC_INFO; if the syscall ever stops working the fields
// silently degrade to zero, so pin them down.
func TestPlatformTimesOnWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(path, []byte("probe"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	ts := times(path, fi)
	if ts.Btime.IsZero() {
		t.Error("Btime is zero: creation time missing")
	}
	if ts.Ctime.IsZero() {
		t.Error("Ctime is zero: change time missing")
	}
	if ts.Mtime.IsZero() {
		t.Error("Mtime is zero")
	}
	if ts.Ctime.Location() != time.UTC {
		t.Errorf("Ctime location = %v, want UTC", ts.Ctime.Location())
	}
	if ts.Btime.After(ts.Mtime) {
		t.Errorf("Btime %v is after Mtime %v: creation after write", ts.Btime, ts.Mtime)
	}
}

// 布局不能跟着 GOARCH 走：Windows 侧无论 32/64 位都按 40 字节校验缓冲区，
// 而 Go 在 386 上对 int64 只做 4 字节对齐。尺寸偏了不会报错给调用方，
// 只会静默返回全零时间，所以这里逐一钉住偏移。
func TestFileBasicInfoLayout(t *testing.T) {
	var info fileBasicInfo
	if got := unsafe.Sizeof(info); got != 40 {
		t.Fatalf("FILE_BASIC_INFO size = %d, want 40", got)
	}
	offsets := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"CreationTime", unsafe.Offsetof(info.CreationTime), 0},
		{"LastAccessTime", unsafe.Offsetof(info.LastAccessTime), 8},
		{"LastWriteTime", unsafe.Offsetof(info.LastWriteTime), 16},
		{"ChangeTime", unsafe.Offsetof(info.ChangeTime), 24},
		{"FileAttributes", unsafe.Offsetof(info.FileAttributes), 32},
	}
	for _, o := range offsets {
		if o.got != o.want {
			t.Errorf("%s offset = %d, want %d", o.name, o.got, o.want)
		}
	}
}

// 读目录也要能拿到时间：os.Open 对目录是有效的（FILE_FLAG_BACKUP_SEMANTICS）。
func TestPlatformTimesOnDirectory(t *testing.T) {
	dir := t.TempDir()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	ts := times(dir, fi)
	if ts.Btime.IsZero() || ts.Ctime.IsZero() {
		t.Fatalf("directory timestamps incomplete: %+v", ts)
	}
}
