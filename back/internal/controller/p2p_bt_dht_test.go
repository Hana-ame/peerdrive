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
// 此时 /bt/* 路由仍注册。为避免 footgun 与潜在 panic，测试严格断言在 btSvc == nil 时，
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
