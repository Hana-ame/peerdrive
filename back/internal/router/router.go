// Package router registers Gin routes to all Controller handlers.
// Assembled by serverapp: it constructs one Router from a Deps value (see deps.go)
// — `router.NewRouter(router.Deps{...})` then `rt.Engine()`. Dependencies used to
// arrive via package-level injectors (SetPeerJSService/SetSourceManager/…) that had
// to be called *before* SetupRouter; they are now constructor arguments, so there
// is no ordering contract to honour.
// Downloader is still constructed inside this package from cfg (no longer receives
// P2PService — libp2p stack fully removed on 2026-08-16, see doc/archive/LEGACY.md §A).
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
//   /ipfs/*            — IPFS gateway download (GET)
//   /iwara/video/:id   — iwara.tv video metadata + resolution list (GET, optional module)
//   /embedding/v1/*    — standalone EmbeddingGemma embeddings (POST embed/batch/ping, GET status; optional module, authRequired)
//   /peerjs/*          — PeerJS node discovery + /ws/peer local session
//   /swagger/*         — Swagger UI page (GET)

package router

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/log"
	"peerdrive/internal/panel"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// Engine builds this Router's gin engine and registers all routes (health check,
// file download, P2P, collections, WebDAV, signaling, etc.).
//
// It replaces the old package-level `SetupRouter(cfg)`, which read its
// dependencies out of package globals at registration time. Two consequences of
// the change worth knowing:
//
//   - Calling Engine twice on one Router panics (gin rejects duplicate route
//     registration). That is intentional: one Router owns one engine. Callers
//     that want two surfaces build two Routers with two Deps values.
//   - The engine is cached on the Router, so `Engine()` is idempotent in the sense
//     that later calls return the same engine rather than rebuilding it.
//
// corsAllowedHeaders is the explicit cross-origin header allow-list. Anything not
// listed is refused by the browser preflight. Keep this short: every entry is a
// header a cross-origin page may attach to this node (red-team R2).
const corsAllowedHeaders = "Authorization, Content-Type, Accept, X-Request-ID, X-Protocol, X-PSK"

