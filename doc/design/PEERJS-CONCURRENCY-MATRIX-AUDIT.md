# PeerJS 多客户端并发矩阵盘点规范 (N 客户端 × M 节点)

> **关联 Issue**: [#205](https://github.com/Hana-ame/peerdrive/issues/205)  
> **文档定位**: 传输与互联层（`back/peerjs/`、`back/internal/transport/`）在多客户端与多节点复杂拓扑下的并发覆盖边界盘点表。先全量盘点，确立「已覆盖 / 未覆盖 / 无法覆盖」边界与根本成因，作为后续补齐测试的基准依据。

---

## 1. 背景与盘点目标

`back/peerjs/`（独立 Go 模块 `github.com/Hana-ame/go-peerjs`）是 Peerdrive 整个系统的 P2P 传输基座，同时服务于三种不同的并发调用形态：
1. **节点间互联 (Node-to-Node)**：双向常驻或发现触发的 WebRTC DataChannel，承载 `share`、`req/data/done`、`file_index` 增量同步与端口转发；
2. **节点 ↔ 面板 (Node-to-Panel)**：零依赖纯 WebRTC 消费端（`packages/peerdrive-client`），浏览器多标签页或多用户并发拉取文件；
3. **Media-Node ↔ 节点 (Media Server)**：`packages/peerdrive-media` 通过 DataChannel 将 HTTP 资源分块流式传输给浏览器 `<img>`/`<video>`。

此前测试主要分散于 `contract_test.go`、`flowcontrol_test.go`、`peer_test.go` 及上层集成测试中。由于缺乏统一的拓扑维度盘点，面临三项痛点：
- **组合盲区未知**：无法确定多客户端竞争同一节点、双节点对称对拨（Glare）等极端场景是否有测试保障；
- **补测缺乏依据**：缺少清晰的覆盖度边界，写测试容易陷入主观猜测；
- **子模块门禁独立**：主模块 `go test ./...` 无法直接运行子模块测试，容易遗漏并发退化。

本规范确立「先盘点、后补测」的实施原则，给出包含 N 客户端 × M 节点 × 操作 × 时序 × 失败注入的多维矩阵。

---

## 2. 多维矩阵维度定义

| 维度 | 取值范围 | 说明 |
|---|---|---|
| **客户端数 N** | `1` / `2` / `≥3` | 发起传输或调用操作的独立对端实例数（如浏览器标签页、PeerPuller 任务、外来 Peer 连接） |
| **节点数 M** | `1` / `2` / `≥3` | 提供文件、信令或路由服务的服务端节点实例数（持有本地 SQLite 索引与 Storage 存储） |
| **操作 (Verb/Action)** | 上传 (`upload`) / 下载 (`req/pull`) / 列举 (`share/list`) / 中断重连 (`reconnect`) | 核心网络与业务操作类型 |
| **时序 (Timing)** | 同时 (`simultaneous`) / 交错 (`interleaved`) / 重复触发 (`duplicate`) | 并发事件在微秒级与毫秒级的相对顺序 |
| **失败注入 (Failure)** | 中途断链 (`drop`) / 限流触发 (`backpressure`) / 超时 (`timeout`) | 网络不稳定与吞吐受限下的异常注入 |

### 评判分类标准
- **已覆盖 (Covered)**：现有代码库中已有自动化测试用例，提供代码位置、函数名及测试环境要求。
- **未覆盖 (Uncovered)**：在当前架构下可由单元测试或本地集成测试模拟，但当前缺少明确用例覆盖，存在并发竞态或状态未定义风险。
- **无法覆盖 (Uncoverable)**：因底层环境强依赖（如真实多层对称 NAT、多宿主物理网卡硬件差异、缺少分布式中心时钟），单机/CI 沙箱无法无假设覆盖，必须依赖手工或公网实网验证。

---

## 3. 并发矩阵全量盘点表

### 3.1 客户端数 N=1，节点数 M=1（基线基座）

| 操作 | 时序 | 失败注入 | 覆盖状态 | 证据 / 现状分析 | 改进与风险说明 |
|---|---|---|---|---|---|
| **上传/写数据** | 同时并发写帧 | 无 | **已覆盖** | `back/peerjs/contract_test.go:TestContract_ConcurrentSend` | 单连接上 10 goroutine 并发写，验证 `sendMu` 序列化，防二进制帧交错 |
| **上传/写数据** | 并发写 + 关链 | 中途断链 | **已覆盖** | `back/peerjs/contract_test.go:TestContract_ConcurrentSendAndClose` | 写过程中 close，验证并发安全性与 channel 关闭 panic 防护 |
| **数据传输** | 连续写 | 限流触发 | **已覆盖** | `back/peerjs/flowcontrol_test.go:TestSendFrame_FlowControl_Resumes` | 模拟 >512KB 高水位阻塞与低于低水位唤醒恢复 |
| **数据传输** | 阻塞写 | 中途断链 | **已覆盖** | `back/peerjs/flowcontrol_test.go:TestSendFrame_FlowControl_CloseAborts` | 处于背压挂起状态时连接关闭，验证非死锁退出 |
| **建立连接** | 单次 | 超时/取消 | **已覆盖** | `back/peerjs/contract_test.go:TestContract_DialContextCancelled` | Context 超时或取消时拨号优雅终止 |
| **建立连接** | 单次 | 节点不可达 | **已覆盖** | `back/peerjs/contract_test.go:TestContract_DialUnreachable` | 无法连接远端信令/对端时返回明确错误并清理 |
| **中断重连** | 重复触发 | 中途断链 | **未覆盖** | `back/peerjs/peer_test.go` 仅覆盖单次断链 | 单客户端在传输大文件中途断开重连时，断点续传 offset 与 `.part` 临时文件复用（关联 #212）未在 peerjs 层闭环 |

---

### 3.2 客户端数 N=2，节点数 M=1（多端争用单节点）

| 操作 | 时序 | 失败注入 | 覆盖状态 | 证据 / 现状分析 | 改进与风险说明 |
|---|---|---|---|---|---|
| **下载 (req)** | 同时 | 无 | **未覆盖** | 两个独立客户端并发向同一节点拉取不同文件或同一文件 | **核心风险**：底层 DataChannel 吞吐分配、磁盘并发 SafeOpen 句柄占用与 WAL 读锁扩展 |
| **下载 (req)** | 同时/交错 | 限流触发 | **未覆盖** | 慢客户端（高背压）与快客户端混用 | **核心风险**：单个连接被低水位挂起时，是否会影响节点事件调度循环中的其他客户端？ |
| **上传 (upload)** | 同时 | 无 | **未覆盖** | 两个客户端同时向同一节点上传相同或不同 hash 文件 | **核心风险**：临时文件命名是否冲突、SQLite `file_index` 写入串行事务是否产生 `SQLITE_BUSY`（WAL 下写写串行） |
| **列举 (share/list)** | 同时 | 无 | **未覆盖** | 多个客户端并发请求清单 | **低风险**：纯只读事务，WAL 模式下多读者并发支持度高，但尚未编写双 client 测试 |
| **中断重连** | 重复触发 (同一 peerId) | 重复拨号 | **已覆盖 (部分)** | `back/peerjs/peer_test.go:TestRouteOffer_DuplicateConnectionID_ClosesOld` | 仅在底座层验证了重复 ConnectionID 会清理旧连接；上层相同 PeerID 跨连接状态清理未覆盖 |
| **中断重连** | 交错 | 中途断链 | **未覆盖** | 客户端 A 异常断链并重新注册同一 PeerID | 旧连接处于僵尸半开状态（Half-open），节点未能及时触发 Leave 清理，新连接建立可能触发状态冲突 |

---

### 3.3 客户端数 N=1，节点数 M=2（双节点对拨与跨节点互操作）

| 操作 | 时序 | 失败注入 | 覆盖状态 | 证据 / 现状分析 | 改进与风险说明 |
|---|---|---|---|---|---|
| **互联 (Dial)** | **同时对拨 (对称 Glare)** | 正常信令 | **未覆盖 (P0 盲区)** | Node A 与 Node B 在几乎同一毫秒互相发起 Offer 拨号 | **严重风险**：双方同时处于 Offerer 状态（WebRTC Glare）。缺少 tie-breaking 仲裁机制（如按 PeerID 字典序决胜），可能导致双方都失败或建立两条孤立通道 |
| **下载/拉取** | 串行/交错 | 正常 | **已覆盖** | `test/integration/netdisk_test.go` + `scripts/netdisk-local-demo.sh` | Node B 通过 PeerPuller 拉取 Node A 文件，包含 sha256 校验与 PSK 鉴权验证 |
| **拉取保存** | 单次 | 中途断链 | **未覆盖** | 拉取大文件 50% 时主动断开链路 | 验证 `.part` 临时文件是否残留、PeerPuller 是否正确感知 ErrBrokenPipe 并置任务为 Failed |
| **发现与碰面** | 同时 | 无 | **已覆盖** | `back/internal/transport/discovery_rooms_test.go` + `back/test/integration/selfhosted_test.go:TestInterconnectViaPresenceRoom` | 验证 Presence 房间固定 SHA-256 空间下的双向对端发现 |
| **鉴权拦截** | 同时 | PSK 错误 | **已覆盖** | `test/integration/netdisk_test.go` | PSK 不匹配时拦截并正确返回 `PSK_REQUIRED`，带密钥后恢复互通 |

---

### 3.4 客户端数 N≥3，节点数 M=1（星型并发高负载）

| 操作 | 时序 | 失败注入 | 覆盖状态 | 证据 / 现状分析 | 改进与风险说明 |
|---|---|---|---|---|---|
| **全量连接生命周期** | 同时建立与关闭 | 中途断链 | **已覆盖** | `back/peerjs/peer_test.go:TestPeerClose_ClosesAllConnections` | 单 Peer 管理多个 Connection 时的批量释放和资源清理幂等性 |
| **并发下载** | 同时抢占带宽 | 限流触发 | **未覆盖** | ≥3 个消费端打满节点上行带宽 | 验证 `PEERDRIVE_MAX_PEERS` 或节点级流控是否会导致调度饥饿 |
| **并发列举** | 密集脉冲重复触发 | 超时 | **未覆盖** | 高并发查询对外共享目录清单 | 验证只读并发下是否会耗尽 DB 连接池（`SetMaxOpenConns(8)`） |

---

### 3.5 客户端数 N≥3，节点数 M≥3（分布式网状拓扑 Mesh）

| 操作 | 时序 | 失败注入 | 覆盖状态 | 证据 / 现状分析 | 改进与风险说明 |
|---|---|---|---|---|---|
| **网状自动发现** | 同时上线 | 无 | **未覆盖** | 多个节点同时向 Presence 房间广播 | **核心风险**：O(N²) 全互联拨号风暴。是否被 `PEERDRIVE_MAX_PEERS=8` 准确定时截断并防止死锁？ |
| **多源并行切片下载 (Swarm)** | 同时 | 中途断链 | **无法覆盖 (架构约束)** | 从多个节点并行拉取同一文件不同分块 | **无法覆盖原因**：Peerdrive 架构当前明确采用点对点单源流式拉取（`Roadmap Phase 1-6`），未设计 BitTorrent/Swarm 风格多源协调器 |
| **跨 IP 族 / NAT 真实并发** | 跨 IPv4/IPv6 | 真实丢包/黑洞 | **无法覆盖 (物理环境约束)** | 双栈多宿主网络下的并发打洞 | **无法覆盖原因**：CI 虚拟环境与单机隔离网络缺乏真实 NAT 路由器分级（Full Cone vs Symmetric NAT），需依赖外部物理测试场或手工验证（参见 #144, #145） |

---

## 4. 关键缺陷与高危盲区深度剖析

### 4.1 盲区一：对称打洞冲突 (Symmetric Glare Condition)
- **现象描述**：在去中心化 P2P 网络中，Node A 发现 Node B，同时 Node B 也发现 Node A。双方几乎在同一微秒向信令发送针对对方的 `OFFER` 消息。
- **协议状态**：
  - Node A 正在等待来自 Node B 的 `ANSWER`，却收到了 Node B 的 `OFFER`；
  - 根据 WebRTC 标准规范，此种情况产生 Glare。如果底层未实现 Rollback（`setLocalDescription` 撤销）或基于 PeerID 字典序决胜（Higher ID takes Offerer role, Lower ID yields to Answerer），会导致双方连接均陷入僵死或生成两对重复连接。
- **当前现状**：`back/peerjs/peer.go` 中目前收到 Offer 时直接创建 Connection 并应答，未比对已存在 outbound connection，极易引发双向冗余连接竞争。

### 4.2 盲区二：重连幂等与僵尸会话残留 (Zombie Connection Leaks)
- **现象描述**：客户端由于移动网络切换或瞬时网络抖动快速重连，以相同的 PeerID 或新的随机 PeerID 请求节点。
- **潜在风险**：
  - 老连接在 TCP/UDP 层面已成为半开连接（Half-open），心跳检测超时需 5s~15s 才触发；
  - 节点内部 `peerLocks` 或活跃连接表未清理，导致新连接被误判为冲突或内存连接泄漏（关联 #214）；
  - 未落盘的传输任务在重连后无法准确定位未完成文件位置。

### 4.3 盲区三：多端向单节点写并发的 SQLite 锁争用
- **现象描述**：2~3 个客户端同时向同一节点上传分片数据。
- **潜在风险**：
  - 每个文件上传结束均需调用 `file_index` 注册（`UpsertFileIndex`），该方法涉及 `SELECT MAX(seq) + INSERT` 严格事务；
  - 虽然配有 `busy_timeout=5s` 与 WAL 模式，但在密集上传时频繁的长事务可能引发高负载写阻塞。

---

## 5. 后续补测演进路线 (高性价比实施方案)

根据先盘点、后补测原则，建议后续测试按以下优先级分步落地：

```mermaid
flowchart TD
    M[矩阵盘点完成 (#205)] --> P0[P0: 对称打洞冲突 (Glare) 测试]
    M --> P1[P0: 重连幂等与重复拨号测试]
    P0 --> P2[P1: 双客户端并发下载与背压隔离测试]
    P1 --> P3[P1: 双客户端并发上传与 SQLite 争用测试]
    P2 --> P4[P2: 存在房间 N 节点网状防风暴测试]
```

### 优先级规划详情

1. **P0 - 对称失败 (Symmetric Glare) 单元测试**
   - **目标包**：`back/peerjs/`
   - **测试场景**：构造虚拟通道，让 Peer 1 与 Peer 2 同时互发 Offer，断言至少有一条连接稳定达成 `open` 状态，且多余连接被确定性清理，不产生资源泄漏。
2. **P0 - 重连幂等 (Reconnect Idempotency) 集成测试**
   - **目标包**：`back/test/integration/`
   - **测试场景**：建立连接并传输部分数据 -> 强杀传输通道 -> 相同 Client 立即以相同 Identity 重新建链 -> 验证旧会话被驱逐、新会话无阻塞复用。
3. **P1 - 双端向单节点并发下载与流控隔离**
   - **目标包**：`back/peerjs/flowcontrol_test.go`
   - **测试场景**：Client A（满带宽消费）与 Client B（人工暂停读取触发背压）同时从 Server 读数据，验证 Client B 的挂起不阻塞 Client A 的数据流分派。
4. **P1 - 双端向单节点并发上传**
   - **目标包**：`back/test/integration/`
   - **测试场景**：Client A 与 Client B 同时 POST/Upload 不同文件，验证落盘后 SHA-256 校验和 SQLite `file_index` 连续自增 seq 正确。

---

## 6. 约束与 CI 映射

1. **子模块测试执行边界**：
   - `back/peerjs/` 的所有新增测试仅能在独立模块下通过 `go test ./...` 运行，或在根目录下由 CI 的 `submodules` job 和 `peerjs` job 执行；
   - 编写并发用例必须带 `-race` 检查，严格杜绝数据竞争。
2. **零外网依赖原则**：
   - 并发测试必须采用内存虚拟信令或本地 `127.0.0.1` 环回接口，不得依赖公共 STUN/TURN 服务器或外部 MQTT Broker，避免因网络抖动造成 CI 偶发红灯。
3. **超时与防假死规范**：
   - 并发与重试测试必须绑定 `context.WithTimeout`（单用例建议不超过 5s），严禁无超时死等 channel。
