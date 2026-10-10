// file_index_search_test.go — 文件索引搜索的传输层测试（service + 帧动词）。
//
// 发现背景：feat/file-index-search。repository 层的 SQL 语义已有表驱动测试覆盖
// （internal/repository/file_index_search_test.go），这里验证两件上层特有的事：
//   1. FileIndexService.Search 的**对外契约**（返回空切片而非 nil、limit clamp、
//      offset 回显与归零）——它被 HTTP/帧两层共用，语义漂移会直接变成前端 bug。
//   2. serveSearch 的**帧协议**（search-resp 字段、total 与 list 语义不同、路径脱敏）。
//      脱敏尤其关键：search 的匹配条件含 path，省掉 redactDisallowedPath 等于在
//      serveList 辛苦做的边界隔离上又开个洞。
//
// 测试数据走 Create 真实登记（受 H2 根目录限制），路径都在 uploadDir 下，
// 这样 path 子串搜索才有可断言的真实目录段。

package transport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"peerdrive/internal/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// int64p 搜索测试里的「确实传了这个 size 边界」的指针写法。
func int64p(v int64) *int64 { return &v }

// seedIndexFiles 在服务根目录的 media/ 子目录下建出这些文件并登记，返回 FileInfo。
// 放进子目录是为了让「只在 path 里、name 里没有」的关键词（media）可测。
func seedIndexFiles(t *testing.T, svc *FileIndexService, names ...string) []FileInfo {
	t.Helper()
	mediaDir := filepath.Join(svc.uploadDir, "media")
	require.NoError(t, os.MkdirAll(mediaDir, 0o755))

	out := make([]FileInfo, 0, len(names))
	for _, n := range names {
		p := filepath.Join(mediaDir, n)
		require.NoError(t, os.WriteFile(p, []byte(n+"-content"), 0o644))
		fi, err := svc.Create(p)
		require.NoError(t, err, "create %s", n)
		out = append(out, *fi)
	}
	return out
}

// newSearchTestService 一个带内存库的 PeerJSService（帧动词测试用）。
// initTestDB 必须显式调：它只建 DB，transport 的 service 不负责初始化。
func newSearchTestService(t *testing.T) *PeerJSService {
	t.Helper()
	initTestDB(t)
	return newTestPeerJSService(t) // 已注册 fileIndex.Close（Windows TempDir 清理）
}

// TestFileIndexSearch_Basic 子串命中 + 目录级命中 + 未命中。
func TestFileIndexSearch_Basic(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)
	seedIndexFiles(t, svc, "album01.mp3", "album02.mp3", "film.mov")

	// name 子串；seq DESC → 后登记的（album02）在前
	page, err := svc.Search(SearchQuery{Q: "album"})
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, int64(2), page.Total)
	require.Len(t, page.Files, 2)
	assert.Equal(t, "album02.mp3", page.Files[0].Name, "按 seq DESC，后登记的排在前")
	assert.Equal(t, "album01.mp3", page.Files[1].Name)

	// 只在 path 里的目录关键词（name 全都不含 media）
	page, err = svc.Search(SearchQuery{Q: "media"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), page.Total)

	// 未命中：Files 是**空切片不是 nil**（调用方直接 JSON 编码，nil 会成 "files":null）
	page, err = svc.Search(SearchQuery{Q: "nosuchfile"})
	require.NoError(t, err)
	require.NotNil(t, page.Files, "必须返回空切片")
	assert.Len(t, page.Files, 0)
	assert.Equal(t, int64(0), page.Total)

	// 空 q = 不过滤
	page, err = svc.Search(SearchQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), page.Total)
	assert.Len(t, page.Files, 3)
}

