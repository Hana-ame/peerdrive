# Peerdrive IPFS/libp2p Protocol Specification

> **Version:** 1.0  
> **Last updated:** 2026-04-27  
> **Scope:** libp2p-based peer-to-peer networking, content addressing, DHT discovery, and file exchange for Peerdrive storage nodes.

---

## 1. Protocol Stack

Peerdrive builds on the libp2p modular network stack. The following layers are used:

| Layer | Protocol / Component | Purpose |
|-------|---------------------|---------|
| Transport | TCP, QUIC, WebSocket | Raw byte transport between peers |
| Security | Noise (libp2p automatic handshake) | Encrypted, authenticated communication |
| Stream Multiplexing | Yamux | Multiple logical streams over a single connection |
| NAT Traversal | AutoNAT v2, DCUtR, NAT-PMP (optional) | Determine reachability, punch holes |
| Relay | Circuit Relay v2 | Relay traffic through public nodes when direct connection is impossible |
| Peer Discovery | mDNS (LAN), Kademlia DHT (global) | Find peers on local network or globally |
| Application | `/peerdrive/exchange/1.0.0`, `/peerdrive/chunk/1.0.0`, `/peerdrive/announce/1.0.0`, `/peerdrive/request/1.0.0` | File transfer, announcement, and request |

---

## 2. libp2p Host

Each Peerdrive node creates a libp2p `host.Host` with the following defaults:

- **Listen addresses:** `/ip4/0.0.0.0/tcp/0` (random OS-assigned TCP port)
- **Peer ID:** Derived from the host's Ed25519 keypair, encoded as a base58btch multihash (`12D3KooW...`)
- **Agent version:** `peerdrive/1.0`

The host is responsible for managing connections, streams, and protocol handlers.

---

## 3. Exchange Protocol (`/peerdrive/exchange/1.0.0`)

The Exchange protocol is the primary mechanism for requesting and transferring file data between peers over a libp2p stream.

### 3.1 File Request

```
Request:  <64-character hex SHA256 hash>\n
```

- Request consists of exactly 64 lowercase hex characters followed by a newline (`\n`, 0x0a).
- The hash is the SHA-256 digest of the file content.

### 3.2 File Response (Success)

```
Response: OK <size-in-bytes>\n<raw binary data>
```

- `OK` indicates the file was found.
- `<size-in-bytes>` is the decimal ASCII representation of the file length.
- A newline follows the size.
- The raw binary file data follows immediately.
- The receiver should read exactly `<size-in-bytes>` bytes after the newline.

### 3.3 File Response (Error)

```
Response: ERR <message>\n
```

- `ERR` indicates the file was not found or an error occurred.
- `<message>` is a human-readable error description.
- The stream is closed after the error line.

### 3.4 SIZE Command

Before performing chunked transfers, the requesting peer may query the file size:

```
Request:  SIZE <64-character hex SHA256 hash>\n
Response: OK <size-in-bytes>\n
Response: ERR <message>\n
```

- The SIZE command shares the same response format as a file request but returns only the size, not the data.
- Used by the chunked transfer logic to determine file size before requesting chunks in parallel.

### 3.5 Exchange Protocol Handler (Server Side)

```python
# Pseudocode for the exchange stream handler
def handle_exchange(stream):
    reader = BufferedReader(stream)
    line = reader.read_line()  # read until \n

    if line starts with "SIZE ":
        hash = line[5:].strip()
        size = stat_storage(hash)  # look up file in storage dir
        if found:
            stream.write(f"OK {size}\n")
        else:
            stream.write("ERR not found\n")
        return

    hash = line.strip()
    if len(hash) != 64:
        stream.write(f"ERR invalid hash length {len(hash)}\n")
        return

    data = read_from_storage(hash)
    if data is None:
        stream.write("ERR not found\n")
        return

    stream.write(f"OK {len(data)}\n")
    stream.write(data)  # raw binary
```

### 3.6 Exchange Protocol Client (Requesting Side)

