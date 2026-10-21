# 服务器基础设施层（注册/中转/发现/DNS）解耦架构方案与落地路线

> 本文档对应 Issue #90：服务器基础设施层（注册/中转/发现/DNS）从主模块剥离的现状核对、Go `internal/` 约束分析与落地演进规划。

---

## 1. 现状核对与职责边界

在 Peerdrive 架构中，节点本质为自托管与去中心化文件网络节点，而「注册/中转/发现/DNS」等基础设施角色往往由长期在线的中枢实例或公共服务提供。现状四项技术点的代码分布如下：

### 1.1 注册服务 (`regserver`)
- **物理位置**：`back/signalserver/regserver/`
- **代码结构**（已在 Issue #200 中完成独立模块解耦）：
  - `regserver.go`：Server 结构体装配、速率限制与生命周期（`New`, `Handler`, `Serve`, `Close`）。
  - `jwt.go`：JWT 签发与校验（HS256，字节级兼容原独立版本）。
  - `auth.go`：JSON 工具与 `net/http` 原生 Bearer 认证中间件（无 Gin 依赖）。
  - `db.go`：SQLite 打开与 schema 定义（`users`, `relay_nodes` 两张表）。
  - `handlers_auth.go`：`/auth/*` 路由处理与 bcrypt 辅助函数。
  - `handlers_relay.go`：`/p2p/relay/*` 路由处理与地址序列化。
  - `tracker_auth.go`：BitTorrent tracker 的 `/tracker/bans` 封禁管理与 JWT 适配。
- **依赖耦合分析**：
  - **入向引用（谁依赖它）**：均位于装配与外层边界：
    - `back/cmd/peerdrive/subcommands.go`（`runReg` 与 `runHub` 子命令）
    - `back/internal/services/services.go`（`UnifiedMux` 多服务挂载）
    - `back/signalserver/cmd/peerserver/main.go`（独立辅助二进制）
  - **出向引用（它依赖谁）**：零 `peerdrive/internal/*` 依赖，纯净标准库与独立模块内部 `tracker`。

### 1.2 中转服务 (`relay`)
- **物理现状**：仓库内并无独立的 `internal/relay` 包。
- **实现位置**：所有中继登记与心跳逻辑已全部收敛在 `back/signalserver/regserver/handlers_relay.go`：
  - `POST /p2p/relay/register`（幂等 upsert 节点地址与负载）
  - `POST /p2p/relay/heartbeat`（心跳保活更新）
  - `GET  /p2p/relay/list`（查询在线中继节点）
- **边界定性**：`relay` 不是独立待拆的模块，而是 `regserver` 内部管理中心服务器视角下的 `relay_nodes` 登记表，已天然与注册服务同生共存。

### 1.3 发现服务 (`discovery`)
- **客户端形态**：位于 `back/internal/transport/`：
  - `http_discovery.go`（HTTP 轮询自托管信令发现 API）
  - `mqtt_discovery.go`（MQTT 公共 broker 分片房间广播与监听）
- **服务端形态**：**已经是独立模块**：
  - `back/signalserver/` 拥有独立的 `go.mod`（`github.com/Hana-ame/go-peerserver`，tag v0.1.0 同步），并产出独立二进制 `cmd/peersignal` 与 `cmd/peerserver`。
  - 主模块通过 `PEERDRIVE_DISCOVER_URL` 与自托管信令发现 API 交互。

### 1.4 DNS 解析与发现 (`DNS`)
- **物理现状**：仓库内无 DNS 相关 Go 代码包（`grep -rn dns back/` 无业务包）。
- **定位**：DNS 属于运维部署与基础设施编排范畴（见 `AGENTS.md`「线上部署」：`peersignal.moonchan.xyz` 走 Cloudflare 橙云 Proxied A 记录回源）。不应在后端主模块引入多余的自定义 DNS 协议解析代码。

---

## 2. Go `internal/` 约束与解耦难点

Go 编译器的 `internal/` 访问控制规则强制规定：`internal` 目录下的包只能被同父级目录树下的包导入。

`back/signalserver/regserver` 已成功迁出主模块 `internal/`，成为独立模块 `go-peerserver` 的子包：
1. **消除对 internal 的所有依赖**：解耦后不再导入 `peerdrive/internal/*`，自包含令牌桶、退避与端口规范化。
2. **主模块跨模块引入**：主模块通过 `github.com/Hana-ame/go-peerserver/regserver` 引入。

### 应对模式对比

| 模式 | 做法 | 优势 | 代价 / 风险 |
|---|---|---|---|
| **模式 A: 提升公共包至 `back/pkg/`** | 将 `internal/httpd`、`internal/ratelimit` 提升至 `back/pkg/` | 解除 `internal` 隔离，主模块与基础设施模块均可纯净引用 | `pkg/` 成为公共 API 面，需遵守向后兼容契约 |
| **模式 B: 独立 module 模块** | 将 `regserver` 独立为子模块，主 `go.mod` 通过 `replace` 本地链接 | 进程边界清晰，依赖面完全解耦，可独立发布二进制 | 引入多版本 tag 维护义务（类似 `go-peerjs` 与 `go-peersignal`） |
| **模式 C: 并入现有自托管信令** | 将 `regserver` 与 `signalserver` 合并为统一的中心基础设施服务 | 减少独立组件数量，发现与认证统一托管 | 改变了 `signalserver` 纯轻量信令的单一职责 |

---

## 3. 分阶段落地演进路线

```
[阶段 1: 已完成] ──> PR #42 将 regserver.go 单文件解构为 6 个高内聚文件 (含 relay 路由收拢)
       │
[阶段 2: 依赖解耦] ──> 评估并提取 internal/ratelimit 与 httpd 至 back/pkg/
       │
[阶段 3: 独立模块] ──> 将 regserver 提升为独立 go.mod 模块，主 cmd 通过接口装配
       │
[阶段 4: 部署收敛] ──> 保持 signalserver 独立信令定位，DNS 维持外部解析治理
```

### 3.1 阶段 1：handler 与职责拆分（已完成）
在 PR #42 中，656 行单文件已拆解完毕，数据模型与中间件彻底剥离了对 Gin 的依赖，纯基于 `net/http` 与 SQLite。

### 3.2 阶段 2：公共依赖解耦（下一步）
1. 梳理 `ratelimit` 逻辑，将其作为通用的 `pkg/ratelimit` 暴露。
2. 消除 `regserver` 对 `internal/httpd` 的微小辅助函数依赖，使 `regserver` 完全不依赖任何 `peerdrive/internal/*`。

### 3.3 阶段 3：独立模块抽取
1. 在规划的独立子目录创建独立 `go.mod`。
2. 主仓库 `cmd/peerdrive` 仅通过顶层接口调用 `regserver`。
3. 主 `go.mod` 增加本地 `replace` 声明。

### 3.4 阶段 4：目录与 CI 守卫
- 严格遵循 `AGENTS.md` 硬约束：仓库根目录始终保持 `/front` + `/back` + `/doc`，不新增顶层目录。
- CI `go-build.yml` 的 `submodules` job 追加新模块的交叉编译与测试矩阵。
