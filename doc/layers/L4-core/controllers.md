# controller 层（back/internal/controller/）

> 层归属：AOP ④ 业务核心（见 doc/LAYERS.md §1）。
> HTTP 语义业务的「端点组」组织层：每个文件一组相关端点，只做参数绑定/校验/响应装配，
> 业务规则全部委托 service。不感知自己被 HTTP 直接调用还是被 admin verb 内部转发
> （router.go 经 `SetAdminHandler` 把 gin engine 注入 transport，REFACTOR.md §3.10）。
> 路由真实注册处：`back/internal/router/router.go`（LEGACY HTTP 路由区）+ `collection_dispatch.go`
> （/collections 分派器）+ `peerjs_routes.go` + `source_routes.go`。

**一句话职责**：把 HTTP 请求翻译成 service 调用——绑定 body、校验入参、按错误类型映射
HTTP 状态码、组装 JSON/文件响应；不写业务逻辑、不直连 repository（M2 收层后仅剩
download.go 的 `fileSvc` 等注入依赖，见坑 §7）。

## 职责

### 解决什么问题

controller 是业务核心（AOP ④）的最外层：router 只负责路径→handler 的静态映射，
controller 负责「请求语义」——请求长什么样（字段、校验规则）、响应长什么样（状态码、
错误体、header）。业务规则（hash 校验、路径防御、多源路由、事务）在 service/source/
downloader 里，controller 只编排。

### 装配模式（两种并存）

包内存在两种依赖注入风格，写新代码时沿用同文件风格：

1. **包级变量 + Init\* 函数**（占多数）：`controller/anon.go:15` 的 `var anonSvc *service.AnonService`
   由 router.go:117 `InitAnonController(...)` 注入。同模式：`InitFileController`（file.go:33）、
   `InitCollectionController`（collection.go:42）、`InitShareController`（share.go:17）、
   `InitTaskController`（task.go:24）、`InitPinController`（p2p.go:44）、
   `InitUniversalDownloader`（download.go:35）、`InitIPFSProvider`（download.go:41）、
   `InitForwardController`（p2p.go:48）、`InitBTController`（p2p.go:56）、`InitBTClient`（p2p.go:66）。
2. **结构体 + New\* 构造函数**：`AuthController`（auth.go:13）、`SyncController`（sync.go:12）
   由 router 直接 `NewAuthController(...)` / `NewSyncController(...)` 创建。

### 双入口（设计意图，非违规）

同一批 handler 同时服务两条入口（router.go:176-184 注释区明示）：

- **直接 HTTP**：curl / 旧前端 / 集成测试
- **admin verb 内部转发**：浏览器经 `/ws/peer` 发 `{"type":"admin",...}` 帧 →
  transport/admin.go 构造 *http.Request → 注入本 gin engine 的 ServeHTTP
  （router.go:357 `SetAdminHandler`）→ 复用全部 controller，零重复实现

业务层只认 *http.Request，不感知来源（LAYERS.md §6）。前端新代码禁止直接 fetch 这些
端点（LAYERS.md §5 禁区表）。

### 认证策略

`authRequired`（router.go:92，auth_middleware.go）挂到全部 mutating/admin 路由：
- 未配置注册服务器（RegistrationServer 为空）时内部放行（本地单机模式）
- 配置后要求 `Authorization: Bearer <authkey>`
- 读接口（GET/download、anon GET、/files GET、/s/:token、/ws/peer）保持公开

## 模块清单（端点组）

### 1. auth.go — 认证（用户体系）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| auth.go | 注册/登录/登出/当前用户 | `AuthController`（`Register`/`Login`/`Logout`/`Me`）、`NewAuthController` |

路由（router.go 注册，注意：**此组未在 router.go 挂路由**——注册服务器是外部服务
（RegistrationServer），authkey 由外部签发；本组 handler 由集成测试/外部装配直接调用，
见 LEGACY.md 第 61 行「无路由注册」标注）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /auth/register | `Register` | body `RegisterRequest`（username/password）→ `AuthResponse` |
| POST /auth/login | `Login` | 校验 bcrypt，失败 401 `ErrInvalidCredentials` |
| POST /auth/logout | `Logout` | 按 Bearer authkey 清会话 |
| GET /auth/me | `Me` | `ValidateKey` 返回用户信息 |

