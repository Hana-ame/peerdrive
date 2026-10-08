// file_search_test.go — 文件索引搜索端点测试（feat/file-index-search）。
//
// 发现背景：搜索此前只有内部 API（repository/transport），没有 HTTP 面。
// 本文件验三件 handler 层特有的事：
//   1. 参数解析的**容错语义**：坏参数忽略而非 400（搜索框是边打字边发的，半截输入
//     不该让前端反复报错）；minSize=0 与「不传」必须区分开。
//   2. 响应契约：files 恒为数组、total 是**命中总数**不是本页长度、offset 回显。
//   3. 状态码分支：未注入→503、未连接→409、对端不支持→501、查询出错→500/502。
//
// 用假实现而非真 PeerJSService：controller 依赖的是窄接口
// （fileIndexSearcher），这正是把它抽出来的目的——测一个 handler 不必起
// 信令、WebRTC、SQLite。

package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"peerdrive/internal/transport"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSearcher controller.fileIndexSearcher 的测试实现。
type fakeSearcher struct {
	lastQ transport.SearchQuery

	localFiles []transport.FileInfo
	localTotal int64
	localErr   error
	peerFiles  []transport.FileInfo
	peerTotal  int64
	peerOffset int
	peerErr    error
	peerID     string
	connected  map[string]bool
	localCalls int
	peerCalls  int
}

func (f *fakeSearcher) Search(q transport.SearchQuery) (*transport.SearchPage, error) {
	f.localCalls++
	f.lastQ = q
	if f.localErr != nil {
		return nil, f.localErr
	}
	return &transport.SearchPage{Files: f.localFiles, Total: f.localTotal, Offset: q.Offset}, nil
}

func (f *fakeSearcher) RequestSearch(peerID string, q transport.SearchQuery) (*transport.SearchPage, error) {
	f.peerCalls++
	f.peerID = peerID
	f.lastQ = q
	if f.peerErr != nil {
		return nil, f.peerErr
	}
	return &transport.SearchPage{Files: f.peerFiles, Total: f.peerTotal, Offset: f.peerOffset}, nil
}

func (f *fakeSearcher) ConnectedPeerIDs() map[string]bool { return f.connected }

// setupSearchRouter 装一个只挂两个搜索路由的 gin，并注入假实现。
func setupSearchRouter(t *testing.T, f *fakeSearcher) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	InitFileSearchController(f)
	t.Cleanup(func() { InitFileSearchController(nil) })

	r := gin.New()
	r.GET("/peerjs/files/search", SearchLocalFiles)
	r.GET("/peerjs/nodes/:peer/search", SearchPeerFiles)
	return r
}

// doGet 发一个 GET 并返回 recorder。
func doGet(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

// decodeBody 解出响应体。
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	b, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m), "body=%s", b)
	return m
}

// TestSearchLocalFiles_Basic 本地搜索：参数解析 + 响应契约。
func TestSearchLocalFiles_Basic(t *testing.T) {
	f := &fakeSearcher{
		localFiles: []transport.FileInfo{
			{Hash: "aa", Name: "album01.mp3", Path: "/data/album01.mp3", Size: 100, Seq: 1},
		},
		localTotal: 7, // 命中 7 条，本页只回 1 条
	}
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/files/search?q=album&minSize=10&maxSize=1000&offset=2&limit=5")
	require.Equal(t, http.StatusOK, w.Code)

	// 参数真的解析到了
	assert.Equal(t, "album", f.lastQ.Q)
	require.NotNil(t, f.lastQ.MinSize)
	assert.Equal(t, int64(10), *f.lastQ.MinSize)
	require.NotNil(t, f.lastQ.MaxSize)
	assert.Equal(t, int64(1000), *f.lastQ.MaxSize)
	assert.Equal(t, 2, f.lastQ.Offset)
	assert.Equal(t, 5, f.lastQ.Limit)

	body := decodeBody(t, w)
	assert.Equal(t, float64(7), body["total"], "total 必须是命中总数，不是本页长度")
	assert.Equal(t, float64(2), body["offset"], "offset 必须回显")
	files := body["files"].([]any)
	require.Len(t, files, 1)
	f0 := files[0].(map[string]any)
	assert.Equal(t, "album01.mp3", f0["name"])
	assert.Equal(t, "/data/album01.mp3", f0["path"])
	assert.Equal(t, float64(100), f0["size"])
	assert.Equal(t, float64(1), f0["seq"])
}

