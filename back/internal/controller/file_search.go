// file_search.go: 本地文件索引搜索端点（feat/file-index-search）。
//
//   GET /peerjs/files/search?q=&minSize=&maxSize=&offset=&limit=
//   GET /peerjs/nodes/:peer/search?q=&…  → 同一套参数，查的是**对端**的索引
//
// 为什么分两个端点：
//   - 本机搜索是自用工具（管理面板「找文件」框）：读的是本地 file_index，
//     不出网，所以不依赖 peerjs 连接是否建立。
//   - 对端搜索是「这台节点上有没有叫 X 的文件」的探测：**出网**，把关键词送到对端
//     执行。它比 share 动词更敏感（share 只返回运营者显式共享的范围，search 返回
//     对端全量索引的命中），所以它的风险等级按 share 那一档处理——挂 auth（见路由处）。
//
// 参数与语义（与 transport.SearchQuery 一一对应）：
//   q        子串，匹配 name 或 path，ASCII 大小写不敏感；空 = 不过滤
//   minSize  下界（含）；maxSize 上界（含）。**空参数 = 不施加该条件**，
//             传 0 是合法的「只要 0 字节」，与「不传」不同（指针语义）
//   offset   跳过前 N 条；limit 本页条数（上限 SearchMaxLimit，服务端 clamp）
//
// 响应 {files:[{hash,path,name,size,seq}], total:N, offset:M}
//   total 是**命中总数**（不是本页长度），供前端判断还有没有下一页。

package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"peerdrive/internal/log"
	"peerdrive/internal/transport"

	"github.com/gin-gonic/gin"
)

// fileIndexSearcher 搜索能力接口。controller 不直接依赖 *transport.PeerJSService：
// 它只需要「能搜本地索引」和「能搜对端索引」这两件事，用窄接口接住，单测可塞假实现。
type fileIndexSearcher interface {
	// Search 返回本地索引的一页（transport.SearchQuery / transport.SearchPage）。
	Search(q transport.SearchQuery) (*transport.SearchPage, error)
	// RequestSearch 检索对端索引。
	RequestSearch(peerID string, q transport.SearchQuery) (*transport.SearchPage, error)
	// ConnectedPeerIDs 当前直连的对端（"local" 已在 transport 侧排除）。
	ConnectedPeerIDs() map[string]bool
}

// fileSearchSvc 由 InitFileSearchController 注入（nil = 这组端点返回 503）。
var fileSearchSvc fileIndexSearcher

// InitFileSearchController 注入搜索能力。
func InitFileSearchController(s fileIndexSearcher) {
	log.LogDebug("ctrl-file-search: InitFileSearchController")
	fileSearchSvc = s
}

// parseSearchQuery 从 query string 解析搜索参数。
//
// 坏参数一律**忽略并按"未传"处理**，不返回 400：搜索框是人在边打字边发请求的，
// 前缀经常处于中间态（minSize= 空、limit=0）。为一个半截输入弹 400 会让前端
// 反复报红，而忽略它得到的结果最多是"多几条/少几条"——对搜索这种尽力而为的
// 操作，退化比报错好。真正的非法值（limit=abc）同样退回默认值。
func parseSearchQuery(c *gin.Context) transport.SearchQuery {
	q := transport.SearchQuery{
		Q:     c.Query("q"),
		Limit: transport.SearchDefaultLimit,
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			q.Offset = n
		}
	}
	// 空串 = 未传；显式 "0" = 0 字节（指针在这里就是为这个区分存在的）
	if v := c.Query("minSize"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.MinSize = &n
		}
	}
	if v := c.Query("maxSize"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			q.MaxSize = &n
		}
	}
	// limit 上限在 service/repository 层 clamp；这里只挡住明显离谱的负值
	if q.Limit < 0 {
		q.Limit = transport.SearchDefaultLimit
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	return q
}

// searchResponseJSON 统一的响应构造（本地/对端两个端点共用，保证字段完全一致）。
func searchResponseJSON(c *gin.Context, page *transport.SearchPage) {
	files := page.Files
	if files == nil {
		files = []transport.FileInfo{} // 保持 []，不给前端塞 null
	}
	c.JSON(http.StatusOK, gin.H{
		"files":  files,
		"total":  page.Total,
		"offset": page.Offset,
	})
}

// SearchLocalFiles handles GET /peerjs/files/search — 搜本机文件索引。
func SearchLocalFiles(c *gin.Context) {
	if fileSearchSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "file index search not available"})
		return
	}
	page, err := fileSearchSvc.Search(parseSearchQuery(c))
	if err != nil {
		log.LogWarn("ctrl-file-search: local search failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	searchResponseJSON(c, page)
}

// SearchPeerFiles handles GET /peerjs/nodes/:peer/search — 搜对端节点的文件索引。
func SearchPeerFiles(c *gin.Context) {
	if fileSearchSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "file index search not available"})
		return
	}
	peer := strings.TrimSpace(c.Param("peer"))
	if peer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer is required"})
		return
	}

	// 不像 GetPeerShares 那样自动拨号：本端点是**探测性**的（问一句「你有没有」），
	// 为了一句查询去建一条 WebRTC 连接并阻塞 8 秒，对未在网的对端来说是纯粹的
	// 资源浪费。没连上就直接 409，前端据此提示"节点不在线"而不是干等。
	if !fileSearchSvc.ConnectedPeerIDs()[peer] {
		c.JSON(http.StatusConflict, gin.H{"error": "peer not connected", "peer": peer})
		return
	}

	page, err := fileSearchSvc.RequestSearch(peer, parseSearchQuery(c))
	if err != nil {
		if errors.Is(err, transport.ErrPeerSearchUnsupported) {
			c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error(), "peer": peer})
			return
		}
		log.LogWarn("ctrl-file-search: search %s failed: %v", peer, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	searchResponseJSON(c, page)
}
