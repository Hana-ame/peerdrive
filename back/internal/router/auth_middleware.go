// Package router provides Gin authentication middleware. AuthOptional allows anonymous requests through (authenticated=false),
// AuthRequired rejects requests without a valid token. Tokens are validated via registration server /auth/whoami.
package router

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var regServerURL string

// SetRegServer sets the registration server URL for token validation.
func SetRegServer(url string) {
	regServerURL = url
}

// authDisabled returns true when the authentication backend is not configured.
// Background: local single-machine mode has no registration server; AuthRequired
// forcing rejection would make the entire site return 401 and unusable.
// Public deployments configure RegistrationServer to automatically tighten.
// Allow/tighten decisions rely on this check.
func authDisabled() bool { return regServerURL == "" }

// AuthOptional validates Bearer token (if present), sets authenticated, username, and role in the Gin context.
func AuthOptional() gin.HandlerFunc {
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
		username, role := validateToken(parts[1])
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
// Passes through when no registration server is configured (local single-machine mode has no auth backend, see authDisabled).
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if authDisabled() {
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
		username, role := validateToken(parts[1])
		if username == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set("username", username)
		c.Set("role", role)
		c.Next()
	}
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

// tokenCache token → validation result. Process memory only: no disk, no logging.
var tokenCache = struct {
	sync.RWMutex
	m map[string]tokenCacheEntry
}{m: map[string]tokenCacheEntry{}}

type tokenCacheEntry struct {
	username string
	role     string
	exp      time.Time
}

// cacheGet reads from cache; expired entries are treated as misses.
func cacheGet(token string) (string, string, bool) {
	tokenCache.RLock()
	e, ok := tokenCache.m[token]
	tokenCache.RUnlock()
	if !ok || time.Now().After(e.exp) {
		return "", "", false
	}
	return e.username, e.role, true
}

// cachePut writes to cache and cleans up expired entries (otherwise invalid tokens
// that were scanned would keep occupying memory forever).
func cachePut(token, username, role string) {
	tokenCache.Lock()
	defer tokenCache.Unlock()
	if len(tokenCache.m) > 1024 {
		now := time.Now()
		for k, v := range tokenCache.m {
			if now.After(v.exp) {
				delete(tokenCache.m, k)
			}
		}
	}
	tokenCache.m[token] = tokenCacheEntry{username: username, role: role, exp: time.Now().Add(tokenCacheTTL)}
}

func validateToken(token string) (string, string) {
	if authDisabled() {
		return "", "" // no reg server configured, auth disabled
	}
	if u, r, ok := cacheGet(token); ok {
		return u, r
	}
	u, r := queryWhoami(token)
	// Only cache successful results: failures may be network hiccups; caching would
	// extend a single hiccup's impact for 30s.
	if u != "" {
		cachePut(token, u, r)
	}
	return u, r
}

func queryWhoami(token string) (string, string) {
	// Gotcha: original implementation had http.Client{} with no timeout — when the
	// registration server hangs, every request blocks forever (connection pool exhaustion).
	// Fix: 5s timeout + 64KB response body limit (whoami responses are tiny, this
	// prevents malicious/compromised registration servers from returning large payloads).
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", regServerURL+"/auth/whoami", nil)
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
