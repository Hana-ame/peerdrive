package service

// peerpull.go：跨节点拉取保存（doc/NETDISK.md M3 / ROADMAP 阶段 6）。
//
// 目标链路：用户在对方节点看到"文件链接"（share 帧给的合集条目或单文件）
// → 点保存 → 内容从对方节点流到本节点并落盘 → 出现在"我的文件"里。
//
// 形态参考 BT/PT 下载任务（用户类比里的"快播/PT 思路"）：
//   - 任务列表 + 进度 + 取消；
//   - 已在本地的内容**直接跳过**（内容寻址天然去重，不必再下一遍）。
//
// 与既有能力的分工：
//   - transport.OpenStream：流式读对端内容（不驻留内存，8GB 也一样）；
//   - 本服务：把流写成文件 + 校验 sha256 + 登记进 file_index（"我的文件"可见）；
//   - source.Manager 的 peer 源是"按需回源"（不落盘），与本服务的"保存"
//     是两种语义，不要互相替代。
//
// 落盘策略（**单写**，不写 CAS 副本）：
//   流 → <DownloadDir>/pulled/<相对路径>.part → 校验 sha256 → rename 成正式名
//   → file_index.Create 登记。
//   为什么落 DownloadDir 而不是 CAS：file_index 允许根目录就是 DownloadDir
//   （见 transport.NewFileIndexService 的 H2 安全边界），登记后该文件既能被
//   本节点 serveFile 服务给别的节点，又能直接出现在"我的文件"列表里——
//   一份数据同时满足"保存"与"共享"。写 CAS 副本只会让同一内容存两份。
//   去重靠"先查 file_index"这一步：同一 hash 已登记过就不再下载。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/extractor"
	"peerdrive/internal/log"
)

// PullStatus 拉取任务状态。
type PullStatus string

const (
	PullRunning   PullStatus = "running"
	PullResuming  PullStatus = "resuming"
	PullDone      PullStatus = "done"
	PullFailed    PullStatus = "failed"
	PullCancelled PullStatus = "cancelled"
)

// pullConcurrency 同时进行的拉取数上限。
// 每条拉取占用一条 WebRTC 连接的带宽；过多并发会让每个都变慢（且对端
// serveFile 是逐连接串行），3 是"批量保存一个合集"时的合理折中。
const pullConcurrency = 3

// pullMaxJobs 任务表保留上限（超出丢弃最老的已结束任务）。
// 为什么要有：任务表在内存里，长期运行 + 频繁拉取会无界增长。
const pullMaxJobs = 200

// PullJob 一个拉取任务（对前端即"传输"列表里的一行）。
type PullJob struct {
	ID       string     `json:"id"`
	Peer     string     `json:"peer"`
	Hash     string     `json:"hash"`
	Name     string     `json:"name,omitempty"`
	Path     string     `json:"path,omitempty"` // 相对保存路径（合集内路径）
	Coll     string     `json:"collection,omitempty"`
	Total    int64      `json:"total"`    // -1 = 对端未声明大小
	Received int64      `json:"received"`
	Status   PullStatus `json:"status"`
	Error    string     `json:"error,omitempty"`
	Skipped  bool       `json:"skipped,omitempty"` // 本地已有同 hash 内容
	SavedTo  string     `json:"saved_to,omitempty"`
	Started  time.Time  `json:"started_at"`
	Ended    *time.Time `json:"ended_at,omitempty"`
}

// Done 任务是否已结束（终态）。
func (j PullJob) Done() bool {
	return j.Status == PullDone || j.Status == PullFailed || j.Status == PullCancelled
}

// PullSource 拉取数据面（*transport.PeerJSService 满足）。
// 只依赖"能按 hash 流式读"这一件事，便于单测注入假实现。
type PullSource interface {
	OpenStream(peerID, hash string, offset, size int64) (io.ReadCloser, error)
}

// totalReporter 能报告对端声明大小的 reader（*transport.fetchReader 满足）。
// 用可选接口而不是硬依赖：假实现/未来实现不必提供大小，进度按未知渲染。
type totalReporter interface{ Total() int64 }

