package controller

// HTTP 层穿透：这一层独有的攻击面是**解码**。
// gin 的 c.Query / JSON 绑定会把 %2e%2e%2f 还原成 ../，把 \u002e 还原成 .，
// 之后才交给 service 判定。所以"服务层挡得住"不等于"HTTP 层挡得住"——
// 历史上多起绕过都是解码发生在校验之后。这里把编码形态的 payload 也打一遍。

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

// setupTraversalRouter storage 根 + 同级的诱饵目录 outside。
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

// encodedPayloads 编码形态的穿透 payload。都相对 storage 的最终父级构造，
// 目标是摸到同级的 outside/secret.txt。
func encodedPayloads(outside string) []struct {
	name string
	path string
} {
	return []struct {
		name string
		path string
	}{
		{"全编码点点", "%2e%2e%2f%2e%2e%2foutside%2fsecret.txt"},
		{"大小写混合编码", "%2E%2E/%2E%2E/outside/secret.txt"},
		{"只编码斜杠", "..%2f..%2foutside%2fsecret.txt"},
		{"只编码点", "%2e%2e/%2e%2e/outside/secret.txt"},
		{"双重编码", "%252e%252e%252f%252e%252e%252foutside"},
		{"点点双斜杠", "....//....//outside/secret.txt"},
		{"点点反斜杠", `..\..\outside\secret.txt`},
		{"编码反斜杠", "..%5c..%5coutside%5csecret.txt"},
		{"Unicode 点号", "..\u002f..\u002foutside"},
		{"分号夹带", "..;/..;/outside/secret.txt"},
		{"尾部点号目录", "outside/secret.txt/."},
		{"绝对路径", outside + "/secret.txt"},
		{"绝对路径系统", systemAbsolutePath()},
	}
}

// systemAbsolutePath 一个"系统里确实存在、且绝不在 storage 根下"的绝对路径。
//
// 不能写死 "/etc/passwd"：Windows 上它没有卷名，**不是**绝对路径，会被当成
// 相对路径拼到 storage 底下，于是路径判定放行、失败原因变成"文件不存在"——
// 断言就假绿了（2026-09-20 真机 Windows 跑出来的）。
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
				"browse 不许返回 200：path=%q body=%s", c.path, w.Body.String())
		})
	}

	// 正常用法不能被误伤
	for _, good := range []string{"", "/", ".", "sub", "./sub", "sub/."} {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/files/browse?path="+url.QueryEscape(good), nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "storage 根内应能浏览：path=%q", good)
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
				"register_local 不许返回 200：path=%q body=%s", c.path, w.Body.String())
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
				"register_folder 不许返回 200：path=%q body=%s", c.path, w.Body.String())
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
				"copy 不许返回 200：dest=%q body=%s", c.path, w.Body.String())
			// 错误必须是"路径越权"，而不是"源不存在"之类把它糊过去的说法
			if c.name == "绝对路径" || c.name == "绝对路径系统" {
				assert.Contains(t, w.Body.String(), "outside",
					"越权应由路径判定拦下，而不是别的原因：%s", w.Body.String())
			}
		})
	}
}

// TestTraversal_HTTP_Symlink 软链在 HTTP 层同样不能绕过。
func TestTraversal_HTTP_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	r, storage, outside := setupTraversalRouter(t)

	link := filepath.Join(storage, "link.txt")
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), link))

	body, _ := json.Marshal(map[string]string{"path": link, "filename": "link.txt"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/files/register_local", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code, "指向外部的软链不得登记：%s", w.Body.String())

	// 响应体不能把外侧的绝对路径吐回去
	assert.False(t, strings.Contains(w.Body.String(), outside),
		"错误响应不应泄露外部绝对路径：%s", w.Body.String())
}