```python
# Pseudocode for requesting a file from a peer
def request_file(peer_id, hash):
    stream = host.new_stream(peer_id, "/peerdrive/exchange/1.0.0")
    stream.write(f"{hash}\n")

    line = stream.read_line()
    if line starts with "OK ":
        size = int(line[3:].strip())
        data = stream.read_exactly(size)
        verify_sha256(data, hash)
        return data
    else:
        raise Error(line)
```

---

## 4. Announce Protocol (`/peerdrive/announce/1.0.0`)

Used by a peer to broadcast that it holds a particular file.

### 4.1 Message Format

```
Request:  <64-character hex SHA256 hash>\n
Response: OK\n
```

- The receiving peer automatically calls `DHT.Provide()` to register itself as a provider for the announced content.

### 4.2 Behavior

1. Peer A opens a stream to Peer B with protocol ID `/peerdrive/announce/1.0.0`.
2. Peer A sends the SHA256 hash of the file it holds, followed by `\n`.
3. Peer B responds with `OK\n`.
4. Peer B asynchronously calls `DHT.Provide()` to announce to the DHT that it (or the announcing peer) can serve this content.
5. The stream is closed.

---

## 5. Request Protocol (`/peerdrive/request/1.0.0`)

A lightweight notification protocol used to signal file interest to WebSocket-connected browser peers.

### 5.1 Message Format

```
Request: <64-character hex SHA256 hash>\n
```

- No response is sent over the stream.
- The request is forwarded to the WebSocket hub for browser-based peers.

---

## 6. Chunk Protocol (`/peerdrive/chunk/1.0.0`)

Used for parallel chunked transfer of large files.

### 6.1 Message Format

```
Request:  CHUNK <64-char hex SHA256> <offset> <size>\n
Response: <raw binary chunk data>
Error:    ERR <message>\n
```

- `offset`: byte offset from the start of the file (decimal).
- `size`: number of bytes to read (max 262144 = 256 KB).

### 6.2 Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| Chunk size | 256 KB | Maximum size per chunk request |
| Concurrency | 8 | Maximum parallel chunk requests |
| Timeout | 5 minutes | Total transfer timeout |

---

## 7. CID Format

Peerdrive maps SHA256 content hashes to IPFS Content Identifiers (CIDv1) for DHT operations.

### 7.1 Conversion Pipeline

```
SHA256 hex string (64 chars)
    |
    v
Raw bytes (32 bytes, hex-decoded)
    |
    v
Multihash encoding (varint prefix + hash)
    |-- hash code:  0x12 (SHA2-256, 18 in decimal)
    |-- digest length: 0x20 (32 bytes)
    |-- digest: raw 32-byte SHA256 output
    |
    v
CIDv1 construction
    |-- version:  1 (CIDv1)
    |-- codec:    0x55 (raw binary, 85 in decimal)
    |-- multihash: the multihash blob from above
```

### 7.2 CID Binary Layout

```
+------------+--------+------------------+----------------+
| CIDv1 (1)  | Raw    | Multihash header | SHA256 digest  |
| (varint)   | (0x55) | (0x12 0x20)      | (32 bytes)     |
+------------+--------+------------------+----------------+
```

Total multihash: 34 bytes (2 prefix + 32 digest)  
Total CID: 36 bytes (2 prefix + 34 multihash)

### 7.3 Implementation Reference

```go
func cidFromSha256(hash string) cid.Cid {
    raw, _ := hex.DecodeString(hash)           // 32 bytes
    mhash, _ := mh.Encode(raw, mh.SHA2_256)     // multihash: 0x12 0x20 <32 bytes>
    return cid.NewCidV1(cid.Raw, mhash)         // CIDv1 with raw codec
}
```

### 7.4 CID String Representation

CIDs are represented in the base32lower encoding (default for CIDv1):

```
bafkqaaa... (human-readable string)
```

---

## 8. DHT Operations

Peerdrive uses the Kademlia DHT (`go-libp2p-kad-dht`) for content routing and peer discovery.

### 8.1 DHT Mode

- Mode: `ModeServer` — the node both stores provider records and responds to queries.
- Bucket size (k): 20 (libp2p default)
- Concurrency (alpha): 10 (libp2p default)

### 8.2 DHT Initialization

