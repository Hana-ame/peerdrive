package controller

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAnonSecurityTestRouter(t *testing.T) (*gin.Engine, *service.AnonService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	_ = repository.InitDB(":memory:")
	r := gin.New()
	cfg := config.Load()
	cfg.StorageDir = t.TempDir()
	cfg.StorageEnable = true

	r.Use(func(c *gin.Context) {
		c.Set("storageDir", cfg.StorageDir)
		c.Next()
	})

	anonS := service.NewAnonService(cfg)
	InitAnonController(anonS)
	fileS := service.NewFileService(cfg)
	InitFileController(fileS)

	r.POST("/anon/collections", CreateAnonCollection)
	r.GET("/anon/collections/:hash", GetAnonCollection)
	r.GET("/anon/collections/:hash/*filepath", DownloadAnonFile)
	r.POST("/anon/collections/fork", ForkAnonCollection)
	r.POST("/anon/collections/commit", CommitAnonCollection)
	r.POST("/files/upload", UploadFile)

	return r, anonS
}

// TestAnonCollection_PasscodeNotLeaked 发现背景：受保护合集在出示正确密码解锁后，
// 返回的 JSON 绝不能把明文 passcode 回显给客户端，否则导致口令泄漏。
func TestAnonCollection_PasscodeNotLeaked(t *testing.T) {
	r, anonS := setupAnonSecurityTestRouter(t)

	entries := []model.AnonCollectionEntry{
		{Path: "doc.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonS.CreateCollectionWithPolicy("test", entries, nil, model.VisibilityPublic, nil, model.AccessPolicyProtected, "supersecret", "")
	require.NoError(t, err)

	// 1. 无口令访问：受保护状态，条目为空，且无 passcode
	req1, _ := http.NewRequest("GET", "/anon/collections/"+hash, nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)

	var resp1 map[string]any
	err = json.Unmarshal(w1.Body.Bytes(), &resp1)
	require.NoError(t, err)
	assert.Equal(t, true, resp1["is_protected"])
	assert.Empty(t, resp1["passcode"], "passcode must not be leaked in locked response")

	// 2. 带正确口令访问：已解锁，返回条目，但明文 passcode 依然必须被置空
	req2, _ := http.NewRequest("GET", "/anon/collections/"+hash+"?passcode=supersecret", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var resp2 map[string]any
	err = json.Unmarshal(w2.Body.Bytes(), &resp2)
	require.NoError(t, err)
	assert.Empty(t, resp2["passcode"], "passcode must never be leaked in unlocked response")
	ents, ok := resp2["entries"].([]any)
	assert.True(t, ok)
	assert.Len(t, ents, 1, "entries must be returned when unlocked")
}

// TestAnonCollection_ForkRequiresPasscode 发现背景：未授权客户端若可对受保护合集执行 fork，
// 并将其策略改为 public，则能直接绕过口令保护读取受限条目。
func TestAnonCollection_ForkRequiresPasscode(t *testing.T) {
	r, anonS := setupAnonSecurityTestRouter(t)

	entries := []model.AnonCollectionEntry{
		{Path: "doc.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonS.CreateCollectionWithPolicy("test", entries, nil, model.VisibilityPublic, nil, model.AccessPolicyProtected, "supersecret", "")
	require.NoError(t, err)

	// 1. 未提供 passcode fork：返回 403 Forbidden
	forkBody1, _ := json.Marshal(map[string]any{
		"source_hash": hash,
	})
	req1, _ := http.NewRequest("POST", "/anon/collections/fork", bytes.NewReader(forkBody1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusForbidden, w1.Code)

	// 2. 错误 passcode fork：返回 403 Forbidden
	forkBody2, _ := json.Marshal(map[string]any{
		"source_hash": hash,
		"passcode":    "wrongpass",
	})
	req2, _ := http.NewRequest("POST", "/anon/collections/fork", bytes.NewReader(forkBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusForbidden, w2.Code)

	// 3. 正确 passcode fork：返回 201 Created
	forkBody3, _ := json.Marshal(map[string]any{
		"source_hash": hash,
		"passcode":    "supersecret",
	})
	req3, _ := http.NewRequest("POST", "/anon/collections/fork", bytes.NewReader(forkBody3))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusCreated, w3.Code)
}

// TestAnonCollection_CommitRequiresPasscode 发现背景：未授权客户端若可对受保护合集执行 commit，
// 会导致受保护合集被随意修改或内容被覆盖。
func TestAnonCollection_CommitRequiresPasscode(t *testing.T) {
	r, anonS := setupAnonSecurityTestRouter(t)

	entries := []model.AnonCollectionEntry{
		{Path: "doc.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonS.CreateCollectionWithPolicy("test", entries, nil, model.VisibilityPublic, nil, model.AccessPolicyProtected, "supersecret", "")
	require.NoError(t, err)

	// 1. 无口令 commit 尝试：403
	body1, _ := json.Marshal(map[string]any{
		"source_hash": hash,
		"entries": []map[string]any{
			{"path": "new.txt", "hash": "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		},
	})
	req1, _ := http.NewRequest("POST", "/anon/collections/commit", bytes.NewReader(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusForbidden, w1.Code)

	// 2. 错误口令 commit 尝试：403
	body2, _ := json.Marshal(map[string]any{
		"source_hash": hash,
		"passcode":    "wrong",
		"entries": []map[string]any{
			{"path": "new.txt", "hash": "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		},
	})
	req2, _ := http.NewRequest("POST", "/anon/collections/commit", bytes.NewReader(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusForbidden, w2.Code)

	// 3. 正确口令 commit：201
	body3, _ := json.Marshal(map[string]any{
		"source_hash": hash,
		"passcode":    "supersecret",
		"entries": []map[string]any{
			{"path": "new.txt", "hash": "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
		},
	})
	req3, _ := http.NewRequest("POST", "/anon/collections/commit", bytes.NewReader(body3))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusCreated, w3.Code)
}

// TestFileUpload_FilenameSanitization 发现背景：客户端上传文件时可能传入携带目录穿越路径的文件名
// （如 ../../shell.sh 或 ..\evil.exe），必须在写入元数据与索引前归一化为 Base 文件名。
func TestFileUpload_FilenameSanitization(t *testing.T) {
	r, _ := setupAnonSecurityTestRouter(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "../../nested/shell.sh")
	require.NoError(t, err)
	_, _ = part.Write([]byte("echo hello"))
	require.NoError(t, mw.Close())

	req, _ := http.NewRequest("POST", "/files/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var resp map[string]any
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "shell.sh", resp["filename"], "filename must be stripped of traversal path segments")
}
