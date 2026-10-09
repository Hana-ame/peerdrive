// node_market.go: node market endpoints (doc/NETDISK.md M1).
//
// Semantics aligned with "own node / other people's node":
//   - GET    /peerjs/nodes            market list (online ∪ joined), includes own node entry
//   - GET    /peerjs/nodes/joined     nodes I have joined
//   - POST   /peerjs/nodes/join       join a node (persisted + immediate dial)
//   - DELETE /peerjs/nodes/join?peer= leave (does not disconnect in-flight connections, see service comment)
//
// Deliberately named separately from /peerjs/node (single node self info) to avoid
// semantic confusion with legacy endpoints.
package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"

	"github.com/gin-gonic/gin"
)

// nodeDir is injected by main (same pattern as InitForwardController).
var nodeDir *service.NodeDirectory

// InitNodeDirectory injects the node market directory service (nil = this group of endpoints unavailable).
func InitNodeDirectory(d *service.NodeDirectory) {
	log.LogDebug("ctrl-node-market: InitNodeDirectory")
	nodeDir = d
}

// marketTimeout is the discovery server query timeout: the market list is an interactive
// request and shouldn't keep the frontend stuck in "loading" for too long (when the discovery
// server is unreachable, render the joined nodes as an empty list).
const marketTimeout = 6 * time.Second

// peerShareSvc provides the "ask the peer for its share manifest" capability (transport.PeerJSService).
// Injected separately rather than reusing forwardPeer: the two have unrelated semantics, and
// sharing a package-level variable would accidentally let "changing forwarding" affect the node detail page.
var peerShareSvc *transport.PeerJSService

// InitPeerShareController injects the PeerJS service for querying peer node share manifests.
func InitPeerShareController(svc *transport.PeerJSService) {
	log.LogDebug("ctrl-node-market: InitPeerShareController")
	peerShareSvc = svc
}

// shareWaitTimeout is the upper limit for waiting on "connecting to the peer node".
// When the user clicks into a peer node's details, that node may not yet be directly connected
// (just joined / just restarted), so we proactively dial and wait a moment — better UX than
// immediately reporting "not connected", but can't wait too long (HTTP request would hang).
const shareWaitTimeout = 8 * time.Second

// GetPeerShares handles GET /peerjs/nodes/:peer/shares.
// Returns the peer node's share manifest (collections + single files) for the frontend to render
// as a "file links" list.
func GetPeerShares(c *gin.Context) {
	if peerShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peerjs service not enabled"})
		return
	}
	peer := c.Param("peer")
	if peer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer is required"})
		return
	}

	// If not directly connected, dial first (idempotent), wait up to shareWaitTimeout
	if !peerShareSvc.ConnectedPeerIDs()[peer] {
		peerShareSvc.EnsureConnection(peer)
		deadline := time.Now().Add(shareWaitTimeout)
		for time.Now().Before(deadline) {
			if peerShareSvc.ConnectedPeerIDs()[peer] {
				break
			}
			select {
			case <-c.Request.Context().Done():
				c.JSON(http.StatusRequestTimeout, gin.H{"error": "cancelled"})
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}

	snap, err := peerShareSvc.RequestShares(peer)
	if err != nil {
		log.LogWarn("ctrl-node-market: request shares from %s failed: %v", peer, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"peer":        peer,
		"collections": snap.Collections,
		"files":       snap.Files,
		"dirs":        snap.Dirs,
		"total":       len(snap.Collections) + len(snap.Files),
	})
}

// GetNodeMarket handles GET /peerjs/nodes.
func GetNodeMarket(c *gin.Context) {
	if nodeDir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node directory not enabled"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), marketTimeout)
	defer cancel()
	nodes := nodeDir.Market(ctx)
	if nodes == nil {
		nodes = []model.NodeSummary{} // Keep JSON as [] (frontend doesn't need to check for null)
	}
	c.JSON(http.StatusOK, gin.H{
		"self":  nodeDir.Self(ctx),
		"nodes": nodes,
	})
}

// GetJoinedNodes handles GET /peerjs/nodes/joined.
func GetJoinedNodes(c *gin.Context) {
	if nodeDir == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "node directory not enabled"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), marketTimeout)
	defer cancel()
	c.JSON(http.StatusOK, gin.H{"nodes": nodeDir.Joined(ctx)})
}

// JoinNode handles POST /peerjs/nodes/join {peer}.
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

// LeaveNode handles DELETE /peerjs/nodes/join?peer=<id>.
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

// GetPeerBlocklist handles GET /peerjs/blocklist.
func GetPeerBlocklist(c *gin.Context) {
	if peerShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peerjs service unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": peerShareSvc.BlockedPeers()})
}

// BlockPeerRequest payload for POST /peerjs/blocklist.
type BlockPeerRequest struct {
	PeerID string `json:"peer_id"`
	Reason string `json:"reason,omitempty"`
}

// PostPeerBlocklist handles POST /peerjs/blocklist.
func PostPeerBlocklist(c *gin.Context) {
	if peerShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peerjs service unavailable"})
		return
	}
	var req BlockPeerRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.PeerID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer_id is required"})
		return
	}
	if err := peerShareSvc.BlockPeer(req.PeerID, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "blocked", "peer_id": req.PeerID})
}

// DeletePeerBlocklist handles DELETE /peerjs/blocklist/:peer.
func DeletePeerBlocklist(c *gin.Context) {
	if peerShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peerjs service unavailable"})
		return
	}
	peer := c.Param("peer")
	if strings.TrimSpace(peer) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer parameter is required"})
		return
	}
	if err := peerShareSvc.UnblockPeer(peer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "unblocked", "peer_id": peer})
}
