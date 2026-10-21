# WebRTC P2P Architecture

> 2026-04-28

## Problem

The current architecture assumes all nodes have HTTP publicly reachable addresses. In reality, most client nodes are behind NAT/firewalls
and cannot be directly accessed.

## New Architecture

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

## Protocol Stack

| Layer | Protocol | Description |
|----|------|------|
| Signaling | WebSocket (wss://vps/ws/signal) | Peer discovery, SDP exchange, ICE candidate forwarding |
| Connection | WebRTC (STUN + ICE) | NAT traversal, peer-to-peer direct connection |
| Data | WebRTC Data Channel | File transfer (binary stream) |
| Authentication | JWT (Registration Server) | Signaling connection authentication |

## Signaling Protocol

### Client → Server

```json
{"type": "register", "peer_id": "12D3...", "token": "jwt..."}
{"type": "offer", "to": "12D3...", "sdp": "v=0\r\n..."}
{"type": "answer", "to": "12D3...", "sdp": "v=0\r\n..."}
{"type": "ice_candidate", "to": "12D3...", "candidate": "candidate:..."}
{"type": "request_peers"}
{"type": "announce_file", "hash": "sha256..."}
{"type": "find_file", "hash": "sha256..."}
```

### Server → Client

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

After establishing a connection, files are transferred via the `FileTransfer` data channel:

```
Request: {"type":"request","hash":"<sha256>"}
Response: {"type":"response","hash":"<sha256>","size":1234}
      (followed by binary data frames)
Error: {"type":"error","hash":"<sha256>","message":"not found"}
```

## STUN/TURN

- STUN: `stun:stun.moonchan.xyz:3478`
- TURN: `turn:turn.moonchan.xyz:3478` (fallback, relayed)

## Implementation Plan

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
