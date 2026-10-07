// Package router provides Gin authentication middleware. AuthOptional allows anonymous requests through (authenticated=false),
// AuthRequired rejects requests without a valid token. Tokens are validated via registration server /auth/whoami,
// or locally against PEERDRIVE_ADMIN_TOKEN when no registration server is configured.
//
// ── Instance-scoped since the 2026-10 dependency-injection refactor ──
//
// These used to be package-level globals (`regServerURL`, `adminToken`,
// `tokenCache`) mutated by `SetRegServer`/`SetAdminToken`, which meant one auth
// configuration per process and a mandatory "reset the globals" dance before each
// test. They are now fields of an Authenticator, one per Router:
//
//	a := router.NewAuthenticator(regServerURL, adminToken)
//	r.Use(a.AuthOptional())
//	r.GET("/admin", a.AuthRequired(), handler)
//
// Two Authenticators in one process are fully independent — including their token
// caches, so a token validated via one is never visible to the other.
package router

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Authenticator holds one node's auth backend choice and the token cache for it.
//
// Both string fields are set once at construction and never mutated, so a single
// Authenticator is safe for concurrent use by many requests. The cache is
// mutex-guarded because it is written on every successful remote validation.
type Authenticator struct {
	// regServerURL is the registration server base URL; empty means "no remote
	// auth backend" (then the local adminToken path, or auth-disabled, applies).
	regServerURL string
	// adminToken is the local Bearer token (PEERDRIVE_ADMIN_TOKEN).
	adminToken string
	// tokenCache caches remote whoami results. Per-instance so that two
	// Authenticators pointed at different registration servers never share
	// entries — a shared cache would let a token validated by server A be
	// accepted by a Router wired to server B.
	tokenCache tokenCache
}

// NewAuthenticator builds an Authenticator for one registration server URL and
// local admin token. Pass "" for either to leave that backend unconfigured.
func NewAuthenticator(regServerURL, adminToken string) *Authenticator {
	return &Authenticator{
		regServerURL: regServerURL,
		adminToken:   adminToken,
		tokenCache:   newTokenCache(),
	}
}

// authDisabled returns true when NO authentication backend is configured.
// Background: local single-machine mode has no registration server and no admin
// token; AuthRequired forcing rejection would make the entire site return 401
// and unusable. Public deployments configure RegistrationServer or AdminToken
// to automatically tighten. Allow/tighten decisions rely on this check.
func (a *Authenticator) authDisabled() bool { return a.regServerURL == "" && a.adminToken == "" }

// AuthOptional validates Bearer token (if present), sets authenticated, username, and role in the Gin context.
func (a *Authenticator) AuthOptional() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.Set("authenticated", false)
			c.Next()
			return
		}
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			c.Set("authenticated", false)
			c.Next()
			return
		}
		// 无远端注册服务器时走本地令牌分支：用常量时间比较 AdminToken，
		// 不向任何远端服务发请求（又能让管理面在登录服务关闭时仍可鉴权）。
		if a.regServerURL == "" {
			if a.adminToken != "" && constantTimeEqual(parts[1], a.adminToken) {
				c.Set("authenticated", true)
			} else {
				c.Set("authenticated", false)
			}
			c.Next()
			return
		}
		username, role := a.validateToken(parts[1])
		if username != "" {
			c.Set("authenticated", true)
			c.Set("username", username)
			c.Set("role", role)
		} else {
			c.Set("authenticated", false)
		}
		c.Next()
	}
}

// AuthRequired rejects requests without a valid Bearer token, returns 401.
// Auth mode selection (in priority order):
//  1. regServerURL != "" → remote registration server whoami validation
//  2. adminToken != "" → local constant-time Bearer comparison
//  3. both empty → auth disabled, pass through (local single-machine mode)
func (a *Authenticator) AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.authDisabled() {
			c.Next()
			return
		}
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			return
		}
		// 本地管理员令牌模式：常量时间比较，不给时序侧信道（C-14 follow-up）。
		if a.regServerURL == "" {
			if a.adminToken == "" || !constantTimeEqual(parts[1], a.adminToken) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
				return
			}
			c.Set("authenticated", true)
			c.Next()
			return
		}
		// 远端注册服务器模式。
		username, role := a.validateToken(parts[1])
		if username == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set("username", username)
		c.Set("role", role)
		c.Next()
	}
}

