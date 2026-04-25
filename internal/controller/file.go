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
	"net/http"

	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var fileSvc *service.FileService

func InitFileController(svc *service.FileService) {
	fileSvc = svc
}

// UploadFile godoc
func UploadFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	hash, err := fileSvc.Upload(file, header.Filename)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": header.Filename})
}

// RegisterLocalFile godoc
func RegisterLocalFile(c *gin.Context) {
	var req struct {
		Path     string `json:"path"`
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	hash, err := fileSvc.RegisterLocal(req.Path, req.Filename)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": req.Filename})
}

// RegisterFolder godoc
func RegisterFolder(c *gin.Context) {
	var req struct {
		FolderPath string `json:"folder_path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	results, err := fileSvc.RegisterFolder(req.FolderPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"registered": results})
}

// VerifyFile godoc
func VerifyFile(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}

	meta, err := fileSvc.Verify(hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"hash":     meta.Hash,
		"filename": meta.Filename,
		"size":     meta.Size,
		"mime":     meta.MimeType,
	})
}

// DeleteFile godoc
func DeleteFile(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}

	if err := fileSvc.Delete(hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// DiffVersions godoc
func DiffVersions(c *gin.Context) {
	var req struct {
		VersionA int `json:"version_a"`
		VersionB int `json:"version_b"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	entriesA, err := repository.GetVersionEntries(req.VersionA)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	entriesB, err := repository.GetVersionEntries(req.VersionB)
	if err != nil {
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
	c.JSON(http.StatusOK, gin.H{"added": added, "removed": removed, "modified": modified})
}
