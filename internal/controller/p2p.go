// Package controller 提供 P2P 网络相关 HTTP 处理函数，包括节点信息、对端管理、BT DHT、双网络、端口转发等端点。
package controller

import (
	"context"
	"encoding/base64"
	"encoding/hex"
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
var btClient *p2p_bt.BTClient
var dualSvc *service.DualP2PService

var peerTracker *service.PeerTracker
var peerScanner *service.PeerScanner

var forwardSvc *service.ForwardService

var resumeMgr *service.ResumeManager
var multiPeerDl *service.MultiPeerDownloader

// InitPeerScanner 注入 PeerScanner 实例供 P2P 扫描状态查询使用。
func InitPeerScanner(s *service.PeerScanner) {
	log.LogDebug("ctrl-p2p: InitPeerScanner")
	peerScanner = s
}

func InitForwardController(svc *service.ForwardService) {
	log.LogDebug("ctrl-p2p: InitForwardController")
	forwardSvc = svc
}

func InitP2PController(svc *service.P2PService) {
	log.LogDebug("ctrl-p2p: InitP2PController")
	p2pSvc = svc
}

func InitBTController(svc *p2p_bt.BTDHTService) {
	log.LogDebug("ctrl-p2p: InitBTController")
	btSvc = svc
}

// InitResumeManager 注入 ResumeManager 实例供断点续传端点使用。
func InitResumeManager(mgr *service.ResumeManager) {
	log.LogDebug("ctrl-p2p: InitResumeManager")
	resumeMgr = mgr
}

// InitMultiPeerDownloader 注入 MultiPeerDownloader 实例供多源并行下载端点使用。
func InitMultiPeerDownloader(mp *service.MultiPeerDownloader) {
	log.LogDebug("ctrl-p2p: InitMultiPeerDownloader")
	multiPeerDl = mp
}

func InitBTClient(c *p2p_bt.BTClient) {
	log.LogDebug("ctrl-p2p: InitBTClient")
	btClient = c
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
		log.LogWarn("ctrl-p2p: AnnounceHash %s partial: %v", req.Hash, err)
		c.JSON(http.StatusOK, gin.H{"status": "announced locally", "warning": err.Error()})
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


// --- BEP 44 (Arbitrary DHT Data Storage) ---

// BEP44Put stores data in the BitTorrent DHT using BEP 44.
//   POST /p2p/bt/bep44/put
//   Request: {data: "<base64>", mutable: bool, salt: "<base64>?"}
//   Response: {target: "<hex>", ...}
func BEP44Put(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BEP44Put")
	if btSvc == nil || btSvc.Server == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT DHT not enabled"})
		return
	}

	var req struct {
		Data    string `json:"data"`
		Mutable bool   `json:"mutable"`
		Salt    string `json:"salt"`
		Key     string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: BEP44Put invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	rawData, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		log.LogWarn("ctrl-p2p: BEP44Put invalid base64: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid base64 data"})
		return
	}

	if req.Mutable {
		log.LogInfo("ctrl-p2p: BEP44Put mutable requested but requires key via API; returning stub")
		c.JSON(http.StatusNotImplemented, gin.H{
			"error":   "mutable put via API requires key management; use Go API directly",
		})
		return
	}

	// Immutable put.
	target, err := btSvc.PutImmutable(rawData)
	if err != nil {
		log.LogError("ctrl-p2p: BEP44Put failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BEP44Put immutable target=%x size=%d", target, len(rawData))
	c.JSON(http.StatusOK, gin.H{
		"target":  hex.EncodeToString(target[:]),
		"size":    len(rawData),
		"mutable": false,
	})
}

// BEP44Get retrieves data from the BitTorrent DHT using BEP 44.
//   POST /p2p/bt/bep44/get
//   Request: {target: "<hex>"}
//   Response: {data: "<base64>", ...}
func BEP44Get(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BEP44Get")
	if btSvc == nil || btSvc.Server == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT DHT not enabled"})
		return
	}

	var req struct {
		Target string `json:"target"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Target) != 40 {
		log.LogWarn("ctrl-p2p: BEP44Get invalid target")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target (expected 40-char hex)"})
		return
	}

	raw, err := hex.DecodeString(req.Target)
	if err != nil || len(raw) != 20 {
		log.LogWarn("ctrl-p2p: BEP44Get bad hex: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hex encoding"})
		return
	}
	var target [20]byte
	copy(target[:], raw)

	data, err := btSvc.GetImmutable(target)
	if err != nil {
		log.LogError("ctrl-p2p: BEP44Get failed: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BEP44Get target=%x size=%d", target, len(data))
	c.JSON(http.StatusOK, gin.H{
		"data": base64.StdEncoding.EncodeToString(data),
		"size": len(data),
	})
}

// --- BEP 51 (Infohash Indexing) ---

// BEP51Sample returns discovered infohashes from the DHT using BEP 51.
//   GET /p2p/bt/bep51/sample
//   Response: {samples: ["<hex>", ...], count: int}
func BEP51Sample(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BEP51Sample")
	if btSvc == nil || btSvc.Server == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT DHT not enabled"})
		return
	}

	samples, err := btSvc.DiscoverInfohashes(200)
	if err != nil {
		log.LogError("ctrl-p2p: BEP51Sample failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	hexSamples := make([]string, len(samples))
	for i, s := range samples {
		hexSamples[i] = hex.EncodeToString(s[:])
	}

	log.LogInfo("ctrl-p2p: BEP51Sample collected %d infohashes", len(samples))
	c.JSON(http.StatusOK, gin.H{
		"samples": hexSamples,
		"count":   len(samples),
	})
}

// --- BitTorrent Download Handlers ---

// BTTorrentUpload accepts a .torrent file upload, parses it, and starts
// downloading the torrent.
//   POST /p2p/bt/torrent
func BTTorrentUpload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTTorrentUpload")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	file, header, err := c.Request.FormFile("torrent")
	if err != nil {
		log.LogWarn("ctrl-p2p: BTTorrentUpload missing torrent file: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing torrent file"})
		return
	}
	defer file.Close()

	data := make([]byte, header.Size)
	if _, err := file.Read(data); err != nil {
		log.LogError("ctrl-p2p: BTTorrentUpload read failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read torrent file"})
		return
	}

	meta, err := p2p_bt.ParseTorrent(data)
	if err != nil {
		log.LogError("ctrl-p2p: BTTorrentUpload parse failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid torrent file: " + err.Error()})
		return
	}

	if err := btClient.AddTorrent(meta); err != nil {
		log.LogError("ctrl-p2p: BTTorrentUpload add torrent failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTTorrentUpload started %q (infohash=%s)", meta.Name, meta.InfoHashHex)
	c.JSON(http.StatusOK, gin.H{
		"infohash":  meta.InfoHashHex,
		"name":      meta.Name,
		"files":     len(meta.Files),
		"total":     meta.TotalSize,
		"pieces":    len(meta.Pieces),
		"status":    "downloading",
	})
}

// BTMagnetResolve accepts a magnet URI and starts downloading.
//   POST /p2p/bt/magnet  {"uri": "magnet:?xt=urn:btih:..."}
func BTMagnetResolve(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTMagnetResolve")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	var req struct {
		URI string `json:"uri"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: BTMagnetResolve invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	magnet, err := p2p_bt.ParseMagnet(req.URI)
	if err != nil {
		log.LogError("ctrl-p2p: BTMagnetResolve parse failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid magnet URI: " + err.Error()})
		return
	}

	if err := btClient.AddMagnet(magnet); err != nil {
		log.LogError("ctrl-p2p: BTMagnetResolve add magnet failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTMagnetResolve started %q (infohash=%s)", magnet.DisplayName, magnet.InfoHash)
	c.JSON(http.StatusOK, gin.H{
		"infohash":     magnet.InfoHash,
		"display_name": magnet.DisplayName,
		"trackers":     magnet.Trackers,
		"status":       "downloading",
	})
}

// BTDownloadProgress returns the download progress for a specific infohash.
//   GET /p2p/bt/download/:infohash
func BTDownloadProgress(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDownloadProgress")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	status := btClient.GetDownload(infohash)
	if status == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "download not found"})
		return
	}

	log.LogInfo("ctrl-p2p: BTDownloadProgress %s: %s (%d/%d pieces)", infohash, status.Status, status.PiecesDone, status.PiecesTotal)
	c.JSON(http.StatusOK, status)
}

