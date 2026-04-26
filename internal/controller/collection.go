// 集合控制器 — 集合 CRUD、条目增删、版本提交/日志/回滚。
// 集合是一个命名的一组 path→hash 映射，属于某个用户。
// 通过 repository 包操作 SQLite 的 collections、collection_entries、
// collection_versions、version_entries 四张表。
//
// CID 指针机制：
//   Commit 时，除原有版本快照外，还将当前 entries 组装为 AnonCollection JSON，
//   经 anon_repo.StoreCollection 存储并获取 CID，然后调用 repo.UpdateCurrentCID。
//   GetCollection 时，若 collections.current_cid 非空，优先通过 Downloader
//   读取该 CID 的 JSON 返回 entries；否则 fallback 到 collection_entries 表。
//   Rollback 后也需重新生成 CID 并更新 current_cid。
//
// 路由：
//   POST   /collections                              — 创建集合
//   GET    /collections/:username                     — 列出用户集合
//   GET    /collections/:username/:coll               — 获取集合 + 条目列表（优先走 CID）
//   POST   /collections/:username/:coll/entries       — 添加 path→hash 条目
//   DELETE /collections/:username/:coll/entries/:path — 删除条目
//   POST   /collections/:username/:coll/commit        — 快照当前条目为版本 + 生成 CID
//   GET    /collections/:username/:coll/log           — 版本历史（最新优先）
//   POST   /collections/:username/:coll/rollback/:vid — 回滚到指定版本 + 更新 CID
//   GET    /:username/:coll/*filepath                 — 从集合下文件条目下载

package controller

import (
	"fmt"
	"net/http"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
)

