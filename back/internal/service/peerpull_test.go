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
	"encoding/json"
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
	if offset > 0 {
		if offset > int64(len(data)) {
			data = nil
		} else {
			data = data[offset:]
		}
	}
	if size >= 0 && int64(len(data)) > size {
		data = data[:size]
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

// pullStreamSource 是 puller 唯一需要的数据面能力：开一个内容流。
// 抽成接口而不是写死 *fakePullSource，才能把 flakyPullSource 也喂进去
// （故障注入用例要模拟「连着断」，不是「一上来就报错」）。
type pullStreamSource interface {
	OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error)
}

// newPullerForTest builds a pull service with a fake data plane + fake registration.
func newPullerForTest(t *testing.T, src pullStreamSource, local []string) (*PeerPuller, string, *[]string) {
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
// waitJob 等 job 落到终态。
// 预算 30s 而非 5s：重试用例最坏要走 pullAttempts-1 次退避（1s+2s）+ 三轮传输，
// 5s 会在**成功重试**的路径上误报 timeout，把一条绿测试变成红。
func waitJob(t *testing.T, p *PeerPuller, id string) PullJob {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := p.Get(id)
		if !ok {
			t.Fatalf("job %s does not exist", id)
		}
		if j.Done() {
			return j
		}
		time.Sleep(10 * time.Millisecond)
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

// flakyPullSource 前 failBefore 次 OpenStream 返回 err，之后正常返回内容。
// 模拟真实故障：P2P 连接在传输途中被 ICE/NAT/对端重启打断（CI 上
// TestPeerPullSavesToLocalDrive 的 err="peerjs: connection closed" 就是它）。
type flakyPullSource struct {
	content    map[string][]byte
	err        error
	failBefore int
	opens      int
	mu         sync.Mutex
}

func (f *flakyPullSource) OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error) {
	f.mu.Lock()
	n := f.opens
	f.opens++
	f.mu.Unlock()
	if n < f.failBefore {
		return nil, f.err
	}
	data, ok := f.content[hash]
	if !ok {
		return nil, errors.New("not found")
	}
	if offset > 0 {
		if offset > int64(len(data)) {
			data = nil
		} else {
			data = data[offset:]
		}
	}
	if size >= 0 && int64(len(data)) > size {
		data = data[:size]
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *flakyPullSource) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

// 断线后应自动重试并最终成功 —— 这是 pullAttempts 的核心行为。
// 没有这条，改动前的实现（单次失败即 failed）在 CI 上表现为偶发红。
func TestPullRetriesAfterConnectionDrop(t *testing.T) {
	data := []byte("payload that survives a dropped connection")
	h := hashOf(data)
	src := &flakyPullSource{
		content:    map[string][]byte{h: data},
		err:        errors.New("peerjs: connection closed"),
		failBefore: 2, // 前两次断开，第三次成功
	}
	p, _, registered := newPullerForTest(t, src, nil)

	job, err := p.Start("peer-a", h, "out.bin", "sub/dir/out.bin", "")
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	done := waitJob(t, p, job.ID)
	if done.Status != PullDone {
		t.Fatalf("status = %v, want PullDone (err=%s)", done.Status, done.Error)
	}
	if got := src.openCount(); got != 3 {
		t.Fatalf("openCount = %d, want 3 (1 initial + 2 retries)", got)
	}
	if len(*registered) != 1 {
		t.Fatalf("registered %d paths, want exactly 1", len(*registered))
	}
}

// 连接稳定断开时必须有上限，不能无限重试把 job 永久挂住。
func TestPullGivesUpAfterMaxAttempts(t *testing.T) {
	data := []byte("never reachable")
	h := hashOf(data)
	src := &flakyPullSource{
		content:    map[string][]byte{h: data},
		err:        errors.New("peerjs: connection closed"),
		failBefore: 999, // 永远断
	}
	p, _, registered := newPullerForTest(t, src, nil)

	job, err := p.Start("peer-a", h, "out.bin", "out.bin", "")
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	done := waitJob(t, p, job.ID)
	if done.Status != PullFailed {
		t.Fatalf("status = %v, want PullFailed", done.Status)
	}
	if got := src.openCount(); got != pullAttempts {
		t.Fatalf("openCount = %d, want %d (must stop at pullAttempts, not loop forever)", got, pullAttempts)
	}
	if len(*registered) != 0 {
		t.Fatalf("registered %d paths, want 0 (failed pull must not register anything)", len(*registered))
	}
}

// 不可重试的错误（协议/本地类）必须**一次就放弃**——
// 无脑重试只会把用户的等待从 1 秒拖成 7 秒，再给出一模一样的错误。
func TestPullDoesNotRetryNonTransientError(t *testing.T) {
	h := hashOf([]byte("never registered"))
	src := &fakePullSource{content: map[string][]byte{}, err: errors.New("not found")}
	p, _, _ := newPullerForTest(t, src, nil)

	job, err := p.Start("peer-a", h, "out.bin", "out.bin", "")
	if err != nil { t.Fatalf("unexpected error: %v", err) }

	done := waitJob(t, p, job.ID)
	if done.Status != PullFailed {
		t.Fatalf("status = %v, want PullFailed", done.Status)
	}
	if retryablePullErr(errors.New("not found")) {
		t.Fatal(`"not found" must not be retryable`)
	}
	if retryablePullErr(errors.New("open temp: permission denied")) {
		t.Fatal("local IO errors must not be retryable")
	}
	if !retryablePullErr(errors.New("peerjs: connection closed")) {
		t.Fatal("connection closed must be retryable")
	}
	if !retryablePullErr(errors.New("unexpected EOF")) {
		t.Fatal("EOF must be retryable")
	}
}

// 发现背景：Issue #212 断点续传测试 —— 当磁盘已存在部分 .part 文件时，Start 必须以 PullResuming
// 状态启动，并且从已有 offset 续传，合并校验全文件 SHA-256 并完成落盘。
func TestPullResumeFromExistingPart(t *testing.T) {
	fullContent := []byte("hello world this is a resumable transfer test payload with enough bytes")
	h := hashOf(fullContent)
	src := &fakePullSource{content: map[string][]byte{h: fullContent}}
	p, root, registered := newPullerForTest(t, src, nil)

	// 事先写入前 20 个字节到 .part 文件中
	partBytes := fullContent[:20]
	pulledDir := filepath.Join(root, "pulled")
	if err := os.MkdirAll(pulledDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	partPath := filepath.Join(pulledDir, "resumable.txt.part")
	if err := os.WriteFile(partPath, partBytes, 0o644); err != nil {
		t.Fatalf("write part failed: %v", err)
	}

	job, err := p.Start("peer-a", h, "resumable.txt", "resumable.txt", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if job.Status != PullResuming {
		t.Fatalf("initial job status = %v, want %v", job.Status, PullResuming)
	}
	if job.Received != int64(len(partBytes)) {
		t.Fatalf("initial job received = %d, want %d", job.Received, len(partBytes))
	}

	done := waitJob(t, p, job.ID)
	if done.Status != PullDone {
		t.Fatalf("status = %v, want PullDone (err=%s)", done.Status, done.Error)
	}

	targetPath := filepath.Join(pulledDir, "resumable.txt")
	gotData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target failed: %v", err)
	}
	if !bytes.Equal(gotData, fullContent) {
		t.Fatalf("content mismatch: got %q, want %q", gotData, fullContent)
	}

	// 确认 .part 与 .meta 已被清理
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf(".part file should be cleaned up after completion")
	}
	if _, err := os.Stat(partPath + ".meta"); !os.IsNotExist(err) {
		t.Fatalf(".part.meta file should be cleaned up after completion")
	}
	if len(*registered) != 1 {
		t.Fatalf("expected 1 registered path, got %d", len(*registered))
	}
}

// 发现背景：Issue #212 启动恢复测试 —— 进程重启后扫描 .part 与 .part.meta 文件，
// 自动恢复为 PullResuming 状态并继续断点续传。
func TestPullStartupRecovery(t *testing.T) {
	fullContent := []byte("startup recovery content that should resume properly across process restart")
	h := hashOf(fullContent)
	src := &fakePullSource{content: map[string][]byte{h: fullContent}}

	root := t.TempDir()
	pulledDir := filepath.Join(root, "pulled")
	if err := os.MkdirAll(pulledDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	partBytes := fullContent[:30]
	partPath := filepath.Join(pulledDir, "recovery.txt.part")
	if err := os.WriteFile(partPath, partBytes, 0o644); err != nil {
		t.Fatalf("write part failed: %v", err)
	}

	meta := pullJobMeta{
		ID:        "pull-recovered-1",
		Peer:      "peer-b",
		Hash:      h,
		Name:      "recovery.txt",
		Path:      "recovery.txt",
		Total:     int64(len(fullContent)),
		CreatedAt: time.Now().Add(-5 * time.Minute),
	}
	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(partPath+".meta", metaBytes, 0o644); err != nil {
		t.Fatalf("write meta failed: %v", err)
	}

	// 创建新的 Puller 模拟重启
	p := NewPeerPuller(root)
	p.SetSource(src)
	var registered []string
	var regMu sync.Mutex
	p.SetFileAccess(
		func(hash string) bool { return false },
		func(path string) (string, int64, error) {
			regMu.Lock()
			registered = append(registered, path)
			regMu.Unlock()
			return h, int64(len(fullContent)), nil
		},
	)

	recovered := p.RecoverIncompleteTasks()
	if len(recovered) != 1 {
		t.Fatalf("RecoverIncompleteTasks returned %d jobs, want 1", len(recovered))
	}
	job := recovered[0]
	if job.Status != PullResuming {
		t.Fatalf("recovered job status = %v, want %v", job.Status, PullResuming)
	}
	if job.Received != int64(len(partBytes)) {
		t.Fatalf("recovered job received = %d, want %d", job.Received, len(partBytes))
	}

	done := waitJob(t, p, job.ID)
	if done.Status != PullDone {
		t.Fatalf("status = %v, want PullDone (err=%s)", done.Status, done.Error)
	}

	targetPath := filepath.Join(pulledDir, "recovery.txt")
	gotData, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target failed: %v", err)
	}
	if !bytes.Equal(gotData, fullContent) {
		t.Fatalf("content mismatch: got %q, want %q", gotData, fullContent)
	}
	if _, err := os.Stat(partPath); !os.IsNotExist(err) {
		t.Fatalf(".part should be removed after completion")
	}
	if _, err := os.Stat(partPath + ".meta"); !os.IsNotExist(err) {
		t.Fatalf(".part.meta should be removed after completion")
	}
}
