# 模块 12：media-node ECH 媒体链

- **代码位置**：`back/cmd/media-node`（进程入口与业务逻辑，`back/cmd/media-node/main.go`）+ `back/ech`（ECH 域前置 HTTP 客户端库，`back/ech/ech.go`）；验证工具 `back/cmd/echclient`（`back/cmd/echclient/main.go`）。设计文档 README 中模块 12 的代码位置即 `back/cmd/media-node/` + `back/ech/`（`doc/design/README.md:27`）。
- **功能一句话**：一个**独立二进制**媒体节点——注册到 PeerJS 信令服务器，接收浏览器 WebRTC DataChannel 连接，唯一允许 `https://video-cf.twimg.com/` 前缀的真实 URL，经内置 ech 包走 ECH 域前置（cloudflare-ech.com 外壳）直连 twitter 媒体 CDN，把媒体流按 64KB 分块实时回传；不监听任何额外端口、不落盘、不依赖外部代理进程（`back/cmd/media-node/main.go:1-28`）。
- **依赖**：
  - `back/ech`（同模块，`back/ech/ech.go`）：ECH HTTP 客户端——DoH 拉取 ECH 配置（`ech.go:131-195`）+ 内存 TTL 缓存（`ech.go:37-67`）+ TLS1.3 ECH 域名前置 transport（`ech.go:306-341`）+ 每 5 分钟刷新（`ech.go:394-417`）。
  - peerjs 库：`github.com/Hana-ame/go-peerjs`，经 `back/go.mod` 的 `replace github.com/Hana-ame/go-peerjs => ./peerjs` 指向仓库内 `back/peerjs/`（模块 10）——提供信令客户端（HEARTBEAT 保活，`back/peerjs/peer.go:482-500`）与 DataChannel 传输原语（文本帧/二进制帧/写缓冲流控，`back/peerjs/connection.go`）。
  - 无其它：不 import `internal/config`/`repository`/`storage`/`router`/`controller`/`service` 任何主进程模块（`back/cmd/media-node/main.go:31-48` 的 import 列表），不写数据库、不读配置文件。
- **被依赖**：
  - `back/cmd/echclient/main.go`：临时验证客户端，以 `echclient-<随机 5 位>` 身份接入同一信令并连接 media-node 的 peer id，发一个 `url` 请求验证「信令 → WebRTC → ECH → twimg」全链路（`back/cmd/echclient/main.go:1-5,40-62,81-86`）。
  - 浏览器端 peerdrive-media：按帧协议连接固定 peer id `media-node`（`back/cmd/media-node/main.go:199`；帧协议注释 `main.go:20-28`）。
  - 其它 Go 代码无引用（media-node 是独立 main 包，非库）。

## 1. 逻辑

**职责**：先行验证版的独立媒体节点（`back/cmd/media-node/main.go:2-6` 注释「peerdrive 其余部分尚未实现，本模块单独先行」）。整条链路内置在本二进制内：信令注册 → WebRTC 接入 → ECH 域前置抓取 → 分块回传。

**ECH 域前置机制**（`back/cmd/media-node/main.go:8-14`、`back/ech/ech.go:1-8`）：浏览器把真实目标 URL 原样发来；本端 TCP 连 `cloudflare-ech.com` 外壳（该域名不被墙），TLS 握手时用 ECH 加密的 ClientHello（`EncryptedClientHelloConfigList`，`ech.go:325`）携带真实目标域名 `video-cf.twimg.com`，Cloudflare 边缘据此路由到 twitter CDN；GFW 只见外壳域名的明文 SNI。注释明确「ECH 域前置只对 Cloudflare 托管的域名有效」（`main.go:14`）。

**核心类型**：

- `Msg` 协议帧（`back/cmd/media-node/main.go:56-65`）：`Type/URL/ReqID/Mime/Size/Status/Msg` 七个 JSON 字段，`reqId` 标识一次请求。
- `ech.Client`（`back/ech/ech.go:289-293`）：包一个 `*http.Client`（`Timeout: 0`，大文件不限时，`ech.go:347`），`Do` 把 `req.Host` 设为真实目标域名后发出（`ech.go:354-359`）。
- `ech.Config`（`ech.go:121-129`）：`DoHURL`/`ProxyURL`/`ShellDomain` 三个可选项。
- `echEntry`（`ech.go:32-35`）：ECH 配置缓存条目 `{config []byte, expiry time.Time}`。

