# 模块 05：controller HTTP 处理器

- **代码位置**：`back/internal/controller`（15 个 Go 文件；路由注册与认证中间件在 `back/internal/router/`，与本模块强相关，见 §6）
- **功能一句话**：把 HTTP 请求翻译成 service / downloader / transport 调用——负责参数解析、格式与鉴权校验、错误映射为状态码，**自身几乎不持久化**，全部读写委托给下级模块。
- **依赖**：`back/internal/service`（File/Collection/Share/Anon/Pin/NodeDirectory/NodeShare/PeerPuller/Sync 服务）、`back/internal/downloader`（UniversalDownloader）、`back/internal/provider`（IPFSProvider）、`back/internal/transport`（PeerJSService 端口转发/共享清单查询）、`back/internal/nodestate`（Operator）、`back/pkg/hashutil`（SHA256 校验）、`back/internal/config`（WebRTCInfo）、`github.com/Hana-ame/go-peerdrive-bt`（BT DHT/BT client）。
- **被依赖**：`back/internal/router/router.go` 装配并路由到全部 handler；`transport/admin.go` 的管理面 admin 帧**内部转发**到本 gin engine 复用同一套 controller（`back/internal/router/router.go:402-414`）；前端/curl/集成测试直接请求这些 HTTP 端点（`back/internal/router/router.go:215-224` 注释）。

## 1. 逻辑

### 1.1 职责划分

controller 是请求处理链中的「HTTP 处理器面」：**参数解析 / 鉴权校验 / 调用 service 与 downloader**，不直接做业务持久化。三个典型模式：

1. **解析**：`c.ShouldBindJSON(&req)`（如 `back/internal/controller/file.go:99`、`collection.go:80`）、`c.Request.FormFile("file")`（`file.go:46`）、`c.Param`/`c.Query`/`c.DefaultQuery`（`file.go:176/328/345`）、`c.GetHeader`（`download.go:84`）。
2. **校验**：路径 hash 一律先过 `hashutil.IsValidSHA256`（64 位小写 hex，`back/pkg/hashutil/hashutil.go:16-23`），参数字段非空判断（如 `file.go:104-108`、`share.go:32-35`）。可见性/权限类校验在 service 层（如 `anonSvc.GetCollectionVisibleTo`，`anon.go:141`）。
3. **委托**：调 service 方法，按错误映射状态码（`ErrStorageDisabled`→403、`ErrFileAlreadyExists`→200 带 `already_exists`，`file.go:57-72`；绑定失败→400，未找到→404，其余→500）。

### 1.2 依赖注入方式：包级变量 + Init* 函数

controller 无构造函数，全部依赖存为包级变量，由 router 在进程启动时经 `Init*` 注入（`file.go:30-36`、`collection.go:38-44`、`p2p.go:45-64` 等）。清单（每个 handler 文件头部的 `var`）：

| 包级变量 | 类型 | 注入函数 | 用途 |
|---|---|---|---|
| `fileSvc` | `*service.FileService` | `InitFileController` | 文件上传/注册/删除/列表/浏览 |
| `collSvc` | `*service.CollectionService` | `InitCollectionController` | 集合 CRUD/条目/版本/CID |
| `shareSvc` | `*service.ShareService` | `InitShareController` | 分享链接 |
| `anonSvc` | `*service.AnonService` | `InitAnonController` | 匿名（内容寻址）集合 |
| `pinSvc` | `*service.PinService` | `InitPinController` | IPFS pin |
| `peerPuller` | `*service.PeerPuller` | `InitPeerPuller` | 跨节点拉取任务 |
| `nodeDir` | `*service.NodeDirectory` | `InitNodeDirectory` | 节点市场（main 经 `router.SetNodeDirectory` 转发注入，`peerjs_routes.go:33-36`） |
| `nodeShareSvc` | `*service.NodeShare` | `InitNodeShareController` | 本节点共享范围 |
| `peerShareSvc` | `*transport.PeerJSService` | `InitPeerShareController` | 「问对端要共享清单」（share 帧请求方） |
| `forwardPeer` | `*transport.PeerJSService` | `InitForwardController` | 端口转发（forward v2） |
| `btSvc` / `btClient` | `*p2p_bt.BTDHTService` / `*p2p_bt.BTClient` | `InitBTController` / `InitBTClient` | BT DHT / BT 下载 |
| `ipfsGatewayProvider` | `*provider.IPFSProvider` | `InitIPFSProvider` | IPFS 网关回退/抓取 |
| `universalDownloader` | `*downloader.UniversalDownloader` | `InitUniversalDownloader` | 多协议下载管线 |

