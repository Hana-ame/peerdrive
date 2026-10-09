# 节点发现优先回退链与 Lobby 公共节点列表设计规范

> 对应 Issue: #201  
> 状态: 架构规范与最小实现评估  
> 关联 Issue: #147 (发现多通道合并), #145 (IPv6/IPv4 双栈), #146 (DHT 评估), #159 (信令默认配置), #89 (节点准入防匿名), #155 (localStorage 集中化)

---

## 1. 架构目标与动机

Peerdrive 节点发现过去依赖单一的静态配置（`PEERDRIVE_PEERJS_PEERS`）或单一的中心信令/MQTT 广播。为了实现「开箱即用、本地优先、离线鲁棒、公网可触达」的使用体验，本规范定义了一条**有优先级的发现 Fallback 链**：

```text
┌─────────────────────────────────────────────────────────────┐
│                   优先级 0: 本地回环探测 (Loopback)          │
│               127.0.0.1:3000 / [::1]:3000 (超时 50~100ms)    │
└──────────────────────────────┬──────────────────────────────┘
                               │ (未命中 / 本地未启动)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                   优先级 1: Lobby 公共节点池                 │
│         Recently 客户端历史 (前端/本地) + last_heartbeat 排序 │
└──────────────────────────────┬──────────────────────────────┘
                               │ (未命中 / 离线 / 独立私有组网)
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                   优先级 2: 现有发现通道                     │
│         HTTP Discovery (/discover/nodes) / MQTT 分片房间    │
└─────────────────────────────────────────────────────────────┘
```

---

## 2. 最小实现评估（核心问题论证）

### 2.1 问题 1：`relayList` 加 `ORDER BY last_heartbeat DESC` + 过期剔除的成本与风险

#### 现状分析
在 `back/signalserver/regserver/handlers_relay.go` 中，现有 `GET /p2p/relay/list` 执行全表无序扫描：
```sql
SELECT peer_id, addrs, storage_mb, load_pct, version, registered_at, last_heartbeat FROM relay_nodes
```
- **无排序**：`last_heartbeat` 未被利用，死节点与活跃节点随机混杂；
- **无过期**：下线节点永久占据查询列表；
- **无分页/截断**：节点增多后列表体积无限制增长。

#### 建议改动（两行 SQL 升级）
保持现有 `relay_nodes` 表结构不变（零 DDL 迁移），修改查询为：
```sql
SELECT peer_id, addrs, storage_mb, load_pct, version, registered_at, last_heartbeat 
FROM relay_nodes
WHERE datetime(last_heartbeat) >= datetime('now', '-10 minutes')
ORDER BY last_heartbeat DESC
LIMIT 50
```

#### 成本与风险评估
1. **实现成本**：极低（仅修改 `handlers_relay.go` 内部 SQL 文本，新增可选 `?all=true` 用于运维审计）。
2. **时钟一致性**：`CURRENT_TIMESTAMP` 和 `datetime('now')` 均为 SQLite 服务端 UTC 产生，完全免疫客户端时钟回拨与时区差异。
3. **性能风险**：当前 `relay_nodes` 主键为 `peer_id`，未对 `last_heartbeat` 建立二级索引。
   - 节点规模 `< 10,000` 时，SQLite 单表扫描与内存排序耗时 `< 1ms`，零性能瓶颈；
   - 节点规模 `> 10,000` 时，可平滑增加索引 `idx_relay_nodes_heartbeat`，当前阶段无需提前过度设计。
4. **存活窗口选型**：节点心跳周期为 60s，将过滤窗口设为 10 分钟（即允许 10 次丢包容错），兼顾了死节点清理及时性与瞬时网络波动的稳定性。

---

### 2.2 问题 2：本地回环探测的最小形态与预算约束

#### 探测目标与端口
- **目标端点**：优先探测 `127.0.0.1:3000/api/health`（或 `/status`）。
- **IPv6 双栈对齐（遵循 #145）**：若 `127.0.0.1` 握手快速被拒绝（ECONNREFUSED），且环境支持 IPv6，备选回环探测 `[::1]:3000`。
- **端口范围**：严格限制仅探测 Peerdrive 默认标准端口 `3000`（或用户显式配置的候选列表）。严禁全端口盲扫，防止触发主机杀毒软件阻断。

#### 超时预算与执行策略
- **超时上限**：单次探测预算严格限制在 **50ms ~ 100ms**。因为回环地址不经物理网卡，正常本机监听进程在 1ms 内必定完成握手响应；超过 100ms 无响应即可确定本机无节点运行。
- **非阻塞（Non-blocking）约束**：
  - 本地探测作为客户端/前端界面的**并发先验竞争任务**（`Promise.race`）；
  - 探测失败或超时立即静默降级进入 Lobby 搜索，不阻塞 UI 渲染与启动流程。

