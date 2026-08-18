// Peerdrive 服务端入口点。
// 启动 Gin HTTP 服务器，同时初始化 libp2p P2P 节点、SQLite 元数据库
// 和内容寻址文件存储。支持环境变量 PORT（监听端口）和
// PEERDRIVE_STORAGE（存储目录，默认 ./storage）。
// 使用方式：go run ./cmd/server/main.go
//   PORT=3000 PEERDRIVE_STORAGE=./storage go run ./cmd/server/main.go
// 内部流程：InitDB → NewP2PService → IPFSService → UniversalDownloader → SetupRouter
//
// storageDir 注入到 Gin Context，供 controller/anon.go 等使用。

package main

import (
	stdlog "log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	_ "peerdrive/docs"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/source"
	"peerdrive/internal/transport"
)

// @title Peerdrive API
// @version 1.0
// @description P2P file sharing with content-addressable storage, collection management, versioning, merge/fork/pull.
// @host localhost:3000
// @BasePath /

// main 是 Peerdrive 服务器入口，初始化 DB、P2P、HTTP 路由并监听端口。
func main() {
	log.LogInfo("main: Peerdrive server starting")

	cfg := config.Load()
	storageDir := cfg.StorageDir
	log.LogInfo("main: config loaded, storageDir=%s, port=%s", storageDir, cfg.Port)

	// 初始化 DB（含迁移）
	log.LogInfo("main: initializing database")
	if err := repository.InitDB("./peerdrive.db"); err != nil {
		stdlog.Fatalf("数据库初始化失败: %v", err)
	}
	log.LogInfo("main: database initialized")

	// 初始化匿名存储目录（与普通文件同一目录）
	repository.SetAnonStorageDir(storageDir)

	// 初始化 PeerJS 信令 + WebRTC 文件服务（Go 节点作为常驻 peer 提供文件，
	// 与浏览器/其它节点经公共云信令 0.peerjs.com 互联）。
	// 注意：SetPeerJSService 必须在 SetupRouter 之前调用，路由注册时读取。
	var peerjsSvc *transport.PeerJSService
	if cfg.PeerJSEnable {
		log.LogInfo("main: initializing PeerJS WebRTC service")
		peerjsSvc = transport.NewPeerJSService(cfg, storageDir)
		peerjsSvc.Start()
		defer peerjsSvc.Close()
		log.LogInfo("main: PeerJS node id=%s", peerjsSvc.ID())
	}

	// 设置路由（内部注入 storageDir/downloader 到 context）
	if cfg.RegistrationServer != "" {
		router.SetRegServer(cfg.RegistrationServer)
	}
	if peerjsSvc != nil {
		router.SetPeerJSService(peerjsSvc)
		router.SetPeerJSConfig(cfg)
		// 端口转发授权规则（forward v2）：PEERDRIVE_FORWARD_RULES="key:port,key2:port2"。
		// key 即凭证（服务端 HMAC 验证用原文）——配置为敏感文件，建议 chmod 600。
		if cfg.ForwardRules != "" {
			rules := map[string][]int{}
			for _, pair := range strings.Split(cfg.ForwardRules, ",") {
				kv := strings.SplitN(pair, ":", 2)
				if len(kv) != 2 || kv[0] == "" {
					log.LogWarn("main: ignore bad forward rule %q", pair)
					continue
				}
				port, err := strconv.Atoi(kv[1])
				if err != nil || port <= 0 || port > 65535 {
					log.LogWarn("main: ignore bad forward rule port %q", pair)
					continue
				}
				rules[kv[0]] = append(rules[kv[0]], port)
			}
			if len(rules) > 0 {
				peerjsSvc.SetForwardRules(rules)
				log.LogInfo("main: forward rules loaded (%d keys)", len(rules))
			}
		}
		// 统一 source 体系装配：本地磁盘（file_index + CAS）→ p2p 透传 → URL 源。
		// 路由语义：本地优先命中即返回，未命中降级 peer；URL 源经模板注册
		// （PEERDRIVE_URL_SOURCE_TEMPLATE），可为空。管理面 GET /sources。
		mgr := source.New()
		if err := mgr.Register(source.NewLocalSource(storageDir, peerjsSvc.FileIndex())); err != nil {
			log.LogWarn("main: register local source: %v", err)
		}
		if err := mgr.Register(source.NewPeerSource(peerjsSvc)); err != nil {
			log.LogWarn("main: register peer source: %v", err)
		}
		if cfg.URLSourceTemplate != "" {
			if err := mgr.Register(source.NewURLSource(cfg.URLSourceTemplate, nil)); err != nil {
				log.LogWarn("main: register url source: %v", err)
			}
		}
		router.SetSourceManager(mgr)
		// serveFile 多源路由（第 3 项优化 2026-08-18）：对端 req 未命中本地
		// 时回源对端/URL 模板（trace 防环见 dcReq.Trace）。HTTP 下载等根
		// 请求已走 mgr，这里复用同一实例保持路由顺序一致。
		peerjsSvc.SetFileRouter(mgr)
	}
	log.LogInfo("main: setting up HTTP router")
	r := router.SetupRouter(cfg)

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
