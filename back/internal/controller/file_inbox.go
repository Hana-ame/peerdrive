package controller

import (
	"net/http"
	"strconv"

	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

// ListInboxFiles handles GET /files/inbox (Issue #267).
// Returns all quarantined files uploaded by remote peers waiting for host approval.
func ListInboxFiles(c *gin.Context) {
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	files, err := repository.ListInboxFiles(offset, limit)
	if err != nil {
		log.LogWarn("ctrl-inbox: ListInboxFiles error: %v", err)
		writeInternalError(c, err)
		return
	}
	if files == nil {
		files = []repository.FileIndex{}
	}
	c.JSON(http.StatusOK, gin.H{"files": files, "count": len(files)})
}

// ApproveInboxFile handles POST /files/inbox/approve (JSON body {"hash": ...}) and
// POST /files/inbox/:hash/approve (Issue #353). Both spellings reach the same
// repository call, so approve keeps the legacy body contract while gaining a path
// parameter that is symmetric with RejectInboxFile's DELETE /files/inbox/:hash.
func ApproveInboxFile(c *gin.Context) {
	hash, ok := inboxHashFromContext(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}
	if !hashutil.IsStrictSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 hash"})
		return
	}

	if err := repository.ApproveInboxFile(hash); err != nil {
		log.LogWarn("ctrl-inbox: ApproveInboxFile error: %v", err)
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "approved", "hash": hash})
}

// inboxHashFromContext resolves the hash an inbox mutation applies to: the URL
// path parameter wins (/files/inbox/:hash/approve), the JSON body is the
// fallback for the legacy POST /files/inbox/approve (Issue #353). ok=false means
// no path parameter and a missing or unparseable body.
func inboxHashFromContext(c *gin.Context) (hash string, ok bool) {
	hash = c.Param("hash")
	if hash == "" {
		var req struct {
			Hash string `json:"hash" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			return "", false
		}
		hash = req.Hash
	}
	return hash, true
}

// RejectInboxFile handles DELETE /files/inbox/:hash (Issue #267).
// Deletes a quarantined file from storage and index.
func RejectInboxFile(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsStrictSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 hash"})
		return
	}

	fi, err := repository.GetFileIndex(hash)
	if err != nil || fi == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	if !fi.IsInbox {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is not in inbox"})
		return
	}

	if fi.Path != "" {
		// The path came from the file_index table, so treat it as untrusted input
		// (red-team R2): os.Remove follows symlinks in every parent, which turns
		// "delete a file inside storage" into "delete any file on the host". Route it
		// through pathutil so the removal stays inside the allowed roots.
		if err := pathutil.SafeRemoveAny([]string{c.GetString("storageDir")}, fi.Path); err != nil {
			log.LogDebug("ctrl-inbox: refuse to remove %s: %v", fi.Path, err)
		}
	}
	if _, err := repository.DeleteFileIndex(hash); err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "rejected and deleted", "hash": hash})
}
