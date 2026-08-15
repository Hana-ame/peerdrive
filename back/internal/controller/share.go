// 分享链接控制器 — 创建、访问和列出分享链接（文件或合集）。
package controller

import (
	"net/http"

	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// shareSvc 分享服务（M2 收层：不再直调 repository）。
var shareSvc *service.ShareService

// InitShareController 注入 ShareService 实例。
func InitShareController(svc *service.ShareService) {
	shareSvc = svc
}

// CreateShare 处理 POST /shares，创建文件或集合的分享链接。
func CreateShare(c *gin.Context) {
	var req struct {
		Hash     string `json:"hash" binding:"required"`
		Type     string `json:"type" binding:"required"` // file | collection
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "hash and type required"})
		return
	}
	if req.Type != "file" && req.Type != "collection" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be file or collection"})
		return
	}

	share, err := shareSvc.Create(req.Hash, req.Type, req.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":    share.Token,
		"hash":     share.Hash,
		"type":     share.Type,
		"filename": share.Filename,
		"url":      "/s/" + share.Token,
		"expires":  share.ExpiresAt,
	})
}

// AccessShare 处理 GET /s/:token，按 token 访问分享链接并重定向到文件或集合。
func AccessShare(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}

	share, err := shareSvc.GetByToken(token)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share not found or expired"})
		return
	}

	// Redirect based on type
	if share.Type == "collection" {
		c.Redirect(http.StatusFound, "/anon/collections/"+share.Hash)
		return
	}

	// File: redirect to download
	c.Redirect(http.StatusFound, "/sha256sum/"+share.Hash)
}

// ListShares 处理 GET /shares，列出所有未过期的分享链接。
func ListShares(c *gin.Context) {
	shares, err := shareSvc.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if shares == nil {
		shares = []model.ShareLink{}
	}
	c.JSON(http.StatusOK, gin.H{"shares": shares})
}
