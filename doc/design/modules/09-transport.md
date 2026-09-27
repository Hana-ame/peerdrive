# 模块 09：transport P2P 传输层

- **代码位置**：`back/internal/transport`（PeerJSService 装配层 + 帧协议 + 角色 + 发现 + 文件索引；`back/peerjs` 为独立库，见模块 10）
- **功能一句话**：PeerJS/WebRTC 数据面文件服务——一条全双工 Session（WS 或 DataChannel）上承载统一帧协议（拉取/索引/共享/管理/转发/PSK），配合 HTTP/MQTT 节点发现，让浏览器与节点经公共信令互联存取 sha256 内容。
- **依赖**：`github.com/Hana-ame/go-peerjs`（信令 + DataChannel，`peerjs_service.go:26`）、`github.com/pion/webrtc/v4`（ICEServer 类型，`peerjs_service.go:24`）、`github.com/gorilla/websocket`（本地 WS 会话，`ws_session.go:7`）、`github.com/eclipse/paho.mqtt.golang`（MQTT 发现，`mqtt_discovery.go:11`）、`github.com/google/uuid`（reqId，`outbound.go:21`）；内部包 `config`（`peerjs_service.go:27`）、`log`、`pathutil`（`file_index.go:15`、`inbound.go:19`）、`repository`（file_index 持久化委托，`file_index.go:16`）、`pkg/hashutil`（`peerjs_service.go:29`）；`Session` 接口（`ws_session.go:22-29`）；`FileRouter` 接口由外部 `source.Manager` 实现并注入（`inbound.go:46-51`）。
- **被依赖**：`cmd/server/main.go`（装配注入：`SetExtraPeers` L124、`SetShareProvider/SetShareGate` L156-158、`SetFileRouter` L233、`SetForwardRules` L210、`Start/Close` L110-111）；`back/internal/router`（`registerPeerJSRoutes` 注册 `/peerjs/*`、`/ws/peer` 并在 L182-183 建 `NewWSSession("local")` + `BindLocal`；`router.go:402-415` 经 `SetAdminHandler` 注入 gin 内部转发）；`back/internal/router/peerjs_routes.go:142` 与 HTTP 层消费 `FetchFromPeer`；`service.NodeShare`（注入 share 快照/门禁，`nodeShare → transport.SetShareProvider`）、`service.NodeDirectory`（`extraPeers` 常驻对端清单）、`service.PeerPuller`（消费 `OpenStream`/`FetchFromPeer`，`source/peer.go`）；`source.LocalSource` 复用 `FileIndex()`（`source/local.go:78-80,157-180`）；消费端/浏览器帧协议对端；测试见 `back/internal/transport/*_test.go`、`back/test/integration/*`。

## 1. 逻辑

**模块定位**：帧协议核心 + 连接级状态机 + 双向角色装配（`conn.go:3-11` 头注释）。**inbound/outbound 是「帧角色」不是连接方向**——WebRTC 连接全双工对称，同一条 Session 同时承载两角色（一边应答对端 req、一边收集自己请求的响应），连接共享机制只此一份，拆角色时禁止复制（`conn.go:5-11`）：

- `peerjs_service.go`：PeerJSService 装配层（生命周期 Start/Close/startLoop、连接建立 connectLoop/onIncomingConnection、本地会话绑定 BindLocal，`peerjs_service.go:3-14`）
- `conn.go`：连接共享核心（帧类型、connState、bindConn 分派、二进制块路由、流控接线、去重、清理）
- `inbound.go`：入站角色（应答 verb：serveFile / serveCreate / serveUploadBegin / serveList / serveInfo / serveDelete / serveSync / servePull / serveShare，加 uploadWorker/fwdWorker）
- `outbound.go`：出站角色（FetchFromPeer / OpenStream / requestVerb / routeResponse）
- `ws_session.go` / `rtc_session.go`：方向中立传输（`Session` 抽象的两实现）
- `file_index.go`：方向中立持久化（两角色共用）

**Session 抽象**（`ws_session.go:15-29`）：`ID()/SendJSON/SendFrame/OnMessage/OnClose/Close`。两实现语义完全一致：
- `rtcSession` 适配 `*peerjs.Connection`（DataChannel；`rtc_session.go:11-14`），`ID()` = 远端 peer id、`ConnID()` = connectionId（去重键，`rtc_session.go:20-24`）；
- `WSSession` 包装本地 `/ws/peer` WebSocket（`id=="local"`，`ws_session.go:34-42`；接入点 `router/peerjs_routes.go:158-184`）。三种会话身份语义：**"local" 是浏览器直连本节点的管理通道**（`peerjs_service.go:46` 注释；`IsLocal()==true` 视为"自己"，`ws_session.go:88`、`share.go:120-123`）。

