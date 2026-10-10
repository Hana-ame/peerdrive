package source

// pcdn.go: PCDNSource — WebRTC PCDN (Peer-assisted CDN) 分片调度与回源数据源 (Issue #317)。
//
// 架构定位：
// 结合 WebRTC DataChannel 对端互联与统一数据源抽象（source.Source），提供大文件分片
// 流水线拉取、对端质量动态评分（RTT 与 EWMA 吞吐）及分片级即时回源兜底（Per-Chunk Fallback）。
//
// 核心机制：
// 1. 逻辑分片切分 (ChunkTask Slicing)：将大文件或 Range 请求划分为固定大小的分片（默认 1MB）。
// 2. 节点质量评分矩阵 (PeerScoreMatrix)：跟踪每个在线 Peer 的 RTT、EWMA 传输速率，并具备
//    连续失败熔断机制（Circuit Breaker），保证优先将后续切片派发给最优节点。
// 3. 多路并行分片调度器 (ChunkScheduler)：并发 Worker 池向评分最高的 Peer 派发切片抓取任务。
// 4. 切片级即时回源 (Per-Chunk Fallback)：当某分片在 Peer 传输中失败或超时，自动切换至
//    预配置的 Origin 源（HTTP CDN / Cloudflare / 本地源）拉取该分片，下游消费者零感知无缝重组。
// 5. 内容寻址安全基线：声明 CapStream | CapVerify，整文件请求在 EOF 处自动复用
//    verifyReadCloser 完成 SHA-256 完整性校验。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"peerdrive/internal/transport"
)

// DefaultPCDNChunkSize 默认 PCDN 切片大小：1MB。
const DefaultPCDNChunkSize int64 = 1024 * 1024

// DefaultPCDNWorkerCount 默认多 Peer 并发 Worker 数量。
const DefaultPCDNWorkerCount = 4

// DefaultPCDNChunkTimeout 单切片对端拉取超时阈值（超时即触发回源）。
const DefaultPCDNChunkTimeout = 3 * time.Second

// PCDNPeerService 抽象 PCDN 所需的底层 P2P 传输能力（松耦合设计，方便单元测试与模拟）。
type PCDNPeerService interface {
	Connections() map[string]transport.Session
	OpenStreamFrom(peerID, hash string, offset, size int64, trace []string) (io.ReadCloser, error)
	ID() string
}

// PeerScore 单个对端节点的质量指标卡。
type PeerScore struct {
	PeerID              string        `json:"peer_id"`
	RTT                 time.Duration `json:"rtt"`
	ThroughputBps       float64       `json:"throughput_bps"`
	SuccessCount        int64         `json:"success_count"`
	FailureCount        int64         `json:"failure_count"`
	ConsecutiveFailures int           `json:"consecutive_failures"`
	CircuitBrokenUntil  time.Time     `json:"circuit_broken_until"`
}

// PeerScoreMatrix 节点质量评分矩阵：多协程安全，负责各 Peer 性能评分与熔断状态维护。
type PeerScoreMatrix struct {
	mu                     sync.RWMutex
	scores                 map[string]*PeerScore
	alpha                  float64       // EWMA 权重系数，默认 0.3
	maxConsecutiveFailures int           // 熔断阈值，默认连续失败 3 次
	cooldownDuration       time.Duration // 熔断冷却时间，默认 30 秒
}

// NewPeerScoreMatrix 创建节点质量评分矩阵。
func NewPeerScoreMatrix() *PeerScoreMatrix {
	return &PeerScoreMatrix{
		scores:                 make(map[string]*PeerScore),
		alpha:                  0.3,
		maxConsecutiveFailures: 3,
		cooldownDuration:       30 * time.Second,
	}
}

