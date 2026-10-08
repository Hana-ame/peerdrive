package twimg

// process.go: ech-proxy subprocess lifecycle — spawn, readiness, stop.
//
// The lifecycle logic is written against a Runner/Handle pair instead of *exec.Cmd so
// that every decision (port-conflict handling, retry strategy, cleanup on readiness
// failure) is testable on any platform without a real executable present.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"sync"
	"time"
)

// ErrPortBusy is returned when the configured listen address is already bound by
// something this module did not start. The module never kills a foreign process: on a
// shared host 127.0.0.1:8443 may belong to the operator's own ech-proxy or an unrelated
// service, and guessing would be worse than failing loudly.
var ErrPortBusy = errors.New("echproxy: listen address already in use")

// Handle abstracts a running child process. *exec.Cmd is adapted by execHandle.
type Handle interface {
	// Pid is the child's process id, 0 if it is unavailable.
	Pid() int
	// Kill terminates the child. Idempotent.
	Kill() error
	// Done is closed once the child has exited and been reaped.
	Done() <-chan struct{}
}

// Runner starts the ech-proxy child. Default is execRunner (os/exec).
type Runner interface {
	Run(binary string, args []string, dir string) (Handle, error)
}

// execRunner spawns a real child process.
type execRunner struct {
	// Stdout/Stderr are the child's streams. nil → the child writes to /dev/null
	// (Go's default for an unset writer), which keeps a managed daemon quiet. Point
	// these at the peerdrive log or a file to diagnose an ech-proxy startup failure.
	Stdout io.Writer
	Stderr io.Writer
}

// Run implements Runner.
func (r execRunner) Run(binary string, args []string, dir string) (Handle, error) {
	cmd := exec.Command(binary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("echproxy: spawn %s: %w", binary, err)
	}
	return newExecHandle(cmd), nil
}

type execHandle struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func newExecHandle(cmd *exec.Cmd) Handle {
	h := &execHandle{cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		_ = cmd.Wait()
	}()
	return h
}