**帧协议**（`conn.go:13-26`，go↔go 与 go↔web 共用）：
- 文本帧 = JSON 控制头，二进制帧 = 数据块（pion `dc.Send` 发二进制、`SendText` 发文本，发反了头会被当数据块吞掉，约束 1）；
- **data 头与数据块必须连续**（对端 `SendFrame` 原子发送），接收端按连接级 `expect` 状态机把二进制块挂到最近的 data 头所属请求上（约束 2）；
- `reqId` 路由：浏览器端可不传（向后兼容），Go 端始终携带（UUIDv4，`outbound.go:201-202`；约束 3）。
- 请求/响应帧类型一览：请求 `req`、`create`、`upload`、`pull`、`list`、`share`、`info`、`delete`、`sync`（入站 verb，`conn.go:296-327`）；响应 `meta/data/done/err` 拉文件（`conn.go:16-19`）；`admin`、`fwd-*`、`psk-*` 见下。
- `dcReq` 带 `Trace` 回源链路（2026-08-18 防环：serveFile 回源对端时带「经过的节点链」，下游发现自己在链中即拒绝，`conn.go:41-55,78-89`）。

**bindConn 分派**（`conn.go:228-269`）：`conns[c.ID()]=c` → 去重（`dedupConn`，`conn.go:213-226`，连接级 UUID 字典序小者胜，两端保留同一条物理连接）→ 建 `connState`（`fetches/verbWaits/binCh/binDone/fwdCh`，`conn.go:239-245`）→ **先挂 OnMessage 再发 psk-auth**（顺序敏感，见 §5）→ 起 `uploadWorker` + `fwdWorker`（`conn.go:262-265`）→ `pskSendAuth`（`conn.go:268`）。`dispatchFrame`（`conn.go:278-425`）是消息泵：文本帧按 `r.Type` 分派（`req/create/upload/pull/list/share/info/delete/sync` 均 `go` 异步；`admin` 与 `fwd-data` 头**同步**处理，见 §5），二进制帧按 `fwd.pending → pendingUpload → adminUp → expect` 优先级路由（`conn.go:360-424`），IO 全部交给连接级 worker（H5）。

**连接生命周期**：
- `Start()` → `startLoop`（`peerjs_service.go:147-149,184-311`）：`NewPeer` + `Dial` 注册信令；成功后拨配置 `PEERDRIVE_PEERJS_PEERS` 与 `extraPeers()`（节点市场加入的对端，不受 MAX_PEERS 预算限制，`peerjs_service.go:225-243,72-77`）；信令断线（H7）`p.Done()` 触发整轮重连，backoff 2s↔60s 翻倍（`peerjs_service.go:285-309`）。
- 主动拨号 `connectLoop`（`peerjs_service.go:400-469`，`connecting` map 去重；`OnOpen` 后 `bindConn(newRTCSession(c))`，`conn.Done()` 后重连）；被动接受 `onIncomingConnection`（`peerjs_service.go:471-481`）。
- `Close()`（`peerjs_service.go:152-182`）：cancel → 关全部 conns → `peer.Close()` + `httpDisc.Stop()`。

**发现链**（多路并取，HTTP 优先 MQTT，`peerjs_service.go:245-283`）：信令连接成功后，`DiscoverURL` 非空 → `NewHTTPDiscovery`（announce 立即 + 30s 心跳，10s 轮询 discover，`http_discovery.go:84-100`）；否则 `MQTTEnable` → `NewMQTTDiscovery`（按 collection hash 分片 topic 订阅 + 发布，60s 重发心跳，`mqtt_discovery.go:76-99,119-172`）。两条路都回调 `onDiscoveredPeer`（`peerjs_service.go:326-341`）→ 预算检查（`discoveryDialAllowed`，本地 `"local"` 不计入，`peerjs_service.go:345-363`）→ `connectLoop`。**内容分片房间**只来自配置 `PEERDRIVE_MQTT_COLLECTIONS`（`collectionHashes`，`peerjs_service.go:383-398`，**不广播本地持有内容**）；HTTP 额外带**节点级存在房间** `PresenceRoom`（= sha256("peerdrive/presence/v1") 字面量，`http_discovery.go:16-26,365-381`），让零共享内容的节点也能互联；announce 只上报共享**数量**不报 hash（`share.go:125-149`）。