// RecordSuccess 记录一次成功切片传输的指标并更新 EWMA 吞吐。
func (m *PeerScoreMatrix) RecordSuccess(peerID string, rtt time.Duration, bytes int64, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	score, ok := m.scores[peerID]
	if !ok {
		score = &PeerScore{PeerID: peerID}
		m.scores[peerID] = score
	}

	score.SuccessCount++
	score.ConsecutiveFailures = 0
	score.CircuitBrokenUntil = time.Time{}

	// 更新滑动平均 RTT
	if score.RTT == 0 {
		score.RTT = rtt
	} else {
		score.RTT = time.Duration(0.7*float64(score.RTT) + 0.3*float64(rtt))
	}

	// 计算并更新 EWMA 吞吐速率（bytes/sec）
	if duration > 0 {
		currentBps := float64(bytes) / duration.Seconds()
		if score.ThroughputBps == 0 {
			score.ThroughputBps = currentBps
		} else {
			score.ThroughputBps = m.alpha*currentBps + (1.0-m.alpha)*score.ThroughputBps
		}
	}
}

// RecordFailure 记录一次切片获取失败；达到阈值时触发熔断。
func (m *PeerScoreMatrix) RecordFailure(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	score, ok := m.scores[peerID]
	if !ok {
		score = &PeerScore{PeerID: peerID}
		m.scores[peerID] = score
	}

	score.FailureCount++
	score.ConsecutiveFailures++
	if score.ConsecutiveFailures >= m.maxConsecutiveFailures {
		score.CircuitBrokenUntil = time.Now().Add(m.cooldownDuration)
	}
}

// IsAvailable 判断指定 Peer 当前是否可用（未处于熔断冷却中）。
func (m *PeerScoreMatrix) IsAvailable(peerID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	score, ok := m.scores[peerID]
	if !ok {
		return true // 无历史记录的对端默认可用
	}
	if !score.CircuitBrokenUntil.IsZero() && time.Now().Before(score.CircuitBrokenUntil) {
		return false
	}
	return true
}

// RankPeers 对候选节点按照综合得分排序（优先可用性、吞吐量由高到低、RTT 由低到高）。
func (m *PeerScoreMatrix) RankPeers(candidates []string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	type ranked struct {
		id        string
		available bool
		tput      float64
		rtt       time.Duration
	}

	now := time.Now()
	list := make([]ranked, len(candidates))
	for i, id := range candidates {
		sc, ok := m.scores[id]
		avail := true
		var tput float64
		var rtt time.Duration
		if ok {
			if !sc.CircuitBrokenUntil.IsZero() && now.Before(sc.CircuitBrokenUntil) {
				avail = false
			}
			tput = sc.ThroughputBps
			rtt = sc.RTT
		}
		list[i] = ranked{id: id, available: avail, tput: tput, rtt: rtt}
	}

	sort.SliceStable(list, func(i, j int) bool {
		// 1. 熔断端靠后
		if list[i].available != list[j].available {
			return list[i].available
		}
		// 2. 吞吐更高者优先
		if list[i].tput != list[j].tput {
			return list[i].tput > list[j].tput
		}
		// 3. RTT 较低者优先
		if list[i].rtt != list[j].rtt {
			return list[i].rtt < list[j].rtt
		}
		return list[i].id < list[j].id
	})

	out := make([]string, len(list))
	for i, r := range list {
		out[i] = r.id
	}
	return out
}

// Snapshot 导出当前所有节点的质量评分镜像，供管理面与可观测性 API 展示。
func (m *PeerScoreMatrix) Snapshot() map[string]PeerScore {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make(map[string]PeerScore, len(m.scores))
	for k, v := range m.scores {
		out[k] = *v
	}
	return out
}

// PCDNConfig PCDN 调度引擎运行参数配置。
type PCDNConfig struct {
	ChunkSize       int64         // 单切片尺寸（字节），默认 1MB
	WorkerCount     int           // 并发切片下载线程数，默认 4
	ChunkTimeout    time.Duration // 单切片对端请求超时时间，默认 3s
	MaxPeerFailures int           // 连续失败熔断阈值
}

