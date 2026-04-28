// 本地同步控制器 — 将集合文件保存到本地磁盘并查询同步状态。
package controller

import (
	"net/http"
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
