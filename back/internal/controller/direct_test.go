// direct_test.go — DirectFileBySHA256 / DirectCollectionByHash unit tests
// (invariant #2 self-contained content URLs, Issue #410).
//
// 发现背景：/f/ /c/ 后端 handler 在 v1.0-rc 修复时被切除（d2d2830，zip 源快照没写
// 完），补回时按「按测试钉死的契约（peerdrive-core/src/url.js + core.test.mjs
// Module 2）+ 合理最小实现」重写。本测试钉住 handler 的最小行为：
//   - /f/ 按 sha256 直接吐内容（与 /sha256sum/ 共用 downloadLocalByHash），
//     ?meta=1 吐 JSON 元数据，HEAD/Range 走 http.ServeContent
//   - /c/ 无 filepath 回合集 manifest JSON（复用 GetAnonCollection 的可见性门禁），
//     带 filepath 走条目下载（URL provider → 302，复用 DownloadAnonFile）
//   - 越权/格式错误照既有惯例：非法 hash → 400(/f/) / 404(/c/)，私有合集对未登录
//     请求者不可见 → 404

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// setupDirectRouter builds a bare gin engine with exactly the six canonical
// /f/ /c/ routes and real file/anon services over an in-memory DB (same pattern
// as setupRedTeamRouter in redteam_security_test.go).
func setupDirectRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, repository.InitDB(":memory:"))

	cfg := config.Load()
	cfg.StorageDir = t.TempDir()
	cfg.StorageEnable = true
	InitFileController(service.NewFileService(cfg))
	InitAnonController(service.NewAnonService(cfg))

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", cfg.StorageDir)
		c.Next()
	})
	r.POST("/files/upload", UploadFile)
	r.GET("/f/:sha256", DirectFileBySHA256)
	r.HEAD("/f/:sha256", DirectFileBySHA256)
	r.GET("/f/:sha256/:filename", DirectFileBySHA256)
	r.HEAD("/f/:sha256/:filename", DirectFileBySHA256)
	r.GET("/c/:hash", DirectCollectionByHash)
	r.GET("/c/:hash/*filepath", DirectCollectionByHash)
	return r, cfg.StorageDir
}

// writeAnonCollection writes a content-addressed collection JSON to
// <storageDir>/<hash[:2]>/<hash> exactly like AnonService.saveCollectionJSON
// does, and returns its sha256 identity.
func writeAnonCollection(t *testing.T, storageDir string, coll model.AnonCollection) string {
	t.Helper()
	b, err := json.Marshal(coll)
	require.NoError(t, err)
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	dir := filepath.Join(storageDir, h[:2])
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, h), b, 0o644))
	return h
}

// ── /f/:sha256 ────────────────────────────────────────────────────────────────

func TestDirectFileBySHA256_ServesContent(t *testing.T) {
	r, _ := setupDirectRouter(t)
	hash := uploadMultipart(t, r, "note.txt", "hello direct url")

	// plain GET — same serving path as /sha256sum/
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/f/"+hash, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hello direct url", w.Body.String())

	// ?inline=1 + canonical /f/<hash>/<filename> form — the URL filename names
	// the response, and .txt is on the inline preview whitelist
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/f/"+hash+"/note.txt?inline=1", nil))
	require.Equal(t, http.StatusOK, w2.Code)
	require.Equal(t, "hello direct url", w2.Body.String())
	require.Equal(t, "text/plain; charset=utf-8", w2.Header().Get("Content-Type"))

	// Range request (RFC 7233) still works through http.ServeContent
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/f/"+hash, nil)
	req3.Header.Set("Range", "bytes=0-4")
	r.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusPartialContent, w3.Code)
	require.Equal(t, "hello", w3.Body.String())
}

