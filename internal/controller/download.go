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
	reader, filename, err := downloader.GetFileStream(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer reader.Close()

	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
}