**主要流程**：

1. **启动**（`back/cmd/media-node/main.go:158-199`）：flag 解析（160-175）→ `ech.InitDefault` 初始化内置 ECH 客户端（失败即 `log.Fatalf`，177-180）→ `peerjs.NewPeer` 并用 15s 超时 `Dial` 信令（184-198）。
2. **连接接入**（`main.go:202-256`）：`peer.OnConnection` 回调里为每条 DataChannel 初始化活跃态并启动 keepalive goroutine（202-232），`OnMessage` 分派帧（234-250），`OnClose` 清理（252-255）。
3. **请求处理**（`serveRequest`，`main.go:104-156`）：URL 非空校验（108-111）→ **单前缀允许列表**校验（112-116）→ `fetchTwimg` 经 ECH 抓取（119-124）→ 上游 `>=400` 返回 err（126-129）→ 发 `meta`（含 mime/size，131-133）→ 按 `chunkSize`（默认 64KB）循环分块 `conn.Send`（136-153）→ `done`（154）。
4. **ECH 客户端内部**（`back/ech/ech.go`）：`New` 首启 15s 超时拉取 ECH 配置（296-304）→ `newTransport` 构造拨号到外壳域名 443、TLS1.3 + ECH 握手的 transport（306-341）→ `refreshLoop` 每 5 分钟换配置（394-417）。
5. **帧协议**（`main.go:20-28` 注释）：浏览器 → node `{"type":"url","url":...,"reqId":...}`；node → 浏览器依次 `meta` → 二进制块 × N → `done` 或 `err`；keepalive 为 node **主动**每 5s 发 `{"type":"ping"}`，对端回 `{"type":"ping-ack"}`，15s 无任何帧即断开。

**生命周期**：进程级常驻。启动即 DoH 拉 ECH 配置并连信令；此后持续接受/踢出浏览器连接；收到 `SIGINT`/`SIGTERM` 后 `peer.Close()` 退出（258-263），无任何清理之外的收尾动作。每 5 分钟 `refreshLoop` 轮换 ECH 客户端（`ech.go:395-416`），Cloudflare 会轮换 ECH 配置（`ech.go:394` 注释）。

## 2. 如何储存

**不持久化——纯内存态 + 连接态**。本模块没有任何磁盘/数据库写入：`main.go` 无文件写入调用、无 DB import；`ech.go` 无任何文件 IO。数据面是「从 twimg 流式读 → DataChannel 分块发」的直通管道（`main.go:135-154`），天然不留痕。内存态分三层：

1. **ECH 配置缓存**（进程内 goroutine 间共享，`back/ech/ech.go:37-40`）：包级变量 `cache map[string]*echEntry` + `cacheMu sync.Mutex`。key 为外壳域名（进程内实际只有默认 `cloudflare-ech.com`），value 为 `{config []byte, expiry time.Time}`。TTL 经 `setCachedECH` 夹取到 `[60, 86400]` 秒（`ech.go:42-45,57-67`），元素生命周期 = TTL 或进程重启；**重启后清空**，下次启动重新 DoH 拉取（`main.go:177-180` → `ech.go:296-303`）。
2. **全局默认 ECH 客户端**（进程内，`ech.go:363`）：`defaultClient atomic.Pointer[Client]`——单个原子指针持有当前生效的 `*Client`（内含 ECH transport 与连接池）。`InitDefault` 启动时 `Store`（`ech.go:371`），`refreshLoop` 每 5 分钟 `Swap` 新的并对旧 transport `CloseIdleConnections`（`ech.go:410-414`）。该指针是「始终只有一个生效配置」的换位载体。
3. **每条 DataChannel 的连接活跃态**（连接级内存，随连接生灭）：`lastActive atomic.Int64`（毫秒时间戳，`main.go:209-210`）、`kaStop chan struct{}`（`main.go:211`）、每连接一个的 keepalive goroutine（`main.go:212-232`）。`OnClose` 时 `close(kaStop)`（`main.go:253`）终止 goroutine。对端断开/本端踢出/进程退出即随内存回收。

