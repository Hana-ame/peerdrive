package source

// source_test.go: Manager routing + LocalSource tests.
// Discovery background: the source system (unified file management) -- routing priority (local first),
// capability flags (CapStream chunking), statistics management (Snapshot), and the error fallback chain.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

func testHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// TestLocalSource_OpenCAS content-addressed storage reads (chunked + full).
func TestLocalSource_OpenCAS(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("hello-source-", 100)
	hash := testHash(content)
	// write the file in CAS layout: storageDir/<h[:2]>/<h>
	p := filepath.Join(dir, hash[:2], hash)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))

	s := NewLocalSource(dir, nil)
	require.True(t, s.Available(context.Background()))

	// chunked read (CapStream)
	r, err := s.Open(context.Background(), hash, 100, 50)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content[100:150], string(got), "chunked read must slice by offset/size")

	// full read
	r, err = s.Open(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	got, err = io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content, string(got))

	// an illegal hash is rejected
	_, err = s.Open(context.Background(), "short", 0, -1)
	require.Error(t, err, "invalid hash must be rejected")

	// not found
	_, err = s.Open(context.Background(), testHash("missing"), 0, -1)
	require.Error(t, err)
}

// TestLocalSource_IndexPriority a file_index mapping takes priority over CAS (the same hash exists in both places).
func TestLocalSource_IndexPriority(t *testing.T) {
	dir := t.TempDir()
	// fileIndex's allowed root = its uploadDir (checked by IsPathAllowed) -- the test files are written under it;
	// Create depends on SQLite persistence, so InitDB comes first (same pattern as the transport tests)
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	fi := transport.NewFileIndexService(idxDir)
	content := "indexed-content"
	path := filepath.Join(idxDir, "a.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	created, err := fi.Create(path)
	require.NoError(t, err)

	s := NewLocalSource(dir, fi)
	r, err := s.Open(context.Background(), created.Hash, 0, -1)
	require.NoError(t, err)
	got, _ := io.ReadAll(r)
	r.Close()
	assert.Equal(t, content, string(got), "should hit file_index mapped path")
}

// TestManager_RoutePriority routing: a local hit returns immediately; a local miss falls back to peer.
// A stub source (programmable success/failure) verifies the priority and the fallback chain.
func TestManager_RoutePriority(t *testing.T) {
	m := New()

	// stub sources: programmable
	local := &stubSource{name: "local", priority: 0, caps: CapStream, ok: true}
	peer := &stubSource{name: "peer", priority: 1, caps: CapStream, ok: false}
	url := &stubSource{name: "url", priority: 2, caps: CapFile, ok: true}
	require.NoError(t, m.Register(local))
	require.NoError(t, m.Register(peer))
	require.NoError(t, m.Register(url))
	// duplicate names are rejected
	require.Error(t, m.Register(&stubSource{name: "local"}))

	hash := testHash("x")

	// 1. local hit -> local is called, peer is not
	r, err := m.OpenRange(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	got, _ := io.ReadAll(r)
	r.Close()
	assert.Equal(t, "local-data", string(got))
	assert.Equal(t, []string{"local"}, local.calls)
	assert.Empty(t, peer.calls, "should not try peer after local hit")

	// 2. local miss -> fall back to peer (fails) -> url (CapFile skips OpenRange) -> all fail
	local.ok = false
	_, err = m.OpenRange(context.Background(), hash, 0, -1)
	require.Error(t, err, "local fail + peer fail + url non-streaming → must error")

	// 3. OpenAny: url (CapFile) can backstop a whole-file fetch
	peer.ok = false
	url.ok = true
	r, err = m.OpenAny(context.Background(), hash)
	require.NoError(t, err)
	got, _ = io.ReadAll(r)
	r.Close()
	assert.Equal(t, "file-data", string(got))

	// 4. runtime priority adjustment: move peer ahead of local
	require.NoError(t, m.SetPriority("peer", -1))
	local.ok = false
	peer.ok = true
	r, err = m.OpenRange(context.Background(), hash, 0, -1)
	require.NoError(t, err)
	got, _ = io.ReadAll(r)
	r.Close()
	assert.Equal(t, "peer-data", string(got))

	// 5. Snapshot management plane: statistics are recorded
	st := m.Snapshot()
	require.Len(t, st, 3)
	byName := map[string]SourceStatus{}
	for _, s := range st {
		byName[s.Name] = s
	}
	assert.True(t, byName["peer"].Stats.Success >= 1, "peer success count should be recorded")
	assert.True(t, byName["local"].Stats.Fail >= 1, "local fail count should be recorded")
}

// stubSource a programmable test source.
type stubSource struct {
	name     string
	priority int
	caps     Capability
	ok       bool

	calls []string
}

func (s *stubSource) Name() string             { return s.name }
func (s *stubSource) Type() string             { return "stub" }
func (s *stubSource) Capabilities() Capability { return s.caps }
func (s *stubSource) Priority() int            { return s.priority }
func (s *stubSource) SetPriority(p int)        { s.priority = p }
func (s *stubSource) Available(ctx context.Context) bool {
	s.calls = append(s.calls, s.name)
	return true
}
func (s *stubSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if !s.ok {
		return nil, errStub
	}
	return io.NopCloser(strings.NewReader(s.name + "-data")), nil
}
func (s *stubSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if !s.ok {
		return nil, errStub
	}
	return []byte("file-data"), nil
}
func (s *stubSource) Info(ctx context.Context, hash string) (*FileMeta, error) { return nil, nil }

var errStub = &stubErr{}

type stubErr struct{}

func (e *stubErr) Error() string { return "stub source failed" }

// TestLocalSourceControl verifies the LocalSource control plane: AddLocalFile adds a local file,
// WriteFile writes a file directly (discovery background: control-plane design doc/source-control.md, 2026-08-19).
func TestLocalSourceControl(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	storageDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)

	s := NewLocalSource(storageDir, idx)

	// 1. AddLocalFile: add an existing file to the source
	external := filepath.Join(idxDir, "existing.txt")
	content := "control-add-local"
	require.NoError(t, os.WriteFile(external, []byte(content), 0o644))
	meta, err := s.AddLocalFile(external)
	require.NoError(t, err)
	assert.Equal(t, testHash(content), meta.Hash)
	assert.Equal(t, int64(len(content)), meta.Size)
	assert.Equal(t, "existing.txt", meta.Name)

	// after adding, it should be readable through the local source
	r, err := s.Open(context.Background(), meta.Hash, 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content, string(got))

	// 2. WriteFile: write a file directly into the source
	content2 := "control-write-file"
	meta2, err := s.WriteFile("new.bin", strings.NewReader(content2))
	require.NoError(t, err)
	assert.Equal(t, testHash(content2), meta2.Hash)
	assert.Equal(t, int64(len(content2)), meta2.Size)
	assert.Equal(t, "new.bin", meta2.Name)

	r, err = s.Open(context.Background(), meta2.Hash, 0, -1)
	require.NoError(t, err)
	got, err = io.ReadAll(r)
	r.Close()
	require.NoError(t, err)
	assert.Equal(t, content2, string(got))
}

// TestLocalSourceControl_NilFileIndex verifies that with a nil fileIndex, AddLocalFile / WriteFile
// return ErrControlUnsupported (discovery background: NewLocalSource allows a nil fileIndex, so the
// control plane must reject it explicitly).
func TestLocalSourceControl_NilFileIndex(t *testing.T) {
	s := NewLocalSource(t.TempDir(), nil)
	_, err := s.AddLocalFile("/some/path")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = s.WriteFile("x.bin", strings.NewReader("x"))
	assert.ErrorIs(t, err, ErrControlUnsupported)
}

// TestLocalSourceControl_AddLocalFileOutsideRoot adding a file outside the root -> rejected.
func TestLocalSourceControl_AddLocalFileOutsideRoot(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))
	_, err := s.AddLocalFile(outside)
	assert.Error(t, err, "file outside root must be rejected")
}