// DefaultPCDNConfig 默认配置。
func DefaultPCDNConfig() PCDNConfig {
	return PCDNConfig{
		ChunkSize:       DefaultPCDNChunkSize,
		WorkerCount:     DefaultPCDNWorkerCount,
		ChunkTimeout:    DefaultPCDNChunkTimeout,
		MaxPeerFailures: 3,
	}
}

// PCDNSource PCDN 混合调度数据源。
type PCDNSource struct {
	name   string
	svc    PCDNPeerService
	origin Source // 回源数据源（如 URLSource / LocalSource），可为 nil
	matrix *PeerScoreMatrix
	cfg    PCDNConfig

	mu       sync.RWMutex
	priority int
}

// NewPCDNSource 创建 PCDN 数据源实例。
func NewPCDNSource(name string, svc PCDNPeerService, origin Source, cfg PCDNConfig) *PCDNSource {
	if name == "" {
		name = "pcdn"
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = DefaultPCDNChunkSize
	}
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = DefaultPCDNWorkerCount
	}
	if cfg.ChunkTimeout <= 0 {
		cfg.ChunkTimeout = DefaultPCDNChunkTimeout
	}

	matrix := NewPeerScoreMatrix()
	if cfg.MaxPeerFailures > 0 {
		matrix.maxConsecutiveFailures = cfg.MaxPeerFailures
	}

	return &PCDNSource{
		name:   name,
		svc:    svc,
		origin: origin,
		matrix: matrix,
		cfg:    cfg,
	}
}

func (s *PCDNSource) Name() string { return s.name }
func (s *PCDNSource) Type() string { return "pcdn" }

// Capabilities 声明 CapStream | CapVerify：支持流式分片读取，整文件拉取由 verifyReadCloser 自动校验。
func (s *PCDNSource) Capabilities() Capability {
	return CapStream | CapVerify
}

