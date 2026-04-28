package router

import (
	"github.com/gin-gonic/gin"
	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/repository"
)

// SetupRouter builds the Gin engine with all registered routes.
// Modules add their routes by calling Register* functions before setup.
func SetupRouter(cfg *config.Config) *gin.Engine {
	r := gin.Default()

	// CORS middleware
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Range")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Init controllers
	controller.InitConfig(cfg)
	controller.InitFileController(cfg.Storage)

	// === Base routes ===
	r.GET("/ping", controller.Ping)

	// Node info
	r.GET("/p2p/node", controller.GetNodeInfo)

	// SHA256 content-addressed download
	r.GET("/sha256sum/:sha256", controller.DownloadBySHA256)

	// File management — upload & register
	r.POST("/files/upload", controller.UploadFile)
	r.POST("/files/register_local", controller.RegisterLocalFile)

	// File verify
	r.GET("/files/verify/:hash", func(c *gin.Context) {
		hash := c.Param("hash")
		meta, _ := repository.GetFileMeta(hash)
		if meta == nil {
			c.JSON(200, gin.H{"hash": hash, "exists": false})
			return
		}
		c.JSON(200, gin.H{
			"hash":     meta.Hash,
			"filename": meta.Filename,
			"size":     meta.Size,
			"mime":     meta.MimeType,
			"exists":   true,
		})
	})

	return r
}
