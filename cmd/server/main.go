// Peerdrive 服务端入口点。
// 启动 Gin HTTP 服务器，同时初始化 libp2p P2P 节点、SQLite 元数据库
// 和内容寻址文件存储。支持环境变量 PORT（监听端口）和
// PEERDRIVE_STORAGE（存储目录，默认 ./storage）。
// 使用方式：go run ./cmd/server/main.go
//   PORT=3000 PEERDRIVE_STORAGE=./storage go run ./cmd/server/main.go
// 内部流程：InitDB → NewP2PService → NewManager → NewDownloader → SetAnonStorageDir → SetupRouter
//
// storageDir 注入到 Gin Context，供 controller/anon.go 等使用。

package main

import (
	"context"
	stdlog "log"
	"os"
	"os/signal"
	"syscall"

	_ "peerdrive/docs"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"
)

// @title Peerdrive API
// @version 1.0
// @description P2P file sharing with content-addressable storage, collection management, versioning, merge/fork/pull.
// @host localhost:3000
// @BasePath /

// main 是 Peerdrive 服务器入口，初始化 DB、P2P、HTTP 路由并监听端口。
func main() {
	log.LogInfo("main: Peerdrive server starting")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := config.Load()
	storageDir := cfg.StorageDir
	log.LogInfo("main: config loaded, storageDir=%s, port=%s", storageDir, cfg.Port)

	// 初始化 DB（含迁移）
	log.LogInfo("main: initializing database")
	if err := repository.InitDB("./peerdrive.db"); err != nil {
		stdlog.Fatalf("数据库初始化失败: %v", err)
	}
	log.LogInfo("main: database initialized")

	// 初始化 P2P
	log.LogInfo("main: initializing P2P service")
	p2pSvc, err := service.NewP2PService(ctx, cfg)
	if err != nil {
		stdlog.Fatalf("libp2p 节点启动失败: %v", err)
	}
	defer p2pSvc.Close()
	id, addrs := p2pSvc.GetNodeInfo()
	if id != "" {
		log.LogInfo("main: libp2p node started, PeerID=%s, addrs=%v", id, addrs)
	}

	// 启动中继注册（如果配置了注册服务器 URL）
	if cfg.RegServerURL != "" && p2pSvc.IsEnabled() {
		registry := service.NewRelayRegistry(p2pSvc, cfg.RegServerURL, cfg.RelayStorageMB, cfg.RelayVersion)
		registry.Start()
	}

	// 初始化存储
	log.LogInfo("main: initializing provider manager and downloader")
	providerMgr := provider.NewManager(storageDir)
	downloader := service.NewDownloader(providerMgr, p2pSvc, storageDir)

	// 初始化匿名存储目录（与普通文件同一目录）
	repository.SetAnonStorageDir(storageDir)

	// 设置路由（内部注入 storageDir/downloader 到 context）
	if cfg.RegistrationServer != "" {
		router.SetRegServer(cfg.RegistrationServer)
	}
	log.LogInfo("main: setting up HTTP router")
	r := router.SetupRouter(downloader, p2pSvc, cfg)

	port := ":" + cfg.Port

	go func() {
		log.LogInfo("main: starting HTTP server on %s", port)
		if err := r.Run(port); err != nil {
			stdlog.Fatalf("Gin 服务器启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.LogInfo("main: shutting down server")
}
