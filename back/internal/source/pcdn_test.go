package source

// pcdn_test.go: PCDNSource 与 PeerScoreMatrix 核心调度与切片回源单元测试 (Issue #317)。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/transport"
)

// mockPCDNPeerService 内存中模拟 PCDNPeerService 对端传输服务。
type mockPCDNPeerService struct {
	selfID string

	mu       sync.Mutex
	conns    map[string]transport.Session
	handlers map[string]func(peerID, hash string, offset, size int64) (io.ReadCloser, error)
}

func newMockPCDNPeerService(selfID string) *mockPCDNPeerService {
	return &mockPCDNPeerService{
		selfID:   selfID,
		conns:    make(map[string]transport.Session),
		handlers: make(map[string]func(peerID, hash string, offset, size int64) (io.ReadCloser, error)),
	}
}

func (m *mockPCDNPeerService) ID() string { return m.selfID }

func (m *mockPCDNPeerService) Connections() map[string]transport.Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make(map[string]transport.Session, len(m.conns))
	for k, v := range m.conns {
		res[k] = v
	}
	return res
}

func (m *mockPCDNPeerService) addPeer(id string, handler func(peerID, hash string, offset, size int64) (io.ReadCloser, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns[id] = newFakePeerSess(id)
	m.handlers[id] = handler
}

func (m *mockPCDNPeerService) OpenStreamFrom(peerID, hash string, offset, size int64, trace []string) (io.ReadCloser, error) {
	m.mu.Lock()
	h, ok := m.handlers[peerID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mock: peer %s not found", peerID)
	}
	return h(peerID, hash, offset, size)
}

// mockOriginSource 内存中模拟 Origin 源。
type mockOriginSource struct {
	name     string
	content  []byte
	hash     string
	mu       sync.Mutex
	priority int
}

func newMockOriginSource(content []byte) *mockOriginSource {
	h := sha256Hex(content)
	return &mockOriginSource{
		name:    "mock-origin",
		content: content,
		hash:    h,
	}
}

func (m *mockOriginSource) Name() string               { return m.name }
func (m *mockOriginSource) Type() string               { return "mock" }
func (m *mockOriginSource) Capabilities() Capability   { return CapStream | CapVerify | CapMeta }
func (m *mockOriginSource) Priority() int              { return m.priority }
func (m *mockOriginSource) SetPriority(p int)          { m.priority = p }
func (m *mockOriginSource) Available(ctx context.Context) bool { return true }

func (m *mockOriginSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if hash == m.hash {
		return &FileMeta{Hash: m.hash, Size: int64(len(m.content))}, nil
	}
	return nil, nil
}

func (m *mockOriginSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if hash != m.hash {
		return nil, fmt.Errorf("mock origin: hash not found")
	}
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(m.content)) {
		offset = int64(len(m.content))
	}
	end := int64(len(m.content))
	if size >= 0 && offset+size < end {
		end = offset + size
	}
	sub := m.content[offset:end]
	return io.NopCloser(bytes.NewReader(sub)), nil
}

func (m *mockOriginSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if hash != m.hash {
		return nil, fmt.Errorf("mock origin: hash not found")
	}
	return m.content, nil
}

// 发现背景：验证 PCDN 节点质量评分矩阵在记录传输成功后，能够平滑计算滑动平均 RTT 和 EWMA 吞吐速率，并在后续节点排序中将高分节点优先排在前面。
func TestPeerScoreMatrix_SuccessAndEWMA(t *testing.T) {
	matrix := NewPeerScoreMatrix()

	// 记录节点 A：低延迟（10ms）、高吞吐（1MB/0.1s = 10MB/s）
	matrix.RecordSuccess("node-a", 10*time.Millisecond, 1024*1024, 100*time.Millisecond)

	// 记录节点 B：高延迟（100ms）、低吞吐（1MB/1.0s = 1MB/s）
	matrix.RecordSuccess("node-b", 100*time.Millisecond, 1024*1024, 1000*time.Millisecond)

	ranked := matrix.RankPeers([]string{"node-b", "node-a"})
	require.Len(t, ranked, 2)
	assert.Equal(t, "node-a", ranked[0], "高吞吐节点应优先排在第 1 位")
	assert.Equal(t, "node-b", ranked[1])

	snap := matrix.Snapshot()
	assert.True(t, snap["node-a"].ThroughputBps > snap["node-b"].ThroughputBps)
	assert.True(t, snap["node-a"].RTT < snap["node-b"].RTT)
}

// 发现背景：验证在对端连续发生传输失败达到设定阈值时，评分矩阵能够触发熔断并将该 Peer 移至冷却状态，同时在节点排序时将其降级至队尾。
func TestPeerScoreMatrix_CircuitBreaker(t *testing.T) {
	matrix := NewPeerScoreMatrix()
	matrix.maxConsecutiveFailures = 2
	matrix.cooldownDuration = 100 * time.Millisecond

	// 节点 A 正常
	matrix.RecordSuccess("node-a", 20*time.Millisecond, 512*1024, 50*time.Millisecond)

	// 节点 B 发生连续 2 次失败 -> 触发熔断
	matrix.RecordFailure("node-b")
	assert.True(t, matrix.IsAvailable("node-b"), "尚未达到连续 2 次失败，暂不熔断")

	matrix.RecordFailure("node-b")
	assert.False(t, matrix.IsAvailable("node-b"), "连续失败 2 次，应进入熔断冷却")

	// 节点排序：可用节点优先，熔断节点靠后
	ranked := matrix.RankPeers([]string{"node-b", "node-a"})
	assert.Equal(t, "node-a", ranked[0], "正常节点排在最前")
	assert.Equal(t, "node-b", ranked[1], "熔断节点沉底")

	// 等待冷却期过去
	time.Sleep(120 * time.Millisecond)
	assert.True(t, matrix.IsAvailable("node-b"), "冷却期已过，恢复半开/可用状态")
}

