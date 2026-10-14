// Package controller provides HTTP handlers for collection versioning, rollback, visibility, and tags.
package controller

import (
	"fmt"
	"net/http"

	"peerdrive/internal/model"

	"github.com/gin-gonic/gin"
)

// CommitCollection godoc
// @Summary Commit collection entries as a new version
// @Description Snapshot all current entries into a version with a commit message. Creates a linked version chain.
//
//	Also generates an anonymous collection JSON (SHA256), updates collections.current_hash.
//
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{commit_message=string} true "Commit message"
// @Success 200 {object} map[string]interface{} "message, version_number, snapshot_hash"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/commit [post]
func CommitCollection(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		CommitMessage string `json:"commit_message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	// 1. Get current workspace entries
	entries, err := collSvc.ListEntries(col.ID)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	// 2. Convert to anonymous collection entries (with providers)
	anonEntries := make([]model.AnonCollectionEntry, 0, len(entries))
	for _, e := range entries {
		anonEntries = append(anonEntries, model.AnonCollectionEntry{
			Path:      e.Path,
			Providers: e.BuildProviders(),
		})
	}

	// 3. Build anonymous collection and save
	anonColl := model.NewAnonCollection("", anonEntries, nil)
	storageDir := c.MustGet("storageDir").(string)
	hash, err := collSvc.SaveAnon(anonColl, storageDir)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	// 4. Atomically record version snapshot, copy entries, and update current_hash in one transaction
	_, verNum, err := collSvc.CommitSnapshotTx(col.ID, req.CommitMessage, hash)
	if err != nil {
		writeInternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "committed", "version_number": verNum, "snapshot_hash": hash})
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
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	versions, err := collSvc.VersionLog(col.ID)
	if err != nil {
		writeInternalError(c, err)
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
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	versionID := c.Param("version_id")
	var vid int
	if _, err := fmt.Sscanf(versionID, "%d", &vid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version_id"})
		return
	}
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := collSvc.RestoreVersion(vid, col.ID); err != nil {
		writeInternalError(c, err)
		return
	}

	// Regenerate CID while preserving multi-source Providers (URL / ECH / SHA256)
	entries, err := collSvc.ListEntries(col.ID)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	anonEntries := make([]model.AnonCollectionEntry, 0, len(entries))
	for _, e := range entries {
		anonEntries = append(anonEntries, model.AnonCollectionEntry{
			Path:      e.Path,
			Providers: e.BuildProviders(),
		})
	}
	anonColl := model.NewAnonCollection("", anonEntries, nil)
	storageDir := c.MustGet("storageDir").(string)
	hash, err := collSvc.SaveAnon(anonColl, storageDir)
	if err != nil {
		writeInternalError(c, err)
		return
	}
	if err := collSvc.UpdateCurrentHash(col.ID, hash); err != nil {
		writeInternalError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "rolled back"})
}

// SetCollectionVisibility godoc
// @Summary Set collection visibility
// @Description Change the visibility of a collection (public, unlisted, private)
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{visibility=string} true "Visibility: public|unlisted|private"
// @Success 200 {object} map[string]string "ok"
// @Failure 400,404 {object} map[string]string "error"
// @Router /collections/{username}/{collection_name}/visibility [post]
func SetCollectionVisibility(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		Visibility string `json:"visibility"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Visibility != "public" && req.Visibility != "unlisted" && req.Visibility != "private" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "visibility must be public, unlisted, or private"})
		return
	}
	col, err := collSvc.Get(username, collectionName)
	if err != nil || col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := collSvc.SetVisibility(username, collectionName, req.Visibility); err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// UpdateCollectionTags godoc
// @Summary Update collection tags
// @Description Replace the tag list for a collection
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{tags=[]string} true "Tag list"
// @Success 200 {object} map[string]string "ok"
// @Failure 400,404 {object} map[string]string "error"
// @Router /collections/{username}/{collection_name}/tags [post]
func UpdateCollectionTags(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := collSvc.UpdateTags(username, collectionName, req.Tags); err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