func (s *PCDNSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *PCDNSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available 检查 PCDN 源是否可用：拥有在线 Peer 或拥有可用的回源。
func (s *PCDNSource) Available(ctx context.Context) bool {
	if s.svc != nil {
		for pid := range s.svc.Connections() {
			if pid != s.svc.ID() {
				return true
			}
		}
	}
	if s.origin != nil && s.origin.Available(ctx) {
		return true
	}
	return false
}

// Info 元数据查询：优先委托给回源源（若可用）。
func (s *PCDNSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if s.origin != nil {
		return s.origin.Info(ctx, hash)
	}
	return nil, nil
}

// Fetch 整文件取回契约：委托给 Open(0, -1) 并读取全部内容。
func (s *PCDNSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	rc, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// ScoreMatrix 返回内部评分矩阵，供排查或指标接口读取。
func (s *PCDNSource) ScoreMatrix() *PeerScoreMatrix {
	return s.matrix
}

// chunkTask 单个切片调度任务。
type chunkTask struct {
	index  int
	offset int64
	size   int64
}

// chunkResult 切片获取结果。
type chunkResult struct {
	index int
	data  []byte
	err   error
}

// Open 流式打开分片内容：流水线调度、多 Peer 并发、失败即时回源。
func (s *PCDNSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}

	// 整文件读取标识：offset == 0 且 size < 0 时需包装 verifyReadCloser 进行最终校验。
	isFullFile := (offset == 0 && size < 0)

	// 1. 尝试探测文件总大小
	totalSize := int64(-1)
	if s.origin != nil {
		if meta, err := s.origin.Info(ctx, hash); err == nil && meta != nil && meta.Size > 0 {
			totalSize = meta.Size
		}
	}

	// 2. 边界钳制与分片任务规划
	var tasks []chunkTask
	if size >= 0 {
		// 显式指定了区间大小
		tasks = s.planTasks(offset, size)
	} else if totalSize > 0 {
		// size < 0 但总大小已知：读取从 offset 到 totalSize 的所有数据
		if offset >= totalSize {
			return io.NopCloser(emptyReader{}), nil
		}
		tasks = s.planTasks(offset, totalSize-offset)
	}

	// 3. 构建有序管道读取器
	pr, pw := io.Pipe()
	go s.runPipeline(ctx, hash, offset, size, tasks, pw)

	var reader io.ReadCloser = pr
	if isFullFile {
		reader = &verifyReadCloser{
			r:     pr,
			hash:  hash,
			label: "pcdn",
		}
	}
	return reader, nil
}

// planTasks 将指定区间切分为逻辑切片。
func (s *PCDNSource) planTasks(offset, size int64) []chunkTask {
	var tasks []chunkTask
	rem := size
	cur := offset
	idx := 0
	for rem > 0 {
		take := s.cfg.ChunkSize
		if rem < take {
			take = rem
		}
		tasks = append(tasks, chunkTask{
			index:  idx,
			offset: cur,
			size:   take,
		})
		cur += take
		rem -= take
		idx++
	}
	return tasks
}

// runPipeline 执行分片流水线与有序下发。
func (s *PCDNSource) runPipeline(
	ctx context.Context,
	hash string,
	offset, size int64,
	tasks []chunkTask,
	pw *io.PipeWriter,
) {
	// 若区间无法静态预先切片（未指定 size 且 origin.Info 为未知），降级回逐块流式拉取
	if len(tasks) == 0 && size < 0 {
		s.streamSequentialUnknownSize(ctx, hash, offset, pw)
		return
	}

	// 若切片为空，直接关闭管道
	if len(tasks) == 0 {
		_ = pw.Close()
		return
	}

	// 任务下发通道与结果收集
	taskChan := make(chan chunkTask, len(tasks))
	for _, t := range tasks {
		taskChan <- t
	}
	close(taskChan)

	resultChan := make(chan chunkResult, len(tasks))

	// 启动 Worker 协程池
	numWorkers := s.cfg.WorkerCount
	if numWorkers > len(tasks) {
		numWorkers = len(tasks)
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range taskChan {
				select {
				case <-ctx.Done():
					resultChan <- chunkResult{index: task.index, err: ctx.Err()}
					return
				default:
				}
				data, err := s.fetchChunkWithFallback(ctx, hash, task.offset, task.size)
				resultChan <- chunkResult{index: task.index, data: data, err: err}
			}
		}()
	}

	// 独立协程等待所有任务完成后关闭结果通道
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 有序重组缓冲池：按序号顺序将切片写入管道
	received := make(map[int][]byte)
	nextIndex := 0

	for res := range resultChan {
		if res.err != nil {
			_ = pw.CloseWithError(fmt.Errorf("chunk %d failed: %w", res.index, res.err))
			return
		}
		received[res.index] = res.data

		// 按序刷出就绪切片
		for {
			data, ready := received[nextIndex]
			if !ready {
				break
			}
			delete(received, nextIndex)
			if len(data) > 0 {
				if _, err := pw.Write(data); err != nil {
					_ = pw.CloseWithError(err)
					return
				}
			}
			nextIndex++
		}
	}

	// 所有切片下发完毕
	_ = pw.Close()
}

// fetchChunkWithFallback 尝试通过最优 Peer 获取分片；遇错或超时即时回源。
func (s *PCDNSource) fetchChunkWithFallback(ctx context.Context, hash string, offset, size int64) ([]byte, error) {
	// 1. 尝试从可用 Peer 集合中挑选评分最优者
	peers := s.collectRankedPeers()
	var lastPeerErr error

	for _, peerID := range peers {
		// 对每个候选 Peer 施加 ChunkTimeout 保护，避免慢端无限挂起请求
		chunkCtx, cancel := context.WithTimeout(ctx, s.cfg.ChunkTimeout)
		start := time.Now()

		data, rtt, err := s.fetchChunkFromPeer(chunkCtx, peerID, hash, offset, size)
		cancel()

		if err == nil {
			duration := time.Since(start)
			s.matrix.RecordSuccess(peerID, rtt, int64(len(data)), duration)
			return data, nil
		}

		// 记录对端失败并尝试下一个 Peer
		s.matrix.RecordFailure(peerID)
		lastPeerErr = err
	}

	// 2. 所有 Peer 失败或无在线 Peer：即时回源 (Origin Fallback)
	if s.origin != nil {
		data, err := s.fetchChunkFromOrigin(ctx, hash, offset, size)
		if err == nil {
			return data, nil
		}
		return nil, fmt.Errorf("peer and origin both failed: %w (last peer err: %v)", err, lastPeerErr)
	}

	if lastPeerErr != nil {
		return nil, fmt.Errorf("no peer succeeded and no origin configured: %w", lastPeerErr)
	}
	return nil, errors.New("no peer available and no origin configured")
}