唯一的结构体式 controller 是 `SyncController`（`sync.go:12-18`，`NewSyncController` 构造并持有 `syncSvc`）；其余全是包级函数 handler。

### 1.3 主要流程

- **上传流程**（`file.go:1-14` 头注释）：`multipart` → `MaxBytesReader` 限长（`file.go:42-44`，上限由 `fileSvc.MaxUploadBytes(c)` 按认证状态区分，见 §4.4）→ `fileSvc.Upload`（写 CAS `storage/{h[:2]}/{h}` → INSERT `file_meta` → INSERT `file_providers`）→ 201 + hash/size/mime/filename。
- **下载流程**（`download.go:1-8` 头注释）：校验 hash → `universalDownloader.Download(ctx, hash)`（管线 `local → ipfsgw → btdht → http`，`back/internal/downloader/universal_downloader.go:3-14`；成功会回写本地缓存）→ 按 `fileSvc.GetMeta` 设置 `X-Protocol`/`Content-Disposition`（`inline=1` 参数切换）/`Content-Encoding: gzip`/`X-Peerdrive-Collection`（`download.go:65-89`）→ 处理 `Range`（`handleRangeRequest`，支持标准/后缀/开放式，`download.go:171-201`）。
- **集合 CID 指针机制**（`collection.go:6-11` 头注释）：`CommitCollection` 时除版本快照外，把 entries 组装成 AnonCollection JSON 存 CAS 并更新 `collections.current_hash`（`collection.go:364-415`）；`GetCollection` 时若 `current_hash` 非空优先经 `collSvc.GetAnonByHash` 读 JSON（`collection.go:170-185`），否则 fallback 到 `collection_entries` 表（`collection.go:186-192`）；`RollbackCollection` 后重新生成 CID（`collection.go:486-499`）。
- **管理面复用**：浏览器本地 WS（`/ws/peer`）发 `admin` 帧 → `transport.PeerJSService.SetAdminHandler` 把请求包成 `*http.Request` 喂给本 engine（`router.go:396-415`），因此「HTTP 直调」与「admin 帧」行为一致、只维护一份。
- **路由分派**：`/collections`、`/collections/:id`、`/collections/:id/*filepath` 因 gin 不允许同名参数段/通配段共存，统一走分派器按请求形态转发到不同 controller handler（`collection_dispatch.go:15-96`；`:id` 长度 64 判为匿名集合 hash，否则按 username）。

### 1.4 生命周期

- controller 本身无状态、无 goroutine，仅 handler 存在期间存活；包级变量在 `SetupRouter`（`router.go:121-208`）期间一次性注入，之后不变。
- 例外：`p2p.go` 的端口转发在 handler 内**常驻后台**——`ConnectForwardSession` 起 loopback 监听并注册到包级 `fwdListeners` map，`acceptForwardTunnels` 为每连接开隧道（`p2p.go:682-799`），生命周期由 `/p2p/forward/close`（`p2p.go:836-865`）或进程退出终止。

## 2. 如何储存

**controller 自身不持久化**（除 §2.3 列出的少数历史遗留直接盘操作）。所有落盘/落库都发生在被委托的 service → repository / storage / downloader 层；本模块「持有」的只有进程内内存态。

