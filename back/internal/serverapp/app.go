// Peerdrive server entry point.
// Starts the Gin HTTP server, initializes the SQLite metadata database, content-addressable
// file storage, and PeerJS signaling + WebRTC file service (the Go node acts as a persistent
// peer providing files). Supports PORT (listen port) and PEERDRIVE_STORAGE (storage directory,
// default ./storage) environment variables.
// Usage (must use -tags nosqlite due to dual SQLite driver CGO symbol conflict):
//   go run -tags nosqlite ./cmd/server/main.go
//   PORT=3000 PEERDRIVE_STORAGE=./storage go run -tags nosqlite ./cmd/server/main.go
// Internal flow: InitDB → PeerJSService.Start → register local/peer/url source → NewRouter
//
// storageDir is injected into the Gin Context for use by controller/anon.go etc.
// Historical note: the original libp2p interconnection layer was entirely removed on
// 2026-08-16 (see doc/archive/LEGACY.md §A); this comment previously described the old
// "libp2p P2P node / NewP2PService→IPFSService→UniversalDownloader" flow, which is no
// longer accurate. Corrected here to reflect the current flow.

package serverapp

import (
	"context"
	"fmt"
	stdlog "log"
	"net/http"
	"os"

	_ "peerdrive/docs"
	"peerdrive/internal/config"
	"peerdrive/internal/httpd"
	"peerdrive/internal/log"
	"peerdrive/internal/update"
)

// @title Peerdrive API
// @version 1.0
// @description P2P file sharing with content-addressable storage, collection management, versioning, merge/fork/pull.
// @host localhost:3000
// @BasePath /

// Load 读配置并做启动期校验，返回 cfg 与一个清理函数。
//
// 为什么抽出来：`peerdrive all`（单二进制合并模式）需要先拿配置、再把三个
// 服务挂到同一个端口上，不能直接调用「读配置 + 监听」的完整流程。
func Load() (*config.Config, func(), error) {
	cfg := config.Load()
	if err := config.Validate(cfg); err != nil {
		return nil, nil, err
	}
	return cfg, func() {}, nil
}

// ListenAddr 返回主服务的监听地址（host 空 = 监听所有网卡）。
//
// 实现已搬到 internal/httpd；这里保留一个薄包装，因为 cmd/peerdrive 的
// runAll 也用它拼监听地址——两处拼法不同会让「合并模式」和「单独 serve」
// 在 IPv6 host 上产生不同的地址串（JoinHostPort 会加方括号）。
func ListenAddr(cfg *config.Config) string {
	return httpd.JoinHostPort(cfg.Host, cfg.Port)
}

// validateAuthStartup checks that a non-loopback deployment has an auth backend configured,
// or has explicitly opted out with PEERDRIVE_ALLOW_NO_AUTH=1.
//
// Why this check: AuthRequired passes through when no auth backend is configured
// (local single-machine mode). On a loopback-only deployment that's fine — the operator
// is the only one who can reach the port. But on a non-loopback deployment (e.g.
// PEERDRIVE_HOST=0.0.0.0), every authRequired route is wide open. This check forces an
// explicit opt-in for that dangerous configuration.
// validateAdminTokenStrength rejects a too-weak PEERDRIVE_ADMIN_TOKEN on a
// non-loopback deployment (red-team R2).
//
// Why it exists: AuthRequired compares the Bearer token with a constant-time
// compare, and the per-IP rate limiter caps a remote brute-force at
// PEERDRIVE_RATE_LIMIT_RPS (default 30). That is a fine defence for a
// high-entropy token and a meaningless one for a short one — "123456" is
// exhausted in well under a second of single-connection traffic, and 30 rps of
// attempts is still a comfortable pace for a script. The limiter cannot rescue a
// low-entropy secret, so the check moves to startup, where a misconfiguration
// fails fast instead of shipping an open admin surface.
//
// Opt-out: PEERDRIVE_ALLOW_WEAK_ADMIN_TOKEN=1 (the local/single-user deployments
// that intentionally use a short token still work; loopback hosts are exempt
// because the caller cannot brute-force them remotely).
func validateAdminTokenStrength(token, host string) error {
	if httpd.IsLoopback(host) {
		return nil
	}
	if os.Getenv("PEERDRIVE_ALLOW_WEAK_ADMIN_TOKEN") == "1" {
		log.LogWarn("main: PEERDRIVE_ADMIN_TOKEN is short on a non-loopback bind (%s); PEERDRIVE_ALLOW_WEAK_ADMIN_TOKEN=1 set, continuing", host)
		return nil
	}
	if weak, why := isWeakSecret(token); weak {
		return fmt.Errorf("refusing to serve host %q: PEERDRIVE_ADMIN_TOKEN is too weak (%s); set a token of at least 16 chars with at least 9 distinct chars, or PEERDRIVE_ALLOW_WEAK_ADMIN_TOKEN=1 to override", host, why)
	}
	return nil
}

