// Package controller 提供 P2P 网络相关 HTTP 处理函数，包括节点信息、对端管理、BT DHT、双网络、端口转发等端点。
package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/nodestate"
	"peerdrive/internal/p2p_bt"
	"peerdrive/internal/service"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
	"github.com/libp2p/go-libp2p/core/peer"
)

var p2pSvc *service.P2PService
var btSvc *p2p_bt.BTDHTService
var btClient *p2p_bt.BTClient
var dualSvc *service.DualP2PService

var peerTracker *service.PeerTracker
var peerScanner *service.PeerScanner

var pinSvc *service.PinService
var forwardSvc *service.ForwardService

var ipfsCompatLayer *service.IPFSCompatLayer

var resumeMgr *service.ResumeManager
var multiPeerDl *service.MultiPeerDownloader

// InitPeerScanner 注入 PeerScanner 实例供 P2P 扫描状态查询使用。
func InitPeerScanner(s *service.PeerScanner) {
	log.LogDebug("ctrl-p2p: InitPeerScanner")
	peerScanner = s
}

// InitForwardController 注入 ForwardService 实例供端口转发端点使用。
// InitPinController 注入 PinService（M2 收层：pin 端点不再直调 repository）。
func InitPinController(svc *service.PinService) {
	pinSvc = svc
}

func InitForwardController(svc *service.ForwardService) {
	log.LogDebug("ctrl-p2p: InitForwardController")
	forwardSvc = svc
}

// InitP2PController 注入 P2PService 实例供 P2P 处理函数使用。
func InitP2PController(svc *service.P2PService) {
	log.LogDebug("ctrl-p2p: InitP2PController")
	p2pSvc = svc
}

// InitBTController 注入 BTDHTService 实例供 BitTorrent DHT 处理函数使用。
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

// InitBTClient 注入 BTClient 实例供 BitTorrent 下载处理函数使用。
func InitBTClient(c *p2p_bt.BTClient) {
	log.LogDebug("ctrl-p2p: InitBTClient")
	btClient = c
}

// InitDualController 注入 DualP2PService 实例供双网络（IPFS+BT）操作端点使用。
func InitDualController(svc *service.DualP2PService) {
	log.LogDebug("ctrl-p2p: InitDualController")
	dualSvc = svc
}

// InitPeerTracker 注入 PeerTracker 实例供对端元数据和统计查询使用。
func InitPeerTracker(t *service.PeerTracker) {
	log.LogDebug("ctrl-p2p: InitPeerTracker")
	peerTracker = t
}

// GetNodeInfo 处理 GET /p2p/node，返回本地节点 ID 和监听地址列表。
func GetNodeInfo(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetNodeInfo")
	id, addrs := p2pSvc.GetNodeInfo()
	c.JSON(http.StatusOK, gin.H{
		"peer_id": id.String(),
		"addrs":   addrs,
	})
	log.LogInfo("ctrl-p2p: GetNodeInfo peerID=%s, addrs=%d", id.String(), len(addrs))
}

// GetPeers 处理 GET /p2p/peers，返回当前已连接的对端 ID 列表。
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

// GetDiscoveredPeers 处理 GET /p2p/discovered，返回 mDNS 发现的局域网对端列表。
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

// PingPeer 处理 GET /p2p/ping/:peer_id，向指定对端发送 ping 并返回 RTT。
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

// ConnectPeer 处理 POST /p2p/connect，通过 multiaddr 连接到远程对端。
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

// AnnounceHash 处理 POST /p2p/announce，在 IPFS DHT 上宣布本节点持有指定 hash。
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

// FetchCollection 处理 POST /p2p/fetch，从 P2P 网络获取匿名集合。
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

// SyncFromPeer 处理 POST /p2p/sync，从指定对端同步文件到本地目录。
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

// P2PStatus 处理 GET /p2p/status，返回 P2P 节点综合状态信息（连接数、传输任务、中继模式等）。
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
					"hash":     hash,
					"progress": tp.Progress(),
					"total_mb": float64(tp.TotalSize) / 1048576.0,
					"done":     tp.Done,
					"peers":    len(tp.Peers),
					"elapsed":  time.Since(tp.StartTime).String(),
				})
			}
			resp["active_transfers"] = jobs
		}
	}
	c.JSON(http.StatusOK, resp)
}