```
1. Create libp2p host
2. Create IpfsDHT with ModeServer
3. Call dht.Bootstrap(ctx) to warm up the routing table
4. The DHT runs in the background, discovering peers through
   bootstrap nodes and incoming queries
```

### 8.3 Provide

Registers this node as a provider for a given CID in the DHT.

```go
func (p *P2PService) AnnounceHash(hash string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    cid := cidFromSha256(hash)
    return p.DHT.Provide(ctx, cid, true)  // true = advertisement
}
```

- Timeout: 10 seconds
- `Provide` stores the peer's multiaddresses in the DHT so other nodes can find and connect to it.

### 8.4 FindProviders

Searches the DHT for nodes that have advertised a given CID.

```go
func (p *P2PService) FindProviders(hash string) ([]peer.AddrInfo, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    cid := cidFromSha256(hash)
    providers, err := p.DHT.FindProviders(ctx, cid)
    // Filter out self, return peer.AddrInfo list
}
```

- Timeout: 30 seconds
- Before searching, the service attempts to connect to any locally discovered peers to expand the DHT routing table.
- The node itself is filtered out of results.

### 8.5 DHT and Bootstrap Peers

The DHT routing table is populated by:
1. Bootstrap connections (explicit peer addresses)
2. mDNS-discovered peers (LAN)
3. Incoming connections from other peers
4. DHT query responses (closer nodes returned by remote peers)

---

## 9. mDNS Discovery

Peerdrive uses libp2p's built-in mDNS service for automatic peer discovery on the local network.

### 9.1 Configuration

- Service tag: `peerdrive-mdns`
- Protocol: mDNS (Multicast DNS), RFC 6762
- Scope: Local network segment only

### 9.2 Behavior

1. On startup, the mDNS service begins broadcasting the node's presence on the local network.
2. Other Peerdrive nodes on the same network segment discover each other automatically.
3. Discovered peers are stored in `discovered` map and trigger `HandlePeerFound`.
4. The connection manager automatically connects to discovered peers.

---

## 10. Ping Protocol

libp2p's built-in ping protocol is used for latency measurement and connectivity checks.

### 10.1 API

```go
func (p *P2PService) PingPeer(ctx context.Context, peerID peer.ID) (time.Duration, error) {
    result := p.Ping.Ping(ctx, peerID)
    select {
    case res := <-result:
        return res.RTT, res.Error
    case <-ctx.Done():
        return 0, ctx.Err()
    }
}
```

- Uses libp2p's `/ipfs/ping/1.0.0` protocol.
- Returns round-trip time as a `time.Duration`.
- The HTTP API exposes this at `GET /p2p/ping/:peer_id`.

---

## 11. Bootstrap Flow

On startup, the P2P service performs the following initialization sequence:

```
1. Create libp2p Host
   |
2. Create IpfsDHT (ModeServer)
   |
3. Bootstrap DHT
   |
4. Set stream handlers:
   |-- /peerdrive/exchange/1.0.0
   |-- /peerdrive/announce/1.0.0
   |-- /peerdrive/request/1.0.0
   |
5. Start mDNS discovery (if enabled)
   |
6. Connect to bootstrap peer (if configured)
   |
7. Start WebSocket request processor
   |
8. Initialize ConnectionManager + ChunkedTransfer
   |
9. Start auto-connect from discovered peers
   |
10. Start heartbeat loop (30s interval)
```

---

## 12. Connection Management

### 12.1 Heartbeat

- Interval: 30 seconds
- Checks all known peers for connectedness.
- Reconnects to any peer that is not in `Connected` state.

### 12.2 Reconnection

- Initial interval: 10 seconds
- Max backoff: 5 minutes
- Connection timeout: 15 seconds

### 12.3 Statistics

The connection manager tracks:
- `known_peers`: number of tracked peers
- `connected_peers`: number of currently connected peers
- `reconnect_attempts`: total reconnection attempts
- `successful_conns`: total successful connections
- `failed_conns`: total failed connections

---

## 13. File Transfer Flow

### 13.1 Small File Transfer (Exchange)

