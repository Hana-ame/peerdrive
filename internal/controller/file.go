package controller

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"
)

// UploadFile handles POST /files/upload (multipart form)
func UploadFile(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}

	src, err := file.Open()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot open file"})
		return
	}
	defer src.Close()

	hash, err := hashutil.SHA256(src)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash failed"})
		return
	}

	// Check if already exists
	if repository.FileMetaExists(hash) {
		c.JSON(http.StatusOK, gin.H{
			"hash":           hash,
			"filename":       file.Filename,
			"size":           file.Size,
			"already_exists": true,
		})
		return
	}

	// Save to storage
	destPath := fileProvider.BaseDir + "/" + hash
	if err := c.SaveUploadedFile(file, destPath); err != nil {
		log.Error("upload save: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save failed"})
		return
	}

	mime := file.Header.Get("Content-Type")

	meta := &model.FileMeta{
		Hash:         hash,
		Filename:     file.Filename,
		Size:         file.Size,
		MimeType:     mime,
		ProviderType: "local",
		ProviderPath: destPath,
	}
	repository.InsertFileMeta(meta)
	repository.InsertFileProvider(&model.FileProvider{
		Hash:         hash,
		ProviderType: "local",
		ProviderPath: destPath,
	})

	log.Info("upload: %s → %s (%d bytes)", file.Filename, hash[:16], file.Size)
	c.JSON(http.StatusCreated, gin.H{
		"hash":     hash,
		"filename": file.Filename,
		"size":     file.Size,
		"mime":     mime,
	})
}

// RegisterLocalFile handles POST /files/register_local
func RegisterLocalFile(c *gin.Context) {
	var req struct {
		Path     string `json:"path"`
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	hash, err := hashutil.SHA256File(req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read file: " + err.Error()})
		return
	}

	filename := req.Filename
	if filename == "" {
		filename = filepath.Base(req.Path)
	}

	if repository.FileMetaExists(hash) {
		c.JSON(http.StatusOK, gin.H{"hash": hash, "filename": filename, "already_exists": true})
		return
	}

	info, _ := os.Stat(req.Path)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	mime := "application/octet-stream"

	meta := &model.FileMeta{
		Hash:         hash,
		Filename:     filename,
		Size:         size,
		MimeType:     mime,
		ProviderType: "local",
		ProviderPath: req.Path,
	}
	repository.InsertFileMeta(meta)
	repository.InsertFileProvider(&model.FileProvider{
		Hash:         hash,
		ProviderType: "local",
		ProviderPath: req.Path,
	})

	log.Info("register: %s → %s", req.Path, hash[:16])
	c.JSON(http.StatusCreated, gin.H{"hash": hash, "filename": filename, "size": size})
}
