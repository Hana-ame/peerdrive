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

func InitDownloader(s *service.Downloader) {
	downloader = s
}

// InitUniversalDownloader injects the universal downloader singleton.
func InitUniversalDownloader(d *service.UniversalDownloader) {
	universalDownloader = d
}

// DownloadBySHA256 handles GET /sha256sum/:sha256 and is the legacy endpoint.
// When the universal downloader is available it delegates to it and adds the
// X-Protocol response header; otherwise it falls back to the original logic.
func DownloadBySHA256(c *gin.Context) {
	DownloadBySHA256Internal(c, c.Param("sha256"))
}

// DownloadBySHA256Internal is the shared implementation used by the legacy
// endpoint and by other controllers that need to resolve a hash to a stream.
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

// ─── Universal download endpoint ───────────────────────────────────────────

// UniversalDownload handles GET /download/:hash.
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

// UniversalDownloadSources handles GET /download/:hash/sources.
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

// UniversalDownloadRefresh handles POST /download/:hash/refresh.
// It clears the local cache and re-runs the full download pipeline.
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
