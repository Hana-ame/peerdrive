// Download controller — content-addressable file download by SHA256 hash.
// First call InitDownloader(svc) to register the service.Downloader instance.
// Flow: validate hash → Downloader.GetFileStream → parse meta.is_gzip → Content-Encoding
// Routes:
//   GET /sha256sum/:sha256   — download file by SHA256 hash (with multi-protocol fallback)
//   GET /download/:hash       — universal multi-protocol download (X-Protocol header)
//   GET /download/:hash/sources — check protocol availability
//   POST /download/:hash/refresh — force re-check all protocols

package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"peerdrive/internal/downloader"
	"peerdrive/internal/egress"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var universalDownloader *downloader.UniversalDownloader
var ipfsGatewayProvider *provider.IPFSProvider

// InitUniversalDownloader injects the UniversalDownloader instance for multi-protocol download endpoints.
func InitUniversalDownloader(d *downloader.UniversalDownloader) {
	universalDownloader = d
}

// InitIPFSProvider injects the IPFS gateway provider for DownloadByCID fallback.
// Pass nil when IPFS gateway is disabled or not configured.
func InitIPFSProvider(p *provider.IPFSProvider) {
	ipfsGatewayProvider = p
}

// DownloadBySHA256 handles GET /sha256sum/:sha256, preferring UniversalDownloader multi-protocol download, otherwise falls back to original logic.
func DownloadBySHA256(c *gin.Context) {
	DownloadBySHA256Internal(c, c.Param("sha256"))
}

// DownloadBySHA256Internal is the internal implementation of DownloadBySHA256, also used by other controllers to fetch file streams by hash.
func DownloadBySHA256Internal(c *gin.Context, hash string) {
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 format"})
		return
	}

	// If the universal downloader is wired in, use it.
	if universalDownloader != nil {
		ctx := c.Request.Context()
		data, protocol, err := universalDownloader.Download(ctx, hash)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.Header("X-Protocol", protocol)

		meta, _ := fileSvc.GetMeta(hash)
		fn := hash
		if meta != nil && meta.Filename != "" {
			fn = meta.Filename
		}
		if c.Query("inline") == "1" {
			c.Header("Content-Disposition", "inline; filename="+fn)
		} else {
			c.Header("Content-Disposition", "attachment; filename="+fn)
		}
		if meta != nil && meta.Gziped {
			c.Header("Content-Encoding", "gzip")
		}
		if meta != nil && meta.Type == model.FileTypeAnonCollection {
			c.Header("X-Peerdrive-Collection", "true")
		}
		// Handle Range requests for chunked download
		if rng := c.GetHeader("Range"); rng != "" {
			if handled := handleRangeRequest(c, data, rng); handled {
				return
			}
		}
		c.Data(http.StatusOK, "application/octet-stream", data)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
}

// DownloadBySHA256Local handles GET /sha256sum/:sha256, reading directly from local storage with RFC 7233 streaming and Range support.
func DownloadBySHA256Local(c *gin.Context) {
	hash := c.Param("sha256")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 format"})
		return
	}
	meta, err := fileSvc.GetMeta(hash)
	if err != nil || meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	sd, _ := c.Get("storageDir")
	storageDir, _ := sd.(string)
	path := filepath.Join(storageDir, hash[:2], hash)
	f, err := os.Open(path)
	if err != nil {
		if localPath, pErr := fileSvc.GetLocalPath(hash); pErr == nil {
			if f2, oErr := os.Open(localPath); oErr == nil {
				f = f2
				path = localPath
			}
		}
	}
	if f == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found on disk"})
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stat failed"})
		return
	}

	fn := hash
	if meta.Filename != "" {
		fn = meta.Filename
	}
	if c.Query("inline") == "1" {
		c.Header("Content-Disposition", "inline; filename="+fn)
	} else {
		c.Header("Content-Disposition", "attachment; filename="+fn)
	}
	if meta.Gziped {
		c.Header("Content-Encoding", "gzip")
	}
	// http.ServeContent 原生支持流式传输与 RFC 7233 Range / 206 Partial Content / ETag / If-Range
	http.ServeContent(c.Writer, c.Request, fn, st.ModTime(), f)
}

