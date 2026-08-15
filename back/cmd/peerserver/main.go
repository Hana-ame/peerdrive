// Peerserver：自托管 PeerJS 信令服务器 + 内置房间发现。
// 替代公共云信令（0.peerjs.com）与公共 MQTT broker——节点端只需把
// PEERDRIVE_PEERJS_HOST/PORT 指向本服务器，发现走内置 HTTP API。
//
// 用法：peerserver [-addr :9000] [-key peerjs]
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"peerdrive/internal/signalserver"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	key := flag.String("key", "peerjs", "API key (client 必须一致)")
	flag.Parse()

	srv := signalserver.NewServer(*key)
	srv.Start() // 后台 sweeper：清理过期离线队列（H3）

	mux := http.NewServeMux()
	// PeerJS 兼容信令端点
	mux.HandleFunc("/peerjs", srv.HandleWS)
	mux.HandleFunc("/peerjs/id", srv.HandleID)
	// 内置房间发现（替代 MQTT）
	mux.HandleFunc("/discover/announce", srv.HandleAnnounce)
	mux.HandleFunc("/discover/nodes", srv.HandleNodes)

	log.Printf("peerserver listening on %s (key=%s)", *addr, *key)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
	_ = fmt.Sprint()
}
