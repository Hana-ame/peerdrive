# IPFS Module Docs
- ipfs-protocol.md — IPFS/libp2p 协议规范（Bitswap + DHT + 自定义协议）
- webrtc-architecture.md — WebRTC 信令架构
- API-DESIGN.md — IPFS HTTP API 设计

## 架构
- **IPFSService** (`back/internal/service/ipfs_service.go`) — boxo Bitswap 客户端/服务端，复用 libp2p host + DHT
- **peerdriveBlockstore** — 实现 boxo Blockstore 接口，CID 直接映射 SHA-256 内容寻址存储，零文件复制
- **IPFSProvider** (`back/internal/provider/ipfs.go`) — Bitswap 优先获取，HTTP 网关竞速回退
- **IPFSCompatLayer** (`back/internal/service/ipfs_compat.go`) — 兼容层，Bitswap 由 boxo 接管
- **hashutil** (`back/pkg/hashutil/hashutil.go`) — SHA256 ↔ CID 双向转换

## 文件存储
```
storage/<sha256[:2]>/<sha256>  ←── 唯一文件存放处（无重复）
CID → multihash → SHA-256 digest → 读取同一文件
Pin = 文件在 collection 中（storage 中存在）
```
