package controller

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/nodestate"
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
		// Visibility/AccessList 对应前端「公开访问 / 仅限指定权限 / 仅自己」三选项。
		// 旧前端不带这两个字段 → visibility 空串 → 服务层兜底为 public，行为不变。
		Visibility string   `json:"visibility"`
		AccessList []string `json:"access_list"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Owner 取本节点 operator：登录到 regserver 的节点才有账号，匿名节点是空串
	// （private 合集在无主状态下无人能读，所以前端未登录时应禁掉「仅自己」选项）。
	owner := nodestate.GetOperator()

	hash, err := anonSvc.CreateCollectionWithVisibility(req.FriendlyName, req.Entries, req.Tags, req.Visibility, req.AccessList, owner)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "invalid visibility"), strings.Contains(msg, "access_list required"):
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		case strings.Contains(msg, "invalid path"), strings.Contains(msg, "invalid hash"), strings.Contains(msg, "invalid providers"):
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash, "visibility": req.Visibility, "owner": owner})
}

// SetAnonCollectionVisibility godoc
// @Summary      Update visibility of an anonymous collection
// @Description  Switch a collection between public / restricted (access_list) / private.
//
//	The collection is content-addressed: changing visibility writes a new JSON file,
//	so the response carries the NEW hash — the old hash still resolves to the old setting.
//
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        hash path string true "Collection SHA256 hash"
// @Param        body body object{visibility=string,access_list=[]string} true "Visibility settings"
// @Success      200 {object} map[string]string "hash, visibility"
// @Failure      400 {object} map[string]string "Invalid visibility"
// @Failure      404 {object} map[string]string "Not found or not owned by this operator"
// @Router       /anon/collections/{hash}/visibility [put]
func SetAnonCollectionVisibility(c *gin.Context) {
	hash := c.Param("hash")
	var req struct {
		Visibility string   `json:"visibility"`
		AccessList []string `json:"access_list"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	newHash, err := anonSvc.UpdateCollectionVisibility(hash, req.Visibility, req.AccessList, nodestate.GetOperator())
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid visibility") || strings.Contains(msg, "access_list required") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": newHash, "previous_hash": hash, "visibility": req.Visibility})
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
	// 可见性闸门：private/restricted 合集对无权请求者等价于「不存在」（404）。
	// 坑：这里曾用 GetCollectionByHash 裸读——content-addressed 的 JSON 一旦
	// 拿到 hash 谁都能取，权限三档就形同虚设（下载已走 DownloadAnonFile 的
	// 可见性检查，元数据入口漏掉等于绕开）。本节点 operator 当作请求者身份。
	coll, err := anonSvc.GetCollectionVisibleTo(hash, nodestate.GetOperator())
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

	coll, err := anonSvc.GetCollectionVisibleTo(hash, nodestate.GetOperator())
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

	// 源集合按可见性读取（同 GetAnonCollection）：否则拿到任意 hash 就能把
	// private/restricted 合集 fork 成一份 public 副本 —— 权限三档被 fork 绕过。
	operator := nodestate.GetOperator()
	src, err := anonSvc.GetCollectionVisibleTo(req.SourceHash, operator)
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
	// fork 是本机新建的副本，但权限档位必须继承源集合：源为 restricted/private
	// 时，副本若默认 public 等于把受限内容重新公开（同一份文件换个 hash 就绕过权限）。
	// Owner 延续源集合（源无 Owner 时记为本机 operator，保证 private 副本仍可读）。
	forkOwner := src.Owner
	if forkOwner == "" {
		forkOwner = operator
	}
	hash, err := anonSvc.CreateCollectionWithVisibility(
		friendlyName, newEntries, src.Tags, src.Visibility, src.AccessList, forkOwner)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid visibility") || strings.Contains(msg, "access_list required") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
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

	hash, err := anonSvc.CommitCollection(req.SourceHash, req.Entries, req.CommitMessage, nodestate.GetOperator())
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
