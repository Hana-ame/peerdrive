package echproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ProcessManager owns the ech-proxy child process lifecycle:
//
//	download exe → verify SHA256 → check port → start → readiness probe → stop
//
// It is idempotent: Start is safe to call when the process is already running,
// and Stop is safe to call when it is not. State is guarded by a mutex so the
// manager is safe to use from multiple goroutines (the peerdrive server
// calls Start/Stop from the shutdown path and the health-check path).
type ProcessManager struct {
	cfg *ModuleConfig
	// HTTPClient is the client used to download the exe and checksums.
	// nil = http.DefaultClient.
	HTTPClient *http.Client

	mu       sync.Mutex
	cmd      *exec.Cmd
	running  bool
	doneCh   chan struct{} // closed when the child exits
}

// NewProcessManager builds a ProcessManager for the given config.
func NewProcessManager(cfg *ModuleConfig) *ProcessManager {
	if cfg == nil {
		def := NewModuleConfig()
		cfg = &def
	}
	cfg.Normalize()
	return &ProcessManager{cfg: cfg}
}

// EnsureExe downloads the ech-proxy executable if it is missing, verifying
// it against the SHA256 published in checksums.txt. Returns the local exe
// path.
//
// Idempotent: a cached .sha256 file (written alongside the exe on first
// download) allows subsequent calls to verify without a network round-trip.
// A corrupt exe (or missing hash cache) is deleted and re-downloaded.
func (pm *ProcessManager) EnsureExe(ctx context.Context) (string, error) {
	cfg := pm.cfg
	exePath := cfg.ExePath()
	hashPath := exePath + ".sha256"

	// Fast path: exe + hash cache both present and valid → skip.
	if _, err := os.Stat(exePath); err == nil {
		if cached, rerr := os.ReadFile(hashPath); rerr == nil {
			if verr := verifySHA256(exePath, strings.TrimSpace(string(cached))); verr == nil {
				return exePath, nil
			}
			os.Remove(exePath)
			os.Remove(hashPath)
		}
	}

	checksum, err := pm.fetchChecksum(ctx, cfg.ExeName())
	if err != nil {
		return "", fmt.Errorf("echproxy: fetch checksums: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(exePath), 0o755); err != nil {
		return "", fmt.Errorf("echproxy: mkdir: %w", err)
	}

	if err := pm.downloadFile(ctx, cfg.ExeURL(), exePath); err != nil {
		return "", fmt.Errorf("echproxy: download exe: %w", err)
	}

	if err := verifySHA256(exePath, checksum); err != nil {
		os.Remove(exePath)
		return "", fmt.Errorf("echproxy: SHA256 mismatch for %s: %w", cfg.ExeName(), err)
	}

	// Cache the hash so the next call can skip the network.
	os.WriteFile(hashPath, []byte(checksum), 0o644)

	return exePath, nil
}

// fetchChecksum downloads checksums.txt and extracts the SHA256 for exeName.
func (pm *ProcessManager) fetchChecksum(ctx context.Context, exeName string) (string, error) {
	client := pm.client()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pm.cfg.ChecksumsURL(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "  "); idx >= 0 {
			name := strings.TrimSpace(line[idx+2:])
			if name == exeName || strings.HasSuffix(name, "/"+exeName) {
				hash := strings.TrimSpace(line[:idx])
				return hash, nil
			}
		}
	}
	return "", fmt.Errorf("checksum for %s not found in checksums.txt", exeName)
}

// downloadFile streams url to path, verifying the Content-Length when
// present. A partial download leaves no file behind.
func (pm *ProcessManager) downloadFile(ctx context.Context, url, path string) error {
	client := pm.client()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	tmpPath := path + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath) // no partial file on failure
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// client returns the HTTP client, defaulting to http.DefaultClient.
func (pm *ProcessManager) client() *http.Client {
	if pm.HTTPClient != nil {
		return pm.HTTPClient
	}
	return http.DefaultClient
}

