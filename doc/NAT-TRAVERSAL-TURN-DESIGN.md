# NAT 打洞与 TURN 中继支持架构设计规范

> 对应 Issue: #144  
> 状态: 架构规范与需求确立（按 Issue 边界：明确需求与技术方案，待后续专项推进）  
> 关联 Issue: #145 (双栈策略), #90 (基础设施层外移), #88 (访问形态分层), #72 / #99 (连接稳定性)

---

## 1. 现状痛点与根本原因诊断

### 1.1 现状与代码实读
当前 Peerdrive 系统在 WebRTC ICE 配置上存在严重缺口：
- **STUN 配置**：`PEERDRIVE_WEBRTC_STUN` 默认启用 Google 公共 STUN 服务器（`stun:stun.l.google.com:19302`），能够正常发现 `host` 与 `srflx`（Server Reflexive）候选者。
- **TURN 配置**：`PEERDRIVE_WEBRTC_TURN` 默认为空。即使运维显式配置了地址（如 `turn:turn.example.com:3478`），代码解析仅处理了 URL：
  ```go
  // back/internal/transport/peerjs_service.go:721
  if turn != "" {
      urls := strings.Split(turn, ",")
      out = append(out, webrtc.ICEServer{URLs: urls})
  }
  ```
  `webrtc.ICEServer` 的 `Username`、`Credential`、`CredentialType` **完全缺失**。

### 1.2 对称 NAT 无法互通的必然性
在现实家庭与移动蜂窝网络（如移动 4G/5G、部分光猫二级路由）中：
1. **对称 NAT（Symmetric NAT）特性**：NAT 设备不仅根据源 IP/端口分配公网端口，还会对每一个不同的目标地址（Destination IP/Port）动态映射全新的公网端口。
2. **打洞彻底失效**：两个对称 NAT 节点之间，STUN 探测到的端口与相互发包时分配的端口完全不一致，ICE 的常规 UDP 打洞成功率为 0%。
3. **TURN 认证失败**：RFC 5766 规范要求 TURN 服务器在处理 `Allocate` 请求时必须进行凭据认证（长期凭据机制或 REST 临时凭据）。由于当前配置无密码，TURN 服务器返回 `401 Unauthorized`，导致 relay 候选者永远无法生成。
4. **结论**：**当前两个处于对称 NAT 后的 Peerdrive 节点 100% 无法建立直连通道。**

---

## 2. TURN 凭据结构化设计

### 2.1 配置项规范
为兼容标准 URI 语法与运维习惯，支持两种配置模式：

1. **标准 URI 格式（推荐，内嵌凭据）**：
   ```bash
   # 格式: turn:<user>:<pass>@<host>:<port>[?transport=udp|tcp]
   PEERDRIVE_WEBRTC_TURN="turn:alice:secret123@turn.moonchan.xyz:3478,turns:alice:secret123@turn.moonchan.xyz:5349"
   ```
2. **环境变量拆分格式（兼容传统运维）**：
   ```bash
   PEERDRIVE_WEBRTC_TURN="turn:turn.moonchan.xyz:3478"
   PEERDRIVE_WEBRTC_TURN_USER="alice"
   PEERDRIVE_WEBRTC_TURN_PASS="secret123"
   ```

### 2.2 解析与装配契约
`parseICEServers` 扩展为支持标准 URI 解析与凭据装配：
- 若 URL 包含 userinfo（如 `turn:u:p@host`），使用 `url.Parse` 提取用户名和密码；
- 否则回退使用 `TURN_USER` / `TURN_PASS`；
- 装配为：
  ```go
  webrtc.ICEServer{
      URLs:           []string{cleanURL},
      Username:       username,
      Credential:     password,
      CredentialType: webrtc.ICECredentialTypePassword,
  }
  ```

### 2.3 临时凭据与时间戳轮换机制（Coturn REST API）
对于采用 Coturn 共享密钥（`use-auth-secret`）的生产环境：
- 用户名为 `timestamp:username`；
- 密码为 `Base64(HMAC-SHA1(secret, username))`；
- 增加凭据刷新钩子（Refresh Hook），在凭据临近过期（如 10 分钟前）重新计算凭据或向认证服务请求新 Token，无需重启服务。

---

## 3. 连接自检与失败可见性设计

### 3.1 启动期自检（Startup Probe）
服务启动或配置更新时，异步触发一次对 TURN 服务器的健康自检：
1. 创建轻量临时 PeerConnection 发起本地 ICE 收集；
2. 监听是否成功收到 `typ relay` 的候选者：
   - 收到 `typ relay` $\rightarrow$ 标记 `turn_status: "ok"`；
   - 收到 401 / 认证失败 $\rightarrow$ 产生 `LogWarn` 并在诊断状态中标记 `turn_status: "auth_failed"`；
   - 超时未响应 $\rightarrow$ 标记 `turn_status: "unreachable"`。

### 3.2 错误日志与失败原因精准归因
1. **暴露 Candidate 错误**：
   - 在 `back/peerjs/connection.go:232` 中：
     ```go
     if err := c.pc.AddICECandidate(payload.Candidate); err != nil {
         log.LogWarn("peerjs: failed to add remote ice candidate (%s): %v", payload.Candidate.Candidate, err)
     }
     ```
2. **连接失败智能诊断**：
   - 当 `ICEConnectionState` 转移至 `Failed` 时，遍历本地与对端的候选者对：
     - 若两端仅有 host/srflx candidate 且无 relay $\rightarrow$ 明确输出日志：「`ICE failed: symmetric NAT detected and no valid TURN relay candidate available`」；
     - 避免用户陷入无法区分信令断开与打洞失败的困境。

### 3.3 状态端点与前端拓扑透传
在连接状态 API（如 `/peerjs/peers` 与前端面板）中透传连接路径属性：
```json
{
  "peerId": "node-remote",
  "connected": true,
  "transport": {
    "isRelayed": true,
    "localCandidateType": "relay",
    "remoteCandidateType": "srflx",
    "protocol": "udp"
  }
}
```
前端 UI 在对端节点卡片上标注「中继连接（TURN）」或「点对点直连（P2P）」，让运维与用户清晰掌握流量走向。

---

## 4. 实施路线图

1. **阶段 1：解析增强与凭据支持**
   - 扩展 `config.Config`，支持 `PEERDRIVE_WEBRTC_TURN_USER` / `PASS` 及内嵌凭据解析；
   - 完善 `parseICEServers`，构造合法的 `webrtc.ICEServer`。
2. **阶段 2：异常日志与自检链路**
   - 补充 `AddICECandidate` 错误处理；
   - 引入启动期 TURN Allocate 探针与状态暴露。
3. **阶段 3：前端链路透传**
   - 暴露 candidate pair 类型至前端，完成面板 UI 状态展示。
