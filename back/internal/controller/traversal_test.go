package controller

// HTTP-layer traversal: the attack surface unique to this layer is **decoding**.
// gin's c.Query / JSON binding turns %2e%2e%2f back into ../, and \u002e back into .,
// and only then hands it off to the service for judgment. So "the service layer blocks it"
// does not equal "the HTTP layer blocks it" -- many historical bypasses happened because
// decoding occurred after validation. Here we also fire off the encoded-form payloads.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTraversalRouter storage root + a sibling decoy directory outside.
func setupTraversalRouter(t *testing.T) (*gin.Engine, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, repository.InitDB(":memory:"))

	base := t.TempDir()
	storage := filepath.Join(base, "storage")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(storage, "sub"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("top secret"), 0o600))

	cfg := config.Load()
	cfg.StorageDir = storage
	cfg.StorageEnable = true
	InitFileController(service.NewFileService(cfg))

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", storage)
		c.Next()
	})
	r.GET("/files/browse", BrowseDir)
	r.POST("/files/register_local", RegisterLocalFile)
	r.POST("/files/register_folder", RegisterFolder)
	r.POST("/files/copy", CopyFile)
	return r, storage, outside
}

// encodedPayloads encoded-form traversal payloads. All are built relative to the
// storage root's ultimate parent, with the goal of reaching the sibling
// outside/secret.txt.
func encodedPayloads(outside string) []struct {
	name string
	path string
} {
	return []struct {
		name string
		path string
	}{
		{"fully encoded dots", "%2e%2e%2f%2e%2e%2foutside%2fsecret.txt"},
		{"mixed case encoding", "%2E%2E/%2E%2E/outside/secret.txt"},
		{"only encoding slashes", "..%2f..%2foutside%2fsecret.txt"},
		{"only encoding dots", "%2e%2e/%2e%2e/outside/secret.txt"},
		{"double encoding", "%252e%252e%252f%252e%252e%252foutside"},
		{"dot-dot double slash", "....//....//outside/secret.txt"},
		{"dot-dot backslash", `..\..\outside\secret.txt`},
		{"encoded backslash", "..%5c..%5coutside%5csecret.txt"},
		{"Unicode dot", "..\u002f..\u002foutside"},
		{"semicolon smuggling", "..;/..;/outside/secret.txt"},
		{"trailing dot directory", "outside/secret.txt/."},
		{"absolute path", outside + "/secret.txt"},
		{"absolute system path", systemAbsolutePath()},
	}
}

// systemAbsolutePath an absolute path that "genuinely exists on the system and
// is never under the storage root".
//
// Can't hardcode "/etc/passwd": on Windows it has no volume name and is **not**
// an absolute path, so it gets treated as a relative path appended under
// storage, meaning the path check passes and the failure reason becomes "file
// not found" -- the assertion would go falsely green (observed on a real
// Windows machine on 2026-09-20).
func systemAbsolutePath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/passwd"
}

func TestTraversal_HTTP_BrowseEncoded(t *testing.T) {
	r, _, outside := setupTraversalRouter(t)

	for _, c := range encodedPayloads(outside) {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/files/browse?path="+url.QueryEscape(c.path), nil)
			r.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusOK, w.Code,
				"browse must not return 200: path=%q body=%s", c.path, w.Body.String())
		})
	}

	// normal usage must not be accidentally blocked
	for _, good := range []string{"", "/", ".", "sub", "./sub", "sub/."} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/files/browse?path="+url.QueryEscape(good), nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "should be able to browse inside storage root: path=%q", good)
	}
}

func TestTraversal_HTTP_RegisterLocalEncoded(t *testing.T) {
	r, _, outside := setupTraversalRouter(t)

	for _, c := range encodedPayloads(outside) {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"path": c.path, "filename": "x.txt"})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/files/register_local", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusOK, w.Code,
				"register_local must not return 200: path=%q body=%s", c.path, w.Body.String())
		})
	}
}

func TestTraversal_HTTP_RegisterFolderEncoded(t *testing.T) {
	r, _, outside := setupTraversalRouter(t)

	for _, c := range encodedPayloads(outside) {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"folder_path": c.path})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/files/register_folder", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusOK, w.Code,
				"register_folder must not return 200: path=%q body=%s", c.path, w.Body.String())
		})
	}
}

func TestTraversal_HTTP_CopyEncoded(t *testing.T) {
	r, _, outside := setupTraversalRouter(t)
	const dummyHash = "0000000000000000000000000000000000000000000000000000000000000000"

	for _, c := range encodedPayloads(outside) {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"hash": dummyHash, "dest_path": c.path})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/files/copy", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.NotEqual(t, http.StatusOK, w.Code,
				"copy must not return 200: dest=%q body=%s", c.path, w.Body.String())
			// the error must be "path is out of bounds", not something like "source doesn't exist" that papers over it
			if c.name == "absolute path" || c.name == "absolute system path" {
				assert.Contains(t, w.Body.String(), "outside",
					"out-of-bounds should be blocked by path judgment, not some other reason: %s", w.Body.String())
			}
		})
	}
}

// TestTraversal_HTTP_Symlink Symlinks cannot be bypassed at the HTTP layer either.
func TestTraversal_HTTP_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows requires developer mode/admin, skipping")
	}
	r, storage, outside := setupTraversalRouter(t)

	link := filepath.Join(storage, "link.txt")
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), link))

	body, _ := json.Marshal(map[string]string{"path": link, "filename": "link.txt"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/files/register_local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code, "symlink pointing outside must not be registered: %s", w.Body.String())

	// the response body must not echo the outer absolute path
	assert.False(t, strings.Contains(w.Body.String(), outside),
		"error response should not leak external absolute path: %s", w.Body.String())
}
