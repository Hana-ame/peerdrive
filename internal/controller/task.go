// Task controller — query async task status.
// Usage:
//   GET /tasks     — list tasks (currently returns empty)
//   GET /tasks/:id — get task status by ID

package controller

import (
	"net/http"
	"strconv"

	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

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