// CreateCollection godoc
// @Summary Create a new collection
// @Description Create a named collection for a user. Duplicate (username, collection_name) returns 409.
// @Tags collections
// @Accept json
// @Produce json
// @Param body body object{username=string,collection_name=string} true "Username and collection name"
// @Success 200 {object} map[string]interface{} "id, username, collection_name"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 409 {object} map[string]string "Duplicate"
// @Router /collections [post]
func CreateCollection(c *gin.Context) {
	var req struct {
		Username       string `json:"username"`
		CollectionName string `json:"collection_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	id, err := repository.CreateCollection(req.Username, req.CollectionName)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "username": req.Username, "collection_name": req.CollectionName})
}

// ListCollections godoc
// @Summary List collections for a user
// @Description Returns all collections owned by the given username
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/{username} [get]
func ListCollections(c *gin.Context) {
	username := c.Param("username")
	cols, err := repository.ListCollections(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cols == nil {
		cols = []model.Collection{}
	}
	c.JSON(http.StatusOK, gin.H{"data": cols})
}

// SearchCollections godoc
// @Summary Search collections
// @Description Search collections by username or collection name
// @Tags collections
// @Produce json
// @Param q query string true "Search query"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/search [get]
func SearchCollections(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"data": []model.Collection{}})
		return
	}
	cols, err := repository.SearchCollections(q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cols == nil {
		cols = []model.Collection{}
	}
	c.JSON(http.StatusOK, gin.H{"data": cols})
}

// GetCollection godoc
// @Summary Get collection details with entries
// @Description Returns collection metadata and its path→hash entries
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Success 200 {object} map[string]interface{} "collection and entries"
// @Failure 404 {object} map[string]string "Not found"
// @Router /collections/{username}/{collection_name} [get]
func GetCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var entries []model.CollectionEntry
	if col.CurrentHash != nil && *col.CurrentHash != "" {
		storageDir := c.MustGet("storageDir").(string)
		anonColl, err := repository.GetAnonCollectionByHash(*col.CurrentHash, storageDir)
		if err == nil {
			entries = make([]model.CollectionEntry, 0, len(anonColl.Entries))
			for _, ae := range anonColl.Entries {
				entries = append(entries, model.CollectionEntry{
					Path: ae.Path,
					FileHash: ae.Hash,
				})
			}
		}
	}
	if entries == nil {
		entries, err = repository.ListCollectionEntries(col.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if entries == nil {
		entries = []model.CollectionEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"collection": col, "entries": entries})
}

// AddEntry godoc
// @Summary Add an entry to a collection
// @Description Map a path (e.g. "dir/file.txt") to a SHA256 hash within a collection. Creates the collection if it doesn't exist.
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{path=string,hash=string} true "Entry path and file hash"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid request"
// @Router /collections/{username}/{collection_name}/entries [post]
func AddEntry(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	var req struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	collID, err := repository.GetOrCreateCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := repository.AddCollectionEntry(collID, req.Path, req.Hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry added"})
}

// RemoveEntry godoc
// @Summary Remove an entry from a collection
// @Description Delete a path→hash mapping from a collection
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param path path string true "Entry path (URL-encoded)"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/entries/{path} [delete]
func RemoveEntry(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	path := strings.TrimPrefix(c.Param("path"), "/")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := repository.RemoveCollectionEntry(col.ID, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry removed"})
}

// DownloadCollectionFile godoc
// @Summary Download a file from a collection
// @Description Look up the path in the collection's entries and redirect to /sha256sum/{hash} for download
// @Tags collections
// @Produce octet-stream
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param filepath path string true "File path within the collection"
// @Success 200 {file} binary "File content"
// @Failure 404 {object} map[string]string "Collection or file not found"
// @Router /{username}/{collection_name}/{filepath} [get]
func DownloadCollectionFile(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	filepath := strings.TrimPrefix(c.Param("filepath"), "/")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	entry, err := repository.GetCollectionEntry(col.ID, filepath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}
	DownloadBySHA256Internal(c, entry.FileHash)
}

// CommitCollection godoc
// @Summary Commit collection entries as a new version
// @Description Snapshot all current entries into a version with a commit message. Creates a linked version chain.
//   Also generates an anonymous collection JSON (SHA256), updates collections.current_hash.
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{commit_message=string} true "Commit message"
// @Success 200 {object} map[string]interface{} "message, version_number, snapshot_hash"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/commit [post]
func CommitCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	var req struct {
		CommitMessage string `json:"commit_message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	// 1. 获取当前工作区条目
	entries, err := repository.ListCollectionEntries(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 2. 转换为匿名集合的 entries
	anonEntries := make([]model.AnonCollectionEntry, 0, len(entries))
	for _, e := range entries {
		anonEntries = append(anonEntries, model.AnonCollectionEntry{
			Path: e.Path,
			Hash: e.FileHash,
		})
	}

	// 3. 构造匿名集合并保存
	anonColl := model.NewAnonCollection("", anonEntries)
	storageDir := c.MustGet("storageDir").(string)
	hash, err := repository.SaveCollection(anonColl, storageDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create snapshot: " + err.Error()})
		return
	}

	// 4. 更新 current_hash
	if err := repository.UpdateCurrentHash(col.ID, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update current hash: " + err.Error()})
		return
	}

	// 5. 原有版本快照逻辑 (保留历史)
	versions, err := repository.GetVersionLog(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var parentID *int
	if len(versions) > 0 {
		parentID = &versions[0].ID
	}
	verID, verNum, err := repository.CreateVersion(col.ID, req.CommitMessage, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := repository.SnapshotVersionEntries(verID, col.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "committed", "version_number": verNum, "snapshot_hash": hash})
}
// GetVersionLog godoc
// @Summary Get collection version history
// @Description Returns all committed versions for a collection, newest first
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Success 200 {object} map[string]interface{} "data array of versions"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/log [get]
func GetVersionLog(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	versions, err := repository.GetVersionLog(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if versions == nil {
		versions = []model.CollectionVersion{}
	}
	c.JSON(http.StatusOK, gin.H{"data": versions})
}

// RollbackCollection godoc
// @Summary Rollback collection to a previous version
// @Description Restore collection entries from a specific version's snapshot. Replaces all current entries.
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param version_id path int true "Version ID to restore"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid version_id"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/rollback/{version_id} [post]
func RollbackCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	versionID := c.Param("version_id")
	var vid int
	if _, err := fmt.Sscanf(versionID, "%d", &vid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version_id"})
		return
	}
	col, err := repository.GetCollection(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := repository.RestoreVersionEntries(vid, col.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 重新生成 CID
	entries, err := repository.ListCollectionEntries(col.ID)
	if err == nil {
		anonEntries := make([]model.AnonCollectionEntry, 0, len(entries))
		for _, e := range entries {
			anonEntries = append(anonEntries, model.AnonCollectionEntry{Path: e.Path, Hash: e.FileHash})
		}
		anonColl := model.NewAnonCollection("", anonEntries)
		storageDir := c.MustGet("storageDir").(string)
		hash, err := repository.SaveCollection(anonColl, storageDir)
		if err == nil {
			repository.UpdateCurrentHash(col.ID, hash)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "rolled back"})
}
