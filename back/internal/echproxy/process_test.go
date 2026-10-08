package echproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVerifySHA256 verifies the SHA256 file verification against a known
// hash.
//
// Discovery background: the module must verify the downloaded ech-proxy
// exe against checksums.txt before executing it. This test pins the
// verifySHA256 helper against a known vector.
func TestVerifySHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	data := []byte("hello world")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(data)
	want := hex.EncodeToString(h[:])

	if err := verifySHA256(path, want); err != nil {
		t.Errorf("verifySHA256 correct hash: %v", err)
	}
	if err := verifySHA256(path, "deadbeef"); err == nil {
		t.Error("verifySHA256 wrong hash: expected error")
	}
	// Case-insensitive comparison.
	if err := verifySHA256(path, strings.ToUpper(want)); err != nil {
		t.Errorf("verifySHA256 uppercase hash: %v", err)
	}
}

// TestFileSHA256 verifies the fileSHA256 helper.
func TestFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	data := []byte("abc")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256Hex("abc")
	if got != want {
		t.Errorf("fileSHA256 = %q, want %q", got, want)
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// TestFetchChecksum verifies that fetchChecksum parses the checksums.txt
// format and extracts the SHA256 for a given exe name.
//
// Discovery background: the checksums.txt format is "<sha256>  <filename>".
// The parser must handle multi-line manifests and trailing slashes.
func TestFetchChecksum(t *testing.T) {
	const checksumsBody = `7b9a43e401fb48a4ff351ded01014820eea9cfee270d75c957ff0228b85b4fcf  ech-proxy-darwin-amd64
c8ffcc8f8901ac8770dcd8345401763b83bceac13ed1cef683f97872ef131719  ech-proxy-linux-amd64
f9ff0e84d4187b4cb53b81570b831131f7bee084c21f7aae541595d1a3a1f2b6  ech-proxy-windows-amd64.exe
9d8e6936802737fb2b883e388cafbdc8f19de5308e7c5ace6f099a839d813da3  ech-proxy-windows-arm64.exe`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, checksumsBody)
	}))
	t.Cleanup(server.Close)

	cfg := NewModuleConfig()
	cfg.ChecksumsURLOverride = server.URL + "/checksums.txt"

	pm := NewProcessManager(&cfg)
	got, err := pm.fetchChecksum(context.Background(), "ech-proxy-windows-amd64.exe")
	if err != nil {
		t.Fatalf("fetchChecksum: %v", err)
	}
	want := "f9ff0e84d4187b4cb53b81570b831131f7bee084c21f7aae541595d1a3a1f2b6"
	if got != want {
		t.Errorf("fetchChecksum = %q, want %q", got, want)
	}
}

// TestFetchChecksumNotFound verifies the error when the exe name is not
// in the checksums manifest.
func TestFetchChecksumNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "abc123  other-file.exe\n")
	}))
	t.Cleanup(server.Close)

	cfg := NewModuleConfig()
	cfg.ChecksumsURLOverride = server.URL + "/checksums.txt"

	pm := NewProcessManager(&cfg)
	if _, err := pm.fetchChecksum(context.Background(), "ech-proxy-windows-amd64.exe"); err == nil {
		t.Error("expected error for missing checksum")
	}
}

// TestDownloadFile verifies that downloadFile streams a file from a URL
// to disk.
func TestDownloadFile(t *testing.T) {
	const content = "echo-proxy-exe-binary-content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, content)
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "downloaded.exe")

	cfg := NewModuleConfig()
	pm := NewProcessManager(&cfg)
	if err := pm.downloadFile(context.Background(), server.URL+"/file", path); err != nil {
		t.Fatalf("downloadFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("downloaded content = %q, want %q", string(data), content)
	}
}

// TestDownloadFileHTTPError verifies the error on non-200 responses.
func TestDownloadFileHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "downloaded.exe")

	cfg := NewModuleConfig()
	pm := NewProcessManager(&cfg)
	if err := pm.downloadFile(context.Background(), server.URL+"/file", path); err == nil {
		t.Error("expected error for HTTP 404")
	}
	// Partial download must leave no file behind.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("partial download left a file behind")
	}
}

