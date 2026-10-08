package twimg

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes: the lifecycle logic must be testable without a real executable ---

// controlledHandle is a fake child. It can be told to actually bind addr (so the
// readiness probe succeeds) and its Done channel is under the test's control.
//
// autoExit makes Kill close Done, i.e. models a well-behaved child that exits after
// being killed. Set it false to model a child that ignores the kill.
type controlledHandle struct {
	pid      int
	done     chan struct{}
	kills    atomic.Int64
	once     sync.Once
	listen   net.Listener
	autoExit bool
}

func newControlledHandle(pid int) *controlledHandle {
	return &controlledHandle{pid: pid, done: make(chan struct{}), autoExit: true}
}

func (h *controlledHandle) Pid() int { return h.pid }

func (h *controlledHandle) Kill() error {
	h.kills.Add(1)
	// A real child stops serving on its socket when it dies; mirror that so a second
	// Start in the same test can bind the address again.
	if h.listen != nil {
		_ = h.listen.Close()
		h.listen = nil
	}
	if h.autoExit {
		h.exit()
	}
	return nil
}

func (h *controlledHandle) Done() <-chan struct{} { return h.done }

// exit simulates the child terminating on its own.
func (h *controlledHandle) exit() {
	h.once.Do(func() { close(h.done) })
}

// bind listens on addr so PortBusy reports it as occupied (readiness satisfied).
func (h *controlledHandle) bind(t *testing.T, addr string) {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	h.listen = ln
	t.Cleanup(func() { _ = ln.Close() })
}

type spawnRecord struct {
	binary string
	args   []string
	dir    string
}

// fakeRunner records every spawn and returns handles from a factory.
type fakeRunner struct {
	mu      sync.Mutex
	records []spawnRecord
	factory func() Handle
	fail    error
}

func (f *fakeRunner) Run(binary string, args []string, dir string) (Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, spawnRecord{binary: binary, args: args, dir: dir})
	if f.fail != nil {
		return nil, f.fail
	}
	if f.factory == nil {
		return newControlledHandle(1000), nil
	}
	return f.factory(), nil
}

func (f *fakeRunner) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.records)
}

// freeAddr binds a listener and returns the address it got plus the listener.
func freeAddr(t *testing.T) (string, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), ln
}

// TestProcess_Args pins the exact ech-proxy argument list. -http must never appear:
// the module rewrites https://pbs.twimg.com to https://twimg-pbs.l.moonchan.xyz:8443 and
// would be downgrading the scheme.
func TestProcess_Args(t *testing.T) {
	t.Run("with ip mode", func(t *testing.T) {
		p := &Process{Addr: "127.0.0.1:8443", IPMode: "v4"}
		assert.Equal(t, []string{"-addr", "127.0.0.1:8443", "-no-browser", "-ip-mode", "v4"}, p.Args())
	})
	t.Run("without ip mode", func(t *testing.T) {
		p := &Process{Addr: "127.0.0.1:8443"}
		assert.Equal(t, []string{"-addr", "127.0.0.1:8443", "-no-browser"}, p.Args())
	})
	t.Run("never passes -http", func(t *testing.T) {
		p := &Process{Addr: "127.0.0.1:8443", IPMode: "auto"}
		for _, a := range p.Args() {
			assert.NotEqual(t, "-http", a, "http mode would downgrade https to http")
		}
	})
}

// TestPortBusy distinguishes a bound port from a free one.
func TestPortBusy(t *testing.T) {
	t.Run("occupied", func(t *testing.T) {
		addr, _ := freeAddr(t)
		assert.True(t, PortBusy(addr, 500*time.Millisecond))
	})
	t.Run("free", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := ln.Addr().String()
		_ = ln.Close() // release it, so nothing answers anymore
		assert.False(t, PortBusy(addr, 200*time.Millisecond))
	})
}

