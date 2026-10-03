package service

// Test background (doc/NETDISK.md M3): cross-node pull-and-save is the step where
// "the user clicks save → the file actually lands on my side", and four risks must be
// pinned down:
//  1. content from the peer must be verified against sha256 (otherwise the peer could
//     write arbitrary content to this node);
//  2. the path given by the peer must be sanitized (otherwise "../" is arbitrary file write);
//  3. cancel must really stop and clean up the partial file (a leftover .part would be
//     mistaken for a result);
//  4. if local content with the same hash already exists, skip it (content-addressed dedup,
//     don't download it for nothing).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePullSource a fake data plane: returns preset content by hash.
type fakePullSource struct {
	content map[string][]byte
	err     error
	block   chan struct{} // when non-nil, Read blocks until Close (used by the cancel test)
}

func (f *fakePullSource) OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	data, ok := f.content[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	if f.block != nil {
		return &blockingReader{data: data, release: f.block}, nil
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// blockingReader blocks after reading the first chunk until Close is called (simulates "in transfer").
type blockingReader struct {
	data    []byte
	off     int
	release chan struct{}
	once    sync.Once
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if b.off == 0 {
		n := copy(p, b.data)
		b.off += n
		return n, nil
	}
	<-b.release
	return 0, errors.New("closed")
}

func (b *blockingReader) Close() error {
	b.once.Do(func() { close(b.release) })
	return nil
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// newPullerForTest builds a pull service with a fake data plane + fake registration.
func newPullerForTest(t *testing.T, src *fakePullSource, local []string) (*PeerPuller, string, *[]string) {
	t.Helper()
	root := t.TempDir()
	p := NewPeerPuller(root)
	p.SetSource(src)
	registered := &[]string{}
	localSet := map[string]bool{}
	for _, h := range local {
		localSet[h] = true
	}
	p.SetFileAccess(
		func(hash string) bool { return localSet[hash] },
		func(path string) (string, int64, error) {
			data, err := os.ReadFile(path)
			if err != nil {
				return "", 0, err
			}
			*registered = append(*registered, path)
			return hashOf(data), int64(len(data)), nil
		},
	)
	return p, root, registered
}

// waitJob waits for a job to reach a terminal state (pulling is asynchronous).
func waitJob(t *testing.T, p *PeerPuller, id string) PullJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := p.Get(id)
		if !ok {
			t.Fatalf("job %s does not exist", id)
		}
		if j.Done() {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s timed out before finishing", id)
	return PullJob{}
}

// TestStartPullSavesAndRegisters normal pull: saved to pulled/<relative path> and registered.
// Discovery background: the user ask is simply "select a file and save, and I can download it
// from someone else" -- here we assert the save result is a **real file** (matching size and
// content) and that it is registered ("my files" can see it).
func TestStartPullSavesAndRegisters(t *testing.T) {
	content := []byte("remote file content")
	h := hashOf(content)
	src := &fakePullSource{content: map[string][]byte{h: content}}
	p, root, registered := newPullerForTest(t, src, nil)

	job, err := p.Start("peer-a", h, "readme.txt", "docs/readme.txt", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, p, job.ID)
	if done.Status != PullDone {
		t.Fatalf("status = %s (err=%s), want done", done.Status, done.Error)
	}
	if done.Skipped {
		t.Fatal("should not skip (no content locally)")
	}
	want := filepath.Join(root, "pulled", "docs", "readme.txt")
	if done.SavedTo != want {
		t.Fatalf("saved_to = %s, want %s", done.SavedTo, want)
	}
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: %q", got)
	}
	if done.Received != int64(len(content)) {
		t.Fatalf("received = %d, want %d", done.Received, len(content))
	}
	if len(*registered) != 1 || (*registered)[0] != want {
		t.Fatalf("registered path mismatch: %v", *registered)
	}
	// no .part may be left behind
	if _, err := os.Stat(want + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part residue: %v", err)
	}
}

// TestStartPullSkipsWhenLocal the same hash already exists locally → skip the download.
// Discovery background: content addressing dedupes naturally; "saving" the same file again
// should not go over the network a second time.
func TestStartPullSkipsWhenLocal(t *testing.T) {
	content := []byte("already here")
	h := hashOf(content)
	src := &fakePullSource{content: map[string][]byte{h: content}}
	p, _, registered := newPullerForTest(t, src, []string{h})

	job, err := p.Start("peer-a", h, "x.bin", "x.bin", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, p, job.ID)
	if done.Status != PullDone || !done.Skipped {
		t.Fatalf("want done+skipped, got %+v", done)
	}
	if len(*registered) != 0 {
		t.Fatal("should not re-register when skipped")
	}
}

// TestStartPullHashMismatch peer content does not match the requested hash → fail, leaving no file.
// Discovery background: not verifying means letting the peer write arbitrary content to this
// node; and the .part must be deleted, otherwise the next rename would promote garbage to a
// real file.
func TestStartPullHashMismatch(t *testing.T) {
	want := []byte("expected content")
	other := []byte("evil content")
	h := hashOf(want)
	src := &fakePullSource{content: map[string][]byte{h: other}}
	p, root, registered := newPullerForTest(t, src, nil)

	job, err := p.Start("peer-a", h, "a.txt", "a.txt", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	done := waitJob(t, p, job.ID)
	if done.Status != PullFailed || !strings.Contains(done.Error, "hash mismatch") {
		t.Fatalf("want failed/hash mismatch, got %+v", done)
	}
	target := filepath.Join(root, "pulled", "a.txt")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("no final file should remain after validation failure")
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatal("no .part should remain after validation failure")
	}
	if len(*registered) != 0 {
		t.Fatal("should not register after validation failure")
	}
}

// TestStartPullCleansPathTraversal a path given by the peer must not escape the save directory.
// Discovery background: relPath comes from the peer (the collection entry path), so an
// unsanitized "../../etc/passwd" or absolute path is arbitrary file write.
func TestStartPullCleansPathTraversal(t *testing.T) {
	content := []byte("traversal")
	h := hashOf(content)
	src := &fakePullSource{content: map[string][]byte{h: content}}
	p, root, _ := newPullerForTest(t, src, nil)

	cases := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"..\\..\\windows\\system32\\cfg",
		"a/../../b.txt",
	}
	pulledRoot := filepath.Join(root, "pulled")
	for _, in := range cases {
		job, err := p.Start("peer-a", h, "", in, "")
		if err != nil {
			t.Fatalf("start(%q): %v", in, err)
		}
		done := waitJob(t, p, job.ID)
		if done.Status != PullDone {
			t.Fatalf("%q: status=%s err=%s", in, done.Status, done.Error)
		}
		abs, err := filepath.Abs(done.SavedTo)
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		if !strings.HasPrefix(abs, pulledRoot+string(filepath.Separator)) {
			t.Fatalf("%q escaped the save directory: %s", in, abs)
		}
	}
}

// TestStartPullCancel cancel: the job is marked cancelled, the temp file is cleaned, and no more writing happens.
// Discovery background: the user may misclick or change their mind; a half-written file left
// on disk would be mistaken for a saved one.
func TestStartPullCancel(t *testing.T) {
	content := []byte("slow content")
	h := hashOf(content)
	release := make(chan struct{})
	src := &fakePullSource{content: map[string][]byte{h: content}, block: release}
	p, root, _ := newPullerForTest(t, src, nil)

	job, err := p.Start("peer-a", h, "slow.bin", "slow.bin", "")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// wait until it really starts reading (first chunk written)
	time.Sleep(50 * time.Millisecond)
	if err := p.Cancel(job.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	done := waitJob(t, p, job.ID)
	if done.Status != PullCancelled {
		t.Fatalf("status = %s (err=%s), want cancelled", done.Status, done.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "pulled", "slow.bin.part")); !os.IsNotExist(err) {
		t.Fatal(".part should be cleaned after cancel")
	}
	// cancelling an already finished job must error (the frontend uses this to show "the job is already done")
	if err := p.Cancel(job.ID); err == nil {
		t.Fatal("cancel on finished job should error")
	}
}

// TestStartCollectionPartialFailure batch pull: one bad entry does not affect the others.
// Discovery background: when "save the whole collection" has one illegal hash or a missing
// peer entry, a whole-batch failure would leave the user unable to save anything at all;
// each entry becomes its own job, so the frontend can show which ones failed.
func TestStartCollectionPartialFailure(t *testing.T) {
	good := []byte("good")
	gh := hashOf(good)
	src := &fakePullSource{content: map[string][]byte{gh: good}}
	p, _, _ := newPullerForTest(t, src, nil)

	jobs := p.StartCollection("peer-a", "collhash", []PullEntry{
		{Path: "ok.txt", Hash: gh},
		{Path: "bad.txt", Hash: "not-a-hash"},
		{Path: "missing.txt", Hash: strings.Repeat("f", 64)},
	})
	if len(jobs) != 3 {
		t.Fatalf("jobs = %d, want 3", len(jobs))
	}
	if jobs[1].Status != PullFailed || jobs[1].Error == "" {
		t.Fatalf("invalid hash should fail immediately: %+v", jobs[1])
	}
	if done := waitJob(t, p, jobs[0].ID); done.Status != PullDone {
		t.Fatalf("good entry should succeed: %+v", done)
	}
	if done := waitJob(t, p, jobs[2].ID); done.Status != PullFailed {
		t.Fatalf("peer-missing entry should fail: %+v", done)
	}
}

// TestStartPullRejectsBadInput input validation (frontend error messages depend on these errors).
func TestStartPullRejectsBadInput(t *testing.T) {
	p := NewPeerPuller(t.TempDir())
	if _, err := p.Start("peer-a", strings.Repeat("a", 64), "x", "x", ""); err == nil {
		t.Fatal("should error when source not injected")
	}
	p.SetSource(&fakePullSource{})
	if _, err := p.Start("", strings.Repeat("a", 64), "x", "x", ""); err == nil {
		t.Fatal("empty peer should error")
	}
	if _, err := p.Start("peer-a", "short", "x", "x", ""); err == nil {
		t.Fatal("invalid hash should error")
	}
}

// TestFetchManifest for a collection not in the listing, fetch the manifest back by hash (the exit for unlisted collections).
//
// Why this must be supported: a collection manifest is itself content-addressed JSON whose
// hash is its sha256, so "fetch by hash" holds for collections too; and an unlisted
// collection is, by definition, not in the shared listing -- accepting only the listing
// would make "give someone a collection link, have them save the whole thing to their own
// node" impossible forever. Levels are unaffected: fetching uses the same req channel, and a
// private manifest is blocked by the peer's ShareGate.
func TestFetchManifest(t *testing.T) {
	manifest := []byte(`{"version":2,"friendly_name":"c","entries":[{"path":"a.txt","hash":"` +
		strings.Repeat("a", 64) + `"}]}`)
	h := sha256.Sum256(manifest)
	hash := hex.EncodeToString(h[:])

	p := NewPeerPuller(t.TempDir())
	if _, err := p.FetchManifest("peer-a", hash, 0); err == nil {
		t.Fatal("should error when source not injected")
	}
	p.SetSource(&fakePullSource{content: map[string][]byte{hash: manifest}})

	got, err := p.FetchManifest("peer-a", hash, 0)
	if err != nil {
		t.Fatalf("failed to fetch manifest: %v", err)
	}
	if string(got) != string(manifest) {
		t.Fatalf("manifest content mismatch: %q", got)
	}

	// cap: the argument may be a hash the user typed by hand, pointing at something that is
	// not a manifest (maybe several GB), and without a cap we would read the whole thing into
	// memory before deciding.
	trimmed, err := p.FetchManifest("peer-a", hash, 16)
	if err != nil {
		t.Fatalf("failed to fetch with cap: %v", err)
	}
	if len(trimmed) != 16 {
		t.Fatalf("maxBytes did not take effect: got %d bytes", len(trimmed))
	}

	if _, err := p.FetchManifest("peer-a", "short", 0); err == nil {
		t.Fatal("invalid hash should error")
	}
	// a peer refusal (private blocked by ShareGate) must pass through, not be swallowed as "an empty collection"
	p.SetSource(&fakePullSource{err: errors.New("err: private")})
	if _, err := p.FetchManifest("peer-a", hash, 0); err == nil {
		t.Fatal("peer refusal should return error")
	}
}

// TestSanitizeRelPath the path-sanitizing rule table.
// Discovery background: sanitizing is the first line of the security boundary (the second is
// the absolute-path prefix check on targetPath), so the behavior must be explicit and
// regressable.
func TestSanitizeRelPath(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"a/b.txt":            filepath.Join("a", "b.txt"),
		"../../etc/passwd":   filepath.Join("etc", "passwd"),
		"/abs/path.txt":      filepath.Join("abs", "path.txt"),
		"..\\..\\win\\x.txt": filepath.Join("win", "x.txt"),
		"./a/./b":            filepath.Join("a", "b"),
		"a//b":               filepath.Join("a", "b"),
		"c:evil?.txt":        "c_evil_.txt",
	}
	for in, want := range cases {
		if got := sanitizeRelPath(in); got != want {
			t.Fatalf("sanitizeRelPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPullJobListOrderAndPrune job list ordering + cap trimming.
// Discovery background: the job table lives in memory and grows unboundedly over a long run;
// trimming must only drop finished jobs (dropping a running one would hide an in-progress
// download from the user).
func TestPullJobListOrderAndPrune(t *testing.T) {
	content := []byte("x")
	h := hashOf(content)
	src := &fakePullSource{content: map[string][]byte{h: content}}
	p, _, _ := newPullerForTest(t, src, nil)

	var lastID string
	for i := 0; i < 3; i++ {
		job, err := p.Start("peer-a", h, "f.txt", "f.txt", "")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		lastID = job.ID
		waitJob(t, p, job.ID)
		time.Sleep(2 * time.Millisecond)
	}
	list := p.List()
	if len(list) != 3 {
		t.Fatalf("list = %d, want 3", len(list))
	}
	if list[0].ID != lastID {
		t.Fatalf("list should be ordered by start time descending, first = %s, want %s", list[0].ID, lastID)
	}
}