调用关系：全部委托 `service.AuthService`（Register/Login/Logout/ValidateKey）。

### 2. anon.go — 匿名集合（内容寻址）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| anon.go | 匿名集合 CRUD/下载/fork/commit | `CreateAnonCollection`、`ListAnonCollections`、`GetAnonCollection`、`DownloadAnonFile`、`ForkAnonCollection`、`CommitAnonCollection`、`InitAnonController` |

路由（router.go:271-279，/anon 组；写操作挂 authRequired）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /anon/collections | `CreateAnonCollection` | body `{friendly_name, entries:[{path,hash,providers}], tags}` → 201 `{hash}`；path/hash/provider 非法 → 400 |
| GET /anon/collections | `ListAnonCollections` | 本节点已知匿名集合摘要列表 |
| POST /anon/collections/commit | `CommitAnonCollection` | 基于 source_hash 提交新版本（entries 空 hash=删除条目）→ 201 `{hash}` |
| GET /anon/collections/:hash | `GetAnonCollection` | 集合元数据 + 条目 |
| GET /anon/collections/:hash/*filepath | `DownloadAnonFile` | 按 provider 顺序下载：sha256（经 universalDownloader）→ url 302 重定向 |
| POST /anon/collections/fork | `ForkAnonCollection` | 源集合 ± 增删条目 → 新集合 hash |

下载语义（anon.go:125-155）：条目 `GetPrimaryHash()` 优先 → `universalDownloader.Download`，
成功打 `X-Protocol` 头（`?inline=1` 控制 Content-Disposition）；失败降级 url provider 302。

调用关系：`service.AnonService`（CreateCollection/GetCollectionByHash/ListCollections/
CommitCollection）+ `downloader.UniversalDownloader`（包级 `universalDownloader`）。

### 3. collection.go — 用户集合（CRUD + 版本）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| collection.go | 用户集合 CRUD、条目增删、版本提交/日志/回滚、可见性、标签 | `CreateCollection`、`ListCollections`、`SearchCollections`、`GetCollection`、`AddEntry`、`RemoveEntry`、`DownloadCollectionFile`、`CommitCollection`、`GetVersionLog`、`RollbackCollection`、`SetCollectionVisibility`、`ListPublicCollections`、`UpdateCollectionTags`、`InitCollectionController` |

路由（router.go:296-307 管理组 + collection_dispatch.go 分派组 + router.go:327 通配下载）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| GET /collections/public | `ListPublicCollections` | visibility='public'，`?q=` 过滤 |
| GET /collections/search | `SearchCollections` | 模糊搜索 |
| POST /collections | `dispatchCreateCollection` | 分派器：body 含 username → `CreateCollection`（用户体系）；否则 `CreateAnonCollection`（匿名） |
| POST /collections/fork\|merge\|pull | `ForkAnonCollection`/`MergeFromSource`/`PullCollection` | 见 fork.go/merge.go |
| POST /collections/upload、/register-local、/register-url、/register-folder | 复用 file.go handler | 前端统一 /collections 前缀路径 |
| GET /collections/:id | `dispatchGetCollection` | 64hex → `GetAnonCollection`；否则按 username `ListCollections` |
| GET /collections/:id/*filepath | `dispatchGetTree` | 64hex → 匿名文件下载；否则按段数分派 `ListCollections`/`GetCollection`/`GetVersionLog`（gin 不允许 :param 与 *wildcard 并存，统一并入，见 collection_dispatch.go:56-58 坑注释） |
| POST /collections/:id/:collection_name/entries | `AddEntry` | body `{path, hash | providers}`；集合不存在自动创建（GetOrCreate） |
| DELETE /collections/:id/:collection_name/entries/*path | `RemoveEntry` | path 前置 TrimPrefix "/" |
| POST /collections/:id/:collection_name/commit | `CommitCollection` | 5 步：ListEntries → 组 AnonCollection → SaveAnon 生成 CID（current_hash）→ CreateVersion+SnapshotEntries 快照 |
| GET /collections/:id/:collection_name/log | 经 `dispatchGetTree` | 版本历史（最新优先） |
| POST /collections/:id/:collection_name/rollback/:version_id | `RollbackCollection` | RestoreVersion + 重新生成 CID 更新 current_hash |
| POST /collections/:id/:collection_name/visibility | `SetCollectionVisibility` | public/unlisted/private 三选一 |
| POST /collections/:id/:collection_name/tags | `UpdateCollectionTags` | 全量替换标签 |
| GET /:username/:collection_name/*filepath | `DownloadCollectionFile` | 查条目 → sha256 provider → `DownloadBySHA256Internal`；url provider 按 `col.FollowRedirects` 决定 302 或返回 `{url, follow_redirects:false}` JSON |

CID 指针机制（collection.go:6-11 头注释）：Commit 时除版本快照外，把 entries 组为
AnonCollection JSON 存 content-addressed 存储并记 `collections.current_hash`；
`GetCollection` 优先经 `collSvc.GetAnonByHash` 读 CID JSON 返回 entries（providers 全量），
失败才 fallback `collection_entries` 表。

调用关系：`service.CollectionService`（纯转发层）+ `service.AnonService` 无关——
`GetAnonByHash`/`SaveAnon` 经 CollectionService 转发 repository。

### 4. download.go — 内容寻址下载（多协议）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| download.go | sha256/CID 下载、Range 请求、通用多协议下载端点 | `DownloadBySHA256`、`DownloadBySHA256Internal`、`DownloadBySHA256Local`、`DownloadByCID`、`UniversalDownload`、`UniversalDownloadSources`、`UniversalDownloadRefresh`、`InitUniversalDownloader`、`InitIPFSProvider` |

路由（router.go:186-194）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| GET /sha256sum/:sha256 | `DownloadBySHA256Local` | 仅本地存储直读（无多协议回退），`X-Content-Encoding: gzip` 支持 |
| GET /sha256sum/:sha256/:filename | 同上 | 带文件名变体（同 handler） |
| GET /ipfs/:cid | `DownloadByCID` | `GetMetaByCID` 查本地 → 未命中且配了 IPFS 网关 → `ipfsGatewayProvider.FetchByCID` → `fileSvc.ImportGatewayData` 落盘登记 → 转 `DownloadBySHA256Internal`；命中打 `X-CID` 头 |
| GET /download/:hash | `UniversalDownload` | `universalDownloader.Download` → `X-Protocol` 头 |
| GET /download/:hash/sources | `UniversalDownloadSources` | `CheckSources`（30s 超时） |
| POST /download/:hash/refresh | `UniversalDownloadRefresh` | authRequired；`ClearLocalCache` 后重跑流水线 |

Range 支持（download.go:164-201 `handleRangeRequest` + 275-326 `parseRangeHeader`）：
标准 `bytes=N-M`、后缀 `bytes=-N`、开放式 `bytes=N-`、越界 416 `Content-Range: bytes */total`、
end 越界 clamp 到文件尾。`parseRangeHeader` 源注：legacy/relay.go ParseRange 批2 迁移。

调用关系：`downloader.UniversalDownloader` + `service.FileService`（GetMeta/GetMetaByCID/
ImportGatewayData）+ `provider.IPFSProvider`。

### 5. file.go — 文件管理（上传/注册/验证/删除/复制/浏览）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| file.go | 文件上传与本地注册、元数据验证、删除、复制、目录浏览、版本 diff、列表 | `UploadFile`、`RegisterURL`、`RegisterLocalFile`、`RegisterFolder`、`VerifyFile`、`DeleteFile`、`CopyFile`、`DiffVersions`、`ListFiles`、`BrowseDir`、`InitFileController` |

路由（router.go:282-294，/files 组；写操作挂 authRequired）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| GET /files | `ListFiles` | `?sort=time\|name\|path\|type\|size`，`fileSvc.ListAll` |
| POST /files/upload | `UploadFile` | multipart `file` 字段；`MaxBytesReader` 按认证状态限流（`MaxUploadBytes`/`MaxUploadBytesAnon`）；409 语义：已存在返回 200 + `already_exists:true`；storage 关闭 → 403 |
| POST /files/register_local | `RegisterLocalFile` | body `{path, filename}` |
| POST /files/register_url | `RegisterURL` | body `{url, filename}`，自动跟随重定向 |
| POST /files/register_folder | `RegisterFolder` | body `{folder_path}`，递归注册全部文件 |
| GET /files/verify/:hash | `VerifyFile` | 元数据查询（400 非法 hash / 404 未找到） |
| GET /files/browse | `BrowseDir` | `?path=`；空与 "/" 都映射 storage 根（前端文件管理器默认 "/"） |
| DELETE /files/:hash | `DeleteFile` | 删本地文件 + file_meta + file_providers |
| POST /files/copy | `CopyFile` | body `{hash, dest_path}`，经 `ReadFile` 读源再写目标 |
| POST /files/diff | `DiffVersions` | body `{version_a, version_b}`，返回 added/removed/modified 三组 |

调用关系：`service.FileService`（Upload/RegisterLocal/RegisterFolder/RegisterURL/Verify/
Delete/CopyFile/ReadFile/MaxUploadBytes/ListAll/BrowseDir）+ `service.CollectionService`
（DiffVersions 的 VersionEntries）。

### 6. fork.go — 复刻 / 拉取

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| fork.go | 集合复刻（Fork）、上游拉取（Pull 占位） | `ForkCollection`、`PullCollection` |

路由（router.go:320-324，/actions 组；authRequired）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /actions/fork | `ForkCollection` | 查源集合 → `CreatePlain` 目标 → 逐条 `AddProviderEntry`/`AddEntry` 复制；目标已存在 409 |
| POST /actions/pull | `PullCollection` | 占位：返回 "not implemented"，随后建 transfer_tasks 记录标 completed（LEGACY.md 标注可删的 no-op） |

调用关系：`service.CollectionService` + `service.TaskService`（PullCollection 的假任务）。

### 7. merge.go — 合并（三策略）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| merge.go | 源集合合并到本地集合，ours/theirs/manual 三策略 | `Conflict`、`MergeFromSource` |

路由（router.go:262,321：/collections/merge 与 /actions/merge 同 handler）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /collections/merge、POST /actions/merge | `MergeFromSource` | body `{username, collection_name, source_username, source_coll_name, strategy}` |

冲突语义（merge.go:93-123）：按 path 比对 providers 的 sha256 主 hash；
`manual` 且存在冲突 → 409 + `{conflicts:[{path, local_hash, source_hash, local_providers, source_providers}]}`；
合并时新增条目直接采用，冲突条目按策略（theirs 取源 / ours 默认保留本地）。

调用关系：`service.CollectionService`。

### 8. p2p.go — P2P/BT/IPFS/pin/端口转发（1092 行，最大端点组）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| p2p.go | 多个子端点组的集合：BT DHT（announce/find/bep44/bep51）、BT 下载（torrent/magnet/进度/做种）、BT 合集做种、端口转发 v2（HTTP 面）、认证状态、IPFS pin、网关健康 | `InitBTController`/`InitBTClient`/`InitPinController`/`InitForwardController` + 约 30 个 handler |

子组路由（router.go:196-244）：

**/p2p**（批2 精简后仅剩认证状态/WebRTC 信息/端口转发）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| GET /p2p/auth/status | `AuthStatus` | 读 gin context 的 authenticated/username/role |
| GET /p2p/webrtc/info | `WebRTCInfoHandler(cfg)` | STUN/TURN 配置（见 webrtc.go） |
| POST /p2p/forward/create | `CreateForwardSession` | authRequired；`{key, port}` → `AddForwardRule`（运行时登记，不持久化） |
| POST /p2p/forward/connect | `ConnectForwardSession` | authRequired；`{key, target_peer, local_port, port?}` → 127.0.0.1 起监听，幂等（同 key 已监听直接返回） |
| GET /p2p/forward/list | `ListForwardSessions` | `{listeners, tunnels}`（tunnels 来自 `ListForwardStreams`） |
| POST /p2p/forward/close | `CloseForwardSession` | authRequired；按 key 关监听、按 peer_id 断隧道 |

转发机制（p2p.go:713-847）：`fwdListeners` 包级 map 管理监听；`acceptForwardTunnels`
每条本地 TCP → `forwardPeer.OpenForward(ctx, targetPeer, key, targetPort)`（30s 超时）→
`pipeTCPForward` 双向透传，任一侧 EOF 双向关闭。

**/bt**（依赖 p2p_bt 独立库，controller 是薄包装）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| GET /bt/status | `BTDHTStatus` | `{enabled, listen_addr, num_nodes, node_id}`；未启用 200 `{enabled:false}` |
| POST /bt/announce | `BTAnnounce` | `btSvc.Announce(hash)` |
| POST /bt/find | `BTFindProviders` | `btSvc.FindProviders(hash)` |
| POST /bt/bep44/put | `BEP44Put` | base64 data；mutable 拒绝 501（API 不做 key 管理）；immutable → `PutImmutable` |
| POST /bt/bep44/get | `BEP44Get` | 40hex target → `GetImmutable` |
| GET /bt/bep51/sample | `BEP51Sample` | `DiscoverInfohashes(200)` |
| POST /bt/torrent | `BTTorrentUpload` | multipart `torrent` → `AddTorrentBytes` |
| POST /bt/magnet | `BTMagnetResolve` | `AddMagnetURI` |
| GET /bt/downloads | `BTDownloadList` | `ListDownloads` |
| GET /bt/download/:infohash | `BTDownloadProgress` | `GetDownload` |
| GET /bt/download/:infohash/torrent | `BTDownloadTorrent` | .torrent 文件，文件名做非法字符清洗（p2p.go:563-570） |
| GET /bt/download/:infohash/magnet | `BTDownloadMagnet` | magnet URI |
| POST /bt/download/:infohash/pause\|resume\|seed\|unseed | `BTPauseDownload` 等 | 任务控制 |
| DELETE /bt/download/:infohash | `BTRemoveDownload` | 删任务及文件 |
| GET /bt/stats | `BTGlobalStats` | `GetGlobalStats` + `ListSeeders` |
| POST /bt/seed-collection | `BTSeedCollection` | 合集 → 临时目录组文件 → `BuildFromFilePath` 建 torrent → 数据写入 BT 下载目录 → `AddTorrentBytes` + `SetAutoSeed` |

**/ipfs**（HTTP 网关 pin）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /ipfs/pin/:cid | `PinCID` | 已 pin 直接返回；否则 `FetchByCID`（60s 超时）→ 落盘 CAS → `pinSvc.InsertMeta`+`Insert` |
| DELETE /ipfs/pin/:cid | `UnpinCID` | `pinSvc.Get`/`Remove` |
| GET /ipfs/pins | `ListPins` | `pinSvc.List` |
| GET /ipfs/gateways | `IPFSGatewayStatus` | 每个网关 HEAD 探活（5s 超时），`/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn` 恒测节点 |

调用关系：`p2p_bt.BTDHTService` / `p2p_bt.BTClient`（外部能力切面 ⑦）、
`service.PinService`、`transport.PeerJSService`（forward）、`service.CollectionService`
（BTSeedCollection 的 GetAnonByHash）、`provider.IPFSProvider`（包级 ipfsGatewayProvider）。

### 9. ping.go — 健康检查

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| ping.go | 健康检查 | `Ping` |

路由：GET /ping → 200 "pong"（router.go:186）。无依赖。

### 10. share.go — 分享链接

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| share.go | 创建/访问/列出分享链接 | `CreateShare`、`AccessShare`、`ListShares`、`InitShareController` |

路由（router.go:337-342；创建/列出挂 authRequired，访问 token 公开）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /shares | `CreateShare` | body `{hash, type: file\|collection, filename}` → `{token, hash, type, filename, url:"/s/"+token, expires}`（30 天） |
| GET /s/:token | `AccessShare` | collection → 302 `/anon/collections/{hash}`；file → 302 `/sha256sum/{hash}` |
| GET /shares | `ListShares` | 未过期分享列表（最多 100） |

调用关系：`service.ShareService`。

### 11. sync.go — 本地同步

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| sync.go | 集合文件同步到本地磁盘 + 状态查询 | `SyncController`（`SaveLocal`/`GetStatus`）、`NewSyncController` |

路由（router.go:310-314，/local 组）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| POST /local/save | `SaveLocal` | body `SaveLocalRequest`（collection_hash/local_path/include/exclude）→ `syncSvc.SaveToDisk` |
| GET /local/status/:hash | `GetStatus` | `syncSvc.GetStatus` → 已保存/缺失文件清单 |

调用关系：`service.SyncService`（含 `downloader.UniversalDownloader` 注入，见 services.md）。

### 12. task.go — 异步任务（占位）

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| task.go | 任务状态查询（占位） | `GetTaskStatus`、`ListTasks`、`InitTaskController` |

路由（router.go:330-334，/tasks 组）：

| 方法/路径 | handler | 说明 |
|---|---|---|
| GET /tasks | `ListTasks` | 恒返回空数组（LEGACY.md 标注可删） |
| GET /tasks/:id | `GetTaskStatus` | `taskSvc.Get`（transfer_tasks 表） |

调用关系：`service.TaskService`。

### 13. webrtc.go — WebRTC 配置

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| webrtc.go | STUN/TURN 配置下发 | `WebRTCInfoHandler(cfg)`（工厂函数返回 handler） |

路由：GET /p2p/webrtc/info → `{stun_server, turn_server?}`。依赖 `config.Config`。

## 关键机制

### 1. Init\* 包级变量注入

历史包袱：controller 包用包级可变全局（`anonSvc`/`fileSvc`/`collSvc`/`universalDownloader`/
`btSvc`/`forwardPeer`...）+ `Init*` 注入，router.go:94-174 在 SetupRouter 里统一装配。
新代码优先用 `NewXxxController` 结构体风格（auth.go/sync.go），避免全局状态。

### 2. 错误→状态码映射（无统一错误中间件）

每个 handler 自己按 service 返回的错误映射状态码，约定：

| 情况 | 状态码 |
|---|---|
| 绑定失败/参数非法 | 400 |
| 认证失败 | 401 / 403（storage disabled） |
| 资源不存在 | 404 |
| 重复创建/合并冲突 | 409（merge 的 manual 冲突也走 409 + conflicts 清单） |
| 依赖未装配（universalDownloader nil 等） | 503 |
| 其他 | 500 |

### 3. 下载统一入口 `DownloadBySHA256Internal`

collection.go 的 `DownloadCollectionFile` 与 download.go 的 `DownloadByCID` 都收敛到
`DownloadBySHA256Internal`（download.go:51）——一个函数统一 X-Protocol 头、文件名、
inline/attachment、gzip Content-Encoding、Range 处理。

## 与其它模块的关系

```
router（路由注册 + Init* 装配 + admin 内部转发入口）
  ↓ 调用