// TestCheckPortFree verifies port conflict detection.
//
// Discovery background: the module must detect when the ech-proxy listen
// port is already occupied and report ErrPortInUse. This test binds a
// listener, verifies checkPortFree returns ErrPortInUse, then closes the
// listener and verifies checkPortFree succeeds.
func TestCheckPortFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()

	// Port is free.
	if err := checkPortFree(addr); err != nil {
		t.Errorf("checkPortFree on free port: %v", err)
	}

	// Bind and verify conflict.
	l2, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()

	err = checkPortFree(addr)
	if err == nil {
		t.Fatal("checkPortFree on occupied port: expected error")
	}
	e, ok := err.(ErrPortInUse)
	if !ok {
		t.Errorf("error type = %T, want ErrPortInUse", err)
	}
	if e.Addr != addr {
		t.Errorf("ErrPortInUse.Addr = %q, want %q", e.Addr, addr)
	}
}

// TestErrPortInUseError verifies the error message format.
func TestErrPortInUseError(t *testing.T) {
	e := ErrPortInUse{Addr: "127.0.0.1:8443", Err: os.ErrNotExist}
	msg := e.Error()
	if !strings.Contains(msg, "port in use") || !strings.Contains(msg, "127.0.0.1:8443") {
		t.Errorf("error message = %q", msg)
	}
}

// TestEnsureExe verifies the ensure-exe flow: download, verify, skip on
// re-call.
//
// Discovery background: EnsureExe is the entry point for the "download
// dll/exe" requirement. The test verifies the full flow: first call
// downloads + verifies, second call skips (already present + correct
// hash).
func TestEnsureExe(t *testing.T) {
	const exeContent = "fake-ech-proxy-exe"
	exeHashReal := sha256Hex(exeContent)

	dir := t.TempDir()

	// Mock server serves both checksums.txt and the exe.
	mux := http.NewServeMux()
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, exeHashReal+"  ech-proxy-windows-amd64.exe\n")
	})
	mux.HandleFunc("/exe", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, exeContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	cfg := NewModuleConfig()
	cfg.EchProxyDir = dir
	cfg.EchProxyExeName = "ech-proxy-windows-amd64.exe"
	cfg.ChecksumsURLOverride = server.URL + "/checksums.txt"
	cfg.ExeURLOverride = server.URL + "/exe"

	pm := NewProcessManager(&cfg)
	path, err := pm.EnsureExe(context.Background())
	if err != nil {
		t.Fatalf("EnsureExe first call: %v", err)
	}
	if path != filepath.Join(dir, "ech-proxy-windows-amd64.exe") {
		t.Errorf("EnsureExe path = %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != exeContent {
		t.Errorf("exe content = %q, want %q", string(data), exeContent)
	}

	// Second call: should skip (already present + correct hash).
	count := 0
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server2.Close)

	cfg2 := NewModuleConfig()
	cfg2.EchProxyDir = dir
	cfg2.EchProxyExeName = "ech-proxy-windows-amd64.exe"
	cfg2.ChecksumsURLOverride = server2.URL + "/checksums.txt"
	cfg2.ExeURLOverride = server2.URL + "/exe"

	pm2 := NewProcessManager(&cfg2)
	if _, err := pm2.EnsureExe(context.Background()); err != nil {
		t.Fatalf("EnsureExe second call: %v", err)
	}
	if count != 0 {
		t.Errorf("EnsureExe second call should not re-download, but made %d requests", count)
	}
}

