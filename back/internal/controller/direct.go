// Direct content-addressable URL handlers for invariant #2 (self-contained
// content URLs): /f/:sha256 and /c/:hash — the backend halves of the canonical
// URL scheme pinned in packages/peerdrive-core/src/url.js (buildFileUrl /
// buildCollectionUrl, mode='http').
//
// These routes were excised on 2026-10-17 (d2d2830) because the v1.0-rc source
// snapshot never shipped the handlers; they are restored here (Issue #410) by
// reusing the existing serving logic rather than re-implementing it:
//
//   - /f/:sha256[/:filename] -> downloadLocalByHash (extracted from
//     DownloadBySHA256Local: the same http.ServeContent streaming with RFC 7233
//     Range / ETag, whitelist Content-Type and blob CSP). ?meta=1 returns JSON
//     metadata instead of the body.
//   - /c/:hash[/*filepath]    -> GetAnonCollection / DownloadAnonFile verbatim,
//     so visibility/PSK gating and provider resolution are identical to
//     /anon/collections/*. The route param names ("hash" / "filepath") line up
//     with those handlers, which is why direct reuse is possible.

package controller

import (
	"net/http"
	"strings"

	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

// DirectFileBySHA256 handles GET/HEAD /f/:sha256[/:filename]: direct file
// access by content hash. The optional trailing /<filename> segment of the
// canonical URL is a download-name hint (sanitized like any hostile filename);
// ?meta=1 returns lightweight JSON metadata instead of streaming the body.
func DirectFileBySHA256(c *gin.Context) {
	hash := c.Param("sha256")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 format"})
		return
	}
	if c.Query("meta") == "1" {
		directFileMeta(c, hash)
		return
	}
	downloadLocalByHash(c, hash, c.Param("filename"))
}

// directFileMeta serves the ?meta=1 contract of /f/:sha256: JSON metadata
// without the file body.
// 推断说明：v1.0-rc 快照的路由注释写明了 /f/ 自带 "with Range, ETag, ?meta=1"，
// 但 handler 从未在仓库落地（连切除前都没有），所以 meta 的字段形状无测试钉死。
// 这里按最小语义实现：hash / size / filename / contentType / gzip，全部直接取自
// file_meta 表（与 /sha256sum/ 同一份数据源），不发明额外字段。
func directFileMeta(c *gin.Context, hash string) {
	meta, err := fileSvc.GetMeta(hash)
	if err != nil || meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	ctype := meta.MimeType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	c.JSON(http.StatusOK, gin.H{
		"hash":        hash,
		"size":        meta.Size,
		"filename":    meta.Filename,
		"contentType": ctype,
		"gzip":        meta.Gziped,
	})
}

// DirectCollectionByHash handles GET /c/:hash[/*filepath]: the collection
// manifest JSON (no filepath) or one entry's file content (filepath present).
//
// Why direct reuse instead of a dispatcher: gin hands /c/:hash the "hash" param
// and /c/:hash/*filepath the "hash"+"filepath" params — exactly what
// GetAnonCollection / DownloadAnonFile read — so both handlers are callable
// as-is and every gate (visibility / passcode / PSK / provider resolution)
// stays identical to /anon/collections/*. A trailing-slash /c/:hash/ resolves
// to the manifest like /c/:hash.
//
// 已知细微行为（继承自 DownloadAnonFile）：/c/:hash/history 会走合集历史而不是
// 名为 "history" 的条目（/anon/ 同款特判），条目恰好叫 history 的合集请走
// /anon/collections/:hash/history 形态。
func DirectCollectionByHash(c *gin.Context) {
	if strings.TrimPrefix(c.Param("filepath"), "/") == "" {
		GetAnonCollection(c)
		return
	}
	DownloadAnonFile(c)
}
