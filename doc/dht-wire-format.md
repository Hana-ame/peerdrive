# DHT 报文格式

## BT DHT — KRPC (bencode over UDP)

基于 BEP 5 / Mainline DHT，使用 KRPC 协议，UDP 传输，bencode 编码。

### 通用报文结构

```
UDP 数据报
  └─ bencode 字典 (Msg)
       ├─ t: string    ← 事务 ID (通常2字节)
       ├─ y: "q"|"r"|"e"  ← 查询/响应/错误
       ├─ q: string    ← 仅查询: "ping"|"find_node"|"get_peers"|"announce_peer"
       ├─ a: dict      ← 仅查询: 参数 (MsgArgs)
       ├─ r: dict      ← 仅响应: 返回值 (Return)
       ├─ e: [int str] ← 仅错误: [错误码, 消息]
       ├─ ip: bytes    ← 可选: 发送者IP (BEP 7)
       ├─ ro: int      ← 可选: 只读标志 (BEP 43)
       └─ v: string    ← 可选: 客户端标识
```

### MsgArgs (查询参数 `a`)

| 字段 | 类型 | 方法 | 说明 |
|------|------|------|------|
| `id` | 20 bytes | **所有** | 发送者节点 ID |
| `info_hash` | 20 bytes | get_peers, announce_peer | 目标 infohash |
| `target` | 20 bytes | find_node, get, sample_infohashes | Kademlia 查找目标 |
| `token` | string | announce_peer, put, get | 写入令牌 (来自 get_peers) |
| `port` | int | announce_peer | 下载端口 |
| `implied_port` | bool | announce_peer | 是否用 DHT 端口作为下载端口 |
| `want` | ["n4"]/["n6"] | 所有 | BEP 32: 想要的地址族 |
| `noseed` | int | get_peers | BEP 33: 排除纯做种者 |
| `scrape` | int | get_peers | BEP 33: 仅统计 |
| `v` | bencode | get/put (BEP 44) | 存储的值 |
| `seq` | int | get/put (BEP 44) | 可变项序列号 |
| `cas` | int | put (BEP 44) | Compare-And-Swap |
| `k` | 32 bytes | get/put (BEP 44) | Ed25519 公钥 |
| `salt` | bytes | get/put (BEP 44) | 盐值 (≤64 bytes) |
| `sig` | 64 bytes | put (BEP 44) | Ed25519 签名 |

### Return (响应 `r`)

| 字段 | 类型 | 方法 | 说明 |
|------|------|------|------|
| `id` | 20 bytes | **所有** | 响应者节点 ID |
| `nodes` | 26 bytes × n | find_node, get_peers | 紧凑 IPv4 节点列表 |
| `nodes6` | 38 bytes × n | find_node, get_peers | 紧凑 IPv6 节点列表 |
| `token` | string | get_peers | 写入令牌 |
| `values` | [NodeAddr] | get_peers | 持有 infohash 的 peer 地址 |
| `v` | bencode | get (BEP 44) | 值数据 |
| `k` | 32 bytes | get (BEP 44) | Ed25519 公钥 |
| `sig` | 64 bytes | get (BEP 44) | Ed25519 签名 |
| `seq` | int | get (BEP 44) | 序列号 |
| `BFsd` | bloom filter | get_peers (BEP 33) | 下载者布隆过滤器 |
| `BFpe` | bloom filter | get_peers (BEP 33) | 做种者布隆过滤器 |
| `samples` | [20 bytes] | sample_infohashes (BEP 51) | infohash 样本 |
| `num` | int | sample_infohashes (BEP 51) | 总 infohash 数 |
| `interval` | int | sample_infohashes (BEP 51) | 建议重试间隔 |

### 紧凑节点编码

- **IPv4**: 26 bytes = `节点ID(20) + IP(4) + Port(2, big-endian)`
- **IPv6**: 38 bytes = `节点ID(20) + IP(16) + Port(2, big-endian)`

### BEP 44 目标计算

- **不可变项**（无 `k` 字段）: `target = SHA1(bencode(v))`
- **可变项**（有 `k` 字段）: `target = SHA1(32-byte-pubkey || salt)`

**限制**: bencoded `v` ≤ 1000 bytes，`salt` ≤ 64 bytes

### KRPC 错误码

| 码 | 含义 |
|----|------|
| 201 | 通用错误 |
| 202 | 服务器错误 |
| 203 | 协议错误 |
| 204 | 方法未知 |
| 205 | v 字段过大 |
| 206 | 签名无效 |
| 207 | salt 字段过大 |
| 301 | CAS 哈希不匹配 |
| 302 | seq 小于当前 |

### 查询方法汇总

| 方法 | 查询参数 (a) | 响应 (r) | 用途 |
|------|-------------|----------|------|
| `ping` | id | id | 保活探测 |
| `find_node` | id, target | nodes, nodes6 | 路由表查找 |
| `get_peers` | id, info_hash | values, nodes, nodes6, token | 查找种子节点 |
| `announce_peer` | id, info_hash, port, token, implied_port | id | 宣告持有数据 |
| `get` (BEP 44) | id, target[, k, salt, seq] | v, k, sig, seq | 读取 DHT 存储 |
| `put` (BEP 44) | id, target, v, k, sig, seq, salt, cas | id | 写入 DHT 存储 |
| `sample_infohashes` (BEP 51) | id, target | id, nodes, nodes6, samples, num, interval | 批量采样 |

### 项目中的 BT DHT 配置

