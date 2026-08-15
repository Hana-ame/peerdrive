package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/controller"
)

// dispatchCreateCollection 分派 POST /collections：
// body 含 username 字段 → 用户体系（CreateCollection），否则匿名集合。
func dispatchCreateCollection(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		controller.CreateAnonCollection(c)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))

	var probe struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Username != "" {
		controller.CreateCollection(c)
		return
	}
	controller.CreateAnonCollection(c)
}

// dispatchGetCollection 分派 GET /collections/:id：
// 64 位 hex → 匿名集合（GetAnonCollection），否则按 username 列集合。
// 背景：anon 体系（hash 寻址）与用户体系（username 寻址）共用 /collections
// 路径，但 gin 不允许同层 :param 用不同名字注册，故统一 :id 再按形态分派。
func dispatchGetCollection(c *gin.Context) {
	id := c.Param("id")
	if len(id) == 64 {
		cp := withParams(c, "hash", id)
		controller.GetAnonCollection(cp)
		return
	}
	cp := withParams(c, "username", id)
	controller.ListCollections(cp)
}

// dispatchGetTree 分派 GET /collections/:id/*filepath：
//   - id 为 64 位 hex → 匿名集合文件下载（DownloadAnonFile）
//   - filepath 为空 → 用户集合列表（ListCollections）
//   - filepath 单段 → 用户集合详情（GetCollection）
//   - filepath 两段且末段为 log → 版本日志（GetVersionLog）
//
// 坑：gin 不允许同一位置的 :param 与 *wildcard 并存
// （"/:username/:collection_name" 与 "/:username/*filepath" 注册即 panic），
// 所以用户体系的深层 GET 全部并入这一个 wildcard 路由内部分派。
func dispatchGetTree(c *gin.Context) {
	id := c.Param("id")
	parts := splitPath(c.Param("filepath"))

	if len(id) == 64 {
		cp := withParams(c, "hash", id)
		controller.DownloadAnonFile(cp)
		return
	}
	switch {
	case len(parts) == 0:
		cp := withParams(c, "username", id)
		controller.ListCollections(cp)
	case len(parts) == 1:
		cp := withParams(c, "username", id, "collection_name", parts[0])
		controller.GetCollection(cp)
	case len(parts) == 2 && parts[1] == "log":
		cp := withParams(c, "username", id, "collection_name", parts[0])
		controller.GetVersionLog(cp)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	}
}

// withParams 复制 gin context 并追加 URL 参数。
// 背景：gin 的 c.Param 只读注册时的参数名，分派器合并了不同语义的路由
// （:id → :hash/:username/:collection_name），用 Copy + 追加 Params 补齐，
// 让底层 controller 无需改动。Copy 会复制 Keys 与 ResponseWriter，handler 内串行使用安全。
func withParams(c *gin.Context, kv ...string) *gin.Context {
	cp := c.Copy()
	for i := 0; i+1 < len(kv); i += 2 {
		cp.Params = append(cp.Params, gin.Param{Key: kv[i], Value: kv[i+1]})
	}
	return cp
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