// isWeakSecret reports whether a secret looks brute-forceable. Deliberately simple:
// character count plus distinct-symbol count catches the realistic weak cases
// ("123456", "password", "abcd1234") without a dictionary or shannon check.
func isWeakSecret(s string) (bool, string) {
	if len(s) < 16 {
		return true, fmt.Sprintf("only %d chars", len(s))
	}
	distinct := map[rune]struct{}{}
	for _, r := range s {
		distinct[r] = struct{}{}
	}
	if len(distinct) < 9 {
		return true, fmt.Sprintf("only %d distinct chars", len(distinct))
	}
	return false, ""
}

// ValidateAuthStartup checks that a non-loopback deployment has an auth backend configured,
// or has explicitly opted out with PEERDRIVE_ALLOW_NO_AUTH=1.
// Exported so `peerdrive all` and `peerdrive serve` share the exact same startup security gate.
func ValidateAuthStartup(cfg *config.Config) error {
	return validateAuthStartup(cfg)
}

func validateAuthStartup(cfg *config.Config) error {
	addr := ListenAddr(cfg)
	if httpd.IsLoopback(cfg.Host) {
		return nil // loopback only — safe
	}
	if cfg.RegistrationServer != "" {
		return nil // remote auth backend configured — safe
	}
	if cfg.AdminToken != "" {
		if err := validateAdminTokenStrength(cfg.AdminToken, cfg.Host); err != nil {
			return err
		}
		return nil // local admin token configured — safe (C-14 follow-up: login service off, HTTP surface still gated)
	}
	if os.Getenv("PEERDRIVE_ALLOW_NO_AUTH") == "1" {
		log.LogWarn("main: non-loopback with no auth backend; PEERDRIVE_ALLOW_NO_AUTH=1 set, continuing")
		return nil
	}
	return fmt.Errorf("refusing to serve %s (non-loopback) with no auth backend; set PEERDRIVE_REG_SERVER, PEERDRIVE_ADMIN_TOKEN, or PEERDRIVE_ALLOW_NO_AUTH=1 to override", addr)
}

// ReadyInfo 是 serve 起来之后交给「平台外壳」的信息包。
//
// 目前唯一的外壳是 Windows 系统托盘（internal/tray，由 cmd/peerdrive-win
// 经 cli.Options.Tray 接入）：它需要知道监听地址和浏览器该打开的 URL。
// 放在这里而不是让外壳自己拼：URL 的 host 折叠规则（监听在 0.0.0.0 时
// 浏览器要开 127.0.0.1）与启动横幅同一份实现，见 BrowserPanelURL。
type ReadyInfo struct {
	// Addr 是实际监听地址（host:port），原样给托盘提示用。
	Addr string
	// PanelURL 是浏览器打开内嵌面板的地址（http://127.0.0.1:3000/panel）。
	PanelURL string
	// DriveURL 是网盘视图地址（面板根路径）。
	DriveURL string
	// NodeID 是本节点 ID，托盘提示里带上便于多开时分辨。
	NodeID string
}

