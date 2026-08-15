// Package router 提供 Gin 认证中间件。AuthOptional 允许匿名请求通过（authenticated=false），
// AuthRequired 拒绝无有效令牌的请求。令牌通过注册服务器 /auth/whoami 验证。
package router

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
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

func validateToken(token string) (string, string) {
	if authDisabled() {
		return "", "" // no reg server configured, auth disabled
	}
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