```go
// p2p_bt/bt_dht.go
// 引导节点:
dht.NewServer(dht.Config{
    Addrs: []string{":6881"},
    BootstrapNodes: []Addr{
        "router.bittorrent.com:6881",
        "dht.transmissionbt.com:6881",
    },
})

// Announce: infoHash = SHA1(bencode(v)) / SHA1(pubkey || salt)
// 或: infoHash = sha256[:20] (文件 hash 截断)
```

---

## IPFS DHT — Protobuf over libp2p Streams

基于 Kademlia DHT (go-libp2p-kad-dht)，Protobuf 编码，通过 libp2p 多路复用流传输。

### Message 顶层结构

```protobuf
// dht.proto
message Message {
    enum MessageType {
        PUT_VALUE     = 0;  // 存储值
        GET_VALUE     = 1;  // 读取值
        ADD_PROVIDER  = 2;  // 宣告提供者
        GET_PROVIDERS = 3;  // 查找提供者
        FIND_NODE     = 4;  // 查找节点
        PING          = 5;  // 保活
    }

    enum ConnectionType {
        NOT_CONNECTED  = 0;
        CONNECTED      = 1;
        CAN_CONNECT    = 2;
        CANNOT_CONNECT = 3;
    }

    message Peer {
        bytes          id         = 1;  // PeerID (libp2p CID)
        repeated bytes addrs      = 2;  // 多地址列表
        ConnectionType connection = 3;  // 连接状态
    }

    MessageType       type           = 1;  // 消息类型
    int32             clusterLevelRaw = 10; // Coral 集群层级 (未使用)
    bytes             key            = 2;  // 查询键 (CID)
    record.pb.Record  record         = 3;  // 值记录
    repeated Peer     closerPeers    = 8;  // 靠近 key 的节点
    repeated Peer     providerPeers  = 9;  // key 的提供者
}
```

### Record 子结构

```protobuf
// record.proto
message Record {
    bytes  key          = 1;  // 记录的键
    bytes  value        = 2;  // 存储的值
    string timeReceived = 5;  // 接收时间 (由接收者设置)
    // 字段 3(author), 4(signature) 已移除
}
```

### Bitswap 报文格式 (兼容层)

Bitswap 1.2.0 协议，protowire 编码，通过 libp2p 流：

```
协议 ID: /ipfs/bitswap/1.0.0, /ipfs/bitswap/1.1.0, /ipfs/bitswap/1.2.0

Message {
    wantlist {
        entries  [Entry]  // 需要的条目列表
        full     bool     // 是否是完整 wantlist
    }
    blocks       [bytes]  // 遗留块数据 (1.0.0)
    payload      [Block]  // 1.2.0: 携带块数据
    pendingBytes int64    // 1.2.0: 待处理字节数
}

Entry {
    block         bytes // CID 字节
    cancel        bool  // 取消需要
    wantType      int   // 0=WANT_HAVE, 1=WANT_BLOCK
    sendDontHave  bool  // 无数据时返回 DONT_HAVE
}

Block {
    prefix bytes  // CID 前缀
    data   bytes  // 原始块数据
}
```

### 消息类型对比

| 类型 | BT DHT (KRPC) | IPFS DHT (Protobuf) |
|------|---------------|-------------------|
| 保活 | `ping` → id | `PING` (type=5) |
| 节点查找 | `find_node` → nodes | `FIND_NODE` (type=4) → closerPeers |
| 存储值 | BEP 44 `put` → v, k, sig, seq | `PUT_VALUE` (type=0) → record |
| 读取值 | BEP 44 `get` → v, k, sig, seq | `GET_VALUE` (type=1) → record |
| 宣告内容 | `announce_peer` → info_hash, port | `ADD_PROVIDER` (type=2) → key, providerPeers |
| 查找内容 | `get_peers` → values, token | `GET_PROVIDERS` (type=3) → providerPeers |

### 键格式对比

| 属性 | BT DHT | IPFS DHT |
|------|--------|----------|
| 哈希 | SHA1 (20 bytes) | SHA2-256 (32 bytes) |
| 键标识 | `info_hash`: 20 bytes binary | `key`: CID v1 bytes (multihash + codec) |
| 字符串形式 | 40 chars hex | `bafkrei...` (CID) |
| 内容寻址 | infohash = SHA1(file piece) 或 SHA1(v) | CID = multihash(SHA2-256(file)) |
| 文件 hash 截断 | SHA256[:20] → 20 bytes infohash | SHA256 → multihash → CID v1(raw) |

### 项目中的 IPFS DHT 配置

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

### CID 构造流程

```
SHA256 hex (64 chars)
  → decode → 32 bytes
  → multihash(0x12, 0x20, 32bytes)  // SHA2-256, 32 bytes length
  → cid.NewCidV1(cid.Raw, mh)       // Raw codec (0x55)
  → "bafkrei..."  (CIDv1 base32)
```

---

## 网络传输差异总结

| | BT DHT | IPFS DHT |
|--|--------|----------|
| 传输层 | UDP | TCP/QUIC (libp2p stream) |
| 序列化 | bencode | Protocol Buffers (proto3) |
| 协议标识 | 无固定标识 (隐含在 DHT 消息中) | libp2p 协议协商 |
| 节点 ID | 20 bytes, SHA1 派生 | libp2p PeerID (multihash) |
| 值大小 | ≤ 1000 bytes (v 字段) | 无硬限制 (受流协议约束) |
| 认证 | Ed25519 签名 (BEP 44) | Record 签名 (已从 proto 移除) |
| DHT 库 | anacrolix/dht/v2 | go-libp2p-kad-dht |
| 本地端口 | 可配置 UDP (:6881) | libp2p 监听端口 |
| 引导 | router.bittorrent.com + transmissionbt.com | libp2p 默认引导节点 / 静态配置 |
