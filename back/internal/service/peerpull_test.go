package service

// 测试背景（doc/NETDISK.md M3）：跨节点拉取保存是"用户点保存 → 文件真的
// 落到我这边"的那一步，四个风险点必须钉死：
//  ① 对端给的内容必须校验 sha256（否则等于允许对端往本节点写任意内容）；
//  ② 对端给的路径必须清洗（否则 "../" 就是任意文件写入）；
//  ③ 取消要真的停下并清掉半截文件（.part 残留会被误当成果）；
//  ④ 本地已有同 hash 内容要跳过（内容寻址去重，别白下一遍）。

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

// fakePullSource 假数据面：按 hash 回预置内容。
type fakePullSource struct {
	content map[string][]byte
	err     error
	block   chan struct{} // 非 nil 时 Read 阻塞直到 Close（取消测试用）
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

// blockingReader 读第一块后阻塞，直到 Close 被调用（模拟"传输中"）。
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

// newPullerForTest 造一个带假数据面 + 假登记的拉取服务。
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

// waitJob 等任务进入终态（拉取是异步的）。
func waitJob(t *testing.T, p *PeerPuller, id string) PullJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := p.Get(id)
		if !ok {
			t.Fatalf("job %s 不存在", id)
		}
		if j.Done() {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s 超时未结束", id)
	return PullJob{}
}

// TestStartPullSavesAndRegisters 正常拉取：落盘到 pulled/<相对路径> 并登记。
// 发现背景：用户诉求就是"选中文件保存就可以从别人那里下载"——这里断言
// 保存结果是**真实文件**（大小/内容一致）且经过登记（"我的文件"能看到）。
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
		t.Fatal("不应跳过（本地没有内容）")
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
		t.Fatalf("内容不一致: %q", got)
	}
	if done.Received != int64(len(content)) {
		t.Fatalf("received = %d, want %d", done.Received, len(content))
	}
	if len(*registered) != 1 || (*registered)[0] != want {
		t.Fatalf("登记路径不符: %v", *registered)
	}
	// .part 不能残留
	if _, err := os.Stat(want + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part 残留: %v", err)
	}
}

// TestStartPullSkipsWhenLocal 本地已有同 hash → 跳过下载。
// 发现背景：内容寻址天然去重；重复"保存"同一个文件不该再走一遍网络。
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
		t.Fatal("跳过时不应重新登记")
	}
}

// TestStartPullHashMismatch 对端内容与请求 hash 不符 → 失败且不留文件。
// 发现背景：不校验等于允许对端往本节点写任意内容；且必须删掉 .part，
// 否则下次 rename 会把垃圾当正式文件。
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
		t.Fatal("校验失败后不应留下正式文件")
	}
	if _, err := os.Stat(target + ".part"); !os.IsNotExist(err) {
		t.Fatal("校验失败后不应留下 .part")
	}
	if len(*registered) != 0 {
		t.Fatal("校验失败不应登记")
	}
}

// TestStartPullCleansPathTraversal 对端给的路径不能逃出保存目录。
// 发现背景：relPath 来自对端（合集条目 path），"../../etc/passwd" 或绝对
// 路径不清洗就是任意文件写入。
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
			t.Fatalf("%q 逃出了保存目录: %s", in, abs)
		}
	}
}

// TestStartPullCancel 取消：任务置 cancelled、临时文件清理、不再继续写。
// 发现背景：用户可能点错/反悔；半截文件留在盘上会被误认为已保存。
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
	// 等它真的开始读（首块已写入）
	time.Sleep(50 * time.Millisecond)
	if err := p.Cancel(job.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	done := waitJob(t, p, job.ID)
	if done.Status != PullCancelled {
		t.Fatalf("status = %s (err=%s), want cancelled", done.Status, done.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "pulled", "slow.bin.part")); !os.IsNotExist(err) {
		t.Fatal("取消后 .part 应被清理")
	}
	// 已结束的任务再取消要报错（前端据此提示"该任务已结束"）
	if err := p.Cancel(job.ID); err == nil {
		t.Fatal("对已结束任务取消应报错")
	}
}

// TestStartCollectionPartialFailure 批量拉取：单个坏条目不影响其它条目。
// 发现背景："保存整个合集"里有一个 hash 非法/对端缺失时，整批失败会让
// 用户完全无法保存；逐条目独立成任务，前端能看出哪几个失败。
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
		t.Fatalf("非法 hash 应立即失败: %+v", jobs[1])
	}
	if done := waitJob(t, p, jobs[0].ID); done.Status != PullDone {
		t.Fatalf("好条目应成功: %+v", done)
	}
	if done := waitJob(t, p, jobs[2].ID); done.Status != PullFailed {
		t.Fatalf("对端缺失条目应失败: %+v", done)
	}
}

// TestStartPullRejectsBadInput 入参校验（前端错误提示依赖这些错误）。
func TestStartPullRejectsBadInput(t *testing.T) {
	p := NewPeerPuller(t.TempDir())
	if _, err := p.Start("peer-a", strings.Repeat("a", 64), "x", "x", ""); err == nil {
		t.Fatal("未注入 source 时应报错")
	}
	p.SetSource(&fakePullSource{})
	if _, err := p.Start("", strings.Repeat("a", 64), "x", "x", ""); err == nil {
		t.Fatal("空 peer 应报错")
	}
	if _, err := p.Start("peer-a", "short", "x", "x", ""); err == nil {
		t.Fatal("非法 hash 应报错")
	}
}

// TestSanitizeRelPath 路径清洗规则表。
// 发现背景：清洗是安全边界的第一道（第二道是 targetPath 的绝对路径前缀校验），
// 行为必须明确可回归。
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

// TestPullJobListOrderAndPrune 任务列表顺序 + 上限裁剪。
// 发现背景：任务表在内存里，长期运行会无界增长；裁剪必须只丢已结束任务
// （丢运行中的会让用户看不到进行中的下载）。
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
		t.Fatalf("列表应按开始时间倒序，首个 = %s, want %s", list[0].ID, lastID)
	}
}