```
Requesting Peer                  Providing Peer
      |                               |
      |--- [/peerdrive/exchange] ---->|
      |    "<hash>\n"                  |
      |                               |-- lookup hash in storage
      |<-- "OK <size>\n<data>" -------|
      |    or "ERR not found\n"        |
```

### 13.2 Large File Transfer (Chunked)

```
Requesting Peer                  Providing Peer(s)
      |                               |
      |--- SIZE <hash>\n ------------>|
      |<-- OK <total_size>\n ---------|
      |                               |
      |--- CHUNK <hash> 0 262144 ---->|
      |--- CHUNK <hash> 262144 262144 ->|
      |--- CHUNK <hash> 524288 262144 ->|
      |   ... (8 concurrent)           |
      |<-- <chunk data> --------------|
      |<-- <chunk data> --------------|
      |                               |
      |-- verify SHA256 of assembled file
```

### 13.3 File Discovery and Fetch (Complete Flow)

```
1. Requesting Peer
   |-- POST /p2p/announce (optional, if it has the file)
   |-- GET /sha256sum/:hash
       |
       v
2. Check local storage
   |-- Found? Return file immediately
   |
3. Check file_providers table
   |-- Found local provider? Return file
   |
4. P2P discovery
   |-- DHT.FindProviders(cid)
   |-- Connect to each provider
   |-- Exchange protocol: request file
   |
5. Return file to caller
```

---

## 14. Peer Identity

### 14.1 Peer ID Format

- Generated from Ed25519 keypair.
- Encoded as a base58btch multihash.
- Format: `12D3KooW<44 base58 characters>` (52 characters total).

Example:
```
12D3KooWJk3CVmBQ2KJQcz3JLBWzJLn2TQJc8YQkpmLmZn6ZwY4Z
```

### 14.2 Multiaddress Format

```
/ip4/<IPv4>/tcp/<port>/p2p/<PeerID>
/ip6/<IPv6>/tcp/<port>/p2p/<PeerID>
/ip4/<IPv4>/udp/<port>/quic/p2p/<PeerID>
```

---

## 15. HTTP API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/ping` | Health check (returns "pong") |
| GET | `/p2p/status` | P2P status including peer ID, addresses, connection stats, active transfers |
| GET | `/p2p/node` | Local node info (peer ID, multiaddrs) |
| GET | `/p2p/peers` | Connected peers list |
| GET | `/p2p/discovered` | Discovered peers list (with addresses) |
| GET | `/p2p/ping/:peer_id` | Ping a specific peer (returns RTT) |
| POST | `/p2p/connect` | Connect to a peer by multiaddress |
| POST | `/p2p/announce` | Announce a file hash to the network |
| POST | `/p2p/fetch` | Fetch a collection from P2P network |
| POST | `/p2p/sync` | Sync files from a specific peer |
| POST | `/p2p/push` | Push collection to peers |
| POST | `/p2p/request-file` | Broadcast file request to connected peers |
| GET | `/p2p/ws/info` | WebSocket connection info |
| GET | `/p2p/bt/status` | BitTorrent DHT status |
| POST | `/p2p/bt/announce` | Announce on BitTorrent DHT |
| POST | `/p2p/bt/find` | Find providers via BitTorrent DHT |
| POST | `/p2p/dual/announce` | Announce on both IPFS and BT DHT |
| POST | `/p2p/dual/find` | Find providers on both networks |

---

## 16. Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PEERDRIVE_P2P_ENABLE` | `true` | Enable P2P networking |
| `PEERDRIVE_P2P_LISTEN` | `/ip4/0.0.0.0/tcp/0` | libp2p listen address(es) |
| `PEERDRIVE_P2P_LISTEN_V6` | `""` | IPv6 listen address (optional) |
| `PEERDRIVE_BOOTSTRAP_PEER` | `""` | Bootstrap peer multiaddress |
| `PEERDRIVE_MDNS_ENABLE` | `true` | Enable mDNS LAN discovery |
| `PEERDRIVE_RELAY_ENABLE` | `false` | Enable circuit relay |
| `PEERDRIVE_RELAY_MODE` | `client` | Relay mode: `client`, `server`, or `off` |
| `PEERDRIVE_STATIC_RELAYS` | `""` | Comma-separated static relay multiaddresses |
| `PEERDRIVE_HOLE_PUNCH` | `true` | Enable NAT hole punching (DCUtR) |
| `PEERDRIVE_PUBLIC_REACHABLE` | `false` | Node is publicly reachable |
| `PEERDRIVE_AUTO_NAT` | `true` | Enable AutoNAT v2 |
| `PEERDRIVE_NAT_PORTMAP` | `false` | Enable NAT-PMP/UPnP port mapping |
| `PEERDRIVE_PUBLIC_DOMAIN` | `""` | Public domain name for the node |
| `PEERDRIVE_STORAGE_DIR` | `./storage` | Content-addressed storage root |

