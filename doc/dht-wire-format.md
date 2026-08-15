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

---

## 附录：BT DHT 验证方法

以下操作在本地 node (port 3001) 或 VPS relay (port 3000) 上執行，均使用 `--noproxy` 避免代理干擾。

### 1. 確認 BT DHT 已啟用 + 路由表大小

```bash
curl -s --noproxy '*' http://localhost:3001/bt/status | python3 -m json.tool
```

**預期輸出**:
```json
{
    "enabled": true,
    "listen_addr": "0.0.0.0:6881",
    "num_nodes": 28
}
```

- `num_nodes` > 0 表示節點已成功引導進入 Mainline DHT
- 初始為 0，bootstrap 後通常 10-60 秒內增長到 8-40+
- 值越大，路由表越完善

### 2. BT DHT 全局狀態 (含更多統計)

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

### 3. 在 BT DHT 上 Announce 一個 hash

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/bt/announce \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

**預期**: `{"status":"announced on BT DHT"}`

底層流程:
1. 取 SHA256 前 20 bytes 作為 BT infohash
2. 調用 `dht.Server.Announce(infoHash, port, false)`
3. KRPC `get_peers` → 獲得 token → KRPC `announce_peer` 向最近 K 個節點註冊

### 4. 在 BT DHT 上查找 providers

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/bt/find \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

**預期**:
```json
{
    "hash": "59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb",
    "peers": ["1.2.3.4:6881", "5.6.7.8:6881"],
    "count": 2
}
```

- 內部調用 `s.Server.AnnounceTraversal(infoHash)` 遍歷 DHT
- 超時 15 秒
- 如果無人 announce 該 hash，`peers` 為空數組

### 5. 雙網同時 Announce (BT + IPFS)

```bash
curl -s --noproxy '*' -X POST http://localhost:3001/p2p/dual/announce \
  -H 'Content-Type: application/json' \
  -d '{"hash":"59ea11d9aaec055a68eeb42cdad638fd8c9745a699be3e70a5197b524bfc0abb"}'
```

```json
{"status":"announced on both networks"}
```

### 6. 雙網同時查找 (BT + IPFS)

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

### 7. BEP 44 不可變項存儲測試

```bash
# 寫入
DATA=$(echo -n "Hello DHT from peerdrive" | base64 -w0)
RESP=$(curl -s --noproxy '*' -X POST http://localhost:3001/bt/bep44/put \
  -H 'Content-Type: application/json' \
  -d "{\"data\":\"$DATA\",\"mutable\":false}")
echo "$RESP" | python3 -m json.tool
TARGET=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['target'])")

# 讀取
curl -s --noproxy '*' -X POST http://localhost:3001/bt/bep44/get \
  -H 'Content-Type: application/json' \
  -d "{\"target\":\"$TARGET\"}" | python3 -m json.tool
```

### 8. BEP 51 infohash 採樣

```bash
curl -s --noproxy '*' http://localhost:3001/bt/bep51/sample | python3 -m json.tool
```

```json
{
    "samples": ["<40-char-hex>", ...],
    "count": 8
}
```

查詢路由表中最近 K 個節點的 infohash 樣本，用於觀察 DHT 網絡中活躍的內容。

### 驗證判斷標準

| 指標 | 健康 | 異常 |
|------|------|------|
| `num_nodes` | > 0 且持續增長 | = 0 或停滯，表示 bootstrap 失敗或被防火牆阻擋 |
| Announce 響應 | `"status": "announced on BT DHT"` | 報錯或超時 |
| Find 響應 | 返回 200，`peers` 可能有/空 | HTTP 錯誤 |
| BEP 44 round-trip | put 後 get 返回相同數據 | 數據不匹配 |
| BEP 51 sample | 返回 samples 列表 | 空數組或錯誤 |
| 端口 | UDP 6881 可外部訪問 | 防火牆阻擋 UDP |

### 注意事項

- BT DHT 使用 **UDP**，需要防火牆放行 UDP 端口（默認 6881）
- NAT/防火牆會影響 DHT bootstrap：如果 UDP 被阻擋，`num_nodes` 會一直為 0
- 引導節點是公共的 `router.bittorrent.com:6881` 和 `dht.transmissionbt.com:6881`
- 路由表填充需要時間：bootstrap 後等 30-60 秒再查 `num_nodes`
- `/p2p/status` 只顯示 IPFS/libp2p 信息，BT DHT 專用端點在 `/bt/` 路徑下