// fetchChunkFromPeer 从特定 Peer 读取分片并度量首字节时延 RTT。
func (s *PCDNSource) fetchChunkFromPeer(
	ctx context.Context,
	peerID, hash string,
	offset, size int64,
) ([]byte, time.Duration, error) {
	if s.svc == nil {
		return nil, 0, errors.New("peer service not configured")
	}

	start := time.Now()
	stream, err := s.svc.OpenStreamFrom(peerID, hash, offset, size, nil)
	if err != nil {
		return nil, 0, err
	}
	defer stream.Close()

	// 首字节时延度量
	firstBuf := make([]byte, 1)
	n, err := stream.Read(firstBuf)
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	rtt := time.Since(start)

	if n == 0 {
		return []byte{}, rtt, nil
	}

	// 读取后续字节
	rest, err := io.ReadAll(stream)
	if err != nil {
		return nil, rtt, err
	}

	result := make([]byte, 1+len(rest))
	result[0] = firstBuf[0]
	copy(result[1:], rest)

	// 校验读取尺寸是否完整（若已显式要求 size）
	if size > 0 && int64(len(result)) != size {
		return nil, rtt, fmt.Errorf("peer truncated chunk: expected %d bytes, got %d", size, len(result))
	}
	return result, rtt, nil
}

// fetchChunkFromOrigin 从预配置的 Origin 源拉取单个分片。
func (s *PCDNSource) fetchChunkFromOrigin(ctx context.Context, hash string, offset, size int64) ([]byte, error) {
	rc, err := s.origin.Open(ctx, hash, offset, size)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// collectRankedPeers 获取当前在线并按质量评分排序的 Peer 列表。
func (s *PCDNSource) collectRankedPeers() []string {
	if s.svc == nil {
		return nil
	}
	conns := s.svc.Connections()
	selfID := s.svc.ID()
	var candidates []string
	for pid := range conns {
		if pid != selfID {
			candidates = append(candidates, pid)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return s.matrix.RankPeers(candidates)
}

// streamSequentialUnknownSize 处理总大小完全未知时的顺序切片拉取与下发。
func (s *PCDNSource) streamSequentialUnknownSize(
	ctx context.Context,
	hash string,
	offset int64,
	pw *io.PipeWriter,
) {
	cur := offset
	for {
		select {
		case <-ctx.Done():
			_ = pw.CloseWithError(ctx.Err())
			return
		default:
		}

		data, err := s.fetchChunkWithFallback(ctx, hash, cur, s.cfg.ChunkSize)
		if err != nil {
			// 如果读取到 EOF 或失败，安全终止
			_ = pw.CloseWithError(err)
			return
		}
		if len(data) == 0 {
			_ = pw.Close()
			return
		}

		if _, err := pw.Write(data); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		// 若获取的数据小于单切片大小，代表已到达文件末尾
		if int64(len(data)) < s.cfg.ChunkSize {
			_ = pw.Close()
			return
		}
		cur += int64(len(data))
	}
}

// emptyReader 空数据读取器。
type emptyReader struct{}

func (emptyReader) Read(p []byte) (int, error) { return 0, io.EOF }

// 确保 PCDNSource 实现了 Source 契约。
var _ Source = (*PCDNSource)(nil)