// PushSync 处理 POST /p2p/push，推送集合条目到目标目录供对端获取。
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

// RequestFile 处理 POST /p2p/request-file，向指定对端列表广播文件请求。
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
		"hash":      req.Hash,
		"requested": len(pids),
		"responses": len(results),
		"details":   responses,
	})
}

// WSInfo 处理 GET /p2p/ws/info，返回 WebSocket 传输端点信息和消息类型。
func WSInfo(c *gin.Context) {
	log.LogDebug("ctrl-p2p: WSInfo")
	c.JSON(http.StatusOK, gin.H{
		"ws_connections": p2pSvc.WSCount(),
		"ws_endpoint":    "/ws/transfer",
		"message_types":  []string{"request", "response", "ping", "pong"},
	})
}

// --- BitTorrent DHT handlers ---

// BTDHTStatus 处理 GET /bt/status，返回 BitTorrent DHT 节点状态。
func BTDHTStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDHTStatus")
	if btSvc == nil || btSvc.Server == nil {
		log.LogInfo("ctrl-p2p: BTDHTStatus disabled")
		c.JSON(http.StatusOK, gin.H{"enabled": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":     true,
		"listen_addr": btSvc.Server.Addr().String(),
		"num_nodes":   btSvc.NumNodes(),
		"node_id":     fmt.Sprintf("%x", btSvc.NodeID),
	})
}

// BTAnnounce 处理 POST /bt/announce，在 BitTorrent DHT 上 announce 指定 hash。
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

// BTFindProviders 处理 POST /bt/find，在 BitTorrent DHT 上查找持有指定 hash 的对端。
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
		"hash":  req.Hash,
		"peers": peers,
		"count": len(peers),
	})
}

// --- Dual P2P (IPFS + BT DHT) handlers ---

// DualAnnounce 处理 POST /p2p/dual/announce，同时在 IPFS DHT 和 BT DHT 上 announce。
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

// DualFindProviders 处理 POST /p2p/dual/find，同时在 IPFS DHT 和 BT DHT 上查找 providers。
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

// GetPeersDetail 处理 GET /p2p/peers/detail，返回所有已跟踪对端的详细元数据。
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

// GetPeerDetail 处理 GET /p2p/peers/detail/:peer_id，返回指定对端的详细元数据。
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

// GetP2PStats 处理 GET /p2p/stats，返回全局 P2P 统计信息（传输量、对端数等）。
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

// GetConnections 处理 GET /p2p/connections，返回按方向统计的连接数和扫描器状态。
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

// GetTopology 处理 GET /p2p/topology，返回连接拓扑图。
func GetTopology(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetTopology")
	if p2pSvc == nil || !p2pSvc.IsEnabled() {
		c.JSON(http.StatusOK, service.TopologyGraph{
			LocalPeerID: "",
			Edges:       []service.TopologyEdge{},
		})
		return
	}
	topo := p2pSvc.GetTopology()
	log.LogInfo("ctrl-p2p: GetTopology local=%s edges=%d", topo.LocalPeerID, len(topo.Edges))
	c.JSON(http.StatusOK, topo)
}

// GetConnectionQuality 处理 GET /p2p/quality，返回所有对端的连接质量指标。
func GetConnectionQuality(c *gin.Context) {
	log.LogDebug("ctrl-p2p: GetConnectionQuality")
	if p2pSvc == nil || !p2pSvc.IsEnabled() || p2pSvc.ConnMgr == nil {
		c.JSON(http.StatusOK, []service.ConnectionQuality{})
		return
	}

	// If a specific peer is requested
	peerID := c.Query("peer_id")
	if peerID != "" {
		pid, err := peer.Decode(peerID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid peer id"})
			return
		}
		quality := p2pSvc.ConnMgr.GetConnectionQuality(pid)
		c.JSON(http.StatusOK, quality)
		return
	}

	qualities := p2pSvc.ConnMgr.GetAllConnectionQualities()
	log.LogInfo("ctrl-p2p: GetConnectionQuality peers=%d", len(qualities))
	c.JSON(http.StatusOK, qualities)
}

// --- BEP 44 (Arbitrary DHT Data Storage) ---

