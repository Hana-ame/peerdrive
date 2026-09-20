// Peersignal：自托管 PeerJS 信令服务器 + 内置房间发现。
// 替代公共云信令（0.peerjs.com）与公共 MQTT broker——节点端只需把
// PEERDRIVE_PEERJS_HOST/PORT 指向本服务器，发现走内置 HTTP API。
//
// 用法：peersignal [-addr :9000] [-key peerjs] [-tokens tok1,tok2] [-tls-cert c.pem -tls-key k.pem]
//
//	-tokens 可选：信令 token 白名单（逗号分隔）。设置后 WS 连接的 token
//	必须在名单内，否则拒绝升级（防止任意客户端冒充节点收信令）。
//	-tls-cert/-tls-key 可选：同时给定时以 HTTPS/WSS 提供服务。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Hana-ame/go-peersignal"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	key := flag.String("key", "peerjs", "API key (client 必须一致)")
	tokens := flag.String("tokens", "", "信令 token 白名单（逗号分隔；空 = 不限制）")
	tlsCert := flag.String("tls-cert", "", "TLS 证书（PEM）。与 -tls-key 一起给定时以 HTTPS/WSS 提供服务")
	tlsKey := flag.String("tls-key", "", "TLS 私钥（PEM）")
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
	mux.HandleFunc("/discover/leave", srv.HandleLeave)
	mux.HandleFunc("/discover/nodes", srv.HandleNodes)
	// 状态 API 与 dashboard（graph 可视化）
	mux.HandleFunc("/status", srv.HandleStatus)
	mux.HandleFunc("/", srv.HandleDashboard)

	if err := Serve(*addr, *tlsCert, *tlsKey, mux); err != nil {
		log.Fatal(err)
	}
}

// Serve 在 addr 上提供服务：certFile/keyFile 都给定时走 HTTPS/WSS，否则走 HTTP/WS。
//
// 为什么要支持 TLS：公共面板（packages/peerdrive-client/dist/panel.html）部署在
// GitHub Pages 上，Pages 强制 HTTPS。浏览器会把 HTTPS 页面发起的 ws:// 当作
// 混合内容直接拦掉（PeerJS 侧只表现为连不上，看不出原因），所以自托管信令
// 想被公共面板连，必须是 wss:// —— 要么本进程直接 TLS，要么前面挂反代。
func Serve(addr, certFile, keyFile string, h http.Handler) error {
	if certFile == "" && keyFile == "" {
		log.Printf("peerserver listening on %s (ws)", addr)
		return http.ListenAndServe(addr, h)
	}
	// 只给一半是典型的手误：静默降级回 http 的话，对面 HTTPS 页面会被混合内容
	// 拦截，而服务端看起来"正常启动了"，极难排查。宁可直接报错。
	if certFile == "" || keyFile == "" {
		return fmt.Errorf("TLS 需要同时指定 -tls-cert 和 -tls-key（当前 cert=%q key=%q）", certFile, keyFile)
	}
	log.Printf("peerserver listening on %s (wss)", addr)
	return http.ListenAndServeTLS(addr, certFile, keyFile, h)
}
