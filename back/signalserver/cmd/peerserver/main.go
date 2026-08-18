// Peerserver：自托管 PeerJS 信令服务器 + 内置房间发现。
// 替代公共云信令（0.peerjs.com）与公共 MQTT broker——节点端只需把
// PEERDRIVE_PEERJS_HOST/PORT 指向本服务器，发现走内置 HTTP API。
//
// 用法：peerserver [-addr :9000] [-key peerjs] [-tokens tok1,tok2]
//   -tokens 可选：信令 token 白名单（逗号分隔）。设置后 WS 连接的 token
//   必须在名单内，否则拒绝升级（防止任意客户端冒充节点收信令）。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Hana-ame/go-peerserver"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	key := flag.String("key", "peerjs", "API key (client 必须一致)")
	tokens := flag.String("tokens", "", "信令 token 白名单（逗号分隔；空 = 不限制）")
	flag.Parse()

	var opts []signalserver.Option
	if *tokens != "" {
		opts = append(opts, signalserver.WithTokenWhitelist(strings.Split(*tokens, ",")))
	}
	srv := signalserver.NewServer(*key, opts...)
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
