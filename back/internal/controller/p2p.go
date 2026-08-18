// Package controller 提供 P2P 网络相关 HTTP 处理函数，包括节点信息、对端管理、BT DHT、双网络、端口转发等端点。
package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
)

var btSvc *p2p_bt.BTDHTService
var btClient *p2p_bt.BTClient

var pinSvc *service.PinService
var forwardPeer *transport.PeerJSService // PeerJS 版端口转发（forward v2，见 transport/forward.go）

// InitPeerScanner 注入 PeerScanner 实例供 P2P 扫描状态查询使用。

// InitForwardController 注入 PeerJS 服务供端口转发端点使用（forward v2）。
// 转发语义：本节点经 PeerJS DataChannel 把远端 peer 的本地端口暴露给自己——
// create 端点登记本节点授权规则（key→端口白名单），connect 端点建立本地
// loopback 监听并逐连接开隧道。legacy 的 libp2p ForwardService 已删除。
// InitPinController 注入 PinService（M2 收层：pin 端点不再直调 repository）。
func InitPinController(svc *service.PinService) {
	pinSvc = svc
}

func InitForwardController(svc *transport.PeerJSService) {
	log.LogDebug("ctrl-p2p: InitForwardController")
	forwardPeer = svc
}

// InitBTController 注入 BTDHTService 实例供 BitTorrent DHT 处理函数使用。
func InitBTController(svc *p2p_bt.BTDHTService) {
	log.LogDebug("ctrl-p2p: InitBTController")
	btSvc = svc
}

