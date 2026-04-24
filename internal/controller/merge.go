package controller

import (
	"net/http"

	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

type Conflict struct {
	Path      string `json:"path"`
	LocalHash string `json:"local_hash"`
	SourceHash string `json:"source_hash"`
}

func MergeFromSource(c *gin.Context) {
	var req struct {
		Username       string `json:"username"`
		CollectionName string `json:"collection_name"`
		SourceUsername string `json:"source_username"`
		SourceCollName string `json:"source_coll_name"`
		Strategy       string `json:"strategy"` // "ours", "theirs", "manual"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	local, err := repository.GetCollection(req.Username, req.CollectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if local == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "local collection not found"})
		return
	}

	source, err := repository.GetCollection(req.SourceUsername, req.SourceCollName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if source == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	localEntries, err := repository.ListCollectionEntries(local.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sourceEntries, err := repository.ListCollectionEntries(source.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	localMap := make(map[string]string)
	for _, e := range localEntries {
		localMap[e.Path] = e.FileHash
	}
	sourceMap := make(map[string]string)
	for _, e := range sourceEntries {
		sourceMap[e.Path] = e.FileHash
	}

	var conflicts []Conflict
	for path, sourceHash := range sourceMap {
		if localHash, ok := localMap[path]; ok {
			if localHash != sourceHash {
				conflicts = append(conflicts, Conflict{Path: path, LocalHash: localHash, SourceHash: sourceHash})
			}
		}
	}

	if len(conflicts) > 0 && req.Strategy == "manual" {
		c.JSON(http.StatusConflict, gin.H{"conflicts": conflicts, "message": "resolve conflicts and re-merge with strategy=ours or theirs"})
		return
	}

	merged := make(map[string]string)
	for k, v := range localMap {
		merged[k] = v
	}
	for path, sourceHash := range sourceMap {
		if _, exists := localMap[path]; !exists {
			merged[path] = sourceHash
		} else if localMap[path] != sourceHash {
			if req.Strategy == "theirs" {
				merged[path] = sourceHash
			}
		}
	}

	for path, hash := range merged {
		if err := repository.AddCollectionEntry(local.ID, path, hash); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	result := map[string]interface{}{
		"message":         "merge complete",
		"conflicts_found": len(conflicts),
		"total_entries":   len(merged),
	}
	c.JSON(http.StatusOK, result)
}
