# 模块 14：nodestate 运行时共享状态

- **代码位置**：`back/internal/nodestate`（唯一文件 `back/internal/nodestate/nodestate.go`，目录内无 `_test.go`）
- **功能一句话**：以进程内包级变量保存节点运行时身份/连接状态（operator 用户名、reg server URL、认证 token、peerID），并提供向注册服务器上报传输统计的能力；独立成包是为了打破 controller↔service 的循环导入（包注释原话）；当前代码中它只被 controller 读取，**没有任何写入调用方**。
- **依赖**：仅 Go 标准库（`sync`/`encoding/json`/`net/http`/`net/url`/`bytes`/`time`/`fmt`，`back/internal/nodestate/nodestate.go:5-15`）；零业务依赖。
- **被依赖**：`back/internal/controller`——全 back 模块（`go list ./...` 扫描 + 符号检索）中**唯一**导入方：`back/internal/controller/anon.go` 6 处、`back/internal/controller/p2p.go` 1 处调用 `nodestate.GetOperator()`。包注释声称「controller 和 service 都需要访问此状态」（nodestate.go:2），与实际导入关系不符（见 §5）。

---

## 1. 逻辑

**职责**（nodestate.go:1-3 包注释）：存储 Peerdrive 节点的运行时状态（operator 用户名、reg server 连接信息），并提供统计报告功能；因 controller 和 service 都需要访问此状态，独立为一个包以打破循环导入。

**核心形态：全局单例的包级变量，而非结构体**。本包没有定义任何 `struct`/接口，状态就是 4 个包级 `string` 变量加一把锁（nodestate.go:17-23）：

```go
var (
	mu        sync.Mutex
	operator  string
	regURL    string
	authToken string
	peerID    string
)
```

**公开 API 共 5 个函数**：

| 函数 | 作用 | 代码位置 |
|---|---|---|
| `Configure(op, url, token, pid string)` | 一次性全量设置四个字段 | nodestate.go:26-33 |
| `SetOperator(username string)` | 单独设置 operator（空串 = 匿名） | nodestate.go:36-40 |
| `GetOperator() string` | 读 operator | nodestate.go:43-47 |
| `GetPeerID() string` | 读 peerID | nodestate.go:50-54 |
| `ReportStats(uploadBytes, downloadBytes int64)` | 向 reg server 上报传输统计 | nodestate.go:57-86 |

除 `Configure` 外每个函数都独立加 `mu.Lock()` 保护（写函数 27-32/37-39，读函数 44-46/51-53，ReportStats 先加锁拷快照再解锁 58-62）。

**主流程有两个**：

1. **读流程（当前唯一被真实触发的流程）**：HTTP 请求到达 controller 的匿合集端点 → handler 调 `nodestate.GetOperator()` 得到本节点 operator → 作为 `owner`/`requester` 传入 `service.AnonService`（`back/internal/controller/anon.go:51,97,141,163,239,317`）→ service 把该值写进匿合集 JSON 的 `Owner` 字段落盘（`back/internal/service/anon_service.go:131,140-160`）。也经 `GET /p2p/auth/status` 返回给前端（p2p.go:895）。
2. **上报流程（按设计存在、当前无调用方）**：`ReportStats` 守卫生效后把 `peerID` 与上传/下载字节数 POST 到 `regURL + "/auth/node/stats"`，带 `Authorization: Bearer <authToken>` 头（nodestate.go:78-81）。

**生命周期**：进程级。包级变量在进程启动时即存在（零值空串），无初始化函数、无 `Close`/`Stop` 钩子，进程退出即整包状态消失。`cmd/server/main.go` 的启动/优雅停机流程（main.go:49-285）完全不涉及本包。

> 注：模块地图对它的定位是「operator/reg/peerID 进程内共享状态（打破循环导入的独立包）」（doc/design/如何连接.md:28、doc/design/README.md:29）；架构评审文档的结论是「nodestate 包仅为解决循环引用」（doc/archive/report/ARCHITECTURE-REVIEW.md:34-35）。

---

## 2. 如何储存

**介质与位置：纯进程内存，不持久化。** 状态只存在于 `back/internal/nodestate/nodestate.go:17-23` 的包级变量里，没有任何落盘/数据库/文件写入，也没有任何导出持久化路径——本包不 import repository/storage，依赖清单只有标准库（nodestate.go:5-15）。

**内存态构成**：4 个 `string`（`operator`、`regURL`、`authToken`、`peerID`）+ 1 把 `sync.Mutex`；没有 struct 实例、没有 map/slice。初始值全部为 Go 零值空串。

**生命周期**：进程启动即存在 → 进程退出即清零，无中间持久化。**进程重启后四个字段全部回到 `""`**，无恢复机制（找不到任何从磁盘/DB 回填的代码路径；`cmd/server/main.go` 全流程不触碰本包）。