// TestSearchLocalFiles_ParamTolerance 坏参数按「未传」处理，不报 400。
//
// 发现背景：搜索框是边打字边发请求的，前缀经常是半截状态（minSize= 空、limit=0）。
// 为一个半截输入弹 400 会让前端反复报错，而忽略它最多得到"多几条/少几条"——
// 对搜索这种尽力而为的操作，退化比报错好。
func TestSearchLocalFiles_ParamTolerance(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		wantLimit int
		wantOff   int
		wantMin   *int64
	}{
		{"全部缺省用默认 limit", "/peerjs/files/search", transport.SearchDefaultLimit, 0, nil},
		{"limit 为空串", "/peerjs/files/search?limit=", transport.SearchDefaultLimit, 0, nil},
		{"limit 非数字", "/peerjs/files/search?limit=abc", transport.SearchDefaultLimit, 0, nil},
		{"limit 负数", "/peerjs/files/search?limit=-3", transport.SearchDefaultLimit, 0, nil},
		{"offset 非数字", "/peerjs/files/search?offset=x", transport.SearchDefaultLimit, 0, nil},
		{"offset 负数归零", "/peerjs/files/search?offset=-9", transport.SearchDefaultLimit, 0, nil},
		{"minSize 空串=未传", "/peerjs/files/search?minSize=", transport.SearchDefaultLimit, 0, nil},
		{"minSize 非数字=未传", "/peerjs/files/search?minSize=big", transport.SearchDefaultLimit, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSearcher{}
			r := setupSearchRouter(t, f)
			w := doGet(t, r, tc.url)
			assert.Equal(t, http.StatusOK, w.Code, "坏参数必须被忽略而不是 400")
			assert.Equal(t, tc.wantLimit, f.lastQ.Limit)
			assert.Equal(t, tc.wantOff, f.lastQ.Offset)
			if tc.wantMin == nil {
				assert.Nil(t, f.lastQ.MinSize)
			}
		})
	}
}

// TestSearchLocalFiles_ZeroSizeDistinct minSize=0 必须与「不传」区分开。
//
// 发现背景：空文件在索引里 size 就是 0。若把「不传」也当 0，前端就永远搜不到
// 「所有 0 字节文件」这个合法查询；若把 0 当「不限制」，反过来搜不出空文件。
// 指针参数是这里唯一正确的编码方式——nil 是「不限制」，&0 是「要 0 字节」。
func TestSearchLocalFiles_ZeroSizeDistinct(t *testing.T) {
	f := &fakeSearcher{}
	r := setupSearchRouter(t, f)

	// 不传
	w := doGet(t, r, "/peerjs/files/search")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, f.lastQ.MinSize, "minSize 未传必须是 nil（不限制）")

	// 显式 0
	w = doGet(t, r, "/peerjs/files/search?minSize=0")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, f.lastQ.MinSize, "minSize=0 必须是「限定 0 字节」，不是「不限制」")
	assert.Equal(t, int64(0), *f.lastQ.MinSize)
}

// TestSearchLocalFiles_EmptyFilesIsArray 零命中时 files 是 [] 不是 null。
func TestSearchLocalFiles_EmptyFilesIsArray(t *testing.T) {
	f := &fakeSearcher{} // Files 为 nil，模拟无命中
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/files/search?q=nomatch")
	require.Equal(t, http.StatusOK, w.Code)
	body := decodeBody(t, w)
	assert.Equal(t, float64(0), body["total"])
	_, ok := body["files"].([]any)
	assert.True(t, ok, "files 必须序列化成 []，不能是 null：%v", body["files"])
}

