# P2P Network Architecture

> **⚠️ Outdated Documentation (Historical Archive)**: The libp2p stack described in this document was entirely removed on 2026-08-16
> (REFACTOR.md §8). The current interconnect layer = PeerJS signaling + WebRTC DataChannel
> (`back/internal/transport/peerjs_service.go` + `back/peerjs/`); the following content is for historical reference only.

> **Last Modified**: 2026-04-27 · **Version**: v2.0
> **Previous Version**: v1.0 (2026-04-26) — Basic libp2p node, mDNS discovery, Exchange protocol
> **New in this version**: Connection manager (heartbeat/reconnect), chunked transfer (256KB chunk/8 concurrent), STUN configuration, SIZE protocol

## Overview

Peerdrive builds a P2P peer-to-peer network based on libp2p, supporting node discovery, file transfer, DHT routing, and relay forwarding.

## Protocol Stack

| Layer | Protocol/Component | Description |
|---|---|---|
| Transport | TCP, QUIC, WebSocket | Low-level communication protocols |
| Security | Noise (auto-negotiated) | libp2p built-in encryption |
| Stream Multiplexing | Yamux | Multi-stream multiplexing over a single connection |
| NAT Traversal | AutoNAT v2, DCUtR, NAT-PMP | Auto-detect network type and perform hole punching |
| Relay | Circuit Relay v2 | Public relay for forwarding traffic |
| Discovery | mDNS (LAN), Kademlia DHT | Node discovery and routing |
| Application | /peerdrive/exchange/1.0.0, /peerdrive/chunk/1.0.0 | File transfer protocols |

## Application Layer Protocols

### Exchange Protocol (`/peerdrive/exchange/1.0.0`)

Used for whole-file transfer and file size queries.

**Whole File Request:**
```
Request: <64-char hex SHA256>\n
Response: OK <byte_count>\n<raw binary data>
Error: ERR <message>\n
```

**File Size Query:**
```
Request: SIZE <64-char hex SHA256>\n
Response: OK <byte_count>\n
Error: ERR <message>\n
```

### Chunk Protocol (`/peerdrive/chunk/1.0.0`)

Used for chunked transfer of large files.

```
Request: CHUNK <64-char hex SHA256> <offset> <size>\n
Response: <raw binary chunk data>
Error: ERR <message>\n
```

Limit: Maximum 256KB per chunk.

### Announce Protocol (`/peerdrive/announce/1.0.0`)

A node announces to other nodes that it owns a particular file.

```
Request: <64-char hex SHA256>\n
Response: OK\n
```

After receiving an Announce, the node automatically registers itself as a provider on the DHT.

## Connection Management

### Automatic Connection

- **mDNS Discovery**: Automatically discovers and connects to nodes on the local network
- **Bootstrap Nodes**: Attempts to connect to configured bootstrap nodes at startup
- **DHT Discovery**: Attempts to connect when looking up file providers via DHT

### Heartbeat Detection

Checks the status of connected nodes every 30 seconds; attempts to reconnect to disconnected nodes.

### Reconnection Policy

- Initial reconnection interval: 10 seconds
- Backoff ceiling: 5 minutes
- Connection timeout: 15 seconds

## File Transfer

### Small File Transfer

- Uses the Exchange protocol to fetch a complete file in a single request
- Timeout: 30 seconds

### Large File Chunked Transfer

- Files are split into 256KB Chunks
- Up to 8 concurrent Chunk requests
- Supports downloading different chunks in parallel from multiple providers
- Verifies SHA256 hash after download completes
- Supports progress callbacks for real-time download progress display
- Transfer timeout: 5 minutes

### File Lookup Priority

1. Local storage directory (SHA256 content-addressed)
2. file_providers database records (local path)
3. P2P network (DHT + direct requests)

## WebSocket Transfer

Used for file transfer in browser nodes.

- Endpoint: `GET /ws/transfer`
- Message format: JSON + binary frames
- Supports ping/pong heartbeat
- File requests are broadcast via WS to all connected browser nodes