// TestLocalSourceControl_AddLocalFileDuplicate adding the same file twice returns the same hash.
func TestLocalSourceControl_AddLocalFileDuplicate(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	inRoot := filepath.Join(idxDir, "dup.bin")
	require.NoError(t, os.WriteFile(inRoot, []byte("dup"), 0o644))
	meta1, err := s.AddLocalFile(inRoot)
	require.NoError(t, err)
	meta2, err := s.AddLocalFile(inRoot)
	require.NoError(t, err)
	assert.Equal(t, meta1.Hash, meta2.Hash, "duplicate add of same file returns same hash")
}

// TestLocalSourceControl_WriteFileEmptyReader an empty reader -> an empty file can be written.
func TestLocalSourceControl_WriteFileEmptyReader(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	meta, err := s.WriteFile("empty.bin", strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, testHash(""), meta.Hash)
	assert.Equal(t, int64(0), meta.Size)
}

// TestLocalSourceControl_WriteFileNilReader a nil reader -> error.
func TestLocalSourceControl_WriteFileNilReader(t *testing.T) {
	require.NoError(t, repository.InitDB(":memory:"))
	idxDir := t.TempDir()
	idx := transport.NewFileIndexService(idxDir)
	s := NewLocalSource(t.TempDir(), idx)

	_, err := s.WriteFile("nil.bin", nil)
	assert.Error(t, err, "nil reader must be rejected")
}

