package router

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

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
func registerPeerJSRoutes(r *gin.Engine) {
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
	r.POST("/peerjs/fetch", func(c *gin.Context) {
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
		if body.Size == 0 {
			body.Size = -1
		}
		data, err := peerjsService.FetchFromPeer(body.Peer, body.Hash, body.Offset, body.Size)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
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