### 2.1 内存态（进程内，重启即失）

| 内存态 | 定义位置 | 生命周期 | 重启影响 |
|---|---|---|---|
| 全部包级服务/下载器指针（§1.2） | 各 `*_controller.go` 的 `var` | 进程生命周期，启动时注入一次 | 重启由 `SetupRouter` 重新装配，无磁盘残留 |
| `fwdListeners map[string]*fwdListener`（key 为 `"<target_peer>:<local_port>"`） | `p2p.go:688-691` | `/p2p/forward/connect` 时写入、`/p2p/forward/close` 时删除（`p2p.go:753-772, 847-855`） | 重启全部丢失；静态规则走配置 `PEERDRIVE_FORWARD_RULES`（`p2p.go:693-694` 注释），在 transport 侧装配，不受影响 |
| `startedAt time.Time` | `health.go:20` | 进程启动（包初始化） | 重启后重新计时（`/health` 的 `uptime_sec`） |
| `dbPing func() error` 探针 | `health.go:27` | 注入于 `InitHealth`（`health.go:30-32`） | 重启后需重新注入，否则 `/ready` 恒 503（`health.go:57-62`） |

### 2.2 委托给下级模块的储存

- **内容寻址文件（CAS）**：`storageDir/<hash[:2]>/<hash>`。路由注入 `storageDir` 到 gin context（`router.go:75-79`），controller 用 `c.MustGet("storageDir").(string)` 取出（`collection.go:171/382/494`、`p2p.go:575`）。写文件由 service 完成：`fileSvc.Upload`（`file.go:55`）、`anonSvc.CreateCollectionWithVisibility`（`back/internal/service/anon_service.go:140-149`）、`collSvc.SaveAnon`→`repository.SaveCollection`（`collection_service.go:138-139`）。
- **SQLite 元数据**：`file_meta`、`file_providers`、`collections`/`collection_entries`/`collection_versions`/`version_entries`、`pins` 等表，全部经 service→repository 写入（controller 只传参；`collection.go:3-4` 头注释、`pin_service.go:16-46`）。
- **JSON 配置文件**：`storageDir/share_scope.json`（共享范围，`service/nodeshare.go:15,69,191,435`）、`storageDir/joined_nodes.json`（已加入节点，`service/node_directory.go:11,37,153-157`，原子写 + 0600）。
- **下载目录**：BT 下载与跨节点拉取落 `DownloadDir`（`peerpull.go:20-25` 注释：落 DownloadDir 而非 CAS，登记 `file_index` 便于本节点再共享）。

### 2.3 少数「controller 直写盘」的历史遗留（如实列出）

- `PinCID`：直接 `os.MkdirAll` + `os.WriteFile` 写 `storageDir/<hash[:2]>/<hash>`，再 `pinSvc.InsertMeta` 登记（`p2p.go:950-958`）。
- `BTSeedCollection`：直接 `os.MkdirTemp` 建临时目录、`os.ReadFile`/`os.WriteFile` 搬运合集文件进 BT 数据目录，再 `btClient.AddTorrentBytes`（`p2p.go:582-656`）。
- `DownloadBySHA256Local`：本地直读 `storageDir/<hash[:2]>/<hash>`（`download.go:108-114`），不经下载器。
- `checkGateway`：直接发 `HEAD` 请求探测网关健康（`p2p.go:1041-1059`）。

## 3. 何时储存

### 3.1 进程启动（装配期，不落盘）

`SetupRouter` 创建全部 service/下载器并 `Init*` 注入 controller（`router.go:121-208`）；BT DHT/BT client/UniversalDownloader 在此构造（`router.go:137-208`）。此阶段只建立内存依赖，无磁盘写入（BT client 的下载目录是否被库预创建未核实）。

### 3.2 请求触发（controller 层实际「触发储存」的全部时机）