**请求级瞬时状态**（不算「储存」）：`serveRequest` 内局部 `buf [chunkSize]byte`、`sent int64`（`main.go:136-137`）、每请求一个 goroutine（`go serveRequest`，`main.go:246`）。

**委托关系**：无。本模块末端不委托任何下级存储模块（不像主进程 transport → storage/repository 那样落盘）；唯一「下游」是 ECH/信令/WebRTC 的网络 I/O 与 peerjs 库连接对象（其内部状态见 `back/peerjs/connection.go:24-57`）。

## 3. 何时储存

本模块没有「落盘时机」；以下均为**内存态/连接态的写入与刷新时机**，逐条给出触发点：

1. **进程启动时（首次拉取 ECH 配置并写缓存）**：`main()` 调 `ech.InitDefault`（`main.go:177-180`）→ `New`（`ech.go:296-303`）→ `fetchECHConfig` 首次 DoH 拉取后 `setCachedECH` 写缓存（`ech.go:141-143` 缓存未命中 → `ech.go:190`）。
2. **缓存过期后的首次请求（读取即刷新）**：`fetchECHConfig` 先 `getCachedECH`（`ech.go:141-143`），命中且未过期直接返回；过期/缺失返回 nil 后重新 DoH 并再 `setCachedECH`（`ech.go:51-52,190`）。
3. **定时任务（每 5 分钟）**：`InitDefault` 启动的 `refreshLoop`（`ech.go:372,394-417`）周期拉取新 ECH 配置并 `Swap` 全局 `defaultClient`（`ech.go:410-413`）——为应对 Cloudflare 配置轮换（`ech.go:394` 注释）。
4. **每条浏览器连接建立时**：`OnConnection` 回调（`main.go:202`）初始化 `lastActive = now`（`main.go:210`）、创建 `kaStop`（`main.go:211`）、启动 keepalive goroutine（`main.go:212`）。
5. **收到任何帧时**：`OnMessage` 第一步 `lastActive.Store(now)`（`main.go:234-236`）——文本/二进制任何帧都刷新活跃（注释「对端还在，连接未死」，`main.go:235`）。
6. **keepalive tick（每 5s）**：goroutine 内 `time.NewTicker(5s)`（`main.go:213`）检查「now − lastActive > 15000ms」则 `conn.Close()` 并退出（`main.go:221-225`），否则主动 `SendJSON {"type":"ping"}`（`main.go:227-230`）。
7. **连接关闭时**：`OnClose` 回调 `close(kaStop)`（`main.go:252-255`），连接态清理完毕。
8. **优雅关闭（SIGINT/SIGTERM）**：`signal.Notify` 后阻塞等待，收到信号 `peer.Close()`（`main.go:258-263`）——只断开信令/连接，**不写任何持久化数据**。
9. **进程重启**：一切内存态清零（ECH 缓存、defaultClient、所有连接态），重新走启动流程——文档可见该模块状态零跨重启残留。

## 4. 储存什么

**A. ECH 配置缓存条目**（`back/ech/ech.go:32-40,57-67`）：

| 字段 | 含义 | 默认值 / 约束 |
|---|---|---|
| cache key | 外壳域名（`ShellDomain`） | 默认 `cloudflare-ech.com`（`ech.go:138-140,309-311`） |
| `config []byte` | ECHConfigList 二进制，来自 DoH 的 SVCB（type=65）记录 | 解析自 `ech=` 参数 base64 或 `\# ...` 十六进制 wire（`ech.go:176-184`，`parseSVCBWire` 取 key=5，`ech.go:79-109`） |
| `expiry time.Time` | 过期时刻 | `now + TTL`；TTL 夹取 `[minTTL=60, maxTTL=24*3600]` 秒（`ech.go:42-45,57-67`）；DoH 返回 TTL<=0 按 300 处理（`ech.go:171-174`） |

**B. 全局 ECH 客户端**（`ech.go:363,371,410-413`）：当前生效的 `*Client` 一个（原子指针，同一时刻仅一份）。

**C. 每条连接活跃态**（`main.go:209-228`）：

