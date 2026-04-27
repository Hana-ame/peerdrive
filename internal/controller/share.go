package controller

import (
	"net/http"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

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

	share, err := repository.CreateShare(req.Hash, req.Type, req.Filename)
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

func AccessShare(c *gin.Context) {
	token := c.Param("token")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token required"})
		return
	}

	share, err := repository.GetShareByToken(token)
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

func ListShares(c *gin.Context) {
	shares, err := repository.ListShares()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if shares == nil {
		shares = []model.ShareLink{}
	}
	c.JSON(http.StatusOK, gin.H{"shares": shares})
}