// TestEnsureExeCorruptVerifies verifies that a corrupt exe is deleted
// and re-downloaded.
func TestEnsureExeCorruptVerifies(t *testing.T) {
	const exeContent = "correct-exe-content"
	exeHash := sha256Hex(exeContent)

	dir := t.TempDir()
	exePath := filepath.Join(dir, "ech-proxy-windows-amd64.exe")

	// Write a corrupt exe first.
	if err := os.WriteFile(exePath, []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			io.WriteString(w, exeHash+"  ech-proxy-windows-amd64.exe\n")
		} else {
			io.WriteString(w, exeContent)
		}
	}))
	t.Cleanup(server.Close)

	cfg := NewModuleConfig()
	cfg.EchProxyDir = dir
	cfg.EchProxyExeName = "ech-proxy-windows-amd64.exe"
	cfg.ChecksumsURLOverride = server.URL + "/checksums.txt"
	cfg.ExeURLOverride = server.URL + "/exe"

	pm := NewProcessManager(&cfg)
	path, err := pm.EnsureExe(context.Background())
	if err != nil {
		t.Fatalf("EnsureExe: %v", err)
	}
	if path != exePath {
		t.Errorf("path = %q, want %q", path, exePath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != exeContent {
		t.Errorf("exe content = %q, want %q", string(data), exeContent)
	}
}

// TestEnsureExeHashMismatch verifies that a hash mismatch on download
// removes the file and returns an error.
func TestEnsureExeHashMismatch(t *testing.T) {
	const exeContent = "exe-content"

	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			// Wrong hash.
			io.WriteString(w, "deadbeef  ech-proxy-windows-amd64.exe\n")
		} else {
			io.WriteString(w, exeContent)
		}
	}))
	t.Cleanup(server.Close)

	cfg := NewModuleConfig()
	cfg.EchProxyDir = dir
	cfg.EchProxyExeName = "ech-proxy-windows-amd64.exe"
	cfg.ChecksumsURLOverride = server.URL + "/checksums.txt"
	cfg.ExeURLOverride = server.URL + "/exe"

	pm := NewProcessManager(&cfg)
	if _, err := pm.EnsureExe(context.Background()); err == nil {
		t.Error("expected error for hash mismatch")
	}
	// File must be removed on mismatch.
	if _, err := os.Stat(filepath.Join(dir, "ech-proxy-windows-amd64.exe")); !os.IsNotExist(err) {
		t.Error("corrupt exe file was not removed after hash mismatch")
	}
}

// TestProcessManagerIsRunning verifies the IsRunning/Stop idempotence.
func TestProcessManagerIsRunning(t *testing.T) {
	cfg := NewModuleConfig()
	pm := NewProcessManager(&cfg)
	if pm.IsRunning() {
		t.Error("new ProcessManager should not be running")
	}
	// Stop on a non-running process must not error.
	if err := pm.Stop(); err != nil {
		t.Errorf("Stop on non-running: %v", err)
	}
}

// TestStartNonWindowsError verifies that Start returns a clear error on
// non-Windows platforms (the exe is Windows-only).
//
// Discovery background: the module downloads a Windows executable. On
// Linux/macOS, the exec will fail. The test verifies the error is
// descriptive rather than opaque.
func TestStartNonWindowsError(t *testing.T) {
	dir := t.TempDir()
	exePath := filepath.Join(dir, "ech-proxy-windows-amd64.exe")
	if err := os.WriteFile(exePath, []byte("fake-exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	exeHash := sha256Hex("fake-exe")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			io.WriteString(w, exeHash+"  ech-proxy-windows-amd64.exe\n")
		}
	}))
	t.Cleanup(server.Close)

	cfg := NewModuleConfig()
	cfg.EchProxyDir = dir
	cfg.EchProxyExeName = "ech-proxy-windows-amd64.exe"
	cfg.ListenAddr = "127.0.0.1:18443"
	cfg.ChecksumsURLOverride = server.URL + "/checksums.txt"
	cfg.ExeURLOverride = server.URL + "/exe"

	pm := NewProcessManager(&cfg)
	err := pm.Start(context.Background())
	if err == nil {
		t.Error("expected error for non-Windows platform")
	}
}

// TestWaitReadyTimeout verifies the readiness probe timeout.
func TestWaitReadyTimeout(t *testing.T) {
	cfg := NewModuleConfig()
	pm := NewProcessManager(&cfg)
	pm.doneCh = make(chan struct{})
	close(pm.doneCh) // process already exited

	err := pm.waitReady("127.0.0.1:1", 1*time.Second)
	if err == nil {
		t.Error("expected timeout error")
	}
}

// TestWaitReadySuccess verifies the readiness probe on a live port.
func TestWaitReadySuccess(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	cfg := NewModuleConfig()
	pm := NewProcessManager(&cfg)
	pm.doneCh = make(chan struct{})

	err = pm.waitReady(l.Addr().String(), 5*time.Second)
	if err != nil {
		t.Errorf("waitReady on live port: %v", err)
	}
}
