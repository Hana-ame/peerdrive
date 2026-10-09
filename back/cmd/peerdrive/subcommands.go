package main

// serve / signal / reg / all 四个子命令的实现。
//
// 与 cmd/server/main.go 的关系：serve 直接调用那边导出的 RunServe()，
// 保证「peerdrive serve」与旧的 peerdrive-server 是**同一份**启动逻辑
// （复制一份会迟早分叉）。

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/Hana-ame/go-peerserver/tracker"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/regserver"
	"peerdrive/internal/serverapp"
	"peerdrive/internal/services"
)

// normalizePort 让 PORT 两种写法都能用。
//
// 旧 reg-server 收的是裸端口号（`PORT=4000` → `":" + port`），
// 而监听地址习惯写成 `HOST:PORT`（`:4000` / `127.0.0.1:4000`）。
// 两种都见过，所以都支持——但不能一律拼冒号，
// 否则 `HOST=127.0.0.1 PORT=4000` 会被拼成 `127.0.0.1:127.0.0.1:4000`。
// regAddrFromEnv 解析 reg 子命令的监听地址。
//
// ⚠️ PORT 沿用旧 reg-server 的写法：不带冒号（"4000"）是常态——旧实现是
// `addr := ":" + port`。若直接把 PORT 当监听地址传下去，`PORT=4000` 会报
// "address 4000: missing port in address"，等于把还能跑的旧部署脚本弄坏。
//
// 单独成函数而不是内联在 runReg 里，是为了让测试能调到**同一个**入口：
// 上一版测试直接调 normalizePort，摘掉 runReg 里的调用照样绿；
// 再一版调 regAddrFromEnv，可它有自己的实现，跟 runReg 那个 flag 无关，还是绿。
func regAddrFromEnv() string {
	return normalizePort(envOr("PORT", ":4000"))
}

// regFlagSet 构造 reg 子命令的 flag 定义。
//
// 抽出来是为了让测试能拿到**真实的那一份**：前几版的护栏都是「测辅助函数、
// 不测调用点」，把 runReg 改回 envOr("PORT", ":4000") 照样绿。
// 现在 runReg 与测试读同一个 FlagSet，摘掉 normalizePort 即失败。
func regFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("reg", flag.ContinueOnError)
	fs.String("addr", regAddrFromEnv(), "listen address")
	fs.String("db", "", "sqlite path (default: $DB_PATH → $PEERDRIVE_REG_DB → ./reg.db)")
	fs.String("tls-cert", os.Getenv("PEERDRIVE_REG_TLS_CERT"), "TLS certificate (PEM)")
	fs.String("tls-key", os.Getenv("PEERDRIVE_REG_TLS_KEY"), "TLS private key (PEM)")
	// BitTorrent HTTP tracker (BEP 12/31). When enabled, the reg server
	// also serves /announce, /scrape, and /tracker/bans. Ban management
	// uses JWT Bearer auth (same as /auth/whoami).
	fs.Bool("bt-tracker", false, "enable BitTorrent HTTP tracker (/announce, /scrape)")
	fs.String("bt-bans-file", "tracker_bans.json", "JSON file for tracker ban persistence")
	fs.Int("bt-interval", 900, "announce interval in seconds (default 900 = 15 min)")
	fs.Int("bt-max-peers", 100, "max peers per info_hash (default 100)")
	return fs
}

func normalizePort(p string) string {
	if p == "" {
		return ":4000"
	}
	if strings.Contains(p, ":") {
		return p // 已是 host:port 或 :port
	}
	if strings.Contains(p, ".") { // 纯 IP，没有端口
		return p + ":4000"
	}
	return ":" + p
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// mustInt parses a string to int, returning the default (0) on parse error.
// Used for tracker config flags that fall back to tracker.NewTracker defaults.
func mustInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// runSignal 起信令 + 节点发现，阻塞直到出错或收到退出信号。
//
// 变量名沿用旧 peersignal（PEERSIGNAL_ADDR 等），命令行参数名也沿用
// -addr/-key/-tokens/-tls-cert/-tls-key，便于 `peersignal` → `peerdrive signal`
// 平滑替换。
func runSignal(args []string) {
	cfg := config.Load()
	fs := flag.NewFlagSet("signal", flag.ExitOnError)
	addr := fs.String("addr", cfg.SignalAddr, "listen address")
	// 默认 key 必须与 `peerdrive all`（config.PeerJSKey，同源 DefaultSignalKey）
	// 以及独立 peersignal 一致——不一致时同一个二进制里 signal 与 all 连不上
	// 彼此，且默认配置的节点都连不上（C-8，默认值闸门见 test/deployconsistency）。
	key := fs.String("key", cfg.PeerJSKey, "API key (client must match)")
	tokens := fs.String("tokens", cfg.SignalTokens, "signaling token whitelist (comma-separated)")
	cert := fs.String("tls-cert", cfg.SignalTLSCert, "TLS certificate (PEM); with -tls-key serves HTTPS/WSS")
	tlsKey := fs.String("tls-key", cfg.SignalTLSKey, "TLS private key (PEM)")
	cors := fs.String("cors-origin", cfg.SignalCORS, "CORS allow-list for panel-facing REST endpoints")
	_ = fs.Parse(args)

	// Flag overrides applied to cfg so SignalHandler reads the final values.
	cfg.SignalAddr = *addr
	cfg.PeerJSKey = *key
	cfg.SignalTokens = *tokens
	cfg.SignalTLSCert = *cert
	cfg.SignalTLSKey = *tlsKey
	cfg.SignalCORS = *cors

	h := services.SignalHandler(cfg)
	if err := services.ServeHTTP(*addr, *cert, *tlsKey, h); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive signal: %v\n", err)
		os.Exit(1)
	}
}

