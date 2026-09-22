// peer_pull.go：跨节点拉取保存端点（doc/NETDISK.md M3 / ROADMAP 阶段 6）。
//
// 用户动作 → 端点映射：
//   - 对方节点详情页"保存到我的网盘"（单文件）→ POST /p2p/pull
//   - 合集卡片"保存整个合集"               → POST /p2p/pull/collection
//   - 传输页查看/取消                      → GET /p2p/pull、POST /p2p/pull/cancel
//
// 全部为静态路径（不用 :id 参数）：gin 的路由树在静态段与参数段同级时容易
// 冲突，而这些动作语义上本来就能用 body 表达，没必要引入路径参数。
package controller

import (
	"encoding/json"
	"net/http"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// peerPuller 由 main 注入。
var peerPuller *service.PeerPuller

// InitPeerPuller 注入跨节点拉取服务。
func InitPeerPuller(p *service.PeerPuller) {
	log.LogDebug("ctrl-peer-pull: InitPeerPuller")
	peerPuller = p
}

// ListPullJobs 处理 GET /p2p/pull。
func ListPullJobs(c *gin.Context) {
	if peerPuller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	jobs := peerPuller.List()
	c.JSON(http.StatusOK, gin.H{"jobs": jobs, "count": len(jobs)})
}

// StartPull 处理 POST /p2p/pull {peer, hash, name?, path?}。
func StartPull(c *gin.Context) {
	if peerPuller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	var req struct {
		Peer string `json:"peer"`
		Hash string `json:"hash"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Peer == "" || req.Hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer and hash are required"})
		return
	}
	job, err := peerPuller.Start(req.Peer, req.Hash, req.Name, req.Path, "")
	if err != nil {
		log.LogWarn("ctrl-peer-pull: start pull from %s failed: %v", req.Peer, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

// StartPullCollection 处理 POST /p2p/pull/collection {peer, collection}。
//
// 语义：向对端要一次共享清单（share 帧）→ 找到该合集 → 把它的条目全部
// 建成拉取任务。清单是**当场拉取**的而不是预先缓存的：节点随时可能改共享
// 范围，用户点"保存整个合集"时必须以对端当前状态为准（与市场卡片上的
// 数量摘要不是一回事，那个只是引导信息）。
func StartPullCollection(c *gin.Context) {
	if peerPuller == nil || peerShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	var req struct {
		Peer       string `json:"peer"`
		Collection string `json:"collection"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Peer == "" || req.Collection == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer and collection are required"})
		return
	}
	snap, err := peerShareSvc.RequestShares(req.Peer)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	var found bool
	for _, coll := range snap.Collections {
		if coll.Hash != req.Collection {
			continue
		}
		found = true
		entries := make([]service.PullEntry, 0, len(coll.Entries))
		for _, e := range coll.Entries {
			entries = append(entries, service.PullEntry{Path: e.Path, Hash: e.Hash})
		}
		jobs := peerPuller.StartCollection(req.Peer, coll.Hash, entries)
		c.JSON(http.StatusOK, gin.H{
			"collection": coll.Hash,
			"name":       coll.Name,
			"jobs":       jobs,
			"count":      len(jobs),
		})
		return
	}
	if !found {
		// 清单里没有 ≠ 不存在：unlisted 合集**按定义**就不在清单里，而它的 manifest
		// 是按内容寻址存的一份 JSON，凭 hash 能直接取回。不兜这一步，"把一条合集
		// 链接整包存进来"就永远做不成（清单里找不到 → 404）。
		//
		// 级别不受影响：取回走同一条 req 通道，对端的 ShareGate 照样判——private
		// 合集的 manifest 取不到，这里同样回 404（清单里没有的东西，服务端不会
		// 告诉你"它存在但不给你"）。
		jobs, name, ok := startPullByManifest(req.Peer, req.Collection)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "collection not shared by peer"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"collection": req.Collection,
			"name":       name,
			"jobs":       jobs,
			"count":      len(jobs),
		})
	}
}

// startPullByManifest 清单里找不到合集时，按 hash 把 manifest 取回来再建任务。
//
// 返回 (jobs, name, ok)；ok=false 表示这条路也不通（没这个 hash / 离线 / private
// 被挡 / 取回来不是 manifest），调用方一律按 404 处理。
func startPullByManifest(peer, hash string) ([]*service.PullJob, string, bool) {
	raw, err := peerPuller.FetchManifest(peer, hash, 0)
	if err != nil {
		log.LogDebug("ctrl-peer-pull: fetch collection manifest %s failed: %v", hash, err)
		return nil, "", false
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(raw, &coll); err != nil || len(coll.Entries) == 0 {
		// 取回来了但不是合集（用户把某个文件的 hash 当合集 hash 传了）
		log.LogDebug("ctrl-peer-pull: %s is not a collection manifest", hash)
		return nil, "", false
	}
	entries := make([]service.PullEntry, 0, len(coll.Entries))
	for _, e := range coll.Entries {
		h := e.GetPrimaryHash()
		if h == "" {
			continue
		}
		entries = append(entries, service.PullEntry{Path: e.Path, Hash: h})
	}
	if len(entries) == 0 {
		return nil, "", false
	}
	return peerPuller.StartCollection(peer, hash, entries), coll.FriendlyName, true
}

// CancelPull 处理 POST /p2p/pull/cancel {id}。
func CancelPull(c *gin.Context) {
	if peerPuller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if err := peerPuller.Cancel(req.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancelled", "id": req.ID})
}
