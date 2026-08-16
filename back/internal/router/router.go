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
	"net/http"
	"strings"
	"time"

	"github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/legacy"
	"peerdrive/internal/log"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// SetupRouter 创建 Gin 引擎并注册全部路由（健康检查、文件下载、P2P、集合、WebDAV、信令等）。
func SetupRouter(cfg *config.Config) *gin.Engine {
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

	fileSvc := service.NewFileService(cfg)
	controller.InitFileController(fileSvc)
	// M2 收层装配：集合/分享/任务/pin 服务注入 controller（替代原先的 repository 直调）
	controller.InitCollectionController(service.NewCollectionService())
	controller.InitShareController(service.NewShareService())
	controller.InitTaskController(service.NewTaskService())
	controller.InitPinController(service.NewPinService())

	// 端口转发服务（PeerJS DataChannel 版，forward.go）：规则表由 main 装配时
	// SetForwardRules 注入（配置 PEERDRIVE_FORWARD_RULES），运行时端点可动态追加。
	controller.InitForwardController(peerjsService)

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
		}
	}
	controller.InitIPFSProvider(ipfsProv)

	// Initialize the universal multi-protocol downloader.
	downloadTimeout := time.Duration(cfg.DownloadTimeoutSecs) * time.Second
	uniDownloader := legacy.NewUniversalDownloader(
		btSvc,
		cfg.StorageDir,
		cfg.DownloadOrder,
		downloadTimeout,
		ipfsProv,
	)
	controller.InitUniversalDownloader(uniDownloader)

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

	// P2P routes（批2 精简：libp2p 旧栈端点已删，保留认证状态/WebRTC 信息/端口转发）
	p2p := r.Group("/p2p")
	{
		p2p.GET("/auth/status", controller.AuthStatus)
		p2p.GET("/webrtc/info", controller.WebRTCInfoHandler(cfg))
		// Port forwarding routes（forward v2，PeerJS DataChannel）
		p2p.POST("/forward/create", authRequired, controller.CreateForwardSession)
		p2p.POST("/forward/connect", authRequired, controller.ConnectForwardSession)
		p2p.GET("/forward/list", controller.ListForwardSessions)
		p2p.POST("/forward/close", authRequired, controller.CloseForwardSession)
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

	// IPFS 路由（批2 精简：Bitswap 兼容层已删；保留 HTTP 网关 pin/查询）
	ipfs := r.Group("/ipfs")
	{
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

	// PeerJS 节点发现
	registerPeerJSRoutes(r, authRequired)

	// 统一 source 管理（source 体系管理面）
	registerSourceRoutes(r)

	// Count routes
	routes := r.Routes()
	log.LogInfo("router: SetupRouter completed with %d routes", len(routes))
	return r
}
