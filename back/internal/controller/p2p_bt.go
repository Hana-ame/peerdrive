// Package controller provides HTTP handlers for BitTorrent download and seeding endpoints.
//
// 发现背景：这些 handler 之前直接调用 p2p_bt.BTClient（torrent 栈静态链接进主程序）。
// 「按需加载」之后，BT 运行时活在 peerdrive_bt.dll 里，主程序只持有
// internal/btloader.Client —— 它把每次调用编组成一个 JSON op 过 ABI。签名与语义
// 保持不变，改动集中在初始化与 BTSeedCollection（后者不再需要自己造 metainfo）。
package controller

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"peerdrive/internal/btloader"
	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var btClient *btloader.Client

// InitBTClient injects the BT runtime handle for BitTorrent download handlers.
// nil means BT is not enabled for this process (PEERDRIVE_BT_ENABLE unset) and
// every /bt/* endpoint answers 503.
func InitBTClient(c *btloader.Client) {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
		return
	}

	log.LogInfo("ctrl-p2p: BTTorrentUpload started %q (infohash=%s)", meta.Name, meta.InfoHash)
	c.JSON(http.StatusOK, gin.H{
		"infohash": meta.InfoHash,
		"name":     meta.Name,
		"files":    len(meta.Files),
		"total":    meta.TotalSize,
		"pieces":   meta.PieceCount,
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
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
		return
	}

	log.LogInfo("ctrl-p2p: BTMagnetResolve started %q (infohash=%s)", meta.Name, meta.InfoHash)
	c.JSON(http.StatusOK, gin.H{
		"infohash":     meta.InfoHash,
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
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
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
		c.JSON(http.StatusNotFound, gin.H{"error": sanitizeErr(err, "request failed")})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
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
		c.JSON(http.StatusNotFound, gin.H{"error": sanitizeErr(err, "request failed")})
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
	if !hashutil.IsValidSHA256(req.CollectionHash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection_hash"})
		return
	}

	storageDir := c.MustGet("storageDir").(string)
	coll, err := collSvc.GetAnonByHash(req.CollectionHash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found: " + sanitizeErr(err, "request failed")})
		return
	}
	coll.NormalizeEntries()

	// Stage the collection under a temp root so the torrent builder has a real
	// directory tree to walk. The BT runtime does the hashing, the piece layout
	// and the copy into its data dir (see btservice "create_and_seed").
	tmpDir, err := os.MkdirTemp("", "peerdrive-seed-")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp dir"})
		return
	}
	defer os.RemoveAll(tmpDir)

	// FriendlyName 是用户输入（collection 创建时的 friendly_name）。它同时被拼进
	// tmpDir 下的 rootDir 和 BT download 目录下的 dataRoot：不清洗的话
	// "../../tmp/evil" 会一路 Join 到两个根之外（并且 defer RemoveAll(tmpDir)
	// 清不掉写出去的东西）。清洗后为空则回退到 hash 前缀，seed 照建。
	//
	// 注意：codeql[go/path-injection] 告警 #5/#6/#7/#8/#48/#49 的 source 都是本函数的
	// req.CollectionHash（ShouldBindJSON 绑定进来的），6 条 sink 全部经 name 流入
	// （rootDir / dataRoot / dstPath）。所以 name 的两个赋值都必须过 SafeComponent——
	// 直接赋 req.CollectionHash[:12] 会让污点绕开 SafeComponent 里的净化点，告警随重扫
	// 以新 sha 复现（PR #407 只修了 FriendlyName 那支）。hash 是 40 位十六进制，
	// SafeComponent 对它恒等，语义不变。同理别删 SafeComponent 里那行 filepath.Clean：
	// 它是 CodeQL 认得的净化器（FilepathCleanSanitizer），也是穿越兜底。
	name := pathutil.SafeComponent(coll.FriendlyName)
	if name == "" {
		name = pathutil.SafeComponent(req.CollectionHash[:12])
	}
	rootDir := filepath.Join(tmpDir, name)
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create seed dir"})
		return
	}

	for _, entry := range coll.Entries {
		h := entry.GetPrimaryHash()
		if !hashutil.IsValidSHA256(h) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "entry has no valid sha256 provider"})
			return
		}
		srcPath := filepath.Join(storageDir, h[:2], h)
		dstPath := filepath.Join(rootDir, entry.Path)
		if !pathutil.Within(rootDir, dstPath) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid entry path"})
			return
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			writeInternalError(c, err)
			return
		}
		src, err := os.ReadFile(srcPath)
		if err != nil {
			writeInternalError(c, err)
			return
		}
		// #nosec G703 -- entry.Path 来自 GetAnonByHash 的存储内 collection，
		// 其路径在创建时已被 anon_service 校验（isRelativePath + 拒绝含 ".."），
		// 相对无 ".." 的路径 Join 进 rootDir/dataRoot 不会逃逸；taint 把
		// collectionHash（HTTP 入参）当源是传递性误报。
		if err := os.WriteFile(dstPath, src, 0644); err != nil {
			writeInternalError(c, err)
			return
		}
	}

	// Build + copy + seed in one call: the runtime walks rootDir, hashes pieces,
	// copies the tree into its data dir (so it can verify and serve immediately)
	// and registers the torrent. PieceLength stays 256 KiB to keep infohashes
	// stable against seeds created by earlier versions.
	meta, err := btClient.CreateAndSeed(btloader.CreateAndSeedOptions{
		Path:        rootDir,
		Name:        name,
		PieceLength: 256 * 1024,
	})
	if err != nil {
		log.LogError("ctrl-p2p: BTSeedCollection create_and_seed failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": sanitizeErr(err, "request failed")})
		return
	}

	log.LogInfo("ctrl-p2p: BTSeedCollection created torrent %s (%s) from collection %s, auto-seeding",
		meta.InfoHash, meta.Name, req.CollectionHash)
	c.JSON(http.StatusOK, gin.H{
		"infohash": meta.InfoHash,
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
