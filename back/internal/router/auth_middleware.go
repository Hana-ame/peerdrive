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
	"os"
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

// PSKGate returns the node-level credential gate for the anonymous
// file-content (byte) outlets: /f/, /sha256sum/, /download/, /ipfs/.
//
// Scope is deliberately the bytes only, not the file index. /files and
// /files/browse return filenames and hashes; a hash cannot become bytes without
// this gate, so the enumeration itself is not the leak. Keeping the index open
// also keeps the operator's directory browsing and
// scripts/netdisk-traversal-probe.sh working. The boundary is pinned in both
// directions by internal/router/http_psk_gate_mandatory_test.go
// (boundary-files-index-not-gated).
//
// Design (peerdrive HTTP access model):
//
//   - HTTP is served by the node itself (same process, same port as the admin
//     panel), so its credential is the node's own PSK — not a peer identity.
//     Holding the PSK means "you are talking to this node", which is why a PSK
//     holder gets FULL access: no share-scope check on /f/ or /sha256sum/. The
//     share scope (public/unlisted/private + friends) belongs to the P2P
//     channel, where the caller is an external peer.
//   - Presentation follows the collection-level convention already used by
//     controller/anon.go: X-PSK header or ?psk= query.
//   - A valid admin token is admitted as well: it is the operator's existing
//     credential for this same node (PEERDRIVE_ADMIN_TOKEN or the registration
//     server), and it is what the browser panel presents. Without it, panel
//     downloads break the moment an operator sets PeerPSK.
//   - psk == "" leaves the gate off. PeerPSK unset is the historical default
//     (no key gate anywhere), and keeping those deployments working matters
//     more than tightening them by surprise.
func PSKGate(auth *Authenticator, psk string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if psk == "" {
			c.Next()
			return
		}
		got := c.GetHeader("X-PSK")
		if got == "" {
			got = c.Query("psk")
		}
		if got != "" && constantTimeEqual(got, psk) {
			c.Next()
			return
		}
		if auth != nil && auth.IsValidAdminCredential(bearerToken(c)) {
			c.Next()
			return
		}
		// 401 = authentication failure (no/invalid credential), matching the
		// AuthRequired vocabulary. 404 would hide "is the file even there",
		// which is exactly the ambiguity this gate exists to remove.
		c.AbortWithStatusJSON(http.StatusUnauthorized,
			gin.H{"error": "psk required or invalid"})
	}
}

// bearerToken extracts the bearer credential from an Authorization header, or ""
// if the header is missing, empty, or not in "Bearer <token>" form.
func bearerToken(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") || parts[1] == "" {
		return ""
	}
	return parts[1]
}

// IsValidAdminCredential reports whether token is a valid admin credential for
// this authenticator — the same two backends AuthRequired uses: local
// PEERDRIVE_ADMIN_TOKEN, or the registration server's /auth/whoami.
func (a *Authenticator) IsValidAdminCredential(token string) bool {
	if token == "" || a == nil {
		return false
	}
	if a.regServerURL == "" {
		return a.adminToken != "" && constantTimeEqual(token, a.adminToken)
	}
	username, _ := a.validateToken(token)
	return username != ""
}

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
				c.Set("role", "admin")
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

// AuthRequired rejects requests without a valid Bearer token.
// Auth mode selection (in priority order):
//  1. regServerURL != "" → remote registration server whoami validation
//  2. adminToken != "" → local constant-time Bearer comparison
//  3. both empty → 503 Service Unavailable (Issue #282: admin surface must not be
//     exposed without authentication; configure PEERDRIVE_ADMIN_TOKEN or
//     PEERDRIVE_REG_SERVER, or bind to localhost). Previously this passed through,
//     making every HTTP admin route world-open in default config.
func (a *Authenticator) AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if a.authDisabled() {
			if os.Getenv("PEERDRIVE_ALLOW_NO_AUTH") == "1" {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error":  "admin auth not configured",
				"detail": "set PEERDRIVE_ADMIN_TOKEN or PEERDRIVE_REG_SERVER to enable admin surface",
			})
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
			c.Set("role", "admin")
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

// ValidateToken validates a Bearer token and returns (username, role).
// Returns empty strings if invalid, expired, or unauthenticated.
func (a *Authenticator) ValidateToken(token string) (string, string) {
	return a.validateToken(token)
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

// Standard role constants for RBAC access control (Issue #433, FULLSTACK-AUDIT §2).
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleMember   = "member"
	RoleViewer   = "viewer"
)

// RequireRole enforces role-based access control (RBAC, FULLSTACK-AUDIT §2 item 2)
// after AuthRequired has authenticated the request.
//
// Rules:
//  1. When auth is disabled and PEERDRIVE_ALLOW_NO_AUTH=1 is set (local single-machine mode),
//     requests pass through.
//  2. The "admin" role (including local AdminToken authentication) is always permitted.
//  3. Otherwise, the context "role" from registration server /auth/whoami must match one of
//     allowedRoles (case-insensitive), or 403 Forbidden is returned.
func (a *Authenticator) RequireRole(allowedRoles ...string) gin.HandlerFunc {
	allowed := make(map[string]bool, len(allowedRoles)+1)
	allowed["admin"] = true
	for _, r := range allowedRoles {
		trimmed := strings.ToLower(strings.TrimSpace(r))
		if trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return func(c *gin.Context) {
		if a.authDisabled() && os.Getenv("PEERDRIVE_ALLOW_NO_AUTH") == "1" {
			c.Next()
			return
		}
		role := strings.ToLower(strings.TrimSpace(c.GetString("role")))
		if role != "" && allowed[role] {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "insufficient role permissions",
		})
	}
}
