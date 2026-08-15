# Peerdrive BitTorrent Mainline DHT Protocol

> Technical specification for integrating BT Mainline DHT (Kademlia) into Peerdrive's P2P dual-stack architecture.

---

## 1. Overview

BitTorrent Mainline DHT is a distributed hash table based on **Kademlia**, running over **UDP** with **bencode** serialization. It is the world's largest deployed DHT, with millions of nodes forming a self-organizing overlay network.

Peerdrive integrates BT Mainline DHT as a secondary P2P discovery layer alongside libp2p/IPFS. This allows Peerdrive nodes to:

- Announce file availability to the global BT DHT network
- Discover peers hosting a given infohash via the BT DHT
- Bridge between libp2p and BT DHT networks (dual-stack)

---

## 2. Protocol Fundamentals

### 2.1 Kademlia DHT

| Property | Value |
|----------|-------|
| Routing | XOR-based binary tree, k-buckets |
| k (bucket size) | 8 |
| α (parallelism) | 3 |
| Node ID | 160-bit (20 bytes), random |
| Transport | UDP |
| Serialization | Bencode (bittorrent encoding) |
| KRPC | Query-Response RPC over UDP |

### 2.2 Bencode Encoding

Bencode supports four data types:

```
Integer:   i<number>e          → i123e
String:    <length>:<bytes>     → 4:spam
List:      l<values>e           → l4:spami123ee
Dict:      d<key-value pairs>e  → d3:bar4:spame
```

All KRPC messages are bencoded dictionaries.

### 2.3 Bootstrap Nodes

Peerdrive connects to the BT DHT network via well-known bootstrap nodes:

| Host | Port |
|------|------|
| `router.bittorrent.com` | 6881 |
| `dht.transmissionbt.com` | 6881 |
| `router.utorrent.com` | 6881 |

**Node ID derivation:** Each bootstrap node is resolved via DNS A record, then a `find_node` query is sent with a random target node ID. The response populates the local routing table, and iterative lookups complete the bootstrap.

### 2.4 KRPC Message Format

All messages have a common outer structure:

```bencode
d
  1:t<tid>          # transaction ID (2 bytes, binary)
  1:y<type>          # message type: 'q'=query, 'r'=response, 'e'=error
  1:q<query>         # (query only) query name
  1:a<args>          # (query only) query arguments dict
  1:r<response>      # (response only) response dict
  1:e<error>         # (error only) [code, message]
e
```

---

## 3. KRPC Query Types

### 3.1 `ping`

Probes whether a node is alive.

**Query:**
```bencode
d
  1:t2:aa
  1:y1:q
  1:q4:ping
  1:ad2:id20:<node_id>
e
```

**Response:**
```bencode
d
  1:t2:aa
  1:y1:r
  1:rd2:id20:<node_id>
e
```

### 3.2 `find_node`

Requests the k closest nodes to a target ID from the recipient's routing table.

**Query:**
```bencode
d
  1:t2:ab
  1:y1:q
  1:q9:find_node
  1:ad2:id20:<node_id>6:target20:<target_id>
e
```

**Response:**
```bencode
d
  1:t2:ab
  1:y1:r
  1:rd2:id20:<node_id>5:nodes<compact_nodes>
e
```

**Compact node info:** Each node is 26 bytes:
- 20 bytes: Node ID
- 4 bytes: IP address (IPv4, network byte order)
- 2 bytes: UDP port (network byte order)

For IPv6, compact node info is 38 bytes per node:
- 20 bytes: Node ID
- 16 bytes: IP address (IPv6)
- 2 bytes: UDP port

### 3.3 `get_peers`

Requests peers associated with a given infohash. If the recipient has no peers for the hash, it returns the k closest nodes instead.

**Query:**
```bencode
d
  1:t2:ac
  1:y1:q
  1:q9:get_peers
  1:ad2:id20:<node_id>9:info_hash20:<infohash>
e
```

**Response (peers found):**
```bencode
d
  1:t2:ac
  1:y1:r
  1:rd
    2:id20:<node_id>
    5:tokens4:<opaque_token>
    6:valuesl6:<compact_peer>6:<compact_peer>e
e
```

**Response (no peers, close nodes):**
```bencode
d
  1:t2:ac
  1:y1:r
  1:rd
    2:id20:<node_id>
    5:token4:<opaque_token>
    5:nodes<compact_nodes>
e
```

**Compact peer info:** 6 bytes per peer:
- 4 bytes: IP address (network byte order)
- 2 bytes: TCP port (network byte order)

**Token:** Opaque 4-byte token returned by the responding node. Required as proof of address for `announce_peer`. Typically derived from a hash of the infohash + node secret.

### 3.4 `announce_peer`

Registers the sender as a peer willing to serve a given infohash.

**Query:**
```bencode
d
  1:t2:ad
  1:y1:q
  1:q14:announce_peer
  1:ad
    2:id20:<node_id>
    9:info_hash20:<infohash>
    4:porti<tcp_port>e
    4:token4:<token>
    11:implied_porti0e
e
```

