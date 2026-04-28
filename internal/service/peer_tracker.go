// PeerTracker 记录所有 P2P 对端的元数据（连接时间、传输量、延迟、方向），支持并发读写和统计查询。
package service

import (
	"sort"
	"sync"
	"time"

	"peerdrive/internal/model"
)

// PeerTracker tracks detailed metadata about P2P peers seen by this node.
// It is safe for concurrent use.
type PeerTracker struct {
	mu              sync.RWMutex
	peers           map[string]*model.PeerInfo
	connectionStart map[string]time.Time

	serverVersion      string
	transports         []string
	startTime          time.Time
	regServerConnected bool
}

// NewPeerTracker 创建 PeerTracker 实例，初始化默认值。
func NewPeerTracker() *PeerTracker {
	return &PeerTracker{
		peers:           make(map[string]*model.PeerInfo),
		connectionStart: make(map[string]time.Time),
		startTime:       time.Now(),
		serverVersion:   "peerdrive/1.0.0",
		transports:      []string{"tcp"},
	}
}

// SetServerVersion 设置本地服务器版本，暴露在统计信息中。
func (t *PeerTracker) SetServerVersion(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.serverVersion = version
}

// SetTransports 设置支持的传输协议列表，暴露在统计信息中。
func (t *PeerTracker) SetTransports(transports []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.transports = transports
}

// SetRegServerConnected 设置本节点是否已连接注册服务器。
func (t *PeerTracker) SetRegServerConnected(connected bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.regServerConnected = connected
}

// RecordConnection 记录对端连接事件，新对端设置 FirstSeen，已有对端更新 LastSeen，同时记录连接起始时间。
func (t *PeerTracker) RecordConnection(peerID string, addrs []string, userAgent string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	if existing, ok := t.peers[peerID]; ok {
		existing.LastSeen = now
		existing.Addrs = addrs
		existing.ConnectedAt = now
		if userAgent != "" {
			existing.UserAgent = userAgent
		}
	} else {
		t.peers[peerID] = &model.PeerInfo{
			PeerID:      peerID,
			Addrs:       addrs,
			FirstSeen:   now,
			LastSeen:    now,
			ConnectedAt: now,
			UserAgent:   userAgent,
		}
	}
	t.connectionStart[peerID] = now
}

// RecordDisconnect 更新对端的 LastSeen 并根据之前记录的计算连接时长。
func (t *PeerTracker) RecordDisconnect(peerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.LastSeen = time.Now()
		existing.DisconnectReason = "remote close"
		if start, ok := t.connectionStart[peerID]; ok {
			existing.ConnectionDur = time.Since(start).Round(time.Millisecond).String()
			delete(t.connectionStart, peerID)
		}
	}
}

// RecordBytesSent 增加对端的已发送字节计数。
func (t *PeerTracker) RecordBytesSent(peerID string, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.BytesSent += n
	}
}

// RecordBytesRecv 增加对端的已接收字节计数。
func (t *PeerTracker) RecordBytesRecv(peerID string, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.BytesRecv += n
	}
}

// RecordLatency 记录对端最近一次测量的 RTT 延迟。
func (t *PeerTracker) RecordLatency(peerID string, rtt time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.Latency = rtt.Round(time.Millisecond).String()
	}
}

// SetRegInfo 设置对端的注册验证信息，如对端未跟踪则创建最小记录。
func (t *PeerTracker) SetRegInfo(peerID, username string, verified bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.RegVerified = verified
		existing.RegUsername = username
	} else {
		t.peers[peerID] = &model.PeerInfo{
			PeerID:      peerID,
			RegVerified: verified,
			RegUsername: username,
		}
	}
}

// GetPeer 返回指定对端信息的副本，未知对端返回 nil。
func (t *PeerTracker) GetPeer(peerID string) *model.PeerInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if existing, ok := t.peers[peerID]; ok {
		cpy := *existing
		return &cpy
	}
	return nil
}

// GetAllPeers 返回所有已跟踪对端的排序副本（按首次看到时间排序）。
func (t *PeerTracker) GetAllPeers() []*model.PeerInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]*model.PeerInfo, 0, len(t.peers))
	for _, p := range t.peers {
		cpy := *p
		result = append(result, &cpy)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].FirstSeen.Before(result[j].FirstSeen)
	})
	return result
}

// GetStats 返回聚合的全局统计信息（对端数、传输量、运行时间等），适用于 JSON 序列化。
func (t *PeerTracker) GetStats() map[string]interface{} {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var totalSent, totalRecv int64
	for _, p := range t.peers {
		totalSent += p.BytesSent
		totalRecv += p.BytesRecv
	}

	return map[string]interface{}{
		"total_peers_seen":     len(t.peers),
		"total_bytes_sent":     totalSent,
		"total_bytes_recv":     totalRecv,
		"uptime":               time.Since(t.startTime).String(),
		"server_version":       t.serverVersion,
		"supported_transports": t.transports,
		"reg_server_connected": t.regServerConnected,
	}
}

// PeerCount 返回已看到的唯一对端数量。
func (t *PeerTracker) PeerCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// TotalBytesSent 返回所有对端的已发送字节总数。
func (t *PeerTracker) TotalBytesSent() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total int64
	for _, p := range t.peers {
		total += p.BytesSent
	}
	return total
}

// TotalBytesRecv 返回所有对端的已接收字节总数。
func (t *PeerTracker) TotalBytesRecv() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total int64
	for _, p := range t.peers {
		total += p.BytesRecv
	}
	return total
}

// Uptime 返回自追踪器创建以来的运行时间。
func (t *PeerTracker) Uptime() time.Duration {
	return time.Since(t.startTime)
}

// ServerVersion 返回已配置的服务器版本字符串。
func (t *PeerTracker) ServerVersion() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.serverVersion
}

// Transports 返回所支持的传输协议列表的副本。
func (t *PeerTracker) Transports() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]string, len(t.transports))
	copy(result, t.transports)
	return result
}

// RegServerConnected 返回是否已连接注册服务器。
func (t *PeerTracker) RegServerConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.regServerConnected
}

// SetDirection 设置对端的连接方向（"inbound" 或 "outbound"），未跟踪的对端不操作。
func (t *PeerTracker) SetDirection(peerID, direction string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, ok := t.peers[peerID]; ok {
		existing.Direction = direction
	}
}

// ConnectionCounts 按方向返回活跃连接数（仅统计 DisconnectReason 为空的活跃对端）。
func (t *PeerTracker) ConnectionCounts() (inbound, outbound int) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, p := range t.peers {
		if p.DisconnectReason != "" {
			continue
		}
		switch p.Direction {
		case "inbound":
			inbound++
		case "outbound":
			outbound++
		default:
			outbound++
		}
	}
	return
}
