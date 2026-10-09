package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"peerdrive/internal/repository"
)

func setupTagTestRouter(t *testing.T) *gin.Engine {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "tag_ctrl_test.db")
	require.NoError(t, repository.InitDB(dbPath))
	t.Cleanup(func() {
		_ = repository.CloseDB()
	})

	r := gin.New()
	r.GET("/tags/sha/:sha", GetShaTags)
	r.POST("/tags/sha/:sha", SetShaTags)
	r.GET("/tags/search", SearchTags)
	r.GET("/collections/search", SearchCollections)
	return r
}

func TestTagController_Endpoints(t *testing.T) {
	r := setupTagTestRouter(t)
	sha := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"

	// 1. Initial GET
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/tags/sha/"+sha, nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Data)

	// 2. Set tags via POST
	body, _ := json.Marshal(map[string]interface{}{
		"tags": []string{"work", "confidential"},
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/tags/sha/"+sha, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 3. GET verify
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/tags/sha/"+sha, nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, []string{"confidential", "work"}, resp.Data)

	// 4. Search tags
	_, err := repository.UpsertFileIndex(sha, "/storage/secret.doc", "secret.doc", 512, false)
	require.NoError(t, err)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/tags/search?tag=work", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var searchResp struct {
		Data []repository.FileIndex `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &searchResp))
	assert.Len(t, searchResp.Data, 1)
	assert.Equal(t, "secret.doc", searchResp.Data[0].Name)
}

func TestCollectionSearch_WithTags(t *testing.T) {
	r := setupTagTestRouter(t)

	_, err := repository.CreateCollectionWithTags("admin", "cool_album", "public", []string{"photo", "trip"})
	require.NoError(t, err)

	_, err = repository.CreateCollectionWithTags("admin", "invoices", "public", []string{"finance", "pdf"})
	require.NoError(t, err)

	// Search with tag
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/collections/search?tag=photo", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var res struct {
		Data []map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.Equal(t, "cool_album", res.Data[0]["collection_name"])
}
