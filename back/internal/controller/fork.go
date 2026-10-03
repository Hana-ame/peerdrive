// Fork controller — copies all entries from a source collection into a new collection (Fork).
// Fork flow: query source collection → GetOrCreate target collection → copy entries one by one.
//   from collection_entries.
// Routes:
//   POST /actions/fork — copy source collection entries to a new local collection
// Note: PullCollection placeholder (no-op + fake task) was deleted on 2026-08-19
// (TaskService deleted in the same batch; /tasks and /pull routes removed together).

package controller

import (
	"net/http"

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

	source, err := collSvc.Get(req.SourceUsername, req.SourceCollName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if source == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	localID, terr := collSvc.CreatePlain(req.Username, req.CollectionName)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	entries, err := collSvc.ListEntries(source.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	for _, e := range entries {
		providers := e.BuildProviders()
		if len(providers) > 0 && e.ProvidersJSON != "" {
			if err := collSvc.AddProviderEntry(localID, e.Path, providers); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		} else {
			if err := collSvc.AddEntry(localID, e.Path, e.FileHash); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
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