**Parameters:**

| Field | Type | Description |
|-------|------|-------------|
| `id` | 20 bytes | Sender's node ID |
| `info_hash` | 20 bytes | Target infohash |
| `port` | integer | TCP port where the peer serves the file (typically 6881-6889) |
| `token` | 4 bytes | Token received from `get_peers` response |
| `implied_port` | integer | If non-zero, use sender's UDP port instead of `port` |

**Response:** Standard success response with the node's ID.

---

## 4. Infohash Derivation (Peerdrive)

Peerdrive uses **SHA256** (64 hex chars) internally for content addressing. BT Mainline DHT uses **160-bit (20 byte)** infohashes. The conversion is:

### 4.1 SHA256 to BT Infohash

```
BT infohash = SHA256(content)[0:20]
```

Take the **first 20 bytes** (first 40 hex chars) of the SHA256 digest. This yields a 160-bit value compatible with BT DHT.

### 4.2 BT Infohash to SHA256 (reverse lookup)

Since truncation loses information, there is no deterministic reverse. Peerdrive implements:

```
SHA256 candidates = all stored SHA256 hashes where hash[0:20] == infohash
```

If multiple SHA256 hashes share the same first 20 bytes (collision probability: 2^-160), all are returned. In practice, collisions are negligible.

### 4.3 Protocol Invariants

| Side | Operation | Input | Output |
|------|-----------|-------|--------|
| Announce | SHA256 hex (64 chars) | `sha256[0:20]` | 20-byte infohash |
| Find | SHA256 hex (64 chars) | `sha256[0:20]` | 20-byte infohash for DHT query |
| Response | DHT peer addresses | — | Map of infohash → (ip, port) pairs |

---

## 5. Announce Flow

```
Uploader Node                    BT DHT Network
     │                                │
     │  1. File uploaded              │
     │     (SHA256 computed)          │
     │                                │
     │  2. infohash = sha256[0:20]    │
     │                                │
     │  3. POST /bt/announce      │
     │     {"hash": "<sha256_hex>"}   │
     │                                │
     │         ────────────────────── │
     │  4. find_node → bootstrap      │
     │  5. get_peers → get token      │
     │  6. announce_peer → close nodes│
     │         ────────────────────── │
     │                                │
     │  7. Return: {"status":         │
     │     "announced",               │
     │     "announced_to": N}         │
```

### Implementation Detail

The announce process in Go:

```go
func (bt *BTDHT) Announce(ctx context.Context, infoHash [20]byte) (int, error) {
    // 1. Bootstrap / ensure routing table is populated
    bt.bootstrap(ctx)

    // 2. Find k closest nodes to infoHash via iterative lookup
    closest := bt.findClosestNodes(ctx, infoHash)

    // 3. For each close node, call get_peers to obtain token
    // 4. Call announce_peer with token on each node
    announced := 0
    for _, node := range closest {
        token := bt.getPeers(ctx, node, infoHash)
        if token != nil {
            err := bt.announcePeer(ctx, node, infoHash, bt.announcePort, token)
            if err == nil {
                announced++
            }
        }
    }
    return announced, nil
}
```

---

## 6. Find Flow

```
Seeker Node                      BT DHT Network
     │                                │
     │  1. POST /bt/find          │
     │     {"hash": "<sha256_hex>"}   │
     │                                │
     │  2. infohash = sha256[0:20]    │
     │                                │
     │         ────────────────────── │
     │  3. find_node → bootstrap      │
     │  4. get_peers(infoHash)        │
     │     → iterative lookup         │
     │  5. Collect peer addresses     │
     │         ────────────────────── │
     │                                │
     │  6. Return: {                  │
     │     "infohash": "<hex_40>",    │
     │     "peers": [                 │
     │       {"ip": "...", "port": N} │
     │     ],                         │
     │     "sources": N               │
     │   }                            │
```

---

## 7. Peerdrive BT DHT API Endpoints

All endpoints are registered under the `/bt/` prefix on the Peerdrive Gin router.

### 7.1 `GET /bt/status`

Returns the current state of the BT DHT node.

