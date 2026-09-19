// node_market.go：节点市场端点（doc/NETDISK.md M1）。
//
// 语义与「自己的节点/别人的节点」对齐：
//   - GET    /peerjs/nodes            市场列表（在线 ∪ 已加入），含本节点自身条目
//   - GET    /peerjs/nodes/joined     我加入的节点
//   - POST   /peerjs/nodes/join       加入节点（落盘 + 立即拨号）
//   - DELETE /peerjs/nodes/join?peer= 移出（不断开在途连接，见 service 注释）
//
// 与 /peerjs/node（单节点自身信息）刻意分开命名，避免与旧端点语义混淆。
package controller

import (
	"context"
	"net/http"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// nodeDir 由 main 注入（同 InitForwardController 模式）。
var nodeDir *service.NodeDirectory

// InitNodeDirectory 注入节点市场目录服务（nil = 该组端点不可用）。
func InitNodeDirectory(d *service.NodeDirectory) {
	log.LogDebug("ctrl-node-market: InitNodeDirectory")
	nodeDir = d
}

// marketTimeout 发现服务器查询超时：市场列表是交互式请求，不能让前端
// 长时间挂在"加载中"（发现服务器不可达时按空列表渲染已加入节点）。
const marketTimeout = 6 * time.Second

// GetNodeMarket 处理 GET /peerjs/nodes。
func GetNodeMarket(c *gin.Context) {
	if nodeDir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node directory not enabled"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), marketTimeout)
	defer cancel()
	nodes := nodeDir.Market(ctx)
	if nodes == nil {
		nodes = []model.NodeSummary{} // 保持 JSON 为 []（前端不必判 null）
	}
	c.JSON(http.StatusOK, gin.H{
		"self":  nodeDir.Self(ctx),
		"nodes": nodes,
	})
}

// GetJoinedNodes 处理 GET /peerjs/nodes/joined。
func GetJoinedNodes(c *gin.Context) {
	if nodeDir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node directory not enabled"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), marketTimeout)
	defer cancel()
	c.JSON(http.StatusOK, gin.H{"nodes": nodeDir.Joined(ctx)})
}

// JoinNode 处理 POST /peerjs/nodes/join {peer}。
func JoinNode(c *gin.Context) {
	if nodeDir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node directory not enabled"})
		return
	}
	var req struct {
		Peer string `json:"peer"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := nodeDir.Join(req.Peer); err != nil {
		log.LogWarn("ctrl-node-market: join %s failed: %v", req.Peer, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "joined", "peer": req.Peer})
}

// LeaveNode 处理 DELETE /peerjs/nodes/join?peer=<id>。
func LeaveNode(c *gin.Context) {
	if nodeDir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node directory not enabled"})
		return
	}
	peer := c.Query("peer")
	if peer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer query parameter is required"})
		return
	}
	if err := nodeDir.Leave(peer); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "left", "peer": peer})
}
