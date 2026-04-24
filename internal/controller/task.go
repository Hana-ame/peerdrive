package controller

import (
	"net/http"
	"strconv"

	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

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

func ListTasks(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tasks": []interface{}{}})
}
