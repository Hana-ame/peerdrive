// Package controller provides HTTP handlers for P2P network related endpoints, including node info, peer management, BT DHT, dual network, port forwarding, etc.
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
	"peerdrive/internal/nodestate"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
)

var btSvc *p2p_bt.BTDHTService
var btClient *p2p_bt.BTClient

var pinSvc *service.PinService
var forwardPeer *transport.PeerJSService // PeerJS-based port forwarding (forward v2, see transport/forward.go)

// InitPeerScanner injects PeerScanner instance for P2P scan status queries.

// InitForwardController injects PeerJS service for port forwarding endpoints (forward v2).
// Forwarding semantics: this node uses PeerJS DataChannel to expose the remote peer's local port to itself --
// the create endpoint registers this node's authorization rule (key->port whitelist), the connect endpoint sets up
// a local loopback listener and opens a tunnel per connection. The legacy libp2p ForwardService has been removed.
// InitPinController injects PinService (M2 collection layer: pin endpoints no longer call repository directly).
func InitPinController(svc *service.PinService) {
	pinSvc = svc
}

func InitForwardController(svc *transport.PeerJSService) {
	log.LogDebug("ctrl-p2p: InitForwardController")
	forwardPeer = svc
}

// InitBTController injects BTDHTService instance for BitTorrent DHT handlers.
func InitBTController(svc *p2p_bt.BTDHTService) {
	log.LogDebug("ctrl-p2p: InitBTController")
	btSvc = svc
}

// InitBTClient injects BTClient instance for BitTorrent download handlers.
func InitBTClient(c *p2p_bt.BTClient) {
	log.LogDebug("ctrl-p2p: InitBTClient")
	btClient = c
}

// --- BitTorrent DHT handlers ---

// BTDHTStatus handles GET /bt/status, returns BitTorrent DHT node status.
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

// BTAnnounce handles POST /bt/announce, announces the specified hash on BitTorrent DHT.
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

// BTFindProviders handles POST /bt/find, finds peers holding the specified hash on BitTorrent DHT.
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

// DualAnnounce handles POST /p2p/dual/announce, announces on both IPFS DHT and BT DHT simultaneously.

// DualFindProviders handles POST /p2p/dual/find, searches providers on both IPFS DHT and BT DHT simultaneously.

// GetPeersDetail handles GET /p2p/peers/detail, returns detailed metadata for all tracked peers.

// GetPeerDetail handles GET /p2p/peers/detail/:peer_id, returns detailed metadata for the specified peer.

// GetP2PStats handles GET /p2p/stats, returns global P2P statistics (transfer volume, peer count, etc.).

// GetConnections handles GET /p2p/connections, returns connection counts by direction and scanner status.

// GetTopology handles GET /p2p/topology, returns the connection topology graph.

// GetConnectionQuality handles GET /p2p/quality, returns connection quality metrics for all peers.

// --- BEP 44 (Arbitrary DHT Data Storage) ---

// BEP44Put handles POST /bt/bep44/put, stores immutable data on BT DHT via BEP 44.
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

// BEP44Get handles POST /bt/bep44/get, reads immutable data from BT DHT via BEP 44.
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

// BEP51Sample handles GET /bt/bep51/sample, collects infohash samples from DHT via BEP 51.
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

// BTTorrentUpload handles POST /bt/torrent, accepts .torrent file upload and starts download.
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

// BTMagnetResolve handles POST /bt/magnet, resolves a magnet URI and starts a BitTorrent download.
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

// BTDownloadProgress handles GET /bt/download/:infohash, queries download progress for the specified infohash.
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

// BTPauseDownload handles POST /bt/download/:infohash/pause, pauses the specified download task.
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

// BTResumeDownload handles POST /bt/download/:infohash/resume, resumes a paused download task.
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

// BTRemoveDownload handles DELETE /bt/download/:infohash, removes the download task and its files.
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

// BTGlobalStats handles GET /bt/stats, returns global BT client statistics.
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

// BTSeedTorrent handles POST /bt/seed/:infohash, starts seeding a completed download.
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

