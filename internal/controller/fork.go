// 复刻/拉取控制器 — 将源集合的所有条目复制到新集合（Fork），
// 或同步上游更新（Pull，当前为占位实现）。
// Fork 流程：查询源集合 → GetOrCreate 目标集合 → 逐条复制
//   collection_entries 内容。
// Pull 流程：验证请求 → 返回 "not implemented" 消息 → 创建
//   transfer_tasks 记录并标记为 completed。
// 路由：
//   POST /actions/fork — 将源集合条目复制到新本地集合
//   POST /actions/pull — 拉取上游更新（占位，v2 实现）

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
		providers := e.BuildProviders()
		if len(providers) > 0 && e.ProvidersJSON != "" {
			if err := repository.AddProviderCollectionEntry(localID, e.Path, providers); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		} else {
			if err := repository.AddCollectionEntry(localID, e.Path, e.FileHash); err != nil {
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
