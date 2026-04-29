# BT 模块

BitTorrent Mainline DHT + 完整 BT 下载客户端。

## 源码

| 文件 | 说明 |
|------|------|
| [p2p_bt/bt_dht.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/bt_dht.go) | Mainline Kademlia DHT 节点 (32+ nodes) |
| [p2p_bt/bep44.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/bep44.go) | BEP44 不可变/可变数据存储 |
| [p2p_bt/bt_client.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/p2p_bt/bt_client.go) | BT 下载客户端 (torrent/magnet) |
| [controller/p2p.go#L461-L1050](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L461) | BT HTTP handlers |

## 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/bt/status` | DHT 节点状态 |
| POST | `/bt/announce` | 宣告 hash 到 BT DHT |
| POST | `/bt/find` | 在 BT DHT 查找提供者 |
| POST | `/bt/bep44/put` | BEP44 存储数据 |
| POST | `/bt/bep44/get` | BEP44 读取数据 |
| POST | `/bt/dht/get` | DHT 直接查询 |
| GET | `/bt/bep51/sample` | BEP51 infohash 采样 |
| POST | `/bt/torrent` | 上传 .torrent 文件 |
| POST | `/bt/magnet` | 添加磁力链接 |
| GET | `/bt/downloads` | 列出下载任务 |
| GET | `/bt/download/:infohash` | 下载进度 |
| GET | `/bt/stats` | 全局统计 |

## 子文档

- [API-DESIGN.md](API-DESIGN.md) — BT API 设计
- [bt-dht-protocol.md](bt-dht-protocol.md) — BT DHT 协议
- [TEST-MATRIX.md](TEST-MATRIX.md) — 测试矩阵
