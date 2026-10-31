package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// 发现背景（Issue #426）：PEERDRIVE_BT_DHT_ENABLE 默认关闭（false），
// 此时 /bt/* 路由仍注册。为避免 footgun 与潜在 panic，测试严格断言在 BT 运行时
// 未注入（btClient == nil，即 PEERDRIVE_BT_ENABLE=0 或 DLL 缺失）时，
// 各端点明确返回 503 Service Unavailable 或 status enabled: false，不发生空指针异常。
func TestBTDHT_Disabled_SafeGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitBTController(nil)

	// 1. Status 端点返回 enabled: false (200 OK)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/bt/status", nil)
	BTDHTStatus(c)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, false, resp["enabled"])

	// 2. Announce 返回 503
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/bt/announce", strings.NewReader(`{"hash":"1122334455667788990011223344556677889900"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	BTAnnounce(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	// 3. FindProviders 返回 503
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/bt/find", strings.NewReader(`{"hash":"1122334455667788990011223344556677889900"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	BTFindProviders(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	// 4. BEP44Put 返回 503
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/bt/bep44/put", strings.NewReader(`{"data":"dGVzdA=="}`))
	c.Request.Header.Set("Content-Type", "application/json")
	BEP44Put(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	// 5. BEP44Get 返回 503
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/bt/bep44/get", strings.NewReader(`{"target":"1122334455667788990011223344556677889900"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	BEP44Get(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	// 6. BEP51Sample 返回 503
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/bt/bep51/sample", nil)
	BEP51Sample(c)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// 发现背景（BT 运行时 DLL 化）：BT 客户端过去是**无条件**创建的，所以 /bt/downloads
// 在「BT 未启用」时也返回 200 + 空列表。现在加载被 PEERDRIVE_BT_ENABLE gate 住，
// 默认配置下 btClient 为 nil —— 这里把「关闭时明确 503」钉住，避免将来有人把
// 客户端创建又挪回无条件路径（那会让每个进程重新开 BT 端口、跑 DHT、拉 tracker）。
func TestBTClient_Disabled_Returns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitBTController(nil)

	cases := []struct {
		name string
		call func(*gin.Context)
	}{
		{"downloads", BTDownloadList},
		{"stats", BTGlobalStats},
		{"torrent upload", BTTorrentUpload},
		{"magnet", BTMagnetResolve},
		{"seed collection", BTSeedCollection},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/bt", nil)
		tc.call(c)
		assert.Equalf(t, http.StatusServiceUnavailable, w.Code, "%s should be 503 when BT is disabled", tc.name)
	}
}
