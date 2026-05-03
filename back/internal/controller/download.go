// 下载控制器 — 通过 SHA256 哈希进行内容寻址文件下载。
// 先调用 InitDownloader(svc) 注册 service.Downloader 实例。
// 流程：校验 hash → Downloader.GetFileStream → 解析 meta.is_gzip → Content-Encoding
// 路由：
//   GET /sha256sum/:sha256   — 按 SHA256 哈希下载文件（含多协议回退）
//   GET /download/:hash       — 通用多协议下载（X-Protocol 头）
//   GET /download/:hash/sources — 检查各协议可用性
//   POST /download/:hash/refresh — 强制重新检查所有协议

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"strconv"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var universalDownloader *service.UniversalDownloader
var ipfsGatewayProvider *provider.IPFSProvider

// InitUniversalDownloader 注入 UniversalDownloader 实例供多协议下载端点使用。
func InitUniversalDownloader(d *service.UniversalDownloader) {
	universalDownloader = d
}

// InitIPFSProvider 注入 IPFS 网关提供者供 DownloadByCID 回退使用。
// 当 IPFS 网关被禁用或未配置时传入 nil。
func InitIPFSProvider(p *provider.IPFSProvider) {
	ipfsGatewayProvider = p
}

// DownloadBySHA256 处理 GET /sha256sum/:sha256，优先使用 UniversalDownloader 多协议下载，否则回退到原始逻辑。
func DownloadBySHA256(c *gin.Context) {
	DownloadBySHA256Internal(c, c.Param("sha256"))
}

// DownloadBySHA256Internal 是 DownloadBySHA256 的内部实现，也供其他控制器按 hash 获取文件流。
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

		meta, _ := repository.GetFileMeta(hash)
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
		if meta != nil && meta.Type == repository.FileTypeAnonCollection {
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

// DownloadBySHA256Local 处理 GET /sha256sum/:sha256，仅从本地存储读取，不含 P2P 回退。
func DownloadBySHA256Local(c *gin.Context) {
	hash := c.Param("sha256")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 format"})
		return
	}
	meta, err := repository.GetFileMeta(hash)
	if err != nil || meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	sd, _ := c.Get("storageDir")
	storageDir, _ := sd.(string)
	path := filepath.Join(storageDir, hash[:2], hash)
	data, err := os.ReadFile(path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found on disk"})
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
	c.Data(http.StatusOK, "application/octet-stream", data)
}

// DownloadByCID handles GET /ipfs/:cid, looking up the file by its IPFS CID and
// streaming it back with an X-CID header.  Falls back to public IPFS gateways
// when the CID is not in local storage and IPFS gateway fetching is enabled.
func DownloadByCID(c *gin.Context) {
	searchCID := c.Param("cid")
	meta, err := repository.GetFileMetaByCID(searchCID)
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
				// Compute SHA256 and cache locally.
				h := sha256.Sum256(data)
				hashStr := hex.EncodeToString(h[:])

				storageDir := ""
				if d, ok := c.Get("storageDir"); ok {
					storageDir, _ = d.(string)
				}
				if storageDir != "" {
					relPath := filepath.Join(hashStr[:2], hashStr)
					fullPath := filepath.Join(storageDir, relPath)
					_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
					_ = os.WriteFile(fullPath, data, 0644)

					_ = repository.InsertFileMeta(&model.FileMeta{
						Hash:     hashStr,
						Size:     int64(len(data)),
						Filename: searchCID,
						Type:     repository.FileTypeBlob,
					})
					_ = repository.InsertFileProvider(hashStr, "local", relPath)
				}

				c.Header("X-CID", searchCID)
				DownloadBySHA256Internal(c, hashStr)
				return
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

	start, end, ok := service.ParseRange(rangeHeader, total)
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

// UniversalDownload 处理 GET /download/:hash，使用通用下载器跨协议获取文件。
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

// UniversalDownloadSources 处理 GET /download/:hash/sources，列出所有可用协议源。
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

// UniversalDownloadRefresh 处理 POST /download/:hash/refresh，清除本地缓存后重新执行下载流水线。
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
