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
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"

	"github.com/gin-gonic/gin"
)

var storageDir string

func InitFileController(storage string) {
	storageDir = storage
	os.MkdirAll(storage, 0755)
}

// UploadFile godoc
func UploadFile(c *gin.Context) {
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash failed"})
		return
	}
	hash := hex.EncodeToString(h.Sum(nil))

	file.Seek(0, 0)

	relPath := hash[:2] + "/" + hash
	fullPath := filepath.Join(storageDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	dst, err := os.Create(fullPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save failed"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write failed"})
		return
	}

	isGzip := isGzipFile(fullPath)

	// file_meta（幂等）
	_ = repository.InsertFileMeta(&model.FileMeta{
		Hash:     hash,
		Gziped:   isGzip,
		Filename: header.Filename,
		Type:     repository.FileTypeBlob,
	})
	// file_providers
	_ = repository.InsertFileProvider(hash, "local", relPath)

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

	fullPath := filepath.Join(storageDir, req.Path)
	f, err := os.Open(fullPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file not found"})
		return
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash failed"})
		return
	}
	hash := hex.EncodeToString(h.Sum(nil))

	isGzip := isGzipFile(fullPath)

	// file_meta（hash 已存在则忽略）
	existing, _ := repository.GetFileMeta(hash)
	if existing == nil {
		_ = repository.InsertFileMeta(&model.FileMeta{
			Hash:     hash,
			Gziped:   isGzip,
			Filename: req.Filename,
			Type:     repository.FileTypeBlob,
		})
	}
	// file_providers（同一位置可重复注册）
	_ = repository.InsertFileProvider(hash, "local", req.Path)

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

	fullDir := filepath.Join(storageDir, req.FolderPath)
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder not found"})
		return
	}

	var results []map[string]string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fp := filepath.Join(fullDir, entry.Name())
		f, err := os.Open(fp)
		if err != nil {
			continue
		}
		h := sha256.New()
		io.Copy(h, f)
		f.Close()
		hash := hex.EncodeToString(h.Sum(nil))
		rel := filepath.Join(req.FolderPath, entry.Name())
		isGzip := isGzipFile(fp)

		if existing, _ := repository.GetFileMeta(hash); existing == nil {
			_ = repository.InsertFileMeta(&model.FileMeta{
				Hash:     hash,
				Gziped:   isGzip,
				Filename: entry.Name(),
				Type:     repository.FileTypeBlob,
			})
		}
		_ = repository.InsertFileProvider(hash, "local", rel)

		results = append(results, map[string]string{
			"filename": entry.Name(),
			"hash":     hash,
		})
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
	meta, err := repository.GetFileMeta(hash)
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
		"gziped":   meta.Gziped,
		"type":     meta.Type,
	})
}

// DeleteFile godoc
func DeleteFile(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}
	meta, err := repository.GetFileMeta(hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	// 删除本地文件（如果有 local provider）
	providers, _ := repository.GetFileProviders(hash)
	for _, p := range providers {
		if p.ProviderType == "local" {
			os.Remove(filepath.Join(storageDir, p.Path))
		}
	}
	// 删除 file_providers 和 file_meta
	repository.DB.Exec(`DELETE FROM file_providers WHERE hash = ?`, hash)
	repository.DB.Exec(`DELETE FROM file_meta WHERE hash = ?`, hash)
}

// isGzipFile 检测文件是否为 gzip 压缩（魔数 0x1f 0x8b）。
func isGzipFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2)
	n, _ := f.Read(buf)
	return n == 2 && buf[0] == 0x1f && buf[1] == 0x8b
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
