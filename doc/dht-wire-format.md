# DHT Message Format

## BT DHT — KRPC (bencode over UDP)

Based on BEP 5 / Mainline DHT, using KRPC protocol, UDP transport, bencode encoding.

### Common Message Structure

```
UDP Datagram
  └─ bencode dictionary (Msg)
       ├─ t: string    ← Transaction ID (usually 2 bytes)
       ├─ y: "q"|"r"|"e"  ← Query/Response/Error
       ├─ q: string    ← Query only: "ping"|"find_node"|"get_peers"|"announce_peer"
       ├─ a: dict      ← Query only: Arguments (MsgArgs)
       ├─ r: dict      ← Response only: Return value (Return)
       ├─ e: [int str] ← Error only: [error code, message]
       ├─ ip: bytes    ← Optional: Sender IP (BEP 7)
       ├─ ro: int      ← Optional: Read-only flag (BEP 43)
       └─ v: string    ← Optional: Client identifier
```

### MsgArgs (Query Parameters `a`)

| Field | Type | Method | Description |
|------|------|------|------|
| `id` | 20 bytes | **All** | Sender node ID |
| `info_hash` | 20 bytes | get_peers, announce_peer | Target infohash |
| `target` | 20 bytes | find_node, get, sample_infohashes | Kademlia lookup target |
| `token` | string | announce_peer, put, get | Write token (from get_peers) |
| `port` | int | announce_peer | Download port |
| `implied_port` | bool | announce_peer | Whether to use DHT port as download port |
| `want` | ["n4"]/["n6"] | All | BEP 32: desired address family |
| `noseed` | int | get_peers | BEP 33: exclude pure seeders |
| `scrape` | int | get_peers | BEP 33: statistics only |
| `v` | bencode | get/put (BEP 44) | Stored value |
| `seq` | int | get/put (BEP 44) | Mutable item sequence number |
| `cas` | int | put (BEP 44) | Compare-And-Swap |
| `k` | 32 bytes | get/put (BEP 44) | Ed25519 public key |
| `salt` | bytes | get/put (BEP 44) | Salt (≤64 bytes) |
| `sig` | 64 bytes | put (BEP 44) | Ed25519 signature |

### Return (Response `r`)

| Field | Type | Method | Description |
|------|------|------|------|
| `id` | 20 bytes | **All** | Responder node ID |
| `nodes` | 26 bytes × n | find_node, get_peers | Compact IPv4 node list |
| `nodes6` | 38 bytes × n | find_node, get_peers | Compact IPv6 node list |
| `token` | string | get_peers | Write token |
| `values` | [NodeAddr] | get_peers | Peer addresses holding the infohash |
| `v` | bencode | get (BEP 44) | Value data |
| `k` | 32 bytes | get (BEP 44) | Ed25519 public key |
| `sig` | 64 bytes | get (BEP 44) | Ed25519 signature |
| `seq` | int | get (BEP 44) | Sequence number |
| `BFsd` | bloom filter | get_peers (BEP 33) | Downloader bloom filter |
| `BFpe` | bloom filter | get_peers (BEP 33) | Seeder bloom filter |
| `samples` | [20 bytes] | sample_infohashes (BEP 51) | Infohash samples |
| `num` | int | sample_infohashes (BEP 51) | Total infohash count |
| `interval` | int | sample_infohashes (BEP 51) | Suggested retry interval |

### Compact Node Encoding

- **IPv4**: 26 bytes = `NodeID(20) + IP(4) + Port(2, big-endian)`
- **IPv6**: 38 bytes = `NodeID(20) + IP(16) + Port(2, big-endian)`

### BEP 44 Target Calculation

- **Immutable items** (no `k` field): `target = SHA1(bencode(v))`
- **Mutable items** (with `k` field): `target = SHA1(32-byte-pubkey || salt)`

**Limits**: bencoded `v` ≤ 1000 bytes, `salt` ≤ 64 bytes

