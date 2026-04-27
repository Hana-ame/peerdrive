// Package controller provides HTTP handlers for WebRTC-related endpoints.
package controller

import (
	"net/http"

	"peerdrive/internal/config"

	"github.com/gin-gonic/gin"
)

// WebRTCInfoHandler 返回 GET /p2p/webrtc/info 的 Gin 处理函数，提供 STUN/TURN 配置。
func WebRTCInfoHandler(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := gin.H{
			"stun_server": cfg.WebRTCSTUNServer,
		}
		if cfg.WebRTCTURNServer != "" {
			resp["turn_server"] = cfg.WebRTCTURNServer
		}
		c.JSON(http.StatusOK, resp)
	}
}