controller（本层）
  ├→ service（FileService/AnonService/CollectionService/ShareService/TaskService/
  │          PinService/SyncService/AuthService）
  ├→ downloader.UniversalDownloader（下载端点 + anon 文件下载）
  ├→ provider.IPFSProvider（网关 fallback / pin / 健康检查）
  ├→ transport.PeerJSService（端口转发 HTTP 面）
  └→ p2p_bt（BTDHTService/BTClient，外部能力切面）
```

- **不反向依赖**：controller 不 import repository（M2 收层达成，REFACTOR.md §7 M2）。
- **双入口**：admin 帧 → gin engine ServeHTTP（router.go:357）复用本层全部 handler。
- **与 ② 的边界**：controller 经 `transport.PeerJSService` 的语义 API（OpenForward/
  AddForwardRule/ListForwardStreams/CloseForwardStream）做转发，不直接操作连接。

## 坑与设计决策

1. **包级全局注入的竞态面**：Init\* 在 SetupRouter 内串行调用，运行时不变；但测试必须
   先 Init 再调 handler，否则 nil 解引用（file_test.go 的 setupFileTestRouter 模式）。
2. **gin 不允许 :param 与 *wildcard 并存**（collection_dispatch.go:56-58）：用户体系深层
   GET 全部并入 `/:id/*filepath` 一个 wildcard 路由内部分派；`withParams` 用
   `c.Copy()` + 追加 Params 补齐参数名，controller 零改动（REFACTOR.md §3.1）。
3. **/collections 双语义分派**（dispatchCreateCollection/dispatchGetCollection）：同一路径
   按 body/路径形态分派匿名（hash 寻址）与用户（username 寻址）两套体系——注册路由冲突
   的修复产物（REFACTOR.md §3.1，删 legacy redirect 前的启动 panic）。
