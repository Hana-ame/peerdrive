# P2P Peer Discovery 流程

## 概述

Peerdrive 的节点发现系统由多个并行机制组成，在节点启动后自动运行。所有发现到的对端最终都会通过 ConnectionManager 进行连接管理和心跳维护。

整体流程如下：

```
节点启动
  │
  ├─▶ NewP2PService (p2p.go)
  │     ├── 创建 libp2p Host
  │     ├── 创建 DHT (ModeServer)
  │     ├── 设置 mDNS 发现
  │     ├── 连接 Bootstrap Peer (如有配置)
  │     ├── 连接 Static Relays (如有配置)
  │     └── AutoConnectFromDiscovered (连接 mDNS 发现的对端)
  │
  └─▶ SetupRouter (router.go)
        └── PeerScanner.Start()
              ├── DHT Scanner        (每60s)
              ├── RegServer Scanner  (每120s)
              ├── LAN Scanner        (每30s)
              └── Bootstrap Maintain (每300s)
```

---

## 1. 节点启动时的发现

### 1.1 mDNS 发现 (`p2p.go:822`)

```go
func (svc *P2PService) setupMDNS() {
    mdnsService, _ = mdns.NewMdnsService(h, "peerdrive-mdns", svc)
    mdnsService.Start()
}
```

- 在本地网络广播 mDNS 查询（标签 `peerdrive-mdns`）
- `HandlePeerFound` 回调将发现的对端存入 `svc.discovered` 映射表和 PeerTracker
- 默认每 30 秒广播一次

### 1.2 DHT Bootstrap (`p2p.go:133`)

```go
dhtInst, _ := dht.New(ctx, h, dht.Mode(dht.ModeServer))
dhtInst.Bootstrap(ctx)
```

- 创建 Kademlia DHT 实例（始终以 ModeServer 模式运行）
- 立即 Bootstrap 连接到 DHT 网络

### 1.3 静态 Relay 连接 (`p2p.go:103`)

```go
for _, relayAddr := range parseStaticRelays(cfg.P2PStaticRelays) {
    addrs = append(addrs, relayAddr)
}
libp2p.EnableAutoRelayWithStaticRelays(addrs)
```

- 解析 `PEERDRIVE_STATIC_RELAYS` 中的 multiaddr 列表
- 通过 libp2p 的自动中继管理器连接

### 1.4 Bootstrap Peer 连接 (`p2p.go:170`)

```go
if cfg.P2PBootstrapPeer != "" {
    h.Connect(ctx, *info)
}
```

- 如果配置了 `PEERDRIVE_BOOTSTRAP_PEER`，立即解析并直连

### 1.5 AutoConnectFromDiscovered (`p2p_connection.go:194`)

```go
func (cm *ConnectionManager) AutoConnectFromDiscovered() {
    peers := cm.p2pSvc.GetDiscoveredPeers()
    for _, pi := range peers {
        cm.ConnectToPeer(pi, 15*time.Second)
    }
}
```

- 遍历 mDNS 发现的所有对端，逐个尝试连接（15s 超时）

---

## 2. PeerScanner — 4 个后台发现循环

`PeerScanner` 在 `router.go` 中创建并启动，运行 4 个独立的扫描协程：

### 2.1 DHT Scanner (每 60 秒, `peer_scanner.go:160`)

```
scanDHT 循环:
  1. collectStorageHashes()
     └─ 扫描 storageDir，找 {prefix}/{64-char-hash} 目录
  2. 对每个 hash:
     └─ FindProviders(cid)
          ├─ 先尝试连接已发现(mDNS)对端
          └─ DHT.FindProviders(ctx, cid, 30s timeout)
               └─ Kademlia DHT 查找谁 Provide 了该 CID
  3. 对每个 provider:
     └─ connectToPeer(pi) (如未连接)
```

**注意**: DHT Scanner 只搜索本地已存文件的 provider，不做"谁在线"的广播查询。

