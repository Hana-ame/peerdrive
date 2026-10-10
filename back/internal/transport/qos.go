package transport

import (
	"sync"
	"sync/atomic"
	"time"
)

// QoSGuard protects the node's outbound uplink from saturation (Issue #269).
// Responsibilities:
// 1. Max concurrent streams limit (protects upstream bandwidth & thread pool).
// 2. Token bucket bandwidth rate limiting (smooth traffic shaping).
type QoSGuard struct {
	maxConcurrentStreams int64
	activeStreams        atomic.Int64
	uploadSpeedLimit     int64 // bytes per second, 0 = unlimited

	mu       sync.Mutex
	tokens   float64
	lastFill time.Time
}

// NewQoSGuard creates a QoS concurrency and bandwidth limiter.
func NewQoSGuard(maxStreams int64, uploadSpeedBytesPerSec int64) *QoSGuard {
	if maxStreams <= 0 {
		maxStreams = 8 // default 8 concurrent streams
	}
	return &QoSGuard{
		maxConcurrentStreams: maxStreams,
		uploadSpeedLimit:     uploadSpeedBytesPerSec,
		tokens:               float64(uploadSpeedBytesPerSec),
		lastFill:             time.Now(),
	}
}

// TryAcquire attempts to acquire an active stream slot. Returns false if quota is exceeded.
func (q *QoSGuard) TryAcquire() bool {
	if q == nil {
		return true
	}
	for {
		cur := q.activeStreams.Load()
		if cur >= q.maxConcurrentStreams {
			return false
		}
		if q.activeStreams.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Release releases an acquired stream slot.
func (q *QoSGuard) Release() {
	if q == nil {
		return
	}
	for {
		cur := q.activeStreams.Load()
		if cur <= 0 {
			break
		}
		if q.activeStreams.CompareAndSwap(cur, cur-1) {
			break
		}
	}
}

// ActiveStreams returns the number of currently active streams.
func (q *QoSGuard) ActiveStreams() int64 {
	if q == nil {
		return 0
	}
	return q.activeStreams.Load()
}

// Throttle enforces bandwidth token bucket rate limiting before chunk delivery.
func (q *QoSGuard) Throttle(bytes int64) {
	if q == nil || q.uploadSpeedLimit <= 0 || bytes <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(q.lastFill).Seconds()
	q.lastFill = now

	// Refill tokens
	q.tokens += elapsed * float64(q.uploadSpeedLimit)
	burstLimit := float64(q.uploadSpeedLimit) * 2
	if q.tokens > burstLimit {
		q.tokens = burstLimit
	}

	q.tokens -= float64(bytes)
	if q.tokens < 0 {
		deficit := -q.tokens
		sleepSec := deficit / float64(q.uploadSpeedLimit)
		if sleepSec > 0 {
			time.Sleep(time.Duration(sleepSec * float64(time.Second)))
		}
		q.tokens = 0
	}
}
