// peerdrive-win7 —— Windows 7 专用最小入口。
//
// 为什么单独一个入口（而不是给 win 入口加个 flag）：
//
//  1. 工具链现实：Go 1.22+ 的运行时不再支持 Win7（官方最后支持到 1.21）。
//     这个文件连同它直达的 serverapp.RunServe 只用长期稳定的 API，
//     是为了「值得的时候」能用 Go ≤1.21 从本模块构一份跑在 Win7 上的二进制
//     （注意：当前 back/go.mod 的 go 指令是 1.26，老工具链构建前需先
//     `go mod edit -go=1.21`，见 doc/MODULE-MAP.md 的平台入口一节）。
//  2. 最小引用：老机器只要「起服务 + 面板」。托盘、自动更新、tun/mcp/embed/
//     demo 全部不进这个入口的 import 图——它只 import gin + serverapp，
//     与 cmd/server 同级的最小面，链接期不带 update/mcp/tun 等包。
//  3. 启动逻辑零复制：serve 直接调 serverapp.RunServe，与 `peerdrive serve`
//     和 cmd/server 是同一份实现（复制一份会迟早分叉）。
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/serverapp"
	"peerdrive/internal/version"
)

func main() {
	// 与 cli.Run 同一处约定：进程模式只在二进制入口设，router 包不设
	// （router 还被依赖 debug 模式的测试 import）。
	gin.SetMode(gin.ReleaseMode)

	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "serve":
		serverapp.RunServe()
	case "version", "--version", "-v":
		fmt.Printf("peerdrive %s (win7 minimal)\n", version.Version)
	case "help", "--help", "-h":
		fmt.Print(`peerdrive-win7 — Windows 7 最小入口（起服务 + 面板）

用法:
  peerdrive-win7 [serve]  起主服务（默认，环境变量与 peerdrive serve 完全一致）
  peerdrive-win7 version  打印版本
  peerdrive-win7 help     本帮助

需要托盘 / 自动更新 / tun / mcp 等完整特性，请用 Windows 10+ 的
peerdrive.exe（cmd/peerdrive-win 构建产物）。
`)
	default:
		fmt.Fprintf(os.Stderr, "peerdrive-win7: 仅支持 serve / version / help，收到 %q\n", cmd)
		os.Exit(2)
	}
}