### 2.2 RegServer Scanner (每 120 秒, `peer_scanner.go:257`)

```
scanRegServer 循环:
  1. GET {regURL}/auth/list
  2. 解析返回的 peers 列表
  3. 对每个 peer (跳过自己):
     └─ 解析 multiaddr → connectToPeer(pi)
```

### 2.3 LAN Scanner (每 30 秒, `peer_scanner.go:354`)

```
scanLAN 循环:
  1. 获取 mDNS 发现的所有对端
  2. 对每个未连接对端:
     └─ connectToPeer(pi)
```

### 2.4 Bootstrap Maintainer (每 300 秒, `peer_scanner.go:410`)

```
maintainBootstrap 循环:
  1. 如果 BootstrapPeer 已配置但断连
  2. 重新连接 BootstrapPeer
```

---

## 3. ConnectionManager — 心跳与重连

`p2p_connection.go` 中的心跳循环（每 30 秒）：

```go
1. 遍历所有 known peers
2. 检查连接状态
3. 如果断开 → 尝试重连 (15s 超时)
4. 更新延迟质量评分
```

### 延迟质量评分

- 维护每个对端的延迟历史（最多 10 个样本）
- 加权评分：延迟 / 抖动 / 丢包 → 0-100 分

---

## 4. Relay 注册与发现

### RelayRegistry (`relay_registry.go`)

如果配置了 `PEERDRIVE_REG_SERVER_URL`，中继节点会：

```go
1. POST /p2p/relay/register  → 注册本节点作为中继
2. 每 60 秒 POST /p2p/relay/heartbeat → 保活
```

### bootstrapFromRelayList (`router.go:456`)

客户端节点从注册服务器获取中继列表：

```go
1. GET {regURL}/p2p/relay/list
2. 对列表中每个 relay:
   └─ 解析 multiaddr → 连接
```

---

## 5. 完整发现流程时序图

```
节点启动
  │
  ├─ mDNS 发现开始 ──────────────────────────▶ LAN 广播 (30s 间隔)
  │
  ├─ DHT Bootstrap ──────────────────────────▶ Kademlia 网络
  │
  ├─ 连接 Static Relays (如有配置)
  │
  ├─ AutoConnectFromDiscovered ──────────────▶ 连接 mDNS 发现的 peers
  │
  ├─ PeerScanner 启动 ───────────────────────┐
  │    ├── DHT Scanner (60s) ────────────────┤
  │    ├── RegServer Scanner (120s) ─────────┤
  │    ├── LAN Scanner (30s) ────────────────┤
  │    └── Bootstrap Maintainer (300s) ──────┘
  │
  └─ ConnectionManager 心跳 (30s) ──────────▶ 保持连接 / 自动重连
```

---

## 6. 关键配置变量

| 环境变量 | 默认值 | 作用 |
|----------|--------|------|
| `PEERDRIVE_P2P_ENABLE` | true | 是否启用 P2P |
| `PEERDRIVE_MDNS_ENABLE` | true | 是否启用 mDNS 局域网发现 |
| `PEERDRIVE_BOOTSTRAP_PEER` | "" | 启动时直连的引导节点 multiaddr |
| `PEERDRIVE_STATIC_RELAYS` | "" | 静态中继节点 multiaddr 列表 |
| `PEERDRIVE_RELAY_MODE` | client | client / server / none |
| `PEERDRIVE_REG_SERVER_URL` | "" | 注册服务器 URL (用于中继列表发现) |
| `PEERDRIVE_HOLE_PUNCH` | true | 是否启用 NAT 打洞 |
| `PEERDRIVE_AUTO_NAT` | true | 是否启用 AutoNAT |
| `PEERDRIVE_PUBLIC_REACHABLE` | false | 是否公网可达 |
| `PEERDRIVE_PUBLIC_DOMAIN` | "" | 公网域名 |
| `PEERDRIVE_MAX_PEERS` | 8 | 最大对端数量 |