// TestManager_Get verifies Manager.Get looks up a source by name (discovery background: needed for control-plane routing).
func TestManager_Get(t *testing.T) {
	m := New()
	local := &stubSource{name: "local", priority: 0, caps: CapStream, ok: true}
	peer := &stubSource{name: "peer", priority: 1, caps: CapStream, ok: false}
	require.NoError(t, m.Register(local))
	require.NoError(t, m.Register(peer))

	got := m.Get("local")
	require.NotNil(t, got)
	assert.Equal(t, "local", got.Name())

	got = m.Get("peer")
	require.NotNil(t, got)
	assert.Equal(t, "peer", got.Name())

	got = m.Get("nonexistent")
	assert.Nil(t, got, "nonexistent name returns nil")
}

// TestLocalControlOf_NonLocalSource a non-LocalSource must not support LocalControl.
func TestLocalControlOf_NonLocalSource(t *testing.T) {
	dir := t.TempDir()
	// URLSource does not support LocalControl
	urlSrc := NewURLSource("https://example.com/%s", nil)
	_, ok := LocalControlOf(urlSrc)
	assert.False(t, ok, "URLSource should not support LocalControl")

	// LocalSource should support it
	ls := NewLocalSource(dir, nil)
	_, ok = LocalControlOf(ls)
	assert.True(t, ok, "LocalSource should support LocalControl")
}

