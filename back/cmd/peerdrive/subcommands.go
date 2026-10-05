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
	"syscall"

	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/regserver"
	"peerdrive/internal/serverapp"
	"peerdrive/internal/services"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// runSignal 起信令 + 节点发现，阻塞直到出错或收到退出信号。
//
// 变量名沿用旧 peersignal（PEERSIGNAL_ADDR 等），命令行参数名也沿用
// -addr/-key/-tokens/-tls-cert/-tls-key，便于 `peersignal` → `peerdrive signal`
// 平滑替换。
func runSignal(args []string) {
	fs := flag.NewFlagSet("signal", flag.ExitOnError)
	addr := fs.String("addr", envOr("PEERSIGNAL_ADDR", ":9000"), "listen address")
	key := fs.String("key", envOr("PEERSIGNAL_KEY", "peerjs"), "API key (client must match)")
	tokens := fs.String("tokens", os.Getenv("PEERJS_TOKENS"), "signaling token whitelist (comma-separated)")
	cert := fs.String("tls-cert", os.Getenv("PEERSIGNAL_TLS_CERT"), "TLS certificate (PEM); with -tls-key serves HTTPS/WSS")
	tlsKey := fs.String("tls-key", os.Getenv("PEERSIGNAL_TLS_KEY"), "TLS private key (PEM)")
	cors := fs.String("cors-origin", os.Getenv("PEERSIGNAL_CORS"), "CORS allow-list for panel-facing REST endpoints")
	_ = fs.Parse(args)

	h := services.SignalHandler(*key, *tokens, *cors)
	if err := services.ServeHTTP(*addr, *cert, *tlsKey, h); err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive signal: %v\n", err)
		os.Exit(1)
	}
}

// runReg 起注册/认证/中继登记，阻塞直到出错或收到退出信号。
func runReg(args []string) {
	fs := flag.NewFlagSet("reg", flag.ExitOnError)
	addr := fs.String("addr", envOr("PORT", ":4000"), "listen address")
	dbPath := fs.String("db", "", "sqlite path (default: $DB_PATH → $PEERDRIVE_REG_DB → ./reg.db)")
	cert := fs.String("tls-cert", os.Getenv("PEERDRIVE_REG_TLS_CERT"), "TLS certificate (PEM)")
	tlsKey := fs.String("tls-key", os.Getenv("PEERDRIVE_REG_TLS_KEY"), "TLS private key (PEM)")
	_ = fs.Parse(args)

	srv, err := regserver.New(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "peerdrive reg: %v\n", err)
		os.Exit(1)
	}
	defer srv.Close()

	log.LogInfo("reg: listening on %s", *addr)
	if err := srv.Serve(*addr, *cert, *tlsKey); err != nil {
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
