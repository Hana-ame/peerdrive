package controller

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	tempDir := t.TempDir()
	if realDir, err := filepath.EvalSymlinks(tempDir); err == nil {
		tempDir = realDir
	}
	cfg.StorageDir = tempDir
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
	r.PUT("/anon/collections/:hash/permissions", SetAnonCollectionPermissions)
	r.POST("/anon/collections/:hash/entries", AddAnonCollectionEntry)
	r.DELETE("/anon/collections/:hash/entries", RemoveAnonCollectionEntry)
	r.PUT("/anon/collections/:hash/entries", UpdateAnonCollectionEntry)
	r.GET("/anon/history/:hash", GetAnonCollectionHistory)
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

// 发现背景 (Issue #338)：控制器端对 PSK 保护合集的请求必须脱敏：未提供 PSK 或 PSK 错误时，
// 返回 200 但条目被置空且 is_protected=true；带正确 PSK 时解锁返回条目；且 psk_hash 绝不回显。
func TestAnonCollection_PSKControllerFlow(t *testing.T) {
	r, anonS := setupAnonSecurityTestRouter(t)

	entries := []model.AnonCollectionEntry{
		{Path: "secret.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonS.CreateCollectionWithPolicyAndPSK("psk-coll", entries, nil,
		model.VisibilityPSK, nil, model.AccessPolicyPSK, "", "mypassword123", "")
	require.NoError(t, err)

	// 1. 无 PSK 访问：条目置空，is_protected=true，psk_hash 不回显
	req1, _ := http.NewRequest("GET", "/anon/collections/"+hash, nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusOK, w1.Code)
	var resp1 map[string]any
	require.NoError(t, json.Unmarshal(w1.Body.Bytes(), &resp1))
	assert.Equal(t, true, resp1["is_protected"])
	assert.Empty(t, resp1["psk_hash"], "psk_hash must not be leaked")
	ents1, ok := resp1["entries"].([]any)
	assert.True(t, ok)
	assert.Empty(t, ents1)

	// 2. 错误 PSK 访问：依然条目置空
	req2, _ := http.NewRequest("GET", "/anon/collections/"+hash+"?psk=wrong", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	var resp2 map[string]any
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
	assert.Equal(t, true, resp2["is_protected"])
	ents2, _ := resp2["entries"].([]any)
	assert.Empty(t, ents2)

	// 3. 正确 PSK 访问：解锁返回条目
	req3, _ := http.NewRequest("GET", "/anon/collections/"+hash+"?psk=mypassword123", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code)
	var resp3 map[string]any
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &resp3))
	assert.Empty(t, resp3["psk_hash"], "psk_hash must not be leaked in unlocked response")
	ents3, ok := resp3["entries"].([]any)
	assert.True(t, ok)
	assert.Len(t, ents3, 1, "entries must be returned when PSK is verified")

	// 4. 无 PSK 下载文件：403 Forbidden
	req4, _ := http.NewRequest("GET", "/anon/collections/"+hash+"/secret.txt", nil)
	w4 := httptest.NewRecorder()
	r.ServeHTTP(w4, req4)
	assert.Equal(t, http.StatusForbidden, w4.Code)
}

// 发现背景 (Issue #338)：验证通过 HTTP API 进行条目增删改（Add / Update / Remove）时，
// 响应必须返回新版本的 hash 与指向上一个版本的 prev_sha，且 GET /anon/collections/:hash/history
// 能正确返回逆向追溯的完整版本链。
func TestAnonCollection_EntryCRUDAndHistoryController(t *testing.T) {
	r, anonS := setupAnonSecurityTestRouter(t)

	// 创建初始合集 A
	entries := []model.AnonCollectionEntry{
		{Path: "file1.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hashA, err := anonS.CreateCollectionWithPolicy("crud-test", entries, nil,
		model.VisibilityPublic, nil, model.AccessPolicyPublic, "", "")
	require.NoError(t, err)

	// 1. POST /anon/collections/:hash/entries 添加条目
	bodyAdd, _ := json.Marshal(map[string]any{
		"entry": map[string]any{
			"path": "file2.txt",
			"hash": "b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b",
		},
	})
	reqAdd, _ := http.NewRequest("POST", "/anon/collections/"+hashA+"/entries", bytes.NewReader(bodyAdd))
	reqAdd.Header.Set("Content-Type", "application/json")
	wAdd := httptest.NewRecorder()
	r.ServeHTTP(wAdd, reqAdd)
	assert.Equal(t, http.StatusCreated, wAdd.Code)
	var respAdd map[string]any
	require.NoError(t, json.Unmarshal(wAdd.Body.Bytes(), &respAdd))
	hashB := respAdd["hash"].(string)
	assert.NotEmpty(t, hashB)
	assert.Equal(t, hashA, respAdd["prev_sha"], "response prev_sha must point to hashA")

	// 2. PUT /anon/collections/:hash/entries 更新条目
	bodyUpdate, _ := json.Marshal(map[string]any{
		"entry": map[string]any{
			"path": "file2.txt",
			"hash": "c7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434c",
		},
	})
	reqUpdate, _ := http.NewRequest("PUT", "/anon/collections/"+hashB+"/entries", bytes.NewReader(bodyUpdate))
	reqUpdate.Header.Set("Content-Type", "application/json")
	wUpdate := httptest.NewRecorder()
	r.ServeHTTP(wUpdate, reqUpdate)
	assert.Equal(t, http.StatusOK, wUpdate.Code)
	var respUpdate map[string]any
	require.NoError(t, json.Unmarshal(wUpdate.Body.Bytes(), &respUpdate))
	hashC := respUpdate["hash"].(string)
	assert.NotEmpty(t, hashC)
	assert.Equal(t, hashB, respUpdate["prev_sha"], "response prev_sha must point to hashB")

	// 3. DELETE /anon/collections/:hash/entries?path=file1.txt 删除条目
	reqDel, _ := http.NewRequest("DELETE", "/anon/collections/"+hashC+"/entries?path=file1.txt", nil)
	wDel := httptest.NewRecorder()
	r.ServeHTTP(wDel, reqDel)
	assert.Equal(t, http.StatusOK, wDel.Code)
	var respDel map[string]any
	require.NoError(t, json.Unmarshal(wDel.Body.Bytes(), &respDel))
	hashD := respDel["hash"].(string)
	assert.NotEmpty(t, hashD)
	assert.Equal(t, hashC, respDel["prev_sha"], "response prev_sha must point to hashC")

	// 4. GET /anon/collections/:hash/history 获取版本历史
	reqHist, _ := http.NewRequest("GET", "/anon/collections/"+hashD+"/history", nil)
	wHist := httptest.NewRecorder()
	r.ServeHTTP(wHist, reqHist)
	assert.Equal(t, http.StatusOK, wHist.Code)
	var history []map[string]any
	require.NoError(t, json.Unmarshal(wHist.Body.Bytes(), &history))
	assert.Len(t, history, 4, "history chain should contain 4 nodes [D, C, B, A]")
	assert.Equal(t, hashD, history[0]["hash"])
	assert.Equal(t, hashC, history[0]["prev_sha"])
	assert.Equal(t, hashC, history[1]["hash"])
	assert.Equal(t, hashB, history[1]["prev_sha"])
	assert.Equal(t, hashB, history[2]["hash"])
	assert.Equal(t, hashA, history[2]["prev_sha"])
	assert.Equal(t, hashA, history[3]["hash"])
}