**委托关系（值会流向下游并被下游持久化，但本包自身不落盘）**：`GetOperator()` 读出的值被 controller 作为 `owner`/`requester` 传给 `service.AnonService`，最终**由 service 层**写进内容寻址的匿合集 JSON 并登记数据库（`back/internal/service/anon_service.go:128-160`：`coll.Owner = owner` 参与 `json.MarshalIndent` → `sha256Hex` → 写 `storage/<hash 前两位>/<hash>` → `repository.InsertFileMeta` + `InsertFileProvider`）。即：nodestate 提供的是进程内「暂存位」，持久化发生在 service/repository/storage 的下游链路（详见 §6 连接 03/04）。

---

## 3. 何时储存

严格区分「写入」与「读取」两个方向，如实呈现：

**写入（4 个写入口全部**无调用方**——按设计存在、当前代码中零触发点，标注未核实）：**

| 写入口 | 代码中的声明时机 | 实际触发点 |
|---|---|---|
| `Configure` | 注释声明「由 NodeRegistrar.Start() 调用」（nodestate.go:25），即节点启动阶段 | **未核实：全仓库无 `NodeRegistrar` 类型/调用点**（go list 全模块扫描 + 符号检索均无） |
| `SetOperator` | 注释语义：「节点登录 regserver 后登记的运营者账号」（对照 p2p.go:888-890 与 doc/REFACTOR.md:567,595 的语义描述） | **未核实：无任何调用点**，operator 因此恒为空串 |
| `ReportStats` | 注释语义：节点已认证时向注册服务器报告传输统计（nodestate.go:56） | **未核实：无任何调用点**（对照 doc/modules/auth/API-DESIGN.md:627 设计的 `POST /stats/report` 批量窗口也未落地） |

另有只读 API `GetPeerID`（nodestate.go:50-54）同样**无任何调用方**（未核实）。

**读取（当前唯一真实发生的存取，全部是每请求同步读）：**

| 触发时机（HTTP 请求） | 读取点 |
|---|---|
| `POST /anon/collections`（创建匿合集）→ `CreateAnonCollection`，取 operator 作 `owner` | anon.go:51 |
| `PUT /anon/collections/:hash/visibility`（切档）→ `SetAnonCollectionVisibility`，作 requester 做越权判定 | anon.go:97 |
| `GET /anon/collections/:hash`（读元数据）→ `GetAnonCollection` | anon.go:141 |
| `GET /anon/collections/:hash/:filepath`（下载条目）→ `DownloadAnonFile` | anon.go:163 |
| `POST /anon/collections/fork`（fork）→ `ForkAnonCollection` | anon.go:239 |
| `POST /anon/collections/commit`（提交新版本）→ `CommitAnonCollection` | anon.go:317 |
| `GET /p2p/auth/status` → `AuthStatus`，把 operator 放进响应 | p2p.go:895 |

无定时任务、无事件回调、无优雅关闭钩子触及本包。

---

## 4. 储存什么

**本包自持的 4 个字段**（全部为进程内存、无默认值逻辑以外的初始化）：

| 字段 | 类型/初值 | 写入函数 | 读取/消费方 | 关键约束 |
|---|---|---|---|---|
| `operator` | `string`，初值 `""` | `Configure`(29)/`SetOperator`(38) | `GetOperator`(43) → anon.go 六处、p2p.go:895 | 空串 = 匿名节点（nodestate.go:35,42 注释）；**代码未做任何长度/格式校验** |
| `regURL` | `string`，初值 `""` | `Configure`(30) | `ReportStats`(59,78) | 空串时 `ReportStats` 直接返回（64-66）；用作 HTTP POST 前缀，直接拼接 `/auth/node/stats`（78） |
| `authToken` | `string`，初值 `""` | `Configure`(31) | `ReportStats`(60,80) | 空串时 `ReportStats` 直接返回（64-66）；作 `Authorization: Bearer <token>` 头（80） |
| `peerID` | `string`，初值 `""` | `Configure`(32) | `GetPeerID`(50)、`ReportStats`(61,72) | 空串时 `ReportStats` 直接返回（64-66）；上报 JSON 的 `peer_id` 字段（72） |

**委托下游持久化的内容（不属于本包字段，但值源于本包，供追溯）**：`AnonCollection.Owner`——写入匿合集 JSON 尾部（`back/internal/service/anon_service.go:131`），随内容寻址摘要参与 hash 计算（138）；约束：`private` 仅 Owner 可看、`restricted` 放行 Owner+AccessList（`back/internal/model/anon.go:139-163`）；越权一律 404（anon_service.go:212-216）；hash 为 64 位小写十六进制 sha256（anon_service.go:36-40,138），文件落 `storage/<hash[:2]>/<hash>`，0644（anon_service.go:140-149）。

---

## 5. 边界与坑

