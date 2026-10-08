package twimg

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newModuleForTest builds a Module whose downloader reads from an in-process fake release
// server and whose subprocess is a fake. Nothing real is downloaded and nothing real is
// spawned, so the whole Start/Stop path is exercisable on any platform.
func newModuleForTest(t *testing.T) (*Module, *fakeReleaseServer, *fakeRunner) {
	t.Helper()

	body := []byte("MZ\x00\x00fake-windows-binary")
	sum := sha256Hex(body)
	f := newFakeReleaseServer(t, map[string]string{assetName: sum}, map[string][]byte{assetName: body})
	t.Cleanup(f.server.Close)

	// A free local address for the subprocess to bind.
	addr, ln := freeAddr(t)
	_ = ln.Close()

	m, err := New(Config{
		Entry: Entry{
			SrcHost:    "pbs.twimg.com",
			EntryHost:  "twimg-pbs.l.moonchan.xyz",
			Port:       "8443",
			ListenAddr: addr,
			SkipTLS:    true,
		},
		Repo:         "Hana-ame/ech-proxy",
		Version:      "v1.3.0",
		Asset:        assetName,
		InstallDir:   t.TempDir(),
		IPMode:       "v4",
		Attempts:     1,
		Backoff:      time.Millisecond,
		StartTimeout: 20 * time.Second,
	})
	require.NoError(t, err)

	m.dl.Client = f.client()

	runner := &fakeRunner{factory: func() Handle {
		h := newControlledHandle(31337)
		h.bind(t, addr) // the readiness probe succeeds
		return h
	}}
	m.proc.Runner = runner
	return m, f, runner
}

// TestModule_StartStop is the full facade lifecycle: download → spawn → ready → stop.
func TestModule_StartStop(t *testing.T) {
	m, _, runner := newModuleForTest(t)

	require.Nil(t, m.Client(), "Client must be nil before Start")
	assert.False(t, m.Alive())

	require.NoError(t, m.Start(context.Background()))

	// The verified binary was installed and exactly one child was spawned.
	_, err := os.Stat(m.BinaryPath())
	require.NoError(t, err, "the binary must be installed")
	assert.Equal(t, 1, runner.Calls())
	require.NotEmpty(t, runner.records[0].args)
	assert.Contains(t, runner.records[0].args, m.Addr())
	assert.Contains(t, runner.records[0].args, "-no-browser")
	assert.Contains(t, runner.records[0].args, "-ip-mode")

	client := m.Client()
	require.NotNil(t, client, "Client must be non-nil after a successful Start")
	_, ok := client.Transport.(*RoundTripper)
	require.True(t, ok, "the client transport must be the rewriting RoundTripper")
	assert.True(t, m.Alive())
	assert.Equal(t, 31337, m.Pid())

	require.NoError(t, m.Stop(context.Background()))
	assert.Nil(t, m.Client())
	assert.False(t, m.Alive())
}

// TestModule_Start_Idempotent: a second Start while running is a no-op, not a second spawn.
func TestModule_Start_Idempotent(t *testing.T) {
	m, _, runner := newModuleForTest(t)

	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Start(context.Background()))
	assert.Equal(t, 1, runner.Calls(), "Start must be idempotent")
	assert.True(t, m.Alive())
	require.NoError(t, m.Stop(context.Background()))
}

// TestModule_Stop_Idempotent is safe to call before Start and again after Stop.
func TestModule_Stop_Idempotent(t *testing.T) {
	m, _, runner := newModuleForTest(t)

	require.NoError(t, m.Stop(context.Background()))
	assert.Nil(t, m.Client())

	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Stop(context.Background()))
	require.NoError(t, m.Stop(context.Background()))
	assert.Equal(t, 1, runner.Calls())
}

// TestModule_Start_DownloadFailure fails loudly and never spawns a subprocess. There is
// deliberately no silent fallback to direct pbs.twimg.com traffic — an operator who opted
// into the module must know it did not come up.
func TestModule_Start_DownloadFailure(t *testing.T) {
	m, f, runner := newModuleForTest(t)
	f.assetStatus = 503

	err := m.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 503")
	assert.Equal(t, 0, runner.Calls(), "no subprocess may be spawned on download failure")
	assert.Nil(t, m.Client())
	assert.False(t, m.Alive())

	entries, rerr := os.ReadDir(m.cfg.InstallDir)
	assert.True(t, os.IsNotExist(rerr) || len(entries) == 0, "no partial files may remain")
}

// TestModule_Start_PortBusy propagates ErrPortBusy through the facade.
func TestModule_Start_PortBusy(t *testing.T) {
	m, _, runner := newModuleForTest(t)

	// Occupy the port the module will try to bind.
	ln, err := net.Listen("tcp", m.Addr())
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	err = m.Start(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPortBusy)
	assert.Equal(t, 0, runner.Calls(), "nothing may be spawned on a busy port")
	assert.Nil(t, m.Client())
}

