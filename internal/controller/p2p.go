package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/p2p_bt"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-libp2p/core/peer"
)

var p2pSvc *service.P2PService
var btSvc *p2p_bt.BTDHTService
var dualSvc *service.DualP2PService

var peerTracker *service.PeerTracker
var peerScanner *service.PeerScanner

// InitPeerScanner injects the PeerScanner singleton into the controller
// package so that handlers can query scanner stats.
func InitPeerScanner(s *service.PeerScanner) {
	log.LogDebug("ctrl-p2p: InitPeerScanner")
	peerScanner = s
}

func InitP2PController(svc *service.P2PService) {
	log.LogDebug("ctrl-p2p: InitP2PController")
	p2pSvc = svc
}

func InitBTController(svc *p2p_bt.BTDHTService) {
	log.LogDebug("ctrl-p2p: InitBTController")
	btSvc = svc
}

func InitDualController(svc *service.DualP2PService) {
	log.LogDebug("ctrl-p2p: InitDualController")
	dualSvc = svc
}

// InitPeerTracker injects the PeerTracker singleton into the controller
// package so that handlers can query peer metadata and record events.
func InitPeerTracker(t *service.PeerTracker) {
	log.LogDebug("ctrl-p2p: InitPeerTracker")
	peerTracker = t
}

func GetNodeInfo(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetNodeInfo")
	id, addrs := p2pSvc.GetNodeInfo()
	c.JSON(http.StatusOK, gin.H{
		"peer_id": id.String(),
		"addrs":   addrs,
	})
	log.LogInfo("ctrl-p2p: GetNodeInfo peerID=%s, addrs=%d", id.String(), len(addrs))
}

func GetPeers(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetPeers")
	peers := p2pSvc.GetConnectedPeers()
	strs := make([]string, len(peers))
	for i, p := range peers {
		strs[i] = p.String()
	}
	c.JSON(http.StatusOK, gin.H{"peers": strs})
	log.LogInfo("ctrl-p2p: GetPeers count=%d", len(peers))
}

func GetDiscoveredPeers(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetDiscoveredPeers")
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
	log.LogInfo("ctrl-p2p: GetDiscoveredPeers count=%d", len(peers))
}

