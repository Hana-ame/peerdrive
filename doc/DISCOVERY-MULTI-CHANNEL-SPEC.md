# 节点发现多通道合并去重、统一门面与故障切换规范

> 对应 Issue: #147  
> 状态: 架构规范与需求确立（按 Issue 边界：明确需求与实测行为基准，待后续单通道实测后实施）  
> 关联 Issue: #146 (DHT 发现层), #145 (双栈策略), #144 (NAT/TURN), #90 (发现层外移), #115 (架构总纲)

---

## 1. 现状通道与行为诊断

当前 Peerdrive 的节点互联包含三条物理通道：

```text
               ┌────────────────────────────────────────────────────────┐
               │                    PeerJSService                       │
               └───────────▲─────────────────▲─────────────────▲────────┘
                           │                 │                 │
                (静态直连拨号)       (轮询回调)        (订阅消息回调)
                           │                 │                 │
              PEERJS_PEERS 静态列表    HTTPDiscovery     MQTTDiscovery
                                    (/discover/nodes)  (公共 broker 房间)
```

### 1.1 发现通道与模式现状（实读代码）
在 `back/internal/transport/peerjs_service.go:401` 中：
- `mode == "auto"`（默认）：
  ```go
  http := s.cfg.DiscoverURL != ""
  mqtt := !http && s.cfg.MQTTEnable
  ```
  在 `auto` 模式下，**HTTP 发现与 MQTT 是硬互斥的**。当配了 `PEERDRIVE_DISCOVER_URL` 时，MQTT 根本不会启动。
- 若未来支持双通道并发或切换为「主备模式」，两条通道将同时或交替产生发现事件。

### 1.2 跨通道并发拨号与预算竞争漏洞（代码实读）
当前发现回调处理逻辑（`peerjs_service.go:350`）：
```go
func (s *PeerJSService) onDiscoveredPeer(peerID string) {
    if peerID == "" || peerID == s.id || s.IsPeerBlocked(peerID) {
        return
    }
    s.mu.Lock()
    _, ok := s.conns[peerID]
    s.mu.Unlock()
    if ok { return }
    if !s.discoveryDialAllowed() { return }
    go s.connectLoop(peerID)
}
```
**现存缺陷分析**：
1. **预算漏网（In-Flight Dials 穿透）**：
   - `discoveryDialAllowed()` 仅统计当前已经建立好 DataChannel 的连接数：`len(s.conns)`。
   - 当发现通道突发推入 10 个新节点时，10 个 goroutine 并发启动 `connectLoop(peerID)`。由于 ICE 协商与信令需要 1~3 秒，此时 `conns` 数量尚未增加，导致 10 个拨号**全部通过预算检查**，瞬时并发拨号数突破 `PEERDRIVE_MAX_PEERS`。
2. **多通道同一 Peer 重复触发**：
   - 虽然 `connectLoop` 内部拥有 `s.connecting[peerID]` 互斥锁，能阻断对**同一目标节点**的并发双重拨号；但由于检查发生在 goroutine 启动之后，多个通道同时发现同一节点时，会产生冗余的 goroutine 调度与日志噪音。
3. **主备缺失（纯互斥非容灾）**：
   - `DiscoverURL` 宕机或网络分区时，系统不会自动降级到备用 MQTT Broker。

---

## 2. 统一发现门面（Discoverer Facade）架构设计

### 2.1 候选节点流模型

为屏蔽底层信令、HTTP、MQTT 乃至未来 DHT（#146）的差异，引入统一的候选节点事件结构：

```go
type CandidatePeer struct {
    PeerID     string            // 节点唯一 ID (PeerJS ID)
    Channel    string            // 来源通道: "static" | "http" | "mqtt" | "dht"
    Rooms      []string          // 所在房间 (64hex 内容 hash 或 presence hash)
    Discovered time.Time         // 发现时间戳
    Metadata   map[string]any    // 携带的元数据 (如节点类型、共享数量、版本)
}
```

### 2.2 统一聚合器 `DiscoveryAggregator`