| 对象 | 类型 | 生命周期 | 约束 |
|---|---|---|---|
| `lastActive` | `atomic.Int64`（毫秒时间戳） | 连接建立 → 关闭 | 任何帧刷新；超时判据 15000ms（`main.go:221`） |
| `kaStop` | `chan struct{}` | 同上 | 仅 `OnClose` close 一次（`main.go:253`） |
| keepalive goroutine | 每连接一个 | 同上 | 5s tick；超时/发送失败即 `conn.Close()` 退出（`main.go:221-231`） |

**D. 协议帧字段**（`Msg`，`main.go:56-65`，每次请求瞬时）：`type`（`url`/`meta`/`done`/`err`/`ping`/`ping-ack`，`main.go:26-27,244-249`）、`url`（允许列表内）、`reqId`（回显原值）、`mime`（`guessMime` 推断或上游 Content-Type，`main.go:67-90,131`）、`size`（上游 `ContentLength`，无该头为 -1，`main.go:132`）、`status`、`msg`。

**E. 硬编码默认/常量**（`main.go` / `ech.go`）：

| 项 | 值 | 位置 |
|---|---|---|
| `twimgHost` | `video-cf.twimg.com`（写死，唯一后端） | `main.go:50-51` |
| `twimgURLPrefix` | `https://video-cf.twimg.com/`（允许列表唯一前缀） | `main.go:53-54,112-116` |
| chunkSize | 64KB（可 `-chunk-size` 覆盖） | `main.go:165,173` |
| 信令参数 | host `peersignal.moonchan.xyz`、port `443`、secure、key `pd-signal-b9447b406828e500` | `main.go:161-164`（数值与 peerjs 库 `DefaultOptions` 一致，`back/peerjs/peer.go:46-57`；echclient 直接调 `DefaultOptions` 后覆盖，`echclient/main.go:34-38`） |
| peer id | `media-node`（可 `-peer-id` 覆盖） | `main.go:160,168,199` |
| 信令心跳 `PingInterval` | 5s（信号层 HEARTBEAT） | `main.go:190`；`back/peerjs/message.go:91`，`peer.go:482-500` |
| 应用层 keepalive | 每 5s 发 ping / 15s 无帧踢 | `main.go:207-208,213,221` |
| `DefaultDoHURL` | `https://moonchan.xyz/doh`（自托管 DoH 端点） | `ech.go:117-119`；`Config.DoHURL` 为空时用默认（media-node 只设 `ProxyURL`，`main.go:178`，故走默认） |
| 默认 `ShellDomain` | `cloudflare-ech.com` | `ech.go:138-140,309-311` |
| 抓取请求头 | `Referer: https://x.com` + Chrome 120 UA（防盗链） | `main.go:93,98-100` |

## 5. 边界与坑

