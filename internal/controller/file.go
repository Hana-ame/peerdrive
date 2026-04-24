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

	meta := &model.FileMetadata{
		Hash:         hash,
		ProviderType: "local",
		Path:         relPath,
		Filename:     header.Filename,
	}
	if err := repository.InsertFile(meta); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "file already exists", "hash": hash})
		return
	}

	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": header.Filename})
}

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

	meta := &model.FileMetadata{
		Hash:         hash,
		ProviderType: "local",
		Path:         req.Path,
		Filename:     req.Filename,
	}
	if err := repository.InsertFile(meta); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": req.Filename})
}

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

		existing, _ := repository.GetFileByHash(hash)
		if existing == nil {
			repository.InsertFile(&model.FileMetadata{
				Hash:         hash,
				ProviderType: "local",
				Path:         rel,
				Filename:     entry.Name(),
			})
		}
		results = append(results, map[string]string{
			"filename": entry.Name(),
			"hash":     hash,
		})
	}
	c.JSON(http.StatusOK, gin.H{"registered": results})
}

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
	})
}

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