**入站数据面**：`serveFile`（`inbound.go:66-185`）——hash 必须 64hex（H1）→ ShareGate 门禁（只挡 private，`inbound.go:71-77`）→ trace 防环 → 多源路由（装配了 `FileRouter` 时：`InfoSize` 拿 meta、`OpenRange` 流式读，`inbound.go:91-104`；未装配走本地：file_index 优先 + CAS 兜底，`inbound.go:105-154`）→ 64KB 块 × `SendFrame(data头+块)` → `done` 帧。上传：`serveUploadBegin`（`inbound.go:258-320`，连接级单流 `pendingUpload`）→ 二进制块经 `uploadWorker` `WriteAt`（`inbound.go:391-422`）→ 位图全满 `Complete`（fsync + hashFile + 登记，`file_index.go:415-472`）。

**出站数据面**：`OpenStreamFrom` 发 `req` 帧返回流式 `fetchReader`（`outbound.go:200-245`：有界队列 8、`meta` 异步到达、`done`/`errCh`/空闲超时 5min/全量请求 EOF 校验 sha256，`outbound.go:247-379`）；`FetchFromPeer` 为 `[]byte` 兼容封装，带**去重窗口重试**（恒最多 2 次，`outbound.go:141-181`）；`requestVerb` 是「请求-应答」小 JSON verb（share/info）入口（15s 超时，`outbound.go:29-84`）。

**文件索引 verb**（`file_index.go:20-28` 头注释）：`create`（登记外部文件 sha256→绝对路径，不复制，`inbound.go:242-253`）、`upload`（流式）、`list/info/delete/sync`（列表/按 hash 查/逻辑删除 tombstone/seq 增量同步，`inbound.go:324-380`）。路径脱敏 `redactDisallowedPath`（`inbound.go:233-238`）。

**共享与门禁**：`share` 帧回答「显式声明了什么」（`share.go:3-26`，与 `list` 严格区分）；`SetShareProvider`/`SetShareGate` 由 main 注入 `service.NodeShare`（`share.go:68-111`）；`private` 内容在 `req` 上经 `ShareGate.AllowsDownload` 拦截，好友凭自报 peer id（仅设 PSK 时可靠，`README.md:63`）。

**PSK 门禁**（`psk.go`）：配了 `PEERDRIVE_PSK` 时连接建立后本端第一帧 `psk-auth`（`pskSendAuth`，`psk.go:57-71`）；`pskGate` 只拦 `servedVerbs`（req/create/upload/list/share/info/delete/sync/pull/fwd-*，`psk.go:44-50`）不拦应答帧；`local` 会话豁免（`psk.go:95`）；错误码 `PSK_REQUIRED`（`psk.go:39`）。

**端口转发**（`forward.go`）：`fwd-open → fwd-challenge(nonce) → fwd-auth(HMAC) → fwd-ok/err` 后建立隧道，`fwd-data` 头+块双向透传、`fwd-close` 收尾（`forward.go:10-15`）；服务端只 dial `127.0.0.1`（SSRF，`forward.go:288-289`）；规则表 `key → 允许端口[]`（配置装载 + 运行时追加，`forward.go:106-126`）。

**管理面 admin**（`admin.go`）：仅本地 WS 会话（`c.ID()=="local"`，`admin.go:136-140`）；`admin` 帧 → 构造内部 `*http.Request` → `SetAdminHandler` 包装的 gin engine → 复用全部 HTTP controller（`admin.go:13-27,317-350`；装配在 `router.go:402-415`）。JSON 响应回 `admin-resp`，二进制回 `admin-bin` 头 + 单二进制帧（≤64MB，`admin.go:43-45,341-349`）；`binary=true` 上传收集到临时文件后构造 multipart 转发（`admin.go:151-208,253-295`）。

**pull（被动出站）**（`pull.go`）：对端给 URL 让节点抓取入库——三类约束：过 PSK 门禁、SSRF 防（`guardPullURL` 拒绝非 http(s)/内网/本机/链路本地，重定向逐跳校验，`pull.go:169-236`）、大小/超时上限（`pullMaxBytes` 100MB 兜底 + `pullTimeout` 5min，`pull.go:29-39,78-83`）；结果走 `fileIndex.WriteFile` 流式落盘（`pull.go:101-135`）。

**文件索引服务**（`file_index.go`）：`FileIndexService` 只负责本地落盘与登记，持久化交给 `repository`（SQLite）；读写边界分离（登记侧 `IsPathAllowed`/`OpenAllowed` 只认 download 根，读取侧 `IsPathReadable`/`OpenReadable` 加 storage 根与 `PEERDRIVE_SHARE_DIRS`，`file_index.go:65-137`）。

