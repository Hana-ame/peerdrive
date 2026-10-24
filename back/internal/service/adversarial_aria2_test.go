package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
)

// adversarial_aria2_test.go — Adversarial and security test suite for aria2 download integration.
// 发现背景：Issue #257（为遥控投屏、流切片广播、端口转发与落盘链路增加攻击对抗测试套件）。

// TestAdversarial_Aria2_PathTraversalFilename tests that attempts to escape the download
// directory via outFilename path traversal are strictly prevented.
// 发现背景：Issue #257（攻击对抗：aria2 下载文件名目录穿越载荷防御）。
func TestAdversarial_Aria2_PathTraversalFilename(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: true,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
		DownloadDir: "/data/downloads",
	}
	bridge := NewAria2Bridge(cfg)

	traversalFilenames := []string{
		"../../etc/cron.d/root",
		"../../../etc/shadow",
		"..\\..\\windows\\system32\\calc.exe",
		"/tmp/evil_payload",
		"subfolder/file.bin",
		"sub\\file.bin",
		"../relative_escape",
	}

	for _, badFilename := range traversalFilenames {
		t.Run("traversal filename: "+badFilename, func(t *testing.T) {
			_, err := bridge.AddURI(context.Background(), "https://example.com/file.iso", badFilename)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "path traversal characters disallowed")
		})
	}

	// Clean single-level filenames should not be rejected by traversal check
	// (they will fail only on actual network/RPC connect if daemon is offline)
	cleanFilenames := []string{
		"file.iso",
		"ubuntu-22.04.iso",
		"archive_2026.tar.gz",
	}
	for _, cleanName := range cleanFilenames {
		_, err := bridge.AddURI(context.Background(), "https://example.com/file.iso", cleanName)
		// Should NOT fail on path traversal check
		if err != nil {
			assert.NotContains(t, err.Error(), "path traversal characters disallowed")
		}
	}
}

// TestAdversarial_Aria2_DisallowedURISchemes tests that attempts to use non-download protocols
// (file://, data:, gopher://, dict://, etc.) are strictly blocked.
// 发现背景：Issue #257（攻击对抗：aria2 本地文件读取与非安全协议 SSRF 防御）。
func TestAdversarial_Aria2_DisallowedURISchemes(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: true,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := NewAria2Bridge(cfg)

	disallowedURIs := []string{
		"file:///etc/passwd",
		"FILE:///C:/boot.ini",
		"data:text/plain;base64,SGVsbG8sIFdvcmxkIQ==",
		"gopher://127.0.0.1:6379/_flushall",
		"dict://127.0.0.1:11211/stat",
		"ldap://127.0.0.1:389/o=anonymous",
		"javascript:alert(1)",
	}

	for _, badURI := range disallowedURIs {
		t.Run("disallowed scheme: "+badURI, func(t *testing.T) {
			_, err := bridge.AddURI(context.Background(), badURI, "safe_file.bin")
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), "unsupported or disallowed download URI scheme"))
		})
	}
}