// TestModule_ReusesInstalledBinary across Stop/Start without re-download.
func TestModule_ReusesInstalledBinary(t *testing.T) {
	m, f, runner := newModuleForTest(t)

	require.NoError(t, m.Start(context.Background()))
	require.EqualValues(t, 1, f.assetsFetched.Load())
	require.NoError(t, m.Stop(context.Background()))

	require.NoError(t, m.Start(context.Background()))
	assert.EqualValues(t, 1, f.assetsFetched.Load(), "a verified binary must not be re-download")
	assert.Equal(t, 2, runner.Calls())
	assert.True(t, m.Alive())
	require.NoError(t, m.Stop(context.Background()))
}

// TestModule_ExposesConfig surfaces the resolved settings for operators to inspect.
func TestModule_ExposesConfig(t *testing.T) {
	m, _, _ := newModuleForTest(t)
	e := m.Entry()
	assert.Equal(t, "pbs.twimg.com", e.SrcHost)
	assert.Equal(t, "twimg-pbs.l.moonchan.xyz", e.EntryHost)
	assert.Equal(t, "8443", e.Port)
	assert.NotEmpty(t, e.ListenAddr)
	assert.NotEmpty(t, m.BinaryPath())
	assert.NotEmpty(t, m.Addr())
}

// TestNew_Defaults fills every zero field with the documented default.
// Discovery background: New derives the release asset from the running platform, and there is
// no darwin-arm64 asset (CI macos arm64 cell). Skip rather than assert a designed failure.
func TestNew_Defaults(t *testing.T) {
	skipIfUnsupportedPlatform(t)
	m, err := New(Config{InstallDir: t.TempDir()})
	require.NoError(t, err)

	e := m.Entry()
	assert.Equal(t, "pbs.twimg.com", e.SrcHost)
	assert.Equal(t, "twimg-pbs.l.moonchan.xyz", e.EntryHost)
	assert.Equal(t, "8443", e.Port)
	assert.Equal(t, "127.0.0.1:8443", e.ListenAddr)
	assert.True(t, e.SkipTLS, "a zero Entry must get the documented SkipTLS default")

	assert.Equal(t, "Hana-ame/ech-proxy", m.cfg.Repo)
	assert.Equal(t, "v1.3.0", m.cfg.Version)
	asset, err := DefaultAssetForCurrent()
	require.NoError(t, err)
	assert.Equal(t, asset, m.cfg.Asset, "the asset must be derived from the running platform")
	assert.Equal(t, 3, m.cfg.Attempts)
	assert.Equal(t, time.Second, m.cfg.Backoff)
	assert.Equal(t, 5*time.Second, m.cfg.ReadyTimeout)
	assert.Equal(t, 60*time.Second, m.cfg.StartTimeout)
}

// TestNew_Validation rejects bad configuration before any I/O happens.
func TestNew_Validation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cfg   Config
		errIn string
	}{
		{"missing install dir", Config{}, "Config.InstallDir is required"},
		{"blank install dir", Config{InstallDir: "   "}, "Config.InstallDir is required"},
		{"bad port", Config{
			InstallDir: t.TempDir(),
			Entry: Entry{SrcHost: "pbs.twimg.com", EntryHost: "x",
				Port: "bad", ListenAddr: "127.0.0.1:8443"},
		}, "not a valid port"},
		{"bad listen address", Config{
			InstallDir: t.TempDir(),
			Entry:      Entry{SrcHost: "h", EntryHost: "x", Port: "8443", ListenAddr: "nope"},
		}, "not a valid host:port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errIn)
		})
	}
}

// TestNew_UnsupportedPlatform refuses a platform with no published binary instead of
// guessing an asset name that will 404 at startup.
func TestNew_UnsupportedPlatform(t *testing.T) {
	old := assets
	assets = map[string]string{"windows-amd64": "ech-proxy-windows-amd64.exe"}
	t.Cleanup(func() { assets = old })

	_, err := New(Config{InstallDir: t.TempDir()})
	require.Error(t, err)
}

// TestModule_ConcurrentAccess reads the read-only accessors while the test goroutine cycles
// Start/Stop, so the accessors observe every state transition (nil client, started, stopped).
// Run with -race to prove the client/started pair and the proc fields are consistently guarded.
func TestModule_ConcurrentAccess(t *testing.T) {
	m, _, _ := newModuleForTest(t)

	var readers sync.WaitGroup
	done := make(chan struct{})
	for i := 0; i < 3; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = m.Client()
				_ = m.Alive()
				_ = m.Entry()
				_ = m.Pid()
				_ = m.BinaryPath()
				_ = m.Addr()
			}
		}()
	}

	// Release the readers after a short moment so they overlap the cycles below.
	go func() { time.Sleep(200 * time.Millisecond); close(done) }()

	for i := 0; i < 20; i++ {
		if err := m.Start(context.Background()); err != nil {
			t.Errorf("start cycle %d: %v", i, err)
			break
		}
		if err := m.Stop(context.Background()); err != nil {
			t.Errorf("stop cycle %d: %v", i, err)
			break
		}
	}
	readers.Wait()

	assert.False(t, m.Alive(), "the module must be stopped after the last cycle")
	assert.Nil(t, m.Client())
}
