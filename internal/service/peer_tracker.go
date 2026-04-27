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

// NewPeerTracker creates a new PeerTracker with sensible defaults.
func NewPeerTracker() *PeerTracker {
	return &PeerTracker{
		peers:           make(map[string]*model.PeerInfo),
		connectionStart: make(map[string]time.Time),
		startTime:       time.Now(),
		serverVersion:   "peerdrive/1.0.0",
		transports:      []string{"tcp"},
	}
}

// SetServerVersion sets the local server version string exposed in stats.
func (t *PeerTracker) SetServerVersion(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.serverVersion = version
}

// SetTransports sets the list of supported transports exposed in stats.
func (t *PeerTracker) SetTransports(transports []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.transports = transports
}

// SetRegServerConnected sets whether this node is connected to a
// registration server.
func (t *PeerTracker) SetRegServerConnected(connected bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.regServerConnected = connected
}

// RecordConnection records that a peer was seen.  If the peer is new its
// FirstSeen field is populated; otherwise only LastSeen is updated.  The
// connection start timestamp is stored so that RecordDisconnect can compute
// connection duration.
func (t *PeerTracker) RecordConnection(peerID string, addrs []string, userAgent string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	if existing, ok := t.peers[peerID]; ok {
		existing.LastSeen = now
		existing.Addrs = addrs
		if userAgent != "" {
			existing.UserAgent = userAgent
		}
	} else {
		t.peers[peerID] = &model.PeerInfo{
			PeerID:    peerID,
			Addrs:     addrs,
			FirstSeen: now,
			LastSeen:  now,
			UserAgent: userAgent,
		}
	}
	t.connectionStart[peerID] = now
}

// RecordDisconnect updates the peer's LastSeen and computes the connection
// duration from the previously recorded connection start time.
func (t *PeerTracker) RecordDisconnect(peerID string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.LastSeen = time.Now()
		if start, ok := t.connectionStart[peerID]; ok {
			existing.ConnectionDur = time.Since(start).Round(time.Millisecond).String()
			delete(t.connectionStart, peerID)
		}
	}
}

// RecordBytesSent adds n bytes to the peer's BytesSent counter.
func (t *PeerTracker) RecordBytesSent(peerID string, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.BytesSent += n
	}
}

// RecordBytesRecv adds n bytes to the peer's BytesRecv counter.
func (t *PeerTracker) RecordBytesRecv(peerID string, n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.BytesRecv += n
	}
}

// RecordLatency records the last measured RTT for a peer.
func (t *PeerTracker) RecordLatency(peerID string, rtt time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if existing, ok := t.peers[peerID]; ok {
		existing.Latency = rtt.Round(time.Millisecond).String()
	}
}

// SetRegInfo sets registration-verification info on a peer.  If the peer is
// not yet tracked, a minimal entry is created.
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

// GetPeer returns a copy of the peer info, or nil if the peer is unknown.
func (t *PeerTracker) GetPeer(peerID string) *model.PeerInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if existing, ok := t.peers[peerID]; ok {
		cpy := *existing
		return &cpy
	}
	return nil
}

// GetAllPeers returns a sorted copy of all tracked peers.
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

// GetStats returns aggregated global statistics as a map suitable for JSON
// serialization.
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

// PeerCount returns the number of unique peers seen.
func (t *PeerTracker) PeerCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.peers)
}

// TotalBytesSent returns the sum of BytesSent across all peers.
func (t *PeerTracker) TotalBytesSent() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total int64
	for _, p := range t.peers {
		total += p.BytesSent
	}
	return total
}

// TotalBytesRecv returns the sum of BytesRecv across all peers.
func (t *PeerTracker) TotalBytesRecv() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total int64
	for _, p := range t.peers {
		total += p.BytesRecv
	}
	return total
}

// Uptime returns the duration since the tracker was created.
func (t *PeerTracker) Uptime() time.Duration {
	return time.Since(t.startTime)
}

// ServerVersion returns the configured server version.
func (t *PeerTracker) ServerVersion() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.serverVersion
}

// Transports returns a copy of the supported transports list.
func (t *PeerTracker) Transports() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]string, len(t.transports))
	copy(result, t.transports)
	return result
}

// RegServerConnected returns whether a registration server is connected.
func (t *PeerTracker) RegServerConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.regServerConnected
}