- **单前缀允许列表防 SSRF**：`serveRequest` 校验收到的 url 必须以 `twimgURLPrefix` 开头，否则回 `err`（`main.go:112-116`）；目标域名写死在 `twimgHost` 常量（`main.go:50-51`）。这是本模块唯一外部可指定的网络目标。
- **防盗链**：twitter CDN 要求 `Referer: https://x.com` 且校验 User-Agent（`main.go:93` 注释；头在 `main.go:99-100` 写死）。
- **无 STUN 环境下断线无 close 事件**：keepalive 注释明确「WebRTC 断线无 close 事件，必须靠超时感知」（`main.go:207-208`）——ping 由本端**主动**发、对端回 `ping-ack` 才刷新 `lastActive`（`main.go:226-230`），15s 无任何帧才踢（`main.go:221-225`）。
- **踢出与清理的配合**：超时路径 `conn.Close()`（`main.go:223`）触发 peerjs 库 `OnClose` → 本端 `close(kaStop)`（`main.go:253`；库内 `Close` 幂等且回调同步触发，`back/peerjs/connection.go:178-198`）。
- **文本帧 vs 二进制帧**：JSON 控制帧必须用文本帧（`SendJSON`/`SendText`，SCTP PPID 51），媒体块用 `Send` 二进制帧（PPID 53）；「用 `Send` 发 JSON 头」会被对端误判为数据块（`back/peerjs/connection.go:90-114` 注释）。本端忽略收到的二进制帧（`main.go:237-239`）。
- **流控与并发**：`main.go:135` 注释称「`Connection.SendFrame` 已内置低水位流控」，但 `serveRequest` 实际逐块直接 `conn.Send`（`main.go:141`）；库内 `SendFrame` 的流控阈值为 512KB、单帧等待上限 30s（`back/peerjs/connection.go:17,152-166`）。多个 `url` 请求各自 goroutine 并发（`main.go:246`），DataChannel `Ordered: true` 保序（`connection.go:273-274`），块间可能交错，浏览器端按 `reqId` 与「meta 后跟二进制块」状态机路由（`main.go:20-28` 注释；浏览器侧实现细节未核实）。
- **启动快速失败**：ECH 初始化失败（DoH 不通/解析失败）直接 `log.Fatalf`（`main.go:178-180`）；信令 `Dial` 失败同样 `Fatalf`（`main.go:196-198`）。
- **刷新失败不换档**：`refreshLoop` 拉取失败仅 `continue`，保留旧客户端继续用（`ech.go:407-409`）。
- **超时参数一览**：DoH `http.Client` 8s（`ech.go:152`）、拨号/代理 CONNECT 10s（`ech.go:225,234`）、TLS 握手 10s、连接池 idle 90s（`ech.go:337-339`）、`New` 上下文 15s（`ech.go:297`）、信令 `Dial` 15s（`main.go:194`）、ECH `http.Client` 本体 `Timeout: 0`（大文件不限时，`ech.go:347`）。
- **固定实现对特定基础设施**：ECH 域前置只对 Cloudflare 托管域名有效（`main.go:14` 注释）；DoH 依赖自托管 `moonchan.xyz/doh`（`ech.go:117-119`）。
- **密钥与凭证硬编码**：信令 API key `pd-signal-b9447b406828e500` 直接写死在二进制与 echclient（`main.go:164`、`echclient/main.go:38`）；本机访问需代理时用 `-proxy` 或 `HTTPS_PROXY` 环境变量（`main.go:166,174`，代理语义见 `ech.go:214-220,222-285`）。
- **无任何持久化兜底**：进程重启后媒体链状态（ECH 缓存、连接、在途请求）全体清零，正在传输的请求直接中断——模块定位即「先行验证版」，无断点续传机制（`main.go:2-6` 注释）。

## 6. 对外连接

- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md)：media-node 与 ech **同属模块 12**（`doc/design/README.md:49` 注），本连接描述其内部接线——启动时 `ech.InitDefault`（`main.go:177-180`）、请求期 `fetchTwimg` → `ech.Do`（`main.go:94-102`）、ECH 配置缓存与刷新（`ech.go:37-67,394-417`）；方向 media-node → ech。
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md)：media-node 与主进程 transport 共用同一 peerjs 库（module 10，`back/go.mod` replace → `./peerjs`），本连接载明该库提供的信令客户端与 DataChannel 传输原语语义（`back/peerjs/connection.go`、`peer.go:482-500`）；方向 media-node → peerjs 库。
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md)：media-node 以固定 peer id 注册到项目公共信令 `peersignal.moonchan.xyz`（`main.go:161-164,184-199`），浏览器连接的协商消息（CANDIDATE 发出 `back/peerjs/connection.go:255-265`、ANSWER/CANDIDATE 处理 `connection.go:203-218`、OFFER 发出 `connection.go:331-348`、ANSWER 回应 `connection.go:351-364`）全部经信令转发；方向 media-node → 信令服务器。
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md)：浏览器端 peerdrive-media 及 `echclient` 经同一信令拨号 media-node 的 peer id（`echclient/main.go:34-49` 演示了消费端视角：`Connect("media-node","media")`）；方向 前端/echclient → 信令 → media-node。
- 关联说明：与本模块相关的模块文档为同目录 [10-peerjs.md](10-peerjs.md)（peerjs 库实现）。media-node 与主进程其它模块（config/repository/storage/router/controller/service/transport 等）**无任何依赖与数据流**——独立二进制，唯一共享物是信令服务器与 peerjs 库。