# 模块 11：signalserver 信令与发现服务

- **代码位置**：`back/signalserver`（独立 go.mod，module `github.com/Hana-ame/go-peersignal`，见 `back/signalserver/go.mod:1`；主仓库在 `back/go.mod:35` require v0.0.0 并在 `back/go.mod:164` 以 `replace github.com/Hana-ame/go-peerserver => ./signalserver` 指向本地目录）
- **功能一句话**：自托管 PeerJS 信令服务器（节点注册、OFFER/ANSWER/CANDIDATE 按 dst 转发、离线入队、心跳保活、ID 分配）+ 内置房间发现（节点 announce 关注的集合、HTTP 查询在线节点），替代公共信令（0.peerjs.com）与公共 MQTT broker；**纯内存态，不持久化任何数据**。
- **依赖**：仅 `github.com/gorilla/websocket v1.5.3`（WS 升级与读写，`back/signalserver/go.mod:5`）；标准库 `crypto/rand`（随机 id，`back/signalserver/signalserver.go:19,790-798`）、`embed`（内嵌 dashboard.html，`back/signalserver/signalserver.go:20,32-33`）、`encoding/json`、`net/http`、`sync`/`sync/atomic`、`time`；测试用 `stretchr/testify`（`back/signalserver/go.mod:7`）。不依赖 gin / sqlite / 数据库。
- **被依赖**：
  - 独立二进制 `cmd/peersignal`（`back/signalserver/cmd/peersignal/main.go:22-52` 拼装 Server + 路由注册，README 部署说明见 `back/signalserver/README.md:10-22`）；
  - 主仓库集成测试以 httptest 内嵌本包做「内存信令」（`back/test/integration/integration_test.go:32,43`、`back/test/integration/file_lifecycle_test.go:19,33`）——主仓库生产代码**不** import 本包（全仓库检索仅此两处）；
  - 节点端 `transport.HTTPDiscovery` 消费 `/discover/announce|nodes`（`back/internal/transport/http_discovery.go:84-100,103-117,136-170`）；
  - 浏览器端 peerjs 客户端直连 `/peerjs` WS 与 `/peerjs/id`（详见 12-frontend-signalserver）。

## 1. 逻辑

**模块定位**：包注释明确「自托管 PeerJS 信令服务器（兼容 peerjs-server 协议子集）+ 内置房间发现（替代公共信令 + 公共 MQTT broker）」，职责二分为信令与发现（`back/signalserver/signalserver.go:1-16`）。协议对齐 peers/peerjs-server 的 `webSocketServer` / `messageHandler`：WS URL 形如 `/{path}peerjs?key=&id=&token=`；消息 `{type, src, dst, payload}` 且**服务端覆盖 src**；dst 在线转发、不在线入队（LEAVE/EXPIRE 不入队）；OPEN/ID-TAKEN/ERROR 控制消息；客户端每 5s 发 HEARTBEAT（`back/signalserver/signalserver.go:10-15`）。

**核心类型**：

- `Server`：全部状态的容器——`key/path/queueTTL/heartbeatTTL/tokenWhitelist` 配置项 + `startedAt`（启动时间）、`msgCount`（原子转发计数）+ 六张受 `mu sync.Mutex` 保护的内存表（`clients/queues/disc/peerLinks/peerColls/peerStats`）（`back/signalserver/signalserver.go:35-52`）。
- `client`：一条在线信令连接——`id/token/conn`、`sendMu`（gorilla 不允许并发写，写串行化）、`last`（最后心跳）（`back/signalserver/signalserver.go:162-169`）。
- `queuedMsg`：离线队列条目——`msg Message` + `expire time.Time`（入队时带过期时间）（`back/signalserver/signalserver.go:171-175`）。
- `Message`：与 peerjs 客户端协议一致的 `{Type,Src,Dst,Payload}`，Payload 为任意 JSON（`back/signalserver/signalserver.go:177-183`）。
- `NodeInfo` / `GraphLink` / `PeerStats`：发现 API 的响应与内部统计结构（`back/signalserver/signalserver.go:561-670`）。
- `Option`：函数式配置项，唯一实现 `WithTokenWhitelist`（tokens 非空才生效，`back/signalserver/signalserver.go:54-75`）。

**信令主流程**（`HandleWS`，`back/signalserver/signalserver.go:246-296`）：