### KRPC Error Codes

| Code | Meaning |
|----|------|
| 201 | General error |
| 202 | Server error |
| 203 | Protocol error |
| 204 | Unknown method |
| 205 | v field too large |
| 206 | Invalid signature |
| 207 | salt field too large |
| 301 | CAS hash mismatch |
| 302 | seq less than current |

### Query Methods Summary

| Method | Query Parameters (a) | Response (r) | Purpose |
|------|-------------|----------|------|
| `ping` | id | id | Keep-alive probe |
| `find_node` | id, target | nodes, nodes6 | Routing table lookup |
| `get_peers` | id, info_hash | values, nodes, nodes6, token | Find seeder nodes |
| `announce_peer` | id, info_hash, port, token, implied_port | id | Announce data possession |
| `get` (BEP 44) | id, target[, k, salt, seq] | v, k, sig, seq | Read DHT storage |
| `put` (BEP 44) | id, target, v, k, sig, seq, salt, cas | id | Write DHT storage |
| `sample_infohashes` (BEP 51) | id, target | id, nodes, nodes6, samples, num, interval | Batch sampling |

### BT DHT Configuration in This Project

```go
// p2p_bt/bt_dht.go
// Bootstrap nodes:
dht.NewServer(dht.Config{
    Addrs: []string{":6881"},
    BootstrapNodes: []Addr{
        "router.bittorrent.com:6881",
        "dht.transmissionbt.com:6881",
    },
})

// Announce: infoHash = SHA1(bencode(v)) / SHA1(pubkey || salt)
// Or: infoHash = sha256[:20] (truncated file hash)
```

---

## IPFS DHT — Protobuf over libp2p Streams

Based on Kademlia DHT (go-libp2p-kad-dht), Protobuf encoding, transported via libp2p multiplexed streams.

### Message Top-Level Structure

```protobuf
// dht.proto
message Message {
    enum MessageType {
        PUT_VALUE     = 0;  // Store value
        GET_VALUE     = 1;  // Read value
        ADD_PROVIDER  = 2;  // Announce provider
        GET_PROVIDERS = 3;  // Find providers
        FIND_NODE     = 4;  // Find node
        PING          = 5;  // Keep-alive
    }

    enum ConnectionType {
        NOT_CONNECTED  = 0;
        CONNECTED      = 1;
        CAN_CONNECT    = 2;
        CANNOT_CONNECT = 3;
    }

    message Peer {
        bytes          id         = 1;  // PeerID (libp2p CID)
        repeated bytes addrs      = 2;  // Multi-address list
        ConnectionType connection = 3;  // Connection status
    }

    MessageType       type           = 1;  // Message type
    int32             clusterLevelRaw = 10; // Coral cluster level (unused)
    bytes             key            = 2;  // Query key (CID)
    record.pb.Record  record         = 3;  // Value record
    repeated Peer     closerPeers    = 8;  // Nodes closer to key
    repeated Peer     providerPeers  = 9;  // Providers of key
}
```

### Record Sub-Structure

```protobuf
// record.proto
message Record {
    bytes  key          = 1;  // Record key
    bytes  value        = 2;  // Stored value
    string timeReceived = 5;  // Receive time (set by receiver)
    // Fields 3 (author), 4 (signature) removed
}
```

### Bitswap Message Format (Compatibility Layer)

Bitswap 1.2.0 protocol, protowire encoding, over libp2p streams:

```
Protocol ID: /ipfs/bitswap/1.0.0, /ipfs/bitswap/1.1.0, /ipfs/bitswap/1.2.0

Message {
    wantlist {
        entries  [Entry]  // List of wanted entries
        full     bool     // Whether this is a complete wantlist
    }
    blocks       [bytes]  // Legacy block data (1.0.0)
    payload      [Block]  // 1.2.0: carries block data
    pendingBytes int64    // 1.2.0: pending bytes count
}

Entry {
    block         bytes // CID bytes
    cancel        bool  // Cancel the want
    wantType      int   // 0=WANT_HAVE, 1=WANT_BLOCK
    sendDontHave  bool  // Return DONT_HAVE when no data
}

Block {
    prefix bytes  // CID prefix
    data   bytes  // Raw block data
}
```

