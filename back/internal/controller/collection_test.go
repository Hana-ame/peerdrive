package controller

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"peerdrive/internal/repository"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func setupCollectionTest(t *testing.T) (string, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repository.InitDB(":memory:")

	tmpDir, err := os.MkdirTemp("", "peerdrive_coll_test")
	assert.NoError(t, err)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}
	return tmpDir, cleanup
}

func newTestContext(w *httptest.ResponseRecorder) *gin.Context {
	c, _ := gin.CreateTestContext(w)
	return c
}

func setJSONBody(c *gin.Context, method string, body string) {
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
}

func TestCreateCollection_Valid(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"testuser","collection_name":"my-coll"}`)

	CreateCollection(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.NotZero(t, resp["id"])
	assert.Equal(t, "testuser", resp["username"])
	assert.Equal(t, "my-coll", resp["collection_name"])
}

func TestCreateCollection_Duplicate(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"dupuser","collection_name":"dup-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	setJSONBody(c2, http.MethodPost, `{"username":"dupuser","collection_name":"dup-coll"}`)
	CreateCollection(c2)
	assert.Equal(t, http.StatusConflict, w2.Code)
}

func TestListCollections(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"listuser","collection_name":"coll-a"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	setJSONBody(c2, http.MethodPost, `{"username":"listuser","collection_name":"coll-b"}`)
	CreateCollection(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Params = gin.Params{{Key: "username", Value: "listuser"}}
	ListCollections(c3)

	assert.Equal(t, http.StatusOK, w3.Code)

	var resp map[string]json.RawMessage
	err := json.Unmarshal(w3.Body.Bytes(), &resp)
	assert.NoError(t, err)

	var cols []map[string]interface{}
	err = json.Unmarshal(resp["data"], &cols)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(cols), 2)
}

func TestGetCollection(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"getuser","collection_name":"get-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "getuser"},
		{Key: "collection_name", Value: "get-coll"},
	}
	GetCollection(c2)

	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]json.RawMessage
	err := json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.NoError(t, err)

	var col map[string]interface{}
	err = json.Unmarshal(resp["collection"], &col)
	assert.NoError(t, err)
	assert.Equal(t, "getuser", col["username"])
	assert.Equal(t, "get-coll", col["collection_name"])
}

func TestGetCollection_NotFound(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	c.Params = gin.Params{
		{Key: "username", Value: "nouser"},
		{Key: "collection_name", Value: "no-coll"},
	}
	GetCollection(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAddEntry(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"entryuser","collection_name":"entry-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "entryuser"},
		{Key: "collection_name", Value: "entry-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"path":"docs/readme.txt","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c2)

	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "entry added", resp["message"])
}

func TestRemoveEntry(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"rmuser","collection_name":"rm-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "rmuser"},
		{Key: "collection_name", Value: "rm-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"path":"temp.txt","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Set("storageDir", tmpDir)
	c3.Params = gin.Params{
		{Key: "username", Value: "rmuser"},
		{Key: "collection_name", Value: "rm-coll"},
		{Key: "path", Value: "temp.txt"},
	}
	RemoveEntry(c3)

	assert.Equal(t, http.StatusOK, w3.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w3.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "entry removed", resp["message"])

	w4 := httptest.NewRecorder()
	c4 := newTestContext(w4)
	c4.Set("storageDir", tmpDir)
	c4.Params = gin.Params{
		{Key: "username", Value: "rmuser"},
		{Key: "collection_name", Value: "rm-coll"},
	}
	GetCollection(c4)
	assert.Equal(t, http.StatusOK, w4.Code)

	var getResp map[string]json.RawMessage
	err = json.Unmarshal(w4.Body.Bytes(), &getResp)
	assert.NoError(t, err)

	var entries []map[string]interface{}
	err = json.Unmarshal(getResp["entries"], &entries)
	assert.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestRemoveEntry_CollectionNotFound(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	c.Params = gin.Params{
		{Key: "username", Value: "nouser"},
		{Key: "collection_name", Value: "no-coll"},
		{Key: "path", Value: "file.txt"},
	}
	RemoveEntry(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCommitCollection(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"commituser","collection_name":"commit-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "commituser"},
		{Key: "collection_name", Value: "commit-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"path":"main.go","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Set("storageDir", tmpDir)
	c3.Params = gin.Params{
		{Key: "username", Value: "commituser"},
		{Key: "collection_name", Value: "commit-coll"},
	}
	setJSONBody(c3, http.MethodPost, `{"commit_message":"initial commit"}`)
	CommitCollection(c3)

	assert.Equal(t, http.StatusOK, w3.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w3.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "committed", resp["message"])
	assert.NotZero(t, resp["version_number"])
	assert.NotEmpty(t, resp["snapshot_hash"])
}

func TestGetVersionLog(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"veruser","collection_name":"ver-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "veruser"},
		{Key: "collection_name", Value: "ver-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"path":"x.go","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Set("storageDir", tmpDir)
	c3.Params = gin.Params{
		{Key: "username", Value: "veruser"},
		{Key: "collection_name", Value: "ver-coll"},
	}
	setJSONBody(c3, http.MethodPost, `{"commit_message":"v1"}`)
	CommitCollection(c3)
	assert.Equal(t, http.StatusOK, w3.Code)

	w4 := httptest.NewRecorder()
	c4 := newTestContext(w4)
	c4.Params = gin.Params{
		{Key: "username", Value: "veruser"},
		{Key: "collection_name", Value: "ver-coll"},
	}
	GetVersionLog(c4)

	assert.Equal(t, http.StatusOK, w4.Code)

	var resp map[string]json.RawMessage
	err := json.Unmarshal(w4.Body.Bytes(), &resp)
	assert.NoError(t, err)

	var versions []map[string]interface{}
	err = json.Unmarshal(resp["data"], &versions)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(versions), 1)
	assert.Equal(t, "v1", versions[0]["commit_message"])
}

func TestCreateCollection_InvalidBody(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"invalid`)

	CreateCollection(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchCollections(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"searchuser","collection_name":"search-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/?q=search-coll", nil)
	SearchCollections(c2)

	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]json.RawMessage
	err := json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.NoError(t, err)

	var cols []map[string]interface{}
	err = json.Unmarshal(resp["data"], &cols)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(cols), 1)
}

