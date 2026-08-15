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
	"net/http"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/log"
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
		// L8：原实现非白名单 Origin 也回 `Allow-Origin: *` + `Allow-Credentials: true`——
		// 恶意网页（无凭据）仍能跨域读取本机 API 响应，白名单形同虚设。
		// 现在：仅白名单 Origin 回显 origin（+credentials 才合法）；其余不设
		// Allow-Origin（浏览器阻止读取响应）。无 Origin（同源/curl）不设 CORS 头，
		// 同源请求本来就不需要 CORS 授权。
		origin := c.Request.Header.Get("Origin")
		allowOrigin := ""
		if origin != "" {
			if cfg.IsOriginAllowed(origin) {
				allowOrigin = origin
				c.Header("Vary", "Origin")
			}
		}
		if allowOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowOrigin)
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
		c.Header("Access-Control-Allow-Headers", c.Request.Header.Get("Access-Control-Request-Headers"))
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
	// 挂载到所有 mutating/admin 路由：未配置注册服务器时 AuthRequired 内部放行
	// （本地单机模式），配置后则要求 Bearer token（F1：此前 AuthRequired 0 调用点，
	// 任意文件读写/删除接口全部匿名可达）。
	authRequired := AuthRequired()

	controller.InitP2PController(p2pSvc)
	fileSvc := service.NewFileService(cfg)
	fileSvc.SetIPFSCompat(ipfsCompat)
	controller.InitFileController(fileSvc)
	controller.InitIPFSCompatController(ipfsCompat)
	// M2 收层装配：集合/分享/任务/pin 服务注入 controller（替代原先的 repository 直调）
	controller.InitCollectionController(service.NewCollectionService())
	controller.InitShareController(service.NewShareService())
	controller.InitTaskController(service.NewTaskService())
	controller.InitPinController(service.NewPinService())

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
					// M2 收层：登记逻辑收敛进 FileService.RegisterBTFile（原内联写库）
					if err := fileSvc.RegisterBTFile(f.SHA256, f.Size, f.Path); err != nil {
						log.LogWarn("router: register BT file %s failed: %v", f.SHA256, err)
					} else {
						log.LogInfo("router: registered BT file %s -> storage", f.SHA256)
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
	r.POST("/download/:hash/refresh", authRequired, controller.UniversalDownloadRefresh)

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
		// 以下均为 mutating 操作（连对端/拉取/同步/转发/下载控制），挂认证
		p2p.POST("/connect", authRequired, controller.ConnectPeer)
		p2p.POST("/announce", authRequired, controller.AnnounceHash)
		p2p.POST("/fetch", authRequired, controller.FetchCollection)
		p2p.POST("/sync", authRequired, controller.SyncFromPeer)
		p2p.POST("/push", authRequired, controller.PushSync)
		p2p.POST("/request-file", authRequired, controller.RequestFile)
		p2p.GET("/ws/info", controller.WSInfo)
		p2p.GET("/webrtc/info", controller.WebRTCInfoHandler(cfg))

		// Dual P2P (IPFS + BT DHT) routes
		p2p.POST("/dual/announce", authRequired, controller.DualAnnounce)
		p2p.POST("/dual/find", authRequired, controller.DualFindProviders)
		// Port forwarding routes
		p2p.POST("/forward/create", authRequired, controller.CreateForwardSession)
		p2p.POST("/forward/connect", authRequired, controller.ConnectForwardSession)
		p2p.GET("/forward/list", controller.ListForwardSessions)
		p2p.POST("/forward/close", authRequired, controller.CloseForwardSession)

		// Resume-able P2P download routes
		p2p.POST("/download/resume", authRequired, controller.ResumeDownload)
		p2p.GET("/download/progress/:hash", controller.DownloadProgress)
		p2p.POST("/download/cancel/:hash", authRequired, controller.CancelDownload)

		// Multi-peer download routes
		p2p.POST("/download/multipeer", authRequired, controller.MultiPeerDownload)
		p2p.GET("/download/sources/:hash", controller.DownloadSources)
		p2p.GET("/download/multipeer/progress/:hash", controller.MultiPeerProgress)
	}

	// BitTorrent routes
	bt := r.Group("/bt")
	{
		bt.GET("/status", controller.BTDHTStatus)
		bt.POST("/announce", authRequired, controller.BTAnnounce)
		bt.POST("/find", authRequired, controller.BTFindProviders)
		// BEP 44 (arbitrary DHT data storage)
		bt.POST("/bep44/put", authRequired, controller.BEP44Put)
		bt.POST("/bep44/get", authRequired, controller.BEP44Get)
		// BEP 51 (infohash indexing)
		bt.GET("/bep51/sample", controller.BEP51Sample)
		// BitTorrent download routes (torrent files, magnet links)
		bt.POST("/torrent", authRequired, controller.BTTorrentUpload)
		bt.POST("/magnet", authRequired, controller.BTMagnetResolve)
		bt.GET("/download/:infohash", controller.BTDownloadProgress)
		bt.GET("/download/:infohash/torrent", controller.BTDownloadTorrent)
		bt.GET("/download/:infohash/magnet", controller.BTDownloadMagnet)
		bt.GET("/downloads", controller.BTDownloadList)
		bt.POST("/download/:infohash/pause", authRequired, controller.BTPauseDownload)
		bt.POST("/download/:infohash/resume", authRequired, controller.BTResumeDownload)
		bt.POST("/download/:infohash/seed", authRequired, controller.BTSeedTorrent)
		bt.POST("/download/:infohash/unseed", authRequired, controller.BTStopSeed)
		bt.DELETE("/download/:infohash", authRequired, controller.BTRemoveDownload)
		bt.POST("/seed-collection", authRequired, controller.BTSeedCollection)
		bt.GET("/stats", controller.BTGlobalStats)
	}

	// IPFS compat routes
	ipfs := r.Group("/ipfs")
	{
		ipfs.GET("", controller.IPFSCompatStatus)
		ipfs.POST("/toggle", authRequired, controller.IPFSCompatToggle)
		// IPFS pin routes
		ipfs.POST("/pin/:cid", authRequired, controller.PinCID)
		ipfs.DELETE("/pin/:cid", authRequired, controller.UnpinCID)
		ipfs.GET("/pins", controller.ListPins)
		// IPFS gateway status
		ipfs.GET("/gateways", controller.IPFSGatewayStatus)
	}

	// ── 统一 Collection 路由（新代码使用这些） ──
	// 背景：原代码存在 legacy redirect（/anon/*、/actions/*）与真实路由重复注册，
	// gin 启动即 panic（"handlers are already registered"），服务根本起不来。
	// 修复方案：删掉全部 redirect（前端已直接用新路径），冲突路由合并为
	// 分派器（dispatchCreateCollection / dispatchGetCollection / dispatchGetTree）。
	coll := r.Group("/collections")
	{
		// POST /collections 分派：body 带 username 走用户体系，否则匿名集合
		coll.POST("", authRequired, dispatchCreateCollection)
		coll.GET("", controller.ListAnonCollections)
		// GET /collections/:id 分派：64 位 hex 为匿名集合 hash，否则按 username 列出
		coll.GET("/:id", dispatchGetCollection)
		// GET /collections/:id/*filepath 分派 anon 文件下载与用户集合子路由
		//（gin 不允许 :param 与 *wildcard 共存，统一走分派器）
		coll.GET("/:id/*filepath", dispatchGetTree)
		coll.POST("/fork", authRequired, controller.ForkAnonCollection)
		coll.POST("/merge", authRequired, controller.MergeFromSource)
		coll.POST("/pull", authRequired, controller.PullCollection)
		coll.POST("/upload", authRequired, controller.UploadFile)
		coll.POST("/register-local", authRequired, controller.RegisterLocalFile)
		coll.POST("/register-url", authRequired, controller.RegisterURL)
		coll.POST("/register-folder", authRequired, controller.RegisterFolder)
	}

	// Anonymous Collection routes (public read；创建/提交/fork 为写操作挂认证)
	anon := r.Group("/anon")
	{
		anon.POST("/collections", authRequired, controller.CreateAnonCollection)
		anon.GET("/collections", controller.ListAnonCollections)
		anon.POST("/collections/commit", authRequired, controller.CommitAnonCollection)
		anon.GET("/collections/:hash", controller.GetAnonCollection)
		anon.GET("/collections/:hash/*filepath", controller.DownloadAnonFile)
		anon.POST("/collections/fork", authRequired, controller.ForkAnonCollection)
	}

	// File management（browse/list/verify 只读开放；写操作挂认证）
	files := r.Group("/files")
	{
		files.GET("", controller.ListFiles)
		files.POST("/upload", authRequired, controller.UploadFile)
		files.POST("/register_local", authRequired, controller.RegisterLocalFile)
		files.POST("/register_url", authRequired, controller.RegisterURL)
		files.POST("/register_folder", authRequired, controller.RegisterFolder)
		files.GET("/verify/:hash", controller.VerifyFile)
		files.GET("/browse", controller.BrowseDir)
		files.DELETE("/:hash", authRequired, controller.DeleteFile)
		files.POST("/copy", authRequired, controller.CopyFile)
		files.POST("/diff", authRequired, controller.DiffVersions)
	}

	// Collection management（写操作挂认证）
	collections := r.Group("/collections")
	{
		collections.GET("/public", controller.ListPublicCollections)
		collections.GET("/search", controller.SearchCollections)
		collections.POST("/:id/:collection_name/entries", authRequired, controller.AddEntry)
		collections.DELETE("/:id/:collection_name/entries/*path", authRequired, controller.RemoveEntry)
		collections.POST("/:id/:collection_name/commit", authRequired, controller.CommitCollection)
		collections.POST("/:id/:collection_name/rollback/:version_id", authRequired, controller.RollbackCollection)
		collections.POST("/:id/:collection_name/visibility", authRequired, controller.SetCollectionVisibility)
		collections.POST("/:id/:collection_name/tags", authRequired, controller.UpdateCollectionTags)
	}

	// Local sync（写挂认证，读开放）
	sync := r.Group("/local")
	{
		sync.POST("/save", authRequired, syncCtrl.SaveLocal)
		sync.GET("/status/:hash", syncCtrl.GetStatus)
	}
	// 向后兼容 redirects: /actions/* → /collections/*
	// (已移除：与真实路由冲突；前端已直接用 /collections/fork|merge|pull)

	// Collaboration actions
	actions := r.Group("/actions")
	{
		actions.POST("/merge", authRequired, controller.MergeFromSource)
		actions.POST("/fork", authRequired, controller.ForkCollection)
		actions.POST("/pull", authRequired, controller.PullCollection)
	}

	// Public collection file download
	r.GET("/:username/:collection_name/*filepath", controller.DownloadCollectionFile)

	// Task status
	tasks := r.Group("/tasks")
	{
		tasks.GET("", controller.ListTasks)
		tasks.GET("/:id", controller.GetTaskStatus)
	}

	// Share links（创建挂认证；读取 token 公开）
	shares := r.Group("/shares")
	{
		shares.POST("", authRequired, controller.CreateShare)
		shares.GET("", authRequired, controller.ListShares)
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
	// M12：WebDAV 写/删此前无任何认证 → 挂 AuthRequired（未配置注册服务器时放行，本地模式不受影响）
	if cfg.WebDAVEnable {
		webdavSvc := service.NewWebDAVService(cfg.StorageDir)
		// WebDAV uses wildcard path: all /webdav/* requests go to WebDAV handler
		r.Any("/webdav/*path", authRequired, func(c *gin.Context) {
			c.Request.URL.Path = c.Param("path")
			webdavSvc.ServeHTTP(c)
		})
	}

	// WebRTC signaling
	controller.InitSignalHub(service.NewSignalingHub())
	r.GET("/ws/signal", func(c *gin.Context) {
		controller.GetSignalHub().HandleConnection(c.Writer, c.Request)
	})

	// PeerJS 节点发现
	registerPeerJSRoutes(r, authRequired)

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