| 触发端点 | controller 位置 | 触发的落盘/落库动作 |
|---|---|---|
| `POST /files/upload`、`/files/register_local`、`/files/register_url`、`/files/register_folder` | `file.go:39-171` | `fileSvc.Upload/RegisterLocal/RegisterURL/RegisterFolder`：写 CAS + `file_meta` + `file_providers` |
| `DELETE /files/:hash` | `file.go:205-222` | `fileSvc.Delete`：删本地文件 + 元数据 |
| `POST /collections/:id/:coll/commit` | `collection.go:344-416` | `SaveAnon`（写 CAS JSON）+ `UpdateCurrentHash` + `CreateVersion` + `SnapshotEntries`（写版本表） |
| `POST /collections/:id/:coll/rollback/:vid` | `collection.go:463-502` | `RestoreVersion` + 重新 `SaveAnon` + `UpdateCurrentHash` |
| `POST /collections/:id/:coll/entries`、`DELETE …/entries/*path` | `collection.go:211-272` | `AddEntry`/`GetOrCreate`/`RemoveEntry`：写 `collection_entries`（GetOrCreate 会在集合不存在时建行） |
| `POST /anon/collections`、`/anon/collections/commit`、`PUT /anon/collections/:hash/visibility`、`POST /anon/collections/fork` | `anon.go:34-69, 87-108, 225-293, 306-332` | `CreateCollectionWithVisibility`/`CommitCollection`/`UpdateCollectionVisibility`（visibility/commit 后写**新 hash** 的 JSON，旧 hash 仍可解析，`anon.go:72-77` 注释）/fork 继承源权限重写 |
| `POST /shares` | `share.go:22-51` | `shareSvc.Create` 落分享链接记录（token/hash/type/filename/expires） |
| `POST /peerjs/nodes/join`、`DELETE /peerjs/nodes/join` | `node_market.go:129-165` | `nodeDir.Join/Leave`：原子写 `joined_nodes.json`（`node_directory.go:153-157`） |
| `PUT /peerjs/share`、`POST /peerjs/share/files` | `node_share.go:78-135` | `nodeShareSvc.Update/SetFilesShared`：校验后落盘 `share_scope.json`（`nodeshare.go:435`） |
| `POST /ipfs/pin/:cid`、`DELETE /ipfs/pin/:cid` | `p2p.go:910-1000` | 直写 CAS + `pinSvc.InsertMeta/Insert/Remove`（pins 表等） |
| `POST /bt/torrent`、`/bt/magnet`、`/bt/download/:infohash/pause\|resume\|seed\|unseed`、`DELETE /bt/download/:infohash`、`POST /bt/seed-collection` | `p2p.go:282-676` | `btClient.AddTorrentBytes/AddMagnetURI/…`：BT 库落盘下载数据/进度；`seed-collection` 直写临时目录与 BT 数据目录 |
| `POST /local/save` | `sync.go:20-38` | `syncSvc.SaveToDisk`：把合集拉回本地磁盘（`sync_service.go:126-138`） |
| `POST /p2p/pull`、`/p2p/pull/collection`、`/p2p/pull/cancel` | `peer_pull.go:43-180` | `peerPuller.Start/StartCollection/…`：流式落盘 DownloadDir → 复算 sha256 → 登记 `file_index`；任务表在内存（上限 200，`peerpull.go:57-61`） |

### 3.3 事件回调（router 层装配，非 controller 自有）

BT 下载完成回调 `btClient.SetOnComplete` → `fileSvc.RegisterBTFile` 登记入库（`router.go:162-175`）——回调注册在 router，但落库的是 controller 持有的 `fileSvc`。

### 3.4 定时任务 / 优雅关闭

controller 层**没有**定时持久化与优雅关闭钩子（`fwdListeners` 不落盘；`RateLimit` 的 `sweep` 只是内存清理，`middleware.go:185-196`）。进程重启后：内存态清空，持久态（CAS/DB/两个 JSON）原样保留。

