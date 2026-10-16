# WebRTC PCDN 架构与分片调度引擎规范 (Issue #317)

> **关联 Issue**: [#317](https://github.com/Hana-ame/peerdrive/issues/317)  
> **设计定位**: 基于 WebRTC DataChannel 与统一数据源（`source.Source`）架构，构建面向大文件分发与媒体点播的混合 PCDN（Peer-assisted CDN）引擎，涵盖分片切分、节点质量评分、多路并行流水线调度与切片级透明回源。

---

## 1. 背景与可行性评估 (Feasibility Matrix)

在多节点内容分发场景下，单纯依赖中心化 VPS 或云 CDN 会面临巨大的下行带宽成本（尤其是视频、大图集与安装包）。通过在 Peerdrive 基座上构建 PCDN，聚合边缘节点上行带宽，可显著降低源站出口开销。

### 1.1 能力现状与缺口对照

| 维度 | Peerdrive 现状 | PCDN 生产级需求 | 改造状态 |
|---|---|---|---|
| **传输层** | WebRTC DataChannel (SCTP) | 高吞吐、低延迟二进制传输 | ✅ 已就绪（支持 Issue #316 自适应分片与背压） |
| **NAT 穿透** | WebRTC ICE (STUN/TURN) | 复杂公网环境对端互联 | ✅ 已就绪（原生集成） |
| **信令与发现** | peersignal + Presence 房间 | 节点拓扑与集群发现 | ✅ 已就绪（Presence 房间 64hex 发现） |
| **寻址模式** | SHA-256 CAS 内容寻址 | 强一致性、去中心化内容校验 | ✅ 已就绪（全局 64hex 小写 hash） |
| **数据源抽象** | `source.Source` 统一读接口 | 可插拔、多级降级管道 | ✅ 已就绪（`local` / `peer` / `url` / `ech`） |
| **切片切分** | 粗粒度 Range 请求 | 固定大小逻辑切片（Chunk）流水线 | ⚠️ 需补齐（`PCDNSource` 分片切割） |
| **质量评分** | 粗粒度计数统计（`Stats`） | RTT 滑动平均、EWMA 吞吐与熔断 | ⚠️ 需补齐（`PeerScoreMatrix`） |
| **并行调度** | 快者通吃单连接竞速（`raceOpen`） | 多 Peer 并发切片抓取与按序拼装 | ⚠️ 需补齐（`ChunkScheduler` 流水线） |
| **回源容灾** | 源级别整文件粗粒度降级 | 单切片毫秒级透明回源至 CDN/Origin | ⚠️ 需补齐（Per-Chunk Fallback） |

### 1.2 网络拓扑与穿透现实（国内 CGNAT vs 海外节点）

1. **国内家宽对称型 NAT (Symmetric NAT / CGNAT)**：
   - 国内运营商 NAT444 导致对等直连打洞成功率较低（通常在 10%~15% 范围）。
   - 若全部通过 TURN 中继，会将带宽成本转移到中继服务器，无法达到降低带宽费用的目标。
2. **混合分层拓扑解决方案 (Tiered Hybrid Topology)**：
   - **SuperNode / 边缘中继层**：在具备公网 IP 的便宜海外 VPS（如 $2-5/月）或 Full Cone NAT 环境部署常驻 Go 节点作为 SuperNode；
   - **Swarm 对等共享**：同运营商、同省份或全锥型客户端之间通过 WebRTC 直连共享切片；
   - **切片级即时兜底**：任何对端打洞失败或数据抖动的切片，毫秒级回源至 Cloudflare 边缘或 HTTP Origin。

---

## 2. 核心架构设计 (Core Architecture)

```
                    客户端消费请求 (OpenRange / HTTP Egress)
                                      │
                                      ▼
                        ┌───────────────────────────┐
                        │        PCDNSource         │
                        │ (implements source.Source)│
                        └─────────────┬─────────────┘
                                      │
                       切片切分 (ChunkTask Slicing)
                        [C0: 0..1MB] [C1: 1..2MB] ...
                                      │
                                      ▼
                        ┌───────────────────────────┐
                        │      ChunkScheduler       │
                        │    (Worker Pool & Flow)   │
                        └───────┬───────────┬───────┘
                                │           │
                多 Peer 加权分派 │           │ 超时/失败/熔断
                                ▼           ▼
                   ┌──────────────────┐  ┌──────────────────┐
                   │ PeerScoreMatrix  │  │  Origin Fallback │
                   │  - RTT 跟踪      │  │  (HTTP / CDN /   │
                   │  - EWMA 吞吐     │  │   Cloudflare)    │
                   │  - 连续失败熔断  │  └──────────────────┘
                   └────────┬─────────┘
                            │
               WebRTC DataChannel 并行切片
               (Peer A: C0, Peer B: C1, ...)
                            │
                            ▼
                        ┌───────────────────────────┐
                        │   有序重组流水线缓冲      │
                        │  (Ordered ReadCloser)     │
                        └─────────────┬─────────────┘
                                      │
                                      ▼
                          连续字节流交付下游消费者
```

### 2.1 节点质量评分矩阵 (`PeerScoreMatrix`)

为避免慢端（Stragglers）拖垮整体流式吞吐，`PeerScoreMatrix` 为每个在线 Peer 维护指标卡：

- **往返时延（RTT）**：每次发起切片请求至收到首字节的耗时。采用滑动窗口移动平均。
- **有效吞吐率（EWMA Throughput）**：传输切片所用的平均速率，采用指数加权移动平均：
  $$\text{EWMA}_{t} = \alpha \times \text{CurrentRate} + (1 - \alpha) \times \text{EWMA}_{t-1}$$
  默认加权系数 $\alpha = 0.3$。
- **连续失败熔断（Circuit Breaker）**：
  - 连续失败达到阈值（默认 3 次），将该 Peer 标记为熔断，并在冷却窗口期（默认 30 秒）内不再派发切片。
  - 冷却到期后进入半开状态（Half-Open），允许试探性派发单个切片。

### 2.2 多路分片调度器 (`ChunkScheduler`)

1. **分片切分**：将目标区间 $[offset, offset+size)$ 按照设定尺寸（`DefaultChunkSize = 1MB`，支持 64KB ~ 8MB 可配置）划分成 `ChunkTask`。
2. **多路抓取**：并发协程池并发向排序靠前的 Peer 发起 `OpenStreamFrom(peerID, hash, chunkOffset, chunkSize)`。
3. **单切片即时回源**：
   - 若目标 Peer 在指定时限内未响应、连接断开或返回错误；
   - 调度器立刻向预设的 `originSource`（如 `URLSource` 或 HTTP CDN）请求该切片；
   - 回源成功后，流水线无缝拼接，用户端完全感知不到底层对端抖动。
4. **有序重组**：通过优先队列/有序通道将接收到的分片按序号重排，确保下游 `Read` 按字节顺序无缝流出。

---

## 3. 纯 P2P 架构与权威源回退边界

经架构复审确认，Peerdrive **不存在 Cloudflare 等商业 CDN 双层兜底体系**，纯粹作为基于 WebRTC 的去中心化内容寻址网盘：

1. **权威源回退定位**：
   - 调度器中的回退源（`originSource`）仅对接本地已配置的权威数据源（如本地 CAS、指定存储源或离线备份源）；
   - 不依赖、不假设存在公网商业 CDN 边缘节点进行切片缓存；
2. **纯对等互联优势**：
   - 所有多节点并行拉取完全在对等节点间通过 WebRTC DataChannel 展开；
   - 节点自主去中心化协作，无任何中心化 CDN 账单或中间人依赖。


---

## 4. 接口契约与安全性要求

1. **`source.Source` 契约**：
   - `PCDNSource` 实现 `source.Source`。
   - 声明能力位：`CapStream | CapVerify`。
   - 整文件读取（`offset == 0 && size < 0`）必须在 EOF 时校验 SHA-256，防止中间恶意节点篡改。
2. **资源与生命周期管理**：
   - 返回的 `io.ReadCloser` 在 `Close()` 时必须安全回收所有内部协程、缓冲区和待处理任务，严防 goroutine 泄漏。
