// Package router 提供 Gin 认证中间件。AuthOptional 允许匿名请求通过（authenticated=false），
// AuthRequired 拒绝无有效令牌的请求。令牌通过注册服务器 /auth/whoami 验证。
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

// SetRegServer 设置用于令牌验证的注册服务器 URL。
func SetRegServer(url string) {
	regServerURL = url
}

// authDisabled 认证后端未配置时返回 true。
// 背景：本地单机模式没有注册服务器，AuthRequired 若强行拒绝会让全站 401 不可用；
// 公网部署配置 RegistrationServer 后自动收紧。放行/收紧以该判断为准。
func authDisabled() bool { return regServerURL == "" }

// AuthOptional 验证 Bearer 令牌（如存在），在 Gin 上下文中设置 authenticated、username 和 role。
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

// AuthRequired 拒绝无有效 Bearer 令牌的请求，返回 401。
// 无注册服务器配置时放行（本地单机模式无认证后端，见 authDisabled）。
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

// tokenCacheTTL 令牌校验结果的缓存时长。
//
// 为什么必须缓存：validateToken 是**每请求一次**的远程调用（注册服务器
// /auth/whoami）。管理台一次列表页能打出十几个请求，于是每个操作都先付一次
// 网络往返——注册服务器一抖，整个管理台就 401（明明令牌是好的）。
// 代价：令牌被吊销后最长 30s 内仍然有效。这是可用性与即时吊销的经典取舍，
// 选 30s 是因为它远小于运维发现异常并处理的反应时间，换来的却是管理台不再
// 随注册服务器一起抖动。要即时吊销就把这里调成 0。
const tokenCacheTTL = 30 * time.Second

// tokenCache 令牌 → 校验结果。只存进程内存：不落盘、不进日志。
var tokenCache = struct {
	sync.RWMutex
	m map[string]tokenCacheEntry
}{m: map[string]tokenCacheEntry{}}

type tokenCacheEntry struct {
	username string
	role     string
	exp      time.Time
}

// cacheGet 取缓存，过期当没命中。
func cacheGet(token string) (string, string, bool) {
	tokenCache.RLock()
	e, ok := tokenCache.m[token]
	tokenCache.RUnlock()
	if !ok || time.Now().After(e.exp) {
		return "", "", false
	}
	return e.username, e.role, true
}

// cachePut 写入缓存并顺带清理过期项（否则被扫过的无效令牌会一直占着内存）。
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
	// 只缓存成功结果：失败可能是网络抖动，缓存了会让一次抖动的影响持续 30s。
	if u != "" {
		cachePut(token, u, r)
	}
	return u, r
}

func queryWhoami(token string) (string, string) {
	// 坑：原实现 http.Client{} 无超时——注册服务器挂起时每个请求都永久阻塞（连接池耗尽）。
	// 修复：5s 超时 + 响应体 64KB 上限（whoami 响应极小，防恶意/被攻破的注册服务器回大包）。
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