// InitBTClient 注入 BTClient 实例供 BitTorrent 下载处理函数使用。
func InitBTClient(c *p2p_bt.BTClient) {
	log.LogDebug("ctrl-p2p: InitBTClient")
	btClient = c
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

// DualFindProviders 处理 POST /p2p/dual/find，同时在 IPFS DHT 和 BT DHT 上查找 providers。

// GetPeersDetail 处理 GET /p2p/peers/detail，返回所有已跟踪对端的详细元数据。

// GetPeerDetail 处理 GET /p2p/peers/detail/:peer_id，返回指定对端的详细元数据。

// GetP2PStats 处理 GET /p2p/stats，返回全局 P2P 统计信息（传输量、对端数等）。

// GetConnections 处理 GET /p2p/connections，返回按方向统计的连接数和扫描器状态。

// GetTopology 处理 GET /p2p/topology，返回连接拓扑图。

// GetConnectionQuality 处理 GET /p2p/quality，返回所有对端的连接质量指标。

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

// --- Port Forwarding（forward v2：PeerJS DataChannel 隧道，见 transport/forward.go）---

// fwdListener 本端代理监听（connect 端点建立）：本地 loopback 端口收到的每条
// TCP 连接 → 一条转发隧道（OpenForward）。
type fwdListener struct {
	key        string
	targetPeer string
	ln         net.Listener
}

var (
	fwdListenersMu sync.Mutex
	fwdListeners   = map[string]*fwdListener{} // key: "<target_peer>:<local_port>"
)

// CreateForwardSession 处理 POST /p2p/forward/create，登记本节点转发授权规则
// （key → 端口白名单，运行时内存表；静态规则走配置 PEERDRIVE_FORWARD_RULES）。
func CreateForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CreateForwardSession")
	if forwardPeer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}
	var req struct {
		Key  string `json:"key"`
		Port int    `json:"port"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if err := forwardPeer.AddForwardRule(req.Key, req.Port); err != nil {
		log.LogError("ctrl-p2p: CreateForwardSession failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: CreateForwardSession rule added port=%d", req.Port)
	c.JSON(http.StatusOK, gin.H{"status": "rule-added", "port": req.Port})
}

// ConnectForwardSession 处理 POST /p2p/forward/connect：本地 loopback 起监听，
// 每条本地 TCP 经一条转发隧道打到目标 peer 的授权端口（port=0 时目标按规则决定）。
func ConnectForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ConnectForwardSession")
	if forwardPeer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}
	var req struct {
		Key        string `json:"key"`
		TargetPeer string `json:"target_peer"`
		LocalPort  int    `json:"local_port"`
		Port       int    `json:"port"` // 目标端口（可选：0 = 目标节点按规则唯一端口）
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: ConnectForwardSession invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" || req.TargetPeer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key and target_peer are required"})
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid local_port"})
		return
	}
	if req.Port != 0 && (req.Port <= 0 || req.Port > 65535) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid port"})
		return
	}
	// 幂等：同 key+端口已有监听则直接返回（前端重试/重复点击不炸）
	key2 := fmt.Sprintf("%s:%d", req.TargetPeer, req.LocalPort)
	fwdListenersMu.Lock()
	if _, ok := fwdListeners[key2]; ok {
		fwdListenersMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"status": "connected", "local_port": req.LocalPort})
		return
	}
	fwdListenersMu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", req.LocalPort))
	if err != nil {
		log.LogWarn("ctrl-p2p: ConnectForwardSession listen failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fl := &fwdListener{key: req.Key, targetPeer: req.TargetPeer, ln: ln}
	fwdListenersMu.Lock()
	fwdListeners[key2] = fl
	fwdListenersMu.Unlock()
	go acceptForwardTunnels(fl, req.Port)
	log.LogInfo("ctrl-p2p: ConnectForwardSession listening 127.0.0.1:%d -> %s", req.LocalPort, req.TargetPeer)
	c.JSON(http.StatusOK, gin.H{"status": "connected", "local_port": req.LocalPort})
}

// acceptForwardTunnels 为每个本地 TCP 连接开一条转发隧道并双向透传。
// 语义：连接建立失败（坏 key/端口越权/隧道占用）只断这一条，监听继续。
func acceptForwardTunnels(fl *fwdListener, targetPort int) {
	defer fl.ln.Close()
	for {
		tcp, err := fl.ln.Accept()
		if err != nil {
			return // 监听关闭
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			tun, err := forwardPeer.OpenForward(ctx, fl.targetPeer, fl.key, targetPort)
			if err != nil {
				log.LogWarn("ctrl-p2p: forward tunnel to %s failed: %v", fl.targetPeer, err)
				tcp.Close()
				return
			}
			go pipeTCPForward(tcp, tun)
		}()
	}
}

// pipeTCPForward 双向透传本地 TCP ↔ 转发隧道。任一侧 EOF/错误即双向关闭：
// 隧道侧关闭会触发 pump 的 fwd-close 通知对端清槽（见 transport/forward.go）。
func pipeTCPForward(tcp net.Conn, tun net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(tcp, tun)
		done <- struct{}{}
	}()
	io.Copy(tun, tcp)
	done <- struct{}{}
	<-done
	tcp.Close()
	tun.Close()
}

// ListForwardSessions 处理 GET /p2p/forward/list，返回本端活跃监听与隧道。
func ListForwardSessions(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ListForwardSessions")
	listeners := []gin.H{}
	fwdListenersMu.Lock()
	for k, fl := range fwdListeners {
		listeners = append(listeners, gin.H{"id": k, "target_peer": fl.targetPeer, "local_port": fl.ln.Addr().String()})
	}
	fwdListenersMu.Unlock()
	tunnels := []gin.H{}
	if forwardPeer != nil {
		for _, t := range forwardPeer.ListForwardStreams() {
			tunnels = append(tunnels, gin.H{"peer_id": t.PeerID, "port": t.Port, "key_id": t.KeyID})
		}
	}
	c.JSON(http.StatusOK, gin.H{"listeners": listeners, "tunnels": tunnels})
}

// CloseForwardSession 处理 POST /p2p/forward/close，关闭指定 key 的本端监听
// （活跃隧道由隧道两端自然收尾；断开指定 peer 的全部隧道可用 peer_id）。
func CloseForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CloseForwardSession")
	var req struct {
		Key    string `json:"key"`
		PeerID string `json:"peer_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	closed := 0
	fwdListenersMu.Lock()
	for k, fl := range fwdListeners {
		if req.Key != "" && fl.key != req.Key {
			continue
		}
		fl.ln.Close()
		delete(fwdListeners, k)
		closed++
	}
	fwdListenersMu.Unlock()
	if forwardPeer != nil && req.PeerID != "" {
		forwardPeer.CloseForwardStream(req.PeerID)
	}
	if closed == 0 && req.PeerID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no matching forward session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "closed", "closed_listeners": closed})
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

// IPFSCompatStatus 处理 GET /ipfs，返回 IPFS 兼容层的状态信息。

// IPFSCompatToggle 处理 POST /ipfs/toggle，启用或禁用 IPFS 兼容模式。

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
