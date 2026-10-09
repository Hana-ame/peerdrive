package controller

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
	"peerdrive/internal/service"
)

// peer_pull_aria2_test.go — Tests for aria2 controller integration endpoints (Issue #236).
// 发现背景：Issue #236（aria2c 下载插件兼容与集成：支持开关控制与 RPC 桥接）。

func setupAria2TestRouter(bridge *service.Aria2Bridge) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	InitAria2Bridge(bridge)

	p2p := r.Group("/p2p")
	{
		p2p.GET("/aria2/status", GetAria2Status)
		p2p.POST("/aria2/toggle", SetAria2Enabled)
		p2p.POST("/aria2/download", Aria2AddURI)
	}
	return r
}

func TestAria2Controller_StatusDisabled(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: false,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := service.NewAria2Bridge(cfg)
	r := setupAria2TestRouter(bridge)

	req, _ := http.NewRequest(http.MethodGet, "/p2p/aria2/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["enabled"])
	assert.Equal(t, false, resp["connected"])
}

func TestAria2Controller_ToggleRuntime(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: false,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := service.NewAria2Bridge(cfg)
	r := setupAria2TestRouter(bridge)

	// 1. Toggle ON
	bodyOn, _ := json.Marshal(map[string]any{"enabled": true})
	reqOn, _ := http.NewRequest(http.MethodPost, "/p2p/aria2/toggle", bytes.NewReader(bodyOn))
	wOn := httptest.NewRecorder()
	r.ServeHTTP(wOn, reqOn)

	assert.Equal(t, http.StatusOK, wOn.Code)
	var respOn map[string]any
	require.NoError(t, json.Unmarshal(wOn.Body.Bytes(), &respOn))
	assert.Equal(t, true, respOn["enabled"])
	assert.True(t, bridge.IsEnabled())

	// 2. Toggle OFF
	bodyOff, _ := json.Marshal(map[string]any{"enabled": false})
	reqOff, _ := http.NewRequest(http.MethodPost, "/p2p/aria2/toggle", bytes.NewReader(bodyOff))
	wOff := httptest.NewRecorder()
	r.ServeHTTP(wOff, reqOff)

	assert.Equal(t, http.StatusOK, wOff.Code)
	var respOff map[string]any
	require.NoError(t, json.Unmarshal(wOff.Body.Bytes(), &respOff))
	assert.Equal(t, false, respOff["enabled"])
	assert.False(t, bridge.IsEnabled())
}

func TestAria2Controller_AddURIRejectedWhenDisabled(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: false,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := service.NewAria2Bridge(cfg)
	r := setupAria2TestRouter(bridge)

	body, _ := json.Marshal(map[string]any{
		"uri": "https://example.com/bigfile.iso",
	})
	req, _ := http.NewRequest(http.MethodPost, "/p2p/aria2/download", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