1. 参数校验：`id/token/key` 任一为空 → HTTP 400；`key != s.key` → 400「Invalid key provided」；token 白名单非空且 token 不在名单 → 400「Invalid token provided」（空名单 = 不限制，默认，`signalserver.go:249-263`）。
2. 升级 WS（`CheckOrigin` 恒放行——自托管，由调用方配置访问控制，`signalserver.go:265-271`）；`SetReadLimit(40<<10)`（信令消息很小，40KB 够，防超大 payload）并设 60s 读超时（`signalserver.go:272-275`）。
3. ID 占用：同 id 已有连接时 token 匹配 → 关旧连接接管；token 不匹配 → 回 `ID-TAKEN` 并关闭（`signalserver.go:277-291`；行为测试 `signalserver_test.go:122-136`）。
4. 建 `client` 入表、回 `OPEN`、`flushQueue` 补发离线消息、起 `readLoop`（`signalserver.go:292-295`）。
5. `readLoop`：读 JSON → **`m.Src = cl.id` 覆盖 src** → 更新 `cl.last`（收到消息即活跃）→ 续 60s 读超时 → `route(m)`；读错误即清理（`signalserver.go:299-317`）。
6. `route`：dst 在线 → **解锁后再写**（`send` 内部带 10s 写超时，持锁会卡死整个路由）→ 成功则 `msgCount++`；写失败走 `handleDeadDst`；dst 不在线且非 `LEAVE/EXPIRE`、dst 非空 → 入队（`signalserver.go:319-356`）。
7. 死连接处理 `handleDeadDst`：send 失败但还在表里 → 摘除表项、清理该 id 在 disc/peerLinks/peerStats/peerColls 的残留、关连接、向其他在线节点与消息发起方广播 `LEAVE`（修复：旧实现 `_ = dst.send(m)` 静默丢弃，半开连接会把 OFFER/ANSWER/CANDIDATE 吞掉导致发起方永久卡握手，`signalserver.go:319-328,360-402`）。
8. 断开清理 `removeClient`（readLoop defer）：从 `clients` 摘除 → 清理全部发现残留 → 向其他节点广播 `LEAVE`（`signalserver.go:418-447`；行为测试 `signalserver_test.go:105-117`）。

**发现主流程**：

- `HandleAnnounce`（POST /discover/announce）：与信令连接**解耦**（节点可经任意 HTTP 入口上报）——body 限 8KB（M15：防无界 decode）、`collections` 上限 64、逐项 trim 去空串规范化；写 four 张表（`disc` 的 collection→peerId→lastSeen、`peerColls` 的 peerId→collections、`peerStats` 的 peerId→数据、`peerLinks` 的 peerId→邻居→lastSeen 用于 graph 边）；collections 变更立即从旧集合移除节点（不等 TTL）；`peers` 恒覆盖（空也清空旧边，防 graph 残留）（`back/signalserver/signalserver.go:449-528`）。
- `HandleLeave`（POST /discover/leave）：从所有 collection、peerStats、peerColls、peerLinks（含他人邻居列表中的该节点）移除（`back/signalserver/signalserver.go:531-559`）。
- `HandleNodes`（GET /discover/nodes?coll=&type=）：空 `coll` 遍历全部 collection 去重返回；心跳过期（`heartbeatTTL`）剔除；`type` 过滤节点类型（被过滤节点不进 `seen`，允许在其他 collection 再被检查）；graph 边只保留**两端都活跃**的边，按 id 字典序去重成一条（key = `a+"\x00"+b`）（`back/signalserver/signalserver.go:568-638`）。
- `HandleStatus`（GET /status）：服务器状态快照（clients/queues/totalQueued/discovered/nodes/links/msgCount/uptime），dashboard 轮询用（`back/signalserver/signalserver.go:672-736`）。
- `HandleDashboard`（GET /）：返回经 `//go:embed` 内嵌的 dashboard.html（非 `/` 404；`Cache-Control: no-cache`；HTML 每 2s 轮询 /status 画 graph，`back/signalserver/signalserver.go:738-752`、`back/signalserver/dashboard.html:162-193`）。
- `HandleID`（GET /peerjs/id）：返回 16 字符小写字母数字随机 id（xhr 借 ID 用，`back/signalserver/signalserver.go:236-243,789-798`）。
- 所有 REST 端点经 `handleCORS` 写 `Access-Control-Allow-Origin: *` 并短路 OPTIONS 预检（204）；WS 握手不走 CORS（`back/signalserver/signalserver.go:209-234`）。

