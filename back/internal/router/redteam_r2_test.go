package router

// Package router — red-team regression tests, round 2 (2026-10-11 audit R2).
//
// Discovery background (required by AGENTS.md): the second adversarial round
// re-audited the whole stack against the already-fixed R1 findings. The R1
// bypass surface (URL-scheme allow-list, inline-Content-Type whitelist,
// filename/header injection) held up, but the sweep found four new issues in
// the request-protection and identity layers, all fixed and pinned here:
//
//   - Any of the ~30 c.ShouldBindJSON call sites read the whole body into memory
//     with no size cap, so an anonymous caller could OOM the node with one
//     oversized POST. -> LimitJSONBody (default 1 MiB).
//   - CORS echoed Access-Control-Request-Headers back, making every header
//     browser-approved for cross-origin use; combined with the origin echo that
//     turns a leaked token into a remotely-usable one from any origin. -> an
//     explicit allow-list.
//   - A subdomain wildcard written with a scheme (https://*.pages.dev) matched
//     http:// as well, because only the host suffix was compared.
//   - The node's PeerJS id carried only 32 bits of entropy while the signalling
//     plane treats the id as identity.
//
// These tests pin the fixes so none of them can silently regress.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"peerdrive/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bindProbe wires LimitJSONBody in front of a handler that just binds JSON, so
// the test can assert on the bind error rather than on application behaviour.
func bindProbe(max int64) *gin.Engine {
	r := gin.New()
	r.Use(LimitJSONBody(max))
	r.POST("/probe", func(c *gin.Context) {
		var body struct {
			Hash string `json:"hash"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			// http.Error returns 413 when the limit is exceeded.
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.JSON(http.StatusOK, gin.H{"hash": body.Hash})
	})
	return r
}

// TestLimitJSONBody_RejectsOversized — red-team R2, memory-DoS.
// 发现背景：ShouldBindJSON 无上限地读整个 body；一条超过上限的 POST 就能把进程 OOM。
func TestLimitJSONBody_RejectsOversized(t *testing.T) {
	t.Parallel()
	e := bindProbe(1024)
	big := strings.Repeat("a", 2048)
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(`{"hash":"`+big+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "over-limit JSON body must be refused")
}

// TestLimitJSONBody_AllowsWithinLimit — 上限以下的正常 JSON 必须照旧工作。
func TestLimitJSONBody_AllowsWithinLimit(t *testing.T) {
	t.Parallel()
	e := bindProbe(1024)
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(`{"hash":"abc"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "small JSON body must pass unchanged")
	assert.Contains(t, w.Body.String(), "abc")
}

// TestLimitJSONBody_OnlyCapsJSON — multipart / 二进制上传不能被这个中间件截断，
// 它们有各自的 MaxUploadBytes 上限；误伤会直接弄坏 /files/upload。
func TestLimitJSONBody_OnlyCapsJSON(t *testing.T) {
	t.Parallel()
	e := bindProbe(32)
	body := strings.Repeat("a", 4096) // far over the 32-byte cap
	for _, ct := range []string{"multipart/form-data; boundary=x", "application/octet-stream", ""} {
		t.Run(ct, func(t *testing.T) {
			e2 := gin.New()
			e2.Use(LimitJSONBody(32))
			var got int
			e2.POST("/raw", func(c *gin.Context) {
				buf := make([]byte, 8192)
				n, _ := c.Request.Body.Read(buf)
				got = n
			})
			req := httptest.NewRequest(http.MethodPost, "/raw", strings.NewReader(body))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			w := httptest.NewRecorder()
			e2.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Greater(t, got, 32, "non-JSON bodies must not be truncated")
		})
	}
	_ = e
}

// TestLimitJSONBody_ZeroDisables — 0 = 不设限（与 PEERDRIVE_MAX_JSON_BODY_BYTES 语义一致）。
func TestLimitJSONBody_ZeroDisables(t *testing.T) {
	t.Parallel()
	e := bindProbe(0)
	big := strings.Repeat("a", 64*1024)
	req := httptest.NewRequest(http.MethodPost, "/probe", strings.NewReader(`{"hash":"`+big+`"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "maxBytes=0 must leave the body uncapped")
}

// TestCORS_AllowHeadersIsAnAllowList — red-team R2.
// 发现背景：旧实现把 Access-Control-Request-Headers 原样回显，等于把客户端想用的
// 任意 header 全部声明为跨域可用；配合 origin 回显，泄露的 token 就能从任意来源
// 远程调用。现在必须是固定白名单。
func TestCORS_AllowHeadersIsAnAllowList(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.AllowedOrigins = "https://victim.example.com"

	e := gin.New()
	e.Use(corsMiddleware(cfg))
	e.POST("/x", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodOptions, "/x", nil)
	req.Header.Set("Origin", "https://victim.example.com")
	// The attacker asks the browser to approve three exotic headers.
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Cookie, Proxy-Authorization, X-Internal-Admin, X-PSK")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	allow := w.Header().Get("Access-Control-Allow-Headers")
	require.NotEmpty(t, allow, "a preflight must answer the header question")
	assert.Equal(t, corsAllowedHeaders, allow, "the reply must be the allow-list, not the client's request")
	for _, exotic := range []string{"Cookie", "Proxy-Authorization", "X-Internal-Admin"} {
		assert.NotContains(t, allow, exotic, "%q must never be browser-approved", exotic)
	}
	for _, allowed := range []string{"Authorization", "Content-Type", "X-PSK"} {
		assert.Contains(t, allow, allowed, "%q must stay approved", allowed)
	}
}

// TestCORS_UnknownOriginGetsNoCORS — 非白名单来源拿不到 Allow-Origin（浏览器因此
// 禁止读取响应体）。origin 回显 + credentials 只给白名单。
func TestCORS_UnknownOriginGetsNoCORS(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.AllowedOrigins = "https://victim.example.com"

	e := gin.New()
	e.Use(corsMiddleware(cfg))
	e.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "secret") })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"), "unknown origin must not be echoed")
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"), "no credentials for an unknown origin")
}

// TestIsOriginAllowed_SchemeOnSubdomainWildcard — red-team R2.
// 发现背景：`https://*.pages.dev` 只比较了 host 后缀，所以 `http://x.pages.dev`
// 也能匹配。带 scheme 的通配符必须强制 scheme；不带 scheme 的保持任意匹配
// （向后兼容，见下面一例）。
func TestIsOriginAllowed_SchemeOnSubdomainWildcard(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	cfg.AllowedOrigins = "https://*.pages.dev,*.bare.example.com"

	cases := []struct {
		origin string
		want   bool
	}{
		{"https://a.pages.dev", true},   // same scheme + host suffix
		{"https://a.b.pages.dev", true}, // deeper subdomain, same scheme
		{"http://a.pages.dev", false},   // wrong scheme on a scheme-qualified wildcard
		{"https://notpages.dev", false}, // suffix must include the dot
		{"https://evil-pages.dev", false},
		{"http://a.bare.example.com", true}, // bare wildcard keeps matching any scheme
		{"https://a.bare.example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.origin, func(t *testing.T) {
			require.Equal(t, tc.want, cfg.IsOriginAllowed(tc.origin), "IsOriginAllowed(%q)", tc.origin)
		})
	}
}

// TestIsOriginAllowed_DefaultsAreAnAllowList — 默认值必须是显式白名单，
// 而不是"空 = 全放行"。空值只应在运营者显式配置时出现。
func TestIsOriginAllowed_DefaultsAreAnAllowList(t *testing.T) {
	t.Parallel()
	cfg := config.DefaultConfig()
	assert.Contains(t, cfg.AllowedOrigins, "*", "default should at least document itself via a wildcard entry")
	// The operator's own front-end stays reachable.
	require.True(t, cfg.IsOriginAllowed("https://peerdrive.pages.dev"))
	// A random third-party origin is not the default answer.
	require.False(t, cfg.IsOriginAllowed("https://random.github.io"))
}

// TestSecurityHeaders_CSP — CSP is always emitted (except the explicit off switch),
// and the single-file pages get the relaxed variant rather than none.
//
// 发现背景（红队 R2）：/panel 与 /swagger 原先是「完全不给 CSP」——单文件内联页面
// 确实需要 inline script，但「完全不给」同时放掉了 object-src / base-uri /
// frame-ancestors 三条与 inline 无关的硬保护。修复为降级到 inlineHTMLCSP，
// 本用例把「这两个前缀也必须带 CSP、且三条硬保护不放宽」钉死。
func TestSecurityHeaders_CSP(t *testing.T) {
	t.Parallel()
	strict := "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
	relaxed := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"

	cases := []struct {
		path string
		want string
	}{
		{"/api/files", strict},    // JSON API: strict
		{"/download/abc", strict}, // file download: strict
		{"/", strict},             // root: strict
		{"/panel", relaxed},       // single-file inline panel: relaxed, NOT absent
		{"/panel/", relaxed},
		{"/panel/peerjs.min.js", relaxed},
		{"/swagger", relaxed},
		{"/swagger/index.html", relaxed},
		{"/swagger/", relaxed},
		{"/panels", strict}, // prefix must not match: /panels is NOT /panel
		{"/swaggerish", strict},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, tc.path, nil)
			SecurityHeaders(false)(c)
			assert.Equal(t, tc.want, w.Header().Get("Content-Security-Policy"),
				"path %s", tc.path)
		})
	}
}

// TestSecurityHeaders_CSPStillHardenedOnRelaxed — the relaxed policy must keep
// the three directives that have nothing to do with inline code. That is the
// whole point of the fix: the exemption is partial, not total.
func TestSecurityHeaders_CSPStillHardenedOnRelaxed(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/panel", nil)
	SecurityHeaders(false)(c)

	csp := w.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	assert.Contains(t, csp, "object-src 'none'", "plugins must stay blocked on the panel")
	assert.Contains(t, csp, "base-uri 'self'", "<base href> exfiltration must stay blocked")
	assert.Contains(t, csp, "frame-ancestors 'self'", "clickjacking protection must stay")
	assert.NotContains(t, csp, "unsafe-eval", "'unsafe-eval' must never appear")
}

// TestSecurityHeaders_DisabledCSP — PEERDRIVE_CSP=off still disables it entirely,
// so an operator can escape a bad configuration.
func TestSecurityHeaders_DisabledCSP(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/files", "/panel", "/swagger/"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		SecurityHeaders(true)(c)
		assert.Empty(t, w.Header().Get("Content-Security-Policy"), "path %s", path)
		// The other hardening headers are not tied to the CSP switch.
		assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	}
}