// BTDownloadList returns all active and completed BT downloads.
//   GET /p2p/bt/downloads
func BTDownloadList(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDownloadList")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	downloads := btClient.ListDownloads()
	log.LogInfo("ctrl-p2p: BTDownloadList count=%d", len(downloads))
	c.JSON(http.StatusOK, gin.H{
		"downloads": downloads,
		"count":     len(downloads),
	})
}

// --- Port Forwarding ---

// CreateForwardSession registers a local service port for remote forwarding.
//
//	POST /p2p/forward/create  {key: "secret", port: 8080}
//	Response: {status: "listening", port: 8080}
func CreateForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CreateForwardSession")
	if forwardSvc == nil || !forwardSvc.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}

	var req struct {
		Key  string `json:"key"`
		Port int    `json:"port"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: CreateForwardSession invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if req.Port <= 0 || req.Port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid port"})
		return
	}
	if err := forwardSvc.CreateForward(req.Key, req.Port); err != nil {
		log.LogError("ctrl-p2p: CreateForwardSession failed: %v", err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: CreateForwardSession port=%d", req.Port)
	c.JSON(http.StatusOK, gin.H{"status": "listening", "port": req.Port})
}

// ConnectForwardSession connects to a remote peer and forwards a local port.
//
//	POST /p2p/forward/connect  {key: "secret", target_peer: "12D3...", local_port: 18080}
//	Response: {status: "connected", local_port: 18080}
func ConnectForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ConnectForwardSession")
	if forwardSvc == nil || !forwardSvc.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}

	var req struct {
		Key        string `json:"key"`
		TargetPeer string `json:"target_peer"`
		LocalPort  int    `json:"local_port"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: ConnectForwardSession invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid local_port"})
		return
	}
	pid, err := peer.Decode(req.TargetPeer)
	if err != nil {
		log.LogWarn("ctrl-p2p: ConnectForwardSession invalid peer: %s", req.TargetPeer)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid target_peer"})
		return
	}

	ctx := c.Request.Context()
	if err := forwardSvc.ConnectForward(ctx, pid, req.Key, req.LocalPort); err != nil {
		log.LogError("ctrl-p2p: ConnectForwardSession failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: ConnectForwardSession target=%s local_port=%d", req.TargetPeer, req.LocalPort)
	c.JSON(http.StatusOK, gin.H{"status": "connected", "local_port": req.LocalPort})
}