// DownloadByCID handles GET /ipfs/:cid, looking up the file by its IPFS CID and
// streaming it back with an X-CID header.  Falls back to public IPFS gateways
// when the CID is not in local storage and IPFS gateway fetching is enabled.
func DownloadByCID(c *gin.Context) {
	searchCID := c.Param("cid")
	meta, err := fileSvc.GetMetaByCID(searchCID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if meta == nil {
		// IPFS gateway fallback: try fetching from public gateways.
		if ipfsGatewayProvider != nil && len(ipfsGatewayProvider.Gateways) > 0 {
			ctx := c.Request.Context()
			data, fetchErr := ipfsGatewayProvider.FetchByCID(ctx, searchCID)
			if fetchErr == nil {
				// Persist + register metadata/provider (M2 layering: original inline repository triplet)
				hashStr, err := fileSvc.ImportGatewayData(searchCID, data)
				if err == nil {
					c.Header("X-CID", searchCID)
					DownloadBySHA256Internal(c, hashStr)
					return
				}
			}
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found by cid"})
		return
	}

	c.Header("X-CID", searchCID)
	DownloadBySHA256Internal(c, meta.Hash)
}

// handleRangeRequest handles HTTP Range requests including:
//   - Standard range: bytes=START-END
//   - Suffix range: bytes=-N
//   - 0-byte file: returns full file (no partial)
//   - Open-ended range: bytes=N- (returns from N to end)
//
// Returns true if the range was handled (response already written).
func handleRangeRequest(c *gin.Context, data []byte, rangeHeader string) bool {
	total := int64(len(data))
	if total == 0 {
		return false
	}

	// Quick check for unsatisfiable range before handing off to ParseRange
	rangeBody := strings.TrimPrefix(rangeHeader, "bytes=")
	if rangeBody != rangeHeader {
		parts := strings.SplitN(rangeBody, "-", 2)
		if len(parts) == 2 && parts[0] != "" {
			if start, err := strconv.ParseInt(parts[0], 10, 64); err == nil && start >= total {
				c.Header("Content-Range", fmt.Sprintf("bytes */%d", total))
				c.Status(http.StatusRequestedRangeNotSatisfiable)
				return true
			}
		}
	}

	start, end, ok := parseRangeHeader(rangeHeader, total)
	if !ok {
		return false
	}

	length := end - start + 1
	c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
	c.Header("Content-Length", fmt.Sprintf("%d", length))
	c.Status(http.StatusPartialContent)
	c.Writer.Write(data[start : end+1])
	return true
}

// ─── Universal download endpoint ───────────────────────────────────────────
// ─── Universal download endpoint ───────────────────────────────────────────

// UniversalDownload handles GET /download/:hash, using the universal downloader to fetch files across protocols.
func UniversalDownload(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}
	if universalDownloader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "universal downloader not initialized"})
		return
	}

	ctx := c.Request.Context()
	data, protocol, err := universalDownloader.Download(ctx, hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.Header("X-Protocol", protocol)
	c.Data(http.StatusOK, "application/octet-stream", data)
}

// UniversalDownloadSources handles GET /download/:hash/sources, listing all available protocol sources.
func UniversalDownloadSources(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}
	if universalDownloader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "universal downloader not initialized"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	sources := universalDownloader.CheckSources(ctx, hash)
	c.JSON(http.StatusOK, sources)
}

// UniversalDownloadRefresh handles POST /download/:hash/refresh, clearing local cache and re-running the download pipeline.
func UniversalDownloadRefresh(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}
	if universalDownloader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "universal downloader not initialized"})
		return
	}

	// Clear cached data so the pipeline re-fetches from the network.
	universalDownloader.ClearLocalCache(hash)
	log.LogDebug("ctrl-download: cleared local cache for %s, re-running pipeline", hash)

	ctx := c.Request.Context()
	data, protocol, err := universalDownloader.Download(ctx, hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.Header("X-Protocol", protocol)
	c.Data(http.StatusOK, "application/octet-stream", data)
}

// parseRangeHeader parses an HTTP Range header and returns start/end byte indices and total size.
// Supports standard range (bytes=N-M), open-ended (bytes=N-), and suffix (bytes=-N).
// Delegated to egress.ParseRangeHeader for unified RFC 7233 compliance across all egress outlets.
func parseRangeHeader(rangeVal string, fileSize int64) (start, end int64, ok bool) {
	offset, size, isSatisfiable, valid := egress.ParseRangeHeader(rangeVal, fileSize)
	if !valid || !isSatisfiable {
		return 0, 0, false
	}
	return offset, offset + size - 1, true
}
