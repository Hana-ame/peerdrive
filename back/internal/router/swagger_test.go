package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
)

// 发现背景（Issue #426）：/swagger/index.html 正常返回 200，但 /swagger/doc.json 返回 500 空 body，
// 导致 Swagger UI 前端渲染白板。根因为 back/docs/docs.go 缺失 swag.Register 与模板注册。
// 本测试锁定 /swagger/doc.json 必须返回 200 OK 与合法的 Swagger 2.0 JSON 结构。
func TestSwaggerDocJSON(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DisableSwagger = false
	rt := New(cfg, Deps{})
	engine := rt.Engine()

	req := httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "GET /swagger/doc.json must return 200 OK")
	var doc map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &doc)
	require.NoError(t, err, "body must be valid JSON")
	assert.Equal(t, "2.0", doc["swagger"], "swagger version must be 2.0")
	info, ok := doc["info"].(map[string]interface{})
	require.True(t, ok, "info section must exist")
	assert.Equal(t, "Peerdrive API", info["title"])
}

// 发现背景（Issue #426）：当 PEERDRIVE_SWAGGER=off (cfg.DisableSwagger=true) 时，
// /swagger/* 路由不注册，必须返回 404。
func TestSwaggerDocJSON_Disabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DisableSwagger = true
	rt := New(cfg, Deps{})
	engine := rt.Engine()

	req := httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code, "Swagger disabled must return 404")
}
