package router

import (
	"peerdrive/internal/controller"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

func SetupRouter(
	downloader *service.Downloader,
	p2pSvc *service.P2PService,
) *gin.Engine {
	r := gin.Default()

	controller.InitDownloader(downloader)
	controller.InitP2PController(p2pSvc)

	r.GET("/ping", controller.Ping)
	r.GET("/sha256sum/:sha256", controller.DownloadBySHA256)

	p2p := r.Group("/p2p")
	{
		p2p.GET("/node", controller.GetNodeInfo)
		p2p.GET("/peers", controller.GetPeers)
		p2p.GET("/ping/:peer_id", controller.PingPeer)
	}

	return r
}