func TestForkCollection(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"srcuser","collection_name":"src-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "srcuser"},
		{Key: "collection_name", Value: "src-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"path":"forked.txt","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Set("storageDir", tmpDir)
	setJSONBody(c3, http.MethodPost, `{"username":"dstuser","collection_name":"dst-coll","source_username":"srcuser","source_coll_name":"src-coll"}`)
	ForkCollection(c3)

	assert.Equal(t, http.StatusOK, w3.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w3.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "forked", resp["message"])
	assert.Equal(t, "dstuser", resp["username"])
	assert.Equal(t, "dst-coll", resp["collection_name"])
	assert.Equal(t, float64(1), resp["entries_count"])
}

func TestForkCollection_SourceNotFound(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"dstuser","collection_name":"dst-coll","source_username":"nobody","source_coll_name":"nowhere"}`)
	ForkCollection(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAddEntry_CreatesCollectionIfNotExists(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	c.Params = gin.Params{
		{Key: "username", Value: "autocreate"},
		{Key: "collection_name", Value: "auto-coll"},
	}
	setJSONBody(c, http.MethodPost, `{"path":"hello.txt","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "entry added", resp["message"])
}

func TestRollbackCollection(t *testing.T) {
	tmpDir, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Set("storageDir", tmpDir)
	setJSONBody(c, http.MethodPost, `{"username":"rbuser","collection_name":"rb-coll"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Set("storageDir", tmpDir)
	c2.Params = gin.Params{
		{Key: "username", Value: "rbuser"},
		{Key: "collection_name", Value: "rb-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"path":"v1.txt","hash":"a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"}`)
	AddEntry(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Set("storageDir", tmpDir)
	c3.Params = gin.Params{
		{Key: "username", Value: "rbuser"},
		{Key: "collection_name", Value: "rb-coll"},
	}
	setJSONBody(c3, http.MethodPost, `{"commit_message":"v1 commit"}`)
	CommitCollection(c3)
	assert.Equal(t, http.StatusOK, w3.Code)

	var commitResp map[string]interface{}
	err := json.Unmarshal(w3.Body.Bytes(), &commitResp)
	assert.NoError(t, err)

	w4 := httptest.NewRecorder()
	c4 := newTestContext(w4)
	c4.Set("storageDir", tmpDir)
	c4.Params = gin.Params{
		{Key: "username", Value: "rbuser"},
		{Key: "collection_name", Value: "rb-coll"},
	}
	setJSONBody(c4, http.MethodPost, `{"path":"v2.txt","hash":"b7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434b"}`)
	AddEntry(c4)
	assert.Equal(t, http.StatusOK, w4.Code)

	w5 := httptest.NewRecorder()
	c5 := newTestContext(w5)
	c5.Set("storageDir", tmpDir)
	c5.Params = gin.Params{
		{Key: "username", Value: "rbuser"},
		{Key: "collection_name", Value: "rb-coll"},
	}
	setJSONBody(c5, http.MethodPost, `{"commit_message":"v2 commit"}`)
	CommitCollection(c5)
	assert.Equal(t, http.StatusOK, w5.Code)

	w6 := httptest.NewRecorder()
	c6 := newTestContext(w6)
	c6.Set("storageDir", tmpDir)
	c6.Params = gin.Params{
		{Key: "username", Value: "rbuser"},
		{Key: "collection_name", Value: "rb-coll"},
		{Key: "version_id", Value: "1"},
	}
	RollbackCollection(c6)

	assert.Equal(t, http.StatusOK, w6.Code)

	var rbResp map[string]interface{}
	err = json.Unmarshal(w6.Body.Bytes(), &rbResp)
	assert.NoError(t, err)
	assert.Equal(t, "rolled back", rbResp["message"])
}

func getRespJSON(w *httptest.ResponseRecorder) map[string]interface{} {
	var resp map[string]interface{}
	body, _ := io.ReadAll(w.Result().Body)
	json.Unmarshal(body, &resp)
	return resp
}

func TestCreateCollectionWithVisibility(t *testing.T) {
	_, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	setJSONBody(c, http.MethodPost, `{"username":"visuser","collection_name":"public-coll","visibility":"public"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "public", resp["visibility"])
}

func TestCreateCollectionWithPrivateVisibility(t *testing.T) {
	_, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	setJSONBody(c, http.MethodPost, `{"username":"visuser2","collection_name":"private-coll","visibility":"private"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "private", resp["visibility"])
}

func TestSetCollectionVisibility(t *testing.T) {
	_, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	setJSONBody(c, http.MethodPost, `{"username":"svuser","collection_name":"sv-coll","visibility":"public"}`)
	CreateCollection(c)
	assert.Equal(t, http.StatusOK, w.Code)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	c2.Params = gin.Params{
		{Key: "username", Value: "svuser"},
		{Key: "collection_name", Value: "sv-coll"},
	}
	setJSONBody(c2, http.MethodPost, `{"visibility":"private"}`)
	SetCollectionVisibility(c2)
	assert.Equal(t, http.StatusOK, w2.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w2.Body.Bytes(), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp["message"])

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	c3.Params = gin.Params{
		{Key: "username", Value: "svuser"},
		{Key: "collection_name", Value: "sv-coll"},
	}
	GetCollection(c3)
	assert.Equal(t, http.StatusOK, w3.Code)
	var collResp map[string]interface{}
	json.Unmarshal(w3.Body.Bytes(), &collResp)
	col, ok := collResp["collection"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "private", col["visibility"])
}

func TestListPublicCollections(t *testing.T) {
	_, cleanup := setupCollectionTest(t)
	defer cleanup()

	w1 := httptest.NewRecorder()
	c1 := newTestContext(w1)
	setJSONBody(c1, http.MethodPost, `{"username":"lpuser","collection_name":"pub-1","visibility":"public"}`)
	CreateCollection(c1)

	w2 := httptest.NewRecorder()
	c2 := newTestContext(w2)
	setJSONBody(c2, http.MethodPost, `{"username":"lpuser","collection_name":"priv-1","visibility":"private"}`)
	CreateCollection(c2)

	w3 := httptest.NewRecorder()
	c3 := newTestContext(w3)
	setJSONBody(c3, http.MethodPost, `{"username":"lpuser2","collection_name":"ul-1","visibility":"unlisted"}`)
	CreateCollection(c3)

	w4 := httptest.NewRecorder()
	c4 := newTestContext(w4)
	ListPublicCollections(c4)
	assert.Equal(t, http.StatusOK, w4.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w4.Body.Bytes(), &resp)
	assert.NoError(t, err)
	data, ok := resp["data"].([]interface{})
	assert.True(t, ok)
	assert.Equal(t, 1, len(data), "only public collections should appear")
}

func TestListCollectionsForUserReturnsAllVisibilities(t *testing.T) {
	_, cleanup := setupCollectionTest(t)
	defer cleanup()

	CreateCollection(createRespRecorder(`{"username":"luuser","collection_name":"pub-x","visibility":"public"}`))
	CreateCollection(createRespRecorder(`{"username":"luuser","collection_name":"priv-x","visibility":"private"}`))

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Params = gin.Params{{Key: "username", Value: "luuser"}}
	ListCollections(c)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(t, err)
	data, ok := resp["data"].([]interface{})
	assert.True(t, ok)
	assert.Equal(t, 2, len(data), "user should see all of their collections")
}

func TestInvalidVisibilityRejected(t *testing.T) {
	_, cleanup := setupCollectionTest(t)
	defer cleanup()

	w := httptest.NewRecorder()
	c := newTestContext(w)
	c.Params = gin.Params{
		{Key: "username", Value: "ivuser"},
		{Key: "collection_name", Value: "iv-coll"},
	}

	// first create it
	CreateCollection(createRespRecorder(`{"username":"ivuser","collection_name":"iv-coll","visibility":"public"}`))

	setJSONBody(c, http.MethodPost, `{"visibility":"invalid_vis"}`)
	SetCollectionVisibility(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func createRespRecorder(body string) *gin.Context {
	w := httptest.NewRecorder()
	c := newTestContext(w)
	setJSONBody(c, http.MethodPost, body)
	return c
}
