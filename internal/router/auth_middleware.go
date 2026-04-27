// Package router 提供 Gin 认证中间件。AuthOptional 允许匿名请求通过（authenticated=false），
// AuthRequired 拒绝无有效令牌的请求。令牌通过注册服务器 /auth/whoami 验证。
package router

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var regServerURL string

// SetRegServer sets the registration server URL for token validation.
func SetRegServer(url string) {
	regServerURL = url
}

// AuthOptional validates JWT if present, sets username in context.
// Routes behind this middleware get c.GetBool("authenticated") and
// c.GetString("username") if authenticated.
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
		username := validateToken(parts[1])
		if username != "" {
			c.Set("authenticated", true)
			c.Set("username", username)
		} else {
			c.Set("authenticated", false)
		}
		c.Next()
	}
}

// AuthRequired rejects requests without valid JWT.
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
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
		username := validateToken(parts[1])
		if username == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set("username", username)
		c.Next()
	}
}

func validateToken(token string) string {
	if regServerURL == "" {
		return "" // no reg server configured, auth disabled
	}
	client := &http.Client{}
	req, _ := http.NewRequest("GET", regServerURL+"/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var result struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.Username
}
