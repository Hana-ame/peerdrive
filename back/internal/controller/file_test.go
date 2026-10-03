package controller

// Note: this file is the test for legacy code (see doc/archive/LEGACY.md, to be deleted/migrated); the "discovery background" was not annotated case by case. The "discovery background" convention applies to new code.

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

func setupFileTestRouter(t *testing.T) (*gin.Engine, string) {
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
	return r, dir
}

func TestListFiles_Empty(t *testing.T) {
	r, _ := setupFileTestRouter(t)
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
	r, _ := setupFileTestRouter(t)
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
	r, _ := setupFileTestRouter(t)
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
	r, _ := setupFileTestRouter(t)
	r.GET("/files/verify/:hash", VerifyFile)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files/verify/short", nil)
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for short hash, got %d", w.Code)
	}
}

func TestBrowseDir_DefaultRoot(t *testing.T) {
	r, _ := setupFileTestRouter(t)
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
	r, dir := setupFileTestRouter(t)
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

// TestBrowseDir_SlashMeansStorageRoot The frontend file manager uses "/" to
// denote the storage root.
// Discovery background: after the security boundary was tightened, BrowseDir
// rejects any path outside the root, but the frontend defaults to sending "/",
// which left the file manager's / creation page's local browsing perpetually
// returning 400. Fix: both an empty path and "/" map to the storage root.
func TestBrowseDir_SlashMeansStorageRoot(t *testing.T) {
	r, dir := setupFileTestRouter(t)
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.GET("/files/browse", BrowseDir)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/files/browse?path=/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(entries) == 0 {
		t.Error("storage root directory should be able to list files")
	}
}

func TestUploadFile_NoFile(t *testing.T) {
	r, _ := setupFileTestRouter(t)
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
	r, _ := setupFileTestRouter(t)
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
	r, _ := setupFileTestRouter(t)
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
