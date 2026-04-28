package controller

import (
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"peerdrive/internal/log"
	"peerdrive/internal/repository"
	"peerdrive/internal/provider"
)

var fileProvider *provider.LocalProvider

func InitFileController(storageDir string) {
	os.MkdirAll(storageDir, 0755)
	fileProvider = provider.NewLocalProvider(storageDir)
}

// DownloadBySHA256 handles GET /sha256sum/:sha256
func DownloadBySHA256(c *gin.Context) {
	hash := c.Param("sha256")
	if len(hash) != 64 {
		c.String(http.StatusBadRequest, "invalid sha256 hash length")
		return
	}
	if _, err := hex.DecodeString(hash); err != nil {
		c.String(http.StatusBadRequest, "invalid sha256 hash")
		return
	}

	// Look up providers in DB
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			path := p.ProviderPath
			if !filepath.IsAbs(path) {
				path = filepath.Join(fileProvider.BaseDir, path)
			}
			if reader, size, err := fileProvider.GetReader(path); err == nil {
				defer reader.Close()
				c.Header("Content-Length", itoa(size))
				c.Header("X-File-Hash", hash)
				c.Status(http.StatusOK)
				io.Copy(c.Writer, reader)
				return
			}
		}
	}

	// Try direct path from metadata
	meta, _ := repository.GetFileMeta(hash)
	if meta != nil {
		path := meta.ProviderPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(fileProvider.BaseDir, path)
		}
		if reader, size, err := fileProvider.GetReader(path); err == nil {
			defer reader.Close()
			c.Header("Content-Length", itoa(size))
			c.Header("X-File-Hash", hash)
			c.Status(http.StatusOK)
			io.Copy(c.Writer, reader)
			return
		}
	}

	log.Warn("sha256 download: hash %s not found locally", hash[:16])
	c.String(http.StatusNotFound, "file not found")
}

func itoa(n int64) string { return formatInt(n) }
func formatInt(n int64) string {
	if n == 0 { return "0" }
	s := ""
	for n > 0 { s = string(rune('0'+n%10)) + s; n /= 10 }
	return s
}