// RunServe is the Peerdrive server entry point; it initializes the DB, P2P, HTTP router and listens on the port.
func RunServe() {
	runServe(nil)
}

// RunServeOnReady 与 RunServe 同一份启动流程，差别只在 HTTP 起来之后：
// 主 goroutine 交给 onReady（Windows 托盘消息循环），onReady 返回即关停退出。
//
// 为什么钩子放在这里而不是让调用方起 goroutine 自己包：serve 的装配
// （配置校验 → BuildRouterWithInfo → 启动横幅 → StartupCheck → Listen）
// 是一整块，复制到外壳里迟早分叉；而「起来之后干什么」恰恰是平台差异，
// 用钩子分开，两边都只有一份。
//
// 约束：onReady 返回后本函数会 shutdown 并 os.Exit(0)——托盘「退出」的语义
// 就是结束进程，HTTP 服务 goroutine 随之终止。正常前台运行请用 RunServe。
func RunServeOnReady(onReady func(ReadyInfo) error) {
	runServe(onReady)
}

func runServe(onReady func(ReadyInfo) error) {
	log.LogInfo("main: Peerdrive server starting")

	cfg := config.Load()
	// Startup-time validation: misconfigured environment variables won't crash the process,
	// they just cause subtle misbehavior (files written to the wrong location, port not
	// binding), and by the time you notice it's too late. Here we fail fast with a clear
	// message (see config.Validate for details).
	if err := config.Validate(cfg); err != nil {
		stdlog.Fatalf("%v", err)
	}
	// C-14: non-loopback without auth backend is a security risk; require explicit opt-in.
	if err := validateAuthStartup(cfg); err != nil {
		stdlog.Fatalf("%v", err)
	}
	// 启动时把当前认证模式打出来——运营者需要一眼知道这门是开是关、靠什么开。
	// 三种模式：远端注册服务 / 本地 AdminToken / 完全关闭（仅回环安全）。
	switch {
	case cfg.RegistrationServer != "":
		log.LogInfo("main: auth mode = registration server (%s)", cfg.RegistrationServer)
	case cfg.AdminToken != "":
		log.LogInfo("main: auth mode = local admin token (PEERDRIVE_ADMIN_TOKEN)")
	default:
		log.LogInfo("main: auth mode = disabled (no auth backend)")
	}
	r, shutdown, info, err := BuildRouterWithInfo(cfg)
	if err != nil {
		stdlog.Fatalf("%v", err)
	}
	defer shutdown()
	log.LogInfo("main: setting up HTTP router")

	// 启动指引要在 RunHTTP 阻塞之前打出来——那是用户第一次也是唯一一次
	// 需要知道「接下来做什么」的时刻（见 nextstep.go 的说明）。
	PrintNextStep(NextStepInfo{
		NodeID:  info.NodeID,
		Host:    cfg.Host,
		Port:    cfg.Port,
		Storage: cfg.StorageDir,
	}, panelFilePath())

	// 可选自动更新检查（PEERDRIVE_AUTO_UPDATE，默认关 = 零网络请求）：
	// 打开时后台异步检查最新版本并提示，不替换运行中的二进制（替换走
	// `peerdrive update`，见 internal/update 的包注释）。
	update.StartupCheck(cfg)

	addr := ListenAddr(cfg)
	if onReady == nil {
		RunHTTP(addr, r)
		return
	}
	// 平台外壳路径：HTTP 进 goroutine，主 goroutine 交给外壳。
	// RunHTTP 自己不返回错误（内部 Fatalf / 打日志），无需 errCh。
	go func() { RunHTTP(addr, r) }()
	if err := onReady(ReadyInfo{
		Addr:     addr,
		PanelURL: BrowserPanelURL(cfg.Host, cfg.Port, false),
		DriveURL: BrowserBaseURL(cfg.Host, cfg.Port, false) + "/",
		NodeID:   info.NodeID,
	}); err != nil {
		log.LogWarn("main: platform shell exited with error: %v", err)
	}
	log.LogInfo("main: platform shell requested quit — shutting down")
	// 显式 shutdown 后直接退出：defer 的 shutdown 在 os.Exit 时不会跑，
	// 而 HTTP goroutine 此时还阻塞在 Serve 上，靠进程退出终止。
	shutdown()
	os.Exit(0)
}