---

### 2.3 问题 3：`DiscoverMode` 模式语义与 Fallback 链关系

#### 决策：前置步骤（Pre-step）而非孤立互斥模式值
现有 `config.DiscoverMode`（`auto` / `peerjs` / `discover` / `mqtt` / `off`）用于声明外网发现通道的调度方式。

如果将本地回环与 Lobby 定义为一个新的枚举值（如 `DiscoverMode = "local"`），会导致用户在本地节点未启动时被彻底孤立，违背 Fallback 链的设计初衷。

#### 建议落地机制
1. **本地探测作为前置拦截链（Priority 0 Hook）**：
   - 只要当前不是显式纯离线模式，初始化时均快速探测本地；
   - 若本地存在，直接挂载本地实例作为 Preferred Gateway；
2. **Lobby 作为公共发现的第一梯队（Priority 1）**：
   - 当 `DiscoverMode = "auto"` 且 `DiscoverURL` 为空时，自动 fallback 查询注册服务公布的有效 Lobby 列表；
3. **配置解耦**：
   - 新增布尔开关 `PEERDRIVE_DISCOVER_LOCAL=true`（默认启用，允许测试环境显式置为 false 屏蔽本地探测）。

---

### 2.4 问题 4：Lobby 的认证取舍与防女巫/防污染机制（与 #89 对齐）

#### 冲突分析
- **#89 准入控制**：强调身份校验、PSK 门禁、黑白名单防匿名，保证私有数据资产安全；
- **Lobby 公共节点池**：面向开箱即用体验与公网资源发现，倾向于自荐式开放注册。

#### 取舍与防御方案
1. **显式自荐意愿**：节点必须显式开启 `PEERDRIVE_LOBBY_ENABLE=true`（或 `PEERDRIVE_RELAY_REGISTER=true`）才向上游登记入池，默认配置保持静默（不擅自对外暴露存在）；
2. **多层防污染防线**：
   - **L0 限流防御**：复用现有的基于客户端 IP 的 Token 桶速率限制，阻断批量注册机器人的请求突发；
   - **L1 心跳活性校验**：通过 `last_heartbeat` 强制沉没超过 10 分钟无心跳的幽灵条目；
   - **L2 身份背书（Phase 7 演进）**：未来引入节点身份公钥签名校验，在 Lobby 列表内展示 `verified` 徽章，普通客户端优先连接已认证节点。

---

## 3. Recently 访问记录数据落点与排序算法

客户端排序依赖服务端客观状态（`last_heartbeat`）与客户端主观历史（`recently_contacted`）的复合计算。

### 3.1 数据存储落点
1. **服务端（Server Side）**：
   - 仅负责维护公共中继表的 `last_heartbeat` 与 `load_pct`，不存储任何特定客户端的私有访问历史。
2. **客户端面板（Front / Panel Side）**：
   - 遵循 #155 集中化规范，落入浏览器 `localStorage`：
   - 键名：`peerdrive_recent_peers`；
   - 结构：`Array<{ peerId: string, lastConnected: number, alias?: string }>`；
   - 容量约束：上限 20 条，LRU 滚动剔除。
3. **Headless Go 节点（Node Side）**：
   - 保存在节点状态目录下的 `recent_peers.json`（与 `joined_nodes.json` 保持格式对称，但生命周期为时间驱动）。

### 3.2 组合排序权重计算
客户端从服务端拉取活跃 Lobby 节点后，执行客户端侧重排：

```text
得分 Score = S_recent + S_heartbeat + S_load

其中：
- S_recent: 若在近期 7 天内成功连接过，权重加权 +1000 分，按最后连接时间线性递减；
- S_heartbeat: 距离当前心跳越近，得分越高（10 分钟内按秒衰减，0 ~ 100 分）；
- S_load: 低负载加权（(100 - load_pct) * 0.1 分）。
```

---

## 4. 实施里程碑计划

| 阶段 | 交付物 | 范围 |
|---|---|---|
| **Phase 1 (本阶段)** | 架构设计规范与最小实现评估 | 产出技术规范，明确 Fallback 语义与边界 |
| **Phase 2** | regserver 查询端点升级 | 为 `GET /p2p/relay/list` 增加心跳排序与活性窗口 |
| **Phase 3** | 前端与客户端本地回环及最近访问 | 前端/客户端实现 50ms 回环探测 + `localStorage` 最近访问优先排序 |
