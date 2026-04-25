// 下载控制器 — 通过 SHA256 哈希进行内容寻址文件下载。
// 先调用 InitDownloader(svc) 注册 service.Downloader 实例。
// 流程：校验 hash → Downloader.GetFileStream → 解析 meta.is_gzip → Content-Encoding
// 路由：
//   GET /sha256sum/:sha256 — 按 SHA256 哈希下载文件

package controller

import (
	"net/http"
	"peerdrive/internal/service"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var downloader *service.Downloader

func InitDownloader(s *service.Downloader) {
	downloader = s
}

func DownloadBySHA256(c *gin.Context) {
	DownloadBySHA256Internal(c, c.Param("sha256"))
}

func DownloadBySHA256Internal(c *gin.Context, hash string) {
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 format"})
		return
	}
	reader, filename, gziped, err := downloader.GetFileStream(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	c.Header("Content-Disposition", "attachment; filename="+filename)
	if gziped {
		c.Header("Content-Encoding", "gzip")
	}
	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
}