4. **匿名下载是「下载器+重定向」双链**（anon.go:125-155）：sha256 主链（universalDownloader）
   失败降级 url 302——collection.go 的 `FollowRedirects` 为 false 时改为返回 JSON 让前端
   决策（防 SSRF 式跟随）。
5. **/peerjs/fetch 的 64MB 上限**（peerjs_routes.go:69-92，H4）：整个响应 buffer 驻留内存，
   挂认证 + 限制 size；超大文件走 /ws/peer 分片。前端不用此端点。
6. **/files/upload 的 409 语义**：已存在文件返回 200 + `already_exists:true` 而非 409
   （幂等上传，前端可继续拿 hash）。
7. **BTSeedCollection 的全量内存/磁盘拷贝**（p2p.go:615-709）：合集条目逐个 ReadFile +
   WriteFile 两次（临时目录 + BT 数据目录）——大合集内存峰值高，属已知局限（TODO 可
   流式化）。
8. **p2p.go 承载 legacy 端点**（LEGACY.md A 段）：libp2p 时代端点已删（REFACTOR.md §8
   批2，2026-08-16），现 p2p.go 保留 BT/IPFS/forward 三个子组；其中 forward 是新实现
   （REFACTOR.md §3.9），BT/IPFS 依赖外部能力切面 ⑦。