// TestBTControl_NilClient a nil client -> every method returns ErrControlUnsupported.
func TestBTControl_NilClient(t *testing.T) {
	ctrl := NewBTControl(nil)
	_, err := ctrl.DownloadTorrent([]byte("data"))
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = ctrl.DownloadMagnet("magnet:?xt=urn:btih:xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	list := ctrl.ListDownloads()
	assert.Nil(t, list, "nil client returns nil list")
	ds := ctrl.GetDownload("xxx")
	assert.Nil(t, ds, "nil client returns nil status")
	err = ctrl.PauseDownload("xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	err = ctrl.ResumeDownload("xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	err = ctrl.RemoveDownload("xxx")
	assert.ErrorIs(t, err, ErrControlUnsupported)
}

// TestBTControlOf_NonBTSource a non-BT source must not support BTControl.
func TestBTControlOf_NonBTSource(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocalSource(dir, nil)
	_, ok := any(ls).(BTControl)
	assert.False(t, ok, "LocalSource should not support BTControl")

	// btController should support it
	bc := NewBTControl(nil)
	_, ok = any(bc).(BTControl)
	assert.True(t, ok, "btController should support BTControl")
}

// TestIPFSControl_NilProvider a nil provider -> every method returns ErrControlUnsupported.
func TestIPFSControl_NilProvider(t *testing.T) {
	ctrl := NewIPFSControl(nil, t.TempDir())
	_, err := ctrl.PinCID("QmTest")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	err = ctrl.UnpinCID("QmTest")
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = ctrl.ListPins()
	assert.ErrorIs(t, err, ErrControlUnsupported)
	_, err = ctrl.GatewayStatus()
	assert.ErrorIs(t, err, ErrControlUnsupported)
}

// ─────────────────────────────────────────────────────────────────
// Module-level integration tests: the Source control-plane subsystem
// ─────────────────────────────────────────────────────────────────

// TestManager_ControlRegistration verifies the Manager can register and retrieve control-plane instances.
func TestManager_ControlRegistration(t *testing.T) {
	m := New()

	// register the Local source (required, because control-plane operations need it)
	local := NewLocalSource(t.TempDir(), nil)
	require.NoError(t, m.Register(local))

	// register BTControl
	btCtrl := NewBTControl(nil)
	m.SetBTControl(btCtrl)
	assert.NotNil(t, m.GetBTControl())

	// register IPFSControl
	ipfsCtrl := NewIPFSControl(nil, t.TempDir())
	m.SetIPFSControl(ipfsCtrl)
	assert.NotNil(t, m.GetIPFSControl())

	// verify the source still works normally
	ls := m.Get("local")
	require.NotNil(t, ls)
	assert.Equal(t, "local", ls.Name())

	// verify the control planes are each independent
	assert.NotNil(t, m.GetBTControl())
	assert.NotNil(t, m.GetIPFSControl())
}

// TestManager_ControlNilDefault verifies that the Get methods return nil when no control plane is set,
// and that control-plane state is never shared between Manager instances (regression: it used to be a
// package-level global var, so a Set in one test polluted the next test's Get, and nil had to be
// defensively cleared).
func TestManager_ControlNilDefault(t *testing.T) {
	m := New()
	// a fresh instance is nil by default (zero value), so no residue from a previous round is needed
	assert.Nil(t, m.GetBTControl(), "should return nil when BTControl not set")
	assert.Nil(t, m.GetIPFSControl(), "should return nil when IPFSControl not set")

	// instance isolation: after m1 sets a control plane, another instance m2 is unaffected (a global var fix would miss this)
	m1 := New()
	m1.SetBTControl(NewBTControl(nil))
	m1.SetIPFSControl(NewIPFSControl(nil, t.TempDir()))
	m2 := New()
	assert.Nil(t, m2.GetBTControl(), "another instance should not see m1's BTControl")
	assert.Nil(t, m2.GetIPFSControl(), "another instance should not see m1's IPFSControl")
	assert.NotNil(t, m1.GetBTControl(), "m1's own BTControl still valid")
}

// TestIPFSGatewayStatus_MockProvider tests GatewayStatus with a mock provider.
// No real HTTP request is needed (the provider carries its own HTTP client, but the test can build an empty one).
func TestIPFSGatewayStatus_MockProvider(t *testing.T) {
	// an empty provider (no gateways) -> GatewayStatus returns ErrControlUnsupported
	prov := provider.NewIPFSProvider([]string{})
	ctrl := NewIPFSControl(prov, t.TempDir())
	_, err := ctrl.GatewayStatus()
	assert.ErrorIs(t, err, ErrControlUnsupported, "empty gateways should return ErrControlUnsupported")

	// gateways configured but no network -> returns an offline status (no error)
	prov2 := provider.NewIPFSProvider([]string{"https://nonexistent-gateway.example.com"})
	ctrl2 := NewIPFSControl(prov2, t.TempDir())
	status, err := ctrl2.GatewayStatus()
	// even when the network is unreachable, no error should be returned (each gateway's status is independent)
	require.NoError(t, err)
	require.Len(t, status, 1)
	assert.False(t, status[0].Online, "nonexistent gateway should be marked offline")
}
