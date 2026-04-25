// Package router 将 Gin 路由注册到所有 Controller 处理函数。
// 由 cmd/server/main.go 调用，传入已初始化的服务实例（Downloader、
// P2PService、storageDir）。
// 路由分组：
//   /ping              — 健康检查（GET）
//   /sha256sum/:sha256 — 通过 SHA256 哈希下载文件（GET，含 P2P 回退）
//   /auth/*           — 用户注册、登录、登出（POST/POST/POST/GET）
//   /p2p/*             — P2P 节点信息、对等列表、Ping（GET）
//   /anon/*            — 匿名合集创建/读取/Fork（POST/GET）
//   /files/*           — 文件上传/注册/验证/删除/版本差异（POST/POST/POST/GET/DELETE）
//   /collections/*     — 集合 CRUD + 条目管理 + 版本控制（POST/GET）
//   /local/*           — 本地同步状态管理（POST/GET）
//   /actions/*         — 合并/复刻/拉取（POST）
//   /tasks/*           — 异步任务状态查询（GET）
//   /:user/:coll/*     — 从集合条目中下载文件（GET）
//   /collections/search — 公开搜索合集（GET）
//   /swagger/*         — Swagger UI 页面（GET）

package router

import (
	"net/http"

	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func SetupRouter(
	downloader *service.Downloader,
	p2pSvc *service.P2PService,
	cfg *config.Config,
) *gin.Engine {
	r := gin.Default()

	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		} else {
			c.Header("Access-Control-Allow-Origin", "*")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		c.Header("Access-Control-Expose-Headers", "Content-Disposition, X-Peerdrive-Collection")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	controller.InitDownloader(downloader)
	controller.InitP2PController(p2pSvc)
	controller.InitFileController(service.NewFileService(cfg))
	controller.InitAnonController(service.NewAnonService(cfg))

	// Sync controller initialization
	syncRepo := repository.NewSyncRepository()
	syncSvc := service.NewSyncService(syncRepo, downloader)
	syncCtrl := controller.NewSyncController(syncSvc)

	r.GET("/ping", controller.Ping)
	r.GET("/sha256sum/:sha256", controller.DownloadBySHA256)

	// P2P routes (public)
	p2p := r.Group("/p2p")
	{
		p2p.GET("/status", controller.P2PStatus)
		p2p.GET("/node", controller.GetNodeInfo)
		p2p.GET("/peers", controller.GetPeers)
		p2p.GET("/discovered", controller.GetDiscoveredPeers)
		p2p.GET("/ping/:peer_id", controller.PingPeer)
		p2p.POST("/connect", controller.ConnectPeer)
		p2p.POST("/announce", controller.AnnounceHash)
		p2p.POST("/fetch", controller.FetchCollection)
		p2p.POST("/sync", controller.SyncFromPeer)
		p2p.POST("/push", controller.PushSync)
	}

	// Anonymous Collection routes (public)
	anon := r.Group("/anon")
	{
		anon.POST("/collections", controller.CreateAnonCollection)
		anon.GET("/collections/:hash", controller.GetAnonCollection)
		anon.GET("/collections/:hash/entries/*filepath", controller.DownloadAnonFile)
		anon.POST("/collections/fork", controller.ForkAnonCollection)
	}

	// File management
	files := r.Group("/files")
	{
		files.POST("/upload", controller.UploadFile)
		files.POST("/register_local", controller.RegisterLocalFile)
		files.POST("/register_folder", controller.RegisterFolder)
		files.GET("/verify/:hash", controller.VerifyFile)
		files.DELETE("/:hash", controller.DeleteFile)
		files.POST("/diff", controller.DiffVersions)
	}

	// Collection management
	collections := r.Group("/collections")
	{
		collections.POST("", controller.CreateCollection)
		collections.GET("/:username", controller.ListCollections)
		collections.GET("/:username/:collection_name", controller.GetCollection)
		collections.POST("/:username/:collection_name/entries", controller.AddEntry)
		collections.DELETE("/:username/:collection_name/entries/*path", controller.RemoveEntry)
		collections.POST("/:username/:collection_name/commit", controller.CommitCollection)
		collections.GET("/:username/:collection_name/log", controller.GetVersionLog)
		collections.POST("/:username/:collection_name/rollback/:version_id", controller.RollbackCollection)
	}

	// Local sync
	sync := r.Group("/local")
	{
		sync.POST("/save", syncCtrl.SaveLocal)
		sync.GET("/status/:hash", syncCtrl.GetStatus)
	}

	// Collaboration actions
	actions := r.Group("/actions")
	{
		actions.POST("/merge", controller.MergeFromSource)
		actions.POST("/fork", controller.ForkCollection)
		actions.POST("/pull", controller.PullCollection)
	}

	// Public collection file download
	r.GET("/:username/:collection_name/*filepath", controller.DownloadCollectionFile)

	// Task status
	tasks := r.Group("/tasks")
	{
		tasks.GET("", controller.ListTasks)
		tasks.GET("/:id", controller.GetTaskStatus)
	}

	// Public search
	r.GET("/collections/search", controller.SearchCollections)

	// Swagger
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}