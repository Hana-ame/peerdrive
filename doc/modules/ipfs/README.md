# IPFS Module Docs
- ipfs-protocol.md — IPFS/libp2p protocol specification (Bitswap + DHT + custom protocols)
- webrtc-architecture.md — WebRTC signaling architecture
- API-DESIGN.md — IPFS HTTP API design

## Architecture
- **IPFSService** (`back/internal/service/ipfs_service.go`) — boxo Bitswap client/server, reuses libp2p host + DHT
- **peerdriveBlockstore** — Implements boxo Blockstore interface, CID directly maps to SHA-256 content-addressed storage, zero file duplication
- **IPFSProvider** (`back/internal/provider/ipfs.go`) — Bitswap-first retrieval, HTTP gateway racing fallback
- **IPFSCompatLayer** (`back/internal/service/ipfs_compat.go`) — Compatibility layer, Bitswap handled by boxo
- **hashutil** (`back/pkg/hashutil/hashutil.go`) — SHA256 ↔ CID bidirectional conversion

## File Storage
```
storage/<sha256[:2]>/<sha256>  ←── Single file storage location (no duplicates)
CID → multihash → SHA-256 digest → Reads the same file
Pin = file exists in collection (exists in storage)
```
