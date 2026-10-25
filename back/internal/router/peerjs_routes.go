package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	hashutil "peerdrive/pkg/hashutil"

	"peerdrive/internal/controller"
	"peerdrive/internal/transport"
	"peerdrive/internal/wsconn"
)

func (r *Router) injectControllerDeps() {
	controller.InitForwardController(r.deps.PeerJSService)
	controller.InitPeerShareController(r.deps.PeerJSService)
	controller.InitNodeDirectory(r.deps.NodeDirectory)
	controller.InitNodeShareController(r.deps.NodeShare)
	controller.InitPeerPuller(r.deps.PeerPuller)
	controller.InitAria2Bridge(r.deps.Aria2Bridge)
	controller.InitDisplayController(r.deps.PeerJSService)
	controller.InitStreamController(r.deps.PeerJSService)
	controller.InitFileSearchController(r.deps.PeerJSService)
}

func (rt *Router) registerPeerJSRoutes(r *gin.Engine, auth gin.HandlerFunc) {
	peerjsService := rt.deps.PeerJSService
	if peerjsService == nil {
		return
	}
	peerjsCfg := rt.deps.PeerJSCfg
	r.GET("/peerjs/node", func(c *gin.Context) {
		conns := peerjsService.Connections()
		peers := make([]string, 0, len(conns))
		for pid := range conns {
			peers = append(peers, pid)
		}
		pskEnabled, pskOK := peerjsService.PSKState()
		resp := gin.H{
			"id":        peerjsService.ID(),
			"online":    true,
			"peers":     peers,
			"psk":       pskEnabled,
			"psk_peers": pskOK,
		}
		if peerjsCfg != nil {
			resp["signal_url"] = peerjsCfg.PeerJSURL
			resp["signal_host"] = peerjsCfg.PeerJSHost
			resp["signal_port"] = peerjsCfg.PeerJSPort
			resp["signal_path"] = "/"
			resp["signal_secure"] = peerjsCfg.PeerJSSecure
		}
		c.JSON(http.StatusOK, resp)
	})

	r.GET("/peerjs/nodes", controller.GetNodeMarket)
	r.GET("/peerjs/nodes/joined", controller.GetJoinedNodes)
	r.GET("/peerjs/nodes/:peer/shares", controller.GetPeerShares)
	r.GET("/peerjs/nodes/:peer/search", auth, controller.SearchPeerFiles)
	r.POST("/peerjs/nodes/join", auth, controller.JoinNode)
	r.DELETE("/peerjs/nodes/join", auth, controller.LeaveNode)

	r.GET("/peerjs/blocklist", controller.GetPeerBlocklist)
	r.POST("/peerjs/blocklist", auth, controller.PostPeerBlocklist)
	r.DELETE("/peerjs/blocklist/:peer", auth, controller.DeletePeerBlocklist)

	r.GET("/peerjs/files/search", auth, controller.SearchLocalFiles)

	r.GET("/peerjs/share", auth, controller.GetNodeShare)
	r.PUT("/peerjs/share", auth, controller.PutNodeShare)
	r.POST("/peerjs/share/files", auth, controller.PostNodeShareFiles)

	r.POST("/peerjs/fetch", auth, func(c *gin.Context) {
		var body struct {
			Peer   string `json:"peer"`
			Hash   string `json:"hash"`
			Offset int64  `json:"offset"`
			Size   int64  `json:"size"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.Peer == "" || body.Hash == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "peer and hash required"})
			return
		}
		if body.Offset < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "offset must be non-negative"})
			return
		}
		const maxPeerjsHTTPFetch = 64 << 20
		if body.Size <= 0 {
			body.Size = -1
		}
		if body.Size > maxPeerjsHTTPFetch {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "requested size exceeds 64MB limit; use /ws/peer chunked transfer"})
			return
		}
		if !hashutil.IsValidSHA256(body.Hash) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 hash"})
			return
		}
		data, err := peerjsService.FetchFromPeer(body.Peer, body.Hash, body.Offset, body.Size)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "peer fetch failed"})
			return
		}
		if int64(len(data)) > maxPeerjsHTTPFetch {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "peer returned oversized data"})
			return
		}
		c.Data(http.StatusOK, "application/octet-stream", data)
	})

	var originAllow func(string) bool
	if peerjsCfg != nil {
		originAllow = peerjsCfg.IsOriginAllowed
	}
	upgrader := wsconn.NewUpgrader(wsconn.OriginPolicy{Allow: originAllow})
	wsUpgradeHandler := func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "websocket upgrade failed"})
			return
		}
		sessID := c.Query("id")
		if sessID == "" {
			sessID = c.Query("session")
		}
		if sessID == "" {
			sessID = "local"
		}
		sess := transport.NewWSSession(sessID, conn)
		peerjsService.BindLocal(sess)
	}
	wsDataUpgradeHandler := func(c *gin.Context) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "websocket upgrade failed"})
			return
		}
		sessID := c.Query("id")
		if sessID == "" {
			sessID = c.Query("session")
		}
		if sessID == "" {
			sessID = "ws-data"
		}
		sess := transport.NewWSDataSession(sessID, conn)
		peerjsService.BindLocal(sess)
	}
	r.GET("/ws/peer", wsUpgradeHandler)
	r.GET("/ws/admin", wsUpgradeHandler)
	r.GET("/ws/data", wsDataUpgradeHandler)
}