// TestFileIndexSearch_SizeRange size 区间过滤（含 0 字节空文件这一边界）。
func TestFileIndexSearch_SizeRange(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)

	// 空文件：size=0，正是「指针而非 0 哨兵」要覆盖的边界
	empty := filepath.Join(svc.uploadDir, "media", "empty.bin")
	require.NoError(t, os.MkdirAll(filepath.Dir(empty), 0o755))
	require.NoError(t, os.WriteFile(empty, nil, 0o644))
	_, err := svc.Create(empty)
	require.NoError(t, err)

	seedIndexFiles(t, svc, "small.bin", "big.bin")

	page, err := svc.Search(SearchQuery{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), page.Total)

	// 只限下界 → 排除空文件
	page, err = svc.Search(SearchQuery{MinSize: int64p(1)})
	require.NoError(t, err)
	assert.Equal(t, int64(2), page.Total)
	for _, f := range page.Files {
		assert.GreaterOrEqual(t, f.Size, int64(1), "size=0 的空文件必须被下界排除")
	}

	// 精确筛 0 字节 → 只剩空文件（证明 nil/0 语义没有混起来）
	page, err = svc.Search(SearchQuery{MinSize: int64p(0), MaxSize: int64p(0)})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	assert.Equal(t, "empty.bin", page.Files[0].Name)
	assert.Equal(t, int64(0), page.Files[0].Size)
}

// TestFileIndexSearch_LimitClamp limit 的远端可控值必须被夹住（内存 DoS 防御）。
func TestFileIndexSearch_LimitClamp(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)
	seedIndexFiles(t, svc, "a.bin", "b.bin")

	// 超上限 → 被 clamp 到 SearchMaxLimit（此处数据少，返回全量）
	page, err := svc.Search(SearchQuery{Limit: 1 << 20})
	require.NoError(t, err)
	assert.Len(t, page.Files, 2)
	assert.Equal(t, int64(2), page.Total, "clamp 的是本页长度，total 不受影响")

	// limit<=0 → repository 层默认 100
	page, err = svc.Search(SearchQuery{Limit: 0})
	require.NoError(t, err)
	assert.Len(t, page.Files, 2)
}

