package transport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQoSGuard_ConcurrencyLimit verifies that max concurrent streams are strictly enforced.
// 发现背景：Issue #269（节点上行流控与多访客点播并发保护，防并发拉取打爆上行与连接池）。
func TestQoSGuard_ConcurrencyLimit(t *testing.T) {
	guard := NewQoSGuard(2, 0)
	require.Equal(t, int64(0), guard.ActiveStreams())

	// Slot 1: acquire succeeds
	assert.True(t, guard.TryAcquire())
	assert.Equal(t, int64(1), guard.ActiveStreams())

	// Slot 2: acquire succeeds
	assert.True(t, guard.TryAcquire())
	assert.Equal(t, int64(2), guard.ActiveStreams())

	// Slot 3: quota exhausted -> rejected
	assert.False(t, guard.TryAcquire())
	assert.Equal(t, int64(2), guard.ActiveStreams())

	// Release 1 slot
	guard.Release()
	assert.Equal(t, int64(1), guard.ActiveStreams())

	// Slot 3 can now succeed
	assert.True(t, guard.TryAcquire())
	assert.Equal(t, int64(2), guard.ActiveStreams())

	// Release all
	guard.Release()
	guard.Release()
	assert.Equal(t, int64(0), guard.ActiveStreams())
}

// TestQoSGuard_Throttle verifies that token bucket rate limiting runs without error.
// 发现背景：Issue #269。
func TestQoSGuard_Throttle(t *testing.T) {
	guard := NewQoSGuard(4, 1024*1024) // 1MB/s
	// Small chunk should consume tokens immediately without stalling
	guard.Throttle(1024)
}