### Message Type Comparison

| Type | BT DHT (KRPC) | IPFS DHT (Protobuf) |
|------|---------------|-------------------|
| Keep-alive | `ping` → id | `PING` (type=5) |
| Node lookup | `find_node` → nodes | `FIND_NODE` (type=4) → closerPeers |
| Store value | BEP 44 `put` → v, k, sig, seq | `PUT_VALUE` (type=0) → record |
| Read value | BEP 44 `get` → v, k, sig, seq | `GET_VALUE` (type=1) → record |
| Announce content | `announce_peer` → info_hash, port | `ADD_PROVIDER` (type=2) → key, providerPeers |
| Find content | `get_peers` → values, token | `GET_PROVIDERS` (type=3) → providerPeers |

### Key Format Comparison

| Attribute | BT DHT | IPFS DHT |
|------|--------|----------|
| Hash | SHA1 (20 bytes) | SHA2-256 (32 bytes) |
| Key identifier | `info_hash`: 20 bytes binary | `key`: CID v1 bytes (multihash + codec) |
| String form | 40 chars hex | `bafkrei...` (CID) |
| Content addressing | infohash = SHA1(file piece) or SHA1(v) | CID = multihash(SHA2-256(file)) |
| File hash truncation | SHA256[:20] → 20 bytes infohash | SHA256 → multihash → CID v1(raw) |

### IPFS DHT Configuration in This Project

```go
// service/p2p.go
dhtInst, _ := dht.New(ctx, h, dht.Mode(dht.ModeServer))
dhtInst.Bootstrap(ctx)

// Announce (ADD_PROVIDER):
dht.Provide(ctx, cidFromSha256(hash), true)

// Find (GET_PROVIDERS):
dht.FindProviders(ctx, cidFromSha256(hash))  // 30s timeout
dht.FindProvidersAsync(ctx, cidFromSha256(hash), limit)
```

### CID Construction Flow

```
SHA256 hex (64 chars)
  → decode → 32 bytes
  → multihash(0x12, 0x20, 32bytes)  // SHA2-256, 32 bytes length
  → cid.NewCidV1(cid.Raw, mh)       // Raw codec (0x55)
  → "bafkrei..."  (CIDv1 base32)
```

---

## Network Transport Differences Summary

| | BT DHT | IPFS DHT |
|--|--------|----------|
| Transport layer | UDP | TCP/QUIC (libp2p stream) |
| Serialization | bencode | Protocol Buffers (proto3) |
| Protocol identifier | No fixed identifier (implicit in DHT message) | libp2p protocol negotiation |
| Node ID | 20 bytes, SHA1 derived | libp2p PeerID (multihash) |
| Value size | ≤ 1000 bytes (v field) | No hard limit (bounded by stream protocol) |
| Authentication | Ed25519 signature (BEP 44) | Record signature (removed from proto) |
| DHT library | anacrolix/dht/v2 | go-libp2p-kad-dht |
| Local port | Configurable UDP (:6881) | libp2p listen port |
| Bootstrap | router.bittorrent.com + transmissionbt.com | libp2p default bootstrap nodes / static config |

---

## Appendix: BT DHT Verification Methods

The following operations run on local node (port 3001) or VPS relay (port 3000), all using `--noproxy` to avoid proxy interference.

### 1. Confirm BT DHT Enabled + Routing Table Size

```bash
curl -s --noproxy '*' http://localhost:3001/bt/status | python3 -m json.tool
```

**Expected Output**:
```json
{
    "enabled": true,
    "listen_addr": "0.0.0.0:6881",
    "num_nodes": 28
}
```

