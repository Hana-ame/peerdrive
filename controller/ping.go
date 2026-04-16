package controller

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// Ping godoc
// @Summary Ping the server
// @Description Returns a pong message
// @Tags health
// @Produce json
// @Success 200 {object} map[string]string
// @Router /ping [get]
func Ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "pong",
	})
}
