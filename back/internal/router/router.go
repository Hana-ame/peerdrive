// Package router 将 Gin 路由注册到所有 Controller 处理函数。
// 由 cmd/server/main.go 调用，传入已初始化的服务实例（Downloader、
// P2PService、storageDir）。
// 路由分组：
//   /ping              — 健康检查（GET）
//   /sha256sum/:sha256 — 通过 SHA256 哈希下载文件（仅本地存储，无 P2P 回退）
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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/p2p_bt"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// SetupRouter 创建 Gin 引擎并注册全部路由（健康检查、文件下载、P2P、集合、WebDAV、信令等）。
func SetupRouter(
	p2pSvc *service.P2PService,
	cfg *config.Config,
	ipfsCompat *service.IPFSCompatLayer,
	ipfsSvc *service.IPFSService,
) *gin.Engine {
	log.LogInfo("router: SetupRouter starting")
	r := gin.Default()
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false

	// inject shared deps into context (must register before any routes)
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", cfg.StorageDir)
		c.Next()
	})

	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		allowed := "*"
		if origin != "" {
			if cfg.IsOriginAllowed(origin) {
				allowed = origin
				c.Header("Vary", "Origin")
			}
		}
		c.Header("Access-Control-Allow-Origin", allowed)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", c.Request.Header.Get("Access-Control-Request-Headers"))
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})

	// Auth middleware — validates Bearer tokens via registration server.
	// Sets "authenticated" and "username" in Gin context for downstream handlers.
	// Anonymous requests (no token) pass through with authenticated=false.
	if cfg.RegistrationServer != "" {
		SetRegServer(cfg.RegistrationServer)
		r.Use(AuthOptional())
	}

	controller.InitP2PController(p2pSvc)
	fileSvc := service.NewFileService(cfg)
	fileSvc.SetIPFSCompat(ipfsCompat)
	controller.InitFileController(fileSvc)
	controller.InitIPFSCompatController(ipfsCompat)

	// Initialize the port forwarding service.
	var forwardSvc *service.ForwardService
	if p2pSvc != nil && p2pSvc.Host != nil {
		forwardSvc = service.NewForwardService(p2pSvc.Host, cfg.ForwardEnable)
	} else {
		forwardSvc = service.NewForwardService(nil, false)
	}
	controller.InitForwardController(forwardSvc)

	// Initialize BitTorrent DHT service if enabled.
	var btSvc *p2p_bt.BTDHTService
	if cfg.BTDHTEnabled {
		var err error
		btSvc, err = p2p_bt.NewBTDHT(cfg.BTDHTListenAddr)
		if err != nil {
			log.LogWarn("router: BT DHT init warning: %v", err)
		}
	}
	controller.InitBTController(btSvc)

	// Initialize dual P2P service (IPFS + BT DHT).
	dualSvc := service.NewDualP2PService(cfg, p2pSvc, btSvc)
	controller.InitDualController(dualSvc)

	// Initialize resume manager and multi-peer downloader for resume-able downloads.
	resumeMgr := service.NewResumeManager(cfg, p2pSvc, btSvc, dualSvc)
	controller.InitResumeManager(resumeMgr)
	multiPeerDl := service.NewMultiPeerDownloader(cfg, p2pSvc, btSvc, dualSvc)
	controller.InitMultiPeerDownloader(multiPeerDl)

	controller.InitAnonController(service.NewAnonService(cfg))

	// Initialize BitTorrent client for torrent/magnet downloads.
	btClient := p2p_bt.NewBTClient(cfg.DownloadDir)
	if btClient != nil {
		if btSvc != nil && btSvc.Server != nil {
			p2p_bt.SetGlobalDHT(btSvc)
		}
		// When a torrent download completes, register files in peerdrive storage.
		if cfg.StorageEnable {
			btClient.SetOnComplete(func(infohash string, files []p2p_bt.CompletedFile) {
				log.LogInfo("router: BT download complete infohash=%s files=%d", infohash, len(files))
				for _, f := range files {
					if f.SHA256 == "" {
						continue
					}
					_ = repository.InsertFileMeta(&model.FileMeta{
						Hash:     f.SHA256,
						Size:     f.Size,
						Filename: filepath.Base(f.Path),
						Type:     repository.FileTypeBlob,
					})
					relPath := filepath.Join(f.SHA256[:2], f.SHA256)
					_ = repository.InsertFileProvider(f.SHA256, "local", relPath)
					// Copy to storage directory.
					dataDir := filepath.Join(cfg.StorageDir, f.SHA256[:2])
					_ = os.MkdirAll(dataDir, 0755)
					destPath := filepath.Join(dataDir, f.SHA256)
					input, err := os.Open(f.Path)
					if err == nil {
						output, err := os.Create(destPath)
						if err == nil {
							_, _ = io.Copy(output, input)
							_ = output.Close()
							log.LogInfo("router: registered BT file %s -> %s", f.SHA256, destPath)
						}
						_ = input.Close()
					}
				}
			})
		}
		controller.InitBTClient(btClient)
	} else {
		log.LogWarn("router: BT client initialization failed, torrent/magnet features disabled")
	}

	// Initialize the IPFS gateway provider and register with manager.
	var ipfsProv *provider.IPFSProvider
	if cfg.IPFSGatewayEnable {
		gateways := strings.Split(cfg.IPFSGateways, ",")
		for i := range gateways {
			gateways[i] = strings.TrimSpace(gateways[i])
		}
		if len(gateways) > 0 {
			ipfsProv = provider.NewIPFSProvider(gateways)
			if ipfsSvc != nil && ipfsSvc.Enabled() {
				ipfsProv.SetBitswapFetcher(ipfsSvc.FetchByCID)
			}
		}
	}
	controller.InitIPFSProvider(ipfsProv)

	// Initialize the universal multi-protocol downloader.
	downloadTimeout := time.Duration(cfg.DownloadTimeoutSecs) * time.Second
	uniDownloader := service.NewUniversalDownloader(
		p2pSvc,
		btSvc,
		cfg.StorageDir,
		cfg.DownloadOrder,
		downloadTimeout,
		ipfsProv,
	)
	controller.InitUniversalDownloader(uniDownloader)

	// Create peer tracker and wire it into both the P2P service and
	// controller handlers so that connections, transfers, and pings
	// are automatically recorded.
	peerTracker := service.NewPeerTracker()
	transports := []string{"tcp"}
	if strings.Contains(cfg.P2PListenAddr, "quic") || strings.Contains(cfg.P2PListenAddrV6, "quic") {
		transports = append(transports, "quic")
	}
	peerTracker.SetTransports(transports)
	if cfg.RegistrationServer != "" {
		peerTracker.SetRegServerConnected(true)
	}
	controller.InitPeerTracker(peerTracker)
	if p2pSvc != nil {
		p2pSvc.SetPeerTracker(peerTracker)
	}

	// Create and start the PeerScanner when P2P is enabled for proactive
	// peer discovery and outbound connection maintenance.
	if p2pSvc != nil && p2pSvc.IsEnabled() {
		scanner := service.NewPeerScanner(p2pSvc, peerTracker, cfg.RegServerURL)
		scanner.Start()
		controller.InitPeerScanner(scanner)
	}

	// Bootstrap from registration server relay list.
	if cfg.RegServerURL != "" && p2pSvc != nil && p2pSvc.IsEnabled() {
		go bootstrapFromRelayList(cfg.RegServerURL, p2pSvc)
	}

	// Sync controller initialization
	syncRepo := repository.NewSyncRepository()
	syncSvc := service.NewSyncService(syncRepo, uniDownloader, cfg.StorageDir)
	syncCtrl := controller.NewSyncController(syncSvc)

	r.GET("/ping", controller.Ping)
	r.GET("/sha256sum/:sha256", controller.DownloadBySHA256Local)
	r.GET("/sha256sum/:sha256/:filename", controller.DownloadBySHA256Local)
	r.GET("/ipfs/:cid", controller.DownloadByCID)

	// Universal multi-protocol download endpoints.
	r.GET("/download/:hash", controller.UniversalDownload)
	r.GET("/download/:hash/sources", controller.UniversalDownloadSources)
	r.POST("/download/:hash/refresh", controller.UniversalDownloadRefresh)

	// P2P routes (public)
	p2p := r.Group("/p2p")
	{
		p2p.GET("/status", controller.P2PStatus)
		p2p.GET("/auth/status", controller.AuthStatus)
		p2p.GET("/node", controller.GetNodeInfo)
		p2p.GET("/node/operator", controller.GetNodeOperator)
		p2p.GET("/peers", controller.GetPeers)
		p2p.GET("/discovered", controller.GetDiscoveredPeers)
		p2p.GET("/connections", controller.GetConnections)
		p2p.GET("/peers/detail", controller.GetPeersDetail)
		p2p.GET("/peers/detail/:peer_id", controller.GetPeerDetail)
		p2p.GET("/stats", controller.GetP2PStats)
			p2p.GET("/topology", controller.GetTopology)
			p2p.GET("/quality", controller.GetConnectionQuality)
		p2p.GET("/ping/:peer_id", controller.PingPeer)
		p2p.POST("/connect", controller.ConnectPeer)
		p2p.POST("/announce", controller.AnnounceHash)
		p2p.POST("/fetch", controller.FetchCollection)
		p2p.POST("/sync", controller.SyncFromPeer)
		p2p.POST("/push", controller.PushSync)
		p2p.POST("/request-file", controller.RequestFile)
		p2p.GET("/ws/info", controller.WSInfo)
		p2p.GET("/webrtc/info", controller.WebRTCInfoHandler(cfg))


		// Dual P2P (IPFS + BT DHT) routes
		p2p.POST("/dual/announce", controller.DualAnnounce)
		p2p.POST("/dual/find", controller.DualFindProviders)
		// Port forwarding routes
		p2p.POST("/forward/create", controller.CreateForwardSession)
		p2p.POST("/forward/connect", controller.ConnectForwardSession)
		p2p.GET("/forward/list", controller.ListForwardSessions)
		p2p.POST("/forward/close", controller.CloseForwardSession)


			// Resume-able P2P download routes
			p2p.POST("/download/resume", controller.ResumeDownload)
			p2p.GET("/download/progress/:hash", controller.DownloadProgress)
			p2p.POST("/download/cancel/:hash", controller.CancelDownload)

			// Multi-peer download routes
			p2p.POST("/download/multipeer", controller.MultiPeerDownload)
			p2p.GET("/download/sources/:hash", controller.DownloadSources)
			p2p.GET("/download/multipeer/progress/:hash", controller.MultiPeerProgress)
		}


	// BitTorrent routes
	bt := r.Group("/bt")
	{
		bt.GET("/status", controller.BTDHTStatus)
		bt.POST("/announce", controller.BTAnnounce)
		bt.POST("/find", controller.BTFindProviders)
		// BEP 44 (arbitrary DHT data storage)
		bt.POST("/bep44/put", controller.BEP44Put)
		bt.POST("/bep44/get", controller.BEP44Get)
		// BEP 51 (infohash indexing)
		bt.GET("/bep51/sample", controller.BEP51Sample)
		// BitTorrent download routes (torrent files, magnet links)
		bt.POST("/torrent", controller.BTTorrentUpload)
		bt.POST("/magnet", controller.BTMagnetResolve)
		bt.GET("/download/:infohash", controller.BTDownloadProgress)
			bt.GET("/download/:infohash/torrent", controller.BTDownloadTorrent)
			bt.GET("/download/:infohash/magnet", controller.BTDownloadMagnet)
		bt.GET("/downloads", controller.BTDownloadList)
		bt.POST("/download/:infohash/pause", controller.BTPauseDownload)
		bt.POST("/download/:infohash/resume", controller.BTResumeDownload)
		bt.POST("/download/:infohash/seed", controller.BTSeedTorrent)
		bt.POST("/download/:infohash/unseed", controller.BTStopSeed)
		bt.DELETE("/download/:infohash", controller.BTRemoveDownload)
			bt.POST("/seed-collection", controller.BTSeedCollection)
		bt.GET("/stats", controller.BTGlobalStats)
	}

	// IPFS compat routes
	ipfs := r.Group("/ipfs")
	{
		ipfs.GET("", controller.IPFSCompatStatus)
		ipfs.POST("/toggle", controller.IPFSCompatToggle)
		// IPFS pin routes
		ipfs.POST("/pin/:cid", controller.PinCID)
		ipfs.DELETE("/pin/:cid", controller.UnpinCID)
		ipfs.GET("/pins", controller.ListPins)
		// IPFS gateway status
		ipfs.GET("/gateways", controller.IPFSGatewayStatus)
	}

		// ── 统一 Collection 路由（新代码使用这些） ──
		coll := r.Group("/collections")
		{
			coll.POST("", controller.CreateAnonCollection)
			coll.GET("", controller.ListAnonCollections)
			coll.GET("/:hash", controller.GetAnonCollection)
			coll.GET("/:hash/*filepath", controller.DownloadAnonFile)
			coll.POST("/fork", controller.ForkAnonCollection)
			coll.POST("/merge", controller.MergeFromSource)
			coll.POST("/pull", controller.PullCollection)
		}

		// 向后兼容 redirects
		r.GET("/files", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/collections") })
		r.GET("/anon/collections", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/collections") })
		r.POST("/anon/collections", func(c *gin.Context) { c.Redirect(http.StatusPermanentRedirect, "/collections") })
		r.GET("/anon/collections/:hash", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/collections/"+c.Param("hash")) })
		r.GET("/anon/collections/:hash/*filepath", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/collections/"+c.Param("hash")+c.Param("filepath")) })
	// Anonymous Collection routes (public)
	anon := r.Group("/anon")
	{
		anon.POST("/collections", controller.CreateAnonCollection)
		anon.GET("/collections", controller.ListAnonCollections)
		anon.POST("/collections/commit", controller.CommitAnonCollection)
		anon.GET("/collections/:hash", controller.GetAnonCollection)
		anon.GET("/collections/:hash/*filepath", controller.DownloadAnonFile)
		anon.POST("/collections/fork", controller.ForkAnonCollection)
	}

	// File management
	files := r.Group("/files")
	{
		files.GET("", controller.ListFiles)
		files.POST("/upload", controller.UploadFile)
		files.POST("/register_local", controller.RegisterLocalFile)
		files.POST("/register_url", controller.RegisterURL)
		files.POST("/register_folder", controller.RegisterFolder)
		files.GET("/verify/:hash", controller.VerifyFile)
		files.GET("/browse", controller.BrowseDir)
		files.DELETE("/:hash", controller.DeleteFile)
		files.POST("/copy", controller.CopyFile)
		files.POST("/diff", controller.DiffVersions)
	}

	// Collection management
	collections := r.Group("/collections")
	{
		collections.POST("", controller.CreateCollection)
		collections.GET("/public", controller.ListPublicCollections)
		collections.GET("/search", controller.SearchCollections)
		collections.GET("/:username", controller.ListCollections)
		collections.GET("/:username/:collection_name", controller.GetCollection)
		collections.POST("/:username/:collection_name/entries", controller.AddEntry)
		collections.DELETE("/:username/:collection_name/entries/*path", controller.RemoveEntry)
		collections.POST("/:username/:collection_name/commit", controller.CommitCollection)
		collections.GET("/:username/:collection_name/log", controller.GetVersionLog)
		collections.POST("/:username/:collection_name/rollback/:version_id", controller.RollbackCollection)
		collections.POST("/:username/:collection_name/visibility", controller.SetCollectionVisibility)
		collections.POST("/:username/:collection_name/tags", controller.UpdateCollectionTags)
	}

	// Local sync
	sync := r.Group("/local")
	{
		sync.POST("/save", syncCtrl.SaveLocal)
		sync.GET("/status/:hash", syncCtrl.GetStatus)
	}
	// 向后兼容 redirects: /actions/* → /collections/*
	r.POST("/actions/fork", func(c *gin.Context) { c.Redirect(http.StatusPermanentRedirect, "/collections/fork") })
	r.POST("/actions/merge", func(c *gin.Context) { c.Redirect(http.StatusPermanentRedirect, "/collections/merge") })
	r.POST("/actions/pull", func(c *gin.Context) { c.Redirect(http.StatusPermanentRedirect, "/collections/pull") })

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

	// Share links
	shares := r.Group("/shares")
	{
		shares.POST("", controller.CreateShare)
		shares.GET("", controller.ListShares)
	}
	r.GET("/s/:token", controller.AccessShare)

	// Swagger
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// WebSocket file transfer
	if p2pSvc != nil {
		r.GET("/ws/transfer", func(c *gin.Context) {
			p2pSvc.WSHandler()(c.Writer, c.Request)
		})
	}

	// P2P relay proxy
	relaySvc := service.NewRelayService(p2pSvc)
	r.GET("/relay/proxy", relaySvc.ProxyDownload)

	// WebDAV endpoint — mount as network drive
	if cfg.WebDAVEnable {
		webdavSvc := service.NewWebDAVService(cfg.StorageDir)
		// WebDAV uses wildcard path: all /webdav/* requests go to WebDAV handler
		r.Any("/webdav/*path", func(c *gin.Context) {
			c.Request.URL.Path = c.Param("path")
			webdavSvc.ServeHTTP(c)
		})
	}

	// WebRTC signaling
	controller.InitSignalHub(service.NewSignalingHub())
	r.GET("/ws/signal", func(c *gin.Context) {
		controller.GetSignalHub().HandleConnection(c.Writer, c.Request)
	})

	// Count routes
	routes := r.Routes()
	log.LogInfo("router: SetupRouter completed with %d routes", len(routes))
	return r
}

// ──────────────────────────────────────────────
//  Relay bootstrap helpers
// ──────────────────────────────────────────────

// bootstrapFromRelayList fetches the list of active relay nodes from the
// registration server and attempts to connect to each one.  This allows
// new nodes to discover and connect to publicly reachable relay nodes
// without hardcoded bootstrap addresses.
func bootstrapFromRelayList(regURL string, p2pSvc *service.P2PService) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(regURL + "/p2p/relay/list")
	if err != nil {
		log.LogWarn("router: relay list fetch failed: %v", err)
		return
	}
	defer resp.Body.Close()

	var listResp struct {
		Relays []struct {
			PeerID string   `json:"peer_id"`
			Addrs  []string `json:"addrs"`
		} `json:"relays"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		log.LogWarn("router: relay list decode failed: %v", err)
		return
	}

	connected := 0
	for _, relay := range listResp.Relays {
		pid, err := peer.Decode(relay.PeerID)
		if err != nil {
			log.LogWarn("router: invalid relay peer id %s: %v", relay.PeerID, err)
			continue
		}

		if pid == p2pSvc.Host.ID() {
			continue
		}

		var maddrs []multiaddr.Multiaddr
		for _, addrStr := range relay.Addrs {
			maddr, err := multiaddr.NewMultiaddr(addrStr)
			if err != nil {
				continue
			}
			maddrs = append(maddrs, maddr)
		}

		if len(maddrs) == 0 {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := p2pSvc.Host.Connect(ctx, peer.AddrInfo{ID: pid, Addrs: maddrs}); err != nil {
			log.LogWarn("router: connect to relay %s failed: %v", relay.PeerID, err)
		} else {
			log.LogInfo("router: connected to relay %s", relay.PeerID)
			connected++
		}
		cancel()
	}

	log.LogInfo("router: bootstrap from relay list completed (%d connected out of %d)",
		connected, len(listResp.Relays))
}