## 2. 如何储存

本模块的持久化**全部委托下级或磁盘文件**，连接态纯内存。分三层：

**A. SQLite（委托 `repository`）——file_index 映射**：`FileIndexService` 不直接写 DB，调 `repository.UpsertFileIndex / GetFileIndex / ListFileIndex / DeleteFileIndex / ListFileIndexSince`（`file_index.go:16,531,547,570,575`；DDL 见 `repository/file_index_repo.go:24-36`——表 `file_index`，hash TEXT PRIMARY KEY，seq 单调游标，deleted 为 tombstone）。这就是「sha256 → 绝对路径」的持久化表（`file_index_repo.go:9-11`）。

**B. 磁盘文件（本模块直接写，不在 DB）**：
- 上传/pull 落盘目标：`uploadDir = cfg.DownloadDir`（默认 `./downloads`；为空时 `FileIndexService` 兜底 `./files`，`file_index.go:49-52`），文件名 `sanitizeName(name)`（防路径穿越，`file_index.go:638-650`），权限 `0o644`（`file_index.go:258,333`）。分片上传期间产生**磁盘上的临时目标文件**（`BeginUpload` 创建，`file_index.go:332-334`）。
- 内容寻址存储读路径：`storageDir/<hash[:2]>/<hash>`（serveFile 本地兜底打开路径，`inbound.go:107,191`；分片目录写由 service 层负责，本模块只读）。
- admin 二进制上传：`os.CreateTemp("", "peerdrive-admin-upload-*")` 系统临时目录（`admin.go:160`），用完 `cleanupTemp` 必清（`admin.go:109-129,254`）——**不落库、不常驻**。
- 委托外部落盘（不属本模块）：`share_scope.json`（`service.NodeShare`，`peerjs_routes.go:39`、`main.go:131-134` 注释）、`joined_nodes.json`（`service.NodeDirectory`，`peerjs_service.go:72-77` 注释）。

**C. 纯内存态（不持久化）**——随进程生命周期，重启即失：
- `conns map[string]Session`（key = 远端 peer id 或 `"local"`，`peerjs_service.go:46`）；
- `pending map[Session]*connState`（`peerjs_service.go:50`；connState 内含 `fetches`/`verbWaits`/`pendingUpload`/`adminUp`/`fwd`/`fwdHs`/`pskOK`，`conn.go:91-129`）；
- `connecting map[string]struct{}`（拨号去重，`peerjs_service.go:61-62`）；
- `forwardRules map[string][]int` 与 `fwNonces map[string]*fwdNonce`（`peerjs_service.go:96-99`；质询带 5min TTL，`forward.go:47-60`）；
- `HTTPDiscovery.seen map[string]bool`（`http_discovery.go:41`）、`MQTTDiscovery.announce map[string]bool`（`mqtt_discovery.go:33`）；
- `uploads map[string]*UploadSession`（`name → 分片上传会话`，`file_index.go:43-44`）——**会话对象在内存，但它指向的物理文件在磁盘**；断点续传靠「进程重启后按文件大小重建位图」（`file_index.go:348-361`），不精确由最终 sha256 校验兜底；
- 节点 peer id：`cfg.PeerJSID` 或每次生成 `peerdrive-<randHex8>`（32bit 随机，`peerjs_service.go:113-117,548-552`）——**不落盘**，重启即换 id，要跨重启保持身份必须显式设 `PEERDRIVE_PEERJS_ID`（`README.md:217`）。

**生命周期与重启影响**：以上内存态随进程退出全部消失；SQLite 表与 uploaded/created/pulled 落盘文件保留（进程重启后 `list/sync` 仍可枚举）；进行中（未 Complete）的上传会话句柄由进程退出兜底——`reapUploads` 只清 idle>10min 的会话（`file_index.go:140-172`）。生产路径 `main.go` 只见 `defer peerjsSvc.Close()`（L111），**未见 `FileIndexService.Close()` 的调用**（grep 全仓只有测试/方法定义；上传句柄清理究竟由谁保证——未核实）。

## 3. 何时储存

