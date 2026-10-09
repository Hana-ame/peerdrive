// twitterpic.go: twitter-pic 图库（twitter-pic-go 的延伸）的 HTTP 暴露面。
//
// 可选模块（PEERDRIVE_TWITTERPIC_ENABLE，默认关）：x4545810.xyz 旧版界面入口
// 背后的 twitter-pic 数据面，按「一个 user 作为一个 collection」整合进 peerdrive
// —— 拉用户 timeline → 摄取媒体进 sha-文件系统 → 生成内容寻址 collection JSON。
// 本 controller 是薄 HTTP 层：列表、构建、按 sha 读出、fetch-source 监视快照。
// 开关关掉时服务不构造（router 不注册这些路由），对默认节点零变化。

package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/log"
	"peerdrive/internal/twitterpic"
	"peerdrive/pkg/hashutil"
)

// twitterSvc 由 serverapp 注入；nil 表示模块未启用（路由未注册）。
var twitterSvc *twitterpic.Service

// InitTwitterPic 注入 twitter-pic 服务（nil = 端点关闭）。
func InitTwitterPic(s *twitterpic.Service) {
	log.LogDebug("ctrl-twitterpic: InitTwitterPic")
	twitterSvc = s
}

// twitterMetaTimeout 约束一次远程操作（用户列表/单用户构建都可能拉多次 API
// 与媒体），比 iwaraMetaTimeout 更宽松——媒体下载受 Service 的 per-request
// Timeout 约束，这里只兜整体。
const twitterMetaTimeout = 5 * time.Minute

// TwitterPicUsers 处理 GET /twitterpic/users：返回远端用户列表。
func TwitterPicUsers(c *gin.Context) {
	if twitterSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "twitter-pic module not enabled"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), twitterMetaTimeout)
	defer cancel()
	users, err := twitterSvc.ListUsers(ctx)
	if err != nil {
		log.LogDebug("ctrl-twitterpic: list users: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, users)
}

// TwitterPicBuild 处理 POST /twitterpic/users/:username/build：构建该用户的
// collection（摄取媒体 → 落 sha-文件系统 → 记录指针），返回集合 sha。
// 写节点 storage，路由挂 authRequired。
func TwitterPicBuild(c *gin.Context) {
	if twitterSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "twitter-pic module not enabled"})
		return
	}
	username := c.Param("username")
	if username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), twitterMetaTimeout)
	defer cancel()
	res, err := twitterSvc.BuildUserCollection(ctx, username)
	if err != nil {
		log.LogDebug("ctrl-twitterpic: build %q: %v", username, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"username":  username,
		"sha":       res.CollectionSHA,
		"entries":   len(res.Collection.Entries),
		"ingested":  res.Ingested,
		"build_at":  time.Now().Unix(),
	})
}

// TwitterPicCollection 处理 GET /twitterpic/collections/:sha：按 sha 读出集合
// JSON 原文（respond-by-sha 路径，与 collection_test 里 pinned 的 wire 形态一致）。
func TwitterPicCollection(c *gin.Context) {
	if twitterSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "twitter-pic module not enabled"})
		return
	}
	sha := c.Param("sha")
	if !hashutil.IsStrictSHA256(sha) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid sha"})
		return
	}
	data, err := twitterSvc.ReadCollection(c.Request.Context(), sha)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", data)
}

// TwitterPicFetchStats 处理 GET /twitterpic/fetch-stats：返回文件源访问监视
// 快照（各备选源 sha/ech-url/url/private 在时间窗内的次数与成功率 + 累计）。
func TwitterPicFetchStats(c *gin.Context) {
	if twitterSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "twitter-pic module not enabled"})
		return
	}
	stats := twitterSvc.FetchStats()
	recent := twitterSvc.RecentAttempts(0)
	c.JSON(http.StatusOK, gin.H{
		"by_alt": stats,
		"recent": recent,
	})
}