// 发现背景：在多切片 PCDN 分发场景下，验证调度器能够将一个跨多个 1KB 切片的大数据划分为多个并发切片任务，并发拉取并在下游管道中按绝对偏移顺序无缝拼接。
func TestPCDNSource_MultiChunkOrderedAssembly(t *testing.T) {
	ctx := context.Background()
	payload := []byte(strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", 100)) // 3600 字节
	hash := sha256Hex(payload)

	svc := newMockPCDNPeerService("self-node")
	// 注册节点 A 提供数据
	svc.addPeer("peer-fast", func(peerID, h string, offset, size int64) (io.ReadCloser, error) {
		end := offset + size
		if end > int64(len(payload)) {
			end = int64(len(payload))
		}
		return io.NopCloser(bytes.NewReader(payload[offset:end])), nil
	})

	origin := newMockOriginSource(payload)
	cfg := PCDNConfig{
		ChunkSize:       512, // 512 字节分片，触发多次切片派发
		WorkerCount:     2,
		ChunkTimeout:    time.Second,
		MaxPeerFailures: 3,
	}

	pcdn := NewPCDNSource("pcdn-test", svc, origin, cfg)
	assert.Equal(t, "pcdn-test", pcdn.Name())
	assert.Equal(t, "pcdn", pcdn.Type())
	assert.True(t, pcdn.Available(ctx))

	// 打开全量范围
	rc, err := pcdn.Open(ctx, hash, 0, int64(len(payload)))
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, payload, got, "下游读取拼接的数据必须与原始内容逐字节完全一致")
}

// 发现背景：验证关键的单切片容灾能力：当对端服务在获取某一个特定切片时模拟超时或报错，PCDN 调度器能够透明降级回源至预配置的 Origin 源拉取该切片，而下游流式消费者能够完整读取到所有数据，不受单点故障影响。
func TestPCDNSource_PerChunkFallbackToOrigin(t *testing.T) {
	ctx := context.Background()
	payload := []byte("Chunk0-Payload-AAA_Chunk1-Payload-BBB_Chunk2-Payload-CCC")
	hash := sha256Hex(payload)

	svc := newMockPCDNPeerService("self-node")
	// 模拟 peer-buggy：在获取第 2 个切片（offset >= 20）时故意报错
	svc.addPeer("peer-buggy", func(peerID, h string, offset, size int64) (io.ReadCloser, error) {
		if offset >= 20 && offset < 40 {
			return nil, errors.New("simulated network drop on chunk 1")
		}
		end := offset + size
		if end > int64(len(payload)) {
			end = int64(len(payload))
		}
		return io.NopCloser(bytes.NewReader(payload[offset:end])), nil
	})

	origin := newMockOriginSource(payload)
	cfg := PCDNConfig{
		ChunkSize:       20, // 切片大小 20 字节
		WorkerCount:     1,
		ChunkTimeout:    time.Second,
		MaxPeerFailures: 5,
	}

	pcdn := NewPCDNSource("pcdn-fallback", svc, origin, cfg)
	rc, err := pcdn.Open(ctx, hash, 0, int64(len(payload)))
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err, "单切片失败时应当透明回源，不报错")
	assert.Equal(t, payload, got, "回源与对端切片拼接后应与完整原数据一致")
}

// 发现背景：验证在完全没有在线对端节点（网络拓扑初始期或对端全部掉线）的极端情况下，PCDN 源能够透明全量走 Origin 源回源，保证基础可用性。
func TestPCDNSource_NoPeersDirectOriginFallback(t *testing.T) {
	ctx := context.Background()
	payload := []byte("DirectOriginData-NoPeersOnline")
	hash := sha256Hex(payload)

	svc := newMockPCDNPeerService("self-node") // 无任何在线 peer
	origin := newMockOriginSource(payload)

	pcdn := NewPCDNSource("pcdn-empty-peers", svc, origin, DefaultPCDNConfig())
	assert.True(t, pcdn.Available(ctx), "即使无对端但有 origin 时仍然可用")

	rc, err := pcdn.Open(ctx, hash, 0, int64(len(payload)))
	require.NoError(t, err)
	defer rc.Close()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

// 发现背景：验证内容寻址安全基线：当请求为全量整文件（offset == 0 && size < 0）时，若传输的数据中途被恶意节点篡改，在 EOF 处能够由 verifyReadCloser 正确拦截并返回内容哈希失配错误。
func TestPCDNSource_FullFileVerification(t *testing.T) {
	ctx := context.Background()
	realPayload := []byte("AuthenticTrustedPCDNContent")
	realHash := sha256Hex(realPayload)

	tamperedPayload := []byte("MaliciousTamperedPCDNContent")

	svc := newMockPCDNPeerService("self-node")
	svc.addPeer("peer-evil", func(peerID, h string, offset, size int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(tamperedPayload)), nil
	})

	origin := newMockOriginSource(tamperedPayload) // 模拟源端返回了篡改数据
	// 强制 origin 的 info 返回真实期望 hash
	origin.hash = realHash

	pcdn := NewPCDNSource("pcdn-verify", svc, origin, DefaultPCDNConfig())

	// 整文件读取：offset == 0 且 size == -1
	rc, err := pcdn.Open(ctx, realHash, 0, -1)
	require.NoError(t, err)
	defer rc.Close()

	_, err = io.ReadAll(rc)
	require.Error(t, err, "内容被篡改必须返回哈希校验失败")
	assert.Contains(t, err.Error(), "content hash mismatch")
}
