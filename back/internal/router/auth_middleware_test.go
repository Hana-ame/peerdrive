package router

// ── Rewritten for the 2026-10 dependency-injection refactor ──
//
// These tests used to call package-level `SetAdminToken`/`SetRegServer` and then
// undo it via a `resetAuthState()` helper before each case — which is exactly the
// arrangement that makes tests order-dependent and impossible to run in parallel.
// Now each case builds its own Authenticator from the values it wants to assert
// on, so the cases below are marked `t.Parallel()` and share nothing.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// newAuthTestEngine assembles a minimal engine on top of the given Authenticator:
// /protected requires a token, /open is anonymous. Tests only exercise the
// middleware, so no real routes are needed.
//
// Why the Authenticator is a parameter rather than something the helper builds
// itself: several cases must pin two *different* auth configs at once (that is the
// whole point of the refactor), which a no-arg helper could not express.
func newAuthTestEngine(a *Authenticator) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(a.AuthOptional())
	authReq := a.AuthRequired()
	r.GET("/protected", authReq, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/open", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

// authDisabledAuth is the "local single-machine mode" configuration: no
// registration server, no admin token.
func authDisabledAuth() *Authenticator { return NewAuthenticator("", "") }

// localTokenAuth is the "login service off, AdminToken on" configuration.
func localTokenAuth(token string) *Authenticator { return NewAuthenticator("", token) }

// regServerAuth is the remote-registration-server configuration.
func regServerAuth(url string) *Authenticator { return NewAuthenticator(url, "") }

func authRequest(r *gin.Engine, path, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestAuthRequired_BothEmpty_AuthDisabledPassesThrough: 无 reg server、无 AdminToken
// 时认证关闭，AuthRequired 直接放行（本地单机模式）。
// 发现背景：这是 refactor 既有语义（authDisabled()），不是新行为——
// 新行为是"设了 AdminToken 后同一路由会 401"，见下一个测试。
func TestAuthRequired_BothEmpty_AuthDisabledPassesThrough(t *testing.T) {
	t.Parallel()
	r := newAuthTestEngine(authDisabledAuth())

	w := authRequest(r, "/protected", "")
	assert.Equal(t, http.StatusOK, w.Code, "no auth backend configured -> pass through")
}

// TestAuthRequired_AdminToken_WrongToken401_RightToken200: 只设 AdminToken（登录服务关）时，
// /protected 必须对错令牌 401、对正确令牌 200。这是本特性（C-14 follow-up）的核心断言。
func TestAuthRequired_AdminToken_WrongToken401_RightToken200(t *testing.T) {
	t.Parallel()
	r := newAuthTestEngine(localTokenAuth("secret-token"))

	wrong := authRequest(r, "/protected", "Bearer wrong")
	assert.Equal(t, http.StatusUnauthorized, wrong.Code, "wrong admin token must 401")

	right := authRequest(r, "/protected", "Bearer secret-token")
	assert.Equal(t, http.StatusOK, right.Code, "correct admin token must pass")
}

// TestAuthRequired_AdminToken_MissingHeader401: 设了 AdminToken 但没带 Authorization，
// /protected 必须 401——避免"设了又等于没设"的错觉。
func TestAuthRequired_AdminToken_MissingHeader401(t *testing.T) {
	t.Parallel()
	r := newAuthTestEngine(localTokenAuth("secret-token"))

	w := authRequest(r, "/protected", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthRequired_AdminToken_BadFormat401: 非 Bearer 格式的 Authorization 头必须 401。
func TestAuthRequired_AdminToken_BadFormat401(t *testing.T) {
	t.Parallel()
	r := newAuthTestEngine(localTokenAuth("secret-token"))

	w := authRequest(r, "/protected", "Basic abc")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestAuthOptional_AdminToken_SetsAuthenticated: AuthOptional 在本地令牌模式下，
// 带正确令牌 → authenticated=true；匿名 → authenticated=false 但请求放行。
// 前端面板/公共页依赖"匿名也能进、authenticated=false"的合约。
func TestAuthOptional_AdminToken_SetsAuthenticated(t *testing.T) {
	t.Parallel()
	a := localTokenAuth("secret-token")

	r := gin.New()
	r.Use(a.AuthOptional())
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"authenticated": c.GetBool("authenticated")})
	})

	anon := httptest.NewRecorder()
	r.ServeHTTP(anon, httptest.NewRequest(http.MethodGet, "/probe", nil))
	assert.Equal(t, `{"authenticated":false}`, anon.Body.String())

	authed := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	r.ServeHTTP(authed, req)
	assert.Equal(t, `{"authenticated":true}`, authed.Body.String())
}

// TestAuthRequired_RegServerSet_NoLocalTokenStillRequiresRemoteValidation:
// reg server 已设时，AdminToken 路径不应介入；令牌必须经远端 whoami。
// 远端 unreachable → 校验失败 → 带令牌也 401（不静默放行）。
func TestAuthRequired_RegServerSet_NoLocalTokenStillRequiresRemoteValidation(t *testing.T) {
	t.Parallel()
	a := NewAuthenticator("http://127.0.0.1:1", "ignored-local-token") // 故意不可达：whoami 必然失败
	r := newAuthTestEngine(a)

	// 本地令牌在远端模式下不应生效——远端模式优先级更高。
	w := authRequest(r, "/protected", "Bearer ignored-local-token")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "reg server mode ignores local admin token")
}

// TestAuthRequired_AdminToken_AnonymousOpenRoute: /open 未被 AuthRequired 保护，
// 设了 AdminToken 也不影响匿名访问公共路由。
func TestAuthRequired_AdminToken_AnonymousOpenRoute(t *testing.T) {
	t.Parallel()
	r := newAuthTestEngine(localTokenAuth("secret-token"))

	w := authRequest(r, "/open", "")
	assert.Equal(t, http.StatusOK, w.Code, "public routes stay open in local token mode")
}

// TestConstantTimeEqual 验证常量时间比较的函数本身：相等 true、不等 false、
// 长度不同也 false。发现背景：ConstantTimeCompare 长度不同时返回 0，是期望行为。
func TestConstantTimeEqual(t *testing.T) {
	t.Parallel()
	assert.True(t, constantTimeEqual("same", "same"))
	assert.False(t, constantTimeEqual("same", "diff"))
	assert.False(t, constantTimeEqual("a", "ab"), "length mismatch must be false")
	assert.False(t, constantTimeEqual("", "x"))
	assert.True(t, constantTimeEqual("", ""))
}

// TestAuthRequired_RegServerSet_EmptyHeader401: 远端模式（reg server 已设、不可达）下，
// 不带 Authorization 也必须 401——绝不能因 whoami 失败而放行。
func TestAuthRequired_RegServerSet_EmptyHeader401(t *testing.T) {
	t.Parallel()
	r := newAuthTestEngine(regServerAuth("http://127.0.0.1:1"))

	w := authRequest(r, "/protected", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestValidateToken_NoRegServer_ReturnsEmpty: validateToken 在 regServerURL 为空时
// 直接返回空结果，不做任何网络调用（本地模式由 AuthRequired 分流，不会走到这里）。
func TestValidateToken_NoRegServer_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	// authDisabled()=false（有 AdminToken）但 regServerURL 为空
	a := localTokenAuth("secret-token")

	u, role := a.validateToken("any")
	assert.Equal(t, "", u)
	assert.Equal(t, "", role)
}
