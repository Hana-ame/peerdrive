package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	storageDir := "./storage"
	if s := os.Getenv("PEERDRIVE_STORAGE"); s != "" {
		storageDir = s
	}

	if err := repository.InitDB("./peerdrive.db"); err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}

	providerMgr := provider.NewManager(storageDir)
	downloader := service.NewDownloader(providerMgr)

	p2pSvc, err := service.NewP2PService(ctx)
	if err != nil {
		log.Fatalf("libp2p 节点启动失败: %v", err)
	}
	defer p2pSvc.Host.Close()
	id, addrs := p2pSvc.GetNodeInfo()
	log.Printf("libp2p 节点已启动: PeerID=%s, 监听地址=%v", id, addrs)

	r := router.SetupRouter(downloader, p2pSvc, storageDir)

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
