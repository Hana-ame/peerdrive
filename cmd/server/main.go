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
	"log"
	"os"
	"os/signal"
	"syscall"

	_ "peerdrive/docs"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// @title Peerdrive API
// @version 1.0
// @description P2P file sharing with content-addressable storage, collection management, versioning, merge/fork/pull.
// @host localhost:3000
// @BasePath /

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storageDir := "./storage"
	if s := os.Getenv("PEERDRIVE_STORAGE"); s != "" {
		storageDir = s
	}

	// 初始化 DB（含迁移）
	if err := repository.InitDB("./peerdrive.db"); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}

	// 初始化 P2P
	p2pSvc, err := service.NewP2PService(ctx)
	if err != nil {
		log.Fatalf("libp2p 节点启动失败: %v", err)
	}
	defer p2pSvc.Host.Close()
	id, addrs := p2pSvc.GetNodeInfo()
	log.Printf("libp2p 节点已启动: PeerID=%s, 监听地址=%v", id, addrs)

	// 初始化存储
	providerMgr := provider.NewManager(storageDir)
	downloader := service.NewDownloader(providerMgr, p2pSvc, storageDir)

	// 初始化 Auth
	userRepo := repository.NewUserRepository()
	authSvc := service.NewAuthService(userRepo)

	// 初始化匿名存储目录（与普通文件同一目录）
	repository.SetAnonStorageDir(storageDir)

	// 设置路由
	r := router.SetupRouter(downloader, p2pSvc, authSvc, storageDir)

	// 注入到 Gin Context
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", storageDir)
		c.Set("downloader", downloader)
		c.Next()
	})

	port := ":3000"
	if p := os.Getenv("PORT"); p != "" {
		port = ":" + p
	}

	go func() {
		if err := r.Run(port); err != nil {
			log.Fatalf("Gin 服务器启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务器...")
}