// BEP44Put 处理 POST /bt/bep44/put，通过 BEP 44 将不可变数据存储到 BT DHT。
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
			"error": "mutable put via API requires key management; use Go API directly",
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

// BEP44Get 处理 POST /bt/bep44/get，通过 BEP 44 从 BT DHT 读取不可变数据。
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

// BEP51Sample 处理 GET /bt/bep51/sample，通过 BEP 51 采集 DHT 中的 infohash 样本。
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

// BTTorrentUpload 处理 POST /bt/torrent，接受 .torrent 文件上传并启动下载。
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

	meta, err := btClient.AddTorrentBytes(data)
	if err != nil {
		log.LogError("ctrl-p2p: BTTorrentUpload failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTTorrentUpload started %q (infohash=%s)", meta.Name, meta.InfoHashHex)
	c.JSON(http.StatusOK, gin.H{
		"infohash": meta.InfoHashHex,
		"name":     meta.Name,
		"files":    len(meta.Files),
		"total":    meta.TotalSize,
		"pieces":   len(meta.Pieces),
		"status":   "downloading",
	})
}

// BTMagnetResolve 处理 POST /bt/magnet，解析 magnet URI 并启动 BitTorrent 下载。
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

	meta, err := btClient.AddMagnetURI(req.URI)
	if err != nil {
		log.LogError("ctrl-p2p: BTMagnetResolve failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTMagnetResolve started %q (infohash=%s)", meta.Name, meta.InfoHashHex)
	c.JSON(http.StatusOK, gin.H{
		"infohash":     meta.InfoHashHex,
		"display_name": meta.Name,
		"trackers":     meta.AnnounceList,
		"status":       "downloading",
	})
}

// BTDownloadProgress 处理 GET /bt/download/:infohash，查询指定 infohash 的下载进度。
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

// BTPauseDownload 处理 POST /bt/download/:infohash/pause，暂停指定下载任务。
func BTPauseDownload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTPauseDownload")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	if err := btClient.PauseDownload(infohash); err != nil {
		log.LogWarn("ctrl-p2p: BTPauseDownload %s failed: %v", infohash, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTPauseDownload %s paused", infohash)
	c.JSON(http.StatusOK, gin.H{"infohash": infohash, "status": "paused"})
}

// BTResumeDownload 处理 POST /bt/download/:infohash/resume，恢复暂停的下载任务。
func BTResumeDownload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTResumeDownload")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	if err := btClient.ResumeDownload(infohash); err != nil {
		log.LogWarn("ctrl-p2p: BTResumeDownload %s failed: %v", infohash, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTResumeDownload %s resumed", infohash)
	c.JSON(http.StatusOK, gin.H{"infohash": infohash, "status": "downloading"})
}

// BTRemoveDownload 处理 DELETE /bt/download/:infohash，删除下载任务及其文件。
func BTRemoveDownload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTRemoveDownload")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	if err := btClient.RemoveDownload(infohash); err != nil {
		log.LogWarn("ctrl-p2p: BTRemoveDownload %s failed: %v", infohash, err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTRemoveDownload %s removed", infohash)
	c.JSON(http.StatusOK, gin.H{"infohash": infohash, "status": "removed"})
}

// BTGlobalStats 处理 GET /bt/stats，返回全局 BT 客户端统计信息。
func BTGlobalStats(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTGlobalStats")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	stats := btClient.GetGlobalStats()
	seeders := btClient.ListSeeders()
	minimal := gin.H{
		"total_up_bytes":   stats.TotalUp,
		"total_down_bytes": stats.TotalDown,
		"active_torrents":  stats.ActiveTorrents,
		"paused_torrents":  stats.PausedTorrents,
		"completed":        stats.Completed,
		"errors":           stats.Errors,
		"dht_nodes":        stats.DHTNodes,
		"seeding":          len(seeders),
		"seeding_hashes":   seeders,
	}

	log.LogInfo("ctrl-p2p: BTGlobalStats active=%d paused=%d completed=%d seeding=%d dht_nodes=%d",
		stats.ActiveTorrents, stats.PausedTorrents, stats.Completed, len(seeders), stats.DHTNodes)
	c.JSON(http.StatusOK, minimal)
}

