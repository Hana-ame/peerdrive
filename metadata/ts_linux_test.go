//go:build linux

package metadata

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Linux port must produce a real Ctime (statx Ctime / Stat_t Ctim). Btime
// is deliberately not asserted: it depends on the filesystem, and a zero there
// means "this fs does not record one", which the implementation reports
// honestly instead of inventing.
func TestPlatformTimesOnLinux(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.txt")
	if err := os.WriteFile(path, []byte("probe"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	ts := times(path, fi)
	if ts.Ctime.IsZero() {
		t.Error("Ctime is zero")
	}
	if ts.Mtime.IsZero() {
		t.Error("Mtime is zero")
	}
	if ts.Ctime.Location() != time.UTC {
		t.Errorf("Ctime location = %v, want UTC", ts.Ctime.Location())
	}
	if ts.Mtime.After(ts.Ctime) {
		t.Errorf("Mtime %v is after Ctime %v: content newer than its inode", ts.Mtime, ts.Ctime)
	}
}
