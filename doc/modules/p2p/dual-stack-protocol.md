# Peerdrive Dual-Stack P2P Protocol

## Architecture

```
                       ┌──────────────────────────────────────────────┐
                       │              Peerdrive Server                │
                       │                                              │
                       │   ┌──────────┐   ┌──────────┐   ┌────────┐  │
                       │   │  IPFS /   │   │   BT     │   │ Local  │  │
                       │   │  libp2p   │   │ Mainline │   │Storage │  │
                       │   │   DHT     │   │  DHT     │   │  FS    │  │
                       │   └────┬─────┘   └────┬─────┘   └───┬────┘  │
                       │        │              │              │       │
                       │   ┌────┴──────────────┴──────────────┴───┐  │
                       │   │         DualP2PService               │  │
                       │   │  (announce / find / fetch merged)    │  │
                       │   └────────────────┬─────────────────────┘  │
                       │                    │                        │
                       │   ┌────────────────┴──────────────────┐    │
                       │   │        HTTP API (Gin Router)       │    │
                       │   │  /p2p/dual/announce  /p2p/dual/find│    │
                       │   │  /sha256sum/:sha256  /files/upload │    │
                       │   └────────────────┬──────────────────┘    │
                       └────────────────────┼───────────────────────┘
                                            │
                                    HTTP / WAN
                                            │
                       ┌────────────────────┼───────────────────────┐
                       │                    │                        │
                       │   ┌────────────────┴──────────────────┐    │
                       │   │       Clients & Test Peers         │    │
                       │   │  Browser  |  curl  |  ipfs-peer  |  │    │
                       │   │  bt-peer  |  Python tests          │    │
                       │   └────────────────────────────────────┘    │
                       └────────────────────────────────────────────┘
```

### Layer Overview

| Layer | Technology | Role |
|-------|-----------|------|
| IPFS/libp2p DHT | Kademlia DHT over libp2p | Same-network peer discovery, mDNS local peers, direct P2P file transfer |
| BT Mainline DHT | Kademlia DHT over UDP (KRPC) | Global peer discovery, internet-wide swarm participation |
| Local Storage | Content-addressed filesystem on disk | Primary file store: `storage/{hash[:2]}/{hash}` |
| DualP2PService | Go wrapper merging both DHTs | `Announce()` both nets, `FindProviders()` merged, `FetchFile()` prioritized |

---

## Dual Announce

When a file is uploaded or registered, its SHA256 hash is announced on both the IPFS/libp2p DHT and the BitTorrent Mainline DHT.

### Flow

```
Client                    Peerdrive                   IPFS DHT            BT DHT
  │                          │                           │                   │
  │  POST /p2p/dual/announce │                           │                   │
  │  {"hash":"<sha256>"}     │                           │                   │
  │─────────────────────────>│                           │                   │
  │                          │                           │                   │
  │                          │  DHT.Provide(cid, true)   │                   │
  │                          │──────────────────────────>│                   │
  │                          │   <announce_peer>         │                   │
  │                          │──────────────────────────────────────────────>│
  │                          │                           │                   │
  │  {"status":"announced    │                           │                   │
  │   on both networks"}     │                           │                   │
  │<─────────────────────────│                           │                   │
```

### Request

```json
POST /p2p/dual/announce
Content-Type: application/json

{
  "hash": "a1b2c3d4e5f6...<64 hex chars>"
}
```

### Response (200)

```json
{
  "status": "announced on both networks"
}
```

### Per-Network Endpoints

| Endpoint | Network | Error on missing route |
|----------|---------|----------------------|
| `POST /p2p/announce` | IPFS/libp2p only | Returns 500 if P2P disabled |
| `POST /bt/announce` | BT DHT only | Returns 503 if BT DHT disabled |
| `POST /p2p/dual/announce` | Both | Returns 503 if dual not available |

---

## Dual Find

Searches both DHTs **concurrently** and merges results. The `/p2p/dual/find` endpoint triggers two parallel goroutines and waits for both to complete (via `sync.WaitGroup`).

### Flow

```
Client                    Peerdrive                   IPFS DHT            BT DHT
  │                          │                           │                   │
  │  POST /p2p/dual/find     │                           │                   │
  │  {"hash":"<sha256>"}     │                           │                   │
  │─────────────────────────>│                           │                   │
  │                          │  ┌─ goroutine 1 ────────  │                   │
  │                          │  │  DHT.FindProviders()   │                   │
  │                          │  │───────────────────────>│                   │
  │                          │  │  < peer addresses      │                   │
  │                          │  └────────────────────────│                   │
  │                          │                           │                   │
  │                          │  ┌─ goroutine 2 ────────  │                   │
  │                          │  │  BT.FindProviders()    │                   │
  │                          │  │───────────────────────────────────────────>│
  │                          │  │  < peer addresses      │                   │
  │                          │  └────────────────────────│                   │
  │                          │                           │                   │
  │  {"hash":"...",          │                           │                   │
  │   "ipfs_peers":[...],    │                           │                   │
  │   "bt_peers":[...]}      │                           │                   │
  │<─────────────────────────│                           │                   │
```

### Request

```json
POST /p2p/dual/find
Content-Type: application/json

{
  "hash": "a1b2c3d4e5f6...<64 hex chars>"
}
```

### Response (200)

```json
{
  "hash": "a1b2c3d4e5f6...",
  "ipfs_peers": [
    "/ip4/192.168.1.10/tcp/4001/p2p/12D3KooW...",
    "/ip4/10.0.0.5/tcp/4001/p2p/12D3KooX..."
  ],
  "bt_peers": [
    "192.168.1.20:6881",
    "203.0.113.50:6881"
  ]
}
```

### Error Response (503)

```json
{
  "error": "Dual P2P not available"
}
```

