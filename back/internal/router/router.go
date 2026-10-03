// Package router registers Gin routes to all Controller handlers.
// Called by cmd/server/main.go (only passes cfg); PeerJS/source and other service
// instances are assembled by main via package-level injectors like SetPeerJSService/
// SetSourceManager before calling SetupRouter; Downloader is constructed inside this
// function based on cfg (no longer receives P2PService — libp2p stack fully removed
// on 2026-08-16, see doc/archive/LEGACY.md §A).
// Route groups:
//   /ping              — health check (GET)
//   /sha256sum/:sha256 — download file by SHA256 hash (local storage only, no P2P fallback)
//   /p2p/*             — port forwarding v2 + auth status + WebRTC info (GET/POST)
//   /anon/*            — anonymous collection create/read/fork (POST/GET)
//   /files/*           — file upload/register/verify/delete/version diff (POST/POST/POST/GET/DELETE)
//   /collections/*     — collection CRUD + entry management + version control (POST/GET)
//   /local/*           — local sync state management (POST/GET)
//   /actions/*         — merge/fork (POST; /actions/pull removed with TaskService 2026-08-19)
//   /:user/:coll/*     — download files from collection entries (GET)
//   /collections/search — public collection search (GET)
//   /peerjs/*          — PeerJS node discovery + /ws/peer local session
//   /swagger/*         — Swagger UI page (GET)

package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/downloader"
	"peerdrive/internal/log"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/source"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// SetupRouter creates a Gin engine and registers all routes (health check, file download, P2P, collections, WebDAV, signaling, etc.).
