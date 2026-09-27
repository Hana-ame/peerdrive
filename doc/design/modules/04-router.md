# 模块 04：router HTTP 路由与中间件

- **代码位置**：`back/internal/router`
- **功能一句话**：装配 Gin 引擎作为整个后端的唯一 HTTP/WS 入口——注册全部路由（健康检查、文件、集合、P2P、PeerJS、source 管理面）、挂载横切中间件（请求 ID / 安全头 / 访问日志 / 每 IP 限流 / CORS / 认证），并把同一引擎复用作浏览器经 `/ws/peer` admin 帧的管理面后端。
- **依赖**：`config`（`back/internal/config/config.go`——读取 `RateLimitRPS`/`TrustedProxies`/`RegistrationServer`/`DisableCSP`/`DisableSwagger`/`StorageDir`/`AllowedOrigins` 等）、`controller`（全部 handler，`back/internal/router/router.go:225-383` 逐路由引用）、`service`（`SetupRouter` 内构造 `NewFileService`/`NewCollectionService`/`NewShareService`/`NewPinService`/`NewAnonService`/`NewSyncService`，router.go:121-128）、`repository`（`Ping` 探针、`NewSyncRepository`，router.go:124,211）、`source`（`source.Manager`/`NewBTControl`/`NewIPFSControl`，router.go:153,192 与 source_routes.go）、`transport`（`PeerJSService`/`NewWSSession`，peerjs_routes.go）、`downloader`（`NewUniversalDownloader`，router.go:201）、`provider`（`NewIPFSProvider`，router.go:190）、`p2p_bt`（`go-peerdrive-bt`，router.go:141,151）、gin / swaggo / gorilla-websocket。
- **被依赖**：`back/cmd/server/main.go`（main.go:52-236 装配注入器后调用 `router.SetupRouter(cfg)`，把返回的 `*gin.Engine` 作为 `http.Server.Handler`，main.go:250-254）；`transport` 的 admin 管理面（`back/internal/transport/admin.go:13-19` 说明 admin 帧 → 内部 `*http.Request` → 注入的 `AdminHandler` → 复用本 engine 全部 controller）；前端（经 `/ws/peer` 本地 WS 会话发 admin 帧，见 `back/internal/transport/admin.go:26-27`；旧 HTTP 端点为兼容旧前端/curl/集成测试保留，router.go:216-223）；`collection_dispatch_test.go` 等测试直接对 dispatcher/路由进行集成测试。

## 1. 逻辑

模块只有一个入口函数 `SetupRouter(cfg *config.Config) *gin.Engine`（router.go:44），由 main 在全部服务装配完成后调用一次（main.go:236）。职责分四层：

**① 中间件栈装配**（router.go:49-107）。不用 `gin.Default()`（其自带 Logger 与 `AccessLog` 重复输出），显式组装，执行顺序即注册顺序（middleware.go:7-10 注释）：

1. `gin.Recovery()`（最外层，panic 也要能恢复）router.go:50；
2. `RequestID()`：沿用上游 `X-Request-ID` 或生成、截断 >128 的 ID，写入 context 与响应头（middleware.go:37-49）；
3. `SecurityHeaders(cfg.DisableCSP)`：nosniff / X-Frame-Options / Referrer-Policy / COOP / CSP（`/swagger/*` 豁免 CSP，middleware.go:75-87）；
4. `AccessLog()`：每请求输出一行 JSON 访问日志（middleware.go:108-148）；
5. `RateLimit(cfg.RateLimitRPS, 0)`：每 IP 令牌桶限流（middleware.go:203-240）；
6. 两个内联闭包：注入 `storageDir` 到 Gin context（router.go:76-79）、CORS 白名单处理（router.go:81-107，先于认证）；
7. `AuthOptional()`（仅在配置了 `RegistrationServer` 时挂载，router.go:112-115）。

