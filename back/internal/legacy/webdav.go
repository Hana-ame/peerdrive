// WebDAVService 提供 WebDAV 协议访问存储目录，支持文件管理器挂载为网络驱动器。
package legacy

import (
	"net/http"

	"golang.org/x/net/webdav"

	"github.com/gin-gonic/gin"
)

// WebDAVService provides WebDAV protocol access to the Peerdrive storage
// directory, allowing users to mount it as a network drive in file explorers
// (Windows Explorer, macOS Finder, Linux Nautilus via davfs2).
type WebDAVService struct {
	handler    *webdav.Handler
	storageDir string
	enabled    bool
}

// NewWebDAVService creates a new WebDAV service that serves files from the
// specified storage directory. The service is enabled by default.
// NewWebDAVService 创建 WebDAV 服务，使用指定存储目录作为根目录。
func NewWebDAVService(storageDir string) *WebDAVService {
	return &WebDAVService{
		handler: &webdav.Handler{
			FileSystem: webdav.Dir(storageDir),
			LockSystem: webdav.NewMemLS(),
			Logger:     nil,
		},
		storageDir: storageDir,
		enabled:    true,
	}
}

// ServeHTTP handles all WebDAV methods (PROPFIND, MKCOL, GET, HEAD, PUT,
// DELETE, COPY, MOVE, LOCK, UNLOCK, OPTIONS). It is intended to be called
// from a Gin route with a wildcard path (e.g. /webdav/*path).
//
// The caller must set c.Request.URL.Path to the sub-path within the storage
// directory before invoking this handler.
// ServeHTTP 处理所有 WebDAV 方法（PROPFIND/MKCOL/GET/PUT/DELETE/COPY/MOVE/LOCK/UNLOCK/OPTIONS）。
func (w *WebDAVService) ServeHTTP(c *gin.Context) {
	if !w.enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "WebDAV disabled"})
		return
	}
	w.handler.ServeHTTP(c.Writer, c.Request)
}

// IsEnabled returns whether the WebDAV service is currently enabled.
// IsEnabled 返回 WebDAV 服务是否已启用。
func (w *WebDAVService) IsEnabled() bool {
	return w.enabled
}

// SetEnabled enables or disables the WebDAV service at runtime.
// SetEnabled 运行时启用或禁用 WebDAV 服务。
func (w *WebDAVService) SetEnabled(enabled bool) {
	w.enabled = enabled
}
