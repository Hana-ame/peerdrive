// Merge controller — merges source collection entries into a local collection.
// Supports three strategies:
//   "ours"   — on conflict, keep local hash
//   "theirs" — on conflict, accept source hash
//   "manual" — detect conflicts, return 409 + conflict list, frontend resolves then retries
// Merge flow: read all entries from local and source collections → build map by path →
//   detect conflicts (same path, different hash) → merge by strategy → upsert into local one by one.
// Routes:
//   POST /actions/merge — merge source collection into local collection

package controller

import (
	"net/http"

	"peerdrive/internal/model"

	"github.com/gin-gonic/gin"
)

type Conflict struct {
	Path            string           `json:"path"`
	LocalHash       string           `json:"local_hash"`
	SourceHash      string           `json:"source_hash"`
	LocalProviders  []model.Provider `json:"local_providers,omitempty"`
	SourceProviders []model.Provider `json:"source_providers,omitempty"`
}

// MergeFromSource godoc
// @Summary Merge a source collection into the local collection
// @Description Merge entries from source collection into local. Strategy: "ours" keeps local, "theirs" accepts source, "manual" returns conflicts list.
// @Tags actions
// @Accept json
// @Produce json
// @Param body body object{username=string,collection_name=string,source_username=string,source_coll_name=string,strategy=string} true "Merge request"
// @Success 200 {object} map[string]interface{} "merge complete"
// @Failure 409 {object} map[string]interface{} "conflicts list (when strategy=manual)"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /actions/merge [post]
func MergeFromSource(c *gin.Context) {
	var req struct {
		Username       string `json:"username"`
		CollectionName string `json:"collection_name"`
		SourceUsername string `json:"source_username"`
		SourceCollName string `json:"source_coll_name"`
		Strategy       string `json:"strategy"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	local, err := collSvc.Get(req.Username, req.CollectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if local == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "local collection not found"})
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

	localEntries, err := collSvc.ListEntries(local.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sourceEntries, err := collSvc.ListEntries(source.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	localMap := make(map[string][]model.Provider)
	for _, e := range localEntries {
		localMap[e.Path] = e.BuildProviders()
	}
	sourceMap := make(map[string][]model.Provider)
	for _, e := range sourceEntries {
		sourceMap[e.Path] = e.BuildProviders()
	}

	// Extract primary hash for conflict detection
	primaryHash := func(providers []model.Provider) string {
		for _, p := range providers {
			if p.Type == "sha256" && p.Value != "" {
				return p.Value
			}
		}
		return ""
	}

	var conflicts []Conflict
	for path, sourceProviders := range sourceMap {
		if localProviders, ok := localMap[path]; ok {
			localH := primaryHash(localProviders)
			sourceH := primaryHash(sourceProviders)
			if localH != sourceH {
				conflicts = append(conflicts, Conflict{
					Path:            path,
					LocalHash:       localH,
					SourceHash:      sourceH,
					LocalProviders:  localProviders,
					SourceProviders: sourceProviders,
				})
			}
		}
	}

	if len(conflicts) > 0 && req.Strategy == "manual" {
		c.JSON(http.StatusConflict, gin.H{"conflicts": conflicts, "message": "resolve conflicts and re-merge with strategy=ours or theirs"})
		return
	}

	merged := make(map[string][]model.Provider)
	for k, v := range localMap {
		merged[k] = v
	}
	for path, sourceProviders := range sourceMap {
		if _, exists := localMap[path]; !exists {
			merged[path] = sourceProviders
		} else if primaryHash(localMap[path]) != primaryHash(sourceProviders) {
			if req.Strategy == "theirs" {
				merged[path] = sourceProviders
			}
		}
	}

	for path, providers := range merged {
		if err := collSvc.AddProviderEntry(local.ID, path, providers); err != nil {
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