// PeerPuller 跨节点拉取保存服务。
type PeerPuller struct {
	downloadRoot string // 保存根目录（= file_index 的允许根目录）
	source       PullSource

	// isLocal 判断某 hash 是否已在本地（file_index 命中）。
	// register 把落盘后的文件登记进 file_index，返回实际 hash 与大小。
	isLocal  func(hash string) bool
	register func(path string) (string, int64, error)

	// extractor 拉取后自动解包器（nil = 不启用）。
	extractor *extractor.Extractor

	mu   sync.Mutex
	jobs map[string]*PullJob
	// cancels jobID → cancel（cancel 会关闭 reader，让传输立刻中断）
	cancels map[string]context.CancelFunc
	// closers jobID → 当前 reader（Cancel 时 Close，唤醒阻塞的 Read）
	closers map[string]io.Closer
	sem     chan struct{}
}

// NewPeerPuller 创建拉取服务。downloadRoot 为空时退回 ./downloads。
func NewPeerPuller(downloadRoot string) *PeerPuller {
	if downloadRoot == "" {
		downloadRoot = "./downloads"
	}
	if abs, err := filepath.Abs(downloadRoot); err == nil {
		downloadRoot = abs
	}
	return &PeerPuller{
		downloadRoot: downloadRoot,
		jobs:         make(map[string]*PullJob),
		cancels:      make(map[string]context.CancelFunc),
		closers:      make(map[string]io.Closer),
		sem:          make(chan struct{}, pullConcurrency),
	}
}

