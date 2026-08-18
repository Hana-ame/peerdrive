# go-peerserver

自托管 PeerJS 信令服务器 + 内置房间发现（Go）。

替代公共云信令（0.peerjs.com）与公共 MQTT broker：节点端只需把
`PEERDRIVE_PEERJS_HOST/PORT`（或任意 PeerJS 客户端配置）指向本服务器，
发现走内置 HTTP API。PeerJS 协议兼容——任何 `peerjs` JS 客户端与
[go-peerjs](https://github.com/Hana-ame/go-peerjs) 客户端均可直连。

## 用法

```bash
go build -o peerserver ./cmd/peerserver/
./peerserver [-addr :9000] [-key peerjs] [-tokens tok1,tok2]
```

- `-addr` 监听地址（默认 `:9000`）
- `-key` PeerJS API key（客户端必须一致，防无关客户端接入）
- `-tokens` 可选：信令 token 白名单（逗号分隔）。设置后 WS 连接的
  token 必须在名单内，否则拒绝升级（防任意客户端冒充节点收信令）

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