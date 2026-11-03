//go:build windows

package metadata

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