// BTSeedTorrent 处理 POST /bt/seed/:infohash，开始为已完成的下载做种。
func BTSeedTorrent(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTSeedTorrent")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	if err := btClient.StartSeed(infohash); err != nil {
		log.LogWarn("ctrl-p2p: BTSeedTorrent %s failed: %v", infohash, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTSeedTorrent %s started", infohash)
	c.JSON(http.StatusOK, gin.H{"infohash": infohash, "status": "seeding"})
}

// BTStopSeed 处理 POST /bt/download/:infohash/unseed，停止为指定下载做种。
func BTStopSeed(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTStopSeed")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	if err := btClient.StopSeed(infohash); err != nil {
		log.LogWarn("ctrl-p2p: BTStopSeed %s failed: %v", infohash, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTStopSeed %s stopped", infohash)
	c.JSON(http.StatusOK, gin.H{"infohash": infohash, "status": "stopped"})
}

// BTDownloadList 处理 GET /bt/downloads，返回所有活跃和已完成的 BT 下载任务。
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

// BTDownloadTorrent 处理 GET /bt/download/:infohash/torrent，返回 .torrent 文件下载。
func BTDownloadTorrent(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDownloadTorrent")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	data, err := btClient.GetTorrentBytes(infohash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status := btClient.GetDownload(infohash)
	filename := "torrent.torrent"
	if status != nil && status.Name != "" {
		safe := strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || r == ':' || r == '"' || r == '<' || r == '>' || r == '|' || r == '?' || r == '*' {
				return '_'
			}
			return r
		}, status.Name)
		filename = safe + ".torrent"
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/x-bittorrent", data)
}

// BTDownloadMagnet 处理 GET /bt/download/:infohash/magnet，返回磁力链接。
func BTDownloadMagnet(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDownloadMagnet")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	infohash := c.Param("infohash")
	uri := btClient.GetMagnetURI(infohash)
	if uri == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "download not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"magnet_uri": uri})
}

// BTSeedCollection 处理 POST /bt/seed-collection，从合集创建种子并开始做种。
func BTSeedCollection(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTSeedCollection")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	var req struct {
		CollectionHash string `json:"collection_hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.CollectionHash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "collection_hash is required"})
		return
	}

	storageDir := c.MustGet("storageDir").(string)
	coll, err := collSvc.GetAnonByHash(req.CollectionHash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found: " + err.Error()})
		return
	}

	// 创建临时目录，写入合集文件供 BuildFromFilePath 使用。
	tmpDir, err := os.MkdirTemp("", "peerdrive-seed-")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp dir"})
		return
	}
	defer os.RemoveAll(tmpDir)

	name := coll.FriendlyName
	if name == "" {
		name = req.CollectionHash[:12]
	}
	rootDir := filepath.Join(tmpDir, name)
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create seed dir"})
		return
	}

	for _, entry := range coll.Entries {
		srcPath := filepath.Join(storageDir, entry.Hash[:2], entry.Hash)
		dstPath := filepath.Join(rootDir, entry.Path)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create dir: " + err.Error()})
			return
		}
		src, err := os.ReadFile(srcPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file " + entry.Path})
			return
		}
		if err := os.WriteFile(dstPath, src, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write file " + entry.Path})
			return
		}
	}

	// 从文件结构创建 torrent metainfo
	info := &metainfo.Info{
		PieceLength: 256 * 1024,
	}
	if err := info.BuildFromFilePath(rootDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build torrent info: " + err.Error()})
		return
	}

	mi := metainfo.MetaInfo{
		InfoBytes: bencode.MustMarshal(info),
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode torrent: " + err.Error()})
		return
	}
	torrentBytes := buf.Bytes()
	ih := mi.HashInfoBytes().HexString()

	// 将文件写入 BT 数据目录，使库能立即验证并完成
	dataRoot := filepath.Join(btClient.GetDownloadDir(), name)
	if err := os.MkdirAll(dataRoot, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create bt data dir"})
		return
	}
	for _, entry := range coll.Entries {
		srcPath := filepath.Join(storageDir, entry.Hash[:2], entry.Hash)
		dstPath := filepath.Join(dataRoot, entry.Path)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create bt dir: " + err.Error()})
			return
		}
		src, _ := os.ReadFile(srcPath)
		if err := os.WriteFile(dstPath, src, 0644); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to copy to bt dir: " + err.Error()})
			return
		}
	}

	// 添加种子（库会验证已存在的文件后标记完成）
	btClient.SetAutoSeed(ih)
	meta, err := btClient.AddTorrentBytes(torrentBytes)
	if err != nil {
		btClient.SetAutoSeed(ih) // 可能重复但无害，cleanup 在 handler 层面没有单独处理
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: BTSeedCollection created torrent %s (%s) from collection %s, auto-seeding",
		ih, meta.Name, req.CollectionHash)
	c.JSON(http.StatusOK, gin.H{
		"infohash": ih,
		"name":     meta.Name,
		"files":    len(meta.Files),
		"total":    meta.TotalSize,
		"status":   "seeding",
	})
}

// --- Port Forwarding ---

// CreateForwardSession 处理 POST /p2p/forward/create，注册本地服务端口供远程转发。
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

// ConnectForwardSession 处理 POST /p2p/forward/connect，连接远程对端并转发本地端口。
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

// ListForwardSessions 处理 GET /p2p/forward/list，返回所有活跃的端口转发会话。
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

// CloseForwardSession 处理 POST /p2p/forward/close，关闭指定 key 的端口转发会话。
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

// AuthStatus 处理 GET /p2p/auth/status，返回当前节点的认证状态。
func AuthStatus(c *gin.Context) {
	authenticated, _ := c.Get("authenticated")
	username, _ := c.Get("username")
	role, _ := c.Get("role")

	isAuth := false
	if a, ok := authenticated.(bool); ok {
		isAuth = a
	}

	uname := ""
	if u, ok := username.(string); ok {
		uname = u
	}

	r := ""
	if rl, ok := role.(string); ok {
		r = rl
	}

	c.JSON(http.StatusOK, gin.H{
		"authenticated": isAuth,
		"username":      uname,
		"role":          r,
	})
}

// ─── IPFS Compat Handlers ─────────────────────────────────────────

// InitIPFSCompatController 注入 IPFSCompatLayer 实例供 IPFS 兼容端点使用。
func InitIPFSCompatController(layer *service.IPFSCompatLayer) {
	log.LogDebug("ctrl-p2p: InitIPFSCompatController")
	ipfsCompatLayer = layer
}

// IPFSCompatStatus 处理 GET /ipfs，返回 IPFS 兼容层的状态信息。
func IPFSCompatStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: IPFSCompatStatus")
	if ipfsCompatLayer == nil {
		c.JSON(http.StatusOK, gin.H{
			"enabled":     false,
			"block_count": 0,
			"blockstore":  "",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":     ipfsCompatLayer.Enabled(),
		"block_count": ipfsCompatLayer.BlockCount(),
		"blockstore":  ipfsCompatLayer.BlockstorePath(),
	})
}

// IPFSCompatToggle 处理 POST /ipfs/toggle，启用或禁用 IPFS 兼容模式。
func IPFSCompatToggle(c *gin.Context) {
	log.LogDebug("ctrl-p2p: IPFSCompatToggle")
	if ipfsCompatLayer == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "IPFS compat not initialized"})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: IPFSCompatToggle invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.Enabled {
		if err := ipfsCompatLayer.Enable(); err != nil {
			log.LogError("ctrl-p2p: IPFSCompatToggle enable failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		ipfsCompatLayer.Disable()
	}

	log.LogInfo("ctrl-p2p: IPFSCompatToggle enabled=%v", ipfsCompatLayer.Enabled())
	c.JSON(http.StatusOK, gin.H{"enabled": ipfsCompatLayer.Enabled()})
}

// --- IPFS Pin & Gateway Handlers ------------------------------------

// PinCID handles POST /ipfs/pin/:cid, downloads CID from IPFS gateway and caches permanently.
func PinCID(c *gin.Context) {
	log.LogDebug("ctrl-p2p: PinCID")
	cidParam := c.Param("cid")
	if cidParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cid is required"})
		return
	}

	if ipfsGatewayProvider == nil || len(ipfsGatewayProvider.Gateways) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "IPFS gateway not configured"})
		return
	}

	// Check if already pinned.
	existing, _ := pinSvc.Get(cidParam)
	if existing != nil {
		c.JSON(http.StatusOK, gin.H{"status": "already_pinned", "pin": existing})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	data, err := ipfsGatewayProvider.FetchByCID(ctx, cidParam)
	if err != nil {
		log.LogError("ctrl-p2p: PinCID fetch %s failed: %v", cidParam, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "failed to fetch CID from gateways: " + err.Error()})
		return
	}

	// Compute SHA256 and cache locally.
	h := sha256.Sum256(data)
	hashStr := hex.EncodeToString(h[:])

	// Get storage dir from context.
	storageDir := ""
	if d, ok := c.Get("storageDir"); ok {
		storageDir, _ = d.(string)
	}

	if storageDir != "" {
		relPath := filepath.Join(hashStr[:2], hashStr)
		fullPath := filepath.Join(storageDir, relPath)
		_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
		_ = os.WriteFile(fullPath, data, 0644)

		_ = pinSvc.InsertMeta(hashStr, cidParam, int64(len(data)), relPath)

		// Also add to IPFS blockstore if IPFS compat is enabled.
		if ipfsCompatLayer != nil && ipfsCompatLayer.Enabled() {
			_ = ipfsCompatLayer.AddFile(hashStr)
		}
	}

	// Record the pin.
	_ = pinSvc.Insert(cidParam, hashStr, cidParam, int64(len(data)))

	log.LogInfo("ctrl-p2p: PinCID %s -> hash=%s size=%d", cidParam, hashStr, len(data))
	c.JSON(http.StatusOK, gin.H{
		"status": "pinned",
		"cid":    cidParam,
		"hash":   hashStr,
		"size":   len(data),
	})
}

// UnpinCID handles DELETE /ipfs/pin/:cid, unpins a CID.
func UnpinCID(c *gin.Context) {
	log.LogDebug("ctrl-p2p: UnpinCID")
	cidParam := c.Param("cid")
	if cidParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cid is required"})
		return
	}

	pin, err := pinSvc.Get(cidParam)
	if err != nil {
		log.LogError("ctrl-p2p: UnpinCID lookup failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pin == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pin not found"})
		return
	}

	if err := pinSvc.Remove(cidParam); err != nil {
		log.LogError("ctrl-p2p: UnpinCID remove failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: UnpinCID %s removed", cidParam)
	c.JSON(http.StatusOK, gin.H{"status": "unpinned", "cid": cidParam})
}

// ListPins handles GET /ipfs/pins, lists all pinned CIDs.
func ListPins(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ListPins")
	pins, err := pinSvc.List()
	if err != nil {
		log.LogError("ctrl-p2p: ListPins failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pins == nil {
		pins = []model.IPFSPin{}
	}
	c.JSON(http.StatusOK, gin.H{"pins": pins, "count": len(pins)})
}

// gwStatus reports the health of an IPFS gateway.
type gwStatus struct {
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
	Latency string `json:"latency,omitempty"`
}

// IPFSGatewayStatus handles GET /ipfs/gateways, checks health of each configured gateway.
func IPFSGatewayStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: IPFSGatewayStatus")
	if ipfsGatewayProvider == nil || len(ipfsGatewayProvider.Gateways) == 0 {
		c.JSON(http.StatusOK, gin.H{"gateways": []interface{}{}})
		return
	}

	results := make([]gwStatus, len(ipfsGatewayProvider.Gateways))
	for i, gw := range ipfsGatewayProvider.Gateways {
		results[i] = checkGateway(gw)
	}

	c.JSON(http.StatusOK, gin.H{"gateways": results})
}

// checkGateway pings a single IPFS gateway to check its health.
func checkGateway(gw string) gwStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(gw, "/")+"/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn", nil)
	if err != nil {
		return gwStatus{URL: gw, Healthy: false}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return gwStatus{URL: gw, Healthy: false}
	}
	resp.Body.Close()

	latency := time.Since(start)
	healthy := resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound
	return gwStatus{URL: gw, Healthy: healthy, Latency: latency.String()}
}

// ─── Node Operator (who runs this node) ───

// GetNodeOperator handles GET /p2p/node/operator
func GetNodeOperator(ctx *gin.Context) {
	op := nodestate.GetOperator()
	if op == "" {
		ctx.JSON(http.StatusOK, gin.H{
			"operator": nil,
			"note":     "anonymous node",
		})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"operator": op,
	})
}