// TestProcess_Start_Success spawns, waits for readiness and reports alive.
func TestProcess_Start_Success(t *testing.T) {
	addr, ln := freeAddr(t) // holds the port so nothing else can take it
	_ = ln.Close()          // release it; the fake runner re-binds it

	runner := &fakeRunner{factory: func() Handle {
		h := newControlledHandle(4242)
		h.bind(t, addr) // re-bind the address so the readiness probe succeeds
		return h
	}}

	p := &Process{
		BinPath: "/tmp/ech-proxy-windows-amd64.exe", Addr: addr, IPMode: "v4",
		Attempts: 1, Backoff: time.Millisecond, ReadyTimeout: 3 * time.Second, Runner: runner,
	}
	require.NoError(t, p.Start(context.Background()))

	assert.True(t, p.Alive())
	assert.Equal(t, 4242, p.Pid())
	assert.Equal(t, 1, runner.Calls())
	assert.Equal(t, "/tmp/ech-proxy-windows-amd64.exe", runner.records[0].binary)
	assert.Contains(t, runner.records[0].args, "-addr")
	assert.Contains(t, runner.records[0].args, addr)
	assert.Contains(t, runner.records[0].args, "-no-browser")
}

// TestProcess_Start_PortBusy is the port-conflict path: the module must refuse to start
// and must not kill whatever is already listening.
func TestProcess_Start_PortBusy(t *testing.T) {
	addr, ln := freeAddr(t) // stays open for the whole test
	_ = ln

	runner := &fakeRunner{}
	p := &Process{
		BinPath: "/tmp/binary", Addr: addr,
		Attempts: 2, Backoff: time.Millisecond, ReadyTimeout: 500 * time.Millisecond, Runner: runner,
	}
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPortBusy)
	assert.Contains(t, err.Error(), addr)
	assert.Equal(t, 0, runner.Calls(), "nothing may be spawned when the port is busy")
	assert.False(t, p.Alive())
}

// TestProcess_Start_ReadinessFailure kills the child on failure and retries up to Attempts
// times before giving up — no orphan process is left behind.
func TestProcess_Start_ReadinessFailure(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln.Close()

	var handles []*controlledHandle
	runner := &fakeRunner{factory: func() Handle {
		h := newControlledHandle(1)
		handles = append(handles, h)
		return h // never binds addr → readiness always times out
	}}
	p := &Process{
		BinPath: "/tmp/binary", Addr: addr,
		Attempts: 3, Backoff: time.Millisecond, ReadyTimeout: 150 * time.Millisecond, Runner: runner,
	}
	start := time.Now()
	err := p.Start(context.Background())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrPortBusy), "a readiness timeout is not a port conflict")
	assert.Contains(t, err.Error(), "3 attempt(s)")
	assert.Contains(t, err.Error(), "listener did not appear")
	assert.Equal(t, 3, runner.Calls())
	for _, h := range handles {
		assert.EqualValues(t, 1, h.kills.Load(), "every failed attempt must kill its child")
	}
	assert.False(t, p.Alive())
	assert.Equal(t, 0, p.Pid())
	assert.GreaterOrEqual(t, elapsed, 2*time.Millisecond, "backoff should have been applied")
}

// TestProcess_Start_SpawnFailure retries and surfaces the runner error.
func TestProcess_Start_SpawnFailure(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln.Close()

	runner := &fakeRunner{fail: errors.New("exec: not found")}
	p := &Process{
		BinPath: "/no/such/binary", Addr: addr,
		Attempts: 2, Backoff: time.Millisecond, ReadyTimeout: 200 * time.Millisecond, Runner: runner,
	}
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec: not found")
	assert.Equal(t, 2, runner.Calls())
}

// TestProcess_Start_ChildExitsEarly fails fast instead of burning the whole ReadyTimeout.
func TestProcess_Start_ChildExitsEarly(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln.Close()

	runner := &fakeRunner{factory: func() Handle {
		h := newControlledHandle(7)
		h.exit() // the child dies immediately
		return h
	}}
	p := &Process{
		BinPath: "/tmp/binary", Addr: addr,
		Attempts: 1, Backoff: time.Millisecond, ReadyTimeout: 5 * time.Second, Runner: runner,
	}
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "child exited before binding")
	assert.False(t, p.Alive())
}

