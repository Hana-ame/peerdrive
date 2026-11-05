// peerdrive 是 m/e2e 的节点进程。
//
// 两条通道分工：
//
//  1. PeerJS 数据通道（由 0.peerjs.com 撮合）：只搬 JSON 信令字符串。
//  2. 独立 WebRTC DataChannel：SDP 走第 1 条通道交换，通道上跑帧协议，
//     提供 collection 与按 sha256 的区间文件内容。
//
// 纯出站：不监听除本地页面之外的任何端口。
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Hana-ame/peerdrive/e2e/node"
	"github.com/Hana-ame/peerdrive/peerjs"
	"github.com/Hana-ame/peerdrive/webrtc"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)

	share := flag.String("share", "./shared", "要索引并对外提供 collection 的目录")
	db := flag.String("db", "./.peerdrive/metadata.db", "h2f SQLite 数据库路径")
	name := flag.String("name", "", "collection 名字，默认取 share 目录名")
	addr := flag.String("http", "127.0.0.1:8789", "浏览器页面监听地址；空串 = 不启动 HTTP")
	repo := flag.String("repo", ".", "仓库根目录（用于定位 frame.js / sha256.js / peerjs.min.js）")
	host := flag.String("peerjs-host", "", "PeerJS 信令主机，默认 0.peerjs.com")
	port := flag.Int("peerjs-port", 0, "PeerJS 信令端口，默认 443")
	key := flag.String("peerjs-key", "", "PeerJS key，默认 peerjs")
	flag.Parse()

	svc, err := node.Load(*share, *db, *name)
	if err != nil {
		logger.Fatalf("加载 collection 失败: %v", err)
	}
	defer svc.Close()

	if *addr != "" {
		handler, err := node.WebHandler(*repo)
		if err != nil {
			logger.Fatalf("%v", err)
		}
		go func() {
			logger.Printf("页面 · http://%s/", *addr)
			if err := http.ListenAndServe(*addr, handler); err != nil {
				logger.Printf("HTTP 退出: %v", err)
			}
		}()
	}

	client, err := peerjs.Dial(context.Background(), peerjs.Config{
		Host:   *host,
		Port:   *port,
		Key:    *key,
		Logger: logger,
	})
	if err != nil {
		logger.Fatalf("PeerJS 注册失败: %v", err)
	}
	defer client.Close()

	// New 而不是 Open：第一条 PeerJS 通道要保持字符串模式搬信令，不能被
	// webrtc 接管成帧通道。第二条连接由 Bridge 调 Accept 显式建立。
	srv, err := webrtc.New(client, webrtc.Config{
		Logger: logger,
		OnConn: func(c *webrtc.Conn) {
			node.HandleConn(c, svc, logger)
		},
	})
	if err != nil {
		logger.Fatalf("装配 webrtc 失败: %v", err)
	}
	defer srv.Close()

	bridge := &node.Bridge{Client: client, Server: srv, Log: logger}
	client.OnMessage = bridge.Handle

	coll := svc.Collection()
	logger.Printf("")
	logger.Printf("=============== PEER ID ===============")
	logger.Printf("  %s", srv.ID())
	logger.Printf("=======================================")
	logger.Printf("collection %q · %d 个文件 · %d 字节", coll.Name, coll.Count, coll.TotalBytes)
	for i, f := range coll.Files {
		if i >= 8 {
			logger.Printf("  … 还有 %d 个", coll.Count-8)
			break
		}
		logger.Printf("  %.12s  %9d  %s", f.SHA256, f.Size, f.Filename)
	}
	if *addr != "" {
		logger.Printf("打开 http://%s/ ，填上面的 PEER ID", *addr)
	}
	logger.Printf("")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	logger.Printf("收到退出信号，关闭中")
}
