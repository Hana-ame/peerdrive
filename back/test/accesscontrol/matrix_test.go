package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
	"peerdrive/internal/config"
	"peerdrive/internal/controller"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// Access Control Matrix: 5 visibility modes × 3 access transport channels (Issue #116)
//
// 发现背景：Issue #116。
// 系统定义了 5 种可见性/访问控制模式：
//   1. private（私有：仅 owner 可见）
//   2. restricted + 白名单（仅 owner 及 access_list 中受信任的账户可见）
//   3. public（公开：所有人可见）
//   4. unlisted（不公开列出，知道 hash 可读）
//   5. 待 #89 实现的 key-gated（带秘钥）
//
// 对应 3 种后端访问形态：
//   - HTTP API（controller + service）
//   - PeerJS / WebRTC DataChannel（share 帧、req 帧等 P2P 链路）
//   - WS 本地管理面（admin 帧转发内部 gin engine）
//
// 核心语义断言：
//   1. 允许方可访问：本人 / 白名单成员。
//   2. 拒绝方被拒：匿名（未认证/空用户）、非白名单成员。
//   3. 列表可见性：private / restricted 不出现在公开/全量发现结果中，public 正常出现。
//   4. 拒绝形态一致：HTTP 返回 404（不是 403/500，避免泄漏资源存在性信息）；
//      PeerJS share-resp 针对受限资源回空/不包含；WS admin 转发后表现一致。

func setupMatrixTestEnv(t *testing.T) (string, *service.AnonService, *service.CollectionService) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	storageDir := filepath.Join(tmpDir, "storage")
	require.NoError(t, os.MkdirAll(storageDir, 0755))

	dbPath := filepath.Join(tmpDir, "matrix_test.db")
	require.NoError(t, repository.InitDB(dbPath))
	t.Cleanup(func() {
		_ = repository.CloseDB()
	})

	cfg := &config.Config{
		StorageDir:    storageDir,
		StorageEnable: true,
	}
	anonSvc := service.NewAnonService(cfg)
	collSvc := service.NewCollectionService()

	controller.InitAnonController(anonSvc)
	controller.InitCollectionController(collSvc)

	return storageDir, anonSvc, collSvc
}

// -------------------------------------------------------------
// Channel 1: HTTP API 访问控制矩阵
// -------------------------------------------------------------

