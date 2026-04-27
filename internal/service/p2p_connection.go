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
}

const (
	heartbeatInterval   = 30 * time.Second
	reconnectInterval   = 10 * time.Second
	maxReconnectBackoff = 5 * time.Minute
	connectionTimeout   = 15 * time.Second
)

// NewConnectionManager 创建一个 P2P 连接管理器实例。
func NewConnectionManager(svc *P2PService) *ConnectionManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ConnectionManager{
		svc:          svc,
		knownPeers:   make(map[peer.ID]peer.AddrInfo),
		heartbeatCtx: ctx,
		heartbeatCan: cancel,
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

// Stats returns connection manager statistics.
// Stats 返回连接管理器的统计信息（重连次数、成功/失败连接数等）。
func (cm *ConnectionManager) Stats() map[string]interface{} {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	connectedCount := 0
	if cm.svc.IsEnabled() {
		connectedCount = len(cm.svc.GetConnectedPeers())
	}

	return map[string]interface{}{
		"known_peers":        len(cm.knownPeers),
		"connected_peers":    connectedCount,
		"reconnect_attempts": cm.reconnectAttempts,
		"successful_conns":   cm.successfulConns,
		"failed_conns":       cm.failedConns,
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
