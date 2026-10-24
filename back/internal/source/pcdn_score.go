package source

import (
	"io"
	"sort"
	"sync"
	"time"

	"peerdrive/internal/transport"
)

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
