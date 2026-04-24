// Package router 将 Gin 路由注册到所有 Controller 处理函数。
// 由 cmd/server/main.go 调用，传入已初始化的服务实例（Downloader、
// P2PService、storageDir）。
// 路由分组：
//   /ping              — 健康检查（GET）
//   /sha256sum/:sha256 — 通过 SHA256 哈希下载文件（GET）
//   /p2p/*             — P2P 节点信息、对等列表、Ping（GET）
//   /files/*           — 文件上传/注册/验证/删除/版本差异（POST/GET/DELETE）
//   /collections/*     — 集合 CRUD + 条目管理 + 版本控制（POST/GET/DELETE）
//   /actions/*         — 合并/复刻/拉取（POST）
//   /tasks/*           — 异步任务状态查询（GET）
//   /:user/:coll/*     — 从集合条目中下载文件（GET）
//   /swagger/*         — Swagger UI 页面（GET）

package router

import (
	"peerdrive/internal/controller"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func SetupRouter(
	downloader *service.Downloader,
	p2pSvc *service.P2PService,
	storageDir string,
) *gin.Engine {
	r := gin.Default()

	controller.InitDownloader(downloader)
	controller.InitP2PController(p2pSvc)
	controller.InitFileController(storageDir)

	r.GET("/ping", controller.Ping)
	r.GET("/sha256sum/:sha256", controller.DownloadBySHA256)

	p2p := r.Group("/p2p")
	{
		p2p.GET("/node", controller.GetNodeInfo)
		p2p.GET("/peers", controller.GetPeers)
		p2p.GET("/ping/:peer_id", controller.PingPeer)
	}

	files := r.Group("/files")
	{
		files.POST("/upload", controller.UploadFile)
		files.POST("/register_local", controller.RegisterLocalFile)
		files.POST("/register_folder", controller.RegisterFolder)
		files.GET("/verify/:hash", controller.VerifyFile)
		files.DELETE("/:hash", controller.DeleteFile)
		files.POST("/diff", controller.DiffVersions)
	}

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

	r.GET("/:username/:collection_name/*filepath", controller.DownloadCollectionFile)

	actions := r.Group("/actions")
	{
		actions.POST("/merge", controller.MergeFromSource)
		actions.POST("/fork", controller.ForkCollection)
		actions.POST("/pull", controller.PullCollection)
	}

	tasks := r.Group("/tasks")
	{
		tasks.GET("", controller.ListTasks)
		tasks.GET("/:id", controller.GetTaskStatus)
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