func TestDirectFileBySHA256_Meta(t *testing.T) {
	r, _ := setupDirectRouter(t)
	hash := uploadMultipart(t, r, "note.txt", "hello direct url")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/f/"+hash+"?meta=1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var meta struct {
		Hash        string `json:"hash"`
		Size        int64  `json:"size"`
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &meta))
	require.Equal(t, hash, meta.Hash)
	require.Equal(t, int64(len("hello direct url")), meta.Size)
	require.Equal(t, "note.txt", meta.Filename)
	require.NotEmpty(t, meta.ContentType)
}

func TestDirectFileBySHA256_Head(t *testing.T) {
	r, _ := setupDirectRouter(t)
	hash := uploadMultipart(t, r, "note.txt", "hello direct url")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("HEAD", "/f/"+hash, nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Body.String())
}

func TestDirectFileBySHA256_BadHashAndMissing(t *testing.T) {
	r, _ := setupDirectRouter(t)

	// non-64-hex hash → 400 (same as /sha256sum/)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/f/deadbeef", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)

	// well-formed but unknown hash → 404
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/f/"+repeatHex("ab", 64), nil))
	require.Equal(t, http.StatusNotFound, w2.Code)
}

// ── /c/:hash ─────────────────────────────────────────────────────────────────

func TestDirectCollectionByHash_Manifest(t *testing.T) {
	r, storageDir := setupDirectRouter(t)
	coll := model.AnonCollection{
		Version:      1,
		FriendlyName: "Photos 2026",
		CreatedAt:    "2026-10-17",
		Visibility:   model.VisibilityPublic,
		Entries: []model.AnonCollectionEntry{
			{Path: "sub/a.jpg", Providers: []model.Provider{{Type: "url", Value: "https://example.com/a.jpg"}}},
		},
	}
	h := writeAnonCollection(t, storageDir, coll)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/c/"+h, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var got model.AnonCollection
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "Photos 2026", got.FriendlyName)
	require.Len(t, got.Entries, 1)
	require.Equal(t, "sub/a.jpg", got.Entries[0].Path)

	// trailing slash resolves to the manifest too
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/c/"+h+"/", nil))
	require.Equal(t, http.StatusOK, w2.Code)
}

func TestDirectCollectionByHash_EntryRedirect(t *testing.T) {
	r, storageDir := setupDirectRouter(t)
	coll := model.AnonCollection{
		Version:    1,
		Visibility: model.VisibilityPublic,
		Entries: []model.AnonCollectionEntry{
			{Path: "sub/a.jpg", Providers: []model.Provider{{Type: "url", Value: "https://example.com/a.jpg"}}},
		},
	}
	h := writeAnonCollection(t, storageDir, coll)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/c/"+h+"/sub/a.jpg", nil))
	require.Equal(t, http.StatusFound, w.Code)
	require.Equal(t, "https://example.com/a.jpg", w.Header().Get("Location"))

	// unknown entry path → 404
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/c/"+h+"/nope.png", nil))
	require.Equal(t, http.StatusNotFound, w2.Code)
}

func TestDirectCollectionByHash_PrivateDenied(t *testing.T) {
	r, storageDir := setupDirectRouter(t)
	coll := model.AnonCollection{
		Version:    1,
		Visibility: model.VisibilityPrivate,
		Owner:      "alice",
	}
	h := writeAnonCollection(t, storageDir, coll)

	// anonymous requester (GetOperator() == "") cannot view a private collection
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/c/"+h, nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestDirectCollectionByHash_BadAndMissing(t *testing.T) {
	r, _ := setupDirectRouter(t)

	// invalid hash → not found (anon service rejects before any disk access)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/c/deadbeef", nil))
	require.Equal(t, http.StatusNotFound, w.Code)

	// well-formed but unknown hash → 404
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest("GET", "/c/"+repeatHex("cd", 64), nil))
	require.Equal(t, http.StatusNotFound, w2.Code)
}

// repeatHex returns a 64-char hex string of the given two-hex byte repeated.
func repeatHex(byteHex string, n int) string {
	s := ""
	for i := 0; i < n/2; i++ {
		s += byteHex
	}
	return s
}