**后台任务**：`Start()` 起 goroutine，每 30s 依次 `sweepQueues()`（清过期队列项与空队列）与 `sweepDiscovery()`（清心跳过期节点，并同步清理其 peerLinks/peerStats/peerColls 残留；dirties 出发点：过期清理原只在 flushQueue/dst 上线时做，dst 永不连接则过期堆积）（`back/signalserver/signalserver.go:82-160`）。

**生命周期**：`NewServer(key, opts...)`（默认 key `"peerjs"`、queueTTL 30s、heartbeatTTL 90s、空表初始化，`signalserver.go:186-207`）→ `Start()` → main 里 `http.ListenAndServe[AndTLS]` 阻塞（`cmd/peersignal/main.go:34-51,60-72`）；**无优雅关闭代码路径**（无 close hook，进程退出即丢一切内存态；部署层 SIGTERM 处理未核实）。进程终止 → 在线表/队列/发现全空，节点需重新连 WS + 重新 announce。

## 2. 如何储存

**不持久化——纯进程内内存。** 本模块不写磁盘 / DB / 任何文件，也没有委托给 repository / storage 等下级模块（它是发现链路的终点服务）；唯一"文件"是编译期内嵌进二进制的 `dashboard.html`（`//go:embed`，`back/signalserver/signalserver.go:32-33`），运行期只读不写。

**内存态构成**（全部在 `Server` 结构体内，`back/signalserver/signalserver.go:35-52`，`mu` 互斥保护）：

| 字段 | 类型 | 含义 |
|---|---|---|
| `clients` | `map[string]*client` | id → 在线信令连接（`client`：id/token/conn/sendMu/last，`signalserver.go:162-169`） |
| `queues` | `map[string][]queuedMsg` | dst → 待转发离线队列（`queuedMsg{msg, expire}`，`signalserver.go:171-175`） |
| `disc` | `map[string]map[string]time.Time` | collection → peerId → lastSeen（发现核心） |
| `peerLinks` | `map[string]map[string]time.Time` | peerId → 邻居 peerId → lastSeen（graph 边） |
| `peerColls` | `map[string][]string` | peerId → collections（规范化后副本） |
| `peerStats` | `map[string]*PeerStats` | peerId → `{NodeType,Uptime,LoadInfo,LastSeen}`（NodeType/LoadInfo 经 announce 写入；Uptime 恒缺省，见 §5） |
| `msgCount` | `int64`（atomic） | 总转发消息数（`signalserver.go:43`） |

**生命周期与重启影响**：

- 六张表随连接 / announce / 断开 / sweeper 增删，进程退出即全部消失；
- `queues` 条目带 30s 过期（`queueTTL`），`disc/peerLinks/peerStats/peerColls` 的 lastSeen 以 `heartbeatTTL`（90s）为准，由 30s sweeper 与查询时的 cutoff 双重剔除；
- **进程重启后**：所有房间、在线节点、队列、graph 全空。恢复靠客户端行为——节点端 `HTTPDiscovery` 每 30s 发一次 announce（`back/internal/transport/http_discovery.go:87,94-95`）、每 10s 轮询 `/discover/nodes`（`http_discovery.go:86,92-93`）；信令连接由 peerjs 客户端重连（客户端侧行为，见 `back/peerjs`）。节点的 peer id 是否跨重启保持取决于客户端配置（默认每次启动新生成，见模块 10 文档 `back/peerjs/peer.go` 与 `back/internal/transport/peerjs_service.go:113-117`），signalserver 不保存任何身份。
- 服务自身身份（`key`、`tokenWhitelist`、TLS）来自 `cmd/peersignal` 的命令行参数（`cmd/peersignal/main.go:23-27`），不是"储存"，是启动时配置。

## 3. 何时储存

本模块的"储存"全部是内存写入/清理触发点（无磁盘时机）：

