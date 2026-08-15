// 任务控制器 — 查询异步任务状态。
// 异步任务（如 pull）通过 transfer_tasks 表跟踪状态（pending/completed/failed）。
// 技术实现：ListTasks 当前返回空数组占位；GetTaskStatus 通过
//   repository.GetTask 查询 SQLite 并返回完整任务记录。
// 路由：
//   GET /tasks     — 列出所有任务（当前返回空占位）
//   GET /tasks/:id — 按 ID 查询任务状态和结果

package controller

import (
	"net/http"
	"strconv"

	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// taskSvc 任务服务（M2 收层：不再直调 repository）。
var taskSvc *service.TaskService

// InitTaskController 注入 TaskService 实例。
func InitTaskController(svc *service.TaskService) {
	taskSvc = svc
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
	task, err := taskSvc.Get(id)
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
