// 下载控制器 — 通过 SHA256 哈希进行内容寻址文件下载。
// 先调用 InitDownloader(svc) 注册 service.Downloader 实例。
// 流程：校验哈希格式 → Downloader.GetFileStream 读取文件 →
//   GetFileStream 返回 isGzip 标志 →
//   若 isGzip 为 true 则设置 Content-Encoding: gzip 响应头（浏览器自动解压），
//   否则不设置 → Gin DataFromReader 流式返回。
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

// DownloadBySHA256 godoc
// @Summary Download file by SHA256
// @Description Download a file using its SHA256 hash as the content identifier. Looks up metadata in SQLite, then streams from the appropriate provider (local or HTTP).
// @Tags download
// @Produce octet-stream
// @Param sha256 path string true "64-character lowercase SHA256 hex string"
// @Success 200 {file} binary "File content"
// @Failure 400 {object} map[string]string "Invalid hash format"
// @Failure 404 {object} map[string]string "File not found"
// @Router /sha256sum/{sha256} [get]
func DownloadBySHA256(c *gin.Context) {
	DownloadBySHA256Internal(c, c.Param("sha256"))
}

func DownloadBySHA256Internal(c *gin.Context, hash string) {
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 format"})
		return
	}
	reader, filename, isGzip, err := downloader.GetFileStream(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	c.Header("Content-Disposition", "attachment; filename="+filename)
	if isGzip {
		c.Header("Content-Encoding", "gzip")
	}
	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
}
