# P2P Module API Design

> All P2P endpoints are grouped under `/p2p` and served by the Gin HTTP router.
> All responses are JSON (`application/json`).

---

## 1. `GET /p2p/status`

Comprehensive P2P node status including connection counts, relay mode, hole-punch state,
WebSocket connections, active transfers, and connection-manager stats.

**Response (200):**
```json
{
  "enabled": true,
  "peer_id": "12D3KooW...",
  "addrs": ["/ip4/192.168.1.5/tcp/4001", "/ip4/127.0.0.1/tcp/4001"],
  "connected_count": 3,
  "discovered_count": 5,
  "relay_mode": "client",
  "hole_punch": true,
  "ws_connections": 0,
  "signal_peers": 0,
  "conn_stats": {
    "total_known_peers": 10,
    "active_connections": 3,
    "reconnect_attempts": 2,
    "successful_conns": 8,
    "failed_conns": 1
  },
  "active_transfers": [
    {
      "hash": "abc123...",
      "progress": 0.45,
      "total_mb": 12.5,
      "done": false,
      "peers": 2,
      "elapsed": "3.2s"
    }
  ]
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/status | jq .
```

---

## 2. `GET /p2p/node`

Returns the local node's libp2p Peer ID and all advertised multiaddrs.

**Response (200):**
```json
{
  "peer_id": "12D3KooWHvCk...",
  "addrs": ["/ip4/192.168.1.5/tcp/4001", "/ip4/127.0.0.1/tcp/4001"]
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/node | jq .
```

---

## 3. `GET /p2p/peers`

Returns a flat list of currently connected peer IDs.

**Response (200):**
```json
{
  "peers": ["12D3KooWHv...", "12D3KooWAb..."]
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/peers | jq .
```

---

## 4. `GET /p2p/peers/detail`

Returns full metadata for every tracked peer (latency, transport, direction, last-seen, address).

**Response (200):**
```json
[
  {
    "peer_id": "12D3KooWHv...",
    "addrs": ["/ip4/10.0.0.2/tcp/4001"],
    "latency": "12ms",
    "transport": "tcp",
    "direction": "outbound",
    "connected": true,
    "last_seen": "2026-04-28T10:00:00Z",
    "protocols": ["/peerdrive/exchange/1.0.0"],
    "agent_version": "peerdrive/1.0"
  }
]
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/peers/detail | jq .
```

---

## 5. `GET /p2p/peers/detail/:peer_id`

Returns detail for a single peer by its Peer ID.

**Response (200):**
```json
{
  "peer_id": "12D3KooWHv...",
  "addrs": ["/ip4/10.0.0.2/tcp/4001"],
  "latency": "12ms",
  "transport": "tcp",
  "direction": "outbound",
  "connected": true,
  "last_seen": "2026-04-28T10:00:00Z"
}
```

**Response (404):**
```json
{ "error": "peer not found" }
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/peers/detail/12D3KooWHv... | jq .
```

---

## 6. `GET /p2p/discovered`

Returns peers discovered via mDNS on the local LAN.

**Response (200):**
```json
{
  "peers": [
    {
      "peer_id": "12D3KooWHv...",
      "addrs": ["/ip4/192.168.1.10/tcp/4001"]
    }
  ]
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/discovered | jq .
```

---

## 7. `GET /p2p/ping/:peer_id`

Sends a libp2p ping to the specified peer and returns round-trip time.

**Params:**
| Param     | Type   | Description                |
|-----------|--------|----------------------------|
| `peer_id` | string | libp2p Peer ID (base58)    |

**Response (200):**
```json
{
  "peer": "12D3KooWHv...",
  "rtt": "42ms"
}
```

**Response (400 / 500):**
```json
{ "error": "invalid peer id" }
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/ping/12D3KooWHv... | jq .
```

---

## 8. `POST /p2p/connect`

Connects to a remote peer by its full multiaddr (including `/p2p/...` suffix).

**Request:**
```json
{
  "addr": "/ip4/10.0.0.2/tcp/4001/p2p/12D3KooWHv..."
}
```

**Response (200):**
```json
{ "status": "connected" }
```

**Response (500):**
```json
{ "error": "connection failed: ..." }
```

**cURL:**
```bash
curl -s -X POST http://localhost:3000/p2p/connect \
  -H "Content-Type: application/json" \
  -d '{"addr": "/ip4/10.0.0.2/tcp/4001/p2p/12D3KooWHv..."}' | jq .
```

---

## 9. `POST /p2p/announce`

Announces a content hash to the IPFS DHT so other peers can discover this node as a provider.

**Request:**
```json
{
  "hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
}
```

**Response (200):**
```json
{ "status": "announced" }
```

**Warning (200):**
```json
{ "status": "announced locally", "warning": "DHT not fully initialized" }
```

**cURL:**
```bash
curl -s -X POST http://localhost:3000/p2p/announce \
  -H "Content-Type: application/json" \
  -d '{"hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}' | jq .
```

---

## 10. `POST /p2p/fetch`

Fetches an anonymous collection (metadata + file list) from the P2P network by hash.
Supports timeout (60s).

**Request:**
```json
{
  "hash": "abc123..."
}
```

**Response (200):**
```json
{
  "hash": "abc123...",
  "friendly_name": "p2p-test-collection",
  "entries": [
    {
      "path": "test_file.txt",
      "hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "size": 1234
    }
  ],
  "created_at": "2026-04-28T10:00:00Z"
}
```

