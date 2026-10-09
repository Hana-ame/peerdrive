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
