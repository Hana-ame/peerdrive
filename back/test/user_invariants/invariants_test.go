package user_invariants

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"peerdrive/internal/config"
	"peerdrive/internal/pathutil"
)

// TestUser_SecurityInvariants verifies user-specified security invariants on the Go backend.
//
// 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS:
// This test is an immutable user invariant.
// AI agents are STRICTLY FORBIDDEN from modifying, commenting out, or loosening this test.
// If this test fails, you must fix the backend implementation to satisfy it.
func TestUser_SecurityInvariants(t *testing.T) {
	// Invariant 1: Default configuration must keep ShareEnable = false (security-by-default)
	cfg := config.DefaultConfig()
	require.NotNil(t, cfg, "default config must not be nil")
	assert.False(t, cfg.ShareEnable, "ShareEnable must default to false for security")

	// Invariant 2: Path containment must reject directory traversal attempts
	isInside := pathutil.Within("/tmp/safe_root", "/tmp/safe_root/../../etc/passwd")
	assert.False(t, isInside, "pathutil must reject directory traversal outside root")

	// Invariant 3: Clean paths within root must be accepted
	isInsideValid := pathutil.Within("/tmp/safe_root", "/tmp/safe_root/sub/file.txt")
	assert.True(t, isInsideValid, "pathutil must accept valid subpaths")
}