| 时机 | 触发点 | 写入内容 |
|---|---|---|
| 进程启动 | `NewPeerJSService` → `NewFileIndexService`（`peerjs_service.go:113-134,127`；`file_index.go:49-63`） | 初始化 `uploads` 空 map、起 `reapUploads` goroutine；main 随后 `AddReadRoot` + `Start()`（`main.go:99-112`） |
| 信令连接成功 | `startLoop` 内 `Dial` 成功后（`peerjs_service.go:225-243`） | 对每个配置/extra 对端 `connectLoop`；`conns` 尚空 |
| 发现启动 | 同上（`peerjs_service.go:257-283`） | HTTP：立即 `announce()`，之后 30s 心跳 + 10s `discover()`（`http_discovery.go:84-100`）；MQTT：`Start` 订阅 + `Announce` 发布一次 + 60s 心跳重发（`mqtt_discovery.go:76-99,119-172`）。发现组件创建后跨信令重连持续运行，不随 startLoop 重建 |
| 连接建立（open） | `OnOpen` → `bindConn`（`conn.go:228-269`；`peerjs_service.go:443-450,476-481`） | `conns[peerID]=Session`、`pending[Session]=connState`；发 `psk-auth`（本端第一帧） |
| `create` verb | `serveCreate` → `fileIndex.Create`（`inbound.go:242-253`；`file_index.go:205-244`） | SHA256+size+绝对路径 upsert 进 SQLite `file_index`（seq=MAX+1 同事务，`repository/file_index_repo.go:42-68`） |
| `upload` verb | `serveUploadBegin` 开始（`inbound.go:258-320`） | `BeginUpload`：落盘文件 + 建位图（`file_index.go:314-365`） |
| 上传分片到达 | 泵 → `uploadWorker`（`inbound.go:391-422`） | `WriteAt` 写入 + 位图置位（`file_index.go:368-390`） |
| 上传收齐 | `Complete`（末块触发，`inbound.go:405-417`；`file_index.go:415-472`） | `file.Sync()` + 全文件 `hashFile` → `repository.UpsertFileIndex`（**fsync 是本模块最慢的写路径，H5 移出消息泵的原因**）；回 `uploaded` 帧 |
| `pull` verb | `servePull` → `fetchIntoIndex`（`pull.go:43-75,101-135`） | 流式 `WriteFile` 落盘 uploadDir → 复用 `Create` 登记（超限中止不落半成品，`pull.go:137-158`） |
| `delete` verb | `serveDelete` → `DeleteFileIndex`（`inbound.go:355-362`；`file_index.go:566-571`） | tombstone upsert（deleted=1、新 seq），**不删文件** |
| `list`/`info`/`sync` | `serveList/serveInfo/serveSync`（`inbound.go:324-380`） | 只读 SQLite；`sync` 按 seq 增量（`repository/file_index_repo.go:101-117`） |
| 定时清理 | `reapUploads` 每 5 分钟 tick（`file_index.go:140-141`） | idle>10min 的未完成会话：摘句柄 + 删文件 + 摘表项；已完成会话只摘表项不删文件（`file_index.go:146-151`） |
| 连接断开 | `cleanupConn`（`conn.go:430-477`） | 删 `conns/pending` 条目、回收 fetch/fwd/上传状态、`close(binDone)` 放行 worker |
| 优雅关闭 | `PeerJSService.Close`（`peerjs_service.go:152-182`） | 关全部 conns、`peer.Close()`、`httpDisc.Stop()`、置空 peer/httpDisc/discovery |

> 数据面（`req/meta/data/done/err`）与 `share` 应答**不产生任何存储**——纯流式/内存应答。

## 4. 储存什么

**SQLite `file_index` 表**（`repository/file_index_repo.go:24-36`）：

| 列 | 类型 | 约束/说明 |
|---|---|---|
| `hash` | TEXT PRIMARY KEY | sha256 小写 64 hex（写入前 `hashutil.IsStrictSHA256` 校验，`file_index.go:544,597`；`inbound.go:66-70`） |
| `path` | TEXT NOT NULL | 绝对路径（create 必须在 allowed root 内，`file_index.go:206-208`） |
| `name` | TEXT DEFAULT '' | 登记时 `filepath.Base(path)` |
| `size` | INTEGER DEFAULT 0 | 文件字节数 |
| `deleted` | INTEGER DEFAULT 0 | 1 = tombstone（`sync` 增量暴露，`repository/file_index_repo.go:71-73`） |
| `seq` | INTEGER NOT NULL DEFAULT 0 | 单调递增同步游标（每 upsert/delete +1；同事务 MAX+1，防并发重复，`repository/file_index_repo.go:39-53`） |
| `created_at`/`updated_at` | DATETIME | CURRENT_TIMESTAMP；`idx_file_index_seq` 索引在 `seq` 上 |

**磁盘文件**：

