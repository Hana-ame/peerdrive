# P2P 模块

libp2p 主机、DHT、mDNS 发现、Relay、端口转发、双栈宣告、WebSocket 传输。

## 源码

| 文件 | 说明 |
|------|------|
| [service/p2p.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p.go) | libp2p 主机 + DHT + 流处理 (~904行) |
| [service/p2p_connection.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_connection.go) | 连接管理 |
| [service/p2p_transfer.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_transfer.go) | 文件传输协议 |
| [service/p2p_ws.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_ws.go) | WebSocket 传输 |
| [service/p2p_helpers.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_helpers.go) | 辅助函数 |
| [service/p2p_resume.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_resume.go) | 断点续传 |
| [service/p2p_multipeer.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_multipeer.go) | 多源并行下载 |
| [service/p2p_dual.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/p2p_dual.go) | IPFS+BT 双栈 |
| [service/forward.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/forward.go) | 端口转发 |
| [service/peer_tracker.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/peer_tracker.go) | 对等节点追踪 |
| [service/peer_scanner.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/peer_scanner.go) | 主动节点扫描 |
| [controller/p2p.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go) | P2P HTTP handlers (~1615行) |
| [controller/p2p_download.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p_download.go) | 下载管理 handlers |

## 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/p2p/status` | 综合状态 |
| GET | `/p2p/node` | 本地节点信息 |
| GET | `/p2p/peers` | 已连接对端 |
| GET | `/p2p/discovered` | mDNS 发现 |
| GET | `/p2p/peers/detail` | 对端详情 |
| POST | `/p2p/connect` | 连接对端 |
| POST | `/p2p/announce` | 宣告 hash |
| POST | `/p2p/fetch` | 拉取合集 |
| POST | `/p2p/sync` | 同步文件 |
| POST | `/p2p/push` | 推送合集 |
| POST | `/p2p/request-file` | 广播文件请求 |
| POST | `/p2p/dual/announce` | 双栈宣告 |
| POST | `/p2p/dual/find` | 双栈查找 |
| POST | `/p2p/forward/create` | 创建转发 |
| POST | `/p2p/forward/connect` | 连接转发 |
| POST | `/p2p/download/resume` | 续传 |
| POST | `/p2p/download/multipeer` | 多源下载 |

## 子文档

- [p2p.md](p2p.md) — P2P 网络架构
- [dual-stack-protocol.md](dual-stack-protocol.md) — 双栈协议
- [grid.md](grid.md) — P2P 测试网格
- [API-DESIGN.md](API-DESIGN.md) — P2P API 设计