---

## Fetch Priority

When downloading a file by SHA256 hash, Peerdrive uses the following priority chain:

```
1. Local Storage ────────────> "storage/{h[:2]}/{h}"
   (fastest, zero network)

2. IPFS/libp2p DHT ──────────> FindProviders() → Connect → requestData()
   (same-network / LAN peers, low latency)

3. BT DHT (HTTP bridge) ─────> BTFindProviders() → BTBridge.FetchFile()
   (global fallback via HTTP-seeded download)

4. URL Fallback ─────────────> Registration server or configured URL template
   (last resort, external source)
```

### Implementation Flow (`DualP2PService.FetchFile`)

```
FetchFile(ctx, hash)
  ├── IPFS enabled? Try FetchFile(hash)
  │     ├── Success → return data (1)
  │     └── Fail    → log, continue
  ├── BT available? Try BTBridge.FetchFile(hash)
  │     ├── Success → return data (3)
  │     └── Fail    → log, continue
  └── Return error "could not fetch from any network"
```

### Download via HTTP API

```
GET /sha256sum/:sha256
  ├── Downloader.GetFileStream(hash)
  │     ├── GetFileMeta(hash) → found?
  │     │     ├── Yes → iterate providers, return first available
  │     │     └── No  → p2pFallback(hash)
  │     └── p2pFallback:
  │           ├── P2PService.FetchFile(hash) [IPFS DHT]
  │           └── On success: cache to local storage, return stream
  └── Returns file as attachment with Content-Disposition
```

---

## API Endpoints

### Dual-Stack Endpoints

| Method | Route | Description |
|--------|-------|-------------|
| `POST` | `/p2p/dual/announce` | Announce hash on both IPFS and BT DHT |
| `POST` | `/p2p/dual/find` | Find providers on both networks, merge results |

### Supporting Endpoints

| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/sha256sum/:sha256` | Download file by SHA256 (with P2P fallback) |
| `GET` | `/ping` | Health check |
| `GET` | `/p2p/status` | P2P node status and stats |
| `GET` | `/p2p/node` | Peer ID and listen addresses |
| `GET` | `/p2p/peers` | Connected peers |
| `POST` | `/p2p/announce` | Announce on IPFS/libp2p only |
| `POST` | `/p2p/connect` | Connect to a peer by multiaddr |
| `GET` | `/bt/status` | BT DHT node status |
| `POST` | `/bt/announce` | Announce on BT DHT only |
| `POST` | `/bt/find` | Find providers on BT DHT only |
| `POST` | `/files/upload` | Upload file, returns SHA256 hash |

---

## Network Selection Strategy

| Scenario | Use Network | Rationale |
|----------|-------------|-----------|
| Same LAN / mDNS | IPFS/libp2p | mDNS discovery, direct peer connection, low latency |
| Same cloud / datacenter | IPFS/libp2p | Private IP ranges, relay may be available |
| Internet-wide discovery | BT DHT | Largest public DHT, billions of nodes |
| Redundant announce | Both | Maximize discoverability |
| Initial file fetch | IPFS first | Lower latency for same-network peers |
| Fallback fetch | BT DHT | Global swarm reachable even without relay |
| Peer unreachable via libp2p | BT DHT | Works through NATs without hole-punching |

---

## File Sharing Lifecycle

```
                    ┌──────────────────────────┐
                    │   1. User uploads file    │
                    │   POST /files/upload      │
                    └───────────┬──────────────┘
                                │
                    ┌───────────▼──────────────┐
                    │   2. SHA256 hash computed │
                    │   Content stored at:      │
                    │   storage/{h[:2]}/{h}     │
                    └───────────┬──────────────┘
                                │
                    ┌───────────▼──────────────┐
                    │   3. File metadata saved  │
                    │   file_meta: hash,size,   │
                    │   filename,mime,type      │
                    │   file_providers: "local" │
                    └───────────┬──────────────┘
                                │
                    ┌───────────▼──────────────┐
                    │   4. Dual announce        │
                    │   ┌─ IPFS DHT: Provide()  │
                    │   └─ BT DHT: announce_peer│
                    └───────────┬──────────────┘
                                │
                    ┌───────────▼──────────────┐
                    │   5. Any peer can find    │
                    │   Via IPFS DHT or BT DHT │
                    │   by SHA256 hash          │
                    └───────────┬──────────────┘
                                │
                    ┌───────────▼──────────────┐
                    │   6. File download        │
                    │   GET /sha256sum/:hash    │
                    │   └─ Local storage        │
                    │   └─ IPFS P2P fallback    │
                    │   └─ BT DHT fallback      │
                    └──────────────────────────┘
```

### Key Properties

- **Content-addressable**: Every file is identified by its SHA256 hash. Integrity is verified on every transfer.
- **Network-agnostic hash**: The same SHA256 hash is used on both DHTs. IPFS uses a CID derived from the SHA256, BT DHT uses the raw SHA256 bytes directly.
- **No central coordination**: Peers find each other purely through DHT mechanisms. No tracker or registry required.
- **Graceful degradation**: Each network can fail independently. The dual-stack wrapper handles per-network errors and reports partial success.

---

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `PEERDRIVE_P2P_ENABLE` | `true` | Enable IPFS/libp2p DHT node |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | IPv4 listen address |
| `PEERDRIVE_P2P_LISTEN_V6` | `/ip6/::/tcp/0` | IPv6 listen address |
| `PEERDRIVE_BT_DHT_ENABLE` | `true` | Enable BT Mainline DHT node |
| `PEERDRIVE_BT_DHT_LISTEN` | `:6881` | BT DHT UDP listen address |
| `PEERDRIVE_STORAGE` | `./storage` | Content-addressed file storage directory |
| `PORT` | `3000` | HTTP API server port |