- `num_nodes` > 0 indicates the node has successfully bootstrapped into Mainline DHT
- Initially 0, usually grows to 8-40+ within 10-60 seconds after bootstrap
- Larger values mean a more complete routing table

### 2. BT DHT Global Status (With More Statistics)

```bash
curl -s --noproxy '*' http://localhost:3001/bt/stats | python3 -m json.tool
```

```json
{
    "dht_nodes": 28,
    "active_torrents": 0,
    "paused_torrents": 0,
    "completed": 0,
    "seeding": 0
}
```

### 3. Announce a Hash on BT DHT

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/bt/announce \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

**Expected**: `{"status":"announced on BT DHT"}`

Underlying flow:
1. Take first 20 bytes of SHA256 as BT infohash
2. Call `dht.Server.Announce(infoHash, port, false)`
3. KRPC `get_peers` → obtain token → KRPC `announce_peer` to register with nearest K nodes

### 4. Find Providers on BT DHT

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/bt/find \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

**Expected**:
```json
{
    "hash": "59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb",
    "peers": ["1.2.3.4:6881", "5.6.7.8:6881"],
    "count": 2
}
```

- Internally calls `s.Server.AnnounceTraversal(infoHash)` to traverse DHT
- Timeout 15 seconds
- If no one has announced this hash, `peers` is an empty array

### 5. Dual Network Simultaneous Announce (BT + IPFS)

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/p2p/dual/announce \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

```json
{"status":"announced on both networks"}
```

### 6. Dual Network Simultaneous Find (BT + IPFS)

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/p2p/dual/find \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

```json
{
    "hash": "59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb",
    "ipfs_peers": [],
    "bt_peers": ["1.2.3.4:6881"]
}
```

### 7. BEP 44 Immutable Item Storage Test

```bash
# Write
DATA=$(echo -n "Hello DHT from peerdrive" | base64 -w0)
RESP=$(curl -s --noproxy '*' -X POST http://localhost:3001/bt/bep44/put \
  -H 'Content-Type: application/json' \
  -d "{\"data\":\"$DATA\",\"mutable\":false}")
echo "$RESP" | python3 -m json.tool
TARGET=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['target'])")

# Read
curl -s --noproxy '*' -X POST http://localhost:3001/bt/bep44/get \
  -H 'Content-Type: application/json' \
  -d "{\"target\":\"$TARGET\"}" | python3 -m json.tool
```

### 8. BEP 51 Infohash Sampling

```bash
curl -s --noproxy '*' http://localhost:3001/bt/bep51/sample | python3 -m json.tool
```

```json
{
    "samples": ["<40-char-hex>", ...],
    "count": 8
}
```

Queries infohash samples from the nearest K nodes in the routing table, used to observe active content in the DHT network.

### Verification Criteria

| Metric | Healthy | Abnormal |
|------|------|------|
| `num_nodes` | > 0 and continuously growing | = 0 or stagnant, indicating bootstrap failure or firewall blocking |
| Announce response | `"status": "announced on BT DHT"` | Error or timeout |
| Find response | Returns 200, `peers` may be present/empty | HTTP error |
| BEP 44 round-trip | get after put returns same data | Data mismatch |
| BEP 51 sample | Returns samples list | Empty array or error |
| Port | UDP 6881 externally accessible | Firewall blocks UDP |

### Notes

- BT DHT uses **UDP**, firewall must allow UDP port (default 6881)
- NAT/firewall affects DHT bootstrap: if UDP is blocked, `num_nodes` stays at 0
- Bootstrap nodes are public: `router.bittorrent.com:6881` and `dht.transmissionbt.com:6881`
- Routing table population takes time: wait 30-60 seconds after bootstrap before checking `num_nodes`
- `/p2p/status` only shows IPFS/libp2p info; BT DHT-specific endpoints are under `/bt/` path