**Response (200):**
```json
{
  "enabled": true,
  "node_id": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "num_nodes": 142,
  "bootstrap_nodes": [
    "router.bittorrent.com:6881",
    "dht.transmissionbt.com:6881"
  ],
  "announced": 3,
  "listening_addr": "0.0.0.0:6881"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | bool | Whether BT DHT is enabled |
| `node_id` | string | Local node ID (20 bytes, hex-encoded) |
| `num_nodes` | int | Number of nodes in the routing table |
| `bootstrap_nodes` | []string | Bootstrap node addresses |
| `announced` | int | Count of infohashes announced by this node |
| `listening_addr` | string | Local UDP listen address |

### 7.2 `POST /bt/announce`

Announce a SHA256 hash to the BT DHT network.

**Request:**
```json
{
  "hash": "<64-char SHA256 hex>"
}
```

**Response (200):**
```json
{
  "status": "announced",
  "infohash": "<40-char hex (first 20 bytes of SHA256)>",
  "announced_to": 8
}
```

**Error response (400):**
```json
{
  "error": "invalid hash: must be 64 hex characters"
}
```

**Error response (503):**
```json
{
  "error": "BT DHT not enabled",
  "hint": "Set PEERDRIVE_BT_DHT_ENABLE=true"
}
```

### 7.3 `POST /bt/find`

Find peers for a given SHA256 hash via the BT DHT network.

**Request:**
```json
{
  "hash": "<64-char SHA256 hex>"
}
```

**Response (200):**
```json
{
  "infohash": "<40-char hex>",
  "peers": [
    {"ip": "192.168.1.100", "port": 6881},
    {"ip": "10.0.0.5", "port": 6882}
  ],
  "num_peers": 2
}
```

**Error response (404):**
```json
{
  "infohash": "<40-char hex>",
  "peers": [],
  "num_peers": 0,
  "message": "no peers found for this infohash"
}
```

---

## 8. Configuration

### 8.1 Environment Variables

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `PEERDRIVE_BT_DHT_ENABLE` | bool | `false` | Enable BT Mainline DHT node |
| `PEERDRIVE_BT_DHT_LISTEN` | string | `"0.0.0.0:6881"` | UDP listen address for DHT |

### 8.2 Go Config Struct

```go
type Config struct {
    // ... existing fields ...

    BTDHTEnable   bool   // PEERDRIVE_BT_DHT_ENABLE
    BTDHTListen   string // PEERDRIVE_BT_DHT_LISTEN
}
```

### 8.3 Startup Sequence

```
Peerdrive server start
  │
  ├─ Load config (env vars)
  │
  ├─ If PEERDRIVE_BT_DHT_ENABLE=true:
  │    ├─ Create UDP listener on PEERDRIVE_BT_DHT_LISTEN
  │    ├─ Initialize routing table (k-buckets)
  │    ├─ Generate random 160-bit node ID
  │    ├─ Bootstrap via well-known nodes
  │    └─ Start background goroutine for:
  │         ├─ KRPC query handler (UDP packet loop)
  │         ├─ Periodic routing table refresh (every 15 min)
  │         └─ Node expiry / bucket splitting
  │
  └─ Register /bt/* routes on Gin router
```

---

## 9. DHT Node Lifecycle

### 9.1 Routing Table Maintenance

| Event | Action |
|-------|--------|
| Incoming query from unknown node | Insert into routing table |
| k-bucket full | Ping least-recently-seen node; if no response, replace |
| Node unresponsive for 15 min | Mark as stale, evict if bucket full |
| New bucket split | Occurs when own node ID falls within bucket range |

### 9.2 Token Management

Tokens are generated per `get_peers` response and must match for `announce_peer`.

Peerdrive token derivation:

```go
func (bt *BTDHT) generateToken(infoHash [20]byte) [4]byte {
    secret := bt.tokenSecret // rotated every 5 minutes
    h := sha256.New()
    h.Write(secret[:])
    h.Write(infoHash[:])
    return [4]byte(h.Sum(nil)[:4])
}
```

Tokens are validated within a 10-minute window (current + previous secret).

### 9.3 KRPC Timeout & Retry

| Parameter | Value |
|-----------|-------|
| Initial timeout | 2 seconds |
| Max retries | 3 |
| Backoff | 2x (2s → 4s → 8s) |
| Max concurrent queries | 3 (α = 3) |

---

## 10. Dual-Stack Integration

BT DHT discovery feeds into Peerdrive's unified provider lookup:

```
/bt/announce  ──┐
                     ├─► Peerdrive Provider Registry ──► /p2p/fetch
/bt/find     ───┘                                    /p2p/sync
```

When both libp2p DHT and BT DHT are enabled, a `/p2p/dual/find` endpoint queries both networks in parallel and merges results.

---

## 11. Security Considerations

### 11.1 Sybil Resistance

BT Mainline DHT has no built-in Sybil resistance. Node IDs are random self-generated values. Peerdrive mitigates via:

- Using libp2p's cryptographic peer identity for actual data transfer
- BT DHT is used only for discovery, not trust
- All transferred data is verified by SHA256 content hash

### 11.2 Token Replay

Tokens are time-bounded (5-minute secret rotation) to prevent replay attacks from nodes that snoop `get_peers` responses.

### 11.3 Rate Limiting

Peerdrive limits KRPC processing to 100 queries/second per source IP to prevent amplification attacks and resource exhaustion.

---

## 12. References

- [BEP 5: DHT Protocol](https://www.bittorrent.org/beps/bep_0005.html) — Mainline DHT specification
- [BEP 43: Read-only DHT Nodes](https://www.bittorrent.org/beps/bep_0043.html) — DHT node capabilities
- [Kademlia: Peer-to-peer Routing Based on the XOR Metric](https://pdos.csail.mit.edu/~petar/papers/maymounkov-kademlia-lncs.pdf) — Original Kademlia paper
- [libp2p DHT spec](https://github.com/libp2p/specs/tree/master/kad-dht) — libp2p Kademlia DHT specification
