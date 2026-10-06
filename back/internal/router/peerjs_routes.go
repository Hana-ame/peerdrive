package router

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	hashutil "peerdrive/pkg/hashutil"

	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// peerjsService injected by main, exposes the node's ID on the PeerJS signaling network for frontend discovery.
var peerjsService *transport.PeerJSService

// peerjsCfg Origin whitelist for WS local sessions (same config as HTTP CORS).
var peerjsCfg *config.Config

// nodeDirectory node marketplace directory (doc/NETDISK.md M1), injected by main.
var nodeDirectory *service.NodeDirectory

// SetPeerJSService injects PeerJS WebRTC service (nil skips node info routes).
func SetPeerJSService(svc *transport.PeerJSService) {
	peerjsService = svc
}

// SetNodeDirectory injects node marketplace directory (nil → /peerjs/nodes* returns 503).
func SetNodeDirectory(d *service.NodeDirectory) {
	nodeDirectory = d
	controller.InitNodeDirectory(d)
}

// SetNodeShare injects shared scope service (nil → /peerjs/share* returns 503).
// Shared scope is a runtime operator choice (admin console checkbox), persisted under storage.
func SetNodeShare(s *service.NodeShare) {
	controller.InitNodeShareController(s)
}

// SetPeerPuller injects cross-node pull service (nil → /p2p/pull* returns 503).
func SetPeerPuller(p *service.PeerPuller) {
	controller.InitPeerPuller(p)
}

// SetPeerJSConfig injects configuration (WS local session Origin whitelist).
func SetPeerJSConfig(cfg *config.Config) {
	peerjsCfg = cfg
}

// isLoopbackRemote checks if TCP peer is local (RemoteAddr like 127.0.0.1:54321 / [::1]:54321).
//
// Why only check RemoteAddr and not X-Forwarded-For: behind a reverse proxy, XFF
// trust is partly client-controlled (PEERDRIVE_TRUSTED_PROXIES), and this is a
// security boundary — better to be conservative. In reverse-proxy deployments,
// browser requests always carry an Origin header, so the whitelist path works fine.
func isLoopbackRemote(remote string) bool {
	host := remote
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i] // IPv4: strip port
	}
	host = strings.Trim(host, "[]") // IPv6: [::1] → ::1
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// registerPeerJSRoutes registers PeerJS node discovery and interconnection routes.
// Background: peerjsService is injected by main before SetupRouter (package-level
// variable, same pattern as SetRegServer); discovery endpoints serve frontend/MQTT
// room node ID resolution.
// auth parameter: SetupRouter's authRequired (auth middleware that passes through when
// no registration server is configured), used to protect write/fetch endpoints (F1/H4).
// Discovery and local WS sessions remain anonymous (/ws/peer frame protocol; fetch
// requests go through peerjs layer's own validation via req frames).
func registerPeerJSRoutes(r *gin.Engine, auth gin.HandlerFunc) {
	if peerjsService == nil {
		return
	}
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
		// signal_*: 本节点自己用的信令参数。面板拿到它就不必让用户手填
		// host/port/path/key —— 面板连的必须是**这个节点所在的信令**，
		// 否则会连到别处（实测填本机 3000 会得到 ws 404：那个端口是节点自己的
		// HTTP 服务，不是信令）。只读字段，不改任何行为。
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
			resp["signal_key"] = peerjsCfg.PeerJSKey
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
	r.POST("/peerjs/nodes/join", auth, controller.JoinNode)
	r.DELETE("/peerjs/nodes/join", auth, controller.LeaveNode)

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
	r.GET("/ws/peer", func(c *gin.Context) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// Local session security boundary: only allow configured Origins (same as HTTP CORS whitelist)
				origin := r.Header.Get("Origin")
				if origin == "" {
					// Most requests without Origin aren't from browsers (scripts / curl / wscat).
					// Previously all were allowed = if the port is reachable, full admin surface access,
					// and WSSession.IsLocal() is always true, so it was also treated as "self"
					// (private shared content was visible). Now only connections truly from localhost:
					// browser handshakes always carry Origin, so normal frontend is unaffected.
					return isLoopbackRemote(r.RemoteAddr)
				}
				if peerjsCfg == nil {
					return true
				}
				return peerjsCfg.IsOriginAllowed(origin)
			},
		}
		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "websocket upgrade failed"})
			return
		}
		sess := transport.NewWSSession("local", conn)
		peerjsService.BindLocal(sess)
	})
}
