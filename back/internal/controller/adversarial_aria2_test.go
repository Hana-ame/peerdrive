package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/service"
)

// adversarial_aria2_test.go — Adversarial tests for aria2 HTTP API endpoints.
// 发现背景：Issue #257（为遥控投屏、流切片广播、端口转发与落盘链路增加攻击对抗测试套件）。

// TestAdversarial_Aria2Controller_DisallowedURIScheme tests that sending unsafe URI schemes
// to POST /p2p/aria2/download returns HTTP 400 Bad Request.
// 发现背景：Issue #257（攻击对抗：POST /p2p/aria2/download 非法 URI Scheme 拒绝）。
func TestAdversarial_Aria2Controller_DisallowedURIScheme(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: true,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := service.NewAria2Bridge(cfg)
	r := setupAria2TestRouter(bridge)

	evilURIs := []string{
		"file:///etc/shadow",
		"data:text/html,evil",
		"gopher://127.0.0.1:6379",
	}

	for _, uri := range evilURIs {
		body, _ := json.Marshal(map[string]any{
			"uri":      uri,
			"filename": "output.bin",
		})
		req, _ := http.NewRequest(http.MethodPost, "/p2p/aria2/download", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "should return 400 Bad Request for %s", uri)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "unsupported or disallowed download URI scheme")
	}
}

// TestAdversarial_Aria2Controller_PathTraversalFilename tests that sending path traversal
// filenames to POST /p2p/aria2/download returns HTTP 400 Bad Request.
// 发现背景：Issue #257（攻击对抗：POST /p2p/aria2/download 目录穿越文件名拒绝）。
func TestAdversarial_Aria2Controller_PathTraversalFilename(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: true,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := service.NewAria2Bridge(cfg)
	r := setupAria2TestRouter(bridge)

	evilFilenames := []string{
		"../../etc/passwd",
		"..\\..\\windows\\system32",
		"/var/log/evil",
		"sub/file.bin",
	}

	for _, filename := range evilFilenames {
		body, _ := json.Marshal(map[string]any{
			"uri":      "https://example.com/legit_archive.zip",
			"filename": filename,
		})
		req, _ := http.NewRequest(http.MethodPost, "/p2p/aria2/download", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code, "should return 400 Bad Request for %s", filename)
		var resp map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "path traversal characters disallowed")
	}
}
