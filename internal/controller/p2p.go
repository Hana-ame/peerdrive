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

func GetNodeInfo(c *gin.Context) {
	id, addrs := p2pSvc.GetNodeInfo()
	c.JSON(http.StatusOK, gin.H{
		"peer_id": id.String(),
		"addrs":   addrs,
	})
}

func GetPeers(c *gin.Context) {
	peers := p2pSvc.GetConnectedPeers()
	strs := make([]string, len(peers))
	for i, p := range peers {
		strs[i] = p.String()
	}
	c.JSON(http.StatusOK, gin.H{"peers": strs})
}

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