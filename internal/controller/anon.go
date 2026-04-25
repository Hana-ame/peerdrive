// 匿名合集控制器 — 创建、读取、下载匿名合集内的文件。
//
// 匿名合集是不可变的内容寻址 JSON，hash = SHA256(规范化 JSON)。
//
// 路由：
//   POST   /anon/collections        — 创建匿名合集
//     body: {"name":"...", "description":"...", "entries":[{"path":"...","hash":"...","size":123}]}
//     处理：排序 entries → json.Marshal → SHA256 → SaveCollection → 返回 hash
//     返回: {"hash": "sha256hex..."}
//
//   GET    /anon/collections/:hash    — 读取匿名合集
//     处理：GetCollection(hash) → 返回 AnonCollection JSON
//
//   GET    /anon/collections/:hash/entries/*filepath — 下载合集内某个文件
//     处理：解析 JSON → 查找 path 对应的 hash →
//       若本地有则直接提供；若有 URL 则 302 重定向；否则尝试 P2P 拉取
//     返回: 200 stream 或 302 Redirect
//
//   POST   /anon/collections/fork    — Fork 匿名合集（本质是创建新合集）
//     body: {"source_hash":"...", "add_entries":[...], "remove_paths":[...]}
//     处理：读取源 JSON → 增删 entries → 重新排序 → 生成新 hash → 存储
//     返回: {"hash": "new_sha256hex..."}
//
// 注意：匿名合集一旦创建不可修改；Fork 本质是创建新合集。
// entries 的 path 在 URL 中以 *path 参数传递，需要 strings.TrimPrefix(c.Param("path"), "/")
//
// 依赖：
//   - c.MustGet("storageDir")
//   - c.MustGet("downloader")
package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"github.com/gin-gonic/gin"
)

func CreateAnonCollection(c *gin.Context) {
	var req model.AnonCollection
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	storageDir := c.MustGet("storageDir").(string)
	hash, err := repository.SaveCollection(&req, storageDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

func GetAnonCollection(c *gin.Context) {
	hash := c.Param("hash")
	storageDir := c.MustGet("storageDir").(string)
	coll, err := repository.GetAnonCollectionByHash(hash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	c.JSON(http.StatusOK, coll)
}

func DownloadAnonFile(c *gin.Context) {
	hash := c.Param("hash")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")
	storageDir := c.MustGet("storageDir").(string)

	coll, err := repository.GetAnonCollectionByHash(hash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var targetEntry *model.AnonEntry
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

	meta, _ := repository.GetFileByHash(targetEntry.Hash)
	if meta != nil && meta.ProviderType == "local" {
		downloader := c.MustGet("downloader").(*service.Downloader)
		reader, filename, metaJSON, err := downloader.GetFileStream(targetEntry.Hash)
		if err == nil {
			defer reader.Close()
			c.Header("Content-Disposition", "attachment; filename="+filename)
			var metaMap map[string]any
			if metaJSON != "" {
				json.Unmarshal([]byte(metaJSON), &metaMap)
			}
			if isGzip, _ := metaMap["is_gzip"].(bool); isGzip {
				c.Header("Content-Encoding", "gzip")
			}
			c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
			return
		}
	}

	if targetEntry.URL != nil && *targetEntry.URL != "" {
		c.Redirect(http.StatusFound, *targetEntry.URL)
		return
	}

	downloader := c.MustGet("downloader").(*service.Downloader)
	reader, filename, metaJSON, err := downloader.GetFileStream(targetEntry.Hash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file data not available"})
		return
	}
	defer reader.Close()
	c.Header("Content-Disposition", "attachment; filename="+filename)
	var metaMap map[string]any
	if metaJSON != "" {
		json.Unmarshal([]byte(metaJSON), &metaMap)
	}
	if isGzip, _ := metaMap["is_gzip"].(bool); isGzip {
		c.Header("Content-Encoding", "gzip")
	}
	c.DataFromReader(http.StatusOK, -1, "application/octet-stream", reader, nil)
}

func ForkAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash  string           `json:"source_hash"`
		AddEntries []model.AnonEntry `json:"add_entries"`
		RemovePaths []string         `json:"remove_paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	storageDir := c.MustGet("storageDir").(string)
	src, err := repository.GetAnonCollectionByHash(req.SourceHash, storageDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	newEntries := []model.AnonEntry{}
	removeSet := map[string]bool{}
	for _, p := range req.RemovePaths {
		removeSet[p] = true
	}
	for _, e := range src.Entries {
		if !removeSet[e.Path] {
			newEntries = append(newEntries, e)
		}
	}
	for _, e := range req.AddEntries {
		found := false
		for i := range newEntries {
			if newEntries[i].Path == e.Path {
				newEntries[i] = e
				found = true
				break
			}
		}
		if !found {
			newEntries = append(newEntries, e)
		}
	}

	newColl := &model.AnonCollection{
		Version:     1,
		Name:        src.Name + " (forked)",
		Description: src.Description,
		Entries:     newEntries,
	}
	hash, err := repository.SaveCollection(newColl, storageDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}
