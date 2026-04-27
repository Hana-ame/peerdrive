package controller

import (
	"context"
	"net/http"
	"time"

	"peerdrive/internal/log"

	"github.com/gin-gonic/gin"
)

// --- Resume-able Download Handlers ---

// ResumeDownload starts or resumes a download.
//
//	POST /p2p/download/resume  {hash, target_path}
func ResumeDownload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ResumeDownload")
	if resumeMgr == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "resume manager not available"})
		return
	}
	var req struct {
		Hash       string `json:"hash"`
		TargetPath string `json:"target_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: ResumeDownload invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if len(req.Hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash (expected 64 hex chars)"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()

	path, err := resumeMgr.ResumeDownload(ctx, req.Hash, req.TargetPath)
	if err != nil {
		log.LogError("ctrl-p2p: ResumeDownload %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: ResumeDownload %s completed to %s", req.Hash, path)
	c.JSON(http.StatusOK, gin.H{
		"hash":        req.Hash,
		"target_path": path,
		"status":      "completed",
	})
}

// DownloadProgress returns the progress of a current or saved download.
//
//	GET /p2p/download/progress/:hash
func DownloadProgress(c *gin.Context) {
	log.LogDebug("ctrl-p2p: DownloadProgress")
	hash := c.Param("hash")
	if len(hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash"})
		return
	}
	if resumeMgr == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "resume manager not available"})
		return
	}

	progress := resumeMgr.GetProgress(hash)
	if progress == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no download progress found"})
		return
	}

	pct := 0.0
	if progress.TotalSize > 0 {
		pct = float64(progress.ReceivedSize) / float64(progress.TotalSize) * 100
	}

	log.LogInfo("ctrl-p2p: DownloadProgress %s: %.1f%%", hash, pct)
	c.JSON(http.StatusOK, gin.H{
		"hash":         progress.Hash,
		"total":        progress.TotalSize,
		"received":     progress.ReceivedSize,
		"percent":      pct,
		"last_chunk":   progress.LastChunk,
		"chunks_total": progress.ChunksTotal,
		"chunks_done":  progress.ChunksDone,
		"peers_used":   progress.PeersUsed,
		"started_at":   progress.StartedAt,
		"updated_at":   progress.UpdatedAt,
	})
}

// CancelDownload cancels an active download and cleans up partial files.
//
//	POST /p2p/download/cancel/:hash
func CancelDownload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CancelDownload")
	if resumeMgr == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "resume manager not available"})
		return
	}
	hash := c.Param("hash")
	if len(hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash"})
		return
	}

	if err := resumeMgr.CancelDownload(hash); err != nil {
		log.LogError("ctrl-p2p: CancelDownload %s failed: %v", hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: CancelDownload %s successful", hash)
	c.JSON(http.StatusOK, gin.H{
		"hash":   hash,
		"status": "cancelled",
	})
}

// --- Multi-Peer Download Handlers ---

// MultiPeerDownload starts a multi-source parallel download.
//
//	POST /p2p/download/multipeer  {hash, target_path}
func MultiPeerDownload(c *gin.Context) {
	log.LogDebug("ctrl-p2p: MultiPeerDownload")
	if multiPeerDl == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "multi-peer downloader not available"})
		return
	}
	var req struct {
		Hash       string `json:"hash"`
		TargetPath string `json:"target_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: MultiPeerDownload invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if len(req.Hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash (expected 64 hex chars)"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()

	path, err := multiPeerDl.MultiPeerDownload(ctx, req.Hash, req.TargetPath)
	if err != nil {
		log.LogError("ctrl-p2p: MultiPeerDownload %s failed: %v", req.Hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: MultiPeerDownload %s completed to %s", req.Hash, path)
	c.JSON(http.StatusOK, gin.H{
		"hash":        req.Hash,
		"target_path": path,
		"status":      "completed",
	})
}

// DownloadSources lists all available sources for a hash.
//
//	GET /p2p/download/sources/:hash
func DownloadSources(c *gin.Context) {
	log.LogDebug("ctrl-p2p: DownloadSources")
	if multiPeerDl == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "multi-peer downloader not available"})
		return
	}
	hash := c.Param("hash")
	if len(hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	sources, err := multiPeerDl.GetSources(ctx, hash)
	if err != nil {
		log.LogError("ctrl-p2p: DownloadSources %s failed: %v", hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ipfsCount := 0
	btCount := 0
	for _, s := range sources {
		switch s.Network {
		case "ipfs":
			ipfsCount++
		case "bt":
			btCount++
		}
	}

	log.LogInfo("ctrl-p2p: DownloadSources %s found %d sources (%d IPFS, %d BT)", hash, len(sources), ipfsCount, btCount)
	c.JSON(http.StatusOK, gin.H{
		"hash":       hash,
		"sources":    sources,
		"total":      len(sources),
		"ipfs_count": ipfsCount,
		"bt_count":   btCount,
	})
}

// MultiPeerProgress returns the current multi-peer download progress.
//
//	GET /p2p/download/multipeer/progress/:hash
func MultiPeerProgress(c *gin.Context) {
	log.LogDebug("ctrl-p2p: MultiPeerProgress")
	if multiPeerDl == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "multi-peer downloader not available"})
		return
	}
	hash := c.Param("hash")
	if len(hash) != 64 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hash"})
		return
	}

	p := multiPeerDl.GetProgress(hash)
	if p == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "no active multi-peer download"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"hash":         p.Hash,
		"total_size":   p.TotalSize,
		"received":     p.Received,
		"percent":      p.Percent,
		"chunks_total": p.ChunksTotal,
		"chunks_done":  p.ChunksDone,
		"peers":        p.Peers,
		"elapsed":      p.Elapsed,
		"done":         p.Done,
		"error":        p.Error,
	})
}
