# P2P Module Docs

> ⚠️ 2026-08-16 起：互联层已整体替换为 **PeerJS 信令 + WebRTC DataChannel**。

## 当前有效

- [TRANSPORT.md](./TRANSPORT.md) — 互联框架：WS + PeerJS 双传输 + Session 抽象 + 信令装配
- [API-DESIGN.md](./API-DESIGN.md) — `/p2p` 分组下的 HTTP 端点设计

## 已归档（旧 libp2p/BT-DHT 栈，**勿照此实现**）

> 见 [doc/archive/LEGACY.md](../../archive/LEGACY.md)。这三份搬到 [archive/](./archive/)：

- [archive/p2p.md](./archive/p2p.md) — 旧 libp2p 网络架构
- [archive/dual-stack-protocol.md](./archive/dual-stack-protocol.md) — 旧 IPFS+BT 双栈规格
- [archive/grid.md](./archive/grid.md) — 旧 P2P 测试网格
