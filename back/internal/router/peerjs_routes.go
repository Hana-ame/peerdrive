package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	hashutil "peerdrive/pkg/hashutil"

	"peerdrive/internal/controller"
	"peerdrive/internal/transport"
	"peerdrive/internal/wsconn"
)

// injectControllerDeps hands the PeerJS-backed dependencies to the controller
// package, which still carries its own package-level Init* state (a separate
// refactor; see NewRouter's scope note).
//
// Why it is a single method rather than four exported setters: these are only
// ever meaningful together, as one Router's dependency set. Keeping them
// together makes it obvious that they come from Deps and not from the caller,
// and leaves nothing for a caller to wire up in the wrong order.
//
// Note the ordering constraint this replaces: the old code documented "SetNodeDirectory
// must be called before SetupRouter (read during route registration)". Now the
// dependency set is a value handed to NewRouter, so there is no window in which
// the controller can observe a half-assembled router.
func (r *Router) injectControllerDeps() {
	controller.InitForwardController(r.deps.PeerJSService)
	// Peer node share list query (cloud drive target M2): /peerjs/nodes/:peer/shares
	// has controller call transport.RequestShares directly (requester of share frames).
	controller.InitPeerShareController(r.deps.PeerJSService)
	controller.InitNodeDirectory(r.deps.NodeDirectory)
	// Shared scope is a runtime operator choice (admin console checkbox), persisted
	// under storage — so the very same instance must reach both the controller
	// (for /peerjs/share*) and the transport (for `share` frames).
	controller.InitNodeShareController(r.deps.NodeShare)
	controller.InitPeerPuller(r.deps.PeerPuller)
	controller.InitAria2Bridge(r.deps.Aria2Bridge)
	controller.InitDisplayController(r.deps.PeerJSService)
	// File index search: /peerjs/files/search (local) + /peerjs/nodes/:peer/search (remote).
	// PeerJSService itself implements the three methods of the controller's narrow
	// fileIndexSearcher interface (Search / RequestSearch / ConnectedPeerIDs), so the
	// injection is a direct pass-through with no adapter layer.
	controller.InitFileSearchController(r.deps.PeerJSService)
}

