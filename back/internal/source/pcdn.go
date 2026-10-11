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
//    连续失败熔断机制（Circuit Breaker），保证优先将后续切片派发给最优节点（见 pcdn_score.go）。
// 3. 多路并行分片调度器 (ChunkScheduler)：并发 Worker 池向评分最高的 Peer 派发切片抓取任务（见 pcdn_scheduler.go）。
// 4. 切片级即时回源 (Per-Chunk Fallback)：当某分片在 Peer 传输中失败或超时，自动切换至
//    预配置的 Origin 源（HTTP CDN / Cloudflare / 本地源）拉取该分片，下游消费者零感知无缝重组。
// 5. 内容寻址安全基线：声明 CapStream | CapVerify，整文件请求在 EOF 处自动复用
//    verifyReadCloser 完成 SHA-256 完整性校验。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// DefaultPCDNChunkSize 默认 PCDN 切片大小：1MB。
const DefaultPCDNChunkSize int64 = 1024 * 1024

// DefaultPCDNWorkerCount 默认多 Peer 并发 Worker 数量。
const DefaultPCDNWorkerCount = 4

// DefaultPCDNChunkTimeout 单切片对端拉取超时阈值（超时即触发回源）。
const DefaultPCDNChunkTimeout = 3 * time.Second

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

// emptyReader 空数据读取器。
type emptyReader struct{}

func (emptyReader) Read(p []byte) (int, error) { return 0, io.EOF }

// 确保 PCDNSource 实现了 Source 契约。
var _ Source = (*PCDNSource)(nil)
