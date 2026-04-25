// 文件控制器 — 上传、注册本地文件/文件夹、哈希验证、删除、版本差异比较。
// 先调用 InitFileController(storageDir) 创建存储目录并保存引用。
// 上传流程：multipart 读取 → SHA256 计算 → 检测 gzip 魔数（0x1f 0x8b） →
//   存储到 storage/{hex[0:2]}/{hex} → repository.InsertFile 写入 SQLite（含 is_gzip 标记）。
// 注册流程：扫描本地文件 → SHA256 计算 → 同上传检测 gzip → 仅写入 DB 不复制。
// 差异比较：对比两个 version_entries 的快照，返回 added/removed/modified。
// gzip 检测：读取文件前 2 字节，若为 0x1f 0x8b 则视作 gzip 压缩。
//   该标记影响 /sha256sum/:hash 下载时是否设置 Content-Encoding: gzip 头。
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
// @Summary Upload a file
// @Description Upload a file, compute its SHA256 hash, store to disk at storage/{first2}/{hash}, and register in the database. Deduplicates by hash.
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "File to upload"
// @Success 200 {object} map[string]string "hash and filename"
// @Failure 400 {object} map[string]string "Missing file"
// @Failure 409 {object} map[string]string "File already exists"
// @Router /files/upload [post]
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

	// 检测 gzip 魔数
	isGzip := false
	f, _ := os.Open(fullPath)
	if f != nil {
		buf := make([]byte, 2)
		if n, _ := f.Read(buf); n == 2 && buf[0] == 0x1f && buf[1] == 0x8b {
			isGzip = true
		}
		f.Close()
	}

	meta := &model.FileMetadata{
		Hash:         hash,
		ProviderType: "local",
		Path:         relPath,
		Filename:     header.Filename,
		IsGzip:       isGzip,
	}
	if err := repository.InsertFile(meta); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "file already exists", "hash": hash})
		return
	}

	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": header.Filename})
}

// RegisterLocalFile godoc
// @Summary Register a local file
// @Description Register an already-existing file in the storage directory. Computes its SHA256 and inserts into the database without copying.
// @Tags files
// @Accept json
// @Produce json
// @Param body body object{path=string,filename=string} true "Local path and display filename"
// @Success 200 {object} map[string]string "hash, filename, note"
// @Failure 400 {object} map[string]string "Invalid request or file not found"
// @Router /files/register_local [post]
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

	existing, _ := repository.GetFileByHash(hash)
	if existing != nil {
		c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": req.Filename, "note": "already registered"})
		return
	}

	// 检测 gzip 魔数
	isGzip := false
	gzF, _ := os.Open(fullPath)
	if gzF != nil {
		buf := make([]byte, 2)
		if n, _ := gzF.Read(buf); n == 2 && buf[0] == 0x1f && buf[1] == 0x8b {
			isGzip = true
		}
		gzF.Close()
	}

	meta := &model.FileMetadata{
		Hash:         hash,
		ProviderType: "local",
		Path:         req.Path,
		Filename:     req.Filename,
		IsGzip:       isGzip,
	}
	if err := repository.InsertFile(meta); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": req.Filename})
}

// RegisterFolder godoc
// @Summary Register all files in a folder
// @Description Batch-register every file in the given subdirectory of storage/. Non-recursive, skips subdirectories.
// @Tags files
// @Accept json
// @Produce json
// @Param body body object{folder_path=string} true "Relative folder path under storage/"
// @Success 200 {object} map[string]interface{} "registered array"
// @Failure 400 {object} map[string]string "Folder not found"
// @Router /files/register_folder [post]
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

		// 检测 gzip 魔数
		isGzip := false
		gzF, _ := os.Open(fp)
		if gzF != nil {
			buf := make([]byte, 2)
			if n, _ := gzF.Read(buf); n == 2 && buf[0] == 0x1f && buf[1] == 0x8b {
				isGzip = true
			}
			gzF.Close()
		}

		existing, _ := repository.GetFileByHash(hash)
		if existing == nil {
			repository.InsertFile(&model.FileMetadata{
				Hash:         hash,
				ProviderType: "local",
				Path:         rel,
				Filename:     entry.Name(),
				IsGzip:       isGzip,
			})
		}
		results = append(results, map[string]string{
			"filename": entry.Name(),
			"hash":     hash,
		})
	}
	c.JSON(http.StatusOK, gin.H{"registered": results})
}

// VerifyFile godoc
// @Summary Verify a file by hash
// @Description Look up file metadata (filename, provider, path) by its SHA256 hash
// @Tags files
// @Produce json
// @Param hash path string true "SHA256 hash"
// @Success 200 {object} map[string]string "hash, filename, provider, path"
// @Failure 400 {object} map[string]string "Invalid hash"
// @Failure 404 {object} map[string]string "Not found"
// @Router /files/verify/{hash} [get]
func VerifyFile(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}
	meta, err := repository.GetFileByHash(hash)
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
		"provider": meta.ProviderType,
		"path":     meta.Path,
		"is_gzip":  meta.IsGzip,
	})
}

// DeleteFile godoc
// @Summary Delete a file by hash
// @Description Remove file metadata from database and delete the local file if provider_type == "local"
// @Tags files
// @Produce json
// @Param hash path string true "SHA256 hash"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid hash"
// @Failure 404 {object} map[string]string "Not found"
// @Router /files/{hash} [delete]
func DeleteFile(c *gin.Context) {
	hash := c.Param("hash")
	if !hashutil.IsValidSHA256(hash) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha256"})
		return
	}
	meta, err := repository.GetFileByHash(hash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if meta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	if meta.ProviderType == "local" {
		os.Remove(filepath.Join(storageDir, meta.Path))
	}
	repository.DeleteFile(hash)
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// DiffVersions godoc
// @Summary Diff entries between two versions
// @Description Compare version_entries between two collection versions and return added/removed/modified lists
// @Tags files
// @Accept json
// @Produce json
// @Param body body object{version_a=int,version_b=int} true "Version IDs to compare"
// @Success 200 {object} map[string]interface{} "added, removed, modified"
// @Failure 400 {object} map[string]string "Invalid request"
// @Router /files/diff [post]
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
