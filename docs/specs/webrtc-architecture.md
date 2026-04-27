# WebRTC P2P 架构

> 2026-04-28

## 问题

当前架构假设所有节点都有 HTTP 公网可达地址。实际上大部分 client node 在 NAT/防火墙后面，
无法被直接访问。

## 新架构

```
┌─────────────────────────────────────────────────────┐
│  VPS (bwh.moonchan.xyz)                             │
│  ┌─────────────────────────────────────────────┐   │
│  │ Signaling Server (WebSocket)                 │   │
│  │  - Peer discovery & handshake relay          │   │
│  │  - STUN/TURN config distribution             │   │
│  │  - File availability broadcast               │   │
│  ├─────────────────────────────────────────────┤   │
│  │ HTTP API (port 3000)                         │   │
│  │  - Collection CRUD                           │   │
│  │  - File registration                         │   │
│  │  - Share links                               │   │
│  ├─────────────────────────────────────────────┤   │
│  │ Registration Server (port 4000)              │   │
│  │  - User auth (JWT)                           │   │
│  └─────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
         │                    ▲
         │ WebSocket          │ WebSocket
         ▼                    │
┌─────────────────┐   ┌─────────────────┐
│ Client Node A   │   │ Client Node B   │
│ (NAT behind)    │◄──│ (NAT behind)    │
│                 │   │                 │
│ libp2p host     │   │ libp2p host     │
│ WebRTC client   │   │ WebRTC client   │
│ HTTP API client │   │ HTTP API client │
└─────────────────┘   └─────────────────┘
         │                    │
         └──── WebRTC P2P ────┘
         (STUN hole-punched direct connection)
```

## 协议栈

| 层 | 协议 | 说明 |
|----|------|------|
| 信令 | WebSocket (wss://vps/ws/signal) | Peer 发现、SDP 交换、ICE 候选转发 |
| 连接 | WebRTC (STUN + ICE) | NAT 穿越、对等直连 |
| 数据 | WebRTC Data Channel | 文件传输（二进制流） |
| 认证 | JWT (Registration Server) | 信令连接认证 |

## 信令协议

### 客户端 → 服务端

```json
{"type": "register", "peer_id": "12D3...", "token": "jwt..."}
{"type": "offer", "to": "12D3...", "sdp": "v=0\r\n..."}
{"type": "answer", "to": "12D3...", "sdp": "v=0\r\n..."}
{"type": "ice_candidate", "to": "12D3...", "candidate": "candidate:..."}
{"type": "request_peers"}
{"type": "announce_file", "hash": "sha256..."}
{"type": "find_file", "hash": "sha256..."}
```

### 服务端 → 客户端

```json
{"type": "offer", "from": "12D3...", "sdp": "v=0\r\n..."}
{"type": "answer", "from": "12D3...", "sdp": "v=0\r\n..."}
{"type": "ice_candidate", "from": "12D3...", "candidate": "candidate:..."}
{"type": "peers", "peers": ["12D3...", "12D3..."]}
{"type": "file_providers", "hash": "sha256...", "peers": ["12D3..."]}
{"type": "peer_joined", "peer_id": "12D3..."}
{"type": "peer_left", "peer_id": "12D3..."}
```

## WebRTC Data Channel

建立连接后，通过 `FileTransfer` data channel 传输文件：

```
请求: {"type":"request","hash":"<sha256>"}
响应: {"type":"response","hash":"<sha256>","size":1234}
     （后跟二进制数据帧）
错误: {"type":"error","hash":"<sha256>","message":"not found"}
```

## STUN/TURN

- STUN: `stun:stun.moonchan.xyz:3478`
- TURN: `turn:turn.moonchan.xyz:3478` (fallback, relayed)

## 实现计划

### Phase 1: Signaling Server
- [ ] WebSocket signaling endpoint `/ws/signal`
- [ ] Peer registry (connected peers map)
- [ ] SDP/ICE relay between peers
- [ ] File availability tracking

### Phase 2: WebRTC Client (Frontend)
- [ ] WebRTC connection to signaling server
- [ ] RTCPeerConnection setup with STUN
- [ ] Data channel for file transfer
- [ ] File request/response protocol

### Phase 3: Integration
- [ ] Fallback to HTTP for files not available via WebRTC
- [ ] Multi-peer file discovery
- [ ] Progress tracking for WebRTC transfers
