// Package controller provides HTTP handlers for P2P network related endpoints.
package controller

import (
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
	"peerdrive/internal/btloader"
	"peerdrive/internal/log"
)

// btDHTSt is the DHT availability snapshot taken at init. The DHT itself now
// lives inside the BT runtime (peerdrive_bt.dll), so the host only keeps this
// plus the shared btClient handle.
var btDHTSt btloader.DHTStatus

// InitBTController injects the BT runtime for DHT handlers.
//
// 发现背景：DHT 之前由 router 在启动路径上创建（PEERDRIVE_BT_DHT_ENABLE 默认 false，
// 但 p2p_bt.NewBTDHT 会 bootstrap 公共路由并 sleep 1s）。现在 DHT 只在 BT 运行时被
// 按需加载、且 DHTEnabled 时才存在；这里把「有没有 DHT」在装配期定死，免得每个
// 请求都往 DLL 里问一次。
func InitBTController(svc *btloader.Client) {
	log.LogDebug("ctrl-p2p: InitBTController")
	btClient = svc // shared handle: BT client and DHT are one runtime now
	btDHTSt = btloader.DHTStatus{}
	if svc == nil {
		return
	}
	btDHTSt = svc.DHTStatus()
	if !btDHTSt.Available && btDHTSt.Error != "" {
		log.LogWarn("ctrl-p2p: BT DHT unavailable: %s", btDHTSt.Error)
	}
}

// dhtReady reports whether DHT-backed endpoints can serve.
func dhtReady() bool {
	return btClient != nil && btDHTSt.Available
}

// --- BitTorrent DHT handlers ---

// BTDHTStatus handles GET /bt/status, returns BitTorrent DHT node status.
func BTDHTStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTDHTStatus")
	if !dhtReady() {
		log.LogInfo("ctrl-p2p: BTDHTStatus disabled")
		resp := gin.H{"enabled": false}
		if btDHTSt.Error != "" {
			resp["reason"] = btDHTSt.Error
		}
		c.JSON(http.StatusOK, resp)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":     true,
		"listen_addr": btDHTSt.ListenAddr,
		"num_nodes":   btDHTSt.NumNodes,
		"node_id":     btDHTSt.NodeID,
	})
}

// BTAnnounce handles POST /bt/announce, announces the specified hash on BitTorrent DHT.
func BTAnnounce(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTAnnounce")
	if !dhtReady() {
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
	if err := btClient.DHTAnnounce(req.Hash); err != nil {
		log.LogError("ctrl-p2p: BTAnnounce %s failed: %v", req.Hash, err)
		writeInternalError(c, err)
		return
	}
	log.LogInfo("ctrl-p2p: BTAnnounce %s successful", req.Hash)
	c.JSON(http.StatusOK, gin.H{"status": "announced on BT DHT"})
}

// BTFindProviders handles POST /bt/find, finds peers holding the specified hash on BitTorrent DHT.
func BTFindProviders(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTFindProviders")
	if !dhtReady() {
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
	peers, err := btClient.DHTFindProviders(req.Hash)
	if err != nil {
		log.LogError("ctrl-p2p: BTFindProviders %s failed: %v", req.Hash, err)
		writeInternalError(c, err)
		return
	}
	log.LogInfo("ctrl-p2p: BTFindProviders %s found %d peers", req.Hash, len(peers))
	c.JSON(http.StatusOK, gin.H{
		"hash":  req.Hash,
		"peers": peers,
		"count": len(peers),
	})
}

// --- BEP 44 (Arbitrary DHT Data Storage) ---

// BEP44Put handles POST /bt/bep44/put, stores immutable data on BT DHT via BEP 44.
func BEP44Put(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BEP44Put")
	if !dhtReady() {
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

	// Immutable put. The runtime hands back the 40-char hex target directly: the
	// [20]byte never has to cross the boundary.
	target, err := btClient.BEP44Put(rawData)
	if err != nil {
		log.LogError("ctrl-p2p: BEP44Put failed: %v", err)
		writeInternalError(c, err)
		return
	}

	log.LogInfo("ctrl-p2p: BEP44Put immutable target=%s size=%d", target, len(rawData))
	c.JSON(http.StatusOK, gin.H{
		"target":  target,
		"size":    len(rawData),
		"mutable": false,
	})
}

// BEP44Get handles POST /bt/bep44/get, reads immutable data from BT DHT via BEP 44.
func BEP44Get(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BEP44Get")
	if !dhtReady() {
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

	data, err := btClient.BEP44Get(req.Target)
	if err != nil {
		log.LogError("ctrl-p2p: BEP44Get failed: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"error": sanitizeErr(err, "request failed")})
		return
	}

	log.LogInfo("ctrl-p2p: BEP44Get target=%s size=%d", req.Target, len(data))
	c.JSON(http.StatusOK, gin.H{
		"data": base64.StdEncoding.EncodeToString(data),
		"size": len(data),
	})
}

// --- BEP 51 (Infohash Indexing) ---

// BEP51Sample handles GET /bt/bep51/sample, collects infohash samples from DHT via BEP 51.
func BEP51Sample(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BEP51Sample")
	if !dhtReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT DHT not enabled"})
		return
	}

	hexSamples, err := btClient.BEP51Sample(200)
	if err != nil {
		log.LogError("ctrl-p2p: BEP51Sample failed: %v", err)
		writeInternalError(c, err)
		return
	}

	log.LogInfo("ctrl-p2p: BEP51Sample collected %d infohashes", len(hexSamples))
	c.JSON(http.StatusOK, gin.H{
		"samples": hexSamples,
		"count":   len(hexSamples),
	})
}
