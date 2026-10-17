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
	"peerdrive/internal/transport"
)

// stream_test.go — Stream controller HTTP endpoints test suite (Issue #244).

func setupStreamTest(t *testing.T) (*gin.Engine, *transport.PeerJSService) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	svc := transport.NewPeerJSService(cfg, t.TempDir())
	InitStreamController(svc)

	r := gin.New()
	grp := r.Group("/stream")
	{
		grp.GET("/list", ListStreams)
		grp.GET("/:id/manifest", GetStreamManifest)
		grp.POST("/create", CreateStream)
		grp.POST("/:id/chunk", PushStreamChunk)
		grp.POST("/:id/close", CloseStream)
	}
	return r, svc
}

// TestStreamController_Endpoints verifies listing, creating, pushing chunks, and querying manifests.
// 发现背景：Issue #244（流媒体分片广播与清单 HTTP/admin 面端点）。
func TestStreamController_Endpoints(t *testing.T) {
	r, _ := setupStreamTest(t)

	// 1. Initial list is empty
	req := httptest.NewRequest(http.MethodGet, "/stream/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var list []*transport.StreamManifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Empty(t, list)

	// 2. Create a stream
	createBody, _ := json.Marshal(map[string]any{
		"streamId": "ch-alpha",
		"title":    "Alpha Video Live",
		"mimeType": "video/mp4",
	})
	req = httptest.NewRequest(http.MethodPost, "/stream/create", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var manifest transport.StreamManifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &manifest))
	assert.Equal(t, "ch-alpha", manifest.StreamID)
	assert.Equal(t, "Alpha Video Live", manifest.Title)

	// 3. Push a chunk
	chunkBody, _ := json.Marshal(map[string]any{
		"hash":     "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff",
		"size":     65536,
		"duration": 5.0,
		"title":    "Segment #1",
	})
	req = httptest.NewRequest(http.MethodPost, "/stream/ch-alpha/chunk", bytes.NewReader(chunkBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var chunk transport.StreamChunk
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &chunk))
	assert.Equal(t, int64(1), chunk.Seq)
	assert.Equal(t, 5.0, chunk.Duration)

	// 4. Query manifest
	req = httptest.NewRequest(http.MethodGet, "/stream/ch-alpha/manifest", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var mf transport.StreamManifest
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &mf))
	assert.Equal(t, int64(1), mf.CurrentSeq)
	require.Len(t, mf.Chunks, 1)
	assert.Equal(t, "Segment #1", mf.Chunks[0].Title)

	// 5. Close stream
	req = httptest.NewRequest(http.MethodPost, "/stream/ch-alpha/close", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