// runReg 起注册/认证/中继登记，阻塞直到出错或收到退出信号。
func runReg(args []string) {
	fs := regFlagSet()
	addr := fs.Lookup("addr")
	dbPath := fs.Lookup("db")
	cert := fs.Lookup("tls-cert")
	tlsKey := fs.Lookup("tls-key")
	btTracker := fs.Lookup("bt-tracker")
	btBansFile := fs.Lookup("bt-bans-file")
	btInterval := fs.Lookup("bt-interval")
	btMaxPeers := fs.Lookup("bt-max-peers")
	_ = fs.Parse(args)

	srv, err := regserver.New(dbPath.DefValue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive reg: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	// BitTorrent HTTP tracker (BEP 12/31) — ban management uses JWT auth,
	// user-level bans map to regserver usernames via JWT verification.
	if btTracker.Value.String() == "true" {
		btOpts := []tracker.Option{
			tracker.WithAnnounceInterval(mustInt(btInterval.Value.String())),
			tracker.WithMaxPeers(mustInt(btMaxPeers.Value.String())),
		}
		if btBansFile.Value.String() != "" {
			btOpts = append(btOpts, tracker.WithBanFile(btBansFile.Value.String()))
		}
		tr := srv.SetupTracker(btOpts...)
		srv.SetTracker(tr)
		log.LogInfo("reg: bt-tracker enabled (interval=%d maxPeers=%d bansFile=%q)",
			mustInt(btInterval.Value.String()), mustInt(btMaxPeers.Value.String()), btBansFile.Value.String())
	}

	log.LogInfo("reg: listening on %s", addr.Value.String())
	if err := srv.Serve(addr.Value.String(), cert.Value.String(), tlsKey.Value.String()); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive reg: %v\n", err)
		os.Exit(1)
	}
}

// runAll 三者同进程起，共用一个端口。
//
// 这是「合成一个二进制」最直接的兑现方式：一个进程、一个端口、一套配置。
// 路由归属见 services.UnifiedMux 的注释。
//
// 端口：取主服务的 PEERDRIVE_HOST/PORT。信令与注册服务的旧端口变量
// （PEERSIGNAL_ADDR / PORT）在合并模式下**不生效**——同端口下它们没有意义，
// 留着会让人误以为还在分别监听。要分别监听请用 serve / signal / reg。
func runAll(args []string) {
	fs := flag.NewFlagSet("all", flag.ExitOnError)
	cert := fs.String("tls-cert", os.Getenv("PEERDRIVE_TLS_CERT"), "TLS certificate (PEM)")
	tlsKey := fs.String("tls-key", os.Getenv("PEERDRIVE_TLS_KEY"), "TLS private key (PEM)")
	_ = fs.Parse(args)

	cfg := config.Load()
	if err := config.Validate(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive all: %v\n", err)
		os.Exit(1)
	}

	ginH, shutdownMain, err := serverapp.BuildRouter(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive all: %v\n", err)
		os.Exit(1)
	}

	mux, reg, err := services.UnifiedMux(cfg, ginH)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive all: %v\n", err)
		os.Exit(1)
	}
	defer reg.Close()

	addr := serverapp.ListenAddr(cfg)
	log.LogInfo("all: unified on %s — 主服务 /、信令 /peerjs /discover/* /status、注册 /auth/* /p2p/* /ping /api/health", addr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if err := services.ServeHTTP(addr, *cert, *tlsKey, mux); err != nil {
			fmt.Fprintf(os.Stderr, "peerdrive all: %v\n", err)
		}
	}()

	<-quit
	log.LogInfo("all: shutting down")
	shutdownMain()
}