## 4. 储存什么

### 4.1 controller 自持内存态（§2.1 明细，见上表）

### 4.2 请求参数（controller 解析后传给 service 的内容）

| 端点 | JSON/表单字段 | 关键约束 |
|---|---|---|
| 上传 | `file`（multipart）、`filename` | 体积受 `MaxBytesReader` 限制（§4.4） |
| `POST /files/register_url` | `url`（必填）、`filename`（可选） | URL 非空校验（`file.go:104-108`） |
| `POST /files/register_local` | `path`、`filename` | — |
| `POST /files/register_folder` | `folder_path` | — |
| `POST /files/copy` | `hash`（必填）、`dest_path`（必填） | hash 必须通过 `IsValidSHA256`（`file.go:244-248`） |
| `POST /collections` | `username`、`collection_name`、`visibility`、`follow_redirects`(*bool)、`tags` | 重复 (username, name) 409（`collection.go:72-98`） |
| 集合 entries | `path`、`hash` 或 `providers` | 二者至少给一个（`collection.go:228-235`） |
| `POST /shares` | `hash`(binding required)、`type`(`file`/`collection`)、`filename` | type 白名单校验（`share.go:32-35`） |
| `POST /anon/collections` | `friendly_name`、`entries[]`、`tags`、`visibility`、`access_list` | 服务层校验 path 穿越/非法 hash/providers，restricted 必须有 access_list（`anon.go:53-66`；`anon_service.go:94-122`） |
| `POST /bt/bep44/put` | `data`(base64)、`mutable`、`salt`、`key` | mutable=true 走 501 stub（`p2p.go:186-192`） |
| `POST /bt/bep44/get` | `target` | 必须 40 字符 hex（`p2p.go:221-232`） |
| `/peerjs/fetch` | `peer`、`hash`、`offset`、`size` | hash 必须合法 SHA256；size 上限 64MB（`peerjs_routes.go:119-153`） |
| `POST /p2p/forward/create` | `key`、`port` | key 非空（`p2p.go:709-712`） |
| `POST /p2p/forward/connect` | `key`、`target_peer`、`local_port`、`port` | local_port/port ∈ (0,65535]（`p2p.go:745-752`） |

### 4.3 响应内容（handler 组装输出）

- 上传：`hash, size, mime, filename, already_exists`（`file.go:64-86`）；`RegisterLocalFile` 回 `hash, filename`（`file.go:147`）。
- 分享链接：`token, hash, type, filename, url("/s/"+token), expires`（`share.go:43-50`）。
- 下载/匿名集合下载：`X-Protocol`、`Content-Disposition`（`inline=1` 时 `inline`，否则 `attachment`；文件名来自 `meta.Filename` 或路径 basename，`download.go:67-76`、`anon.go:189-199`）、gzip/Collection 标记头。
- 匿名集合创建：`hash, visibility, owner`；可见性更新：`hash(新), previous_hash(旧), visibility`（`anon.go:68,107`）。
- `/bt/*`：infohash/name/files/total 等任务摘要；`/p2p/forward/*`：listeners+tunnels 列表（`p2p.go:817-832`）。

### 4.4 默认值 / 关键约束

