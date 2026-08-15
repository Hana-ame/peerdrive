package controller

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

func setupFileTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.Load()
	cfg.StorageDir = t.TempDir()
	cfg.StorageEnable = true
	dir := cfg.StorageDir
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", dir)
		c.Next()
	})
	InitFileController(service.NewFileService(cfg))
	return r
}

func TestListFiles_Empty(t *testing.T) {
	r := setupFileTestRouter(t)
	r.GET("/files", ListFiles)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var files []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestListFiles_WithSort(t *testing.T) {
	r := setupFileTestRouter(t)
	r.GET("/files", ListFiles)

	for _, sort := range []string{"time", "name", "size", "type", "path"} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/files?sort="+sort, nil)
		r.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Errorf("sort=%s: expected 200, got %d", sort, w.Code)
		}
	}
}

func TestVerifyFile_NotFound(t *testing.T) {
	r := setupFileTestRouter(t)
	r.GET("/files/verify/:hash", VerifyFile)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files/verify/0000000000000000000000000000000000000000000000000000000000000000", nil)
	r.ServeHTTP(w, req)

	// API returns 404 for not-found; that's acceptable behavior
	if w.Code != 200 && w.Code != 404 {
		t.Fatalf("expected 200 or 404, got %d: %s", w.Code, w.Body.String())
	}
	if w.Code == 200 {
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["exists"] != false {
			t.Error("nonexistent file should return exists:false")
		}
	}
}

func TestVerifyFile_InvalidHash(t *testing.T) {
	r := setupFileTestRouter(t)
	r.GET("/files/verify/:hash", VerifyFile)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files/verify/short", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for short hash, got %d", w.Code)
	}
}

func TestBrowseDir_DefaultRoot(t *testing.T) {
	r := setupFileTestRouter(t)
	r.GET("/files/browse", BrowseDir)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files/browse", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
}

func TestBrowseDir_SpecificPath(t *testing.T) {
	r := setupFileTestRouter(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	r.GET("/files/browse", BrowseDir)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files/browse?path="+dir, nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var entries []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &entries)
	if len(entries) == 0 {
		t.Error("expected at least 1 entry in temp dir")
	}
}

func TestUploadFile_NoFile(t *testing.T) {
	r := setupFileTestRouter(t)
	r.POST("/files/upload", UploadFile)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/files/upload", nil)
	req.Header.Set("Content-Type", "multipart/form-data")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCopyFile_MissingParams(t *testing.T) {
	r := setupFileTestRouter(t)
	r.POST("/files/copy", CopyFile)

	// Test missing hash (valid JSON but empty hash)
	w := httptest.NewRecorder()
	body := `{}`
	req := httptest.NewRequest("POST", "/files/copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400 for missing params, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCopyFile_InvalidHash(t *testing.T) {
	r := setupFileTestRouter(t)
	r.POST("/files/copy", CopyFile)

	w := httptest.NewRecorder()
	body := `{"hash":"invalid","dest_path":"/tmp/copy.txt"}`
	req := httptest.NewRequest("POST", "/files/copy", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400 for invalid hash, got %d: %s", w.Code, w.Body.String())
	}
}
