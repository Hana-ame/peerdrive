package controller

import (
	"net/http"
	"os"
	"strconv"

	"peerdrive/internal/log"
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if files == nil {
		files = []repository.FileIndex{}
	}
	c.JSON(http.StatusOK, gin.H{"files": files, "count": len(files)})
}

// ApproveInboxFile handles POST /files/inbox/approve (Issue #267).
// Releases a quarantined file from inbox into standard host storage.
func ApproveInboxFile(c *gin.Context) {
	var req struct {
		Hash string `json:"hash" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}
	if !hashutil.IsStrictSHA256(req.Hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256 hash"})
		return
	}

	if err := repository.ApproveInboxFile(req.Hash); err != nil {
		log.LogWarn("ctrl-inbox: ApproveInboxFile error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "approved", "hash": req.Hash})
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

	if fi.Path != "" {
		_ = os.Remove(fi.Path)
	}
	if _, err := repository.DeleteFileIndex(hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "rejected and deleted", "hash": hash})
}
