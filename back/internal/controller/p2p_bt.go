// Package controller provides HTTP handlers for BitTorrent download and seeding endpoints.
package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hana-ame/go-peerdrive-bt"
	"peerdrive/internal/log"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/gin-gonic/gin"
)

var btClient *p2p_bt.BTClient

// InitBTClient injects BTClient instance for BitTorrent download handlers.
func InitBTClient(c *p2p_bt.BTClient) {
	log.LogDebug("ctrl-p2p: InitBTClient")
	btClient = c
}

// --- BitTorrent Download Handlers ---

// BTTorrentUpload handles POST /bt/torrent, accepts .torrent file upload and starts download.
func BTTorrentUpload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: BTTorrentUpload")
	if btClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "BT client not available"})
		return
	}

	file, _, err := c.Request.FormFile("torrent")
	if err != nil {
		log.LogWarn("ctrl-p2p: BTTorrentUpload missing torrent file: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing torrent file"})
		return
	}
	defer file.Close()

	// Cap the read (red-team R2 DoS): header.Size comes straight from the multipart
	// Content-Length, which the caller controls. `make([]byte, header.Size)` on a body
	// that declares 10 GiB allocated 10 GiB before a single byte arrived. Also a single
	// Read() is not guaranteed to fill the buffer; io.ReadAll is.
	data, err := ReadCappedBody(file, MaxTorrentBytes)
	if err != nil {
		log.LogError("ctrl-p2p: BTTorrentUpload read failed: %v", err)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "torrent file too large"})
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
			writeInternalError(c, err)
			return
		}
		src, err := os.ReadFile(srcPath)
		if err != nil {
			writeInternalError(c, err)
			return
		}
		if err := os.WriteFile(dstPath, src, 0644); err != nil {
			writeInternalError(c, err)
			return
		}
	}

	// Build torrent metainfo from file structure
	info := &metainfo.Info{
		PieceLength: 256 * 1024,
	}
	if err := info.BuildFromFilePath(rootDir); err != nil {
		writeInternalError(c, err)
		return
	}

	mi := metainfo.MetaInfo{
		InfoBytes: bencode.MustMarshal(info),
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		writeInternalError(c, err)
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
			writeInternalError(c, err)
			return
		}
		src, _ := os.ReadFile(srcPath)
		if err := os.WriteFile(dstPath, src, 0644); err != nil {
			writeInternalError(c, err)
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

// maxTorrentBytes caps the .torrent body accepted by /bt/torrent and
// /sources/bt/torrent. Torrent files are normally well under 1 MiB; 16 MiB is a
// very generous ceiling that still stops a caller from declaring a gigabyte body.
const MaxTorrentBytes = 16 << 20

// ReadCappedBody reads r into memory, refusing bodies larger than max.
//
// Why it exists (red-team R2 DoS): multipart form parts report header.Size from
// the client-controlled Content-Length. `make([]byte, header.Size)` allocates that
// much before any byte arrives, and io.ReadAll has no limit at all — either is a
// one-request memory-exhaustion primitive on an authenticated route. Returns an
// error both when the body is over max and when it is exactly max (it may have
// more data after the cap), so callers cannot silently truncate a torrent.
func ReadCappedBody(r io.Reader, max int64) ([]byte, error) {
	lim := io.LimitReader(r, max+1)
	data, err := io.ReadAll(lim)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("body exceeds %d bytes", max)
	}
	return data, nil
}
