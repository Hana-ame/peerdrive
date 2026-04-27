// Package controller provides HTTP handlers for WebRTC-related endpoints.
package controller

import (
	"net/http"

	"peerdrive/internal/config"

	"github.com/gin-gonic/gin"
)

// WebRTCInfoHandler returns a Gin handler for GET /p2p/webrtc/info.
// It returns the STUN/TURN configuration that the browser should use
// when creating an RTCPeerConnection for WebRTC file transfers.
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
