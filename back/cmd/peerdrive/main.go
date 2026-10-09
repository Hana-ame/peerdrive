// peerdrive 单二进制入口（v0.2.0 起）。
//
// 为什么从「三个二进制」改成「一个」：原先 peerdrive-server / peersignal /
// peerdrive-reg-server 各自发布，版本靠同一个 tag 对齐，但下载、升级、部署
// 都要操三份——而这三者本来就是一套系统的三个部件（主服务、它的信令与发现、
// 它的注册认证）。go-peerjs（PeerJS 客户端库）本来就已经链接进 peerdrive-server
// （见 internal/transport/conn.go 的 import），无需额外处理。
//
// 子命令：
//
//	peerdrive                 起主服务（等价于旧的 peerdrive-server，无参数时默认）
//	peerdrive serve           同上，显式写法
//	peerdrive signal          只起信令 + 节点发现（等价于旧的 peersignal）
//	peerdrive reg             只起注册/认证/中继登记（等价于旧的 peerdrive-reg-server）
//	peerdrive all             三者起在同一个进程、同一个端口（端口取主服务的）
//	peerdrive version         打印版本
//
// 向后兼容：旧的无子命令调用（`peerdrive-server` 直接跑）与全部既有环境变量
// 保持原样可用——部署脚本不需要改。
//
// 关于 `all`：默认三者各跑各的端口（主服务 PORT、信令 PEERJS_PORT、
// 注册服务 PORT）——旧部署脚本不用改。`peerdrive all` 则把它们合并到**一个进程、
// 一个端口**，端口取主服务的 PEERDRIVE_PORT：
//
//	主服务   /health /sources /swagger …（兜底）
//	信令     /peerjs /discover/* /status，信令面板在 /_signal
//	注册服务 /auth/* /p2p/* /api/health；与主服务撞名的 /ping 改挂在 /_reg/ping
//
// 撞名的 /ping 是实测踩到的：主服务 router.go:234 也有 GET /ping（返回 pong），
// 谁先被匹配谁生效——不处理的话主服务的 /ping 会静默变成另一个响应体。
// 处理方式见 internal/services/services.go 的 prefixConflicts。

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"peerdrive/internal/log"
	"peerdrive/internal/serverapp"
	"peerdrive/internal/version"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "serve":
		serverapp.RunServe()
	case "signal":
		runSignal(args)
	case "reg":
		runReg(args)
	case "all":
		runAll(args)
	case "hub":
		runHub(args)
	case "demo":
		os.Exit(runDemo(args))
	case "mcp":
		runMCP(args)
	case "version", "--version", "-v":
		fmt.Printf("peerdrive %s\n", version.Version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "peerdrive: unknown subcommand %q\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `peerdrive — 单二进制：主服务 + 信令 + 注册认证 + MCP

用法:
  peerdrive demo         一条命令跑通全链路（信令+两节点+跨节点传文件+校验）
  peerdrive [serve]      起主服务（默认）
  peerdrive signal       只起信令 + 节点发现
  peerdrive reg          只起注册 / 认证 / 中继登记
  peerdrive all          三者同进程、同端口起（端口=主服务的 PEERDRIVE_PORT）
  peerdrive mcp          启动 MCP (Model Context Protocol) stdio 交互服务
  peerdrive version      打印版本
  peerdrive help         本帮助

第一次用，先跑 demo：它不需要任何配置，会把「节点之间怎么传文件」
跑一遍给你看。看完直接 peerdrive serve，浏览器打开它打印的面板地址即可。

子命令之间互不影响：信号与注册服务的旧环境变量全部照旧生效
（PEERJS_PORT / JWT_SECRET / DB_PATH 等），旧的部署脚本不需要改。
`)
}

// resolveDBPath 让注册服务复用主服务已经算好的存储目录，避免
// PEERDRIVE_STORAGE 与 PEERDRIVE_REG_DB/DB_PATH 各指一处、把数据写散。
func resolveDBPath(storageDir string) string {
	if p := os.Getenv("PEERDRIVE_REG_DB"); p != "" {
		return p
	}
	if p := os.Getenv("DB_PATH"); p != "" {
		log.LogWarn("main: DB_PATH is deprecated for reg.db, use PEERDRIVE_REG_DB instead")
		return p
	}
	if storageDir != "" {
		return filepath.Join(storageDir, "reg.db")
	}
	return "./reg.db"
}
