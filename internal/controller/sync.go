// 本地同步控制器 — 将集合文件保存到本地磁盘并查询同步状态。
package controller

import (
	"net/http"
	"strconv"

	"peerdrive/internal/repository"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

type SyncController struct {
	syncSvc *service.SyncService
}

func NewSyncController(syncSvc *service.SyncService) *SyncController {
	return &SyncController{syncSvc: syncSvc}
}

func (c *SyncController) SaveLocal(ctx *gin.Context) {
	var req model.SaveLocalRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.CollectionHash == "" || req.LocalPath == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "collection_hash and local_path are required"})
		return
	}

	if err := c.syncSvc.SaveToDisk(req); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "collection sync started/completed successfully"})
}

func (c *SyncController) GetStatus(ctx *gin.Context) {
	hash := ctx.Param("hash")
	if hash == "" {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "hash is required"})
		return
	}

	status, err := c.syncSvc.GetStatus(hash)
	if err != nil {
		ctx.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, status)
}

// GetTaskStatus godoc
// @Summary Get task status by ID
// @Description Query the status and result of an async task (e.g., pull)
// @Tags tasks
// @Produce json
// @Param id path int true "Task ID"
// @Success 200 {object} map[string]interface{} "task details"
// @Failure 400 {object} map[string]string "Invalid task ID"
// @Failure 404 {object} map[string]string "Not found"
// @Router /tasks/{id} [get]
func GetTaskStatus(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}
	task, err := repository.GetTask(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": task})
}

// ListTasks godoc
// @Summary List all tasks (stub)
// @Description Returns an empty task list
// @Tags tasks
// @Produce json
// @Success 200 {object} map[string]interface{} "tasks array"
// @Router /tasks [get]
func ListTasks(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tasks": []interface{}{}})
}
