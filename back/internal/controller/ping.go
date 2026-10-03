// Health check controller — returns "pong" to confirm the service is running.
// Package controller is the package definition for all API handlers. Each Controller file
// registers its own dependency service instances through independent Init* functions (InitDownloader, InitP2PController, InitFileController).
// This file contains the Ping function: simply returns 200 + "pong" text, used for load balancer health checks.
// Routes:
//   GET /ping — health check

package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Ping godoc
// @Summary Health check
// @Description Returns "pong" to confirm the server is running
// @Tags health
// @Produce plain
// @Success 200 {string} string "pong"
// @Router /ping [get]
func Ping(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}
