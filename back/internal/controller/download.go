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
			c.JSON(http.StatusNotFound, gin.H{"error": sanitizeErr(err, "request failed")})
			return
		}
		c.Header("X-Protocol", protocol)

		meta, _ := fileSvc.GetMeta(hash)
		fn := hash
		if meta != nil && meta.Filename != "" {
			fn = meta.Filename
		}
		setDownloadHeaders(c, fn, c.Query("inline") == "1")
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
	downloadLocalByHash(c, hash, "")
}

// downloadLocalByHash serves a locally stored file by its sha256 with
// http.ServeContent (streaming + RFC 7233 Range / 206 / ETag / If-Range).
//
// urlFilename is the optional filename carried by a canonical /f/:sha256/<name>
// URL; when non-empty it names the download (sanitized by setDownloadHeaders
// like any hostile filename), otherwise the DB meta.Filename, otherwise the
// hash itself. /sha256sum/:sha256 and /f/:sha256 both funnel through here so
// the two direct-by-hash surfaces cannot drift.
func downloadLocalByHash(c *gin.Context, hash, urlFilename string) {
	meta, err := fileSvc.GetMeta(hash)
	if err != nil || meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	sd, _ := c.Get("storageDir")
	storageDir, _ := sd.(string)
	path := filepath.Join(storageDir, hash[:2], hash)
	var f *os.File
	var openErr error
	if fileSvc != nil {
		f, openErr = fileSvc.OpenAllowed(path)
		if openErr != nil {
			if localPath, pErr := fileSvc.GetLocalPath(hash); pErr == nil {
				if f2, oErr := fileSvc.OpenAllowed(localPath); oErr == nil {
					f = f2
					path = localPath
				}
			}
		}
	} else {
		f, openErr = os.Open(path)
		if openErr != nil {
			if localPath, pErr := fileSvc.GetLocalPath(hash); pErr == nil {
				if f2, oErr := os.Open(localPath); oErr == nil {
					f = f2
					path = localPath
				}
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

	fn := urlFilename
	if fn == "" {
		fn = meta.Filename
	}
	if fn == "" {
		fn = hash
	}
	setDownloadHeaders(c, fn, c.Query("inline") == "1")
	if meta.Gziped {
		c.Header("Content-Encoding", "gzip")
	}
	// http.ServeContent 原生支持流式传输与 RFC 7233 Range / 206 Partial Content / ETag / If-Range
	http.ServeContent(c.Writer, c.Request, fn, st.ModTime(), f)
}

// ── Download response headers (security-critical) ────────────────────────────
//
// Why this helper exists (red-team finding R3, HIGH stored XSS):
// `/sha256sum/:hash` used to hand `http.ServeContent` the attacker-chosen upload
// filename, which made the server *guess* the response Content-Type from the
// extension (.html → text/html, .js → text/javascript, .svg → image/svg+xml).
// Combined with `?inline=1` (Content-Disposition: inline) an attacker uploaded a
// page, anyone visiting `/sha256sum/<hash>?inline=1` got it rendered as HTML on
// this origin. The global CSP is `default-src 'self'`, which permits same-origin
// `<script src>` — so a second uploaded .js file made the payload fully
// exploitable (verified in a real browser: document.title was set by the stored
// payload). Same-origin JS can additionally read the admin frontend's
// localStorage (`peerdrive_auth_token` / `peerdrive_auth_key`, see
// front/src/platform/shared/storageKeys.js) whenever the panel is deployed on
// this origin → full node takeover.
//
// Fix: the filename must never decide the Content-Type. Only a tight whitelist of
// preview-safe media types is honoured for inline requests; everything else is
// served as application/octet-stream (download, never parse). A strict CSP is
// pinned on the blob response as belt-and-braces so that even a type that slips
// through cannot execute script.
//
// NOTE: `?inline=1` is not used anywhere in the shipped frontend/panel (grep for
// `inline=` in front/src and packages/peerdrive-client returns nothing), so this
// only affects third-party integrations that asked for inline rendering.

// inlinePreviewTypes maps the small set of file extensions that are safe to
// render inline: images (no script execution) and plain text. Deliberately
// excludes text/html, text/xml, application/xhtml+xml, image/svg+xml,
// application/javascript and text/javascript — all of which can execute script
// when rendered as a document.
var inlinePreviewTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".txt":  "text/plain; charset=utf-8",
	".log":  "text/plain; charset=utf-8",
	".csv":  "text/plain; charset=utf-8",
}

// blobCSP is pinned on every blob response. It blocks script execution outright:
// even if a text/html body were ever served, no inline script, inline handler,
// or external script could run. Media-only documents are unaffected.
const blobCSP = "default-src 'none'; script-src 'none'; object-src 'none'; " +
	"style-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// setDownloadHeaders writes Content-Type / Content-Disposition / CSP for a blob
// download. filename is attacker-controlled (it comes from the uploaded multipart
// name or the stored file_meta.filename), so it is treated as hostile in both
// uses: it only selects an extension for the inline whitelist lookup, and it is
// sanitised before being placed in the header.
func setDownloadHeaders(c *gin.Context, filename string, inline bool) {
	ctype := "application/octet-stream"
	if inline {
		if t, ok := inlinePreviewTypes[strings.ToLower(filepath.Ext(filename))]; ok {
			ctype = t
		}
	}
	c.Header("Content-Type", ctype)
	if inline {
		c.Header("Content-Disposition", "inline; filename=\""+safeFilename(filename)+"\"")
	} else {
		c.Header("Content-Disposition", "attachment; filename=\""+safeFilename(filename)+"\"")
	}
	c.Header("Content-Security-Policy", blobCSP)
}

// safeFilename strips characters that could break out of the Content-Disposition
// parameter (quote, CR, LF, backslash) or be interpreted as control data by a
// header parser. The rest of the name is preserved so downloads still save with
// a readable name.
func safeFilename(name string) string {
	repl := strings.NewReplacer("\r", "", "\n", "", "\x00", "", `"`, "", `'`, "", `\\`, "")
	return repl.Replace(name)
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
		// Generic message: err.Error() carries fetcher names, gateway URLs and upstream
		// status text. Useful in the operator's logs, but it leaks internal topology to
		// the caller, so log it instead (red-team R2, info disclosure).
		log.LogDebug("ctrl-download: universal download %s failed: %v", hash, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	c.Header("X-Protocol", protocol)

	// Shared response-header policy (red-team R3): these outlets used to write a bare
	// c.Data with no Content-Disposition and no CSP — a weaker posture than /sha256sum
	// for the same content. Route through setDownloadHeaders so every download egress
	// gets the same whitelist / strict CSP / sanitized filename, plus Range support.
	meta, _ := fileSvc.GetMeta(hash)
	fn := hash
	if meta != nil && meta.Filename != "" {
		fn = meta.Filename
	}
	setDownloadHeaders(c, fn, c.Query("inline") == "1")
	if meta != nil && meta.Gziped {
		c.Header("Content-Encoding", "gzip")
	}
	if rng := c.GetHeader("Range"); rng != "" {
		if handled := handleRangeRequest(c, data, rng); handled {
			return
		}
	}
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
		// Generic message: err.Error() carries fetcher names, gateway URLs and upstream
		// status text. Useful in the operator's logs, but it leaks internal topology to
		// the caller, so log it instead (red-team R2, info disclosure).
		log.LogDebug("ctrl-download: universal download %s failed: %v", hash, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	c.Header("X-Protocol", protocol)

	// Shared response-header policy (red-team R3): these outlets used to write a bare
	// c.Data with no Content-Disposition and no CSP — a weaker posture than /sha256sum
	// for the same content. Route through setDownloadHeaders so every download egress
	// gets the same whitelist / strict CSP / sanitized filename, plus Range support.
	meta, _ := fileSvc.GetMeta(hash)
	fn := hash
	if meta != nil && meta.Filename != "" {
		fn = meta.Filename
	}
	setDownloadHeaders(c, fn, c.Query("inline") == "1")
	if meta != nil && meta.Gziped {
		c.Header("Content-Encoding", "gzip")
	}
	if rng := c.GetHeader("Range"); rng != "" {
		if handled := handleRangeRequest(c, data, rng); handled {
			return
		}
	}
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
