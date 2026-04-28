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
	"fmt"
	"context"
	"net/http"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var downloader *service.Downloader
var universalDownloader *service.UniversalDownloader

// InitDownloader 注入 Downloader 实例供下载处理函数使用。
func InitDownloader(s *service.Downloader) {
	downloader = s
}

// InitUniversalDownloader 注入 UniversalDownloader 实例供多协议下载端点使用。
func InitUniversalDownloader(d *service.UniversalDownloader) {
	universalDownloader = d
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
			var start, end int64
			if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &start, &end); err == nil && start >= 0 && end >= start {
				total := int64(len(data))
				if end == 0 || end >= total {
					end = total - 1
				}
				c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
				c.Status(http.StatusPartialContent)
				c.Writer.Write(data[start : end+1])
				return
			}
		}
		c.Data(http.StatusOK, "application/octet-stream", data)
		return
	}

	// Legacy path — no universal downloader.
	reader, filename, gziped, err := downloader.GetFileStream(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	if c.Query("inline") == "1" {
		c.Header("Content-Disposition", "inline; filename="+filename)
	} else {
		c.Header("Content-Disposition", "attachment; filename="+filename)
	}
	if gziped {
		c.Header("Content-Encoding", "gzip")
	}

	meta, _ := repository.GetFileMeta(hash)
	if meta != nil && meta.Type == repository.FileTypeAnonCollection {
		c.Header("X-Peerdrive-Collection", "true")
	}

	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
}

// DownloadByCID handles GET /ipfs/:cid, looking up the file by its IPFS CID and
// streaming it back with an X-CID header.
func DownloadByCID(c *gin.Context) {
	searchCID := c.Param("cid")
	meta, err := repository.GetFileMetaByCID(searchCID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database error"})
		return
	}
	if meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found by cid"})
		return
	}

	c.Header("X-CID", searchCID)
	DownloadBySHA256Internal(c, meta.Hash)
}

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
