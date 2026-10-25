// peerdrive 单二进制入口的调度核心（v0.2.0 起）。
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
//	peerdrive check-update    检查是否有新版本（0=最新 / 1=检查失败 / 3=有新版本）
//	peerdrive update          下载新版本并原子替换（sha256 校验、旧版留 .bak、失败回滚）
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
//
// 平台入口（cmd/ 下三个 main 共用本包，不复制任何调度逻辑）：
//
//	cmd/peerdrive       默认入口（linux/darwin/windows 通用）：无托盘
//	cmd/peerdrive-win    Windows 入口：serve/all 起系统托盘（Options.Tray），
//	                     stock Go 构建，目标 Win10/11 amd64
//	cmd/peerdrive-win7   Windows 7 入口：同为托盘入口，用 XTLS/go-win7 补丁
//	                     SDK 构建（官方 Go 1.21.5+ 在 Win7 启动即崩），
//	                     386 为主 + amd64，产物兼容 Win7~11
//	                     （scripts/build-win7.sh）
//
// 托盘只在 Windows 编译进二进制（internal/tray 的 build tag），其他平台
// Available() 恒假，链接期不带任何 Win32 代码。

package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/serverapp"
	"peerdrive/internal/tray"
	"peerdrive/internal/update"
	"peerdrive/internal/version"
)

// Options 是平台入口传给调度核心的差异项。
type Options struct {
	// Tray 让 serve/all 在起来之后挂一个系统托盘（打开面板/网盘、检查更新、退出）。
	// 仅 Windows 有效：其他平台 tray.Available() 恒假，行为与 Tray=false 无异。
	// 运行期可用 PEERDRIVE_TRAY=0 强制关闭（服务化、CI、远程桌面等无托盘场景）。
	Tray bool
}

// trayEnabled 把「平台支持 + 入口请求 + 运行期开关」三个条件合到一处。
// 单独成函数而不是散着 if，是为了保证三个条件的判定只有一份——
// serve 和 all 两条路径都走它，不会出现「一边记得查 env、另一边忘了」。
func (o Options) trayEnabled() bool {
	return o.Tray && tray.Available() && os.Getenv("PEERDRIVE_TRAY") != "0"
}

// Run 执行一次完整的命令行分派，返回进程退出码。
//
// 各平台入口只做一件事：os.Exit(cli.Run(os.Args[1:], cli.Options{…}))。
// gin 的生产模式在这里统一设置——bin entry point 设进程模式、router 包不设
// （router 还被依赖 debug 模式的测试引用，见原 cmd/peerdrive/main.go 的注释）。
//
// 注意：serve/all 在带托盘且托盘可用时不会返回——托盘循环跑在主 goroutine 上，
// 用户从托盘退出时进程直接结束（serverapp.RunServeOnReady 的约定）。
func Run(args []string, opts Options) int {
	gin.SetMode(gin.ReleaseMode)

	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd = args[0]
		args = args[1:]
	}

	switch cmd {
	case "serve":
		if opts.trayEnabled() {
			serverapp.RunServeOnReady(func(ready serverapp.ReadyInfo) error {
				return tray.Run(trayConfig(ready))
			})
			return 0 // 不可达：RunServeOnReady 在用户从托盘退出时结束进程
		}
		serverapp.RunServe()
	case "signal":
		runSignal(args)
	case "reg":
		runReg(args)
	case "all":
		runAll(args, opts)
	case "hub":
		runHub(args)
	case "demo":
		return runDemo(args)
	case "mcp":
		runMCP(args)
	case "tun", "fwd":
		runTun(args)
	case "check-update":
		return runCheckUpdate()
	case "update":
		return runUpdate()
	case "embed":
		return runEmbed(args)
	case "version", "--version", "-v":
		fmt.Printf("peerdrive %s\n", version.Version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "peerdrive: unknown subcommand %q\n\n", cmd)
		usage()
		return 2
	}
	return 0
}

// trayConfig 把 serve/all 的就绪信息翻译成托盘配置。
// 托盘包本身不认识 peerdrive——菜单项、URL、更新检查全在这里装配，
// internal/tray 只提供「图标 + 菜单 + 消息循环」这三块机械能力。
func trayConfig(ready serverapp.ReadyInfo) tray.Config {
	return tray.Config{
		Tooltip: "Peerdrive · " + ready.Addr,
		OnOpen:  func() { openBrowser(ready.PanelURL) },
		Items: []tray.MenuItem{
			{Label: "打开面板", Action: func() { openBrowser(ready.PanelURL) }},
			{Label: "打开网盘", Action: func() { openBrowser(ready.DriveURL) }},
			{Separator: true},
			{Label: "检查更新", Action: checkUpdateFromTray},
			{Separator: true},
			{Label: "退出", Action: tray.Quit},
		},
	}
}

// checkUpdateFromTray 托盘菜单的「检查更新」。
// 走与 `peerdrive check-update` 同一个 internal/update 入口，结果用气球提示：
// 检查是阻塞 HTTP（最长 60s），必须放到 goroutine 里，否则托盘消息循环被堵住，
// 整个托盘在检查期间「假死」——点了没反应正是循环被占的表现。
func checkUpdateFromTray() {
	go func() {
		cfg := config.Load()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := update.Check(ctx, version.Version, cfg.UpdateURL)
		switch {
		case err != nil:
			tray.Balloon("检查更新失败", err.Error())
		case res.HasUpdate:
			tray.Balloon("发现新版本 "+res.Latest, "运行 peerdrive update 升级到 "+res.Latest)
		default:
			tray.Balloon("已是最新版本", "当前 "+version.Version)
		}
	}()
}

// openBrowser 用系统默认浏览器打开一个 URL。
// 跨平台实现放在这里而不是 internal/tray 里：托盘是 Windows-only 的，
// 而 rundll32/xdg-open/open 的分发在任何平台都编得过（只会在托盘动作里被调到）。
func openBrowser(rawURL string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// rundll32 + url.dll,FileProtocolHandler 是 Win7 起就有的打开方式；
		// `cmd /c start` 在带空格的 URL 和部分本地化系统上会解析错。
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		log.LogWarn("cli: open browser: %v", err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `peerdrive — 单二进制：主服务 + 信令 + 注册认证 + MCP + 端口穿透 (p2ptun)

用法:
  peerdrive demo         一条命令跑通全链路（信令+两节点+跨节点传文件+校验）
  peerdrive [serve]      起主服务（默认）
  peerdrive signal       只起信令 + 节点发现
  peerdrive reg          只起注册 / 认证 / 中继登记
  peerdrive all          三者同进程、同端口起（端口=主服务的 PEERDRIVE_PORT）
  peerdrive tun          基于 PeerJS 互联建立端口转发与内网穿透（参考 p2ptun）
  peerdrive mcp          启动 MCP (Model Context Protocol) stdio 交互服务
  peerdrive check-update 检查是否有新版本（0=最新 / 1=检查失败 / 3=有新版本）
  peerdrive update       下载新版本并原子替换（校验 sha256、旧版留 .bak、失败回滚）
  peerdrive embed        对独立 EmbeddingGemma 服务做一次 embedding（--ping 探测）
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