1. **当前是「只读空包」**：全仓库没有任何 `Configure`/`SetOperator`/`ReportStats`/`GetPeerID` 调用方 → `operator` 恒为 `""`。后果：匿合集建出来 Owner 必为空，`private` 档位无主不可读（`CanView` 对空 requester 返回 false，model/anon.go:158-159），前端据此禁用「仅自己」档（doc/REFACTOR.md:567）。这与「节点登录 regserver 后 operator 才有值」的设计意图（p2p.go:888-890）之间存在「写入方未落地」的缺口。
2. **并发安全**：所有公有函数读写均持 `mu`（nodestate.go:27-32,37-39,44-46,51-53,58-62）。`ReportStats` 采用先加锁拷出快照、解锁后再发 HTTP 的写法，避免持锁做网络调用（58-62）；这是刻意为之，调用方不可假设读写原子性超出单函数范围。
3. **静默失败**：`ReportStats` 的错误全部吞掉——HTTP 错误 `err != nil` 直接 `return`（82-85），响应体只关不读、不校验状态码（85）；upload/download 都为 0 时不发请求（67-69）。无日志、无重试、无队列。
4. **代理旁路**：`localClient` 对 `localhost`/`127.*`/`::1` 直连不走环境代理，其余走 `http.ProxyFromEnvironment`（88-103）；Dial 与整体超时均为 10s（100,102）。注意：这里的「本地判定」是给 HTTP 客户端用的，与「本地 WS 管理面」无关。
5. **包注释与现状/文档存在多处不一致**：
   - 注释称 controller 和 service 都访问本包（nodestate.go:2），实际仅 controller 导入（§被依赖）。
   - `Configure` 注释引用的 `NodeRegistrar.Start()`（nodestate.go:25）在仓库中不存在（doc/archive/TRANSPORT-REVIEW2-2026-08-16.md:163 规划的 `NodeRegistrar`/`RelayRegistry` 加 `Stop()` 也未落地）。
   - `doc/FILE-REFERENCE.md:167` 记载本包管「在线/离线/忙碌」状态，代码中没有这些字段。
   - 本包**无任何测试**（目录内仅 nodestate.go；doc/archive/report/ARCHITECTURE-REVIEW.md:84 亦记载）。
6. **`username` ≠ `operator`**：`AuthOptional`/`AuthRequired` 中间件把「本次请求调用者的账号」放进 gin context（`c.Set("username", ...)`，auth_middleware.go:43-50,78-79），来源是逐请求远程校验 reg server `/auth/whoami`（auth_middleware.go:147-168，30s 缓存）；而 `operator` 是「节点登录 regserver 后登记的账号」，二者来源完全不同（p2p.go:888-890 注释明说）。中间件**从不写** nodestate。
7. **`var _ = fmt.Sprintf`（nodestate.go:105-106）**：为保有 `fmt` 导入而添加的占位——`fmt` 在本包并无实际使用，属历史遗留写法，别误以为有格式化逻辑。
8. **reg server 统计上报链路无对应连接文档**：13 份 connections/ 文档均不覆盖节点 → reg server 的 `/auth/node/stats` 上报（nodestate.go:78），属未成文的对外协议；认证方向的既定连接是 router 中间件 → reg server `/auth/whoami`（见 §6 连接 02）。

---

## 6. 对外连接

nodestate 没有专属连接文档，它的读写都经由 controller/service 与其他模块交接，相关连接如下（方向均以 nodestate 为参考点）：

- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md)：出向（读）。前端 `getAuthStatus()` 经本地 WS admin 帧打 `GET /p2p/auth/status`（`front/src/api.js:2-8,574`；管理面只走本地 WS），`AuthStatus` 把 `nodestate.GetOperator()` 放进响应（p2p.go:891-896），前端据此决定「仅自己」档是否可用（p2p.go:890）。
- [../connections/02-router-controller.md](../connections/02-router-controller.md)：周边。router 的 `AuthOptional`/`AuthRequired` 中间件做的是**请求级** token 校验并把 username 放 gin context（auth_middleware.go:28-82，`RegistrationServer` 为空时放行，auth_middleware.go:23-26）——它读 reg server 但**不经 nodestate**；nodestate 只被 controller 的 `AuthStatus` 端点读取（p2p.go:867-897）。
- [../connections/03-controller-service.md](../connections/03-controller-service.md)：出向（读→委托）。controller 把 `nodestate.GetOperator()` 作为 `owner`/`requester` 传入 `AnonService`（anon.go:51,97,141,163,239,317），service 侧做可见性闸门与越权判定（anon_service.go:207-218,224-251,260-330）。
- [../connections/04-service-repository.md](../connections/04-service-repository.md)：间接出向（值最终落库）。`Owner` 随匿合集 JSON 写入内容寻址存储并登记 SQLite `file_meta`（anon_service.go:131,140-160）——持久化发生在该连接的下游链路，nodestate 自身不落盘。

关联模块文档（同目录）：[05-controller.md](05-controller.md)（读取方：匿合集端点与 `AuthStatus`，05-controller.md:161 已记载 username/operator 区分）、[06-service.md](06-service.md)（`AnonService` 消费并落盘该值）、[01-config.md](01-config.md)（`RegistrationServer`/`PEERDRIVE_REG_SERVER` 是 reg server 地址的唯一来源，config.go:34,195；main.go:186-188 仅注入 router 中间件，不注入 nodestate）。