func (h *execHandle) Pid() int {
	if h.cmd == nil || h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

func (h *execHandle) Kill() error {
	if h.cmd == nil || h.cmd.Process == nil {
		return nil
	}
	return h.cmd.Process.Kill()
}

func (h *execHandle) Done() <-chan struct{} { return h.done }

// Process manages one ech-proxy child.
type Process struct {
	// BinPath is the verified binary returned by Downloader.Ensure.
	BinPath string
	// Addr is the listen address passed as -addr (127.0.0.1:8443).
	Addr string
	// IPMode pins the upstream egress family, passed as -ip-mode ("v4").
	IPMode string
	// WorkDir is the child's working directory. Empty → inherited.
	WorkDir string

	// Attempts bounds the Start retry loop (spawn + readiness + port conflict).
	Attempts int
	// Backoff is the base delay; the delay before attempt n is Backoff*(n-1).
	Backoff time.Duration
	// ReadyTimeout bounds how long one attempt waits for the listener to appear.
	ReadyTimeout time.Duration
	// Logf receives progress and failure diagnostics. nil → silenced.
	Logf func(format string, args ...interface{})
	// Runner starts the child. nil → execRunner.
	Runner Runner

	mu sync.Mutex
	h  Handle
}

// Args is the exact argument list handed to ech-proxy.
//
// -http is deliberately NOT passed: the module rewrites https://pbs.twimg.com to
// https://twimg-pbs.l.moonchan.xyz:8443 and would be downgrading the scheme.
// -no-browser keeps a headless server from popping a browser window on every start.
func (p *Process) Args() []string {
	args := []string{"-addr", p.Addr, "-no-browser"}
	if p.IPMode != "" {
		args = append(args, "-ip-mode", p.IPMode)
	}
	return args
}

// PortBusy reports whether a TCP listener already answers on addr.
//
// A refused connection means the port is free. A timeout means the host did not answer,
// which is also reported as free — there is nothing to wait for, and the real failure
// will surface when the subprocess tries to bind.
func PortBusy(addr string, timeout time.Duration) bool {
	d := &net.Dialer{Timeout: timeout}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// logf emits diagnostics through Logf when it is set. Process is used directly in tests
// where Logf is nil, so this must not dereference a nil function.
func (p *Process) logf(format string, args ...interface{}) {
	if p.Logf != nil {
		p.Logf(format, args...)
	}
}

// Start downloads nothing; it only runs the child at p.BinPath and waits until it is
// listening on p.Addr.
//
// Strategy: up to Attempts attempts. Each attempt (1) refuses to proceed if the port is
// already busy, (2) spawns, (3) waits for readiness, killing the child on failure so a
// failed attempt never leaves an orphan process behind.
func (p *Process) Start(ctx context.Context) error {
	if p.BinPath == "" {
		return errors.New("echproxy: Process.BinPath is empty")
	}
	if p.Addr == "" {
		return errors.New("echproxy: Process.Addr is empty")
	}
	if p.Alive() {
		return errors.New("echproxy: already running")
	}

	attempts := p.Attempts
	if attempts < 1 {
		attempts = 1
	}
	backoff := p.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	ready := p.ReadyTimeout
	if ready <= 0 {
		ready = 5 * time.Second
	}

	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			wait := backoff * time.Duration(attempt-1)
			p.logf("echproxy: attempt %d/%d: retrying in %v (last error: %v)", attempt, attempts, wait, last)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		err := p.startOnce(ctx, ready)
		if err == nil {
			return nil
		}
		last = err
		p.logf("echproxy: attempt %d/%d failed: %v", attempt, attempts, err)
	}
	return fmt.Errorf("echproxy: start failed after %d attempt(s): %w", attempts, last)
}

func (p *Process) startOnce(ctx context.Context, ready time.Duration) error {
	if PortBusy(p.Addr, 500*time.Millisecond) {
		return fmt.Errorf("%w: %s", ErrPortBusy, p.Addr)
	}

	runner := p.Runner
	if runner == nil {
		runner = execRunner{}
	}
	h, err := runner.Run(p.BinPath, p.Args(), p.WorkDir)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.h = h
	p.mu.Unlock()

	if err := p.waitReady(ctx, h, ready); err != nil {
		_ = h.Kill()
		_ = p.waitHandle(h, 3*time.Second)
		// Clear the handle: a failed attempt must not leave Alive()/Pid() pointing at a
		// dead child that the caller never asked for.
		p.mu.Lock()
		if p.h == h {
			p.h = nil
		}
		p.mu.Unlock()
		return fmt.Errorf("echproxy: listener did not appear on %s: %w", p.Addr, err)
	}
	p.logf("echproxy: ech-proxy listening on %s (pid %d)", p.Addr, h.Pid())
	return nil
}

func (p *Process) waitReady(ctx context.Context, h Handle, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-h.Done():
			return errors.New("child exited before binding its listener")
		default:
		}
		if PortBusy(p.Addr, 250*time.Millisecond) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-h.Done():
			return errors.New("child exited before binding its listener")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// Stop terminates the child and waits for it to exit. It is idempotent and safe to call
// when the child is already gone.
func (p *Process) Stop(ctx context.Context) error {
	p.mu.Lock()
	h := p.h
	p.h = nil
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	// Hard kill only. ech-proxy installs a graceful handler for os.Interrupt/SIGTERM
	// (win/main.go:63), but os.Process.Signal cannot deliver those reliably on Windows,
	// and ech-proxy keeps no local state of its own — it is a pure proxy — so there is
	// nothing valuable to drain.
	if err := h.Kill(); err != nil {
		p.logf("echproxy: kill pid %d: %v", h.Pid(), err)
	}
	return p.waitHandle(h, 5*time.Second)
}

func (p *Process) waitHandle(h Handle, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-h.Done():
		return nil
	case <-timer.C:
		return fmt.Errorf("echproxy: child pid %d did not exit within %s", h.Pid(), timeout)
	}
}

// Alive reports whether a child started by this Process is still running.
func (p *Process) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.h == nil {
		return false
	}
	select {
	case <-p.h.Done():
		return false
	default:
		return true
	}
}

// Pid returns the running child's pid, or 0.
func (p *Process) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.h == nil {
		return 0
	}
	return p.h.Pid()
}

// DefaultProcess builds a Process for the documented listen address.
func DefaultProcess(binPath, addr, ipMode, workDir string) *Process {
	return &Process{
		BinPath:      binPath,
		Addr:         addr,
		IPMode:       ipMode,
		WorkDir:      workDir,
		Attempts:     3,
		Backoff:      time.Second,
		ReadyTimeout: 5 * time.Second,
		Runner:       execRunner{},
	}
}