`TrustedProxies` 在装配阶段单独处理（router.go:57-73）：默认一个都不信（`ClientIP()` 直接用 `RemoteAddr`），`all` 放行 `0.0.0.0/0`，逗号分隔列表逐个 `SetTrustedProxies`。

**② 控制器依赖装配**（router.go:121-213）。`SetupRouter` 内部构造 service 实例并注入 controller（`controller.InitFileController`/`InitHealth`/`InitCollectionController`/`InitShareController`/`InitPinController`/`InitAnonController` 等，router.go:122-148）；按配置初始化 BT DHT / BT client / IPFS provider / universal downloader / sync controller。BT、IPFS 的「控制句柄」通过 `sourceManager.SetBTControl/SetIPFSControl` 注入 source 体系（router.go:152-155,191-193）。

**③ 路由注册**（router.go:225-394）。分组见源文件头注释（router.go:6-18）与本文 §4 路由表。认证中间件 `authRequired := AuthRequired()` 只构造一次（router.go:119），复用于所有 mutating/admin 路由（**F1 修复**：此前 `AuthRequired` 有 0 个调用点，任意文件读写/删除接口全部匿名可达，router.go:116-118 注释）。只读端点（列表/状态/下载）不挂认证。

**④ PeerJS 与 source 管理面**（router.go:394-418）。`registerPeerJSRoutes(r, authRequired)` 注册 `/peerjs/*` 节点发现与 `/ws/peer` 本地 WebSocket 会话（peerjs_routes.go:74-184）；若 `peerjsService != nil`，向 transport 注入 `SetAdminHandler`——把外部请求包一层（`RemoteAddr` 为空时标成 `127.0.0.1:0`，避免限流把所有管理请求算进同一「未知来源」桶，router.go:403-414），交给 `httptest.NewRecorder() + r.ServeHTTP`，从而 admin 帧复用全部 HTTP controller（router.go:396-401 注释：零重复实现，管理面只暴露给本地 WS，`serveAdmin` 按会话 ID 拒绝远端）。最后 `registerSourceRoutes(r, authRequired)` 注册 `/sources*` 管理端点（source_routes.go:24-45）。

**生命周期**：随进程一次装配、常驻服务，无热更新（路由注册只发生在 `SetupRouter` 调用时）。进程退出/重启后全部重建（见 §2）。

## 2. 如何储存

router 层**不持久化任何数据**（不写文件、不写 DB）；它的「储存」全部是进程内内存态，业务数据均委托下级模块：

| 内存态 | 介质/位置 | 组成 | 生命周期与重启影响 |
| --- | --- | --- | --- |
| 路由表 | gin 引擎内部 radix tree（`gin.Engine`，非持久化） | `SetupRouter` 注册的约 105 个端点（swagger 注释口径，router.go:385；实际数量运行时 `r.Routes()` 统计并记日志，router.go:421-422） | 随进程；重启后由 `SetupRouter` 重建 |
| 限流桶表 | `limiter` 结构（闭包捕获，非持久化） | `buckets map[string]*bucket{tokens,last}`，键为 `ClientIP`（middleware.go:152-162,213） | 进程内；重启后桶全清（限流计数归零）；惰性创建、超 4096 项才清理 |
| Token 校验缓存 | 包级变量 `tokenCache`（`sync.RWMutex + map[string]tokenCacheEntry`，auth_middleware.go:95-98），**只存进程内存：不落盘、不进日志**（auth_middleware.go:94 注释） | key=Bearer token 原文，值=`{username, role, exp}`（auth_middleware.go:100-104） | 进程内；TTL 30s；重启后清空 → 首批请求重新走注册服务器 whoami |
| 包级注入器 | Go 包级全局变量 | `regServerURL`（auth_middleware.go:16）、`peerjsService`/`peerjsCfg`/`nodeDirectory`（peerjs_routes.go:19-25）、`sourceManager`（source_routes.go:17） | 进程内；由 main 在 `SetupRouter` 前注入（main.go:186-234），重启后重新注入 |
| Admin 转发闭包 | transport 侧 `PeerJSService.adminHandler`（peerjs_service.go:105） | `SetAdminHandler` 注入的 `ServeHTTP` 闭包（router.go:402-415） | 进程内；随 `SetupRouter` 装配 |
| `request_id` / 认证结果 | 每请求 Gin context | `request_id`、`storageDir`、`authenticated`、`username`、`role`（middleware.go:45、router.go:77、auth_middleware.go:33-50） | 单请求生命周期，请求结束即丢弃 |