---

## 17. Dual-Stack: IPFS + BitTorrent DHT

Peerdrive optionally operates a dual P2P stack:

- **IPFS DHT** (`go-libp2p-kad-dht`): Primary content routing for IPFS/libp2p peers.
- **BitTorrent DHT** (Mainline DHT): Optional secondary routing using KRPC over UDP for compatibility with BT protocols.

The `DualP2PService` coordinates announcements and lookups across both networks:

```go
type DualP2PService struct {
    ipfs    *P2PService     // IPFS/libp2p DHT
    bt      *p2p_bt.BTDHTService  // BitTorrent Mainline DHT
}
```

- `DualAnnounce`: Announces a hash on both IPFS DHT (`Provide`) and BT DHT (`announce_peer`).
- `DualFindProviders`: Searches both networks in parallel and merges results.

---

## 18. Storage Layout

Files are stored on disk using SHA256 content addressing:

```
<storage_dir>/
  <first 2 hex chars of hash>/
    <full 64-char hex hash>
```

Example:
```
./storage/
  a1/
    a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2
  ff/
    ff00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff00
```

---

## 19. Security

- **Transport encryption**: All libp2p connections use Noise protocol with Curve25519 and AES-256-GCM or ChaCha20-Poly1305.
- **Peer identity**: Verified via Ed25519 signatures during the Noise handshake.
- **Data integrity**: All transferred files are verified against their SHA256 hash before use.
- **Path traversal prevention**: AnonCollection entry paths are validated to prevent directory traversal.

---

## 20. Bitswap Protocol (Standard IPFS)

Peerdrive integrates standard IPFS Bitswap via the **boxo** library (`github.com/ipfs/boxo` v0.37). This enables full interoperability with the IPFS network — Peerdrive nodes can fetch from and serve to standard IPFS nodes.

### 20.1 Bitswap Components

| Component | Implementation | Purpose |
|-----------|---------------|---------|
| Bitswap Client | `bitswap.New()` via boxo | Request blocks from IPFS peers via DHT discovery |
| Bitswap Server | `bsnet.NewFromIpfsHost()` via boxo | Serve blocks to IPFS peers (auto-registers `/ipfs/bitswap/*` handlers) |
| Blockstore | `peerdriveBlockstore` (custom) | CID → SHA-256 content-addressed storage, zero file duplication |
| Content Routing | `dht.IpfsDHT` (shared from P2PService) | DHT provider lookup and announcement |

### 20.2 Bitswap Stream Handlers

Registered automatically by boxo on the shared libp2p host:

```
/ipfs/bitswap/1.0.0  (legacy)
/ipfs/bitswap/1.1.0
/ipfs/bitswap/1.2.0  (with payload extension)
```

These replace the hand-rolled protobuf parsing previously in `IPFSCompatLayer`.

### 20.3 Blockstore Architecture

```
Request: GetBlock(CID)
    │
    ▼
peerdriveBlockstore.Has(CID)
    │
    ▼
CID → Multihash → SHA-256 digest (32 bytes)
    │
    ▼
storage/<sha256[:2]>/<sha256>
    │
    ├── exists? → blocks.NewBlockWithCid(data, cid) → return
    └──不存在? → Bitswap client → DHT FindProviders → 从 IPFS 节点拉取
```

**关键设计:** Blockstore 不复制文件。CID 对应的数据直接从 SHA-256 内容寻址存储读取。文件在 Peerdrive collection 中存在即自动成为 IPFS 可提供的内容。

