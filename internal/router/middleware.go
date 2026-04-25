package router

import (
	"net/http"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware(authSvc *service.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authKey := c.GetHeader("Authorization")
		if len(authKey) > 7 && authKey[:7] == "Bearer " {
			authKey = authKey[7:]
		}

		if authKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			return
		}

		user, err := authSvc.ValidateKey(authKey)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired auth key"})
			return
		}

		c.Set("user", user)
		c.Next()
	}
}
