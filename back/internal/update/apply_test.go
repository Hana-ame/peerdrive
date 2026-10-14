package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFakeCurrent 建一个「当前二进制」临时文件，内容 oldData、权限 0755。
func newFakeCurrent(t *testing.T, oldData []byte) string {
	t.Helper()
	dir := t.TempDir()
	current := filepath.Join(dir, "peerdrive-test-bin")
	if err := os.WriteFile(current, oldData, 0o755); err != nil {
		t.Fatal(err)
	}
	return current
}

func TestApplyToSuccess(t *testing.T) {
	newData := []byte("new-binary-v0.3.1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newData)
	}))
	defer srv.Close()

	current := newFakeCurrent(t, []byte("old-binary"))
	asset := &Asset{Name: "peerdrive-test-bin", URL: srv.URL, SHA256: hexDigest(t, newData)}

	if err := applyTo(context.Background(), asset, current); err != nil {
		t.Fatalf("applyTo = %v", err)
	}

	got, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newData) {
		t.Fatalf("current content = %q, want %q", got, newData)
	}
	// 旧版本留 .bak。
	bak, err := os.ReadFile(current + ".bak")
	if err != nil {
		t.Fatalf("expected .bak backup: %v", err)
	}
	if string(bak) != "old-binary" {
		t.Fatalf(".bak content = %q, want old-binary", bak)
	}
	// 权限保留。
	st, err := os.Stat(current)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("current mode = %v, %v; want 0755", st.Mode(), err)
	}
	// 无残留临时文件。
	leftover, _ := filepath.Glob(filepath.Join(filepath.Dir(current), ".peerdrive-update-*.tmp"))
	if len(leftover) != 0 {
		t.Fatalf("leftover temp files: %v", leftover)
	}
}

func TestApplyToChecksumMismatch(t *testing.T) {
	body := []byte("tampered-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	current := newFakeCurrent(t, []byte("old-binary"))
	// 清单里的 sha256 与下载内容不一致（模拟中间人被篡改 / 清单损坏）。
	asset := &Asset{Name: "peerdrive-test-bin", URL: srv.URL, SHA256: strings.Repeat("0", 64)}

	if err := applyTo(context.Background(), asset, current); err == nil {
		t.Fatal("applyTo with checksum mismatch expected error")
	}
	got, _ := os.ReadFile(current)
	if string(got) != "old-binary" {
		t.Fatalf("current changed on checksum mismatch: %q", got)
	}
	if _, err := os.Stat(current + ".bak"); !os.IsNotExist(err) {
		t.Fatalf(".bak should not exist on checksum mismatch (err=%v)", err)
	}
}

func TestApplyToDownloadFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	current := newFakeCurrent(t, []byte("old-binary"))
	asset := &Asset{Name: "peerdrive-test-bin", URL: srv.URL, SHA256: strings.Repeat("0", 64)}

	if err := applyTo(context.Background(), asset, current); err == nil {
		t.Fatal("applyTo with HTTP 500 expected error")
	}
	got, _ := os.ReadFile(current)
	if string(got) != "old-binary" {
		t.Fatalf("current changed on download failure: %q", got)
	}
}

// TestSwapRollbackOnReplaceFailure 注入 rename 第二次（tmp→current）失败，
// 验证 swap 把 .bak 挪回 current（回滚），原二进制不动、临时文件被清。
func TestSwapRollbackOnReplaceFailure(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current")
	tmp := filepath.Join(dir, "tmp")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	origRename := rename
	defer func() { rename = origRename }()
	calls := 0
	rename = func(old, new string) error {
		calls++
		if calls == 2 { // tmp → current 失败
			return os.ErrPermission
		}
		return origRename(old, new)
	}

	if err := swap(tmp, current); err == nil {
		t.Fatal("swap expected error on injected rename failure")
	}
	got, _ := os.ReadFile(current)
	if string(got) != "old" {
		t.Fatalf("current after rollback = %q, want old", got)
	}
	if _, err := os.Stat(current + ".bak"); !os.IsNotExist(err) {
		t.Fatalf(".bak should be gone after rollback (err=%v)", err)
	}
}

// TestApplyToBackupFailure 注入 rename 第一次（current→bak）失败：
// 原二进制不动，临时文件被清，报错。
func TestApplyToBackupFailure(t *testing.T) {
	newData := []byte("new")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(newData)
	}))
	defer srv.Close()

	current := newFakeCurrent(t, []byte("old"))
	asset := &Asset{Name: "peerdrive-test-bin", URL: srv.URL, SHA256: hexDigest(t, newData)}

	origRename := rename
	defer func() { rename = origRename }()
	calls := 0
	rename = func(old, new string) error {
		calls++
		if calls == 1 { // current → bak 失败
			return os.ErrPermission
		}
		return origRename(old, new)
	}

	if err := applyTo(context.Background(), asset, current); err == nil {
		t.Fatal("applyTo expected error on backup failure")
	}
	got, _ := os.ReadFile(current)
	if string(got) != "old" {
		t.Fatalf("current changed on backup failure: %q", got)
	}
	leftover, _ := filepath.Glob(filepath.Join(filepath.Dir(current), ".peerdrive-update-*.tmp"))
	if len(leftover) != 0 {
		t.Fatalf("temp file leaked on backup failure: %v", leftover)
	}
}