### 20.4 Bitswap 操作

```
// 获取块 (本地优先，再网络)
blk := bitswap.GetBlock(ctx, cid)
    ├── 本地 blockstore.Has(cid)? → 直接返回
    └── 通过 DHT 查找提供者 → 连接 → 请求块 → Put 到 blockstore → 返回
```

### 20.5 与旧 IPFSCompatLayer 的关系

| 功能 | 旧 (IPFSCompatLayer) | 新 (IPFSService + boxo) |
|------|---------------------|------------------------|
| Bitswap 解析 | 手动 protobuf (`protowire`) | boxo 自动处理 |
| AddFile | 复制文件到 `ipfs-blocks/` | 不复制，CID 直接映射 SHA-256 路径 |
| Stream handlers | 手动 `SetStreamHandler` | boxo `NewFromIpfsHost` 自动注册 |
| Pin | 无 | 文件在 storage 即 pinned |

---

## 21. IPFSService (boxo 集成层)

### 21.1 结构

```go
type IPFSService struct {
    host       host.Host          // 复用 P2PService.Host
    dht        *dht.IpfsDHT       // 复用 P2PService.DHT
    storageDir string             // SHA-256 内容寻址存储根目录
    blockstore blockstore.Blockstore  // peerdriveBlockstore
    bswap      *bitswap.Bitswap   // boxo Bitswap 客户端 + 服务端
}
```

### 21.2 初始化流程

```
NewIPFSService(ctx, p2p, storageDir)
    │
    ├── 1. newPeerdriveBlockstore(storageDir)
    │      CID → SHA-256 路径映射
    │
    ├── 2. bsnet.NewFromIpfsHost(p2p.Host)
    │      注册 /ipfs/bitswap/* stream handlers
    │
    ├── 3. bitswap.New(ctx, network, dht, blockstore)
    │      Bitswap 客户端 + 服务端启动
    │
    └── 4. network.Start(bitswap)
           开始接收和处理 Bitswap 请求
```

### 21.3 公开方法

| 方法 | 说明 |
|------|------|
| `FetchByCID(ctx, cid)` | Bitswap 获取（本地 → DHT → P2P） |
| `Provide(ctx, sha256)` | 通过 DHT 宣布提供 SHA-256 文件的 CID |
| `ProvideAll(ctx)` | 遍历所有本地文件并 announce 到 DHT |
| `FindProviders(ctx, cid, n)` | DHT 查找 CID 提供者 |
| `HasCID(cid)` | 检查 CID 是否在本地存储中 |
| `GetBlock(cid)` | 读取 CID 的原始块数据 |
| `AddToBlockstore(sha256)` | 将 SHA-256 文件注册为 IPFS 块 |
| `BlockCount()` | 本地可提供的文件数 |

### 21.4 CID ↔ SHA-256 双向转换

```go
// SHA-256 → CID (pkg/hashutil)
SHA256ToCID("e3b0c442...855")  → "bafkreihk7nxx..."

// CID → SHA-256 (pkg/hashutil, 新增)
CIDToSHA256("bafkreihk7nxx...") → "e3b0c442...855"
```

---

## 22. IPFS Provider (下载策略)

### 22.1 两层获取

```
IPFSProvider.GetReader(cid)
    │
    ├── 1. BitswapFetcher (IPFSService.FetchByCID)
    │      ├── 本地 blockstore → 命中则返回
    │      └── DHT + Bitswap 网络获取
    │
    └── 2. HTTP 网关回退 (fallback)
           ├── 多个网关并发竞速
           ├── 指数退避重试 (3次, 500ms→5s)
           └── 第一个成功者胜出
```

### 22.2 配置

| 环境变量 | 默认值 | 说明 |
|----------|--------|------|
| `PEERDRIVE_IPFS_GATEWAY_ENABLE` | `true` | 是否启用 IPFS 网关回退 |
| `PEERDRIVE_IPFS_GATEWAYS` | `ipfs.io,cloudflare-ipfs.com,dweb.link` | 网关 URL 列表 |
| `PEERDRIVE_IPFS_COMPAT` | `false` | 启用 IPFS 兼容层（Bitswap 服务端） |
