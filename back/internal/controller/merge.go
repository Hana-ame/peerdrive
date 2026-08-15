// 合并控制器 — 将源集合条目合并到本地集合。
// 支持三种策略：
//   "ours"   — 冲突时保留本地哈希
//   "theirs" — 冲突时接受源哈希
//   "manual" — 检测冲突后返回 409 + 冲突列表，前端解决后重试
// 合并流程：读本地和源集合的全部条目 → 按 path 建立 map →
//   检测冲突（同 path 不同 hash）→ 按策略合并 → 逐条 upsert 到本地。
// 路由：
//   POST /actions/merge — 合并源集合到本地集合

package controller

import (
	"net/http"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"

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

	localMap := make(map[string][]model.Provider)
	for _, e := range localEntries {
		localMap[e.Path] = e.BuildProviders()
	}
	sourceMap := make(map[string][]model.Provider)
	for _, e := range sourceEntries {
		sourceMap[e.Path] = e.BuildProviders()
	}

	// 提取主 hash 用于冲突检测
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
		if err := repository.AddProviderCollectionEntry(local.ID, path, providers); err != nil {
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