// BTStopSeed handles POST /bt/download/:infohash/unseed, stops seeding the specified download.
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

// BTDownloadList handles GET /bt/downloads, returns all active and completed BT download tasks.
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

// BTDownloadTorrent handles GET /bt/download/:infohash/torrent, returns the .torrent file download.
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

// BTDownloadMagnet handles GET /bt/download/:infohash/magnet, returns the magnet link.
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

// BTSeedCollection handles POST /bt/seed-collection, creates a seed from a collection and starts seeding.
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

	// Create a temp directory and write collection files for BuildFromFilePath.
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

	// Build torrent metainfo from file structure
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

	// Write files to BT data directory so the library can immediately verify and complete
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

	// Add seed (library will verify existing files and mark as complete)
	btClient.SetAutoSeed(ih)
	meta, err := btClient.AddTorrentBytes(torrentBytes)
	if err != nil {
		btClient.SetAutoSeed(ih) // May duplicate but harmless; cleanup is not handled separately at the handler level
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

// --- Port Forwarding (forward v2: PeerJS DataChannel tunnels, see transport/forward.go) ---

// fwdListener local proxy listener (established by connect endpoint): each local
// TCP connection received on the loopback port -> one forwarding tunnel (OpenForward).
type fwdListener struct {
	key        string
	targetPeer string
	ln         net.Listener
}

var (
	fwdListenersMu sync.Mutex
	fwdListeners   = map[string]*fwdListener{} // key: "<target_peer>:<local_port>"
)

// CreateForwardSession handles POST /p2p/forward/create, registers this node's forwarding authorization rule
// (key -> port whitelist, runtime in-memory table; static rules go through config PEERDRIVE_FORWARD_RULES).
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

// ConnectForwardSession handles POST /p2p/forward/connect: starts a local loopback listener,
// each local TCP connection goes through a forwarding tunnel to the target peer's authorized port (when port=0, the target is determined by rules).
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
		Port       int    `json:"port"` // Target port (optional: 0 = target node's unique port by rules)
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
	// Idempotent: if a listener already exists for the same key+port, return directly (frontend retries/duplicate clicks won't break)
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

// acceptForwardTunnels opens a forwarding tunnel for each local TCP connection and pipes bidirectionally.
// Semantics: if connection establishment fails (bad key/insufficient permissions/tunnel occupied), only that connection is dropped; listening continues.
func acceptForwardTunnels(fl *fwdListener, targetPort int) {
	defer fl.ln.Close()
	for {
		tcp, err := fl.ln.Accept()
		if err != nil {
			return // listener closed
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

// pipeTCPForward pipes bidirectionally between local TCP <-> forwarding tunnel. Either side EOF/error closes both directions:
// tunnel-side close triggers the pump's fwd-close notification to the peer to clear the slot (see transport/forward.go).
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

// ListForwardSessions handles GET /p2p/forward/list, returns local active listeners and tunnels.
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

// CloseForwardSession handles POST /p2p/forward/close, closes the local listener for the specified key
// (active tunnels are naturally closed at both ends; use peer_id to disconnect all tunnels to a specified peer).
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

// AuthStatus handles GET /p2p/auth/status, returns the current node's authentication status.
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

	// operator and username are not the same thing: username comes from the request context (the caller of this request),
	// operator is the operator account registered after the node logs into regserver (anonymized collection Owner uses this value).
	// The frontend needs it to determine whether the "only me" tier is available (Owner empty = no one can read).
	c.JSON(http.StatusOK, gin.H{
		"authenticated": isAuth,
		"username":      uname,
		"role":          r,
		"operator":      nodestate.GetOperator(),
	})
}

// ─── IPFS Compat Handlers ─────────────────────────────────────────

// InitIPFSCompatController injects IPFSCompatLayer instance for IPFS compatibility endpoints.

// IPFSCompatStatus handles GET /ipfs, returns IPFS compatibility layer status info.

// IPFSCompatToggle handles POST /ipfs/toggle, enables or disables IPFS compatibility mode.

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