| 条目 | 位置/命名 | 说明 |
|---|---|---|
| 上传/pull 目标文件 | `uploadDir/<sanitizeName(name)>`，0644 | uploadDir=cfg.DownloadDir（默认 `./downloads`；空兜底 `./files`，`file_index.go:49-52`）；sanitize 规则见 `file_index.go:638-650`（`..`、`\`、`/` 等 → `upload.bin`） |
| 分片上传位图 | 内存 `UploadSession.bitmap []uint64` | 64KB chunk 粒度；`i*64KB..(i+1)*64KB` 置位；`fullWords` 增量计数使 Complete O(1) 判满（`file_index.go:280-304,501-519`） |
| CAS 读路径 | `storageDir/<hash[:2]>/<hash>` | serveFile 本地兜底（`inbound.go:107,191`），本模块只读 |
| admin 上传临时文件 | 系统临时目录 `peerdrive-admin-upload-*`（`admin.go:160`） | 收齐后 multipart 转发、必清（`admin.go:109-129`） |

**发现网络负载**（无本地存储，纯线上消息）：

| 协议 | 消息 | 字段 |
|---|---|---|
| HTTP 发现 | POST `{baseURL}/discover/announce`（`http_discovery.go:103-117`） | `peerId`、`collections`、`peers`(当前直连)、`nodeType:"go-persistent"`、`loadInfo.shares:{collections,files,dirs}`（**只报数量**，`share.go:136-149`） |
| HTTP 发现 | GET `{baseURL}/discover/nodes?coll=<hash>`（`http_discovery.go:136-170`） | 在线节点列表（解码限 256KB，`http_discovery.go:149`；peerID>128 跳过，L155） |
| MQTT 发现 | topic `peerdrive/v1/{collectionHash}/nodes`（`mqtt_discovery.go:64-67`） | `{peerId, ts}`（64KB 上限、peerID≤128，`mqtt_discovery.go:104-115`）；同 peer+集合只发一次 + 60s 心跳（`mqtt_discovery.go:119-139`） |

**连接级内存态 `connState`**（`conn.go:91-129`）：

| 字段 | 内容/默认 |
|---|---|
| `fetches` | reqId → `fetchState`（队列深度 8、received 计数、total atomic[-1]，`outbound.go:160-175`） |
| `verbWaits` | reqId → chan raw JSON（15s 超时，`outbound.go:26-29,40-84`） |
| `pendingUpload` | 单槽；30s stale 自动清空（`inbound.go:302-316`） |
| `adminUp` | 单槽；30s 超时/超 size 中止（超时常量 `admin.go:49`；泵内中止逻辑 `conn.go:384-401`） |
| `fwd` / `fwdHs` | 单槽隧道 / 握手占位（`forward.go:62-87`） |
| `pskOK` | 对端已通过 PSK 校验（`conn.go:125-128`；`psk.go:85-87`） |

**关键上限常量**（跨文件汇总）：数据块 64KB（`inbound.go:25`、`file_index.go:281`）、转发块 32KB（`forward.go:50`）；上传/拉取单文件 ≤8GB（`inbound.go:260`、`outbound.go:115`、`file_index.go:315`）；pull 上限 `MaxUploadBytes` 或 100MB（`pull.go:29-32,78-83`）、5min 超时、重定向 ≤5 跳（`pull.go:35-39`）；`list` clamp 1000（`file_index.go:522-531`）、`sync` LIMIT 1000（`repository/file_index_repo.go:103`）；fetch 空闲超时 5min（`outbound.go:247-250`）；WS 读限 3×64KB、90s 无 pong 断开、写超时 15s（`ws_session.go:50-57,91-110`）；admin 二进制响应 ≤64MB（`admin.go:43-45,343-346`）；fwd nonce TTL 5min、未消费上限 64、握手 30s（`forward.go:47-52,210-221,416`）；发现拨号预算 `PEERDRIVE_MAX_PEERS`（默认 8；≤0 不限，`peerjs_service.go:357-363`、`README.md:225`）。

## 5. 边界与坑

- **帧协议 3 条硬约束不能破坏**（`conn.go:21-26`）：文本帧=头/二进制帧=块；头块原子连续（`SendFrame`）；reqId 浏览器可选、Go 端必带。破坏任一条即协议不兼容。
- **OnMessage 必须先于任何发送挂上**：`peerjs` 库在 onMessage 为 nil 时**直接丢弃**该帧，而 `dc.OnOpen` 与 `dc.OnMessage` 两条回调可并发——对端在 open 瞬间发的 `psk-auth` 可能赶在注册前被静默丢掉，门禁永远等不到出示（CI 面板 E2E 约 1/5 概率复现，`conn.go:250-259`）。
- **H1/H2 路径安全**：hash 未校验就 `req.Hash[:2]` 会越界 panic 杀进程（`inbound.go:55-58,66-70`）；file_index 命中路径必须落在可读根内，否则回退 CAS 不回传根外文件（`inbound.go:108-121`）；打开走 `SafeOpen`（os.Root）防登记后换软链的 TOCTOU（`inbound.go:122-125,216-224`）；**登记/写边界 ≠ 读取边界**——写只认 download 根（`IsPathAllowed`），读可加 storage 根与共享目录（`IsPathReadable`），两套判定不能合并（`file_index.go:65-137`）。
- **IO 必须移出消息泵（H5）**：8GB 上传的 `Complete`（fsync+hashFile，慢磁盘可达秒级）曾头-of-line 冻结整条连接所有帧；分片路由（廉价）留在泵内、落盘 IO 交给连接级 `uploadWorker`/`fwdWorker`（`conn.go:105-111`、`inbound.go:384-442`）。`admin` 的 `adminUp` 占槽与 `fwd-data` 头处理必须在泵内同步完成，否则后续二进制帧先到就丢块（`conn.go:272-277,328-335,340-349`）。
- **H6 恶意对端上限**：data 帧声明 size 无上限 → 无界分配 OOM；meta 声明 total 超 8GB 拒绝；`done.Size` 必须等于已收字节（提前 done 把截断文件当成功 = 静默数据损坏）（`outbound.go:112-115,445-478`）。
- **M6/M7 上传会话**：对端发 upload 头后不发块会永久占位（30s stale 清理，`inbound.go:302-316`）；reap 必须持 `sess.mu` 判定+标记 aborted+摘句柄，与 WriteAt/Complete 串行，否则并发分片写入已关句柄（`file_index.go:152-172,368-390`）。
- **同 peer 双连接去重（2026-08-19）**：双向互拨/重连竞态会留孤儿连接（pending 残留、worker 泄漏）；去重决策必须两端一致（连接级 UUID 字典序小者胜，`conn.go:196-226`）；淘汰连接的清理带 `s.conns[c.ID()] == c` 值相等守卫；`local` 不去重（多标签页各一条，`conn.go:204-205`）。`FetchFromPeer` 为此带一次性重试（`outbound.go:141-181`）。
- **PSK 语义边界**：只拦「对端要我干活」的 `servedVerbs`，**绝不拦应答帧**（否则对端开放、本端配了 PSK 时会自伤，`psk.go:24-30,44-50`）；`local` 豁免（`psk.go:95`）；密钥明文传但 DataChannel 强制 DTLS 加密（`psk.go:16-18`）；校验失败**不关连接**，让对端能重发（`psk.go:73-75`）。
- **forward 安全**：key 即凭证（配置应 chmod 600，`forward.go:18-20`）；nonce 一次性（取出即标 used + 5min 过期）防重放（`forward.go:235-242`）；端口 ∉ 白名单 → fwd-err 不泄露规则（`forward.go:269-280`）；只 dial `127.0.0.1` 防 SSRF（`forward.go:288-289`）；握手不占隧道槽、隧道单槽（`forward.go:24-26,281-303`）。
- **pull 是被动出站的风险面**：三重约束缺一不可（PSK + SSRF 逐跳重定向校验 + 大小/超时上限，`pull.go:8-12,108-116`）；`guardPullURL` 对字面 IP 和 DNS 解析结果**逐个**判定（防 169.254.169.254 / IPv4-mapped IPv6 `::ffff:127.0.0.1`，`pull.go:195-235`）；超限必须报错而非 `io.LimitReader` 静默截断（否则截断文件被当成成功入库，`pull.go:137-158`）。
- **发现语义**：本地持有的合集 hash **不广播**（`collectionHashes` 只取配置，广播=公开「本节点持有什么」，`peerjs_service.go:383-398`）；announce 只报共享数量不报 hash（`share.go:125-135`）；存在房间只用于 HTTP 发现——公共 MQTT broker 上开全局存在房等于向公网广播本节点（`peerjs_service.go:365-381`）。
- **Windows 专属坑**：句柄必须先关再删（`cleanupTemp`、`Complete` 锁外关句柄、`reapUploads` 摘句柄，`admin.go:109-116`、`file_index.go:410-414,468-471`）；`sanitizeName` 必须同时挡 `/` 与 `\`（`filepath.Base("/")` 在 Windows 返回 `\`，`file_index.go:643-649`）。
- **WS 会话保活**：无 `SetReadLimit` 会被恶意大帧打内存、无 ping/pong 会让死连接（标签页挂掉）常驻 + pending fetch 挂 5 分钟（M5，`ws_session.go:45-57`）；本地会话 Origin 白名单 + loopback 兜底（`router/peerjs_routes.go:159-176`）。
- **fetchReader 细节**：空闲定时器只建一次复用（每块 `time.After` = 8GB 13 万个 timer 常驻，`outbound.go:290-304`）；done 后 q 仍有块需逐块取、循环内会被覆盖丢块（`outbound.go:324-339`）；EOF 前非阻塞查 errCh 防 err 被 done 掩盖（`outbound.go:340-348`）；全量请求（offset==0 && size<0）EOF 时校验 sha256（`outbound.go:242,364-379`）。
- **空文件是合法内容寻址值**：sha256(空) 合法；`size==0` 上传直接完成避免占槽（`inbound.go:259,274-292`），拉取下不做空文件特判（`outbound.go:390-397`）。

## 6. 对外连接

- [frontend ↔ backend 本地 WS 会话](../connections/01-frontend-backend.md)：浏览器经 `GET /ws/peer` 升级为 `WSSession`（id="local"）→ `BindLocal` 复用同一 `Session` 接口与帧协议；`admin`/`req`/`upload` 等 verb 均可走本地会话，`admin` 只放行该会话。
- [router ↔ controller](../connections/02-router-controller.md)：admin 管理面内部转发——`admin` 帧构造内部请求注入 gin engine（`router.go:402-415` 经 `SetAdminHandler`），复用全部 HTTP controller；转发时 `RemoteAddr` 标 `127.0.0.1:0` 避开限流误伤。
- [router ↔ source](../connections/05-router-source.md)：`serveFile` 多源路由 `FileRouter` = `source.Manager`（本地→对端→URL 模板，注入点 `main.go:217-233`）；读取边界判定与 `source.LocalSource.resolvePath` 收敛到 `FileIndexService` 同一份（`inbound.go:62-65`）。
- [service ↔ transport](../connections/06-service-transport.md)：`service.NodeShare` 注入 shareProvider/shareGate（`main.go:156-158`）；`service.NodeDirectory` 注入 `extraPeers`/`EnsureConnection`（`main.go:116-124`）；`service.PeerPuller` 消费 `OpenStream`/`FetchFromPeer` 做跨节点保存（`main.go:164-182`）。
- [transport ↔ PeerJS](../connections/07-transport-peerjs.md)：本模块经 `back/peerjs`（模块 10）连接信令并承载 DataChannel；`rtcSession` 把 `*peerjs.Connection` 适配为 `Session`（`rtc_session.go:11-18`）；数据面帧的「文本头+二进制块、原子连续」由 peerjs `SendFrame` 保证。
- [transport ↔ signalserver](../connections/08-transport-signalserver.md)：信令 WS（`/peerjs?key=&id=&token=`，OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE 流转）只转发 SDP/ICE；另走自托管服务器的发现 API（`POST /discover/announce`、`GET /discover/nodes?coll=`，`http_discovery.go:103-170`）——发现链 HTTP 优先、MQTT 兜底（`peerjs_service.go:245-283`）。
- [transport ↔ storage](../connections/11-transport-storage.md)：file_index 映射持久化委托 `repository`（SQLite `file_index` 表，`file_index_repo.go:24-36`）；上传/pull 落盘 `uploadDir`（默认 `./downloads`）与 `storageDir` CAS 读；共享范围 `share_scope.json` 由 `service.NodeShare` 落盘、transport 只读快照。
- [frontend ↔ signalserver](../connections/12-frontend-signalserver.md)：浏览器端 peerjs 以同一协议连同一信令并向节点发起连接——`onIncomingConnection`（`peerjs_service.go:476-481`）是被动接受方。
- [media-node ↔ ech](../connections/13-media-node-ech.md)：`back/cmd/media-node` 与验证客户端使用同一 `go-peerjs` 库与同族帧协议，但走独立帧族（url/keepalive），与本模块无数据面交互——仅共享库与协议风格。

> 与本模块无直接关系（绕经 service/repository/router 层）：[controller ↔ service](../connections/03-controller-service.md)、[service ↔ repository](../connections/04-service-repository.md)、[controller ↔ downloader](../connections/09-controller-downloader.md)、[controller ↔ storage](../connections/10-controller-storage.md)。
>
> 模块文档交叉引用：`10-peerjs.md`（信令/DataChannel 传输原语，本模块的底层传输依赖；其中 §2/§3 与本文「内存态不持久化」结论一致）。