9. **auth.go/task.go ListTasks 无路由注册/恒空**（LEGACY.md 第 61-62 行）：保留占位。

## 测试（94 单测，含 controller/service/source/downloader；`scripts/test-layers.sh` L4 段）

> 命令：`go test -tags nosqlite ./internal/controller/... ./internal/service/... ./internal/source/... ./internal/downloader/...`

> controller 测试除 `TestBrowseDir_SlashMeansStorageRoot` 外均属 legacy 标注
> （文件头注释：「本文件属于 legacy 代码…未逐一标注发现背景」）。

| 文件 | 测试 | 发现背景 |
|---|---|---|
| collection_test.go | `TestCreateCollection_Valid/Duplicate/InvalidBody`、`TestListCollections`、`TestGetCollection(_NotFound)`、`TestAddEntry(_CreatesCollectionIfNotExists)`、`TestRemoveEntry(_CollectionNotFound)`、`TestCommitCollection`、`TestGetVersionLog`、`TestSearchCollections`、`TestForkCollection(_SourceNotFound)`、`TestRollbackCollection`、`TestCreateCollectionWithVisibility(_WithPrivateVisibility)`、`TestSetCollectionVisibility`、`TestListPublicCollections`、`TestListCollectionsForUserReturnsAllVisibilities`、`TestInvalidVisibilityRejected` | legacy（gin 直调 handler，覆盖 CRUD/版本/可见性主链路与 404/409/400 分支） |
| download_test.go | `TestHandleRangeRequest_StandardRange/MidRange/SuffixRange/OpenEndedRange/ZeroByteFile/RangeBeyondFile/EndBeyondFile/SuffixLargerThanFile` | legacy（parseRangeHeader 自 legacy/relay.go 迁移的纯函数回归，覆盖 206/416/clamp 边界） |
| file_test.go | `TestListFiles_Empty(_WithSort)`、`TestVerifyFile_NotFound(_InvalidHash)`、`TestBrowseDir_DefaultRoot(_SpecificPath)`、`TestUploadFile_NoFile`、`TestCopyFile_MissingParams(_InvalidHash)` | legacy |
| file_test.go | `TestBrowseDir_SlashMeansStorageRoot` | **安全边界收紧后 BrowseDir 拒绝根外路径，前端文件管理器默认传 "/" 导致一直 400**；修复：空路径与 "/" 都映射 storage 根（file.go:346-348 注释） |
| ping_test.go | `TestPing` | legacy（健康检查冒烟） |

## 文件清单

```
back/internal/controller/
├── anon.go             匿名集合（Create/List/Get/Download/Fork/Commit）
├── auth.go             AuthController（Register/Login/Logout/Me，无路由注册）
├── collection.go       用户集合 CRUD + 条目 + 版本 + 可见性 + 标签
├── collection_test.go  集合 CRUD/版本/可见性测试（legacy 标注）
├── download.go         sha256/CID 下载 + Range + 通用多协议下载端点
├── download_test.go    Range 处理测试（legacy 标注）
├── file.go             上传/注册/验证/删除/复制/浏览/列表/diff
├── file_test.go        文件管理测试（含发现背景 1 条）
├── fork.go             复刻（Fork）+ 拉取（Pull 占位）
├── merge.go            合并（ours/theirs/manual 三策略）
├── p2p.go              BT DHT/BT 下载/端口转发 v2/pin/网关健康（1092 行）
├── ping.go             健康检查
├── ping_test.go        Ping 测试
├── share.go            分享链接（Create/Access/List）
├── sync.go             SyncController（SaveLocal/GetStatus）
├── task.go             任务查询（ListTasks 占位）
└── webrtc.go           STUN/TURN 配置下发
```