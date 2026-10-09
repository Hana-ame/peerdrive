# IPv6/IPv4 双栈地址族统一策略规范

> 对应 Issue: #145  
> 状态: 需求规范与架构设计已确立（按 Issue 边界：明确需求与策略规范，待真机双栈抓包实测后推进实现）  
> 关联 Issue: #144 (NAT 打洞与 TURN 支持), #107 (ECH 出口与 IPMode), #90 (发现层外移), #115 (架构总纲)

---

## 1. 现状剖析与跨路径断层

在当前 Peerdrive 系统中，不同通信子系统对 IPv6/IPv4 的处理存在明显断层与不一致性：

| 链路 | 当前实现机制 | 存在问题与缺口 |
|---|---|---|
| **ECH 出口 (`echcore`)** | 支持 `IPMode` (`v4` / `v6` / `auto`) 与 `SetIPMode` | 仅控制出站 HTTP 代理，无法作用于节点间 P2P 通信 |
| **PeerJS DataChannel (WebRTC)** | 由 Pion WebRTC 默认收集 host/srflx candidate | 无地址族倾向控制；在多宿主/家庭网络（v6 具公网地址但防火墙拦截，v4 走 NAT）下容易协商僵死 |
| **HTTP Discovery (`/discover`)** | 仅按 TCP 连接对端套接字（`r.RemoteAddr`）记录单个 IP | 客户端若是双栈，仅上报当前 HTTP 握手所用的单个族，遗漏另一地址族 |
| **信令服务器 (PeerJS Signal)** | 取决于信令域名的 DNS 解析（A / AAAA 记录） | 若信令仅有 A 记录（如部分自建 VPS），纯 IPv6 节点无法建连 |

### 1.1 真实踩坑经验（从 `echcore` 继承）
`back/internal/echcore/ech.go:469` 记录的关键教训：
> *"Some upstreams reject IPv6 sources (e.g. pixiv returns a static 403 'Access blocked'); set ip_mode: v4 in config to pin the family."*

在真实互联网中，部分 CDN、网关或上游服务对 IPv6 存在严格的风控、丢包或封锁；而在 P2P 直连场景下，国内部分运营商虽然下发了 IPv6 公网地址，但上层入站防火墙默认丢弃未经主动出站放行的 SYN/UDP 包。**地址族不能仅靠系统默认，必须支持配置钉选（Pinning）**。

---

## 2. 统一配置：`PEERDRIVE_IP_MODE`

将 `echcore` 的局部配置提升为节点全局级配置环境变量：

```bash
# 可选值: auto (默认) | v4 | v6 | dual
PEERDRIVE_IP_MODE=auto
```

### 语义定义：
1. `auto`（默认智能模式）：
   - 优先尝试双栈协商；
   - 遵循 RFC 8305（Happy Eyeballs v2）算法：并发探测 v6 与 v4，优先尝试 v6，但若 250ms 内无响应立即启动 v4，谁先就绪用谁。
2. `v4`（强制 IPv4 钉选）：
   - 显式关闭 IPv6 地址收集与解析；
   - ECH 出口仅发起 A 记录解析与 IPv4 拨号；
   - WebRTC Pion 禁用 IPv6 candidate；
   - HTTP Announce 仅上报 IPv4 地址。
3. `v6`（强制 IPv6 钉选）：
   - 专用于纯 IPv6 内网或专线部署；
   - 禁用 IPv4 candidate；
   - 忽略 A 记录，仅尝试 AAAA 记录。
4. `dual`（显式双栈并存）：
   - 全量上报两个地址族；
   - WebRTC 收集两套 candidate，依靠 ICE 优先级由对端匹配。

---

## 3. 三条路径的实施落地规范

### 3.1 HTTP Discovery 的多地址感知与上报
改造 `HTTPDiscovery` 与 `signalserver` 的 announce 协议字段：
```json
{
  "peerId": "node-1",
  "addresses": {
    "v4": "203.0.113.195",
    "v6": "240e:xxx:xxxx::1"
  },
  "ipMode": "auto"
}
```
- **服务端处理**：如果请求体未携带显式外网 IP，服务端根据接入连接判定族别，并与历史未过期的异族记录做关联聚合；
- **对端节点消费**：在节点列表中同时展示 v4/v6 标签，便于网络拓扑可视化与针对性打洞。

### 3.2 WebRTC Pion DataChannel 的 Candidate 排序与过滤
通过 Pion 的 `webrtc.SettingEngine` 进行网络层控制：
1. **当 `IP_MODE == "v4"` 时**：
   ```go
   settingEngine.SetNetworkTypes([]webrtc.NetworkType{
       webrtc.NetworkTypeUDP4,
       webrtc.NetworkTypeTCP4,
   })
   ```
2. **当 `IP_MODE == "auto"` 或 `"dual"` 时**：
   - 开启全部网络类型（UDP4, UDP6, TCP4, TCP6）；
   - 自定义 ICE Candidate 优先级计算规则，确保在家庭宽带直连时优先尝试 IPv6 host candidate（无需经过 STUN 服务器直接直连），500ms 内未连通平滑回落至 IPv4 srflx candidate。

### 3.3 信令服务器域名解析策略
- 自托管信令部署（`peersignal` / Cloudflare 橙云反代）：
  - 域名必须配置完整的 A（IPv4）与 AAAA（IPv6）双栈解析；
  - 客户端信令拨号时引入 2 秒超时回落，防止因 IPv6 路由黑洞造成信令连接长达 30 秒的挂起。

---

## 4. 降级与容灾流程

```text
       [开始连接目标节点]
               │
      PEERDRIVE_IP_MODE?
        /      |      \
     v4        auto     v6
     │         │         │
 仅走 v4   [Happy Eyeballs] 仅走 v6
               │
          ┌────┴────┐
         v6        v4 (250ms 延后启动)
          │         │
          └────┬────┘
               │ 谁先连通使用谁
          [通道建立]
```

- **链路故障静默回落**：若 IPv6 在建立 DataChannel 阶段持续 5 秒协商无进展（通常因中间防火墙静默丢包），ICE 引擎自动触发 candidate-pair 降级为 IPv4 / STUN 反射地址，无需断开重拨。

---

## 5. 实测验收基准（后续实现的前置条件）

按 Issue 边界，编码前需在真机双栈环境下捕获实测数据：
1. 节点 A（双栈）向信令 announce，验证 `/discover/nodes` 返回的地址记录是否准确反映实际网络环境；
2. 节点 A（双栈）与节点 B（纯 IPv4）进行 WebRTC DataChannel 握手，抓包确认 candidate 协商是否在 1 秒内无阻塞落入 IPv4，无协议悬挂。