// constantTimeEqual compares two strings in constant time to prevent timing attacks.
func constantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// tokenCacheTTL cache duration for token validation results.
//
// Why caching is mandatory: validateToken is a remote call PER REQUEST (registration
// server /auth/whoami). A single admin console list page fires a dozen requests,
// so every action pays a network round-trip first — if the registration server
// hiccups, the entire admin console gets 401 (even though tokens are valid).
// Cost: revoked tokens remain valid for up to 30s. This is the classic
// availability-vs-immediate-revocation trade-off. 30s is chosen because it's
// far less than the ops reaction time to detect and handle anomalies, and it
// prevents the admin console from jittering with the registration server.
// To enable immediate revocation, set this to 0.
const tokenCacheTTL = 30 * time.Second

// tokenCacheEntry is one cached whoami result.
type tokenCacheEntry struct {
	username string
	role     string
	exp      time.Time
}

// tokenCache maps token → validation result, guarded by its own mutex.
//
// It is a named struct (rather than the anonymous inline struct it used to be) so
// that each Authenticator owns a distinct map. Embedded in Authenticator by
// value, so constructing an Authenticator is enough to get a private cache — there
// is no package-level map left to share, or to reset between tests.
type tokenCache struct {
	mu sync.RWMutex
	m  map[string]tokenCacheEntry
}

func newTokenCache() tokenCache {
	return tokenCache{m: make(map[string]tokenCacheEntry)}
}

// get reads from cache; expired entries are treated as misses.
func (t *tokenCache) get(token string) (string, string, bool) {
	t.mu.RLock()
	e, ok := t.m[token]
	t.mu.RUnlock()
	if !ok || time.Now().After(e.exp) {
		return "", "", false
	}
	return e.username, e.role, true
}

// put writes to cache and cleans up expired entries (otherwise invalid tokens
// that were scanned would keep occupying memory forever).
func (t *tokenCache) put(token, username, role string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.m) > 1024 {
		now := time.Now()
		for k, v := range t.m {
			if now.After(v.exp) {
				delete(t.m, k)
			}
		}
	}
	t.m[token] = tokenCacheEntry{username: username, role: role, exp: time.Now().Add(tokenCacheTTL)}
}

// len reports how many entries are cached. Used by the tests that assert two
// Authenticator instances do not share cache entries.
func (t *tokenCache) len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.m)
}

func (a *Authenticator) validateToken(token string) (string, string) {
	// 只在远端注册服务器模式下被调用（AuthOptional/AuthRequired 已先分流本地令牌），
	// 用 regServerURL 判空而不是 authDisabled()：AdminToken 已设但 regServer 为空时
	// authDisabled() 为 false，若走这里会拿本地令牌去问一个不存在的远端服务。
	if a.regServerURL == "" {
		return "", "" // no reg server configured — local token path handles auth
	}
	if u, r, ok := a.tokenCache.get(token); ok {
		return u, r
	}
	u, r := a.queryWhoami(token)
	// Only cache successful results: failures may be network hiccups; caching would
	// extend a single hiccup's impact for 30s.
	if u != "" {
		a.tokenCache.put(token, u, r)
	}
	return u, r
}

func (a *Authenticator) queryWhoami(token string) (string, string) {
	// Gotcha: original implementation had http.Client{} with no timeout — when the
	// registration server hangs, every request blocks forever (connection pool exhaustion).
	// Fix: 5s timeout + 64KB response body limit (whoami responses are tiny, this
	// prevents malicious/compromised registration servers from returning large payloads).
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", a.regServerURL+"/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", ""
	}
	var result struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result); err != nil {
		return "", ""
	}
	return result.Username, result.Role
}