### Message Types

**Client -> Server:**
```json
{"type": "request", "hash": "sha256..."}
{"type": "ping"}
```

**Server -> Client:**
```json
{"type": "response", "hash": "sha256...", "size": 1234}
(followed by binary frame)
{"type": "pong"}
{"type": "error", "hash": "sha256...", "message": "not found"}
```

## Environment Variables

| Variable | Description | Default |
|------|------|--------|
| `PEERDRIVE_P2P_ENABLE` | Whether to enable P2P | `true` |
| `PEERDRIVE_P2P_LISTEN` | libp2p listen address | `/ip4/0.0.0.0/tcp/0` |
| `PEERDRIVE_BOOTSTRAP_PEER` | Bootstrap node address | `""` |
| `PEERDRIVE_MDNS_ENABLE` | Whether to enable mDNS | `true` |
| `PEERDRIVE_RELAY_ENABLE` | Whether to enable relay | `false` |
| `PEERDRIVE_RELAY_MODE` | Relay mode (client/server/off) | `client` |
| `PEERDRIVE_STATIC_RELAYS` | Static relay address list | `""` |
| `PEERDRIVE_HOLE_PUNCH` | Whether to enable hole punching | `true` |
| `PEERDRIVE_PUBLIC_REACHABLE` | Whether the node is publicly reachable | `false` |
| `PEERDRIVE_AUTO_NAT` | Whether to enable AutoNAT | `true` |
| `PEERDRIVE_NAT_PORTMAP` | Whether to enable NAT-PMP | `false` |
| `PEERDRIVE_PUBLIC_DOMAIN` | Node public access domain | `""` |

## API Endpoints

| Method | Path | Description |
|------|------|------|
| GET | `/p2p/status` | P2P status (includes connection stats and transfer tasks) |
| GET | `/p2p/node` | This node's information |
| GET | `/p2p/peers` | List of connected nodes |
| GET | `/p2p/discovered` | List of discovered nodes |
| GET | `/p2p/ping/:peer_id` | Ping a node |
| POST | `/p2p/connect` | Manually connect to a node |
| POST | `/p2p/announce` | Announce ownership of a file |
| POST | `/p2p/fetch` | P2P fetch a collection |
| POST | `/p2p/sync` | Sync files from a node |
| POST | `/p2p/push` | Push a collection to a node |
| POST | `/p2p/request-file` | Broadcast file request |
| GET | `/p2p/ws/info` | WebSocket connection info |
| GET | `/ws/transfer` | WebSocket transfer endpoint |

## Running Modes

### Regular Node (default)

```bash
PEERDRIVE_P2P_ENABLE=true go run ./cmd/server/main.go
```

### Relay Server (Public Super Node)

```bash
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=server \
PEERDRIVE_PUBLIC_REACHABLE=true \
PEERDRIVE_PUBLIC_DOMAIN=relay.example.com \
go run ./cmd/server/main.go
```

### Pure Relay Node (does not store files)

```bash
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=server \
PEERDRIVE_STORAGE_ENABLE=false \
go run ./cmd/server/main.go
```

### Relay Client (node behind NAT)

```bash
PEERDRIVE_RELAY_ENABLE=true \
PEERDRIVE_RELAY_MODE=client \
PEERDRIVE_STATIC_RELAYS="/ip4/1.2.3.4/tcp/4001/p2p/12D3..." \
go run ./cmd/server/main.go
```

## Testing

```bash
# Start two nodes to test P2P transfer
# Node A
PEERDRIVE_PORT=3001 go run ./cmd/server/main.go

# Node B (using a different port)
PEERDRIVE_PORT=3002 PEERDRIVE_P2P_LISTEN=/ip4/0.0.0.0/tcp/0 \
go run ./cmd/server/main.go

# Run P2P tests (note: this document describes the libp2p stack that was
# removed on 2026-08-16; the current interconnect layer is PeerJS/WebRTC,
# see doc/REFACTOR.md; test/p2p_transfer.sh was removed with the stack)
```
