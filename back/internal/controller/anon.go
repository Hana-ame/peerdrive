package controller

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

var anonSvc *service.AnonService

// InitAnonController 注入 AnonService 实例供匿名集合处理函数使用。
func InitAnonController(svc *service.AnonService) {
	anonSvc = svc
}

// CreateAnonCollection godoc
// @Summary      Create anonymous collection
// @Description  Create an immutable content-addressed collection with file entries. Entries validated for path traversal and valid SHA256 hashes.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        body  body  object{friendly_name=string,entries=[]object{path=string,hash=string}}  true  "Collection entries"
// @Success      201  {object}  map[string]string  "hash"
// @Failure      400  {object}  map[string]string  "Invalid request or invalid path/hash"
// @Failure      500  {object}  map[string]string  "Internal error"
// @Router       /anon/collections [post]
func CreateAnonCollection(c *gin.Context) {
	var req struct {
		FriendlyName string                      `json:"friendly_name"`
		Entries      []model.AnonCollectionEntry `json:"entries"`
		Tags         []string                    `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash, err := anonSvc.CreateCollection(req.FriendlyName, req.Entries, req.Tags)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid path") || strings.Contains(msg, "invalid hash") || strings.Contains(msg, "invalid providers") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

// ListAnonCollections godoc
// @Summary      List anonymous collections
// @Description  Returns all anonymous collections known to this node (from file_meta).
// @Tags         anon
// @Produce      json
// @Success      200  {array}  model.AnonCollectionSummary  "List of collections"
// @Router       /anon/collections [get]
func ListAnonCollections(c *gin.Context) {
	colls, err := anonSvc.ListCollections()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, colls)
}

// GetAnonCollection godoc
// @Summary      Get anonymous collection by hash
// @Description  Retrieve an anonymous collection's metadata and entries.
// @Tags         anon
// @Produce      json
// @Param        hash  path  string  true  "Collection SHA256 hash"
// @Success      200  {object}  model.AnonCollection  "Collection"
// @Failure      404  {object}  map[string]string     "Collection not found"
// @Router       /anon/collections/{hash} [get]
func GetAnonCollection(c *gin.Context) {
	hash := c.Param("hash")
	coll, err := anonSvc.GetCollectionByHash(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	c.JSON(http.StatusOK, coll)
}

// DownloadAnonFile godoc
// @Summary      Download file from anonymous collection
// @Description  Download a specific file entry from an anonymous collection by hash and file path.
// @Tags         anon
// @Produce      octet-stream
// @Param        hash      path  string  true  "Collection SHA256 hash"
// @Param        filepath  path  string  true  "File path within collection"
// @Success      200  {file}  binary  "File content"
// @Failure      404  {object}  map[string]string  "Collection or file not found"
// @Router       /anon/collections/{hash}/{filepath} [get]
func DownloadAnonFile(c *gin.Context) {
	hash := c.Param("hash")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")

	coll, err := anonSvc.GetCollectionByHash(hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var targetEntry *model.AnonCollectionEntry
	for i := range coll.Entries {
		if coll.Entries[i].Path == filePath {
			targetEntry = &coll.Entries[i]
			break
		}
	}
	if targetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}

	// 按 provider 顺序尝试下载：sha256 优先，url 兜底
	primaryHash := targetEntry.GetPrimaryHash()
	if primaryHash != "" {
		if universalDownloader != nil {
			ctx := c.Request.Context()
			data, protocol, err := universalDownloader.Download(ctx, primaryHash)
			if err == nil {
				c.Header("X-Protocol", protocol)
				downloadFilename := filepath.Base(filePath)
				mimeType := targetEntry.GetPrimaryMime()
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				if c.Query("inline") == "1" {
					c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, downloadFilename))
				} else {
					c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, downloadFilename))
				}
				c.Data(http.StatusOK, mimeType, data)
				return
			}
		}
	}
	// fallback: URL providers
	for _, p := range targetEntry.Providers {
		if p.Type == "url" && p.Value != "" {
			c.Redirect(http.StatusFound, p.Value)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "no usable provider found for this file"})
}

// ForkAnonCollection godoc
// @Summary      Fork anonymous collection
// @Description  Create a variant of an anonymous collection by adding and/or removing file entries.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        body  body  object{source_hash=string,friendly_name=string,add_entries=[]object{path=string,hash=string},remove_paths=[]string}  true  "Fork parameters"
// @Success      201  {object}  map[string]string  "hash"
// @Failure      400  {object}  map[string]string  "Invalid request"
// @Failure      404  {object}  map[string]string  "Source collection not found"
// @Router       /anon/collections/fork [post]
func ForkAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash   string                      `json:"source_hash"`
		FriendlyName string                      `json:"friendly_name"`
		AddEntries   []model.AnonCollectionEntry `json:"add_entries"`
		RemovePaths  []string                    `json:"remove_paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	src, err := anonSvc.GetCollectionByHash(req.SourceHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	removeSet := map[string]bool{}
	for _, p := range req.RemovePaths {
		removeSet[p] = true
	}

	var newEntries []model.AnonCollectionEntry
	for _, e := range src.Entries {
		if !removeSet[e.Path] {
			newEntries = append(newEntries, e)
		}
	}

	existing := map[string]int{}
	for i, e := range newEntries {
		existing[e.Path] = i
	}
	for _, e := range req.AddEntries {
		if idx, ok := existing[e.Path]; ok {
			newEntries[idx] = e
		} else {
			newEntries = append(newEntries, e)
		}
	}

	friendlyName := req.FriendlyName
	if friendlyName == "" {
		friendlyName = src.FriendlyName
	}
	hash, err := anonSvc.CreateCollection(friendlyName, newEntries, src.Tags)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

// CommitAnonCollection godoc
// @Summary      Commit anonymous collection (versioned)
// @Description  Commit modifications to an existing anonymous collection. Accepts a list of entries to add/update (non-empty hash) or remove (empty hash). Increments version and generates a new content hash.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        body  body  object{source_hash=string,entries=[]object{path=string,hash=string},commit_message=string}  true  "Commit parameters"
// @Success      201  {object}  map[string]string  "hash"
// @Failure      400  {object}  map[string]string  "Invalid request or invalid path/hash"
// @Failure      404  {object}  map[string]string  "Source collection not found"
// @Router       /anon/collections/commit [post]
func CommitAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash    string                      `json:"source_hash"`
		Entries       []model.AnonCollectionEntry `json:"entries"`
		CommitMessage string                      `json:"commit_message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash, err := anonSvc.CommitCollection(req.SourceHash, req.Entries, req.CommitMessage)
	if err != nil {
		if strings.Contains(err.Error(), "source collection not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "invalid") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}
