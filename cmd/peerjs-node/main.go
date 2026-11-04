// peerjs-node 让本机进程成为一个 PeerJS peer。
//
// 纯出站：不监听端口、不提供 http/ws 服务。启动后把 PeerServer 分配的
// 标识符打在日志里，浏览器侧照着连即可。
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/Hana-ame/peerdrive/peerjs"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	c, err := peerjs.Dial(context.Background(), peerjs.Config{})
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	defer c.Close()

	log.Printf("")
	log.Printf("=============== PEER ID ===============")
	log.Printf("  %s", c.ID())
	log.Printf("=======================================")
	log.Printf("把它填进页面即可连接；本进程不监听任何端口")
	log.Printf("")

	c.OnPeer = func(remote string, up bool) {
		if up {
			log.Printf("对端上线 · %s", remote)
		} else {
			log.Printf("对端离线 · %s", remote)
		}
	}
	c.OnMessage = func(remote, text string) {
		log.Printf("<< %s: %q", remote, text)
		reply := "echo: " + text
		if err := c.Send(remote, reply); err != nil {
			log.Printf("回复失败: %v", err)
			return
		}
		log.Printf(">> %s: %q", remote, reply)
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}
