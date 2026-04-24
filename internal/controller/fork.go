// Fork/Pull controller — fork a remote collection into local, or pull upstream updates.
// Usage:
//   POST /actions/fork — copy source collection entries into a new local collection
//   POST /actions/pull — (placeholder) sync upstream updates

package controller

import (
	"net/http"

	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

// ForkCollection godoc
// @Summary Fork a collection
// @Description Copy all entries from a source collection into a new collection owned by a different user.
// @Tags actions
// @Accept json
// @Produce json
// @Param body body object{username=string,collection_name=string,source_username=string,source_coll_name=string} true "Fork request"
// @Success 200 {object} map[string]interface{} "forked, id, username, collection_name, entries_count"
// @Failure 404 {object} map[string]string "Source collection not found"
// @Failure 409 {object} map[string]string "Local collection already exists"
// @Router /actions/fork [post]
func ForkCollection(c *gin.Context) {
	var req struct {
		Username       string `json:"username"`
		CollectionName string `json:"collection_name"`
		SourceUsername string `json:"source_username"`
		SourceCollName string `json:"source_coll_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
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

	localID, err := repository.CreateCollection(req.Username, req.CollectionName)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	entries, err := repository.ListCollectionEntries(source.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, e := range entries {
		if err := repository.AddCollectionEntry(localID, e.Path, e.FileHash); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         "forked",
		"id":              localID,
		"username":        req.Username,
		"collection_name": req.CollectionName,
		"entries_count":   len(entries),
	})
}

// PullCollection godoc
// @Summary Pull upstream updates (placeholder)
// @Description Placeholder for syncing upstream changes from a forked source. Currently returns a no-op task.
// @Tags actions
// @Accept json
// @Produce json
// @Param body body object{username=string,collection_name=string} true "Pull request"
// @Success 200 {object} map[string]string "message"
// @Router /actions/pull [post]
func PullCollection(c *gin.Context) {
	var req struct {
		Username       string `json:"username"`
		CollectionName string `json:"collection_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "pull not implemented (upstream sync coming in v2)"})

	taskID, err := repository.CreateTask("pull", "")
	if err == nil {
		repository.UpdateTaskStatus(taskID, "completed", `{"note":"pull no-op"}`)
	}
	_ = taskID
}
