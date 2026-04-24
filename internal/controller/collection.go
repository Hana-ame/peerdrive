// Collection controller — CRUD for collections, entries, commit/version log/rollback.
// A collection is a named set of path→hash mappings belonging to a user.
// Usage:
//   POST   /collections                              — create collection
//   GET    /collections/:username                     — list user's collections
//   GET    /collections/:username/:coll               — get collection + entries
//   POST   /collections/:username/:coll/entries       — add path→hash entry
//   DELETE /collections/:username/:coll/entries/:path — remove entry
//   POST   /collections/:username/:coll/commit        — snapshot entries as version
//   GET    /collections/:username/:coll/log           — version history
//   POST   /collections/:username/:coll/rollback/:vid — restore entries from version
//   GET    /:username/:coll/*filepath                 — download file from collection

package controller

import (
	"fmt"
	"net/http"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

// CreateCollection godoc
// @Summary Create a new collection
// @Description Create a named collection for a user. Duplicate (username, collection_name) returns 409.
// @Tags collections
// @Accept json
// @Produce json
// @Param body body object{username=string,collection_name=string} true "Username and collection name"
// @Success 200 {object} map[string]interface{} "id, username, collection_name"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 409 {object} map[string]string "Duplicate"
// @Router /collections [post]
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

// ListCollections godoc
// @Summary List collections for a user
// @Description Returns all collections owned by the given username
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/{username} [get]
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

// GetCollection godoc
// @Summary Get collection details with entries
// @Description Returns collection metadata and its path→hash entries
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Success 200 {object} map[string]interface{} "collection and entries"
// @Failure 404 {object} map[string]string "Not found"
// @Router /collections/{username}/{collection_name} [get]
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

// CommitCollection godoc
// @Summary Commit collection entries as a new version
// @Description Snapshot all current entries into a version with a commit message. Creates a linked version chain.
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{commit_message=string} true "Commit message"
// @Success 200 {object} map[string]interface{} "message and version_number"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/commit [post]
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

// GetVersionLog godoc
// @Summary Get collection version history
// @Description Returns all committed versions for a collection, newest first
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Success 200 {object} map[string]interface{} "data array of versions"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/log [get]
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

// RollbackCollection godoc
// @Summary Rollback collection to a previous version
// @Description Restore collection entries from a specific version's snapshot. Replaces all current entries.
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param version_id path int true "Version ID to restore"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid version_id"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/rollback/{version_id} [post]
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