// TestSearchLocalFiles_NotInjected 未注入服务 → 503（与本仓库其它 Init* 约定一致）。
func TestSearchLocalFiles_NotInjected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	InitFileSearchController(nil)
	r := gin.New()
	r.GET("/peerjs/files/search", SearchLocalFiles)

	w := doGet(t, r, "/peerjs/files/search?q=x")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// TestSearchLocalFiles_Error 查询出错 → 500。
func TestSearchLocalFiles_Error(t *testing.T) {
	f := &fakeSearcher{localErr: errors.New("db gone")}
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/files/search?q=x")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestSearchPeerFiles_NotConnected 未连接的对端 → 409，且**不**去拨号。
//
// 发现背景：与 GetPeerShares 不同，这个端点是探测性的（「问一句你有没有」）。
// 为一句查询自动建 WebRTC 连接并阻塞 8 秒，对离线对端是纯浪费；返回 409 让
// 前端直接提示"节点不在线"。
func TestSearchPeerFiles_NotConnected(t *testing.T) {
	f := &fakeSearcher{connected: map[string]bool{}}
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/nodes/peer-x/search?q=x")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, 0, f.peerCalls, "未连接时不得发起 search 帧")
	body := decodeBody(t, w)
	assert.Equal(t, "peer-x", body["peer"])
}

// TestSearchPeerFiles_OK 已连接 → 查询并返回。
func TestSearchPeerFiles_OK(t *testing.T) {
	f := &fakeSearcher{
		connected:  map[string]bool{"peer-x": true},
		peerFiles:  []transport.FileInfo{{Hash: "bb", Name: "remote.bin", Size: 42}},
		peerTotal:  3,
		peerOffset: 1,
	}
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/nodes/peer-x/search?q=remote&limit=2")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, f.peerCalls)
	assert.Equal(t, "peer-x", f.peerID)
	assert.Equal(t, "remote", f.lastQ.Q)

	body := decodeBody(t, w)
	assert.Equal(t, float64(3), body["total"])
	files := body["files"].([]any)
	require.Len(t, files, 1)
	assert.Equal(t, "remote.bin", files[0].(map[string]any)["name"])
}

// TestSearchPeerFiles_Unsupported 老对端不支持 search → 501。
//
// 发现背景：老节点的 dispatchFrame 会静默丢弃 search 帧，调用方只能等满 15s
// 超时。transport.RequestSearch 把超时归因为 ErrPeerSearchUnsupported（见其注释），
// 这一层把它翻成 501「不支持」而不是 502「坏网关」——前者让用户知道要升级节点。
func TestSearchPeerFiles_Unsupported(t *testing.T) {
	f := &fakeSearcher{
		connected: map[string]bool{"old-node": true},
		peerErr:   transport.ErrPeerSearchUnsupported,
	}
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/nodes/old-node/search?q=x")
	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

// TestSearchPeerFiles_QueryFailed 对端在线但查询失败 → 502。
func TestSearchPeerFiles_QueryFailed(t *testing.T) {
	f := &fakeSearcher{
		connected: map[string]bool{"peer-x": true},
		peerErr:   errors.New("peerjs: connection closed"),
	}
	r := setupSearchRouter(t, f)

	w := doGet(t, r, "/peerjs/nodes/peer-x/search?q=x")
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// TestParseSearchQuery_LimitNotClampedAtHandler clamp 发生在 service 层，不是 handler。
//
// 理由：HTTP handler 与帧协议两条入口共用 FileIndexService.Search，把上限逻辑
// 放一处才不会被绕过。handler 若自己 clamp 到 SearchMaxLimit，那么直接调
// service 的帧路径就少了一道——所以这里显式断言 handler **不** clamp（1<<20 原样透传）。
func TestParseSearchQuery_LimitNotClampedAtHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?limit=1048576", nil)

	q := parseSearchQuery(c)
	assert.Equal(t, 1<<20, q.Limit, "handler 不做上限 clamp（唯一出入口在 service/repository）")
}
