package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
)

// aria2_bridge_test.go — aria2 bridge test suite (Issue #236).
// 发现背景：Issue #236（aria2c 下载插件兼容与集成：支持开关控制与 RPC 桥接）。

func TestAria2Bridge_DisabledByDefault(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: false,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := NewAria2Bridge(cfg)
	assert.False(t, bridge.IsEnabled())

	// Calling RPC when disabled should immediately return error without network call
	_, err := bridge.callRPC(context.Background(), "aria2.getVersion")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

func TestAria2Bridge_ToggleAtRuntime(t *testing.T) {
	cfg := &config.Config{
		Aria2Enable: false,
		Aria2RPCURL: "http://127.0.0.1:6800/jsonrpc",
	}
	bridge := NewAria2Bridge(cfg)
	assert.False(t, bridge.IsEnabled())

	bridge.SetEnabled(true)
	assert.True(t, bridge.IsEnabled())

	bridge.SetEnabled(false)
	assert.False(t, bridge.IsEnabled())
}

func TestAria2Bridge_MockRPCCalls(t *testing.T) {
	// Mock aria2 JSON-RPC server
	var lastMethod string
	var lastParams []any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastMethod = req.Method
		lastParams = req.Params

		switch req.Method {
		case "aria2.getVersion":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"version": "1.36.0"},
			})
		case "aria2.addUri":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  "2089b05e0a3d9370",
			})
		case "aria2.tellStatus":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"gid":             "2089b05e0a3d9370",
					"status":          "active",
					"totalLength":     "10485760",
					"completedLength": "5242880",
					"downloadSpeed":   "1048576",
				},
			})
		case "aria2.pause", "aria2.unpause", "aria2.remove":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  "OK",
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		Aria2Enable:    true,
		Aria2RPCURL:    server.URL,
		Aria2RPCSecret: "my-secret-token",
	}
	bridge := NewAria2Bridge(cfg)

	// 1. GetVersion
	ver, err := bridge.GetVersion(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1.36.0", ver)
	assert.Equal(t, "aria2.getVersion", lastMethod)
	// Token prepended
	require.NotEmpty(t, lastParams)
	assert.Equal(t, "token:my-secret-token", lastParams[0])

	// 2. AddURI
	gid, err := bridge.AddURI(context.Background(), "https://example.com/file.bin", "file.bin")
	require.NoError(t, err)
	assert.Equal(t, "2089b05e0a3d9370", gid)
	assert.Equal(t, "aria2.addUri", lastMethod)

	// 3. TellStatus
	st, err := bridge.TellStatus(context.Background(), gid)
	require.NoError(t, err)
	assert.Equal(t, gid, st.GID)
	assert.Equal(t, "active", st.Status)
	assert.Equal(t, int64(10485760), st.TotalLength)
	assert.Equal(t, int64(5242880), st.CompletedLength)
	assert.Equal(t, int64(1048576), st.DownloadSpeed)

	// 4. Pause / Resume / Cancel
	require.NoError(t, bridge.Pause(context.Background(), gid))
	require.NoError(t, bridge.Resume(context.Background(), gid))
	require.NoError(t, bridge.Cancel(context.Background(), gid))
}
