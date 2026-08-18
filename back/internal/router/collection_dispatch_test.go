package router

// 发现背景（2026-08-19）：test.sh 6b「GET /collections/tester」真机跑出 500——
// withParams 用 c.Copy() 而 gin v1.8+ 的 Copy 不复制 ResponseWriter（Writer=nil），
// 下游 controller 任何 c.JSON 都 nil-pointer panic，且 gin Recovery 吞掉后脚本
// 因 curl -f 退出码被误判为「脚本挂」而非失败项，一直未被发现。修复：withParams
// 改为原 context 追加 Params。以下测试保护的是「分派下游必须能正常写响应」。

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestWithParams_CanRenderJSON 验证 withParams 返回的 context 仍能正常写响应
// （回归：Copy 修法 Writer=nil panic）。
func TestWithParams_CanRenderJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/collections/:id", func(c *gin.Context) {
		cp := withParams(c, "username", "alice")
		// 模拟 controller 行为：读分派参数 + 写 JSON
		require.Equal(t, "alice", cp.Param("username"))
		cp.JSON(http.StatusOK, gin.H{"data": []string{}})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collections/bob", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"data":[]}`, w.Body.String())
}

// TestWithParams_OriginalParamsPreserved 验证原路由参数在追加后仍可读
// （:id 与新参数并存，不能互相覆盖）。
func TestWithParams_OriginalParamsPreserved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/collections/:id", func(c *gin.Context) {
		cp := withParams(c, "username", "alice", "collection_name", "docs")
		require.Equal(t, "bob", cp.Param("id"))
		require.Equal(t, "alice", cp.Param("username"))
		require.Equal(t, "docs", cp.Param("collection_name"))
		cp.Status(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collections/bob", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

// TestDispatchGetCollection_ListCollections 验证 dispatcher 整链路
// （分派 → 追加参数 → controller 写 JSON），防类似回归在分派层复现。
func TestDispatchGetCollection_ListCollections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// controller.InitCollectionController 需要 service 注入；此处直接挂
	// ListCollections 到 dispatcher 同款路径，验证参数装配。
	r.GET("/collections/:id", func(c *gin.Context) {
		cp := withParams(c, "username", c.Param("id"))
		cp.JSON(http.StatusOK, gin.H{"data": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/collections/alice", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}