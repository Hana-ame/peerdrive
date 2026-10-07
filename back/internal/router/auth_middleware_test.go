package router

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// resetTestState clears all package-level mutable state between tests.
func resetTestState() {
	regServerURL = ""
	adminToken = ""
	tokenCache.Lock()
	tokenCache.m = map[string]tokenCacheEntry{}
	tokenCache.Unlock()
}

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(AuthOptional())
	authReq := AuthRequired()
	r.GET("/protected", authReq, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/open", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func doRequest(r *gin.Engine, method, path, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthRequired_NoRegServerNoAdminToken_AuthDisabled(t *testing.T) {
	resetTestState()
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "")
	assert.Equal(t, http.StatusOK, w.Code, "auth disabled should allow access")
}

func TestAuthRequired_NoRegServerWithAdminToken_NoHeader_Returns401(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "missing Bearer token should return 401")
}

func TestAuthRequired_NoRegServerWithAdminToken_CorrectToken_Passes(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "Bearer my-secret-token")
	assert.Equal(t, http.StatusOK, w.Code, "correct Bearer token should pass")
}

func TestAuthRequired_NoRegServerWithAdminToken_WrongToken_Returns401(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "Bearer wrong-token")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "wrong Bearer token should return 401")
}

func TestAuthRequired_NoRegServerWithAdminToken_InvalidFormat_Returns401(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "Basic dXNlcjpwYXNz")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "invalid Authorization format should return 401")
}

func TestAuthOptional_NoRegServerWithAdminToken_NoHeader_AuthenticatedFalse(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	gin.SetMode(gin.TestMode)
	var got bool
	r := gin.New()
	r.Use(AuthOptional())
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get("authenticated")
		if b, ok := v.(bool); ok {
			got = b
		}
		c.JSON(200, gin.H{})
	})
	doRequest(r, "GET", "/test", "")
	assert.False(t, got, "no header should set authenticated=false")
}

func TestAuthOptional_NoRegServerWithAdminToken_CorrectToken_AuthenticatedTrue(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	gin.SetMode(gin.TestMode)
	var got bool
	r := gin.New()
	r.Use(AuthOptional())
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get("authenticated")
		if b, ok := v.(bool); ok {
			got = b
		}
		c.JSON(200, gin.H{})
	})
	doRequest(r, "GET", "/test", "Bearer my-secret-token")
	assert.True(t, got, "correct Bearer token should set authenticated=true")
}

func TestAuthOptional_NoRegServerWithAdminToken_WrongToken_AuthenticatedFalse(t *testing.T) {
	resetTestState()
	SetAdminToken("my-secret-token")
	gin.SetMode(gin.TestMode)
	var got bool
	r := gin.New()
	r.Use(AuthOptional())
	r.GET("/test", func(c *gin.Context) {
		v, _ := c.Get("authenticated")
		if b, ok := v.(bool); ok {
			got = b
		}
		c.JSON(200, gin.H{})
	})
	doRequest(r, "GET", "/test", "Bearer wrong-token")
	assert.False(t, got, "wrong Bearer token should set authenticated=false, not reject")
}

func TestAuthRequired_WithRegServer_AdminTokenIgnored(t *testing.T) {
	resetTestState()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid"}`))
	}))
	defer ts.Close()
	SetRegServer(ts.URL)
	SetAdminToken("my-secret-token")
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "Bearer my-secret-token")
	assert.Equal(t, http.StatusUnauthorized, w.Code, "with regServer, adminToken is ignored; whoami decides")
}

func TestAuthRequired_WithRegServer_CorrectWhoamiToken_Passes(t *testing.T) {
	resetTestState()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"alice","role":"admin"}`))
	}))
	defer ts.Close()
	SetRegServer(ts.URL)
	r := newTestEngine()
	w := doRequest(r, "GET", "/protected", "Bearer valid-remote-token")
	assert.Equal(t, http.StatusOK, w.Code, "valid remote whoami should pass")
}

func TestConstantTimeEqual(t *testing.T) {
	assert.True(t, constantTimeEqual("same", "same"))
	assert.True(t, constantTimeEqual("", ""))
	assert.False(t, constantTimeEqual("a", "b"))
	assert.False(t, constantTimeEqual("abc", "abd"))
	assert.False(t, constantTimeEqual("", "a"))
	assert.False(t, constantTimeEqual("a", ""))
}

func TestAuthDisabled_LogsCorrectly(t *testing.T) {
	resetTestState()
	assert.True(t, authDisabled(), "both empty should be disabled")
	SetRegServer("http://reg.example.com")
	assert.False(t, authDisabled(), "regServer set should not be disabled")
	SetRegServer("")
	SetAdminToken("tok")
	assert.False(t, authDisabled(), "adminToken set should not be disabled")
	SetRegServer("http://reg.example.com")
	SetAdminToken("tok")
	assert.False(t, authDisabled(), "both set should not be disabled")
	SetRegServer("")
	SetAdminToken("")
	assert.True(t, authDisabled(), "both empty should be disabled")
}

func TestTokenCache_Reset(t *testing.T) {
	resetTestState()
	cachePut("tok1", "user1", "admin")
	u, r, ok := cacheGet("tok1")
	assert.True(t, ok)
	assert.Equal(t, "user1", u)
	assert.Equal(t, "admin", r)
	resetTestState()
	_, _, ok = cacheGet("tok1")
	assert.False(t, ok, "cache should be empty after reset")
}

func TestConcurrentSetters(t *testing.T) {
	resetTestState()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) { defer wg.Done(); SetRegServer("http://reg") }(i)
		go func(i int) { defer wg.Done(); SetAdminToken("tok") }(i)
	}
	wg.Wait()
}
