// 健康检查控制器 — 返回 "pong" 确认服务运行中。
// Package controller 是所有 API 处理函数的包定义。每个 Controller 文件
// 通过独立的 Init* 函数（InitDownloader、InitP2PController、InitFileController）
// 注册各自依赖的服务实例。
// 本文件包含 Ping 函数：简单返回 200 + "pong" 文本，用于负载均衡健康检查。
// 路由：
//   GET /ping — 健康检查

package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Ping godoc
// @Summary Health check
// @Description Returns "pong" to confirm the server is running
// @Tags health
// @Produce plain
// @Success 200 {string} string "pong"
// @Router /ping [get]
func Ping(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}