// BuildRouter 初始化 DB、PeerJS 服务、source manager 并返回 HTTP handler。
//
// 抽出来是为了 `peerdrive all`：它要把主服务挂进一个更大的 mux（与信令、
// 注册服务共端口），不能走「读配置 + 自己 ListenAndServe」的完整流程。
// 返回的 shutdown 负责关闭 DB 与 PeerJS 服务——调用方必须调用，否则
// Windows 上 .db 文件删不掉，且进行中的大文件传输会被截断。
func BuildRouter(cfg *config.Config) (http.Handler, func(), error) {
	h, shutdown, _, err := buildRouter(cfg)
	if err != nil {
		return nil, nil, err
	}
	return h, shutdown, nil
}

// BuildRouterWithInfo 与 BuildRouter 相同，额外带回启动指引需要的节点信息
// （当前只有 nodeID）。单独提供是为了不改动既有调用方——
// cmd/peerdrive 的 all 子命令与既有测试都仍在用 BuildRouter。
func BuildRouterWithInfo(cfg *config.Config) (http.Handler, func(), RouterInfo, error) {
	return buildRouter(cfg)
}

// RouterInfo 是启动指引要用的运行时信息。
type RouterInfo struct {
	NodeID string // PeerJS 节点 ID（未启用 PeerJS 时为空）
}

type builtRouter struct {
	handler  http.Handler
	shutdown func()
	info     RouterInfo
}

// RunHTTP 在 addr 上监听并阻塞，直到出错或收到 SIGINT/SIGTERM，然后优雅退出。
//
// 为什么不用 r.Run()：
//  1. r.Run() 内部调 Fatalf，出错直接 os.Exit —— main 里所有 defer 都跳过，
//     PeerJS 连接与 DB 句柄只能靠进程退出清理（Windows 上 DB 文件句柄不关，
//     下次就打不开了）；
//  2. 没有优雅退出：收到 SIGTERM 直接退，进行中的大文件传输被截断成半个文件——
//     对端拿到的是损坏的残片，却以为传输成功了。
//
// ReadHeaderTimeout 是最低限度的 Slowloris 防护（gin 的 r.Run() 不设这个）。
func RunHTTP(addr string, h http.Handler) {
	// Listen address: PEERDRIVE_HOST empty = listen on all interfaces (historical behavior).
	// The admin surface has no account system; "who can reach this port" is its only boundary.
	// If the admin panel is only used locally, setting PEERDRIVE_HOST=127.0.0.1 is the
	// cheapest wall.
	srv, err := httpd.New(httpd.Config{
		Addr: addr,
		// ReadHeaderTimeout 是最低限度的 Slowloris 防护（gin 的 r.Run() 不设这个）。
		ReadHeaderTimeout: httpd.DefaultReadHeaderTimeout,
		// LogPrefix 保持 "main"：这些日志是运维判断「主服务起没起来、怎么退的」
		// 的，改前缀会让 grep "main:" 之类的检索语句全部失效。
		LogPrefix: "main",
	}, h)
	if err != nil {
		// 只能来自空 addr 或 nil handler，属于装配 bug 而非配置问题：直接 Fatal。
		stdlog.Fatalf("main: build HTTP server: %v", err)
	}
	// Run 返回的 error 只可能来自 ListenAndServe 失败，而 httpd.Serve 已经打过
	// 「main: HTTP server failed: %v」；重复打只会让退出日志看起来像两次故障。
	_ = srv.Run(context.Background())
	log.LogInfo("main: stopped")
}
