package controller

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"peerdrive/internal/repository"
)

// GetShaTags godoc
// @Summary Get tags for a file sha256
// @Tags tags
// @Produce json
// @Param sha path string true "File SHA-256"
// @Success 200 {object} map[string]interface{} "data array of tags"
// @Router /tags/sha/{sha} [get]
func GetShaTags(c *gin.Context) {
	sha := c.Param("sha")
	tags, err := repository.GetShaTags(sha)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// 同时回显 data 与 tags，兼容前端不同消费点（data.data vs data.tags）
	c.JSON(http.StatusOK, gin.H{"data": tags, "tags": tags, "sha": sha})
}

// SetShaTags godoc
// @Summary Set tags for a file sha256
// @Tags tags
// @Accept json
// @Produce json
// @Param sha path string true "File SHA-256"
// @Param body body map[string]interface{} true "Tags payload: {tags: ['tag1', 'tag2']}"
// @Success 200 {object} map[string]interface{} "status and tags"
// @Router /tags/sha/{sha} [post]
func SetShaTags(c *gin.Context) {
	sha := c.Param("sha")
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if err := repository.SetShaTags(sha, req.Tags); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "tags": req.Tags, "data": req.Tags, "sha": sha})
}

// BatchGetShaTags godoc
// @Summary Batch get tags for multiple file sha256 hashes
// @Tags tags
// @Accept json
// @Produce json
// @Param body body map[string]interface{} true "Payload: {shas: ['sha1', 'sha2']}"
// @Success 200 {object} map[string]interface{} "mapping of sha -> tags"
// @Router /tags/batch [post]
func BatchGetShaTags(c *gin.Context) {
	var req struct {
		Shas []string `json:"shas"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	// 边界与防御：限制单次批量上限（1000 个），防止内存 DoS
	const maxBatchShas = 1000
	shas := req.Shas
	if len(shas) > maxBatchShas {
		shas = shas[:maxBatchShas]
	}
	tagsMap, err := repository.GetBatchShaTags(shas)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": tagsMap, "tags": tagsMap})
}

// GetAllTagsSummary godoc
// @Summary Get all distinct tags and their file counts
// @Tags tags
// @Produce json
// @Success 200 {object} map[string]interface{} "list of tags with counts"
// @Router /tags/summary [get]
func GetAllTagsSummary(c *gin.Context) {
	tagCounts, err := repository.GetAllTagsWithCounts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": tagCounts, "tags": tagCounts, "total": len(tagCounts)})
}

// SearchTags godoc
// @Summary Search files matching a tag
// @Tags tags
// @Produce json
// @Param tag query string true "Tag name"
// @Success 200 {object} map[string]interface{} "data array of matching files"
// @Router /tags/search [get]
func SearchTags(c *gin.Context) {
	tag := strings.TrimSpace(c.Query("tag"))
	if tag == "" {
		c.JSON(http.StatusOK, gin.H{"data": []repository.FileIndex{}})
		return
	}
	files, err := repository.SearchFilesByTag(tag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": files, "tag": tag})
}
