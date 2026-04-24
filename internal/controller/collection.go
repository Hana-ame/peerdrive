package controller

import (
	"fmt"
	"net/http"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

func CreateCollection(c *gin.Context) {
	var req struct {
		Username       string `json:"username"`
		CollectionName string `json:"collection_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	id, err := repository.CreateCollection(req.Username, req.CollectionName)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "username": req.Username, "collection_name": req.CollectionName})
}

func ListCollections(c *gin.Context) {
	username := c.Param("username")
	cols, err := repository.ListCollections(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cols == nil {
		cols = []model.Collection{}
	}
	c.JSON(http.StatusOK, gin.H{"data": cols})
}

func GetCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	entries, err := repository.ListCollectionEntries(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []model.CollectionEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"collection": col, "entries": entries})
}

func AddEntry(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	var req struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	collID, err := repository.GetOrCreateCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := repository.AddCollectionEntry(collID, req.Path, req.Hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry added"})
}

func RemoveEntry(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	path := c.Param("path")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := repository.RemoveCollectionEntry(col.ID, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry removed"})
}

func DownloadCollectionFile(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	filepath := c.Param("filepath")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	entry, err := repository.GetCollectionEntry(col.ID, filepath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}
	DownloadBySHA256Internal(c, entry.FileHash)
}

func CommitCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	var req struct {
		CommitMessage string `json:"commit_message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	versions, err := repository.GetVersionLog(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var parentID *int
	if len(versions) > 0 {
		parentID = &versions[0].ID
	}
	verID, verNum, err := repository.CreateVersion(col.ID, req.CommitMessage, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := repository.SnapshotVersionEntries(verID, col.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "committed", "version_number": verNum})
}

func GetVersionLog(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	versions, err := repository.GetVersionLog(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if versions == nil {
		versions = []model.CollectionVersion{}
	}
	c.JSON(http.StatusOK, gin.H{"data": versions})
}

func RollbackCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	versionID := c.Param("version_id")
	var vid int
	if _, err := fmt.Sscanf(versionID, "%d", &vid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version_id"})
		return
	}
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := repository.RestoreVersionEntries(vid, col.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "rolled back"})
}
