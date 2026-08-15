// ConnectionManager 管理 P2P 对端连接，支持心跳检测、自动重连和断线重连。
package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"peerdrive/internal/log"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ConnectionQuality 表示对指定 P2P 对端的连接质量评估。
type ConnectionQuality struct {
	PeerID        string  `json:"peer_id"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	JitterMs      float64 `json:"jitter_ms"`
	PacketLossPct float64 `json:"packet_loss_pct"`
	Score         float64 `json:"score"` // 0-100, higher is better
	LastUpdated   string  `json:"last_updated"`
	Samples       int     `json:"samples"`
}

// ConnectionManager handles auto-connection, heartbeat, and reconnection to peers.
type ConnectionManager struct {
	svc *P2PService

	mu           sync.Mutex
	knownPeers   map[peer.ID]peer.AddrInfo
	heartbeatCtx context.Context
	heartbeatCan context.CancelFunc

	// Stats
	reconnectAttempts int64
	successfulConns   int64
	failedConns       int64

	// Quality metrics
	latencyHistory    map[peer.ID][]time.Duration
	maxLatencySamples int
}

const (
	heartbeatInterval   = 30 * time.Second
	reconnectInterval   = 10 * time.Second
	maxReconnectBackoff = 5 * time.Minute
	connectionTimeout   = 15 * time.Second
	maxLatencySamples   = 10
)

// NewConnectionManager 创建一个 P2P 连接管理器实例。
func NewConnectionManager(svc *P2PService) *ConnectionManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ConnectionManager{
		svc:               svc,
		knownPeers:        make(map[peer.ID]peer.AddrInfo),
		heartbeatCtx:      ctx,
		heartbeatCan:      cancel,
		latencyHistory:    make(map[peer.ID][]time.Duration),
		maxLatencySamples: maxLatencySamples,
	}
}

// AddKnownPeer adds a peer to the known peers list for auto-reconnection.
// AddKnownPeer 将已知对端添加到连接管理器的跟踪列表。
func (cm *ConnectionManager) AddKnownPeer(info peer.AddrInfo) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.knownPeers[info.ID] = info
}

// RemoveKnownPeer removes a peer from tracking.
// RemoveKnownPeer 从连接管理器中移除指定对端。
func (cm *ConnectionManager) RemoveKnownPeer(id peer.ID) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.knownPeers, id)
}

// GetKnownPeers returns all tracked peers.
// GetKnownPeers 返回所有已知对端的信息列表。
func (cm *ConnectionManager) GetKnownPeers() []peer.AddrInfo {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	result := make([]peer.AddrInfo, 0, len(cm.knownPeers))
	for _, info := range cm.knownPeers {
		result = append(result, info)
	}
	return result
}

// ConnectToPeer attempts to connect to a peer with retry logic.
// ConnectToPeer 连接到指定的远程对端，支持自动重试和回退。
func (cm *ConnectionManager) ConnectToPeer(ctx context.Context, info peer.AddrInfo) error {
	defer log.LogDuration("ConnectionManager.ConnectToPeer")()
	log.LogDebug("p2p-conn: ConnectToPeer %s", info.ID.String())

	if !cm.svc.IsEnabled() {
		err := fmt.Errorf("p2p not enabled")
		log.LogError("p2p-conn: ConnectToPeer failed: %v", err)
		return err
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
	defer cancel()

	if err := cm.svc.Host.Connect(connectCtx, info); err != nil {
		cm.mu.Lock()
		cm.failedConns++
		cm.mu.Unlock()
		log.LogError("p2p-conn: connect to %s failed: %v", info.ID.String(), err)
		return fmt.Errorf("connect to %s: %w", info.ID, err)
	}

	cm.AddKnownPeer(info)
	cm.mu.Lock()
	cm.successfulConns++
	cm.mu.Unlock()

	log.LogInfo("p2p-conn: connected to peer: %s", info.ID.String())
	return nil
}

// StartHeartbeat begins periodic health checks on connected peers.
// StartHeartbeat 启动心跳检测协程，定时检查连接状态。
func (cm *ConnectionManager) StartHeartbeat() {
	log.LogDebug("p2p-conn: StartHeartbeat beginning")
	go cm.heartbeatLoop()
}

// StopHeartbeat stops the heartbeat goroutine.
// StopHeartbeat 停止心跳检测协程。
func (cm *ConnectionManager) StopHeartbeat() {
	log.LogDebug("p2p-conn: StopHeartbeat")
	if cm.heartbeatCan != nil {
		cm.heartbeatCan()
	}
}

func (cm *ConnectionManager) heartbeatLoop() {
	log.LogDebug("p2p-conn: heartbeatLoop started")
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.heartbeatCtx.Done():
			log.LogDebug("p2p-conn: heartbeatLoop stopped")
			return
		case <-ticker.C:
			cm.checkAndReconnect()
		}
	}
}

func (cm *ConnectionManager) checkAndReconnect() {
	if !cm.svc.IsEnabled() {
		return
	}

	cm.mu.Lock()
	peers := make([]peer.AddrInfo, 0, len(cm.knownPeers))
	for _, info := range cm.knownPeers {
		peers = append(peers, info)
	}
	cm.mu.Unlock()

	for _, info := range peers {
		connState := cm.svc.Host.Network().Connectedness(info.ID)
		if connState != network.Connected {
			log.LogWarn("p2p-conn: reconnecting to peer: %s", info.ID)
			ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
			if err := cm.svc.Host.Connect(ctx, info); err != nil {
				cm.mu.Lock()
				cm.reconnectAttempts++
				cm.mu.Unlock()
				log.LogWarn("p2p-conn: reconnect failed to %s: %v", info.ID, err)
			} else {
				cm.mu.Lock()
				cm.successfulConns++
				cm.mu.Unlock()
				log.LogInfo("p2p-conn: reconnect successful to %s", info.ID)
			}
			cancel()
		}
	}
}

// AutoConnectFromDiscovered connects to all currently discovered peers.
// AutoConnectFromDiscovered 自动连接所有已发现的未连接对端。
func (cm *ConnectionManager) AutoConnectFromDiscovered() {
	defer log.LogDuration("ConnectionManager.AutoConnectFromDiscovered")()
	log.LogDebug("p2p-conn: AutoConnectFromDiscovered starting")

	if !cm.svc.IsEnabled() {
		log.LogDebug("p2p-conn: AutoConnectFromDiscovered skipped (P2P disabled)")
		return
	}

	discovered := cm.svc.GetDiscoveredPeers()
	log.LogInfo("p2p-conn: AutoConnectFromDiscovered found %d peers", len(discovered))
	for _, info := range discovered {
		if cm.svc.Host.Network().Connectedness(info.ID) == network.Connected {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		cm.ConnectToPeer(ctx, info)
		cancel()
	}
}

// GetPeerLatency returns the last known RTT for a peer.
// GetPeerLatency 测量并返回与指定对端的网络延迟。
func (cm *ConnectionManager) GetPeerLatency(ctx context.Context, peerID peer.ID) (time.Duration, error) {
	return cm.svc.PingPeer(ctx, peerID)
}

// RecordLatencySample records a latency measurement for a peer and updates
// quality metrics. RecordLatencySample 记录一次对端的延迟测量样本。
func (cm *ConnectionManager) RecordLatencySample(peerID peer.ID, rtt time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	history := cm.latencyHistory[peerID]
	history = append(history, rtt)
	if len(history) > cm.maxLatencySamples {
		history = history[len(history)-cm.maxLatencySamples:]
	}
	cm.latencyHistory[peerID] = history
}

// GetConnectionQuality returns the computed connection quality for a peer.
// GetConnectionQuality 返回指定对端的连接质量评估。
func (cm *ConnectionManager) GetConnectionQuality(peerID peer.ID) *ConnectionQuality {
	cm.mu.Lock()
	history := make([]time.Duration, len(cm.latencyHistory[peerID]))
	copy(history, cm.latencyHistory[peerID])
	cm.mu.Unlock()

	if len(history) == 0 {
		return &ConnectionQuality{
			PeerID:  peerID.String(),
			Score:   50.0,
			Samples: 0,
		}
	}

	var sum float64
	for _, rtt := range history {
		sum += float64(rtt.Microseconds())
	}
	avg := sum / float64(len(history))

	// Calculate jitter (mean absolute deviation)
	var devSum float64
	for _, rtt := range history {
		dev := float64(rtt.Microseconds()) - avg
		if dev < 0 {
			dev = -dev
		}
		devSum += dev
	}
	jitter := devSum / float64(len(history))

	// Estimate packet loss: if we have gaps in history, that suggests loss
	lossPct := 0.0
	if len(history) < cm.maxLatencySamples && len(history) > 0 {
		// Fewer samples than expected suggests some pings were lost
		expected := cm.maxLatencySamples
		lossPct = float64(expected-len(history)) / float64(expected) * 100
	}

	// Score: 100 = perfect, 0 = unusable
	// Based on avg latency and jitter
	latencyScore := 100.0
	if avg > 1000 {
		latencyScore = 10.0
	} else if avg > 500 {
		latencyScore = 25.0
	} else if avg > 200 {
		latencyScore = 50.0
	} else if avg > 100 {
		latencyScore = 70.0
	} else if avg > 50 {
		latencyScore = 85.0
	}

	jitterScore := 100.0
	if jitter > 500 {
		jitterScore = 20.0
	} else if jitter > 200 {
		jitterScore = 40.0
	} else if jitter > 100 {
		jitterScore = 60.0
	} else if jitter > 50 {
		jitterScore = 80.0
	}

	lossScore := 100.0 - lossPct*10
	if lossScore < 0 {
		lossScore = 0
	}

	score := latencyScore*0.5 + jitterScore*0.3 + lossScore*0.2

	return &ConnectionQuality{
		PeerID:        peerID.String(),
		AvgLatencyMs:  avg / 1000.0,
		JitterMs:      jitter / 1000.0,
		PacketLossPct: lossPct,
		Score:         score,
		LastUpdated:   time.Now().Format(time.RFC3339),
		Samples:       len(history),
	}
}

// GetAllConnectionQualities returns quality metrics for all tracked peers.
// GetAllConnectionQualities 返回所有已知对端的连接质量。
func (cm *ConnectionManager) GetAllConnectionQualities() []ConnectionQuality {
	cm.mu.Lock()
	peers := make([]peer.ID, 0, len(cm.latencyHistory))
	for pid := range cm.latencyHistory {
		peers = append(peers, pid)
	}
	cm.mu.Unlock()

	qualities := make([]ConnectionQuality, 0, len(peers))
	for _, pid := range peers {
		q := cm.GetConnectionQuality(pid)
		if q != nil {
			qualities = append(qualities, *q)
		}
	}
	return qualities
}

// Stats returns connection manager statistics.
// Stats 返回连接管理器的统计信息（重连次数、成功/失败连接数等）。
func (cm *ConnectionManager) Stats() map[string]interface{} {
	cm.mu.Lock()
	knownCount := len(cm.knownPeers)
	reconnAttempts := cm.reconnectAttempts
	succConns := cm.successfulConns
	failConns := cm.failedConns
	// Collect peer IDs for quality calculation (outside lock)
	qualityPeers := make([]peer.ID, 0, len(cm.latencyHistory))
	for pid := range cm.latencyHistory {
		qualityPeers = append(qualityPeers, pid)
	}
	cm.mu.Unlock()

	connectedCount := 0
	if cm.svc.IsEnabled() {
		connectedCount = len(cm.svc.GetConnectedPeers())
	}

	// Calculate average quality scores
	var totalScore float64
	var scoredPeers int
	var avgLatency float64
	for _, pid := range qualityPeers {
		q := cm.GetConnectionQuality(pid)
		if q != nil && q.Samples > 0 {
			totalScore += q.Score
			avgLatency += q.AvgLatencyMs
			scoredPeers++
		}
	}
	avgQuality := 50.0
	if scoredPeers > 0 {
		avgQuality = totalScore / float64(scoredPeers)
		avgLatency = avgLatency / float64(scoredPeers)
	}

	return map[string]interface{}{
		"known_peers":        knownCount,
		"connected_peers":    connectedCount,
		"reconnect_attempts": reconnAttempts,
		"successful_conns":   succConns,
		"failed_conns":       failConns,
		"quality_score":      avgQuality,
		"avg_latency_ms":     avgLatency,
	}
}

// DisconnectPeer disconnects from a specific peer.
// DisconnectPeer 断开与指定对端的连接。
func (cm *ConnectionManager) DisconnectPeer(id peer.ID) error {
	if !cm.svc.IsEnabled() {
		return fmt.Errorf("p2p not enabled")
	}
	cm.RemoveKnownPeer(id)
	return cm.svc.Host.Network().ClosePeer(id)
}