// TestProcess_Start_AlreadyRunning refuses to double-spawn.
func TestProcess_Start_AlreadyRunning(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln.Close()

	runner := &fakeRunner{factory: func() Handle {
		h := newControlledHandle(5)
		h.bind(t, addr)
		return h
	}}
	p := &Process{BinPath: "/tmp/binary", Addr: addr, Runner: runner, ReadyTimeout: 3 * time.Second}
	require.NoError(t, p.Start(context.Background()))
	err := p.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already running")
	assert.Equal(t, 1, runner.Calls())
}

// TestProcess_Start_ContextCancelled honours the caller's deadline.
func TestProcess_Start_ContextCancelled(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln

	runner := &fakeRunner{}
	p := &Process{
		BinPath: "/tmp/binary", Addr: addr,
		Attempts: 5, Backoff: 50 * time.Millisecond, ReadyTimeout: time.Second, Runner: runner,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	err := p.Start(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestProcess_Start_Validation catches empty configuration.
func TestProcess_Start_Validation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		p     *Process
		errIn string
	}{
		{"empty bin", &Process{Addr: "127.0.0.1:1"}, "BinPath is empty"},
		{"empty addr", &Process{BinPath: "/x"}, "Addr is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Start(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errIn)
		})
	}
}

// TestProcess_Stop kills the child, clears the handle, and is idempotent.
func TestProcess_Stop(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln.Close()

	h := newControlledHandle(999)
	runner := &fakeRunner{factory: func() Handle {
		h.bind(t, addr) // bind at spawn time, after the port-busy check
		return h
	}}
	p := &Process{BinPath: "/tmp/binary", Addr: addr, Runner: runner, ReadyTimeout: 3 * time.Second}

	require.NoError(t, p.Stop(context.Background())) // never started: no-op, no error
	assert.False(t, p.Alive())

	require.NoError(t, p.Start(context.Background()))
	assert.True(t, p.Alive())
	assert.Equal(t, 999, p.Pid())

	require.NoError(t, p.Stop(context.Background()))
	assert.EqualValues(t, 1, h.kills.Load())
	assert.False(t, p.Alive())
	assert.Equal(t, 0, p.Pid())

	// A second stop is a no-op: no second kill, no error.
	require.NoError(t, p.Stop(context.Background()))
	assert.EqualValues(t, 1, h.kills.Load(), "Kill must not be called twice")
}

// TestProcess_Stop_WaitTimeout reports a child that ignores the kill instead of hanging.
func TestProcess_Stop_WaitTimeout(t *testing.T) {
	addr, ln := freeAddr(t)
	_ = ln.Close()

	stub := newControlledHandle(1)
	stub.autoExit = false // ignores the kill
	runner := &fakeRunner{factory: func() Handle {
		stub.bind(t, addr) // readiness passes; Done never closes → Stop times out
		return stub
	}}
	p := &Process{BinPath: "/tmp/binary", Addr: addr, Runner: runner, ReadyTimeout: 3 * time.Second}
	require.NoError(t, p.Start(context.Background()))

	err := p.Stop(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not exit within")
	assert.EqualValues(t, 1, stub.kills.Load())
}

// TestProcess_DefaultProcess wires the documented retry policy.
func TestProcess_DefaultProcess(t *testing.T) {
	p := DefaultProcess("/tmp/binary", "127.0.0.1:8443", "v4", "/tmp/dir")
	assert.Equal(t, 3, p.Attempts)
	assert.Equal(t, time.Second, p.Backoff)
	assert.Equal(t, 5*time.Second, p.ReadyTimeout)
	assert.NotNil(t, p.Runner)
	assert.Equal(t, "127.0.0.1:8443", p.Addr)
	assert.Equal(t, "/tmp/binary", p.BinPath)
	assert.Equal(t, "/tmp/dir", p.WorkDir)
}