- **SHA256 格式**：64 位小写十六进制（`hashutil.go:16-23`）；传输层更严（`IsStrictSHA256`，`hashutil.go:62-73`）。下载端点、`/files/verify/:hash`、`/files/:hash`、`/files/copy` 均先校验、非法回 400（`download.go:52-55`、`file.go:177-181,208-212,244-248`）。
- **上传上限按认证区分**：认证用户 `cfg.MaxUploadBytes`（默认 100MB），匿名 `cfg.MaxUploadBytesAnon`（默认 10MB），以 `c.Get("authenticated")` 判定（`file_service.go:837-842`），再套 `http.MaxBytesReader`（`file.go:42-44`）。
- **多协议下载顺序**：`local → ipfsgw → btdht → http`（`universal_downloader.go:3-8`，顺序/超时可配）。下载成功会**缓存回本地 storage**，下次直接 local 命中（`universal_downloader.go:13-14`）。
- **存储路径规则**：`storageDir/<hash[:2]>/<hash>`（README.md:58、`anon_service.go:140-149`）。
- **共享范围**：默认关闭（`PEERDRIVE_SHARE_ENABLE=false`）；级别三档 `public/unlisted/private`，同内容多条来源命中取最宽松（`node_share.go:13-14`；`nodeshare.go`）。
- **拉取任务表**：内存态，上限 200 条，超出丢弃最老的已结束任务（`peerpull.go:57-61`）。
- **其他**：BEP44 immutable put 的 target 为 20 字节（`p2p.go:227-234`）；`/bt/bep51/sample` 采样 200 个 infohash（`p2p.go:260`）；集合各字段默认值见 service 层（未逐项核实）。

## 5. 边界与坑

- **controller 层不碰持久化是硬约定**（`health.go:23-27` 注释）：连「数据库还活着吗」都通过注入 `func() error`（`repository.Ping`，`router.go:124`）而非直接 import repository；§2.3 的直写盘点是历史遗留（`p2p.go` 注释明确标注 legacy，`pin_service.go:2-3`）。
- **hash 未校验就 `hash[:2]` 会越界 panic**：已在下载与 verify 端点加 `IsValidSHA256` 前置（README.md:77；`download.go:52`）；历史 bug 见 `anon_service_test.go:122-130`。
- **上传大小限制存在**：`MaxBytesReader`（`file.go:42-44`）；存储关闭（`ErrStorageDisabled`）→403；重复上传 →200 + `already_exists`（`file.go:57-72`）。
- **可见性闸门不能只靠 hash 寻址**：匿名集合读取统一走 `GetCollectionVisibleTo`，private/restricted 对无权者回 404（等价不存在，`anon.go:135-147` 注释）。曾用裸 `GetCollectionByHash` 读导致权限形同虚设（`anon.go:138-140` 注释）。fork 必须继承源集合 visibility/owner，否则可把受限内容换 hash 公开（`anon.go:274-282`）。权限切档 `PUT` 必须挂 `authRequired`（`router.go:326-327`）。
- **gin 路由同名参数段冲突**：`/collections` 采用分派器；子路由参数实际为 `:id` 而 handler 读 `:username`，运行时恒空 → `collectionUsername` 兜底（`collection.go:46-59` 注释：2026-08-19 脏行 bug）。分派器补参**不能用 `c.Copy()`**（不复制 ResponseWriter，下游必 nil panic，`collection_dispatch.go:87-90`）。
- **`/peerjs/fetch` 内存风险**：响应整段 buffer 驻留内存，收紧 = 挂认证 + 单次 64MB 上限，超大文件走 `/ws/peer` 分片（`peerjs_routes.go:115-153`）。
- **认证**：`AuthRequired` 未配 `RegistrationServer` 时内部放行（本地单机模式，`auth_middleware.go:26,57-61`）；token 校验每请求一次远程 `whoami`，30s 内存缓存只缓存成功结果（`auth_middleware.go:84-145`）。`AuthStatus` 区分「请求者 username」与「节点 operator」（`p2p.go:888-896`）。
- **健康探针语义分离**（`health.go:1-8` 注释）：`/health` 存活探针不查依赖（防重启风暴），`/ready` 就绪探针查 DB；`dbPing` 未装配时 `/ready` 宁可 503 也不假装健康（`health.go:57-62`）。
- **限流**：按 IP 令牌桶；OPTIONS 预检不计、loopback/未知来源不限流（`middleware.go:215-229`）；admin 内部转发请求标 `RemoteAddr=127.0.0.1:0` 避免全部进同一限流桶（`router.go:403-410`）。
- **CORS**：仅白名单 Origin 回显（无凭据；`router.go:81-107`）；`/ws/peer` 本地会话同样按 Origin/loopback 白名单放行（`peerjs_routes.go:158-184`）。
- **Range 下载**：unsatisfiable（start ≥ total）回 `416 Content-Range: bytes */N`；0 字节文件整包返回不做分片（`download.go:171-188`）；解析来自 legacy `relay.go ParseRange` 迁移（`download.go:277` 注释）。
- **转发幂等**：同 `key:本地端口` 已有监听直接返回「已连接」，重复点击不炸（`p2p.go:753-761`）；隧道失败只断当前连接、监听继续（`p2p.go:778-799`）。
- **其他历史坑（代码注释在案）**：`BTSeedCollection` 的 `SetAutoSeed` 在失败分支可能重复但无害（`p2p.go:662`）；`tokenCache` 超 1024 项顺带清理过期项防内存泄漏（`auth_middleware.go:117-130`）；Swagger 默认开，生产建议 `PEERDRIVE_SWAGGER=off`（`router.go:385-391`）；`/sha256sum` 实际注册的是本地直读的 `DownloadBySHA256Local`，多协议版 `DownloadBySHA256` 未注册（`router.go:229-230`）。

