// P2P 控制器 — libp2p 节点信息、已连接对等节点列表、Ping 测速。
// 先调用 InitP2PController(svc) 注册 service.P2PService 实例。
// 技术实现：通过 libp2p host.Network().Peers() 获取连接；通过
//   ping.PingService 发送协议 Ping 并测量 RTT。
// 路由：
//   GET /p2p/node      — 本节点 PeerID 和监听 multiaddr
//   GET /p2p/peers     — 已连接的对等节点 PeerID 列表
//   GET /p2p/ping/:id  — 向指定 PeerID 发送 Ping 并返回 RTT

package controller

import (
	"net/http"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-libp2p/core/peer"
)

var p2pSvc *service.P2PService

func InitP2PController(svc *service.P2PService) {
	p2pSvc = svc
}

// GetNodeInfo godoc
// @Summary Get P2P node info
// @Description Returns the local node's PeerID and listening multiaddrs
// @Tags p2p
// @Produce json
// @Success 200 {object} map[string]interface{} "peer_id and addrs"
// @Router /p2p/node [get]
func GetNodeInfo(c *gin.Context) {
	id, addrs := p2pSvc.GetNodeInfo()
	c.JSON(http.StatusOK, gin.H{
		"peer_id": id.String(),
		"addrs":   addrs,
	})
}

// GetPeers godoc
// @Summary List connected peers
// @Description Returns the list of PeerIDs currently connected to this node
// @Tags p2p
// @Produce json
// @Success 200 {object} map[string]interface{} "peers array"
// @Router /p2p/peers [get]
func GetPeers(c *gin.Context) {
	peers := p2pSvc.GetConnectedPeers()
	strs := make([]string, len(peers))
	for i, p := range peers {
		strs[i] = p.String()
	}
	c.JSON(http.StatusOK, gin.H{"peers": strs})
}

// PingPeer godoc
// @Summary Ping a P2P peer
// @Description Send a ping to a peer and measure round-trip time
// @Tags p2p
// @Produce json
// @Param peer_id path string true "Peer ID (e.g. 12D3KooW...)"
// @Success 200 {object} map[string]interface{} "peer and rtt"
// @Failure 400 {object} map[string]string "Invalid peer ID"
// @Failure 500 {object} map[string]string "Ping error"
// @Router /p2p/ping/{peer_id} [get]
func PingPeer(c *gin.Context) {
	raw := c.Param("peer_id")
	pid, err := peer.Decode(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid peer id"})
		return
	}
	rtt, err := p2pSvc.PingPeer(c.Request.Context(), pid)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"peer": raw, "rtt": rtt.String()})
}
