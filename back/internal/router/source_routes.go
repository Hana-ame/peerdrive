package router

// source_routes.go：统一 source 管理端点（source 体系的管理面）。
// GET  /sources                  → 每个 source 的状态 + 统计（优先级/能力/在线/命中）
// POST /sources/:name/priority   → 运行时调整路由优先级（body: {"priority": n}）

import (
	"io"
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
func registerSourceRoutes(r *gin.Engine, authRequired gin.HandlerFunc) {
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

	// Source 控制面：Local 添加本地文件/直接写文件。
	// 仅管理/认证路由开放（authRequired 与其它写操作一致）；这是把“文件如何
	// 进本地 source”从分散 controller 收口到统一 source 管理面的第一步。
	r.POST("/sources/local/add", authRequired, func(c *gin.Context) {
		var body struct {
			Path string `json:"path"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
			return
		}
		s := sourceManager.Get("local")
		if s == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "local source not found"})
			return
		}
		lc, ok := source.LocalControlOf(s)
		if !ok {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "local source does not support control"})
			return
		}
		meta, err := lc.AddLocalFile(body.Path)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"hash":     meta.Hash,
			"size":     meta.Size,
			"filename": meta.Name,
			"path":     meta.Path,
		})
	})
	r.POST("/sources/local/write", authRequired, func(c *gin.Context) {
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
			return
		}
		defer file.Close()
		s := sourceManager.Get("local")
		if s == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "local source not found"})
			return
		}
		lc, ok := source.LocalControlOf(s)
		if !ok {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "local source does not support control"})
			return
		}
		meta, err := lc.WriteFile(header.Filename, file)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"hash":     meta.Hash,
			"size":     meta.Size,
			"filename": meta.Name,
			"path":     meta.Path,
		})
	})

	// BT 控制面：下载 torrent / magnet / 管理任务
	r.POST("/sources/bt/torrent", authRequired, func(c *gin.Context) {
		file, _, err := c.Request.FormFile("torrent")
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "torrent file is required"})
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read torrent file"})
			return
		}
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		meta, err := bc.DownloadTorrent(data)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, meta)
	})
	r.POST("/sources/bt/magnet", authRequired, func(c *gin.Context) {
		var body struct {
			URI string `json:"uri"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.URI == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uri is required"})
			return
		}
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		meta, err := bc.DownloadMagnet(body.URI)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, meta)
	})
	r.GET("/sources/bt/downloads", authRequired, func(c *gin.Context) {
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"downloads": bc.ListDownloads()})
	})
	r.GET("/sources/bt/download/:infohash", authRequired, func(c *gin.Context) {
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		ds := bc.GetDownload(c.Param("infohash"))
		if ds == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "download not found"})
			return
		}
		c.JSON(http.StatusOK, ds)
	})
	r.POST("/sources/bt/download/:infohash/pause", authRequired, func(c *gin.Context) {
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		if err := bc.PauseDownload(c.Param("infohash")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.POST("/sources/bt/download/:infohash/resume", authRequired, func(c *gin.Context) {
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		if err := bc.ResumeDownload(c.Param("infohash")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.DELETE("/sources/bt/download/:infohash", authRequired, func(c *gin.Context) {
		bc := sourceManager.GetBTControl()
		if bc == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "BT control not configured"})
			return
		}
		if err := bc.RemoveDownload(c.Param("infohash")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

}
