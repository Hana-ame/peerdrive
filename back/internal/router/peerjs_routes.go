package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	hashutil "peerdrive/pkg/hashutil"

	"peerdrive/internal/config"
	"peerdrive/internal/service"
)

// peerjsService 由 main 注入，暴露节点在 PeerJS 信令网络中的 ID 供前端发现。
var peerjsService *service.PeerJSService

// peerjsCfg WS 本地会话的 Origin 白名单（与 HTTP CORS 同一配置）。
var peerjsCfg *config.Config

// SetPeerJSService 注入 PeerJS WebRTC 服务（nil 则跳过节点信息路由）。
func SetPeerJSService(svc *service.PeerJSService) {
	peerjsService = svc
}

// SetPeerJSConfig 注入配置（WS 本地会话 Origin 白名单）。
func SetPeerJSConfig(cfg *config.Config) {
	peerjsCfg = cfg
}

// registerPeerJSRoutes 注册 PeerJS 节点发现与互联路由。
// 背景：peerjsService 由 main 在 SetupRouter 之前注入（包级变量，与
// SetRegServer 同模式）；发现端点供前端/MQTT 房间解析节点 ID。
// auth 参数：SetupRouter 的 authRequired（无注册服务器时放行的认证中间件），
// 用于保护写/拉取端点（F1/H4）。发现与本地 WS 会话保持匿名（/ws/peer 起帧协议，
// 拉取经 req 帧 peerjs 层自行校验）。
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
		c.JSON(http.StatusOK, gin.H{
			"id":     peerjsService.ID(),
			"online": true,
			"peers":  peers,
		})
	})

	// POST /peerjs/fetch {peer, hash, offset?, size?} 从对端节点拉取 sha256 内容
	// H4：此端点把整个响应 buffer 驻留内存（service 内 8GB cap 只防溢出，不防慢读客户端
	// 长时间持有内存）。收紧：挂认证 + 单次拉取上限 64MB；超大文件应走 /ws/peer 分片。
	// 前端未使用此端点（集成测试直接调 service.FetchFromPeer），收紧无兼容影响。
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
			body.Size = -1 // 全文件 → 由 service 侧 cap，但 HTTP 端点还要再限一次
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
		// 防御：对端声明的文件比请求 size 大时 service 已拒绝；这里兜底防内存超限
		if int64(len(data)) > maxPeerjsHTTPFetch {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "peer returned oversized data"})
			return
		}
		c.Data(http.StatusOK, "application/octet-stream", data)
	})

	// GET /ws/peer 本地 WebSocket 会话：帧协议与远端 DataChannel 完全一致
	// （文本帧=JSON 控制头，二进制帧=数据块），浏览器本地直连无需打洞/信令。
	// 注意与旧 /ws/signal、/ws/transfer（legacy 自建信令）不是一回事。
	r.GET("/ws/peer", func(c *gin.Context) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				// 本地会话安全边界：仅放行配置的 Origin（同 HTTP CORS 白名单）
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
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
		sess := service.NewWSSession("local", conn)
		peerjsService.BindLocal(sess)
	})
}