**委托关系**：router 只把 `storageDir` 注入 context（router.go:76-79，供 controller/anon.go 等使用），真正落库/落盘全部由下层完成——集合/文件等业务数据经 controller → service → repository 落 SQLite（`peerdrive.db`，main.go:76）与存储目录；BT/IPFS 控制面经 `source.Manager` 的 control 接口（下载落 `cfg.DownloadDir`，router.go:151）。router 自身没有任何写入 SQLite/磁盘的代码路径。

## 3. 何时储存

- **进程启动（唯一的大型写时机）**：main 装配注入器后调用 `SetupRouter(cfg)`（main.go:236）→ 一次性完成中间件栈注册、controller 依赖构造、全部路由注册、admin handler 注入（router.go:44-423）。此后路由表不再变化。
- **首个「带有效 token 的请求」**：`validateToken` 缓存未命中 → `queryWhoami`（远程 GET `{regServerURL}/auth/whoami`）成功 → `cachePut` 写入 `tokenCache`（auth_middleware.go:136-144）。之后 30s 内同一 token 直接命中缓存，不再发远程请求（auth_middleware.go:84-92 注释）。
- **缓存清理**：`cachePut` 时若 map 项数 > 1024 顺带删除已过期项（auth_middleware.go:118-130）；`cacheGet` 读到时过期按未命中处理（auth_middleware.go:107-115）。失败（whoami 非 200/超时）**不缓存**，避免网络抖动影响持续 30s（auth_middleware.go:140-143）。
- **每个非本机来源的请求**：`RateLimit.allow` 对该 IP 惰性创建令牌桶并扣减令牌（middleware.go:169-181）；被限流请求在 `AccessLog` 之后（顺序保证被限流也留痕，middleware.go:9-10 注释）记 429 日志并返回 JSON（middleware.go:230-236）。
- **限流桶清理**：每请求 `sweep`，仅当 `len(buckets) >= 4096` 时遍历删除超过 10 分钟不活跃的桶（middleware.go:185-196,238）。
- **每请求（日志）**：`AccessLog` 在 `c.Next()` 返回后写一行 JSON 访问日志（middleware.go:108-148）；`RequestID` 在每个请求开始时生成/沿用 ID（middleware.go:37-49）。
- **BT 下载完成回调（装配时机，非 router 存储）**：`SetupRouter` 内给 BT client 挂 `SetOnComplete`，完成后经 `fileSvc.RegisterBTFile` 登记文件（router.go:161-176）；该闭包生命周期在进程内，实际落库由 FileService/repository 完成。
- **优雅关闭**：main 收到 SIGINT/SIGTERM 后 `srv.Shutdown(ctx)`（20s 超时，main.go:264-283）。router 层无关闭钩子、无待落盘数据；PeerJS 连接与 DB 句柄由 main 的 `defer` 兜底（main.go:79-83,111）。

## 4. 储存什么

**路由表条目**（router.go:225-394，全部在 `SetupRouter` 内注册）：