| 时机 | 触发点 | 写入/清理内容 |
|---|---|---|
| 进程启动 | `NewServer`（`back/signalserver/signalserver.go:186-207`） | 初始化空 `clients/queues/disc/peerLinks/peerColls/peerStats`、`startedAt`、默认 TTL |
| 进程启动 | `Start()`（`signalserver.go:84-93`） | 起 30s 周期自清理 goroutine（sweepQueues+sweepDiscovery） |
| WS 连接建立 | `HandleWS`（`signalserver.go:277-295`） | `clients[id] = cl` 写入；同 id token 匹配先 `closeConn` 旧连接再接管；`flushQueue` 立即删队列并补发未过期消息（`signalserver.go:404-416`） |
| 每条消息 | `readLoop`（`signalserver.go:309-314`） | 更新 `cl.last`（心跳）、续 60s 读超时 |
| 消息路由 | `route` 转发成功（`signalserver.go:334-336`） | `msgCount` +1 |
| 消息路由 | `route` dst 不在线且非 LEAVE/EXPIRE/空 dst（`signalserver.go:343-355`） | `queues[dst]` append `{msg, now+queueTTL}`；超 `maxQueuedPerDst` 丢最旧 |
| 发送失败 | `handleDeadDst`（`signalserver.go:360-402`） | 摘 clients 表项、清理该 id 在 disc/peerLinks/peerStats/peerColls 残留、广播 LEAVE |
| 连接断开 | `readLoop` defer → `removeClient`（`signalserver.go:300-303,419-447`） | 删 `clients` + 全部发现残留 + 广播 LEAVE |
| 节点上报 | `HandleAnnounce`（`signalserver.go:483-525`） | 写 `disc`（lastSeen=now）、删旧集合、写 `peerStats`、覆盖 `peerColls`、覆盖 `peerLinks`（客户端每 30s 心跳触发，`http_discovery.go:87,94-95,103-117`） |
| 节点下线 | `HandleLeave`（`signalserver.go:543-555`） | 全部表移除该节点（含他人邻居边） |
| 定时清理 | `sweepQueues`（`signalserver.go:95-112`） | 删过期队列项；空队列删 key |
| 定时清理 | `sweepDiscovery`（`signalserver.go:114-160`） | 按 `heartbeatTTL` 删过期节点及 peerLinks/peerStats/peerColls 残留 |
| 查询时剔除 | `HandleNodes`/`HandleStatus` 的 cutoff 过滤（`signalserver.go:576,687`） | 只读，不对内存写入 |
| 优雅关闭 | **无代码路径** —— `cmd/peersignal/main.go:49-51` 只有阻塞 `Serve`；无 close hook（部署层 SIGTERM 是否做优雅处理未核实） | — |

## 4. 储存什么

内存条目清单（含默认值与关键约束，代码位置同 §2 表）：

**`clients`**（id → `*client`）：

| 字段 | 说明 / 约束 |
|---|---|
| 键 `id` | 客户端自选或来自 `HandleID`；`HandleWS` 强制 id/token/key 均非空（`signalserver.go:250-253`），id 长度无服务端校验（未核实客户端上限） |
| `token` | `HandleWS` 校验存在；白名单启用时必须在 `tokenWhitelist` 内（`signalserver.go:258-263`） |
| `conn` / `sendMu` | WS 连接与写锁；写超时 10s（`signalserver.go:774-779`） |
| `last` | 最后心跳；服务端**不主动踢**过期客户端（清理只发生在断开/写失败/ID 顶替时；查询不读 last，发现侧才用） |

**`queues`**（dst → `[]queuedMsg`）：每条目 `{msg Message, expire = 入队时间 + queueTTL(30s)}`（`signalserver.go:193,350`）；每 dst 上限 `maxQueuedPerDst = 100`，超限丢最旧（H3 修复：原无上限，恶意客户端对任意随机 ID 发 OFFER 可打爆内存；信令消息过期即失效，丢旧比丢新合理，`signalserver.go:77-80,347-355`）；`LEAVE/EXPIRE` 与空 dst 不入队（`signalserver.go:343`）。

**`disc`**（collection → peerId → lastSeen）：lastSeen 由 announce/心跳刷新（`signalserver.go:493`）；集合键为规范化后的 collection 字符串（trim + 去空串，`signalserver.go:474-481`）；单次 announce 的 collections 上限 64（`maxCollectionsPerAnnounce`，`signalserver.go:469`）；body 上限 8KB（`signalserver.go:457`）；条目在 lastSeen 超过 `heartbeatTTL`（90s，`signalserver.go:194`）后被 sweeper/查询剔除。

