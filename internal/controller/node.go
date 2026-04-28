package controller

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"peerdrive/internal/config"
)

var cfg *config.Config

func InitConfig(c *config.Config) { cfg = c }

// GetNodeInfo handles GET /p2p/node — returns node identity info
func GetNodeInfo(c *gin.Context) {
	hostname, _ := os.Hostname()
	c.JSON(http.StatusOK, gin.H{
		"peer_id":  "base-node-" + hostname,
		"hostname": hostname,
		"addrs":    []string{},
		"version":  cfg.Version,
		"p2p":      false,
	})
}