// verifySHA256 computes the SHA256 of path and compares it to wantHash
// (lowercase hex). Returns nil on match.
func verifySHA256(path, wantHash string) error {
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, wantHash) {
		return fmt.Errorf("got %s, want %s", got, wantHash)
	}
	return nil
}

// fileSHA256 computes the SHA256 hex digest of a file.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// IsRunning reports whether the ech-proxy child process is alive.
func (pm *ProcessManager) IsRunning() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.running
}

// Start downloads the exe (if needed), checks for a port conflict, launches
// the ech-proxy process, and waits for it to become ready.
//
// The process is launched with a fixed upstream.json baked into the
// release binary; the module only needs to point ech-proxy at the iwara
// entry and inject the user's cookie via the upstream "headers" mechanism
// (which the release binary already configures for iwara).
//
// Port conflict: if the listen address is already occupied, Start returns
// ErrPortInUse without launching. The caller can decide whether to fail or
// to fall back to the existing instance (which is the operator's ech-proxy,
// not this module's — so no process is owned).
func (pm *ProcessManager) Start(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.running {
		return nil
	}

	exePath, err := pm.EnsureExe(ctx)
	if err != nil {
		return err
	}

	if err := checkPortFree(pm.cfg.ListenAddr); err != nil {
		return fmt.Errorf("echproxy: %w", err)
	}

	// Launch the ech-proxy process. The Windows exe is the release asset;
	// on non-Windows platforms this will fail at exec time, which is the
	// expected behaviour for this Windows-targeted module.
	cmd := exec.CommandContext(ctx, exePath,
		"-listen", pm.cfg.ListenAddr,
		"-ip-mode", "v4",
	)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if runtime.GOOS != "windows" {
		// Best-effort: the Windows exe cannot run on Linux/macOS. Report a
		// clear error rather than an opaque exec failure.
		pm.mu.Unlock()
		return fmt.Errorf("echproxy: %s is a Windows executable; this module requires Windows (GOOS=%s)", exePath, runtime.GOOS)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("echproxy: start: %w", err)
	}

	pm.cmd = cmd
	pm.running = true
	pm.doneCh = make(chan struct{})
	go func() {
		defer close(pm.doneCh)
		cmd.Wait()
	}()

	// Readiness probe: wait for the listen port to accept connections.
	if err := pm.waitReady(pm.cfg.ListenAddr, 30*time.Second); err != nil {
		pm.stopLocked()
		return fmt.Errorf("echproxy: not ready: %w", err)
	}
	return nil
}

// stopLocked stops the child process. Must be called with pm.mu held.
func (pm *ProcessManager) stopLocked() {
	if pm.cmd == nil {
		pm.running = false
		return
	}
	if pm.cmd.Process != nil {
		pm.cmd.Process.Kill()
		select {
		case <-pm.doneCh:
		case <-time.After(5 * time.Second):
			// Give up after 5s; the process is orphaned.
		}
	}
	pm.cmd = nil
	pm.running = false
}

// Stop terminates the ech-proxy child process. Idempotent.
func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if !pm.running {
		return nil
	}
	pm.stopLocked()
	return nil
}

// waitReady polls the listen address until it accepts a TCP connection,
// or the timeout elapses.
func (pm *ProcessManager) waitReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-pm.doneCh:
			return fmt.Errorf("process exited during readiness wait")
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

// checkPortFree reports whether addr is available for binding. It returns
// ErrPortInUse if a listener is already bound to addr.
func checkPortFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return ErrPortInUse{Addr: addr, Err: err}
	}
	l.Close()
	return nil
}

// ErrPortInUse is returned when the ech-proxy listen address is already
// occupied. It implements error for convenience.
type ErrPortInUse struct {
	Addr string
	Err  error
}

func (e ErrPortInUse) Error() string {
	return fmt.Sprintf("echproxy: port in use %s: %v", e.Addr, e.Err)
}

// Is reports whether the target error is ErrPortInUse.
func (e ErrPortInUse) Is(target error) bool {
	_, ok := target.(ErrPortInUse)
	return ok
}


