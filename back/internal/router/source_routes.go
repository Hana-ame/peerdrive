package router

// source_routes.go：统一 source 管理端点（source 体系的管理面）。
// GET  /sources                  → 每个 source 的状态 + 统计（优先级/能力/在线/命中）
// POST /sources/:name/priority   → 运行时调整路由优先级（body: {"priority": n}）

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/source"
)

// sourceManager 由 main 注入（与 peerjsService 同装配模式）。
var sourceManager *source.Manager

// SetSourceManager 注入统一文件管理（nil 则跳过管理端点）。
func SetSourceManager(mgr *source.Manager) {
	sourceManager = mgr
}

// registerSourceRoutes 注册 source 管理端点。
func registerSourceRoutes(r *gin.Engine) {
	if sourceManager == nil {
		return
	}
	r.GET("/sources", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"sources": sourceManager.Snapshot()})
	})
	r.POST("/sources/:name/priority", func(c *gin.Context) {
		var body struct {
			Priority int `json:"priority"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "priority required"})
			return
		}
		if err := sourceManager.SetPriority(c.Param("name"), body.Priority); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
}
