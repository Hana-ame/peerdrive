package router

import (
	"peerdrive/internal/controller"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
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

	return r
}
