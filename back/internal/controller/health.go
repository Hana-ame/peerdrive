// 健康检查控制器：存活探针 /health 与就绪探针 /ready。
//
// 为什么不是只留 /ping：探针有两种，语义不同，混用会出两种事故——
//   liveness（/health）：进程还在不在。失败就该重启容器。所以它**不查任何
//     依赖**，否则数据库抖一下就把健康的服务杀掉，然后重启，然后又抖……（重启风暴）
//   readiness（/ready）：能不能接客。失败就把流量摘走，等它自己恢复。
//     所以它必须真的去问依赖（这里是元数据库）。
// /ping 保留：历史端点，负载均衡器和老脚本还在用。

package controller

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// startedAt 进程启动时刻，用于 /health 返回 uptime（容器编排看它判断有没有被反复重启）。
var startedAt = time.Now()

// dbPing 由装配层（router.SetupRouter）注入的"数据库还活着吗"探针。
//
// 为什么不直接 import repository：controller 层不碰持久化是实现层的硬约定
// （服务层 → 仓储层，controller 只见服务）。探针只是想知道"库能不能连"，
// 为它破一次例，后面就会有第二次。注入一个 func() error 就够了。
var dbPing func() error

// InitHealth 装配依赖（router 里调用，与 InitFileController 等同一套路）。
func InitHealth(ping func() error) {
	dbPing = ping
}

// Health godoc
// @Summary Liveness probe
// @Description 进程存活探针，不检查任何依赖
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /health [get]
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"uptime_sec": int64(time.Since(startedAt).Seconds()),
	})
}

// Ready godoc
// @Summary Readiness probe
// @Description 就绪探针：元数据库可连通才算就绪
// @Tags health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 503 {object} map[string]interface{}
// @Router /ready [get]
func Ready(c *gin.Context) {
	if dbPing == nil {
		// 没装配就当没就绪：宁可让探针红着，也不要假装一切正常——
		// 一个永远 200 的 readiness 比没有 readiness 更危险（它骗过了编排系统）。
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "health check not wired"})
		return
	}
	// Ping 会真的取一条连接执行，数据库文件被删/权限丢失/句柄耗尽时这里才会红。
	// 超时不另设：database/sql 的 Ping 走 context，本探针由调用方（编排系统）控超时。
	if err := dbPing(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": "database ping failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
