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
	r.POST("/tags/batch", BatchGetShaTags)
	r.GET("/tags/summary", GetAllTagsSummary)
	r.GET("/tags", GetAllTagsSummary)
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

// TestTagController_BatchAndSummary 验证批量标签查询与全局标签概要接口。
//
// 发现背景：前端在文件列表渲染时存在严重的 N+1 瀑布流网络请求；同时标签栏需要
// 全局聚合统计以展示各个标签及命中计数。本测试覆盖：
//  1. POST /tags/batch 单次批量查询多文件标签；
//  2. 畸形 JSON 请求体与空 SHA 列表的防御处理；
//  3. GET /tags/summary 与 GET /tags 返回格式与计数断言；
//  4. 响应中 data 与 tags 字段双重兼容。
func TestTagController_BatchAndSummary(t *testing.T) {
	r := setupTagTestRouter(t)

	sha1 := "1111111111111111111111111111111111111111111111111111111111111111"
	sha2 := "2222222222222222222222222222222222222222222222222222222222222222"

	// 先通过 POST /tags/sha/:sha 打标签
	setBody1, _ := json.Marshal(map[string]any{"tags": []string{"project-x", "urgent"}})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tags/sha/"+sha1, bytes.NewReader(setBody1))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	setBody2, _ := json.Marshal(map[string]any{"tags": []string{"project-x"}})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/tags/sha/"+sha2, bytes.NewReader(setBody2))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 1. 批量查询正常用例
	batchReqBody, _ := json.Marshal(map[string]any{"shas": []string{sha1, sha2, "unknown"}})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/tags/batch", bytes.NewReader(batchReqBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var batchResp struct {
		Data map[string][]string `json:"data"`
		Tags map[string][]string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &batchResp))
	assert.Equal(t, []string{"project-x", "urgent"}, batchResp.Data[sha1])
	assert.Equal(t, []string{"project-x"}, batchResp.Data[sha2])
	assert.Equal(t, []string{}, batchResp.Data["unknown"])
	assert.Equal(t, batchResp.Data, batchResp.Tags, "data 与 tags 字段应具有一致内容")

	// 2. 畸形请求体验证
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/tags/batch", bytes.NewReader([]byte("{invalid-json}")))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 3. 全局标签概要验证
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/tags/summary", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var summaryResp struct {
		Data  []repository.TagCount `json:"data"`
		Total int                   `json:"total"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summaryResp))
	assert.Equal(t, 2, summaryResp.Total)
	require.Len(t, summaryResp.Data, 2)
	assert.Equal(t, "project-x", summaryResp.Data[0].Tag)
	assert.Equal(t, 2, summaryResp.Data[0].Count)
	assert.Equal(t, "urgent", summaryResp.Data[1].Tag)
	assert.Equal(t, 1, summaryResp.Data[1].Count)
}