**`peerLinks`**（peerId → 邻居 → lastSeen）：来自 announce 的 `peers` 数组——trim、去空、**忽略自身 id**（`nid != body.PeerID`，`signalserver.go:517-524`；测试 `signalserver_test.go:293-314`）；恒覆盖（空 peers 也清空旧边，`signalserver.go:515-524`；测试 `signalserver_test.go:238-261`）；输出时为 `GraphLink{source,target,lastSeen(Unix 秒)}`，仅保留两端都活跃的边、按字典序去重（`signalserver.go:612-634`）。

**`peerColls`**（peerId → `[]string`）：与 disc 同源的规范化集合列表（「disc 与 peerColls 使用同一份数据」，`signalserver.go:474`）；恒覆盖（空也清空旧值，防节点清空集合后旧集合残留，`signalserver.go:513-514`）。

**`peerStats`**（peerId → `*PeerStats`）：announce 写入 `NodeType/LastSeen/LoadInfo`（`signalserver.go:505-512`）；`Uptime` 字段定义于 `signalserver.go:641-646`，但 announce body 无 uptime 字段、`HandleAnnounce` 从不赋值（见 §5 未核实）；`LastSeen` 打 `json:"-"` 不外发。

**标量**：`msgCount`（原子转发计数，`signalserver.go:43`）；`startedAt` → `/status` 的 `uptimeSec/uptimeStr`（`formatDuration`：`<1m` 显示 `%.0fs`、`<1h` `%.1fm`、`<24h` `%.1fh`、否则 `%.1fd`，`signalserver.go:721-726,754-767`）。

**控制常量**（`signalserver.go:80,193-194,265-275` 等）：`maxQueuedPerDst=100`；`queueTTL=30s`；`heartbeatTTL=90s`；WS 读上限 40KB；读超时 60s；写超时 10s；announce body 8KB；每 announce 集合数 ≤64；`randomID` 16 位小写字母数字（字符集 `abcdefghijklmnopqrstuvwxyz0123456789`，`signalserver.go:790-798`）。

## 5. 边界与坑

- **H3：离线队列无上限 → OOM**：原实现在 dst 永不连接时队列无限增长；现每 dst 上限 100 条、丢最旧，并有 30s sweeper 兜底清理过期项（`signalserver.go:77-80,347-355,82-93`；审阅记录见 `doc/archive/TRANSPORT-REVIEW2-2026-08-16.md:62`）。
- **send 失败曾静默吞消息**：目标 socket 半开但未摘表时 OFFER/ANSWER/CANDIDATE 被丢、发起方永久卡握手；现 `handleDeadDst` 摘除死连接 + 补发 LEAVE（`signalserver.go:319-328,360-402`；测试 `signalserver_test.go:554-606`）。
- **发送必须在出锁后执行**：`send` 的 `WriteJSON` 带 10s 写超时，一个慢/死客户端持 `s.mu` 会卡死整个服务路由（`signalserver.go:327-334`）。
- **token 白名单**：token 原本只是 ID 占用保护，任意客户端可自定 token 注册任意 ID 冒充节点收信令；`WithTokenWhitelist` 让自托管只信任已知节点（`signalserver.go:57-64`；测试 `signalserver_test.go:152-179`）。**发现端点保持公开**——「发现的目的就是让任何人找到节点，白名单只约束信令面」（`signalserver.go:62-64`）。
- **ID 占用**：同 id token 匹配 → 接管；不匹配 → `ID-TAKEN` + 关闭（`signalserver.go:277-287`；测试 `signalserver_test.go:119-136`）。
- **读限制**：40KB 读上限防超大 SDP/ICE payload；60s 读超时靠 HEARTBEAT/消息续期，断连客户端不再占资源（`signalserver.go:272-275,312-313`）。
- **CORS 全放开是有意为之**：公共面板（`file://` 时 origin 为 `null`）要直连 `GET /peerjs/id` 与发现 API，同源策略会吞掉响应且只表现为含混的 `server-error`；这些接口本来就是公开信息（`signalserver.go:209-218`；测试 `signalserver_test.go:608-643`）。
- **TLS 半配置必须报错**：只给 cert 或只给 key 时 `Serve` 直接返回错误（不静默降级 http，否则 HTTPS 面板被混合内容拦截而服务端看似正常，极难排查）（`cmd/peersignal/main.go:65-69`；测试 `main_test.go:71-83`）。
- **Gorilla 禁止并发写**：`client.sendMu` 串行化写；`closeConn` 也持同一锁（`signalserver.go:774-785`）。
- **graph 边纪律**：仅两端都活跃的边才输出、按字典序去重（`signalserver.go:612-634,696-718`）；`handleDeadDst`/`removeClient`/`sweepDiscovery` 三处都清理 peerLinks 残留（`signalserver.go:373-385,431-442,135-159`）。
- **janitor 必要性**：过期清理原只在 flushQueue（dst 上线）时做，dst 永不连接则过期消息堆积；sweeper 每 30s 全表扫（`signalserver.go:82-93`；测试 `signalserver_test.go:426-454`）。
- **默认 key/ttl**：`NewServer("")` 回落 key=`peerjs`（`signalserver.go:187-189`）；queueTTL 30s / heartbeatTTL 90s 为默认值，无配置项可改（未核实是否有隐藏 flag）。
- **未核实 1**：`PeerStats.Uptime`（`signalserver.go:643`）在信号服务器侧从未被赋值（announce body 无 uptime 字段，`signalserver.go:458-464`），`/discover/nodes` 与 `/status` 里 `uptime` 恒省略（`omitempty`）；dashboard 因此显示 `-`（`dashboard.html:182`）。疑似保留字段或由 wintools 侧实现填充。
- **未核实 2**：`back/signalserver/README.md:13` 的构建路径写 `./cmd/peerserver/`，实际目录是 `back/signalserver/cmd/peersignal`（教程 `doc/tutorial/appendix-build-from-source.md:62` 用 `./cmd/peersignal` 佐证），README 疑似过期。
- **未核实 3**：进程无优雅关闭钩子（main 仅阻塞 Serve，`cmd/peersignal/main.go:49-51`）；重启即丢全部内存态这一影响由客户端每 30s announce / 重新连 WS 自愈，部署层（systemd/nginx）是否做 SIGTERM 优雅处理未在代码内体现。