## 6. 对外连接

- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md)：frontend↔backend 本地 WS 会话（`/ws/peer`）。前端经 `admin` 帧调本模块全部控制器：`transport.PeerJSService.SetAdminHandler` 将 admin 帧转成 HTTP 请求喂给本 gin engine（`router.go:396-415`）；WebRTC 连接刻意不处理 admin。
- [../connections/02-router-controller.md](../connections/02-router-controller.md)：router→controller。`SetupRouter` 注册路由、挂全局中间件（RequestID/安全头/访问日志/限流/CORS/认证）、注入 `storageDir` 到 context 并调用全部 `Init*`（`router.go:44-208`）；写/管理端点统一挂 `AuthRequired`，读端点放开（`router.go:225-424`）。
- [../connections/03-controller-service.md](../connections/03-controller-service.md)：controller→service。本模块唯一正规的下行委托方向：文件/集合/匿名集合/分享/pin/节点市场/共享范围/拉取操作的读写全部经 service（§2.2）；controller 不直碰 repository。
- [../connections/04-service-repository.md](../connections/04-service-repository.md)：service→repository。SQLite 表（`file_meta`/`file_providers`/`collections*`/`pins` 等）由 repository 层落库，controller 只传参（`collection.go:3-4`、`pin_service.go:16-46`）。
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md)：controller→downloader。下载端点（`DownloadBySHA256Internal`/`UniversalDownload`/`UniversalDownloadRefresh`）调 `UniversalDownloader.Download`，多协议管线并把成功结果缓存回本地 storage（`download.go:45-94, 207-273`）。
- [../connections/10-controller-storage.md](../connections/10-controller-storage.md)：controller→storage。controller 从 gin context 取 `storageDir`（`router.go:75-79` 注入；消费点 `collection.go:171/382/494`、`p2p.go:575`），CAS 路径规则 `storageDir/<hash[:2]>/<hash>`；共享范围与已加入节点两个 JSON 也落在 storageDir（§2.2）。
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md)：controller↔信令 REST。`/peerjs/node`、`/peerjs/nodes*` 市场列表依赖信令/发现服务器的在线节点信息（`peerjs_routes.go:74-105`、`node_market.go:99-126`）。
- [../connections/06-service-transport.md](../connections/06-service-transport.md)：分层例外注解。controller 有两处**绕过 service 直接持有 `transport.PeerJSService`** 的注入：`forwardPeer`（端口转发，`p2p.go:49-52`）与 `peerShareSvc`（问对端要共享清单，`node_market.go:44-47`）；这是对「controller 只见 service」约定的既有例外，相关数据传输语义在 transport 侧。