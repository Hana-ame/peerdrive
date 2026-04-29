# IPFS 模块

IPFS 兼容层 — libp2p Kademlia DHT + Bitswap + CID 转换 + WebRTC 信令。

## 源码

| 文件 | 说明 |
|------|------|
| [service/ipfs_compat.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/ipfs_compat.go) | IPFS Bitswap + CID/SHA256 转换 |
| [controller/p2p.go#L1224-L1430](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/p2p.go#L1224) | IPFS HTTP handlers |
| [service/signaling.go](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/service/signaling.go) | WebRTC 信令 Hub |
| [controller/download.go#L130](https://github.com/Hana-ame/peerdrive/blob/feat/node-auth/go/internal/controller/download.go#L130) | CID 下载 handler |

## 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/ipfs` | IPFS 兼容层状态 |
| POST | `/ipfs/toggle` | 开关 IPFS 兼容层 |
| GET | `/ipfs/:cid` | CID 下载 |
| POST | `/ipfs/pin/:cid` | 固定 CID |
| DELETE | `/ipfs/pin/:cid` | 取消固定 |
| GET | `/ipfs/pins` | 列出所有固定 |
| GET | `/ipfs/gateways` | 网关健康检查 |
| POST | `/ipfs/dht/get` | IPFS DHT 查询 |

## 子文档

- [API-DESIGN.md](API-DESIGN.md) — IPFS API 设计
- [ipfs-protocol.md](ipfs-protocol.md) — IPFS 协议
- [webrtc-architecture.md](webrtc-architecture.md) — WebRTC 架构
