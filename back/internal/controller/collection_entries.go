// Package controller provides HTTP handlers for collection entries management.
package controller

import (
	"net/http"
	"strings"

	"peerdrive/internal/model"

	"github.com/gin-gonic/gin"
)

// AddEntry godoc
// @Summary Add an entry to a collection
// @Description Map a path (e.g. "dir/file.txt") to a SHA256 hash within a collection. Creates the collection if it doesn't exist.
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{path=string,hash=string} true "Entry path and file hash"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid request"
// @Router /collections/{username}/{collection_name}/entries [post]
func AddEntry(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		Path      string           `json:"path"`
		Hash      string           `json:"hash"`
		Providers []model.Provider `json:"providers,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	collID, err := collSvc.GetOrCreate(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(req.Providers) > 0 {
		err = collSvc.AddProviderEntry(collID, req.Path, req.Providers)
	} else if req.Hash != "" {
		err = collSvc.AddEntry(collID, req.Path, req.Hash)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must provide hash or providers"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry added"})
}

// RemoveEntry godoc
// @Summary Remove an entry from a collection
// @Description Delete a path→hash mapping from a collection
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param path path string true "Entry path (URL-encoded)"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/entries/{path} [delete]
func RemoveEntry(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	path := strings.TrimPrefix(c.Param("path"), "/")
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := collSvc.RemoveEntry(col.ID, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry removed"})
}

// DownloadCollectionFile godoc
// @Summary Download a file from a collection
// @Description Look up the path in the collection's entries and redirect to /sha256sum/{hash} for download
// @Tags collections
// @Produce octet-stream
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param filepath path string true "File path within the collection"
// @Success 200 {file} binary "File content"
// @Failure 404 {object} map[string]string "Collection or file not found"
// @Router /{username}/{collection_name}/{filepath} [get]
func DownloadCollectionFile(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	entry, err := collSvc.GetEntry(col.ID, filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}
	providers := entry.BuildProviders()
	// Try providers in order: sha256 first
	for _, p := range providers {
		if p.Type == "sha256" && p.Value != "" {
			DownloadBySHA256Internal(c, p.Value)
			return
		}
	}
	// fallback: URL providers
	for _, p := range providers {
		if p.Type == "url" && p.Value != "" {
			if !col.FollowRedirects {
				c.JSON(http.StatusOK, gin.H{"url": p.Value, "follow_redirects": false})
				return
			}
			c.Redirect(http.StatusFound, p.Value)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "no usable provider found"})
}
