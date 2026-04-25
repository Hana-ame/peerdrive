package controller

import (
	"net/http"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

func CreateAnonCollection(c *gin.Context) {
	var req model.AnonCollection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	storageDir := c.MustGet("storageDir").(string)
	hash, err := repository.SaveCollection(&req, storageDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

func GetAnonCollection(c *gin.Context) {
	hash := c.Param("hash")
	storageDir := c.MustGet("storageDir").(string)
	coll, err := repository.GetAnonCollectionByHash(hash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	c.JSON(http.StatusOK, coll)
}

func DownloadAnonFile(c *gin.Context) {
	hash := c.Param("hash")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")
	storageDir := c.MustGet("storageDir").(string)

	coll, err := repository.GetAnonCollectionByHash(hash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var targetEntry *model.AnonEntry
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

	// 检查是否有 local provider
	providers, _ := repository.GetFileProviders(targetEntry.Hash)
	hasLocal := false
	for _, p := range providers {
		if p.ProviderType == "local" {
			hasLocal = true
			break
		}
	}
	if hasLocal {
		dl := c.MustGet("downloader").(*service.Downloader)
		reader, filename, gziped, err := dl.GetFileStream(targetEntry.Hash)
		if err == nil {
			defer reader.Close()
			c.Header("Content-Disposition", "attachment; filename="+filename)
			if gziped {
				c.Header("Content-Encoding", "gzip")
			}
			c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
			return
		}
	}

	if targetEntry.URL != nil && *targetEntry.URL != "" {
		c.Redirect(http.StatusFound, *targetEntry.URL)
		return
	}

	dl := c.MustGet("downloader").(*service.Downloader)
	reader, filename, gziped, err := dl.GetFileStream(targetEntry.Hash)
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
		SourceHash  string           `json:"source_hash"`
		AddEntries []model.AnonEntry `json:"add_entries"`
		RemovePaths []string         `json:"remove_paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	storageDir := c.MustGet("storageDir").(string)
	src, err := repository.GetAnonCollectionByHash(req.SourceHash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	newEntries := []model.AnonEntry{}
	removeSet := map[string]bool{}
	for _, p := range req.RemovePaths {
		removeSet[p] = true
	}
	for _, e := range src.Entries {
		if !removeSet[e.Path] {
			newEntries = append(newEntries, e)
		}
	}
	for _, e := range req.AddEntries {
		found := false
		for i := range newEntries {
			if newEntries[i].Path == e.Path {
				newEntries[i] = e
				found = true
				break
			}
		}
		if !found {
			newEntries = append(newEntries, e)
		}
	}

	newColl := &model.AnonCollection{
		Version:     1,
		Name:        src.Name + " (forked)",
		Description: src.Description,
		Entries:     newEntries,
	}
	hash, err := repository.SaveCollection(newColl, storageDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}