| 分组/路径 | 方法 | handler | 认证 |
| --- | --- | --- | --- |
| `/ping`、`/health`、`/ready` | GET | `controller.Ping/Health/Ready` | 无 |
| `/sha256sum/:sha256(/:filename)` | GET | `DownloadBySHA256Local`（仅本地存储） | 无 |
| `/ipfs/:cid`、`/download/:hash(/:hash/sources)` | GET | `DownloadByCID`、`UniversalDownload`、`UniversalDownloadSources` | 无 |
| `/download/:hash/refresh` | POST | `UniversalDownloadRefresh` | AuthRequired |
| `/p2p/*`：auth/status、webrtc/info、forward/create・connect・close、pull・pull/collection・pull/cancel | GET/POST | `AuthStatus`、`WebRTCInfoHandler`、`CreateForwardSession(...)`、`StartPull(...)` 等 | 写操作挂 AuthRequired（router.go:244-253） |
| `/bt/*`：status、announce、find、bep44/put・get、bep51/sample、torrent、magnet、download*、seed-collection、stats | GET/POST/DELETE | `BTDHTStatus`、`BTAnnounce`、`BEP44Put` …（router.go:257-281） | 写操作挂 AuthRequired |
| `/ipfs/pin/:cid`、`/ipfs/pins`、`/ipfs/gateways` | POST/DELETE/GET | `PinCID`、`UnpinCID`、`ListPins`、`IPFSGatewayStatus` | pin 写操作挂 AuthRequired |
| `/collections`（统一分派） | POST/GET | `dispatchCreateCollection`/`ListAnonCollections` | POST 挂 AuthRequired |
| `/collections/:id`、`/collections/:id/*filepath` | GET | `dispatchGetCollection`/`dispatchGetTree` | 无（读开放） |
| `/collections/fork・merge・upload・register-local・register-url・register-folder` | POST | `ForkAnonCollection`、`MergeFromSource`、`UploadFile` 等 | AuthRequired |
| `/anon/*`：collections・commit・fork・visibility・hash 下载 | POST/GET/PUT | `CreateAnonCollection`、`CommitAnonCollection`、`SetAnonCollectionVisibility` 等 | 写操作挂 AuthRequired |
| `/files/*`：upload・register_*・verify・browse・:hash 删除・copy・diff | GET/POST/DELETE | `UploadFile`、`RegisterLocalFile`、`DeleteFile`、`DiffVersions` 等 | 写操作挂 AuthRequired |
| `/collections/public・search`、`/:id/:collection_name/entries・commit・rollback・visibility・tags` | GET/POST/DELETE | `ListPublicCollections`、`SearchCollections`、`AddEntry`、`CommitCollection` 等 | 后五组写操作挂 AuthRequired |
| `/local/save`、`/local/status/:hash` | POST/GET | `syncCtrl.SaveLocal/GetStatus` | save 挂 AuthRequired |
| `/actions/merge・fork` | POST | `MergeFromSource`、`ForkCollection` | AuthRequired |
| `/:username/:collection_name/*filepath` | GET | `DownloadCollectionFile` | 无 |
| `/shares`、`/s/:token` | POST/GET | `CreateShare`、`ListShares`、`AccessShare` | 创建/列表挂 AuthRequired，token 读取公开 |
| `/swagger/*any` | GET | `ginSwagger.WrapHandler(swaggerFiles.Handler)`（`DisableSwagger` 时关闭，router.go:385-391） | 无 |
| `/peerjs/node`、`/peerjs/nodes*`、`/peerjs/share*`、`/peerjs/fetch`、`/ws/peer` | GET/POST/DELETE/PUT | 见 peerjs_routes.go（§5 表格） | 写/拉取/共享挂 auth |
| `/sources*`：status、priority、local/add・write、bt/*、ipfs/* | GET/POST/DELETE | 见 source_routes.go | 控制面挂 AuthRequired |

**Token 缓存条目**（auth_middleware.go:100-104）：`username string`、`role string`、`exp time.Time`；key = token 原文；TTL 固定 `tokenCacheTTL = 30s`（auth_middleware.go:92）；map 超过 1024 项触发过期清理（auth_middleware.go:121-128）。

**限流桶条目**（middleware.go:152-162）：`tokens float64`（初始 = burst，上限 = burst，按 `now.Sub(last).Seconds() * rps` 匀速补充，middleware.go:175）、`last time.Time`；key = `ClientIP`；burst 未显式给定时默认 `rps*2`（下限 1，middleware.go:207-211）；不活跃 >10 分钟且总桶数 ≥4096 时被删除（middleware.go:192）。

**每请求访问日志字段**（`accessLogEntry`，middleware.go:93-105）：`ts`（RFC3339Nano UTC）、`level`（5xx=error/4xx=warn/其余 info，middleware.go:117-122）、`msg="http request"`、`request_id`、`method`、`path`、`status`、`latency_ms`、`client_ip`、`user_agent`（>200 截断，middleware.go:114-116）、`resp_bytes`。**刻意不记录**：Authorization 头、查询串里的 token、请求体（middleware.go:91-92 注释）。

**Gin context 键**（controller 经 `c.Get` 读取）：`storageDir`（router.go:77）、`request_id`（middleware.go:45，读取辅助 `RequestIDFrom`，middleware.go:61-68）、`authenticated`（bool）、`username`、`role`（auth_middleware.go:33-50,78-79）。

**`/peerjs/fetch` 请求体约束**：`{peer, hash, offset?, size?}`，`hash` 必须过 `hashutil.IsValidSHA256`，`size > 64MB` 直接 413（peerjs_routes.go:119-137,148-150）。

## 5. 边界与坑

- **gin 不允许同层 `:param` 与 `*wildcard` 并存**：`/collections/:id` 与 `/collections/:id/*filepath` 必须统一并入 wildcard 路由内部分派（`dispatchGetTree`，collection_dispatch.go:56-58 注释）。分派语义：`:id` 为 64 位 hex → 匿名集合（hash 寻址）；否则按 username 寻址；`filepath` 空/单段/「两段且末段为 log」分别分派到 `ListCollections`/`GetCollection`/`GetVersionLog`，其余 404（collection_dispatch.go:59-81）。
- **legacy redirect 曾导致启动 panic**：`/anon/*`、`/actions/*` 的 redirect 与真实路由重复注册，gin 启动即 panic（「handlers are already registered」）；修复为删掉全部 redirect、冲突路由合并进分派器（router.go:294-298 注释），前端已直接用新路径。
- **`withParams` 不能用 `c.Copy()`**：gin v1.8+ 的 `Context.Copy` 不复制 ResponseWriter，下游 `c.JSON` 必然 nil-pointer panic（2026-08-19 test.sh 6b GET `/collections/tester` 暴露 500，被 Recovery 吞掉导致误判）。修复为原 context `append` Params，单请求串行调用安全（collection_dispatch.go:87-96 + collection_dispatch_test.go:3-7 注释；回归测试见 test 文件 20-56 行）。
- **限流对「本机/未知来源」放行**：`127.0.0.1`/`::1`/空 IP 不限流——本机回环是管理通道（`/ws/peer` 内部转发把 `RemoteAddr` 标成 `127.0.0.1:0`，router.go:408-410），未知 IP 限了只会误伤（middleware.go:222-229 注释）。OPTIONS 预检不计数（middleware.go:215-220，被 429 的预检不带 CORS 头会让前端看到莫名跨域错误）。
- **TrustedProxies 默认一个都不信**：不配时 `ClientIP()` 用 `RemoteAddr`；反代后面必须配 `PEERDRIVE_TRUSTED_PROXIES`，否则所有人被算成同一个来源一起限流（router.go:55-56 注释 + config.go:119-125）。
- **token 缓存是「即时吊销」的取舍**：被吊销的 token 最长 30s 内仍有效（auth_middleware.go:84-92 注释）；所有中间件一律**不缓存失败结果**、不记录凭据。
- **认证后端缺失 = 单机模式**：`authDisabled()`（`regServerURL == ""`）时 `AuthRequired` 内部放行，全站不 401；公网部署配置 `RegistrationServer` 后自动收紧（auth_middleware.go:23-26,57-62）。F1 修复前这些接口匿名可达（router.go:116-118 注释）。
- **CORS 只回显白名单 Origin**：非白名单 Origin 不设 `Access-Control-Allow-Origin`（浏览器阻止读取响应）；无 Origin（同源/curl）不设 CORS 头；仅白名单命中才带 `Vary: Origin` + `Allow-Credentials: true`（router.go:82-101 注释，L8 修复：原实现非白名单也回 `*` + credentials，白名单形同虚设）。
- **`/ws/peer` 是本地会话安全边界**：无 Origin 的连接只放行 loopback（脚本/curl 当管理员会被拒，peerjs_routes.go:162-170 注释）；带 Origin 的走与 HTTP CORS 同一白名单（`peerjsCfg.IsOriginAllowed`，peerjs_routes.go:171-174）。管理面固定只走本地 WS（transport/admin.go:5-11 注释，`serveAdmin` 按会话 ID 拒绝远端）。
- **中间件顺序是安全语义**：`RequestID` 必须最早（后续日志都带 ID）；`RateLimit` 在 `AccessLog` 之后（被限流的请求也要留痕，middleware.go:7-10 注释）。
- **`/peerjs/fetch` 内存风险（H4）**：整个响应 buffer 驻留内存，收紧为挂认证 + 单次 ≤64MB + service 侧 8GB cap 兜底，超大文件应走 `/ws/peer` 分片（peerjs_routes.go:115-118 注释）。
- **Swagger 默认开**：把全部端点（约 105 个）与参数结构公开，生产建议 `PEERDRIVE_SWAGGER=off`（router.go:385-391 注释）；CSP 对 `/swagger/*` 豁免（其官方实现依赖 inline script/eval，middleware.go:71-74 注释）。
- **`http.Client` 必须有超时**：`queryWhoami` 用 5s 超时 + 响应体 64KB 上限（原实现无超时，注册服务器挂起时连接池被耗尽，auth_middleware.go:147-150 注释）。
- **匿名/用户体系的 `:id` 分派只做长度判断**：64 字符长度即视为匿名集合 hash（collection_dispatch.go:41,63）——遵循「长度即形态」约定，非法 hash 由下游 controller 校验（router 层不再单独 `IsValidSHA256`；与 `/peerjs/fetch` 的显式校验 peerjs_routes.go:138 不同）。

## 6. 对外连接

- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md)：前端↔后端的本地 WS 会话由本模块提供升级端点 `/ws/peer`（peerjs_routes.go:158-184），admin 帧经注入的 `AdminHandler` 复用本 engine 的全部 controller（router.go:402-415）。
- [../connections/02-router-controller.md](../connections/02-router-controller.md)：本模块把每个 HTTP 端点注册到对应 controller handler（router.go:225-383）；collection dispatcher（collection_dispatch.go）在 router 层完成匿名/用户体系的参数装配后分派到不同 controller。
- [../connections/03-controller-service.md](../connections/03-controller-service.md)：`SetupRouter` 直接构造 service 实例注入 controller（router.go:121-128,211-213），router 是 service 装配的一个入口点（其余由 main 注入）。
- [../connections/05-router-source.md](../connections/05-router-source.md)：source 管理面端点（source_routes.go）全部转发到 `source.Manager`——状态快照、优先级调整、local/BT/IPFS 控制面；`sourceManager` 由 main 经 `SetSourceManager` 注入（source_routes.go:17-22）。
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md)：本模块读取包级注入的 `peerjsService` 注册节点发现/拉取/WS 端点（peerjs_routes.go:74-184），并以 `SetAdminHandler` 把 gin engine 反向注入 transport 的管理面（router.go:402-415）。
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md)：`/peerjs/node` 暴露的节点 ID 源自 PeerJS 信令层（`peerjsService.ID()`，peerjs_routes.go:88-94），router 只作转发呈现，不参与信令。
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md)：前端/MQTT 房间经本模块的 `/peerjs/node` 发现端点解析本节点 ID（peerjs_routes.go:69-70 注释）。