// corsMiddleware builds the CORS handler for one Router.
//
// Extracted from Engine so the header policy is unit-testable (red-team R2).
func corsMiddleware(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		// Echoing Access-Control-Request-Headers back (the old behaviour) made every
		// header a browser-approved cross-origin header, so a malicious page could preflight
		// with Authorization + any custom header and, combined with the origin echo, turn a
		// leaked token into a remotely-usable one from any origin. Allow-list instead.
		c.Header("Access-Control-Allow-Headers", corsAllowedHeaders)
		c.Header("Access-Control-Max-Age", "86400")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Router.Engine builds the gin engine

func (rt *Router) Engine() *gin.Engine {
	if rt.engine != nil {
		return rt.engine
	}
	cfg := rt.cfg
	log.LogInfo("router: Engine starting")
	// Don't use gin.Default(): its built-in Logger produces human-readable
	// unstructured text and duplicates with AccessLog below. Explicit assembly here:
	// Recovery (outermost, recovers panics) + RequestID + SecurityHeaders +
	// structured access logging + rate limiting.
	// corsAllowedHeaders is the explicit cross-origin header allow-list. Anything not listed
	// is refused by the browser preflight. Keep this short: every entry is a header a
	// cross-origin page may attach to this node (red-team R2).
	const corsAllowedHeaders = "Authorization, Content-Type, Accept, X-Request-ID, X-Protocol, X-PSK"

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequestID(), SecurityHeaders(cfg.DisableCSP), AccessLog(),
		RateLimit(cfg.RateLimitRPS, 0), LimitJSONBody(cfg.MaxJSONBodyBytes))
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

	r.Use(corsMiddleware(cfg))

	// Auth middleware — validates Bearer tokens via registration server or local admin token.
	// Sets "authenticated" and "username" in Gin context for downstream handlers.
	// Anonymous requests (no token) pass through with authenticated=false.
	// Why always registered: with AdminToken there is a second, equally valid auth
	// backend — local comparison without a remote login service. Registering
	// unconditionally keeps the two paths symmetric and lets AuthRequired decide
	// pass-through via authDisabled().
	//
	// The backend choice itself is baked in at construction (NewRouter builds this
	// Router's Authenticator from cfg); the old code re-read cfg into package globals
	// here, which meant the last SetupRouter call in a process silently won.
	r.Use(rt.authOptional)
	// Attached to all mutating/admin routes: when no registration server is configured,
	// AuthRequired passes through internally (local single-machine mode); after configuration,
	// requires Bearer token (F1: previously AuthRequired had 0 call sites, all file
	// read/write/delete endpoints were anonymously accessible).
	authRequired := rt.authRequired
	// HTTP access model: HTTP is the node's own server, so its credential is the
	// node's PSK and a PSK holder gets FULL access — no share-scope check. The
	// share scope (public/unlisted/private + friends) is the P2P channel's model,
	// where the caller is an external peer. PeerPSK unset = gate off (historical
	// default; see PSKGate).
	pskGate := rt.pskGate

	syncCtrl := rt.initControllers()

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

	// iwara.tv metadata (front/iwara page). Optional module: only registered when
	// the operator enabled PEERDRIVE_IWARA_ENABLE, so a default node never
	// advertises a third-party proxy it is not configured for.
	//
	// Auth: like the other read-only endpoints (/collections GET, /shares GET),
	// no token is required — the request only resolves a download URL for the
	// caller, it does not read node files. The injected cookie is the node
	// owner's, so a public deployment should keep this behind the admin token or
	// a firewall rather than exposing the listen port.
	if rt.deps.IwaraClient != nil {
		controller.InitIwaraClient(rt.deps.IwaraClient)
		r.GET("/iwara/video/:id", controller.GetIwaraVideo)
	}

	// twitter-pic gallery (twitter-pic-go 数据面的 peerdrive 整合). Optional
	// module: only registered when the operator enabled PEERDRIVE_TWITTERPIC_ENABLE,
	// so a default node never advertises a third-party puller it is not
	// configured for (同 iwara/exhentai 的可选模块先例).
	//
	// Auth: 列表/读出/监视为只读端点，不加 token（与 /collections GET 一致）；
	// build 会写节点 storage（摄取媒体 + 集合落 sha-文件系统），挂 authRequired。
	if rt.deps.TwitterPic != nil {
		controller.InitTwitterPic(rt.deps.TwitterPic)
		xg := r.Group("/twitterpic")
		{
			xg.GET("/users", controller.TwitterPicUsers)
			xg.POST("/users/:username/build", authRequired, controller.TwitterPicBuild)
			xg.GET("/collections/:sha", controller.TwitterPicCollection)
			xg.GET("/fetch-stats", controller.TwitterPicFetchStats)
		}
	}

	// standalone EmbeddingGemma embeddings (internal/embedding). Optional module:
	// only registered when the operator enabled PEERDRIVE_EMBEDDING_ENABLE, so a
	// default node never advertises an endpoint that spends an operator's
	// inference compute (同 iwara/exhentai/twitterpic 的可选模块先例).
	//
	// Auth: every endpoint is authRequired. Embedding burns the compute of a
	// third-party inference process, so an anonymous caller reaching this route
	// could pin the operator's GPU with large requests.
	if rt.deps.EmbeddingClient != nil {
		controller.InitEmbeddingClient(rt.deps.EmbeddingClient)
		eg := r.Group("/embedding/v1")
		{
			eg.POST("/embed", authRequired, controller.EmbedText)
			eg.POST("/embed/batch", authRequired, controller.EmbedBatch)
			eg.POST("/ping", authRequired, controller.EmbedPing)
			eg.GET("/status", authRequired, controller.EmbedStatus)
		}
	}
	r.GET("/sha256sum/:sha256", pskGate, controller.DownloadBySHA256Local)
	r.HEAD("/sha256sum/:sha256", pskGate, controller.DownloadBySHA256Local)
	r.GET("/sha256sum/:sha256/:filename", pskGate, controller.DownloadBySHA256Local)
	r.HEAD("/sha256sum/:sha256/:filename", pskGate, controller.DownloadBySHA256Local)

	// Canonical self-contained content-addressed URLs for SHA256 files and Collections
	// (invariant #2, Issue #410 — restored after the 2026-10-17 excision d2d2830):
	//   GET/HEAD /f/:sha256[/:filename] — direct file access by SHA256 (with Range, ETag, ?meta=1)
	//   GET      /c/:hash[/*filepath]   — direct collection manifest or entry access by SHA256
	//
	// pskGate on the file outlets only. /c/ is deliberately NOT gated here: a
	// collection carries its own visibility/PSK/passcode policy (see
	// controller/anon.go GetAnonCollection), and adding the node-level PSK on
	// top would break collections shared between nodes whose operators never
	// exchanged a node PSK.
	r.GET("/f/:sha256", pskGate, controller.DirectFileBySHA256)
	r.HEAD("/f/:sha256", pskGate, controller.DirectFileBySHA256)
	r.GET("/f/:sha256/:filename", pskGate, controller.DirectFileBySHA256)
	r.HEAD("/f/:sha256/:filename", pskGate, controller.DirectFileBySHA256)
	r.GET("/c/:hash", controller.DirectCollectionByHash)
	r.GET("/c/:hash/*filepath", controller.DirectCollectionByHash)
	r.GET("/ipfs/:cid", pskGate, controller.DownloadByCID)

	// Universal multi-protocol download endpoints.
	r.GET("/download/:hash", pskGate, controller.UniversalDownload)
	r.GET("/download/:hash/sources", pskGate, controller.UniversalDownloadSources)
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
		// RESTful close aliases (Issue #354): same handler, target from the URL instead
		// of the JSON body. POST /forward/close keeps its original body contract.
		p2p.DELETE("/forward/listeners/:key", authRequired, controller.CloseForwardSession)
		p2p.DELETE("/forward/tunnels/:peer_id", authRequired, controller.CloseForwardSession)
		// Cross-node pull & save (cloud drive target M3): pull peer files/collections to this node's disk.
		// Read list is open (consistent with other status endpoints); start/cancel are write operations with auth.
		p2p.GET("/pull", controller.ListPullJobs)
		p2p.POST("/pull", authRequired, controller.StartPull)
		p2p.POST("/pull/collection", authRequired, controller.StartPullCollection)
		p2p.POST("/pull/cancel", authRequired, controller.CancelPull)

		// aria2c plugin integration routes (Issue #236)
		p2p.GET("/aria2/status", controller.GetAria2Status)
		p2p.POST("/aria2/toggle", authRequired, controller.SetAria2Enabled)
		p2p.POST("/aria2/download", authRequired, controller.Aria2AddURI)
	}

	// Remote screen display & synchronization routes (Issue #243)
	display := r.Group("/display")
	{
		display.GET("/screens", controller.ListDisplayScreens)
		display.GET("/status", controller.GetDisplayStatus)
		display.POST("/cast", authRequired, controller.CastDisplay)
		display.POST("/control", authRequired, controller.ControlDisplay)
		display.POST("/clear", authRequired, controller.ClearDisplay)
	}

	// P2P live stream distribution routes (Issue #244)
	stream := r.Group("/stream")
	{
		stream.GET("/list", controller.ListStreams)
		stream.GET("/:id/manifest", controller.GetStreamManifest)
		stream.POST("/create", authRequired, controller.CreateStream)
		stream.POST("/:id/chunk", authRequired, controller.PushStreamChunk)
		stream.POST("/:id/close", authRequired, controller.CloseStream)
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
		// Visibility & Permissions change: must go through authRequired
		anon.PUT("/collections/:hash/visibility", authRequired, controller.SetAnonCollectionVisibility)
		anon.PUT("/collections/:hash/permissions", authRequired, controller.SetAnonCollectionPermissions)
		// Entry CRUD & History (Issue #338)
		anon.POST("/collections/:hash/entries", authRequired, controller.AddAnonCollectionEntry)
		anon.DELETE("/collections/:hash/entries", authRequired, controller.RemoveAnonCollectionEntry)
		anon.PUT("/collections/:hash/entries", authRequired, controller.UpdateAnonCollectionEntry)
		anon.GET("/history/:hash", controller.GetAnonCollectionHistory)
	}

	// File management (browse/list/verify read-only open; write operations with auth).
	// pskGate is deliberately NOT attached to the /files group: it only guards the
	// file-BYTE outlets (/f/, /sha256sum/, /download/, /ipfs/). /files returns
	// index metadata — filenames and hashes — and a hash is useless without the
	// PSK that turns it into bytes, so the enumeration itself is not the leak.
	// Keeping it open also keeps the operator's directory browsing and
	// scripts/netdisk-traversal-probe.sh working.
	files := r.Group("/files")
	{
		files.GET("", controller.ListFiles)
		files.GET("/search", authRequired, controller.SearchLocalFiles)
		files.POST("/upload", authRequired, controller.UploadFile)
		files.POST("/register_local", authRequired, controller.RegisterLocalFile)
		files.POST("/register_url", authRequired, controller.RegisterURL)
		files.POST("/register_folder", authRequired, controller.RegisterFolder)
		files.GET("/verify/:hash", controller.VerifyFile)
		files.GET("/browse", controller.BrowseDir)
		files.DELETE("/:hash", authRequired, controller.DeleteFile)
		files.POST("/copy", authRequired, controller.CopyFile)
		files.POST("/diff", authRequired, controller.DiffVersions)
		files.GET("/inbox", authRequired, controller.ListInboxFiles)
		files.POST("/inbox/approve", authRequired, controller.ApproveInboxFile)
		// RESTful approve alias (Issue #353): hash from the URL instead of the JSON
		// body, symmetric with the DELETE /inbox/:hash reject route below.
		files.POST("/inbox/:hash/approve", authRequired, controller.ApproveInboxFile)
		files.DELETE("/inbox/:hash", authRequired, controller.RejectInboxFile)
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

	// Tags (Issue #91: tag / collection system extension)
	tagsGroup := r.Group("/tags")
	{
		tagsGroup.GET("/sha/:sha", controller.GetShaTags)
		tagsGroup.POST("/sha/:sha", authRequired, controller.SetShaTags)
		tagsGroup.POST("/batch", controller.BatchGetShaTags)
		tagsGroup.GET("/summary", controller.GetAllTagsSummary)
		tagsGroup.GET("", controller.GetAllTagsSummary)
		tagsGroup.GET("/search", controller.SearchTags)
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

	// 公共面板（internal/panel，内嵌单文件）：零学习成本的关键一环。
	//
	// 挂在 /panel 而不是 "/"："/" 同时是所有未匹配路由的兜底（gin 的 404/redirect），
	// 抢过来会让本来该 404 的地址变成 200 HTML，看起来像 API 在返回页面。
	//
	// 不受 PSK 门禁：面板是「打开就能用」的入口，拦在门外等于取消零学习成本。
	// 它只读节点清单，真正的数据读取仍走既有的鉴权路径。
	// 用 panel.Handler 而不是 panel.HTML()：前者会注入「我已经知道该连谁」的
	// 前置脚本（同源 → 反查本节点 ID → 自动连），后者是原始文件。
	// 直接吐原始文件的话用户打开面板还得手填 node/host/port，不算零学习成本。
	//
	// ⚠️ 只能注册精确路径，不能加 /panel/*any：gin 的路由树里已有 "/" 前缀，
	// 再挂 catch-all 通配会直接 panic
	//   catch-all wildcard '*any' in new path '/panel/*any' conflicts with existing path segment ''
	// 面板没有子资源，精确匹配 /panel 与 /panel/ 两个形态就够。
	//
	// 审计 R2 HIGH：/peerjs/node 不再返回 signal_key，面板 bootstrap 改为
	// 从服务端注入 key（panel.SetSignalKey）。必须在注册 Handler 之前设置。
	panel.SetSignalKey(cfg.PeerJSKey)
	panelMux := http.NewServeMux()
	panelMux.Handle("/panel", panel.Handler("/panel"))
	panelMux.Handle("/panel/", panel.Handler("/panel"))
	r.GET("/panel", gin.WrapH(panelMux))
	r.GET("/panel/", gin.WrapH(panelMux))

	// 面板的同目录 peerjs.min.js 副本：让它「优先加载同目录」这一环恒定命中，
	// 不必每次都去 CDN（内网/墙/慢网下 CDN 会表现为「面板连不上」，
	// e2e.yml:120-140 记录的 known-flaky 就是这个根因）。
	r.GET("/peerjs.min.js", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", panel.PeerJSJS())
	})

	// PeerJS node discovery
	rt.registerPeerJSRoutes(r, authRequired)

	// admin management surface internal forwarding (transport/admin.go): browser sends admin
	// frames via /ws/peer → wrapped here as gin engine to reuse all HTTP controllers (zero
	// duplicate implementation).
	// Why: the original frame protocol verbs only cover the file data plane (req/upload/index);
	// collection/auth/BT/IPFS/task etc. admin surfaces would be massive duplicate work if each
	// got a verb, and WebRTC connections don't handle admin (serveAdmin rejects by session ID),
	// admin surface is only exposed to local WS.
	// Frontend api.js no longer directly fetches HTTP after migration, all goes through /ws/peer admin frames.
	// Support /api/v1 prefix across all HTTP routes and WS admin forwarding (#378 A1):
	// Requests arriving as /api/v1/<path> are transparently dispatched to /<path> on the engine.
	r.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/") || c.Request.URL.Path == "/api/v1" {
			trimmed := strings.TrimPrefix(c.Request.URL.Path, "/api/v1")
			if trimmed == "" {
				trimmed = "/"
			}
			c.Request.URL.Path = trimmed
			c.Request.URL.RawPath = ""
			r.HandleContext(c)
			return
		}
		c.String(http.StatusNotFound, "404 page not found")
	})

	if peerjsSvc := rt.deps.PeerJSService; peerjsSvc != nil {
		peerjsSvc.SetAdminHandler(func(req *http.Request) (int, []byte, string, error) {
			if req.RemoteAddr == "" {
				req.RemoteAddr = "127.0.0.1:0"
			}
			if strings.HasPrefix(req.URL.Path, "/api/v1/") {
				req.URL.Path = strings.TrimPrefix(req.URL.Path, "/api/v1")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			return rec.Code, rec.Body.Bytes(), rec.Header().Get("Content-Type"), nil
		})
	}

	// Unified source management (source system admin surface)
	rt.registerSourceRoutes(r, authRequired)

	// Count routes
	routes := r.Routes()
	log.LogInfo("router: Engine completed with %d routes", len(routes))
	rt.engine = r
	return r
}
