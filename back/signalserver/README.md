# go-peerserver

自托管 PeerJS 信令服务器 + 内置房间发现（Go）。

替代公共云信令（0.peerjs.com）与公共 MQTT broker：节点端只需把
`PEERDRIVE_PEERJS_HOST/PORT`（或任意 PeerJS 客户端配置）指向本服务器，
发现走内置 HTTP API。PeerJS 协议兼容——任何 `peerjs` JS 客户端与
[go-peerjs](https://github.com/Hana-ame/go-peerjs) 客户端均可直连。

## 用法

```bash
go build -o peerserver ./cmd/peerserver/
./peerserver [-addr :9000] [-key peerjs] [-tokens tok1,tok2] [-tls-cert c.pem -tls-key k.pem]
```

- `-addr` 监听地址（默认 `:9000`）
- `-key` PeerJS API key（客户端必须一致，防无关客户端接入）
- `-tokens` 可选：信令 token 白名单（逗号分隔）。设置后 WS 连接的
  token 必须在名单内，否则拒绝升级（防任意客户端冒充节点收信令）
- `-tls-cert` / `-tls-key`：PEM 证书与私钥。**成对给出时以 HTTPS/WSS 提供服务**，
  只给一个会直接报错退出（不静默降级——见下）

### 什么时候必须开 TLS（wss）

公共面板（`packages/peerdrive-client/dist/panel.html`，在线版跑在 GitHub Pages）是
HTTPS 页面，浏览器会把 HTTPS 页面发起的 `ws://` 当**混合内容**直接拦掉，而 PeerJS
侧只表现为"连不上"，没有任何提示。所以：

- 面板用 `localhost` 形式的 ws 信令：多数浏览器网开一面，能用；
- 面板要连局域网/公网上的自托管信令：**必须 wss**。

```bash
./peerserver -addr :9100 -tls-cert cert.pem -tls-key key.pem
# 或者不在本进程开 TLS，而是在前面挂 caddy / nginx / Cloudflare Tunnel 反代
```

REST 端点（`/peerjs/id`、`/discover/*`、`/status`）一律返回 `Access-Control-Allow-Origin: *`
并短路 OPTIONS 预检——公共面板在别的源上，跨域头是硬要求。

## 端点

| 路径 | 说明 |
|---|---|
| `GET /peerjs`（WS） | PeerJS 兼容信令（ice/offer/answer/leave/open） |
| `GET /peerjs/id` | 借 ID 轮换 + 过期回收（H3 队列） |
| `POST /discover/announce` | 节点上线自报（peerid → last-seen） |
| `GET /discover/nodes` | 房间发现列表（过期节点剔除） |

## 库用法（嵌入自建服务）

```go
srv := signalserver.NewServer("peerjs", signalserver.WithTokenWhitelist([]string{"tok"}))
srv.Start() // 后台 sweeper：清理过期离线队列
mux.HandleFunc("/peerjs", srv.HandleWS)
mux.HandleFunc("/discover/nodes", srv.HandleNodes)
```

## 测试

```bash
go test ./... -count=1
```

## 部署参考

- systemd + nginx 反代（`wss://` 到 WS 端点）见 peerdrive 主仓库 AGENTS.md；
- 线上实例：`wss://peersignal.moonchan.xyz/peerjs` + `https://peersignal.moonchan.xyz/discover/*`