// registerPeerJSRoutes registers PeerJS node discovery and interconnection routes.
// Background: PeerJSService comes from this Router's Deps (constructor injection —
// previously it was a package-level global written by SetPeerJSService before
// SetupRouter, which made registration depend on call order).
// auth parameter: the Router's authRequired (auth middleware that passes through when
// no registration server is configured), used to protect write/fetch endpoints (F1/H4).
// Discovery and local WS sessions remain anonymous (/ws/peer frame protocol; fetch
// requests go through peerjs layer's own validation via req frames).
//
// The deps are captured into locals at the top: the handlers below run long after
// this function returns, and reading them off r.deps each time would be both a
// wider access surface and an unnecessary indirection per request.
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
		// psk: whether this node has pre-shared key gate enabled (empty = open, serve anyone who connects);
		// psk_peers: current number of connections that have passed the gate. For admin console / debugging —
		// "can't fetch from peer" first suspect is that they didn't present a key.
		pskEnabled, pskOK := peerjsService.PSKState()
		// signal_*: 本节点自己用的信令参数（host/port/path/secure）。
		// 面板拿到它就不必让用户手填 host/port/path —— 面板连的必须是
		// **这个节点所在的信令**，否则会连到别处（实测填本机 3000 会得到
		// ws 404：那个端口是节点自己的 HTTP 服务，不是信令）。
		//
		// ⚠️ signal_key 已移除（审计 R2 HIGH，2026-10-08）：
		// 信令 key 是注册任意 peer id 的完整凭据——任何匿名客户端读一次
		// /peerjs/node 就拿到了，与 R1 对 signalserver /status 移除 key 的
		// 修复逻辑一致。面板需要它时由面板服务端注入（panel.SetSignalKey），
		// 不走这个匿名端点。
		resp := gin.H{
			"id":        peerjsService.ID(),
			"online":    true,
			"peers":     peers,
			"psk":       pskEnabled,
			"psk_peers": pskOK,
		}
		if peerjsCfg != nil {
			resp["signal_host"] = peerjsCfg.PeerJSHost
			resp["signal_port"] = peerjsCfg.PeerJSPort
			resp["signal_path"] = "/"
			resp["signal_secure"] = peerjsCfg.PeerJSSecure
		}
		c.JSON(http.StatusOK, resp)
	})

	// ── Node marketplace (doc/NETDISK.md M1) ──
	// Listing is read-only open (same level as /peerjs/node, "discovery" info); join/leave are write operations
	// → attached with auth (AuthRequired passes through internally when no registration server = single-machine mode).
	// Semantics: marketplace = online nodes from discovery server ∪ locally joined list (kept offline too).
	r.GET("/peerjs/nodes", controller.GetNodeMarket)
	r.GET("/peerjs/nodes/joined", controller.GetJoinedNodes)
	r.GET("/peerjs/nodes/:peer/shares", controller.GetPeerShares)
	// ── File index search (feat/file-index-search) ──
	// /peerjs/nodes/:peer/search: ask a peer about its index. Auth required: unlike
	// /peerjs/nodes/:peer/shares (returns only the explicitly shared scope), search
	// returns hits from the peer's **full local index** — strictly more sensitive
	// information, so gate it at the same level as /peerjs/share (when no
	// registration server is configured, auth passes through internally = single-machine mode).
	r.GET("/peerjs/nodes/:peer/search", auth, controller.SearchPeerFiles)
	r.POST("/peerjs/nodes/join", auth, controller.JoinNode)
	r.DELETE("/peerjs/nodes/join", auth, controller.LeaveNode)

	// ── Peer blocklist & admission control (Issue #89) ──
	r.GET("/peerjs/blocklist", controller.GetPeerBlocklist)
	r.POST("/peerjs/blocklist", auth, controller.PostPeerBlocklist)
	r.DELETE("/peerjs/blocklist/:peer", auth, controller.DeletePeerBlocklist)

	// GET /peerjs/files/search: search **this node's** index (admin panel's find-a-file box).
	// Also auth-attached: response contains local file names/paths/sizes, same category as
	// GET /peerjs/share. This endpoint does not depend on /peerjs being enabled on the
	// remote side — it reads the local SQLite, so it makes more sense to register it in
	// the /files group; it lives under /peerjs only because FileIndexService belongs to
	// that service set, and moving it later is a one-line change.
	r.GET("/peerjs/files/search", auth, controller.SearchLocalFiles)

	// ── This node's shared scope (doc/NETDISK.md M2.6) ──
	// Read also has auth: response contains local file names/sizes, which is admin surface info;
	// when no registration server is configured, AuthRequired passes through internally (single-machine mode).
	// Deliberately named differently from /peerjs/nodes/:peer/shares which queries the peer's share list.
	r.GET("/peerjs/share", auth, controller.GetNodeShare)
	r.PUT("/peerjs/share", auth, controller.PutNodeShare)
	r.POST("/peerjs/share/files", auth, controller.PostNodeShareFiles)

	// POST /peerjs/fetch {peer, hash, offset?, size?} fetch sha256 content from peer node
	// H4: this endpoint holds the entire response buffer in memory (service's 8GB cap only prevents
	// overflow, not slow-read clients holding memory for extended periods). Tightened: auth required
	// + single fetch limit 64MB; large files should use /ws/peer chunked transfer.
	// Frontend doesn't use this endpoint (integration tests call service.FetchFromPeer directly),
	// no compatibility impact from tightening.
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
		const maxPeerjsHTTPFetch = 64 << 20
		if body.Size <= 0 {
			body.Size = -1 // full file → capped by service side, but HTTP endpoint needs another limit
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
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		// Defense: service already rejects when peer declares file larger than requested size;
		// here as backstop to prevent memory overflow
		if int64(len(data)) > maxPeerjsHTTPFetch {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "peer returned oversized data"})
			return
		}
		c.Data(http.StatusOK, "application/octet-stream", data)
	})

	// GET /ws/peer local WebSocket session: frame protocol is identical to remote DataChannel
	// (text frame = JSON control header, binary frame = data block); browser local connection
	// needs no hole-punching/signaling.
	// Note: different from legacy /ws/signal, /ws/transfer (old self-built signaling).
	//
	// The origin decision is wsconn.OriginPolicy; it is built once here rather than
	// per request because peerjsCfg is already captured as a local above. A nil
	// peerjsCfg means "no allowlist configured" and is expressed as a nil Allow
	// (accept) — IsOriginAllowed dereferences the config, so it must not be
	// reachable through a nil pointer.
	var originAllow func(string) bool
	if peerjsCfg != nil {
		originAllow = peerjsCfg.IsOriginAllowed
	}
	upgrader := wsconn.NewUpgrader(wsconn.OriginPolicy{Allow: originAllow})
	r.GET("/ws/peer", func(c *gin.Context) {
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
	})
}