// ListForwardSessions returns all active forward sessions.
//
//	GET /p2p/forward/list
//	Response: {sessions: [{key, source_peer, local_port, created_at, clients}]}
func ListForwardSessions(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ListForwardSessions")
	if forwardSvc == nil || !forwardSvc.IsEnabled() {
		c.JSON(http.StatusOK, gin.H{"sessions": []interface{}{}})
		return
	}
	sessions := forwardSvc.ListSessions()
	items := make([]gin.H, len(sessions))
	for i, s := range sessions {
		items[i] = gin.H{
			"key":         s.Key,
			"source_peer": s.SourcePeer.String(),
			"local_port":  s.LocalPort,
			"created_at":  s.CreatedAt,
			"clients":     s.Clients,
		}
	}
	log.LogInfo("ctrl-p2p: ListForwardSessions count=%d", len(items))
	c.JSON(http.StatusOK, gin.H{"sessions": items})
}

// CloseForwardSession removes a forward session.
//
//	POST /p2p/forward/close  {key: "secret"}
//	Response: {status: "closed"}
func CloseForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CloseForwardSession")
	if forwardSvc == nil || !forwardSvc.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}

	var req struct {
		Key string `json:"key"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: CloseForwardSession invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if err := forwardSvc.CloseForward(req.Key); err != nil {
		log.LogWarn("ctrl-p2p: CloseForwardSession not found")
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: CloseForwardSession closed")
	c.JSON(http.StatusOK, gin.H{"status": "closed"})
}