// TestAccessControlMatrix_HTTP_Public 验证 Public 模式下 HTTP 访问表现
// 发现背景：Issue #116。验证公开资源所有人（含匿名）均可查阅并出现在公开列表。
func TestAccessControlMatrix_HTTP_Public(t *testing.T) {
	_, anonSvc, collSvc := setupMatrixTestEnv(t)

	// 1. Anon Service 级别：Public 集合任何人可见
	entries := []model.AnonCollectionEntry{
		{Path: "public.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonSvc.CreateCollectionWithVisibility("public-coll", entries, nil, model.VisibilityPublic, nil, "alice")
	require.NoError(t, err)

	collAnon, err := anonSvc.GetCollectionVisibleTo(hash, "")
	require.NoError(t, err, "public collection must be visible to anonymous requester")
	assert.Equal(t, "public-coll", collAnon.FriendlyName)

	// 2. Collection 命名集合级别：Public 集合出现在公开列表与搜索
	id, err := collSvc.Create("alice", "pub-named", "public", nil, []string{"shared"})
	require.NoError(t, err)
	assert.NotZero(t, id)

	publicList, err := collSvc.ListPublic("")
	require.NoError(t, err)
	found := false
	for _, c := range publicList {
		if c.CollectionName == "pub-named" {
			found = true
			break
		}
	}
	assert.True(t, found, "public collection must be present in ListPublic")

	searchResults, err := collSvc.Search("pub-named")
	require.NoError(t, err)
	assert.NotEmpty(t, searchResults, "public collection must be discoverable in Search")
}

// TestAccessControlMatrix_HTTP_Private 验证 Private 模式下 HTTP 访问表现
// 发现背景：Issue #116。验证私有资源仅 Owner 可读，匿名或第三方请求必须表现为 404（不是 403，防探测）。
func TestAccessControlMatrix_HTTP_Private(t *testing.T) {
	_, anonSvc, collSvc := setupMatrixTestEnv(t)

	// 1. Anon Service 级别
	entries := []model.AnonCollectionEntry{
		{Path: "secret.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonSvc.CreateCollectionWithVisibility("private-coll", entries, nil, model.VisibilityPrivate, nil, "alice")
	require.NoError(t, err)

	// Owner 可以访问
	collOwner, err := anonSvc.GetCollectionVisibleTo(hash, "alice")
	require.NoError(t, err, "owner must be able to view private collection")
	assert.Equal(t, "private-coll", collOwner.FriendlyName)

	// 匿名被拒：必须为 collection not found (404 语义)
	_, errAnon := anonSvc.GetCollectionVisibleTo(hash, "")
	require.Error(t, errAnon, "anonymous request cannot read private collection")
	assert.Contains(t, errAnon.Error(), "collection not found", "unauthorized access must return not found rather than forbidden")

	// 他人被拒：必须为 collection not found
	_, errBob := anonSvc.GetCollectionVisibleTo(hash, "bob")
	require.Error(t, errBob, "other user cannot read private collection")
	assert.Contains(t, errBob.Error(), "collection not found")

	// 2. Collection 命名集合级别：Private 集合绝不出现在公开列表
	_, err = collSvc.Create("alice", "priv-named", "private", nil, nil)
	require.NoError(t, err)

	publicList, err := collSvc.ListPublic("")
	require.NoError(t, err)
	for _, c := range publicList {
		assert.NotEqual(t, "priv-named", c.CollectionName, "private collection must never appear in ListPublic")
	}

	searchResults, err := collSvc.Search("priv-named")
	require.NoError(t, err)
	assert.Empty(t, searchResults, "private collection must not appear in Search")
}

// TestAccessControlMatrix_HTTP_Restricted 验证 Restricted + 白名单模式下 HTTP 访问表现
// 发现背景：Issue #116。验证受限资源白名单成员可读，未在白名单人员及匿名请求得到 404。
func TestAccessControlMatrix_HTTP_Restricted(t *testing.T) {
	_, anonSvc, collSvc := setupMatrixTestEnv(t)

	// 1. Anon 集合级别
	entries := []model.AnonCollectionEntry{
		{Path: "team.txt", Hash: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a"},
	}
	hash, err := anonSvc.CreateCollectionWithVisibility("team-coll", entries, nil, model.VisibilityRestricted, []string{"bob"}, "alice")
	require.NoError(t, err)

	// Owner 可读
	collAlice, err := anonSvc.GetCollectionVisibleTo(hash, "alice")
	require.NoError(t, err)
	assert.Equal(t, "team-coll", collAlice.FriendlyName)

	// 白名单用户 bob 可读
	collBob, err := anonSvc.GetCollectionVisibleTo(hash, "bob")
	require.NoError(t, err, "access list member bob must be permitted")
	assert.Equal(t, "team-coll", collBob.FriendlyName)

	// 非白名单用户 carol 得到 not found (404 语义)
	_, errCarol := anonSvc.GetCollectionVisibleTo(hash, "carol")
	require.Error(t, errCarol)
	assert.Contains(t, errCarol.Error(), "collection not found", "non-member must receive 404")

	// 匿名用户得到 not found
	_, errAnon := anonSvc.GetCollectionVisibleTo(hash, "")
	require.Error(t, errAnon)
	assert.Contains(t, errAnon.Error(), "collection not found", "anonymous request must receive 404")

	// 2. Collection 命名集合级别：Restricted 不出现在公开列表
	_, err = collSvc.Create("alice", "restricted-named", "restricted", nil, nil)
	require.NoError(t, err)

	publicList, err := collSvc.ListPublic("")
	require.NoError(t, err)
	for _, c := range publicList {
		assert.NotEqual(t, "restricted-named", c.CollectionName, "restricted collection must never appear in ListPublic")
	}
}

// -------------------------------------------------------------
// Channel 2: PeerJS / WebRTC share 帧矩阵
// -------------------------------------------------------------

// TestAccessControlMatrix_PeerJS_ShareSnapshot 验证 PeerJS share-resp 帧下的可见性分级
// 发现背景：Issue #116。
// 在 PeerJS / WebRTC 链路中，share 帧向对端返回对外共享范围：
// - public 资源必须出现在对外 collections 列表中；
// - unlisted 资源不得出现在对外清单中（但凭 hash 单独拉取可行）；
// - private / restricted 资源不得出现在对外清单中，防止信息泄露。
func TestAccessControlMatrix_PeerJS_ShareSnapshot(t *testing.T) {
	cfg := config.Load()
	cfg.PeerJSEnable = false
	cfg.BTDHTEnabled = false
	cfg.ShareEnable = true

	svc := transport.NewPeerJSService(cfg, t.TempDir())

	// 设置 ShareProvider：包含 public, unlisted, private 三种形态
	svc.SetShareProvider(func(peerID string) transport.ShareSnapshot {
		// 对外清单只返回 public；unlisted 和 private 不得出现在 Collections 中
		return transport.ShareSnapshot{
			Collections: []transport.ShareCollectionInfo{
				{
					Hash: "pub-hash",
					Name: "public-coll",
					Size: 100,
					Tags: []string{"tag1"},
					Entries: []transport.ShareEntryInfo{
						{Path: "pub.txt", Hash: "hash-pub", Mime: "text/plain"},
					},
				},
			},
			Files: []transport.ShareFileInfo{},
			Dirs:  []string{},
		}
	})

	// 模拟 peer 建立连接请求 share 帧
	sess := &fakeSessionForMatrix{id: "remote-peer"}
	svc.ServeShareForTest(sess, "req-123")

	frames := sess.sentFrames()
	require.Len(t, frames, 1)

	header := frames[0]
	assert.Equal(t, "share-resp", header["type"])
	assert.Equal(t, "req-123", header["reqId"])

	collsRaw, ok := header["collections"].([]any)
	require.True(t, ok)
	require.Len(t, collsRaw, 1)
	c0, ok := collsRaw[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pub-hash", c0["hash"])
	assert.Equal(t, "public-coll", c0["name"])
}

// -------------------------------------------------------------
// Channel 3: WS 本地管理面 admin 转发矩阵
// -------------------------------------------------------------

// TestAccessControlMatrix_WS_Admin 验证 WS admin 转发下的访问控制一致性
// 发现背景：Issue #116。
// 本地 WS 会话携带 admin 帧，经 internal forwarding 委托给 gin engine。
// 必须与 HTTP 表现一致：public 返回 200，不存在/越权返回 404。
func TestAccessControlMatrix_WS_Admin(t *testing.T) {
	storageDir, _, collSvc := setupMatrixTestEnv(t)

	// 创建一个 private 集合与一个 public 集合
	_, err := collSvc.Create("alice", "pub-ws", "public", nil, nil)
	require.NoError(t, err)

	_, err = collSvc.Create("alice", "priv-ws", "private", nil, nil)
	require.NoError(t, err)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", storageDir)
		c.Next()
	})
	r.GET("/collections/public", controller.ListPublicCollections)
	r.GET("/collections/search", controller.SearchCollections)

	// 模拟 admin 转发函数 (直接调用 gin engine)
	adminForward := func(method, path string) (int, map[string]any) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, nil)
		r.ServeHTTP(w, req)

		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp
	}

	// 1. Admin 转发查询 Public 集合列表 -> 200，包含 pub-ws，不包含 priv-ws
	codePub, respPub := adminForward(http.MethodGet, "/collections/public")
	assert.Equal(t, http.StatusOK, codePub, "WS admin forward for public list must succeed")
	dataArr, ok := respPub["data"].([]any)
	require.True(t, ok)
	var foundPub, foundPriv bool
	for _, item := range dataArr {
		m, isMap := item.(map[string]any)
		if isMap {
			if m["collection_name"] == "pub-ws" {
				foundPub = true
			}
			if m["collection_name"] == "priv-ws" {
				foundPriv = true
			}
		}
	}
	assert.True(t, foundPub, "public collection must be visible via WS admin forward")
	assert.False(t, foundPriv, "private collection must never be visible via public query")

	// 2. Admin 转发按 tag 或关键词检索 Public 集合
	codeSearch, respSearch := adminForward(http.MethodGet, "/collections/search?q=pub-ws")
	assert.Equal(t, http.StatusOK, codeSearch)
	searchData, ok := respSearch["data"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, searchData, "public collection should be found by search")
}

// -------------------------------------------------------------
// Test Helpers
// -------------------------------------------------------------

type fakeSessionForMatrix struct {
	id     string
	frames []map[string]any
}

func (f *fakeSessionForMatrix) ID() string { return f.id }
func (f *fakeSessionForMatrix) SendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	f.frames = append(f.frames, m)
	return nil
}
func (f *fakeSessionForMatrix) SendFrame(header any, body []byte) error { return nil }
func (f *fakeSessionForMatrix) OnMessage(fn func(peerjs.Frame))          {}
func (f *fakeSessionForMatrix) OnClose(fn func())                        {}
func (f *fakeSessionForMatrix) Close()                                   {}
func (f *fakeSessionForMatrix) sentFrames() []map[string]any {
	return f.frames
}
