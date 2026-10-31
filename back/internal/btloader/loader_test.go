//go:build !windows

package btloader

import (
	"errors"
	"testing"
)

// The off-Windows contract is part of the design, not an accident: the runtime
// is only shipped as a Windows DLL, and dlopen-ing a second Go runtime into a Go
// process is not a supported configuration. Pin it so nobody "fixes" the stub
// into something that silently pretends to work.
func TestUnavailableOffWindows(t *testing.T) {
	if Available() {
		t.Fatal("Available() must be false off Windows")
	}

	client, err := Open(Config{DataDir: t.TempDir()})
	if err == nil {
		client.Close()
		t.Fatal("Open() must fail off Windows")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open() error %v does not wrap ErrUnavailable", err)
	}
	if client != nil {
		t.Fatal("Open() returned a client alongside an error")
	}
}

// A nil client must be inert rather than panicking: call sites keep the handle
// and check it, they do not expect every method to be guarded at the call site.
func TestNilClientIsInert(t *testing.T) {
	var c *Client

	c.Close() // must not panic
	c.OnComplete(func(string, []CompletedFile) {})

	if got := c.GetDownloadDir(); got != "" {
		t.Errorf("GetDownloadDir = %q, want empty", got)
	}
	if got := c.GetDownload("x"); got != nil {
		t.Errorf("GetDownload = %v, want nil", got)
	}
	if got := c.ListDownloads(); got != nil {
		t.Errorf("ListDownloads = %v, want nil", got)
	}
	if err := c.PauseDownload("x"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("PauseDownload error = %v, want ErrUnavailable", err)
	}
	if _, err := c.AddTorrentBytes([]byte("x")); !errors.Is(err, ErrUnavailable) {
		t.Errorf("AddTorrentBytes error = %v, want ErrUnavailable", err)
	}
	if got := c.GetGlobalStats(); got != (GlobalStats{}) {
		t.Errorf("GetGlobalStats = %+v, want zero value", got)
	}
	if got := c.DHTStatus(); got.Available {
		t.Errorf("DHTStatus.Available = true on a nil client")
	}
}
