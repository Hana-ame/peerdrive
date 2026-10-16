package main

import (
	"testing"
)

// TestTunUsage tests that tunUsage prints command line options without panicking.
// 发现背景 (Issue #245): 验证 peerdrive tun CLI 子命令用法提示与子命令路由。
func TestTunUsage(t *testing.T) {
	// Simple test to ensure tunUsage executes without panic
	tunUsage()
}

// TestDefaultAPIBase verifies API URL extraction from environment variables.
// 发现背景 (Issue #245): 验证 defaultAPIBase 支持 PEERDRIVE_API_BASE 及端口回退机制。
func TestDefaultAPIBase(t *testing.T) {
	t.Setenv("PEERDRIVE_API_BASE", "http://192.168.1.50:4000/")
	if got := defaultAPIBase(); got != "http://192.168.1.50:4000" {
		t.Errorf("expected trimmed base URL, got %s", got)
	}

	t.Setenv("PEERDRIVE_API_BASE", "")
	t.Setenv("PEERDRIVE_PORT", "9999")
	if got := defaultAPIBase(); got != "http://127.0.0.1:9999" {
		t.Errorf("expected 127.0.0.1:9999, got %s", got)
	}
}