// pullJobMeta 落地到 .part.meta 文件的断点续传元数据。
type pullJobMeta struct {
	ID        string    `json:"id"`
	Peer      string    `json:"peer"`
	Hash      string    `json:"hash"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	Coll      string    `json:"collection,omitempty"`
	Total     int64     `json:"total"`
	CreatedAt time.Time `json:"created_at"`
}

// saveJobMetaLocked 将任务元数据原子落盘到 metaPath（.part.meta）。调用方必须持有 p.mu。
func (p *PeerPuller) saveJobMetaLocked(job *PullJob, metaPath string) error {
	meta := pullJobMeta{
		ID:        job.ID,
		Peer:      job.Peer,
		Hash:      job.Hash,
		Name:      job.Name,
		Path:      job.Path,
		Coll:      job.Coll,
		Total:     job.Total,
		CreatedAt: job.Started,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	tmpMeta := metaPath + ".tmp"
	if err := os.WriteFile(tmpMeta, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpMeta, metaPath)
}

// saveJobMeta 加锁将任务元数据落盘到 metaPath。
func (p *PeerPuller) saveJobMeta(job *PullJob, metaPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.saveJobMetaLocked(job, metaPath)
}

// SetSource 注入数据面（main 装 transport.PeerJSService）。
func (p *PeerPuller) SetSource(src PullSource) { p.source = src }

// SetFileAccess 注入本地文件访问（存在性判断 + 登记）。
// 用注入而不是直接依赖 transport.FileIndexService：service 单测不需要起
// 真实索引/DB。
func (p *PeerPuller) SetFileAccess(isLocal func(hash string) bool, register func(path string) (string, int64, error)) {
	p.isLocal = isLocal
	p.register = register
}

// SetExtractor 注入拉取后自动解包器（nil = 不启用）。
func (p *PeerPuller) SetExtractor(ex *extractor.Extractor) { p.extractor = ex }

// Start 建一个拉取任务并立刻开始（异步）。
// name/path/coll 只用于落盘命名与前端展示：path 是合集内相对路径（保留目录
// 结构），name 兜底为 path 的 basename。
func (p *PeerPuller) Start(peer, hash, name, relPath, coll string) (*PullJob, error) {
	if peer == "" {
		return nil, errors.New("peer is required")
	}
	if !isSHA256Hex(strings.ToLower(hash)) {
		return nil, fmt.Errorf("invalid sha256 hash %q", hash)
	}
	if p.source == nil {
		return nil, errors.New("pull source not configured")
	}
	hash = strings.ToLower(hash)
	if name == "" {
		name = filepath.Base(relPath)
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = hash[:12] // 对端没给名字：用 hash 前缀当文件名，至少能落到盘上
	}
	p.mu.Lock()
	// 如果已有相同 hash 的活跃任务，直接复用，避免并发重复拉取同一文件
	for _, j := range p.jobs {
		if !j.Done() && j.Hash == hash {
			snap := *j
			p.mu.Unlock()
			return &snap, nil
		}
	}
	p.mu.Unlock()

	status := PullRunning
	var initialReceived int64
	var initialTotal int64 = -1
	target, _ := p.targetPath(&PullJob{Hash: hash, Name: name, Path: relPath})
	if target != "" {
		tmp := target + ".part"
		metaPath := tmp + ".meta"
		if fi, err := os.Stat(tmp); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			status = PullResuming
			initialReceived = fi.Size()
		}
		if raw, err := os.ReadFile(metaPath); err == nil {
			var m pullJobMeta
			if json.Unmarshal(raw, &m) == nil && m.Total > 0 {
				initialTotal = m.Total
			}
		}
	}

	job := &PullJob{
		ID:       newJobID(),
		Peer:     peer,
		Hash:     hash,
		Name:     name,
		Path:     relPath,
		Coll:     coll,
		Total:    initialTotal,
		Received: initialReceived,
		Status:   status,
		Started:  time.Now(),
	}
	p.mu.Lock()
	p.jobs[job.ID] = job
	p.pruneLocked()
	p.mu.Unlock()

	if target != "" {
		_ = p.saveJobMeta(job, target+".part.meta")
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancels[job.ID] = cancel
	snap := *job
	p.mu.Unlock()

	go p.run(ctx, job)
	return &snap, nil
}

// StartCollection 批量拉取一个合集的全部条目（返回各自的任务）。
// entries 为 (相对路径, hash) 列表，由调用方从 share 帧的快照里取。
// 单个条目启动失败（hash 非法等）不中断整批——把失败项也作为任务返回，
// 前端能一眼看出哪几个没起来。
func (p *PeerPuller) StartCollection(peer, coll string, entries []PullEntry) []*PullJob {
	out := make([]*PullJob, 0, len(entries))
	for _, e := range entries {
		job, err := p.Start(peer, e.Hash, filepath.Base(e.Path), e.Path, coll)
		if err != nil {
			out = append(out, &PullJob{
				Hash:   e.Hash,
				Peer:   peer,
				Name:   filepath.Base(e.Path),
				Path:   e.Path,
				Coll:   coll,
				Total:  -1,
				Status: PullFailed,
				Error:  err.Error(),
			})
			continue
		}
		out = append(out, job)
	}
	return out
}

// FetchManifest 按 hash 从对端取回一份合集 manifest（**不落盘**）。
//
// 为什么需要它：合集 manifest 本身就是一份按内容寻址存的 JSON，hash 即其 sha256，
// 所以"凭 hash 取回"对合集同样成立。而 **unlisted 合集按定义不在共享清单里**——
// 只认清单的话，"凭链接保存整个合集"永远做不成（清单里找不到 → 404）。
//
// 级别不受影响：取回走的是同一条 req 通道，对端的 ShareGate 照样判——private
// 合集的 manifest 取不到，这里就返回错误（调用方按"清单里没有"处理）。
//
// maxBytes 是硬上限：这条路的入参可能是用户随手填的一串 hash，指向的未必是
// manifest（可能是个几 GB 的文件），不设限会把它整份读进内存再判断。
func (p *PeerPuller) FetchManifest(peer, hash string, maxBytes int64) ([]byte, error) {
	if p.source == nil {
		return nil, errors.New("pull source not configured")
	}
	if !isSHA256Hex(strings.ToLower(hash)) {
		return nil, fmt.Errorf("invalid sha256 hash %q", hash)
	}
	if maxBytes <= 0 {
		maxBytes = 8 << 20 // 默认 8MB：manifest 通常几 KB
	}
	r, err := p.source.OpenStream(peer, strings.ToLower(hash), 0, maxBytes)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, maxBytes))
}

// PullEntry 合集内一个待拉取条目。
type PullEntry struct {
	Path string
	Hash string
}

// List 任务列表（按开始时间倒序，最新的在最前——前端"传输"页的默认顺序）。
func (p *PeerPuller) List() []PullJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]PullJob, 0, len(p.jobs))
	for _, j := range p.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Get 单个任务。
func (p *PeerPuller) Get(id string) (PullJob, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, ok := p.jobs[id]
	if !ok {
		return PullJob{}, false
	}
	return *j, true
}

// Cancel 取消任务。已结束的任务返回错误（前端可提示"该任务已结束"）。
// 取消 = cancel ctx + Close reader：Close 让阻塞在 Read 的 goroutine 立刻
// 返回（只 cancel 不够——Read 可能在等下一个块）。
func (p *PeerPuller) Cancel(id string) error {
	p.mu.Lock()
	j, ok := p.jobs[id]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("pull job %s not found", id)
	}
	if j.Done() {
		p.mu.Unlock()
		return fmt.Errorf("pull job %s already finished", id)
	}
	cancel := p.cancels[id]
	closer := p.closers[id]
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if closer != nil {
		_ = closer.Close()
	}
	target, _ := p.targetPath(j)
	if target != "" {
		_ = os.Remove(target + ".part")
		_ = os.Remove(target + ".part.meta")
	}
	return nil
}

// pullAttempts 跨节点拉取的最大尝试次数（首次 + 重试）。
//
// 为什么需要重试：transport 的连接是会断的（ICE 切换、对端重启、
// NAT 超时），conn.go 在连接断开时给所有在途 fetch 推 "connection closed"。
// 改之前单次失败即任务 failed——**任何一次网络抖动都会让用户的保存操作
// 彻底失败**，只能手动重来。CI 上这个缺陷表现为
// TestPeerPullSavesToLocalDrive 偶发 err="peerjs: connection closed"
// （2026-10-06 连续两次红，本地跑 3/3 绿：CI 机器负载高、连接更容易被打断）。
//
// 为什么是 3 而不是无限重试：连接断在「对端确实走了」时会一直断，
// 无限重试会让 job 永远挂着，占着并发名额（sem），用户既看不到进展也取消不掉。
// 3 次 + 递增退避足以覆盖瞬时抖动，又能让真断连在十几秒内给出明确失败。
const pullAttempts = 3

// pullBackoff 第 n 次重试前的等待（n 从 0 起）。
// 递增而不是固定：对端可能正在重启，等久一点比连环重试更容易赶上。
func pullBackoff(n int) time.Duration {
	d := time.Duration(1<<uint(n)) * time.Second // 1s → 2s → 4s
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

// retryablePullErr 判断拉取错误是否值得重试。
//
// 重试：连接类瞬时故障（连接断开、流被取消/重置、请求超时）。
// 不重试：本地 I/O 与协议类错误——重跑一遍还是同样的结果，
//   只会把用户的等待时间从 1 秒拖到 7 秒，然后给出一模一样的错误信息。
func retryablePullErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// 对端没在我们请求之前就准备好（会话刚建立时的竞态），重试有意义
	if strings.Contains(msg, "no connection to") || strings.Contains(msg, "connection closed") ||
		strings.Contains(msg, "connection not bound") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "connection reset") || strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "i/o timeout") {
		return true
	}
	return false
}

// run 执行一次拉取：跳过检查 → 流式下载（带进度与断点续传）→ 校验 → 落盘登记。
func (p *PeerPuller) run(ctx context.Context, job *PullJob) {
	// 并发闸：等一个名额（可被取消打断）
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		target, _ := p.targetPath(job)
		if target != "" {
			_ = os.Remove(target + ".part")
			_ = os.Remove(target + ".part.meta")
		}
		p.finish(job, PullCancelled, "", time.Time{})
		return
	}
	defer func() { <-p.sem }()

	// ① 本地已有同 hash 内容 → 跳过（内容寻址去重：同内容不必再下一遍）
	if p.isLocal != nil && p.isLocal(job.Hash) {
		p.mu.Lock()
		job.Skipped = true
		p.mu.Unlock()
		log.LogInfo("peerpull: skip %s (already local)", job.Hash[:12])
		p.finish(job, PullDone, "", time.Time{})
		return
	}

	// ③ 落盘到 .part（与最终文件同目录 → rename 原子，不会留半截"正式文件"）
	target, err := p.targetPath(job)
	if err != nil {
		p.finish(job, PullFailed, err.Error(), time.Time{})
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		p.finish(job, PullFailed, "mkdir: "+err.Error(), time.Time{})
		return
	}
	tmp := target + ".part"

	// ②+③ 合并成可重试的一轮：开流 → 写 .part → 校验 → rename → 登记。
	// 支持断点续传：若已有 .part 文件，先读取已有字节并预先计算哈希，随后从断点续传。
	var lastErr error
	for attempt := 0; attempt < pullAttempts; attempt++ {
		if attempt > 0 {
			// 用户主动取消就别再重试了：等完退避才失败会让人以为卡死
			if ctx.Err() != nil {
				p.finish(job, PullCancelled, "", time.Time{})
				return
			}
			log.LogWarn("peerpull: %s retry %d/%d after %v",
				job.Hash[:12], attempt+1, pullAttempts, lastErr)
			select {
			case <-time.After(pullBackoff(attempt - 1)):
			case <-ctx.Done():
				p.finish(job, PullCancelled, "", time.Time{})
				return
			}
		}
		ok, err := p.fetchOnce(ctx, job, target, tmp)
		if ok {
			return // fetchOnce 内部已 finish（done / failed）
		}
		lastErr = err
		if !retryablePullErr(err) {
			p.finish(job, PullFailed, err.Error(), time.Time{})
			return
		}
	}
	p.finish(job, PullFailed,
		fmt.Sprintf("%v (after %d attempts)", lastErr, pullAttempts), time.Time{})
}

// fetchOnce 跑一轮完整的「开流 → 落盘 → 校验 → 登记」。
// 返回 ok=true 表示已经 finish 过（无论成功还是终态失败），调用方直接返回；
// ok=false + err 表示这是一次**值得重试**的瞬时故障，err 里是原因。
func (p *PeerPuller) fetchOnce(ctx context.Context, job *PullJob, target, tmp string) (bool, error) {
	metaPath := tmp + ".meta"
	_ = p.saveJobMeta(job, metaPath)

	var partOffset int64 = 0
	h := sha256.New()

	if fi, statErr := os.Stat(tmp); statErr == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
		if job.Total > 0 && fi.Size() > job.Total {
			_ = os.Truncate(tmp, 0)
		} else {
			existingFile, oErr := os.Open(tmp)
			if oErr == nil {
				n, cpErr := io.Copy(h, existingFile)
				_ = existingFile.Close()
				if cpErr == nil && n == fi.Size() {
					partOffset = n
					p.mu.Lock()
					job.Received = partOffset
					p.mu.Unlock()
				} else {
					h.Reset()
					_ = os.Truncate(tmp, 0)
					partOffset = 0
				}
			}
		}
	}

	// 极端情况：.part 文件已经完整落盘（且达到已知 total），直接校验并完成
	if partOffset > 0 && job.Total > 0 && partOffset == job.Total {
		sum := hex.EncodeToString(h.Sum(nil))
		if sum == job.Hash {
			_ = os.Remove(metaPath)
			if err := os.Rename(tmp, target); err != nil {
				_ = os.Remove(tmp)
				p.finish(job, PullFailed, "rename: "+err.Error(), time.Time{})
				return true, nil
			}
			p.finishSaved(job, target, partOffset)
			return true, nil
		}
		h.Reset()
		_ = os.Truncate(tmp, 0)
		partOffset = 0
		p.mu.Lock()
		job.Received = 0
		p.mu.Unlock()
	}

	stream, err := p.source.OpenStream(job.Peer, job.Hash, partOffset, -1)
	if err != nil {
		return false, fmt.Errorf("open stream: %w", err)
	}
	p.mu.Lock()
	p.closers[job.ID] = stream
	p.mu.Unlock()
	defer func() {
		_ = stream.Close()
		p.mu.Lock()
		delete(p.closers, job.ID)
		p.mu.Unlock()
	}()

	var f *os.File
	if partOffset > 0 {
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_APPEND, 0o644)
	} else {
		f, err = os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	}
	if err != nil {
		p.finish(job, PullFailed, "open temp: "+err.Error(), time.Time{})
		return true, nil // 本地 I/O 失败，不重试
	}

	written, cpErr := p.copyWithProgress(ctx, f, stream, h, job, partOffset, metaPath)
	closeErr := f.Close()
	if cpErr != nil {
		if ctx.Err() != nil {
			_ = os.Remove(tmp)
			_ = os.Remove(metaPath)
			p.finish(job, PullCancelled, "", time.Time{})
			return true, nil
		}
		// 网络/传输错误：保留 .part 与 .meta 文件供断点续传，不删除！
		_ = p.saveJobMeta(job, metaPath)
		return false, cpErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(metaPath)
		p.finish(job, PullFailed, "close temp: "+closeErr.Error(), time.Time{})
		return true, nil
	}

	// ④ 内容寻址校验：对端给的内容必须真的等于请求的 hash
	sum := hex.EncodeToString(h.Sum(nil))
	if sum != job.Hash {
		_ = os.Remove(tmp)
		_ = os.Remove(metaPath)
		p.finish(job, PullFailed, fmt.Sprintf("hash mismatch: got %s", sum[:12]), time.Time{})
		return true, nil
	}
	_ = os.Remove(metaPath)
	// ⑤ rename 成正式名 + 登记进 file_index
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		p.finish(job, PullFailed, "rename: "+err.Error(), time.Time{})
		return true, nil
	}
	p.finishSaved(job, target, partOffset+written)
	return true, nil
}

// finishSaved 完成保存落盘后的登记、解包与终态处理。
func (p *PeerPuller) finishSaved(job *PullJob, target string, totalBytes int64) {
	ended := time.Now()
	if p.register != nil {
		if _, _, err := p.register(target); err != nil {
			log.LogWarn("peerpull: register %s failed: %v", target, err)
			p.finish(job, PullDone, "saved but not indexed: "+err.Error(), ended)
			p.mu.Lock()
			job.SavedTo = target
			p.mu.Unlock()
			return
		}
	}

	if p.extractor != nil && p.extractor.ShouldExtract(filepath.Base(target)) {
		destDir := target + "_extracted"
		extracted, err := p.extractor.Extract(target, destDir)
		if err != nil {
			log.LogWarn("peerpull: auto-extract %s failed: %v", target, err)
		} else if len(extracted) > 0 && p.register != nil {
			registered := 0
			for _, ef := range extracted {
				if _, _, regErr := p.register(ef.FullName); regErr != nil {
					log.LogWarn("peerpull: register extracted %s failed: %v", ef.FullName, regErr)
				} else {
					registered++
				}
			}
			log.LogInfo("peerpull: auto-extracted %d files (registered %d) from %s",
				len(extracted), registered, target)
		}
	}

	p.mu.Lock()
	job.SavedTo = target
	job.Received = totalBytes
	p.mu.Unlock()
	p.finish(job, PullDone, "", ended)
}

// copyWithProgress 流式拷贝并更新进度（进度按块更新，够前端画进度条）。
func (p *PeerPuller) copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, h io.Writer, job *PullJob, initialOffset int64, metaPath string) (int64, error) {
	buf := make([]byte, 64*1024)
	var written int64
	for {
		if ctx.Err() != nil {
			return written, errors.New("cancelled")
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			_, _ = h.Write(buf[:n])
			written += int64(n)
			p.mu.Lock()
			if !job.Done() {
				job.Received = initialOffset + written
				job.Status = PullRunning
				// 对端 meta 帧可能晚于首块到达：每轮尝试刷新总大小
				if job.Total <= 0 {
					if tr, ok := src.(totalReporter); ok {
						job.Total = tr.Total()
						if job.Total > 0 {
							_ = p.saveJobMetaLocked(job, metaPath)
						}
					}
				}
			}
			p.mu.Unlock()
		}
		if rerr != nil {
			if rerr == io.EOF {
				return written, nil
			}
			return written, rerr
		}
	}
}

// RecoverIncompleteTasks 扫描 downloadRoot/pulled 目录下的 .part 文件，
// 将其映射回 PullJob 并恢复为 resuming 状态启动断点续传。
func (p *PeerPuller) RecoverIncompleteTasks() []*PullJob {
	pulledDir := filepath.Join(p.downloadRoot, "pulled")
	if _, err := os.Stat(pulledDir); err != nil {
		return nil
	}
	var recovered []*PullJob
	_ = filepath.Walk(pulledDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".part") {
			return nil
		}
		metaPath := path + ".meta"
		raw, readErr := os.ReadFile(metaPath)
		if readErr != nil {
			return nil
		}
		var meta pullJobMeta
		if jsonErr := json.Unmarshal(raw, &meta); jsonErr != nil || meta.Hash == "" || meta.Peer == "" {
			return nil
		}
		// 若该 hash 已经在本地索引中（之前已下载完成），清理残余 .part
		if p.isLocal != nil && p.isLocal(meta.Hash) {
			_ = os.Remove(path)
			_ = os.Remove(metaPath)
			return nil
		}
		p.mu.Lock()
		for _, existing := range p.jobs {
			if !existing.Done() && existing.Hash == meta.Hash {
				p.mu.Unlock()
				return nil
			}
		}
		job := &PullJob{
			ID:       meta.ID,
			Peer:     meta.Peer,
			Hash:     meta.Hash,
			Name:     meta.Name,
			Path:     meta.Path,
			Coll:     meta.Coll,
			Total:    meta.Total,
			Received: info.Size(),
			Status:   PullResuming,
			Started:  meta.CreatedAt,
		}
		if job.ID == "" {
			job.ID = newJobID()
		}
		if job.Started.IsZero() {
			job.Started = time.Now()
		}
		p.jobs[job.ID] = job
		p.pruneLocked()
		p.mu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		p.mu.Lock()
		p.cancels[job.ID] = cancel
		snap := *job
		p.mu.Unlock()

		go p.run(ctx, job)
		recovered = append(recovered, &snap)
		return nil
	})
	return recovered
}

// targetPath 计算最终保存路径：<root>/pulled/<清洗后的相对路径>。
//
// 为什么要清洗：relPath 来自对端（合集条目 path），恶意/异常对端可以给
// "../../etc/passwd" 或绝对路径——不清洗就是任意文件写入。
func (p *PeerPuller) targetPath(job *PullJob) (string, error) {
	rel := sanitizeRelPath(job.Path)
	if rel == "" {
		rel = sanitizeRelPath(job.Name)
	}
	if rel == "" {
		rel = job.Hash[:12]
	}
	target := filepath.Join(p.downloadRoot, "pulled", rel)
	// 双保险：Join + 清洗后仍必须落在根目录内（防清洗遗漏的边界情况）
	root, err := filepath.Abs(filepath.Join(p.downloadRoot, "pulled"))
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe target path %q", job.Path)
	}
	return abs, nil
}

// finish 置终态（幂等：重复调用只生效一次）。
func (p *PeerPuller) finish(job *PullJob, status PullStatus, errMsg string, ended time.Time) {
	p.mu.Lock()
	if job.Done() {
		p.mu.Unlock()
		return
	}
	job.Status = status
	job.Error = errMsg
	if ended.IsZero() {
		ended = time.Now()
	}
	job.Ended = &ended
	cancel := p.cancels[job.ID]
	delete(p.cancels, job.ID)
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if status == PullFailed {
		log.LogWarn("peerpull: job %s failed: %s", job.ID, errMsg)
	} else {
		log.LogInfo("peerpull: job %s %s (%s<-%s)", job.ID, status, job.Hash[:12], job.Peer)
	}
}

// pruneLocked 任务表超限时丢弃最老的**已结束**任务（运行中的永不丢）。
func (p *PeerPuller) pruneLocked() {
	if len(p.jobs) <= pullMaxJobs {
		return
	}
	type entry struct {
		id      string
		started time.Time
		done    bool
	}
	list := make([]entry, 0, len(p.jobs))
	for id, j := range p.jobs {
		list = append(list, entry{id: id, started: j.Started, done: j.Done()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].started.Before(list[j].started) })
	for _, e := range list {
		if len(p.jobs) <= pullMaxJobs {
			return
		}
		if e.done {
			delete(p.jobs, e.id)
		}
	}
}

var jobSeq uint64
var jobSeqMu sync.Mutex

// newJobID 生成任务 id（时间戳 + 自增，够读也够唯一；不引 uuid 避免多一个依赖）。
func newJobID() string {
	jobSeqMu.Lock()
	jobSeq++
	n := jobSeq
	jobSeqMu.Unlock()
	return fmt.Sprintf("pull-%d-%d", time.Now().UnixNano(), n)
}

// sanitizeRelPath 把对端给的相对路径清洗成安全相对路径。
// 规则：统一分隔符 → 丢弃空段/"."/".."/绝对前缀 → 段内去掉控制字符。
// 返回空串表示无法安全化（调用方回退到文件名/hash）。
func sanitizeRelPath(p string) string {
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "\\", "/")
	segs := strings.Split(p, "/")
	out := make([]string, 0, len(segs))
	for _, s := range segs {
		s = strings.TrimSpace(s)
		if s == "" || s == "." || s == ".." {
			continue
		}
		// 控制字符与 Windows 保留字符会让 rename/展示出问题
		s = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			switch r {
			case ':', '*', '?', '"', '<', '>', '|':
				return '_'
			}
			return r
		}, s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return strings.Join(out, string(filepath.Separator))
}