```go
type DiscoveryAggregator struct {
    mu         sync.Mutex
    channels   map[string]DiscoveryChannel
    candidates chan CandidatePeer
    budget     *DialBudgetManager
}
```

- **全生命周期管理**：各子通道（HTTP、MQTT、DHT）作为 Plugin 接入聚合器。
- **全局拨号预算（Global Dial Budget）**：
  $$\text{CurrentActive} = \text{ConnectedPeers} + \text{InFlightDials}$$
  当 $\text{CurrentActive} \ge \text{MaxPeers}$ 时，统一由预算管理器挂起或丢弃非关键发现事件，严格遏制拓扑恶化。

---

## 3. 故障切换机制（Failover & Fallback）

针对自托管信令（HTTP Discovery）与公共 MQTT 房间设计主备自动切换状态机：

```text
    ┌──────────────────────┐
    │  PRIMARY (HTTP 发现)  │ ◄─────── 连续 3 次成功心跳恢复
    └──────────┬───────────┘
               │ 连续失败 3 次 / 超时 45s
               ▼
    ┌──────────────────────┐
    │  FALLBACK (MQTT 发现) │
    └──────────────────────┘
```

1. **健康度探针**：
   - 记录主通道的连续失败计数 `failCount` 与最近一次成功时间戳 `lastSuccess`。
   - 当 HTTP announce/poll 连续失败超过阈值（默认 3 次，约 45s）时，自动触发 Fallback 激活 MQTT 监听。
2. **平滑切回**：
   - 在 Fallback 状态下，后台以低频（如 60s）向主通道发送轻量探针。若主通道恢复健康，平滑关闭 MQTT 订阅并切回主通道，避免双通道长期并存导致的资源冗余。

---

## 4. 房间名与 Presence Room 前置校验（防御式设计）

AGENTS.md 硬性约束：**存在房间名必须保持合法 64 位十六进制小写（64hex）**。
若向信令服务提交非 64hex 房间名，可能导致整条 Announce 请求被返回 400 从而断开发现。

**防护措施**：
1. **客户端前置硬校验**：
   在 `HTTPDiscovery.announce()` 与 `MQTTDiscovery.Announce()` 入口处，对即将发出的 `collections` 数组进行统一过滤：
   ```go
   var validRooms []string
   for _, r := range d.collections {
       if hashutil.IsStrictSHA256(r) {
           validRooms = append(validRooms, r)
       } else {
           log.LogWarn("discover: dropping malformed non-64hex room name %q", r)
       }
   }
   ```
2. **Fail-Loud 机制**：
   若过滤后 `validRooms` 为空且开启了 `DiscoverPresence`，则立即报严重错误，绝不静默发送损坏数据。

---

## 5. 发现状态与拓扑可观测性

在管理面和诊断接口（如 `/peerjs/status` 或 WebSocket admin）暴露发现状态结构体：

```json
{
  "discovery": {
    "active_channel": "http",
    "fallback_active": false,
    "presence_room": "1e2b... (sha256)",
    "rooms": [
      {
        "hash": "1e2b...",
        "type": "presence",
        "peer_count": 5
      }
    ],
    "budget": {
      "max_peers": 8,
      "connected": 4,
      "in_flight": 1,
      "available": 3
    },
    "channels": {
      "http": {
        "status": "healthy",
        "last_announce_at": "2026-10-09T14:00:00Z",
        "fail_count": 0
      },
      "mqtt": {
        "status": "standby"
      }
    }
  }
}
```

---

## 6. 实施路线（落地前置条件）

按 Issue #147 边界约束：**本规范确立架构与逻辑边界，编码实施前需执行以下实测**：
1. 在双机/双实例环境下（自托管信令 `cmd/peersignal` + 2 个节点），强制开启 HTTP 与 MQTT 双通道；
2. 捕获并记录同一节点在两个通道并发上报时的实际系统日志，确证 in-flight 拨号竞争现象；
3. 基于上述实测基准数据，落地 `DialBudgetManager` 与多通道聚合门面。
