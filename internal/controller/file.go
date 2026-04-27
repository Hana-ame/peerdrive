// 文件控制器 — 上传、注册本地文件/文件夹、哈希验证、删除、版本差异比较。
// 先调用 InitFileController(storageDir) 创建存储目录并保存引用。
// 上传流程：multipart → SHA256 → storage/{h[:2]}/{h} → INSERT file_meta → INSERT file_providers
// 注册流程：扫描本地文件 → SHA256 → INSERT file_meta（如不存在）→ INSERT file_providers
//
// metadata 属性（gzip/mime_type）存储在 file_meta 的专用列中。
//
// 路由：
//   POST   /files/upload           — 上传文件，按 SHA256 路径存储
//   POST   /files/register_local   — 注册已有本地文件
//   POST   /files/register_folder  — 批量注册文件夹内所有文件（不递归）
//   GET    /files/verify/:hash     — 通过哈希查询文件元数据
//   DELETE /files/:hash            — 按哈希删除文件（同时删本地文件）
//   POST   /files/diff             — 对比两个版本的条目差异

package controller

import (
	"errors"
	"net/http"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var fileSvc *service.FileService

func InitFileController(svc *service.FileService) {
	log.LogDebug("ctrl-file: InitFileController")
	fileSvc = svc
}

// UploadFile godoc
func UploadFile(c *gin.Context) {
	log.LogDebug("ctrl-file: UploadFile")
	// Enforce upload size limit based on auth status
	maxBytes := fileSvc.MaxUploadBytes(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
	log.LogDebug("ctrl-file: UploadFile maxBytes=%d", maxBytes)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		log.LogWarn("ctrl-file: UploadFile no file provided")
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	log.LogDebug("ctrl-file: UploadFile filename=%s", header.Filename)
	meta, err := fileSvc.Upload(file, header.Filename)

	if errors.Is(err, service.ErrStorageDisabled) {
		log.LogWarn("ctrl-file: UploadFile storage disabled")
		c.JSON(http.StatusForbidden, gin.H{"error": "storage is disabled"})
		return
	}
	if errors.Is(err, service.ErrFileAlreadyExists) {
		log.LogInfo("ctrl-file: UploadFile %s already exists (hash=%s)", header.Filename, meta.Hash)
		c.JSON(http.StatusOK, gin.H{
			"hash":           meta.Hash,
			"size":           meta.Size,
			"mime":           meta.MimeType,
			"filename":       meta.Filename,
			"already_exists": true,
		})
		return
	}
	if err != nil {
		log.LogError("ctrl-file: UploadFile %s failed: %v", header.Filename, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-file: UploadFile %s uploaded (hash=%s, size=%d)", header.Filename, meta.Hash, meta.Size)
	c.JSON(http.StatusCreated, gin.H{
		"hash":           meta.Hash,
		"size":           meta.Size,
		"mime":           meta.MimeType,
		"filename":       meta.Filename,
		"already_exists": false,
	})
}

// RegisterURL godoc
// POST /files/register_url
// Body: {"url": "https://...", "filename": "optional"}
// Registers a file fetched from a URL. http.Get auto-follows 301/302 redirects.
func RegisterURL(c *gin.Context) {
	log.LogDebug("ctrl-file: RegisterURL")
	var req struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-file: RegisterURL invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.URL == "" {
		log.LogWarn("ctrl-file: RegisterURL empty url")
		c.JSON(http.StatusBadRequest, gin.H{"error": "url is required"})
		return
	}

	meta, err := fileSvc.RegisterURL(req.URL, req.Filename)
	if err != nil {
		log.LogError("ctrl-file: RegisterURL %s failed: %v", req.URL, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-file: RegisterURL %s -> hash=%s size=%d", req.URL, meta.Hash, meta.Size)
	c.JSON(http.StatusCreated, gin.H{
		"hash":     meta.Hash,
		"size":     meta.Size,
		"mime":     meta.MimeType,
		"filename": meta.Filename,
	})
}

// RegisterLocalFile godoc
func RegisterLocalFile(c *gin.Context) {
	log.LogDebug("ctrl-file: RegisterLocalFile")
	var req struct {
		Path     string `json:"path"`
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-file: RegisterLocalFile invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash, err := fileSvc.RegisterLocal(req.Path, req.Filename)
	if err != nil {
		log.LogError("ctrl-file: RegisterLocalFile %s failed: %v", req.Path, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-file: RegisterLocalFile path=%s hash=%s", req.Path, hash)
	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": req.Filename})
}

// RegisterFolder godoc
func RegisterFolder(c *gin.Context) {
	log.LogDebug("ctrl-file: RegisterFolder")
	var req struct {
		FolderPath string `json:"folder_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-file: RegisterFolder invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	results, err := fileSvc.RegisterFolder(req.FolderPath)
	if err != nil {
		log.LogError("ctrl-file: RegisterFolder %s failed: %v", req.FolderPath, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-file: RegisterFolder %s registered %d files", req.FolderPath, len(results))
	c.JSON(http.StatusOK, gin.H{"registered": results})
}

// VerifyFile godoc
func VerifyFile(c *gin.Context) {
	log.LogDebug("ctrl-file: VerifyFile")
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		log.LogWarn("ctrl-file: VerifyFile invalid hash: %s", hash)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}

	meta, err := fileSvc.Verify(hash)
	if err != nil {
		log.LogError("ctrl-file: VerifyFile %s failed: %v", hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if meta == nil {
		log.LogInfo("ctrl-file: VerifyFile %s not found", hash)
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	log.LogInfo("ctrl-file: VerifyFile %s found (size=%d)", hash, meta.Size)
	c.JSON(http.StatusOK, gin.H{
		"hash":     meta.Hash,
		"filename": meta.Filename,
		"size":     meta.Size,
		"mime":     meta.MimeType,
	})
}

// DeleteFile godoc
func DeleteFile(c *gin.Context) {
	log.LogDebug("ctrl-file: DeleteFile")
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		log.LogWarn("ctrl-file: DeleteFile invalid hash: %s", hash)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}

	if err := fileSvc.Delete(hash); err != nil {
		log.LogError("ctrl-file: DeleteFile %s failed: %v", hash, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-file: DeleteFile %s deleted", hash)
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// DiffVersions godoc
func DiffVersions(c *gin.Context) {
	log.LogDebug("ctrl-file: DiffVersions")
	var req struct {
		VersionA int `json:"version_a"`
		VersionB int `json:"version_b"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-file: DiffVersions invalid request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	entriesA, err := repository.GetVersionEntries(req.VersionA)
	if err != nil {
		log.LogError("ctrl-file: DiffVersions version A %d: %v", req.VersionA, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	entriesB, err := repository.GetVersionEntries(req.VersionB)
	if err != nil {
		log.LogError("ctrl-file: DiffVersions version B %d: %v", req.VersionB, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	mapA := make(map[string]string)
	for _, e := range entriesA {
		mapA[e.Path] = e.FileHash
	}
	var added, removed, modified []map[string]string
	for _, e := range entriesB {
		if hashA, ok := mapA[e.Path]; ok {
			if hashA != e.FileHash {
				modified = append(modified, map[string]string{"path": e.Path, "old_hash": hashA, "new_hash": e.FileHash})
			}
		} else {
			added = append(added, map[string]string{"path": e.Path, "hash": e.FileHash})
		}
	}
	for _, e := range entriesA {
		found := false
		for _, eb := range entriesB {
			if eb.Path == e.Path {
				found = true
				break
			}
		}
		if !found {
			removed = append(removed, map[string]string{"path": e.Path, "hash": e.FileHash})
		}
	}
	log.LogInfo("ctrl-file: DiffVersions %d->%d: added=%d removed=%d modified=%d", req.VersionA, req.VersionB, len(added), len(removed), len(modified))
	c.JSON(http.StatusOK, gin.H{"added": added, "removed": removed, "modified": modified})
}

// ListFiles godoc
// @Summary      List all registered files
// @Description  Returns all registered files with provider info. Supports sort by time, name, path, type, size.
// @Tags         files
// @Produce      json
// @Param        sort  query  string  false  "Sort field: time|name|path|type|size"  default(time)
// @Success      200   {array}   model.FileListItem
// @Failure      500   {object}  map[string]string
// @Router       /files [get]
func ListFiles(c *gin.Context) {
	sortBy := c.DefaultQuery("sort", "time")
	log.LogDebug("ctrl-file: ListFiles sort=%s", sortBy)
	items, err := repository.ListAllFiles(sortBy)
	if err != nil {
		log.LogError("ctrl-file: ListFiles failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if items == nil {
		items = []model.FileListItem{}
	}
	c.JSON(http.StatusOK, items)
}

// BrowseDir godoc
func BrowseDir(c *gin.Context) {
	log.LogDebug("ctrl-file: BrowseDir")
	dirPath := c.Query("path")
	if dirPath == "" {
		dirPath = config.DefaultRootPath()
	}

	entries, err := fileSvc.BrowseDir(dirPath)
	if err != nil {
		log.LogError("ctrl-file: BrowseDir %s failed: %v", dirPath, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if entries == nil {
		entries = []model.DirEntry{}
	}
	log.LogInfo("ctrl-file: BrowseDir %s found %d entries", dirPath, len(entries))
	c.JSON(http.StatusOK, entries)
}
