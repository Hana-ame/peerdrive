package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenListCrawler_CrawlAndReload verifies end-to-end crawling and atomic indexing.
//
// 发现背景：Issue #106（PR ②）。
// 验证爬虫走 /api/fs/list 递归发现远程文件，通过 /p/*path 计算真实 sha256，
// 并在构建完成后一次性原子调用 Reload() 刷新 OpenListSource。
func TestOpenListCrawler_CrawlAndReload(t *testing.T) {
	fileAContent := []byte("hello openlist file A")
	fileBContent := []byte("hello openlist subfolder file B")

	hashA := sha256.Sum256(fileAContent)
	hashAHex := hex.EncodeToString(hashA[:])

	hashB := sha256.Sum256(fileBContent)
	hashBHex := hex.EncodeToString(hashB[:])

	// Mock OpenList HTTP Server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/fs/list":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			dirPath, _ := req["path"].(string)

			w.Header().Set("Content-Type", "application/json")
			if dirPath == "/" || dirPath == "" {
				_ = json.NewEncoder(w).Encode(OpenListFSListResp{
					Code:    200,
					Message: "success",
					Data: struct {
						Content []OpenListFSItem `json:"content"`
						Total   int              `json:"total"`
						Readme  string           `json:"readme"`
					}{
						Content: []OpenListFSItem{
							{Name: "file_a.txt", IsDir: false, Size: int64(len(fileAContent))},
							{Name: "sub", IsDir: true},
						},
						Total: 2,
					},
				})
				return
			} else if dirPath == "/sub" {
				_ = json.NewEncoder(w).Encode(OpenListFSListResp{
					Code:    200,
					Message: "success",
					Data: struct {
						Content []OpenListFSItem `json:"content"`
						Total   int              `json:"total"`
						Readme  string           `json:"readme"`
					}{
						Content: []OpenListFSItem{
							{Name: "file_b.txt", IsDir: false, Size: int64(len(fileBContent))},
						},
						Total: 1,
					},
				})
				return
			}
			http.NotFound(w, r)

		case r.URL.Path == "/p/file_a.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fileAContent)

		case r.URL.Path == "/p/sub/file_b.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(fileBContent)

		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	// 1. 初始化一个空 OpenListSource (Available == false)
	src, err := NewOpenListSource(OpenListConfig{
		BaseURL: ts.URL,
		Verify:  true,
	})
	require.NoError(t, err)
	ctx := context.Background()
	assert.False(t, src.Available(ctx), "source without index must not be available")

	// 2. 初始化爬虫并执行 CrawlAndReload
	crawler, err := NewOpenListCrawler(OpenListCrawlerConfig{
		BaseURL:     ts.URL,
		Concurrency: 2,
		Timeout:     5 * time.Second,
	})
	require.NoError(t, err)

	err = crawler.CrawlAndReload(ctx, "/", src)
	require.NoError(t, err)

	// 3. 验证 Source 状态更新
	assert.True(t, src.Available(ctx), "source must be available after reload")
	assert.Equal(t, 2, src.Count(), "source must index exactly 2 files")

	// 4. 验证通过 hash 命中文件
	pathA, okA := src.lookup(hashAHex)
	assert.True(t, okA)
	assert.Equal(t, "/file_a.txt", pathA)

	pathB, okB := src.lookup(hashBHex)
	assert.True(t, okB)
	assert.Equal(t, "/sub/file_b.txt", pathB)
}