## 6. 对外连接

- [transport ↔ signalserver](../connections/08-transport-signalserver.md)：**最核心的一条**——节点端 `transport.HTTPDiscovery`（`back/internal/transport/http_discovery.go`）注册信令后用 `POST /discover/announce`（30s 心跳）与 `GET /discover/nodes?coll=`（10s 轮询）消费发现面；peerjs 库（模块 10）经 WS `/peerjs?key=&id=&token=` 消费信令面。方向：transport/peerjs 客户端 → 本服务（信令 + 发现），响应（节点列表 → `onPeer` 互联）回流 transport。
- [transport ↔ peerjs](../connections/07-transport-peerjs.md)：peerjs 库作为信令客户端与**本服务**对端互通（OFFER/ANSWER/CANDIDATE/LEAVE/HEARTBEAT 双向流转；服务端覆盖 src 是协议要求，`signalserver.go:12,309`）；本服务只转发 SDP/ICE、不碰数据面。
- [frontend ↔ signalserver](../connections/12-frontend-signalserver.md)：浏览器面板经 `GET /peerjs/id` 借临时 id、以同一 PeerJS 协议连本服务 WS 并按 peer id 拨号；同时经 `GET /discover/nodes` 做房间发现（`packages/peerdrive-client`）。其在别的源上，故所有 REST 端点跨域放开（§5 CORS）。
- [media-node ↔ ech](../connections/13-media-node-ech.md)：`back/cmd/media-node`（经模块 10 的 peerjs 库）注册到本服务承载浏览器媒体 DataChannel——本服务是其信令通道，不参与帧协议。
- 关联说明：[frontend ↔ backend](../connections/01-frontend-backend.md)（本地 WS 会话）与本模块**不直接相关**——本地会话走 router 的 WSSession，不经本信令；本模块只服务节点间（peer↔peer）与浏览器（panel→node）的信令/发现。
- 模块内交叉引用：[01-config](01-config.md)——`PEERDRIVE_PEERJS_HOST/PORT/KEY/DISCOVER_URL` 等决定节点默认指向的本服务实例（`back/internal/config/config.go:54-58,76`）；[10-peerjs](10-peerjs.md)——信令客户端侧协议与默认公共实例 `peersignal.moonchan.xyz`（本服务的线上部署，`back/signalserver/README.md:68`）。