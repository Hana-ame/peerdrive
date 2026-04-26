package controller

import (
	"net/http"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

var anonSvc *service.AnonService

func InitAnonController(svc *service.AnonService) {
	anonSvc = svc
}

func CreateAnonCollection(c *gin.Context) {
	var req struct {
		FriendlyName string                    `json:"friendly_name"`
		Entries      []model.AnonCollectionEntry `json:"entries"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash, err := anonSvc.CreateCollection(req.FriendlyName, req.Entries)
	if err != nil {
		if strings.Contains(err.Error(), "invalid path") || strings.Contains(err.Error(), "invalid hash") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

func GetAnonCollection(c *gin.Context) {
	hash := c.Param("hash")
	coll, err := anonSvc.GetCollectionByHash(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	c.JSON(http.StatusOK, coll)
}

func DownloadAnonFile(c *gin.Context) {
	hash := c.Param("hash")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")

	coll, err := anonSvc.GetCollectionByHash(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var targetEntry *model.AnonCollectionEntry
	for i := range coll.Entries {
		if coll.Entries[i].Path == filePath {
			targetEntry = &coll.Entries[i]
			break
		}
	}
	if targetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}

	reader, filename, gziped, err := downloader.GetFileStream(targetEntry.Hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file data not available"})
		return
	}
	defer reader.Close()

	c.Header("Content-Disposition", "attachment; filename="+filename)
	if gziped {
		c.Header("Content-Encoding", "gzip")
	}
	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
}

func ForkAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash   string                    `json:"source_hash"`
		FriendlyName string                    `json:"friendly_name"`
		AddEntries   []model.AnonCollectionEntry `json:"add_entries"`
		RemovePaths  []string                  `json:"remove_paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	src, err := anonSvc.GetCollectionByHash(req.SourceHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	removeSet := map[string]bool{}
	for _, p := range req.RemovePaths {
		removeSet[p] = true
	}

	var newEntries []model.AnonCollectionEntry
	for _, e := range src.Entries {
		if !removeSet[e.Path] {
			newEntries = append(newEntries, e)
		}
	}

	existing := map[string]int{}
	for i, e := range newEntries {
		existing[e.Path] = i
	}
	for _, e := range req.AddEntries {
		if idx, ok := existing[e.Path]; ok {
			newEntries[idx] = e
		} else {
			newEntries = append(newEntries, e)
		}
	}

	friendlyName := req.FriendlyName
	if friendlyName == "" {
		friendlyName = src.FriendlyName
	}
	hash, err := anonSvc.CreateCollection(friendlyName, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

func CommitAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash    string                    `json:"source_hash"`
		Entries       []model.AnonCollectionEntry `json:"entries"`
		CommitMessage string                    `json:"commit_message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash, err := anonSvc.CommitCollection(req.SourceHash, req.Entries, req.CommitMessage)
	if err != nil {
		if strings.Contains(err.Error(), "source collection not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "invalid") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}
