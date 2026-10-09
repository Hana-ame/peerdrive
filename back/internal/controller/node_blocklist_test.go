package controller

// node_blocklist_test.go — Tests for peer blocklist endpoints (Issue #89).

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// TestPeerBlocklistEndpoints tests GET, POST, DELETE /peerjs/blocklist.
// 发现背景：Issue #89（节点连接准入控制：黑名单管理端点）。
func TestPeerBlocklistEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	cfg.DownloadDir = t.TempDir()
	storageDir := t.TempDir()

	svc := transport.NewPeerJSService(cfg, storageDir)
	defer svc.Close()

	InitPeerShareController(svc)

	r := gin.New()
	r.GET("/peerjs/blocklist", GetPeerBlocklist)
	r.POST("/peerjs/blocklist", PostPeerBlocklist)
	r.DELETE("/peerjs/blocklist/:peer", DeletePeerBlocklist)

	// 1. GET initial blocklist -> empty
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/peerjs/blocklist", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []transport.BlockedPeerEntry `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Empty(t, resp.Data)

	// 2. POST to add a block
	body, _ := json.Marshal(map[string]string{
		"peer_id": "malicious-peer-1",
		"reason":  "ddos attempt",
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/peerjs/blocklist", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 3. GET to verify it is listed
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/peerjs/blocklist", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	resp = struct {
		Data []transport.BlockedPeerEntry `json:"data"`
	}{}
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "malicious-peer-1", resp.Data[0].PeerID)
	assert.Equal(t, "ddos attempt", resp.Data[0].Reason)

	// 4. DELETE to unblock
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/peerjs/blocklist/malicious-peer-1", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 5. GET to verify empty again
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/peerjs/blocklist", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	resp = struct {
		Data []transport.BlockedPeerEntry `json:"data"`
	}{}
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Empty(t, resp.Data)
}