**Response (404):**
```json
{ "error": "collection not found on p2p: ..." }
```

**cURL:**
```bash
curl -s -X POST http://localhost:3000/p2p/fetch \
  -H "Content-Type: application/json" \
  -d '{"hash": "abc123..."}' | jq .
```

---

## 11. `POST /p2p/sync`

Syncs files from a specific peer to a local target directory.
If a collection hash is provided without explicit file_hashes, the collection is fetched first.

**Request:**
```json
{
  "peer_id": "12D3KooWHv...",
  "hash": "abc123...",
  "file_hashes": ["e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"],
  "target_dir": "./p2p_sync"
}
```

**Response (200):**
```json
{
  "synced": ["/tmp/p2p_sync/test_file.txt"],
  "count": 1,
  "saved_to": "./p2p_sync"
}
```

**cURL:**
```bash
curl -s -X POST http://localhost:3000/p2p/sync \
  -H "Content-Type: application/json" \
  -d '{"peer_id": "12D3KooWHv...", "hash": "abc123...", "target_dir": "./p2p_sync"}' | jq .
```

---

## 12. `GET /p2p/connections`

Returns connection-direction statistics (inbound/outbound counts) and active scanner status.

**Response (200):**
```json
{
  "inbound": 1,
  "outbound": 2,
  "total": 3,
  "active_scanners": ["scanner-1"],
  "last_scan_times": {
    "scanner-1": "2026-04-28T10:05:00Z"
  }
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/connections | jq .
```

---

## 13. `GET /p2p/stats`

Global P2P statistics from the PeerTracker (total peers seen, total transferred, uptime, etc.).

**Response (200):**
```json
{
  "total_peers_seen": 15,
  "total_transferred_bytes": 1048576,
  "total_pings_sent": 42,
  "total_pings_received": 38,
  "avg_latency_ms": 24.5,
  "uptime_seconds": 3600
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/stats | jq .
```

---

## 14. `GET /p2p/topology`

Returns the P2P connection topology graph (local peer + edges to connected peers).

**Response (200):**
```json
{
  "local_peer_id": "12D3KooWHv...",
  "edges": [
    {
      "peer_id": "12D3KooWAb...",
      "addrs": ["/ip4/10.0.0.2/tcp/4001"],
      "latency": "15ms",
      "direction": "outbound",
      "protocols": ["/peerdrive/exchange/1.0.0"]
    }
  ]
}
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/topology | jq .
```

---

## 15. `GET /p2p/quality`

Returns connection quality metrics (latency, jitter, packet loss, score) for all known peers.
Supports optional `?peer_id=` query parameter to filter a single peer.

**Query params:**
| Param     | Type   | Description                          |
|-----------|--------|--------------------------------------|
| `peer_id` | string | (optional) Filter for a single peer  |

**Response (200):**
```json
[
  {
    "peer_id": "12D3KooWHv...",
    "avg_latency_ms": 12.5,
    "jitter_ms": 2.3,
    "packet_loss_pct": 0.0,
    "score": 98.2,
    "last_updated": "2026-04-28T10:05:00Z",
    "samples": 10
  }
]
```

**cURL:**
```bash
curl -s http://localhost:3000/p2p/quality | jq .

# Filter single peer:
curl -s "http://localhost:3000/p2p/quality?peer_id=12D3KooWHv..." | jq .
```

---

## Route Summary Table

| #  | Method | Path                        | Handler                    | Description                              |
|----|--------|-----------------------------|----------------------------|------------------------------------------|
| 1  | GET    | `/p2p/status`               | `P2PStatus`                | Comprehensive P2P node status            |
| 2  | GET    | `/p2p/node`                 | `GetNodeInfo`              | Local node Peer ID + addresses           |
| 3  | GET    | `/p2p/peers`                | `GetPeers`                 | Connected peer ID list                   |
| 4  | GET    | `/p2p/peers/detail`         | `GetPeersDetail`           | Full metadata for all peers              |
| 5  | GET    | `/p2p/peers/detail/:peer_id`| `GetPeerDetail`            | Single peer metadata                     |
| 6  | GET    | `/p2p/discovered`           | `GetDiscoveredPeers`       | mDNS-discovered LAN peers                |
| 7  | GET    | `/p2p/ping/:peer_id`        | `PingPeer`                 | libp2p ping (RTT)                        |
| 8  | POST   | `/p2p/connect`              | `ConnectPeer`              | Connect to remote by multiaddr           |
| 9  | POST   | `/p2p/announce`             | `AnnounceHash`             | Announce content hash on IPFS DHT        |
| 10 | POST   | `/p2p/fetch`                | `FetchCollection`          | Fetch anonymous collection by hash       |
| 11 | POST   | `/p2p/sync`                 | `SyncFromPeer`             | Sync files from peer to local dir        |
| 12 | GET    | `/p2p/connections`          | `GetConnections`           | Connection direction stats + scanners    |
| 13 | GET    | `/p2p/stats`                | `GetP2PStats`              | Global P2P statistics                    |
| 14 | GET    | `/p2p/topology`             | `GetTopology`              | Connection topology graph                |
| 15 | GET    | `/p2p/quality`              | `GetConnectionQuality`     | Per-peer connection quality metrics      |

All endpoints are defined in `internal/controller/p2p.go` and registered in `internal/router/router.go`.