func PingPeer(c *gin.Context) {
	log.LogDebug("ctrl-p2p: PingPeer")
	raw := c.Param("peer_id")
	pid, err := peer.Decode(raw)
	if err != nil {
		log.LogWarn("ctrl-p2p: PingPeer invalid peer id: %s", raw)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid peer id"})
		return
	}
	rtt, err := p2pSvc.PingPeer(c.Request.Context(), pid)
	if err != nil {
		log.LogError("ctrl-p2p: PingPeer failed for %s: %v", raw, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: PingPeer %s RTT=%s", raw, rtt.String())

	if peerTracker != nil {
		peerTracker.RecordLatency(raw, rtt)
	}

	c.JSON(http.StatusOK, gin.H{"peer": raw, "rtt": rtt.String()})
}

func ConnectPeer(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ConnectPeer")
	var req struct {
		Addr string `json:"addr"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: ConnectPeer invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := p2pSvc.ConnectByAddr(c.Request.Context(), req.Addr); err != nil {
		log.LogError("ctrl-p2p: ConnectPeer to %s failed: %v", req.Addr, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: ConnectPeer to %s successful", req.Addr)
	c.JSON(http.StatusOK, gin.H{"status": "connected"})
}

func AnnounceHash(c *gin.Context) {
	log.LogDebug("ctrl-p2p: AnnounceHash")
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: AnnounceHash invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := p2pSvc.AnnounceHash(req.Hash); err != nil {
		log.LogError("ctrl-p2p: AnnounceHash %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: AnnounceHash %s successful", req.Hash)
	c.JSON(http.StatusOK, gin.H{"status": "announced"})
}

func FetchCollection(c *gin.Context) {
	log.LogDebug("ctrl-p2p: FetchCollection")
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: FetchCollection invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	coll, err := p2pSvc.FetchCollection(ctx, req.Hash, nil)
	if err != nil {
		log.LogError("ctrl-p2p: FetchCollection %s failed: %v", req.Hash, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found on p2p: " + err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: FetchCollection %s successful", req.Hash)
	c.JSON(http.StatusOK, coll)
}

func SyncFromPeer(c *gin.Context) {
	log.LogDebug("ctrl-p2p: SyncFromPeer")
	var req struct {
		PeerID     string   `json:"peer_id"`
		Hash       string   `json:"hash"`
		FileHashes []string `json:"file_hashes"`
		TargetDir  string   `json:"target_dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: SyncFromPeer invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	pid, err := peer.Decode(req.PeerID)
	if err != nil {
		log.LogWarn("ctrl-p2p: SyncFromPeer invalid peer id: %s", req.PeerID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid peer id"})
		return
	}

	if req.Hash != "" && len(req.FileHashes) == 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		coll, err := p2pSvc.FetchCollection(ctx, req.Hash, []peer.AddrInfo{{ID: pid}})
		if err != nil {
			log.LogError("ctrl-p2p: SyncFromPeer fetch collection %s failed: %v", req.Hash, err)
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
		log.LogWarn("ctrl-p2p: SyncFromPeer no files to sync")
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
		log.LogError("ctrl-p2p: SyncFromPeer failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: SyncFromPeer synced %d files from %s to %s", len(synced), req.PeerID, req.TargetDir)
	c.JSON(http.StatusOK, gin.H{
		"synced":   synced,
		"count":    len(synced),
		"saved_to": req.TargetDir,
	})
}

func P2PStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: P2PStatus")
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
	log.LogDebug("ctrl-p2p: PushSync")
	var req struct {
		Hash      string                      `json:"hash"`
		Entries   []model.AnonCollectionEntry `json:"entries"`
		TargetDir string                      `json:"target_dir"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: PushSync invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.Hash != "" {
		coll, err := anonSvc.GetCollectionByHash(req.Hash)
		if err != nil {
			log.LogError("ctrl-p2p: PushSync collection %s not found: %v", req.Hash, err)
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
		log.LogWarn("ctrl-p2p: PushSync no entries to sync")
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

	log.LogInfo("ctrl-p2p: PushSync %d entries target=%s", len(req.Entries), targetDir)
	c.JSON(http.StatusOK, gin.H{
		"entries":    req.Entries,
		"target_dir": targetDir,
		"message":    "collection received, ready to download",
	})
}

func RequestFile(c *gin.Context) {
	log.LogDebug("ctrl-p2p: RequestFile")
	var req struct {
		Hash    string   `json:"hash"`
		PeerIDs []string `json:"peer_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: RequestFile invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	pids := make([]peer.ID, len(req.PeerIDs))
	for i, s := range req.PeerIDs {
		pid, err := peer.Decode(s)
		if err != nil {
			log.LogWarn("ctrl-p2p: RequestFile invalid peer id: %s", s)
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid peer id: %s", s)})
			return
		}
		pids[i] = pid
	}

	results, err := p2pSvc.BroadcastRequest(req.Hash, pids)
	if err != nil {
		log.LogError("ctrl-p2p: RequestFile %s failed: %v", req.Hash, err)
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

	log.LogInfo("ctrl-p2p: RequestFile %s got %d responses", req.Hash, len(results))
	c.JSON(http.StatusOK, gin.H{
		"hash":       req.Hash,
		"requested":  len(pids),
		"responses":  len(results),
		"details":    responses,
	})
}

func WSInfo(c *gin.Context) {
	log.LogDebug("ctrl-p2p: WSInfo")
	c.JSON(http.StatusOK, gin.H{
		"ws_connections": p2pSvc.WSCount(),
		"ws_endpoint":    "/ws/transfer",
		"message_types":  []string{"request", "response", "ping", "pong"},
	})
}

// --- BitTorrent DHT handlers ---

func BTDHTStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDHTStatus")
	if btSvc == nil || btSvc.Server == nil {
		log.LogInfo("ctrl-p2p: BTDHTStatus disabled")
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":      true,
		"listen_addr":  btSvc.Server.Addr().String(),
		"num_nodes":    btSvc.NumNodes(),
	})
}

func BTAnnounce(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTAnnounce")
	if btSvc == nil || btSvc.Server == nil {
		log.LogError("ctrl-p2p: BTAnnounce BT DHT not enabled")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT DHT not enabled"})
		return
	}
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: BTAnnounce invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := btSvc.Announce(req.Hash); err != nil {
		log.LogError("ctrl-p2p: BTAnnounce %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: BTAnnounce %s successful", req.Hash)
	c.JSON(http.StatusOK, gin.H{"status": "announced on BT DHT"})
}

func BTFindProviders(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTFindProviders")
	if btSvc == nil || btSvc.Server == nil {
		log.LogError("ctrl-p2p: BTFindProviders BT DHT not enabled")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT DHT not enabled"})
		return
	}
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: BTFindProviders invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	peers, err := btSvc.FindProviders(req.Hash)
	if err != nil {
		log.LogError("ctrl-p2p: BTFindProviders %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: BTFindProviders %s found %d peers", req.Hash, len(peers))
	c.JSON(http.StatusOK, gin.H{
		"hash":   req.Hash,
		"peers":  peers,
		"count":  len(peers),
	})
}

// --- Dual P2P (IPFS + BT DHT) handlers ---

func DualAnnounce(c *gin.Context) {
	log.LogDebug("ctrl-p2p: DualAnnounce")
	if dualSvc == nil {
		log.LogError("ctrl-p2p: DualAnnounce dual P2P not available")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Dual P2P not available"})
		return
	}
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: DualAnnounce invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := dualSvc.Announce(req.Hash); err != nil {
		log.LogError("ctrl-p2p: DualAnnounce %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: DualAnnounce %s successful", req.Hash)
	c.JSON(http.StatusOK, gin.H{"status": "announced on both networks"})
}

func DualFindProviders(c *gin.Context) {
	log.LogDebug("ctrl-p2p: DualFindProviders")
	if dualSvc == nil {
		log.LogError("ctrl-p2p: DualFindProviders dual P2P not available")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Dual P2P not available"})
		return
	}
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: DualFindProviders invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	result, err := dualSvc.FindProviders(req.Hash)
	if err != nil {
		log.LogError("ctrl-p2p: DualFindProviders %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: DualFindProviders %s: %d IPFS peers, %d BT peers", req.Hash, len(result.IPFSPeers), len(result.BTPeers))
	c.JSON(http.StatusOK, gin.H{
		"hash":       req.Hash,
		"ipfs_peers": result.IPFSPeers,
		"bt_peers":   result.BTPeers,
	})
}

// GetPeersDetail returns detailed metadata for all tracked peers.
func GetPeersDetail(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetPeersDetail")
	if peerTracker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer tracker not available"})
		return
	}
	peers := peerTracker.GetAllPeers()
	log.LogInfo("ctrl-p2p: GetPeersDetail count=%d", len(peers))
	c.JSON(http.StatusOK, peers)
}

// GetPeerDetail returns detailed metadata for a single peer by peer_id.
func GetPeerDetail(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetPeerDetail")
	if peerTracker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer tracker not available"})
		return
	}
	peerID := c.Param("peer_id")
	peer := peerTracker.GetPeer(peerID)
	if peer == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "peer not found"})
		return
	}
	log.LogInfo("ctrl-p2p: GetPeerDetail peer=%s", peerID)
	c.JSON(http.StatusOK, peer)
}

// GetP2PStats returns global P2P statistics.
func GetP2PStats(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetP2PStats")
	if peerTracker == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer tracker not available"})
		return
	}
	stats := peerTracker.GetStats()
	log.LogInfo("ctrl-p2p: GetP2PStats peers=%v", stats["total_peers_seen"])
	c.JSON(http.StatusOK, stats)
}

// GetConnections returns connection counts by direction and scanner status.
//   GET /p2p/connections
func GetConnections(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetConnections")

	if peerScanner == nil || peerTracker == nil {
		c.JSON(http.StatusOK, gin.H{
			"inbound":         0,
			"outbound":        0,
			"total":           0,
			"active_scanners": []string{},
			"last_scan_times": map[string]string{},
		})
		return
	}

	inbound, outbound := peerTracker.ConnectionCounts()

	c.JSON(http.StatusOK, gin.H{
		"inbound":         inbound,
		"outbound":        outbound,
		"total":           inbound + outbound,
		"active_scanners": peerScanner.ActiveScanners(),
		"last_scan_times": peerScanner.LastScanTimes(),
	})
	log.LogInfo("ctrl-p2p: GetConnections inbound=%d outbound=%d total=%d", inbound, outbound, inbound+outbound)
}
