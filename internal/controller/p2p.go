package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"peerdrive/internal/model"
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

func GetDiscoveredPeers(c *gin.Context) {
	peers := p2pSvc.GetDiscoveredPeers()
	result := make([]gin.H, len(peers))
	for i, pi := range peers {
		addrs := make([]string, len(pi.Addrs))
		for j, a := range pi.Addrs {
			addrs[j] = a.String()
		}
		result[i] = gin.H{
			"peer_id": pi.ID.String(),
			"addrs":   addrs,
		}
	}
	c.JSON(http.StatusOK, gin.H{"peers": result})
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

func ConnectPeer(c *gin.Context) {
	var req struct {
		Addr string `json:"addr"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := p2pSvc.ConnectByAddr(c.Request.Context(), req.Addr); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "connected"})
}

func AnnounceHash(c *gin.Context) {
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := p2pSvc.AnnounceHash(req.Hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "announced"})
}

func FetchCollection(c *gin.Context) {
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	coll, err := p2pSvc.FetchCollection(ctx, req.Hash, nil)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found on p2p: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, coll)
}

func SyncFromPeer(c *gin.Context) {
	var req struct {
		PeerID     string   `json:"peer_id"`
		Hash       string   `json:"hash"`
		FileHashes []string `json:"file_hashes"`
		TargetDir  string   `json:"target_dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	pid, err := peer.Decode(req.PeerID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid peer id"})
		return
	}

	if req.Hash != "" && len(req.FileHashes) == 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		coll, err := p2pSvc.FetchCollection(ctx, req.Hash, []peer.AddrInfo{{ID: pid}})
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		hashes := make([]string, len(coll.Entries))
		for i, e := range coll.Entries {
			hashes[i] = e.Hash
		}
		req.FileHashes = hashes
	}

	if len(req.FileHashes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no files to sync"})
		return
	}

	if req.TargetDir == "" {
		req.TargetDir = "./p2p_sync"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()

	synced, err := p2pSvc.SyncFiles(ctx, pid, req.FileHashes, req.TargetDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"synced":   synced,
		"count":    len(synced),
		"saved_to": req.TargetDir,
	})
}

func P2PStatus(c *gin.Context) {
	enabled := p2pSvc.IsEnabled()
	resp := gin.H{"enabled": enabled}
	if enabled {
		id, addrs := p2pSvc.GetNodeInfo()
		peers := p2pSvc.GetConnectedPeers()
		disc := p2pSvc.GetDiscoveredPeers()
		resp["peer_id"] = id.String()
		resp["addrs"] = addrs
		resp["connected_count"] = len(peers)
		resp["discovered_count"] = len(disc)
		resp["relay_mode"] = p2pSvc.RelayMode()
		resp["hole_punch"] = p2pSvc.HolePunchEnabled()
		resp["ws_connections"] = p2pSvc.WSCount()
		if signalHub != nil {
			resp["signal_peers"] = signalHub.PeerCount()
		}
		// Connection manager stats
		if p2pSvc.ConnMgr != nil {
			resp["conn_stats"] = p2pSvc.ConnMgr.Stats()
		}
		// Active transfer jobs
		if p2pSvc.Transfer != nil {
			jobs := make([]gin.H, 0)
			for hash, tp := range p2pSvc.Transfer.ActiveJobs() {
				jobs = append(jobs, gin.H{
					"hash":      hash,
					"progress":  tp.Progress(),
					"total_mb":  float64(tp.TotalSize) / 1048576.0,
					"done":      tp.Done,
					"peers":     len(tp.Peers),
					"elapsed":   time.Since(tp.StartTime).String(),
				})
			}
			resp["active_transfers"] = jobs
		}
	}
	c.JSON(http.StatusOK, resp)
}

func PushSync(c *gin.Context) {
	var req struct {
		Hash      string                      `json:"hash"`
		Entries   []model.AnonCollectionEntry `json:"entries"`
		TargetDir string                      `json:"target_dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.Hash != "" {
		coll, err := anonSvc.GetCollectionByHash(req.Hash)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
			return
		}
		if req.TargetDir == "" {
			if n := coll.FriendlyName; n != "" {
				req.TargetDir = n
			}
		}
		for _, e := range coll.Entries {
			req.Entries = append(req.Entries, e)
		}
	}

	if len(req.Entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no entries to sync"})
		return
	}

	if req.TargetDir == "" && len(req.Entries) > 0 {
		req.TargetDir = req.Entries[0].Path
		if idx := strings.LastIndex(req.TargetDir, "/"); idx >= 0 {
			req.TargetDir = req.TargetDir[:idx]
		}
	}

	targetDir := req.TargetDir
	if !strings.HasPrefix(targetDir, "/") {
		targetDir = "./" + targetDir
	}

	c.JSON(http.StatusOK, gin.H{
		"entries":    req.Entries,
		"target_dir": targetDir,
		"message":    "collection received, ready to download",
	})
}

func RequestFile(c *gin.Context) {
	var req struct {
		Hash    string   `json:"hash"`
		PeerIDs []string `json:"peer_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	pids := make([]peer.ID, len(req.PeerIDs))
	for i, s := range req.PeerIDs {
		pid, err := peer.Decode(s)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid peer id: %s", s)})
			return
		}
		pids[i] = pid
	}

	results, err := p2pSvc.BroadcastRequest(req.Hash, pids)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	responses := make([]gin.H, len(results))
	for i, r := range results {
		info := gin.H{"hash": r.Hash}
		if r.Err != nil {
			info["error"] = r.Err.Error()
		} else {
			info["size"] = len(r.Data)
		}
		responses[i] = info
	}

	c.JSON(http.StatusOK, gin.H{
		"hash":       req.Hash,
		"requested":  len(pids),
		"responses":  len(results),
		"details":    responses,
	})
}

func WSInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"ws_connections": p2pSvc.WSCount(),
		"ws_endpoint":    "/ws/transfer",
		"message_types":  []string{"request", "response", "ping", "pong"},
	})
}