// TestFileIndexSearch_PaginationOffset 分页与 offset 归零/回显。
func TestFileIndexSearch_PaginationOffset(t *testing.T) {
	initTestDB(t)
	svc := newTestIndex(t)
	seedIndexFiles(t, svc, "f0.bin", "f1.bin", "f2.bin")

	p1, err := svc.Search(SearchQuery{Limit: 2})
	require.NoError(t, err)
	require.Len(t, p1.Files, 2)
	assert.Equal(t, int64(3), p1.Total, "total 是命中总数，不是本页长度")
	assert.Equal(t, 0, p1.Offset)

	p2, err := svc.Search(SearchQuery{Offset: 2, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, p2.Files, 1)
	assert.Equal(t, 2, p2.Offset, "offset 必须回显，前端据此定位翻页状态")
	assert.NotEqual(t, p1.Files[0].Hash, p2.Files[0].Hash, "两页不得重叠")

	// 负 offset 归零
	pn, err := svc.Search(SearchQuery{Offset: -7, Limit: 2})
	require.NoError(t, err)
	assert.Equal(t, 0, pn.Offset)
	assert.Equal(t, p1.Files[0].Hash, pn.Files[0].Hash, "offset<0 应等同 offset=0")

	// offset 越过末尾：空页但 total 仍在（客户端知道数据在后面而非不存在）
	po, err := svc.Search(SearchQuery{Offset: 99, Limit: 2})
	require.NoError(t, err)
	assert.Len(t, po.Files, 0)
	assert.Equal(t, int64(3), po.Total)
}

// TestServeSearch_Frame 帧协议：search-resp 的字段与 total 语义。
func TestServeSearch_Frame(t *testing.T) {
	svc := newSearchTestService(t)
	seedIndexFiles(t, svc.fileIndex, "album01.mp3", "album02.mp3")

	sess := &fakeSession{id: "peer-x"}
	svc.serveSearch(sess, dcResp{Type: "search", Query: "album", ReqID: "s1"})

	frames := sess.sentFrames()
	require.Len(t, frames, 1)
	got := frames[0].header

	assert.Equal(t, "search-resp", got["type"])
	assert.Equal(t, "s1", got["reqId"], "请求-响应配对依赖 reqId")
	assert.Equal(t, float64(2), got["total"], "total 必须是命中总数")

	files, ok := got["files"].([]any)
	require.True(t, ok, "files 必须是数组：%v", got["files"])
	require.Len(t, files, 2)

	f0 := files[0].(map[string]any)
	for _, k := range []string{"hash", "path", "name", "size", "seq"} {
		assert.Contains(t, f0, k, "search-resp 的 file 缺字段 %q", k)
	}
	// seq DESC：album02 后登记，排在前
	assert.Equal(t, "album02.mp3", f0["name"])
}

// TestServeSearch_EmptyResult 零命中时 files 仍是空数组，不是 null。
func TestServeSearch_EmptyResult(t *testing.T) {
	svc := newSearchTestService(t)
	seedIndexFiles(t, svc.fileIndex, "a.bin")

	sess := &fakeSession{id: "peer-x"}
	svc.serveSearch(sess, dcResp{Type: "search", Query: "zzz-nothing", ReqID: "s2"})

	frames := sess.sentFrames()
	require.Len(t, frames, 1)
	got := frames[0].header
	assert.Equal(t, "search-resp", got["type"])
	assert.Equal(t, float64(0), got["total"])
	files, ok := got["files"].([]any)
	require.True(t, ok, "零命中时 files 必须是 []，不能是 null：%v", got["files"])
	assert.Len(t, files, 0)
}

// TestServeSearch_RedactsDisallowedPath 搜索命中后必须脱敏越界路径。
//
// 发现背景：search 的匹配条件含 path，对端能拿目录名当探测串问「这台机器上有没有
// /etc/... 下的文件」，命中与否就是侧信道。所以 serveSearch 必须和 serveList 一样
// 过 redactDisallowedPath：登记在允许根之外的映射（ApplySync 不查 IsPathAllowed）
// 不能把绝对路径回给对端。
func TestServeSearch_RedactsDisallowedPath(t *testing.T) {
	svc := newSearchTestService(t)

	// 直接写库造一条「在允许根之外」的映射（等价于 ApplySync 写进来的）
	outside := filepath.Join(t.TempDir(), "elsewhere", "secret.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o755))
	require.NoError(t, os.WriteFile(outside, []byte("s3cret"), 0o644))
	_, err := repository.UpsertFileIndex(
		"aa00000000000000000000000000000000000000000000000000000000000000",
		outside, "secret.txt", 6, false)
	require.NoError(t, err)

	sess := &fakeSession{id: "peer-x"}
	svc.serveSearch(sess, dcResp{Type: "search", Query: "secret", ReqID: "s3"})

	frames := sess.sentFrames()
	require.Len(t, frames, 1)
	files, ok := frames[0].header["files"].([]any)
	require.True(t, ok)
	require.Len(t, files, 1, "命中本身可以返回，但路径不能泄露")
	f0 := files[0].(map[string]any)
	assert.Equal(t, "", f0["path"], "根目录外的绝对路径必须被抹掉")
	assert.Equal(t, "secret.txt", f0["name"], "name 是 basename，不泄露目录结构")
}

// TestServeSearch_MinMaxSizeFromFrame 帧上的 minSize/maxSize 生效（含指针语义）。
func TestServeSearch_MinMaxSizeFromFrame(t *testing.T) {
	svc := newSearchTestService(t)
	seedIndexFiles(t, svc.fileIndex, "small.bin", "big.bin")

	// 以实际大小为基准取下界（写死 1024 会把两个文件都滤掉——那是测试写错，
	// 不是过滤逻辑错：先查大小再设界，别猜）
	var sizes []int64
	for _, f := range mustSearchAll(t, svc.fileIndex) {
		sizes = append(sizes, f.Size)
	}
	require.Len(t, sizes, 2)
	lo, hi := sizes[0], sizes[1]
	if lo > hi {
		lo, hi = hi, lo
	}

	// 下界 = hi（严格大于小文件）→ 只命中大文件
	sess := &fakeSession{id: "peer-x"}
	svc.serveSearch(sess, dcResp{
		Type: "search", Query: ".bin", ReqID: "s4",
		MinSize: int64p(hi), MaxSize: int64p(hi), Offset: 0, Size: 10,
	})
	frames := sess.sentFrames()
	require.Len(t, frames, 1)
	files := frames[0].header["files"].([]any)
	require.Len(t, files, 1, "下界 = 两个文件里较大的那个，只应命中它")
	gotName := files[0].(map[string]any)["name"].(string)

	// 下界比最大文件还大 → 零命中
	sess2 := &fakeSession{id: "peer-y"}
	svc.serveSearch(sess2, dcResp{
		Type: "search", Query: ".bin", ReqID: "s5",
		MinSize: int64p(hi + 1000), Offset: 0, Size: 10,
	})
	f2 := sess2.sentFrames()
	require.Len(t, f2, 1)
	assert.Len(t, f2[0].header["files"].([]any), 0)
	assert.Equal(t, float64(0), f2[0].header["total"], "零命中时 total=0 也必须上线")

	// 交叉验证：命中的那个确实大小等于 hi
	for _, f := range mustSearchAll(t, svc.fileIndex) {
		if f.Name == gotName {
			assert.Equal(t, hi, f.Size)
		}
	}
}

// mustSearchAll 返回全部登记项（给上面的动态下界取基准）。
func mustSearchAll(t *testing.T, svc *FileIndexService) []FileInfo {
	t.Helper()
	page, err := svc.Search(SearchQuery{})
	require.NoError(t, err)
	return page.Files
}

// TestServeSearch_ZeroTotalOnWire 零命中时 total=0 字段必须真的在线上。
//
// 发现背景：dcResp.Total 带 `json:"total,omitempty"`，零命中时 total 会被整个丢掉，
// 客户端收到的是「没有 total 的 search-resp」——与老节点（不认识 search）的
// 行为无法区分。搜索恰恰是「大概率零结果」的功能（用户打字过程中前缀每一步都
// 可能无命中），所以这个 0 是必须显式出现的信号，不是可有可无的零值。
func TestServeSearch_ZeroTotalOnWire(t *testing.T) {
	svc := newSearchTestService(t)
	seedIndexFiles(t, svc.fileIndex, "a.bin")

	sess := &fakeSession{id: "peer-x"}
	svc.serveSearch(sess, dcResp{Type: "search", Query: "no-such-thing", ReqID: "s6"})

	frames := sess.sentFrames()
	require.Len(t, frames, 1)

	// 从原始 JSON 字节断言，而不是从 Unmarshal 后的 map —— map 里"字段不存在"
	// 和"字段等于零值"长得一模一样，正是这个 bug 藏身的地方。
	raw := sess.sent[0]
	b, err := json.Marshal(raw)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"total":0`,
		"零命中时 total 必须显式上线，否则客户端无法区分无命中与老节点：%s", b)
}

// 发现背景：servedVerbs 是白名单，新动词不在名单里就会被 pskGate 放行——
// 搜索会枚举本地文件名/路径，等于给未过门禁的对端开一扇本地元数据的窗。
func TestSearchVerb_PSKGated(t *testing.T) {
	assert.True(t, servedVerbs["search"],
		"search 必须在 servedVerbs 白名单里，否则 PSK 门禁对它无效")
}

// TestSearchVerb_Dispatched 帧真的能被 dispatchFrame 路由到 serveSearch。
// 覆盖「注册进 switch」这一环——漏注册的话上面所有 handler 单测照样绿。
func TestSearchVerb_Dispatched(t *testing.T) {
	svc := newSearchTestService(t)
	seedIndexFiles(t, svc.fileIndex, "dispatched.bin")

	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapIndex})
	require.NotNil(t, svc.pending[sess], "bindConn 应登记 connState")

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "search", "q": "dispatched", "reqId": "d1",
	}))

	// serveSearch 在 dispatchFrame 里是 go 出去跑的，异步等一下
	got, ok := waitSent(sess, "search-resp", 3*time.Second)
	require.True(t, ok, "dispatch 必须产出 search-resp 帧")
	assert.Equal(t, "d1", got["reqId"])
	files, ok := got["files"].([]any)
	require.True(t, ok)
	require.Len(t, files, 1)
	assert.Equal(t, "dispatched.bin", files[0].(map[string]any)["name"])
}