func SetupRouter(cfg *config.Config) *gin.Engine {
	log.LogInfo("router: SetupRouter starting")
	// Don't use gin.Default(): its built-in Logger produces human-readable
	// unstructured text and duplicates with AccessLog below. Explicit assembly here:
	// Recovery (outermost, recovers panics) + RequestID + SecurityHeaders +
	// structured access logging + rate limiting.
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestID(), SecurityHeaders(cfg.DisableCSP), AccessLog(), RateLimit(cfg.RateLimitRPS, 0))
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false

	// Trusted proxies (see config.TrustedProxies comments): default trusts none,
	// ClientIP() uses RemoteAddr directly. Without configuring this behind a reverse
	// proxy, everyone is counted as the same source and rate-limited together.
	if tp := strings.TrimSpace(cfg.TrustedProxies); tp != "" {
		if tp == "all" {
			_ = r.SetTrustedProxies([]string{"0.0.0.0/0"})
		} else {
			list := []string{}
			for _, p := range strings.Split(tp, ",") {
				if s := strings.TrimSpace(p); s != "" {
					list = append(list, s)
				}
			}
			if err := r.SetTrustedProxies(list); err != nil {
				log.LogWarn("router: bad PEERDRIVE_TRUSTED_PROXIES: %v", err)
			}
		}
	} else {
		_ = r.SetTrustedProxies(nil)
	}

	// inject shared deps into context (must register before any routes)
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", cfg.StorageDir)
		c.Next()
	})

	r.Use(func(c *gin.Context) {
		// L8: original implementation returned `Allow-Origin: *` + `Allow-Credentials: true`
		// even for non-whitelisted Origins — malicious web pages (without credentials) could
		// still cross-origin read local API responses, making the whitelist useless.
		// Now: only whitelisted Origins get origin echoed back (+credentials is then legal);
		// others don't get Allow-Origin set (browser blocks reading the response). No Origin
		// (same-origin/curl) doesn't get CORS headers — same-origin requests don't need CORS
		// authorization anyway.
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
	// Attached to all mutating/admin routes: when no registration server is configured,
	// AuthRequired passes through internally (local single-machine mode); after configuration,
	// requires Bearer token (F1: previously AuthRequired had 0 call sites, all file
	// read/write/delete endpoints were anonymously accessible).
	authRequired := AuthRequired()

	fileSvc := service.NewFileService(cfg)
	controller.InitFileController(fileSvc)
	// Probe: inject "can the database connect" as a dependency (controller doesn't touch repository directly).
	controller.InitHealth(repository.Ping)
	// M2 layering assembly: collection/share/pin services injected into controller (replaces previous direct repository calls)
	controller.InitCollectionController(service.NewCollectionService())
	controller.InitShareController(service.NewShareService())
	controller.InitPinController(service.NewPinService())

	// Port forwarding service (PeerJS DataChannel version, forward.go): rule table injected
	// by main during assembly via SetForwardRules (config PEERDRIVE_FORWARD_RULES), runtime
	// endpoints can dynamically add rules.
	controller.InitForwardController(peerjsService)
	// Peer node share list query (cloud drive target M2): /peerjs/nodes/:peer/shares
	// has controller call transport.RequestShares directly (requester of share frames).
	controller.InitPeerShareController(peerjsService)

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
	if sourceManager != nil && btClient != nil {
		sourceManager.SetBTControl(source.NewBTControl(btClient))
		log.LogInfo("router: BT control injected")
	}
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
					// M2 layering: registration logic consolidated into FileService.RegisterBTFile (previously inline DB write)
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
			if sourceManager != nil && ipfsProv != nil {
				sourceManager.SetIPFSControl(source.NewIPFSControl(ipfsProv, cfg.StorageDir))
				log.LogInfo("router: IPFS control injected")
			}
		}
	}
	controller.InitIPFSProvider(ipfsProv)

	// Initialize the universal multi-protocol downloader.
	downloadTimeout := time.Duration(cfg.DownloadTimeoutSecs) * time.Second
	uniDownloader := downloader.NewUniversalDownloader(
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

	// ── LEGACY HTTP route area (original paths preserved, marked with comments) ──
	// Background: frontend has fully migrated to /ws/peer admin frames (transport/admin.go
	// internally forwards to this engine, covering all controllers below). These HTTP
	// endpoints keep their original paths and continue working: ① compatibility with
	// old frontend/curl/external scripts; ② integration tests go through HTTP directly.
	// New frontend code is prohibited from directly fetching these endpoints (except /ws/peer upgrade).
	// Migration date: 2026-08-17 (completed after frontend api.js switched to ws.js client).
	//
	// Note: admin internal forwarding reuses this engine, so these routes serve both
	// "browser admin frames" and "direct HTTP calls" — consistent behavior, no need
	// to maintain two copies.

	r.GET("/ping", controller.Ping)
	// Liveness / readiness probes (see controller/health.go for the semantic difference between the two)
	r.GET("/health", controller.Health)
	r.GET("/ready", controller.Ready)
	r.GET("/sha256sum/:sha256", controller.DownloadBySHA256Local)
	r.GET("/sha256sum/:sha256/:filename", controller.DownloadBySHA256Local)
	r.GET("/ipfs/:cid", controller.DownloadByCID)

	// Universal multi-protocol download endpoints.
	r.GET("/download/:hash", controller.UniversalDownload)
	r.GET("/download/:hash/sources", controller.UniversalDownloadSources)
	r.POST("/download/:hash/refresh", authRequired, controller.UniversalDownloadRefresh)

	// P2P routes (batch 2 simplification: libp2p legacy stack endpoints removed, kept auth status/WebRTC info/port forwarding)
	p2p := r.Group("/p2p")
	{
		p2p.GET("/auth/status", controller.AuthStatus)
		p2p.GET("/webrtc/info", controller.WebRTCInfoHandler(cfg))
		// Port forwarding routes (forward v2, PeerJS DataChannel)
		p2p.POST("/forward/create", authRequired, controller.CreateForwardSession)
		p2p.POST("/forward/connect", authRequired, controller.ConnectForwardSession)
		p2p.GET("/forward/list", controller.ListForwardSessions)
		p2p.POST("/forward/close", authRequired, controller.CloseForwardSession)
		// Cross-node pull & save (cloud drive target M3): pull peer files/collections to this node's disk.
		// Read list is open (consistent with other status endpoints); start/cancel are write operations with auth.
		p2p.GET("/pull", controller.ListPullJobs)
		p2p.POST("/pull", authRequired, controller.StartPull)
		p2p.POST("/pull/collection", authRequired, controller.StartPullCollection)
		p2p.POST("/pull/cancel", authRequired, controller.CancelPull)
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

	// IPFS routes (batch 2 simplification: Bitswap compatibility layer removed; kept HTTP gateway pin/query)
	ipfs := r.Group("/ipfs")
	{
		// IPFS pin routes
		ipfs.POST("/pin/:cid", authRequired, controller.PinCID)
		ipfs.DELETE("/pin/:cid", authRequired, controller.UnpinCID)
		ipfs.GET("/pins", controller.ListPins)
		// IPFS gateway status
		ipfs.GET("/gateways", controller.IPFSGatewayStatus)
	}

	// ── Unified Collection routes (new code uses these) ──
	// Background: original code had legacy redirects (/anon/*, /actions/*) with duplicate
	// real route registration — gin panics on startup ("handlers are already registered"),
	// service can't start at all.
	// Fix: removed all redirects (frontend already uses new paths directly), conflicting
	// routes merged into dispatchers (dispatchCreateCollection / dispatchGetCollection / dispatchGetTree).
	coll := r.Group("/collections")
	{
		// POST /collections dispatch: body with username goes user system, otherwise anonymous collection
		coll.POST("", authRequired, dispatchCreateCollection)
		coll.GET("", controller.ListAnonCollections)
		// GET /collections/:id dispatch: 64-char hex is anonymous collection hash, otherwise list by username
		coll.GET("/:id", dispatchGetCollection)
		// GET /collections/:id/*filepath dispatch anon file download and user collection sub-routes
		// (gin doesn't allow :param and *wildcard coexistence, unified through dispatcher)
		coll.GET("/:id/*filepath", dispatchGetTree)
		coll.POST("/fork", authRequired, controller.ForkAnonCollection)
		coll.POST("/merge", authRequired, controller.MergeFromSource)
		coll.POST("/upload", authRequired, controller.UploadFile)
		coll.POST("/register-local", authRequired, controller.RegisterLocalFile)
		coll.POST("/register-url", authRequired, controller.RegisterURL)
		coll.POST("/register-folder", authRequired, controller.RegisterFolder)
	}

	// Anonymous Collection routes (public read; create/commit/fork are write operations with auth)
	anon := r.Group("/anon")
	{
		anon.POST("/collections", authRequired, controller.CreateAnonCollection)
		anon.GET("/collections", controller.ListAnonCollections)
		anon.POST("/collections/commit", authRequired, controller.CommitAnonCollection)
		anon.GET("/collections/:hash", controller.GetAnonCollection)
		anon.GET("/collections/:hash/*filepath", controller.DownloadAnonFile)
		anon.POST("/collections/fork", authRequired, controller.ForkAnonCollection)
		// Visibility change: must go through authRequired (bare PUT would let anyone change anyone's visibility)
		anon.PUT("/collections/:hash/visibility", authRequired, controller.SetAnonCollectionVisibility)
	}

	// File management (browse/list/verify read-only open; write operations with auth)
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

	// Collection management (write operations with auth)
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

	// Local sync (write with auth, read open)
	sync := r.Group("/local")
	{
		sync.POST("/save", authRequired, syncCtrl.SaveLocal)
		sync.GET("/status/:hash", syncCtrl.GetStatus)
	}
	// Backward-compatible redirects: /actions/* → /collections/*
	// (removed: conflicts with real routes; frontend already uses /collections/fork|merge|pull directly)

	// Collaboration actions
	actions := r.Group("/actions")
	{
		actions.POST("/merge", authRequired, controller.MergeFromSource)
		actions.POST("/fork", authRequired, controller.ForkCollection)
	}

	// Public collection file download
	r.GET("/:username/:collection_name/*filepath", controller.DownloadCollectionFile)

	// Share links (create with auth; read by token public)
	shares := r.Group("/shares")
	{
		shares.POST("", authRequired, controller.CreateShare)
		shares.GET("", authRequired, controller.ListShares)
	}
	r.GET("/s/:token", controller.AccessShare)

	// Swagger: enabled by default. It publishes all endpoints (105) with parameter structures,
	// which is essentially giving away a free attack map for public deployments —
	// production recommends PEERDRIVE_SWAGGER=off.
	if !cfg.DisableSwagger {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	} else {
		log.LogInfo("router: swagger disabled by PEERDRIVE_SWAGGER=off")
	}

	// PeerJS node discovery
	registerPeerJSRoutes(r, authRequired)

	// admin management surface internal forwarding (transport/admin.go): browser sends admin
	// frames via /ws/peer → wrapped here as gin engine to reuse all HTTP controllers (zero
	// duplicate implementation).
	// Why: the original frame protocol verbs only cover the file data plane (req/upload/index);
	// collection/auth/BT/IPFS/task etc. admin surfaces would be massive duplicate work if each
	// got a verb, and WebRTC connections don't handle admin (serveAdmin rejects by session ID),
	// admin surface is only exposed to local WS.
	// Frontend api.js no longer directly fetches HTTP after migration, all goes through /ws/peer admin frames.
	if peerjsService != nil {
		peerjsService.SetAdminHandler(func(req *http.Request) (int, []byte, string, error) {
			// Internally forwarded requests have no TCP source (they come from an established local WS session);
			// without setting RemoteAddr, ClientIP() is an empty string, and rate limiting would bucket
			// all admin requests into the same "unknown source" bucket — admin console 429 after a few clicks.
			// Tagging as localhost is semantically correct: admin surface already only serves local WS
			// (serveAdmin rejects remote by session ID).
			if req.RemoteAddr == "" {
				req.RemoteAddr = "127.0.0.1:0"
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec.Code, rec.Body.Bytes(), rec.Header().Get("Content-Type"), nil
		})
	}

	// Unified source management (source system admin surface)
	registerSourceRoutes(r, authRequired)

	// Count routes
	routes := r.Routes()
	log.LogInfo("router: SetupRouter completed with %d routes", len(routes))
	return r
}
