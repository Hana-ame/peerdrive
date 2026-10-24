package main

import (
	"github.com/gin-gonic/gin"

	"peerdrive/internal/serverapp"
)

func main() {
	// 与 cmd/peerdrive（cli.Run）同一处约定：进程模式只在二进制入口设。
	// 之前这里漏了——cmd/server 起的服务一直跑在 gin debug 模式：
	// 重复路由 panic + 框架级逐请求日志，与 red-team R2 的收紧相悖。
	gin.SetMode(gin.ReleaseMode)

	serverapp.RunServe()
}
