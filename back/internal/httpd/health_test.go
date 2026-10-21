package httpd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the response BYTES, not just the status. The three probes
// are read by load balancers, container orchestrators and human scripts that
// compare literal bodies; a change that only "looks the same" in a browser is
// a production break.
//
// The Content-Type assertion matters too: gin's c.JSON writes
// "application/json; charset=utf-8" and c.String writes
// "text/plain; charset=utf-8". Dropping the charset on extraction is a
// byte-level change that some parsers treat as a different media type.

func TestPingHandlerExactResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	PingHandler(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rec.Result().Body)
	assert.Equal(t, "pong", string(body))
}

// TestLivenessHandlerExactResponse 存活探针必须是「恰好两个键」的固定体。
//
// 发现背景：/health 被容器编排系统反复轮询，多一个 "reason" 键或少一个
// "uptime_sec" 都会让下游解析脚本炸掉；uptime_sec 是时间的，只能验前缀 +
// 键集合，其余部分逐字节比。
func TestLivenessHandlerExactResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	LivenessHandler(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	body, _ := io.ReadAll(rec.Result().Body)
	// uptime_sec is time-dependent; the rest of the body must be exact.
	assert.True(t, strings.HasPrefix(string(body), `{"status":"ok","uptime_sec":`),
		"body = %q", string(body))
	assert.NotContains(t, string(body), `"reason"`, "liveness must not report a failure reason")

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.EqualValues(t, "ok", got["status"])
	assert.Len(t, got, 2, "body must be exactly {status, uptime_sec}")
	assert.IsType(t, float64(0), got["uptime_sec"], "uptime_sec must be a JSON number")
}

// TestReadinessHandlerUnwiredProbe probe 没接上时不能假装就绪。
//
// 发现背景：探针接线是启动期一步（controller.InitHealth），漏接的后果是
// /ready 永远 200，编排系统据此把流量灌进一个没有数据库的实例。
func TestReadinessHandlerUnwiredProbe(t *testing.T) {
	rec := httptest.NewRecorder()
	ReadinessHandler(nil)(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rec.Result().Body)
	assert.Equal(t, `{"reason":"health check not wired","status":"unavailable"}`, string(body))
}

// TestReadinessHandlerFailingProbe 失败原因必须是固定的分类词，不泄露底层错误。
//
// 发现背景：把 "connection refused" 之类的库错误原文回给探针，等于把内部
// 拓扑泄露给任何能打到 /ready 的扫描器；这里断言 body 是固定分类且不含原文。
func TestReadinessHandlerFailingProbe(t *testing.T) {
	rec := httptest.NewRecorder()
	ReadinessHandler(func() error { return errors.New("connection refused") })(
		rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body, _ := io.ReadAll(rec.Result().Body)
	// The DB error text must NOT leak: the response is a fixed classification.
	assert.Equal(t, `{"reason":"database ping failed","status":"unavailable"}`, string(body))
	assert.NotContains(t, string(body), "connection refused")
}

// TestReadinessHandlerHappyPath probe 成功时只回一个键，且不重复探测。
//
// 发现背景：probe 要真连一次数据库，回环调用它两次就是一次无谓的连接开销；
// 也说明这个 handler 是纯函数式的，没有缓存或重试。
func TestReadinessHandlerHappyPath(t *testing.T) {
	calls := 0
	rec := httptest.NewRecorder()
	ReadinessHandler(func() error { calls++; return nil })(
		rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Result().Body)
	assert.Equal(t, `{"status":"ready"}`, string(body))
	assert.Equal(t, 1, calls, "the probe must be called exactly once")
}

// TestReadinessHandlerFailsClosed 就绪探针必须 fail-closed：probe 缺失或报错
// 一律 503，绝不能因为「大部分时候是好的」就返回 200。
func TestReadinessHandlerFailsClosed(t *testing.T) {
	cases := map[string]func() error{
		"nil":    nil,
		"failed": func() error { return errors.New("disk full") },
	}
	for name, probe := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ReadinessHandler(probe)(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		})
	}
}

// TestHealthHandlersIgnoreMethodAndPath 探针不该关心方法和路径——它们由路由层
// 挂到 GET 上，handler 本身要能容忍任何方法（否则 OPTIONS 预检之类会打到 handler
// 上产生意外的 405）。
func TestHealthHandlersIgnoreMethodAndPath(t *testing.T) {
	for _, m := range []string{"GET", "HEAD", "OPTIONS", "POST"} {
		t.Run(m, func(t *testing.T) {
			rec := httptest.NewRecorder()
			PingHandler(rec, httptest.NewRequest(m, "/anything", nil))
			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}
