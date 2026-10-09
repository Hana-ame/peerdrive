// iwara.go: iwara.tv video metadata endpoint for the iwara page.
//
// The echproxy package (PR #35) shipped the IwaraClient as a library only —
// nothing exposed it over HTTP, so the frontend had no way to turn a pasted
// iwara URL into a title, cover and download options. This controller is the
// thin HTTP layer: one GET endpoint, one call into echproxy.GetVideoMeta.
//
// It deliberately proxies nothing by streaming bytes through the node; the
// CDN download URL is returned as a link the browser opens directly (same
// trust model as echproxy.ResolveDownloadURL, which the downloader CLI uses).
package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/echproxy"
	"peerdrive/internal/log"
)

// iwaraClient is injected by serverapp; nil means the iwara module is disabled
// (PEERDRIVE_IWARA_ENABLE=false, the default) and the route is not registered
// at all — the frontend falls back to its demo entry.
var iwaraClient *echproxy.IwaraClient

// InitIwaraClient injects the iwara metadata client (nil = endpoints off).
func InitIwaraClient(c *echproxy.IwaraClient) {
	log.LogDebug("ctrl-iwara: InitIwaraClient")
	iwaraClient = c
}

// iwaraMetaTimeout bounds one metadata request. It is a 2-step API flow (video
// info → resolution list) and the node sits behind ech-proxy, so the limit is
// deliberately looser than marketTimeout (6s) but still far below "the request
// hangs forever and the page spinner never clears".
const iwaraMetaTimeout = 25 * time.Second

// GetIwaraVideo handles GET /iwara/video/:id.
//
// The id path segment accepts either a bare video ID or a full
// https://www.iwara.tv/videos/{id} URL (echproxy.ParseVideoID normalizes it),
// so the frontend can pass whatever the user pasted.
//
// Errors are reported as 404 with an `error` field, mirroring "this video does
// not exist for us" for both a bad URL and a removed video — the frontend
// renders one error box for either case.
func GetIwaraVideo(c *gin.Context) {
	if iwaraClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "iwara module not enabled"})
		return
	}
	raw := c.Param("id")
	if raw == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "video id is required"})
		return
	}

	// The IwaraClient's own http.Client has Timeout: 0 (it streams gigabyte
	// downloads), so the request context must carry the bound. Derived from the
	// client context so a client disconnect cancels the API calls too.
	ctx, cancel := context.WithTimeout(c.Request.Context(), iwaraMetaTimeout)
	defer cancel()

	meta, err := iwaraClient.GetVideoMeta(ctx, raw)
	if err != nil {
		log.LogDebug("ctrl-iwara: video %q: %v", raw, err)
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, meta)
}
