# 重构记录 (REFACTOR)

> 2026-08-13 · 项目从「封印」解锁后的架构重构记录。所有新做的东西、决策、坑都记在这里。
> 供 agent 后续工作时快速对齐上下文：**先读本文档，再动代码**。

---

## 1. 为什么重构

原 peerdrive 是 libp2p + BT DHT + IPFS + WebDAV + 自建信令的巨型单体，review 发现：
- **服务无法启动**：gin 路由重复注册直接 panic
- 认证形同虚设、任意文件读写删、P2P 路径穿越、CSWSH、远端崩溃 DoS 等安全洞
- 大量死代码（前端 ~4000 行）、全局单例、数据竞争

核心决策：**互联层整体换成 PeerJS 公共云信令 + WebRTC DataChannel**（与 [hana-link](https://github.com/Hana-ame/hana-link) 同一设计哲学：传输格式无关、上层定义 verb）。BT/libp2p 栈降级为 legacy。

## 2. 新架构总览

```
浏览器(peerjs) ─┐
               ├── PeerJS 公共云信令 (0.peerjs.com) ──→ WebRTC DataChannel 直连
Go 节点        ─┘
   │
   └── MQTT 分片房间发现 (peerdrive/v1/{collectionHash}/nodes) ← 只交换 peerId
   └── 静态配置 (PEERDRIVE_PEERJS_PEERS)
   └── (预留) DHT bep44 发现

Go 节点职责：常驻在线、内容寻址存储（sha256）、双向文件服务（serve + fetch）
浏览器职责：通过 peerjs 直连节点拉文件；节点↔节点互联共享
```

## 3. 已完成变更

### 3.1 路由修复（救活服务）
`internal/router/router.go` — 删掉全部 legacy redirect（`/anon/*`、`/actions/*`、`/files`），
冲突路由合并为分派器（`collection_dispatch.go`）：
- `POST /collections` → `dispatchCreateCollection`（body 带 username 走用户体系，否则匿名）
- `GET /collections/:id` → `dispatchGetCollection`（64hex 为匿名 hash，否则 username）
- `GET /collections/:id/*filepath` → `dispatchGetTree`（gin 不允许 `:param` 与 `*wildcard` 共存，
  用户体系深层 GET 全并入此路由）
- `withParams` 用 `c.Copy()` + 追加 Params 补齐参数名，controller 零改动

### 3.2 peerjs 独立模块（新）
位置：`back/peerjs/`，模块名 `github.com/Hana-ame/go-peerjs`（主 go.mod 用 replace 引用）。
定位：**传输原语（信令 + 数据面），业务 verb 由上层定义**。

```
peerjs/
├── message.go      MessageType(开放 string)/Message/Options/Offer/Answer/CandidatePayload
├── signaller.go    Signaller 接口（信令抽象，PeerJS 公共云为默认实现）
├── transport.go    DataChannel 接口（传输抽象）+ Frame{IsText,Data} + pion 适配层
├── peer.go         Peer 顶层（Dial/Connect/OnConnection/路由）+ PeerJS 云信令实现
├── connection.go   Connection（SDP 交换/ICE 转发/文本二进制帧/原子帧）
└── README.md       模块级 Function Set 文档
```

扩展点（后续扩展不动核心）：
- 换信令：`NewPeerWithSignaller()` 注入自定义 `Signaller`
- 换传输：实现 `DataChannel` 接口
- 加 verb：`MessageType`/`Frame` 开放类型

### 3.3 PeerJS 文件服务（新）
`internal/service/peerjs_service.go` — 双向文件服务：
- **被动**：浏览器/节点连接本节点 → `serveFile`（hash 校验 64hex → 分块发送）
- **主动**：`FetchFromPeer(peerID, hash)` → `requestFile`（reqId 路由状态机收集响应）
- 节点互联：`PEERDRIVE_PEERJS_PEERS` 静态配置，`connectLoop` 断线自动重连
- HTTP：`GET /peerjs/node`（发现+对端列表）、`POST /peerjs/fetch`（拉取验证）

### 3.4 MQTT 分片房间发现（新）
`internal/service/mqtt_discovery.go` — topic `peerdrive/v1/{collectionHash}/nodes`：
- **分片模式**（按集合 hash 分片，公共 broker 无规模上限；全局单 topic fan-out 是瓶颈）
- announce 幂等去重 + 60s 心跳；paho 断线自动重订阅（SetOnConnectHandler）
- 发现只交换 `{peerId, ts}`，实际传输仍走 WebRTC 直连
- 配置：`PEERDRIVE_MQTT_ENABLE/BROKER/TOPIC_PREFIX/COLLECTIONS`

### 3.5 本地 WebSocket 会话（新）
`internal/service/ws_session.go` — 浏览器本地直连走 WS，**帧协议与 DataChannel 完全一致**：

```
浏览器 ──WS(/ws/peer)──→ 本地 node：管理/元数据/小文件（毫秒级，无打洞）
浏览器 ──WebRTC───────→ 任意 node（含远端）：大文件、跨节点（打洞直连）
```

- `Session` 接口抽象两种传输（`internal/service/ws_session.go` + `rtc_session.go`）：
  同一 reqId 状态机 / serveFile / FetchFromPeer 零分支复用
- `FetchFromPeer("local", ...)` 复用同一拉取路径；`WSSession` 无写缓冲流控
  （TCP 自带，serveFile 用接口断言只对 DataChannel 做水位控制）
- 注意与旧 `/ws/signal`、`/ws/transfer`（legacy 自建信令）不是一回事

### 3.6 自托管信令服务器（新）
`internal/signalserver/` + `cmd/peerserver/` — 自托管 PeerJS 信令 + **内置房间发现**：
替代公共云信令（0.peerjs.com）与公共 MQTT broker。

```
vps 上跑：peerserver -addr :9000 -key <key>
节点端：PEERDRIVE_PEERJS_HOST/PORT/KEY 指向自托管（peerjs 客户端协议零改动）
       PEERDRIVE_DISCOVER_URL=http://vps:9000 （发现优先于 MQTT）
浏览器：host/port/key 配置指向自托管（信令自有，无 MITM 面）
```

- **信令**：兼容 peerjs-server 协议子集（WS 注册 + token、OFFER/ANSWER/CANDIDATE/LEAVE 按 dst 转发、
  dst 离线入队 30s 过期、OPEN/ID-TAKEN、心跳保活、`GET /{key}/id` 分配）
- **发现**（MQTT 功能并入）：`POST /discover/announce {peerId, collections}`（30s 心跳）+
  `GET /discover/nodes?coll=` 查询在线节点——服务器天然知道所有在线节点，无需广播
- 节点端 `HTTPDiscovery`（`service/http_discovery.go`）：announce + 10s 轮询 → onPeer → 自动互联
- 安全：信令自有后无公共云 MITM 面；后续可在服务器加 token 白名单

### 3.6.1 线上部署（cloudcone）
```
peersignal.moonchan.xyz ──CF 灰云 A 记录──▶ 117.55.237.217（cloudcone nginx）
        │ wss + https
   peerserver（systemd，127.0.0.1:9000，key=pd-signal-b9447b406828e500）
```

- 部署细节与运维命令见项目 AGENTS.md「线上部署」节
- 线上验证：`PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v`
  （TestLiveSignal_DiscoveryAndInterop：线上信令+发现+拉文件全链路；TestLiveSignal_ProtocolCompat：客户端协议兼容）

### 3.7 transport 包 inbound/outbound 角色拆分（2026-08-16）

按帧角色把 `transport/` 拆成两角色 + 共享核心（对齐 Xray/sing-box 心智模型，
但**只切角色不切连接**——WebRTC 连接全双工对称，同一条 Session 同时承载两角色）：

```
conn.go          ← 共享连接核心（禁止复制）：dcReq/dcResp、connState（fetches/expect 归
                    outbound，pendingUpload/binCh 归 inbound，平铺共享）、bindConn 分派
inbound.go       ← 入站角色 = 应答对端 verb 全集：serveFile/openFile、
                    create/upload/list/info/delete/sync 服务端（原 file_index_verbs.go 并入）、
                    uploadWorker（上传落盘）
outbound.go      ← 出站角色 = 本端发起 verb 全集：FetchFromPeer/requestFile/routeResponse/
                    stateFor + maxPeerFetchSize
peerjs_service.go← 瘦身为装配层（854 → 420 行）：信令生命周期、连接建立
                    （connectLoop 拨号 / onIncomingConnection 接受）、BindLocal、
                    发现装配——连接建立只是创建全双工 Session，随后 bindConn 挂双角色
```

- 纯代码归位（剪切+重建文件），行为零变化；`file_index_verbs.go` 删除并入 inbound.go
- 验证：`go build -tags nosqlite ./...` + `go test -tags nosqlite ./internal/transport/ -race` 全绿
- 预留（未做）：outbound 侧 `Fetcher` 接口（localFetcher/peerFetcher/未来 httpFetcher）
  实现「任意入口请求 → 任意出口」路由矩阵，有新传输需求时再落地

### 3.8 统一 source 体系（2026-08-16）

统一文件管理：多后端（本地磁盘 / p2p 透传 / URL 模板）抽象为 `Source`，由
`SourceManager` 统一路由（优先级）、统计（Snapshot）、运行时调整（SetPriority）。

```
internal/source/
  source.go   ← Source 接口 + Capability(CapFile=1 整体拉取 / CapStream=2 流式分片)
                + FileMeta/Stats/SourceStatus
  manager.go  ← 注册/反注册、优先级升序路由：命中即返回、Available()==false 跳过、
                全失败返回汇总错误；record() 记成功/失败/字节/最近错误
  local.go    ← LocalSource：resolvePath 复刻 serveFile（file_index 优先 + IsPathAllowed
                防御 + CAS 兜底），CapStream
  peer.go     ← PeerSource：Connections 枚举 + per-peer Mutex.TryLock 串行尝试
                （连接级 expect 单槽约束：同一 peer 同时只允许一个 fetch 流）
  url.go      ← URLSource：fmt 模板（%s=hash，含 %d 声明 CapStream）→ Range 分片；
                全量请求读取后 sha256 校验（内容寻址兜底）；可注入 http.Client
                指向 ech-proxy 等出口（wintools cmd/ech-proxy），不建独立 source 类型
```

- **路由语义**：本地命中即返回，未命中降级 peer，URL 源最后兜底；大文件只走
  CapStream（OpenRange 拒绝 CapFile 源——防 8GB 全量 buffer OOM），OpenAny 可降级整体
- **装配**（cmd/server/main.go）：local → peer → url(可选, `PEERDRIVE_URL_SOURCE_TEMPLATE`)
- **管理面**：`GET /sources`（状态+统计）、`POST /sources/:name/priority`（运行时调整）
- **边界**：serveFile 保持本地语义不接 manager（避免入站→出站透传递归环）；
  /peerjs/fetch 仍直调 FetchFromPeer（保持语义，未切 Manager）
- 配套：`requestFile` 流式化（fetchState 加 `q chan []byte` 块队列 + pump 投递，
  OpenStream 流式读；FetchFromPeer 保留 []byte 兼容签名）；修复 cleanup 双 close panic、
  fetchReader drain 循环 buf 覆盖丢块两个 bug
- 验证：`go test -tags nosqlite ./internal/source/ -race` + transport 全绿；
  集成测试引用 `service.*` 的 M3 遗留已改 `transport.*`


### 3.9 端口转发 forward v2（2026-08-16，inbound 一环重建）

legacy 的 libp2p 流转发（`/peerdrive/forward/1.0.0`，明文 `KEY xxx` 单行认证、无
白名单）已删除，改为 PeerJS DataChannel 上的 TCP 隧道（`internal/transport/forward.go`，
约 470 行 + 8 个单测）：

```
client ──fwd-open {port, reqId}──────────────▶ server   申请转发目标端口
client ◀──fwd-challenge {nonce, reqId}────────  server   一次性随机数(5min TTL, 上限64防洪水)
client ──fwd-auth {hmac, reqId}──────────────▶ server   HMAC-SHA256(key, nonce)
client ◀──fwd-ok / fwd-err────────────────────  server
之后: fwd-data 头+二进制块双向透传（复用 SendFrame 原子头-块约束）; fwd-close 收尾
```

- **权限控制**：规则表 `key → 端口白名单`（配置 `PEERDRIVE_FORWARD_RULES="key:port,..."`
  或运行时 `POST /p2p/forward/create` 动态登记）；端口越权 → fwd-err，不泄露规则
- **密钥交换**：质询-响应（nonce 一次性+过期），key 明文永不落线；服务端验证需
  key 原文（=凭证，配置 chmod 600）
- **SSRF 防护**：服务端只 dial `127.0.0.1`；握手不占隧道槽，隧道建立才占连接级单槽
- **API**：`PeerJSService.OpenForward(ctx, peerID, key, port)`（net.Conn）；HTTP 端点
  4 个保留（create=登记规则 / connect=本地监听代理 / list / close）
- 转发块写经连接级 worker（fwdCh，H5 同款）——TCP 背压不卡消息泵；
  CloseForwardStream 主动断开即释放单槽
- 验证：8 个单测（握手全流程/坏 key/端口越权/重放/超时/无隧道防御/数据透传）
  + `-race` 全绿

### 3.10 前端全面迁移到 WS + admin 管理 verb（2026-08-17）

前端从 HTTP API 全面迁移到本地 WS 会话（`/ws/peer` 帧协议），HTTP 路由全部保留
（`router.go` 标注 LEGACY 注释区）。用户决策：**管理面只走本地 WS**，WebRTC 连接
不实现管理 verb（防权限面暴露给公共信令上的未知节点）；数据面仍走原 `req` verb。

- **admin verb**（`internal/transport/admin.go`，约 300 行 + 6 个单测）：浏览器经
  本地会话发 `{"type":"admin","method","path","body","token","reqId"}`，内部构造
  *http.Request → 注入 gin engine 的 ServeHTTP（`httptest.NewRecorder`，router 经
  `SetAdminHandler` 装配）→ **复用全部 HTTP controller，零重复实现**
- **二进制上传**：admin 帧 `binary:true` + filename/field/size 声明，后续二进制帧
  收集到临时文件 → multipart 重包转发（controller 的 FormFile 无感知）。field 默认
  `file`，BT torrent 用 `torrent` + `path=/bt/torrent`（reqPath 由声明帧决定，
  **坑**：初版硬编码 `/files/upload` 导致 torrent 打错路由，见 admin_test.go）
- **二进制响应**：文件流 → `admin-bin` 头 + 单二进制帧（≤64MB；大文件走 req verb）
- **认证**：admin 帧 token 字段 → 转发时注入 `Authorization: Bearer`，与 HTTP 一致
- **前端**：`front/src/ws.js`（新，admin/upload/download/downloadToFile，reqId pending
  map + 单槽 binaryExpect）+ `api.js` 全部 request 走 WS；页面下载/预览改 Blob
  方式（`getBlobUrl`/`downloadFileToDisk`）；`__mocks__/api.js` 同步
- 验证：后端 6 个 admin 单测 + 前端 `tests/ws.test.js` 7 个单测（reqId 乱序路由/
  409 透传/token/分块收集/err/admin-bin/断线）+ 全量单测 + build 全绿

## 4. 帧协议（DataChannel 上，go↔go 与 go↔web 共用）

```jsonc
// 请求（任意端）；reqId 为指令 UUID v4（服务端生成，保证跨连接唯一）
{"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<uuid-v4>"}
// 响应（回显 reqId）
{"type":"meta","hash","total","reqId"}
{"type":"data","hash","offset","size","reqId"}   // 后随 size 字节二进制
{"type":"done","hash","offset","size","reqId"}
{"type":"err","msg","reqId"}
```

**文件索引 verb**（`FileIndexService`，SQLite `file_index` 表持久化 sha256→绝对路径）：

```jsonc
create   {type:"create", path}              → created {hash,size,name,path,seq}
upload   {type:"upload", name, size, offset?, reqId}  分片上传（offset 缺省 0）
         → meta {total, offset:连续已写} → data×1 → uploaded{hash,path}（整体完成）| ack{offset}（续传）
list     {type:"list", offset?, size?}       → list-resp {files,total}
info     {type:"info", hash}                → info-resp {hash,size,name,path,seq}
delete   {type:"delete", hash}              → deleted {hash,seq}
sync     {type:"sync", seq}                 → sync-resp {files,lastSeq}（metadata 增量同步）
```

- **分片上传**：offset 按 64KB chunk 对齐，一次 upload 请求 = 一个分片（data 块 ≤64KB）；
  服务端 `UploadSession` 位图跟踪（chunk 粒度），**多 source** = 多连接并发传不同分片，
  位图全满自动触发 uploaded（最后一片的请求方收到）
- **断点续传**：同 name 重开会话幂等复用；meta.offset 返回连续已写偏移（位图重建，
  进程重启后按文件大小近似，最终 sha256 校验兜底）；会话 10 分钟无活动清理
- 同步模型：`file_index.seq` 单调游标，`sync{seq}` 取增量变更（含 tombstone），对端 `ApplySync` 合并
- 上传安全：size 上限 8GB、文件名净化（防路径穿越）、offset 必须 chunk 对齐、越界写拒绝
- download 优先查 file_index（外部登记/上传文件），其次内容寻址存储

**三条协议约束（勿破坏）**：
1. JSON 控制头必须是**文本帧**（`SendText`），数据块是**二进制帧**（`Send`）——发反了对端把控制头当数据块吞掉
2. data 头与数据块必须**原子连续**（`SendFrame` 的 sendMu），接收端按连接级 expect 状态机路由
3. 浏览器端可不传 reqId（向后兼容），Go 端始终携带（UUID v4）

**admin 管理 verb**（§3.10，仅本地 WS 会话，`admin.go`）：

```jsonc
// 普通请求 → admin-resp（4xx/5xx 也走 admin-resp，body 为结构化错误体，409 含 conflicts）
{"type":"admin","method":"GET|POST|DELETE","path":"/files?x=1","body":<JSON>,"token":"<可选>","reqId"}
{"type":"admin-resp","status":200,"body":<原始 JSON>,"reqId"}

// 二进制上传：声明帧 + 后续二进制帧（收齐 multipart 重包转发；field 默认 "file"）
{"type":"admin","method":"POST","path":"/files/upload","binary":true,"filename":"a.bin","field":"file","size":N,"reqId"} + N 字节二进制帧

// 二进制响应（文件流，≤64MB）：admin-bin 头 + 单二进制帧
{"type":"admin-bin","status":200,"size":N,"reqId"} + 二进制帧
```

## 5. E2E 踩过的坑（全部已修）

| 坑 | 修复 |
|---|---|
| answerer 新生成 connectionId → ANSWER 路由不到、ICE 卡 checking | answerer 必须沿用 offerer 的 connectionId |
| pion 不自动发 ICE 候选 → 双方永远 checking | `OnICECandidate` → 信令 CANDIDATE 手动转发 |
| `dc.Send([]byte)` 发二进制帧，JSON 头被当数据块丢弃 | 头用 `SendText` |
| 对端未上线 OFFER 入队过期（EXPIRE）→ 永远等 OnOpen | EXPIRE 时 Close 连接，connectLoop 循环重连 |
| 重连失败后不重试（connectLoop 一次性退出） | 无限循环 + 指数退避 |
| 浏览器测试超时：chromium 不走系统代理 / about:blank 无 crypto.subtle | chromium 显式 `--proxy-server`；sha256 在 Node 侧算 |
| **持锁调用 `conn.Close()` 死锁**（Go mutex 非重入）：handleOffer 重复 connectionId 清理、handleLeave 关闭对端连接 | 锁内只收集，解锁后 Close |
| **WS 并发写 panic**：`gorilla/websocket` 不允许并发 WriteJSON，心跳/ICE 候选/ANSWER 多 goroutine 并发（3 节点互通测试触发） | signaller 加 writeMu 串行化 |
| **并发流控死锁**：旧实现每个 serveFile 各自注册 `OnBufferedAmountLow`（pion 替换式回调）——并发请求只有最后一个注册者能收到低水位事件，其余在 bufferedAmount 超阈值时死等（4 并发 × 2MB 集成测试复现，修复前卡到超时） | 流控下沉到 `peerjs.Connection.SendFrame`（attach 时全局注册一次回调 + lowWater 广播），serveFile 零流控代码；等待可用 c.done 退出（连接关闭不悬挂） |

## 5.1 测试体系

单元测试（无网络，race 下跑）：

```bash
cd back/peerjs && go test ./... -count=1 -race
```

覆盖：connectionId 沿用、重复 OFFER 清理、EXPIRE/LEAVE 关闭、Close 幂等、
SendFrame 并发原子性（8×50 轮验证头体不交织）、**SendFrame 内置流控
（高水位阻塞 → 低水位恢复；连接关闭退出不悬挂）**、文本/二进制帧类型、
远端关闭清理、ICE 配置入口。

集成测试（真实公共信令 0.peerjs.com + 公共 broker broker.emqx.io，需外网+代理）：

```bash
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v
```

**必须 `-p 1` 串行**：公共信令上多组测试并行会互相干扰（发现：默认并行时
ThreeNodes/MQTT 偶发失败，串行全绿）。集成测试依赖真实外部服务，天然不可并行。

覆盖：双节点互通+range 拉取、3 节点两两互联、4 节点星型一对多并发拉取、
**4 并发 × 2MB 大文件拉取（流控死锁回归，修复前卡到超时）**、
MQTT 分片互相发现（含 60s 心跳兜底时序）、MQTT 发现→PeerJS 互联→拉文件全链路、
本地 WS 会话拉取 + FetchFromPeer("local") 双向复用。

## 6. 旧代码处置（详见 doc/LEGACY.md）

- libp2p 栈（p2p.go/transfer/resume/multipeer/dual/ws/signaling/relay...）：**待迁移**（被 PeerJS 取代）
- BT 栈（p2p_bt/）：**待迁移成独立库**——但注意 README 说"可独立使用"是**错的**：
  它依赖 `internal/log`，`PutImmutable` 本地 store 优先掩盖网络失败，`putLocal` 依赖 anacrolix 内部行为
- WebDAV/forward/auth 死代码：**可删**（高危）
- 前端 ~4000 行死组件：✅ **已删**（2026-08-16，见 LEGACY.md F 节；含 FileManager/WebRTCTransfer/
  旧 P2P 状态面板/localDB 等 21 文件 + api.js 死导出清理；CollBrowserNav 勘误保留）
  同步修复一批活跃主链路 bug（合并/移动语义/竞态守卫等，见 doc/REVIEW-FIX-2026-08-16.md 第二轮）

## 7. 目标包结构（依赖分层，渐进迁移）

```
internal/
├── domain/        层0 领域模型（零依赖）—— 未来把 model 拆 collection/file/peer
├── config/ log/   层0 基础设施叶子
├── repository/    层1 持久化（只依赖 domain）
├── provider/      层1 文件获取抽象（把 service 里复制 6 遍的本地查找收敛进来）
├── service/       层2 用例编排（只依赖 domain/repository/provider/transport）
├── transport/     层2 互联传输（peerjs_service + discovery/ 迁入）
└── api/           层3 HTTP（原 controller 只依赖 service）+ router 装配
```
> ✅ 2026-08-16 批1/批2 后：`legacy/` 已全部删除（webdav/forward→v2/测试工具/批2 整栈）。
> `internal/downloader/`（原 universal_downloader）为独立下载器（local/ipfsgw/btdht/http）。
```

迁移顺序：M0 依赖规则文档 → M1 legacy 隔离 → M2 收 controller 越层依赖 → M3 拆 transport → M4 provider 落地。


### §8 依赖规则（M0，2026-08-16 立）

硬性规则（代码评审 + 文档双通道执行）：
1. **禁止 import `internal/p2p_bt`**（除 p2p_bt 库自身与 cmd/test 入口；`internal/legacy`
   已于 2026-08-16 批2 删除，此规则自动升级为「legacy 已不存在」）。
2. 包层级单向：`model ← repository ← provider ← service ← controller ← router ← cmd`，
   `transport` 与 `provider` 同级（可被 service/controller 引用，不反向）。
3. `service` 包内不直接 import `transport`；跨层一律经 controller 装配注入。
4. 准出条件：所有新包测试通过；`go build -tags nosqlite ./...` 全绿。
5. ✅ 已达成：legacy 存量引用于 2026-08-16 批2 清零（webdav/forward/libp2p 端点已删）。

**迁移状态（2026-08-16）**：M2 ✅ 完成 · M3 ✅ 完成 · M4 ✅（provider 已落地）· M1 ✅ 完成（p2p_bt 拆独立库另计）。

M1 legacy 隔离要点（本次完成，internal/legacy/ 落地）：
- 22 个文件从 service 迁入 legacy 包：libp2p 栈（p2p.go/transfer/resume/multipeer/dual/ws/
  helpers/connection/key + 测试）、信令（signaling.go）、中继（relay + relay_registry）、
  注册（node_registrar）、扫描（peer_scanner/peer_tracker）、IPFS（ipfs_service/ipfs_compat）、
  webdav、forward、universal_downloader（依赖 P2PService 的下载栈核心）。
- legacy 依赖面收敛到 config/log/model/nodestate/p2p_bt/provider/repository/hashutil
  （层0/1），service 包零 legacy 反向引用之外的循环依赖。
- 过渡期残留：service/file_service + sync_service、controller/{p2p,signal,download}、
  router、cmd/server 仍引用 legacy（旧栈端点保留至删除决策）；
  test-p2p-colls / test/bt-integration 旧工具已改引用。
- ✅ 后续：p2p_bt 拆独立库（5fb1193，README 断言成立）；webdav/forward 删除（6bfc000/e030216）；
  libp2p+IPFS 整栈删除（a5b090d）；legacy 包清零（下载器迁 internal/downloader）。

M3 收层要点（本次完成，transport 包落地）：
- 新建 `internal/transport/`：PeerJS 文件服务子系统整体迁入——
  `peerjs_service.go`（互联 + 帧协议服务端）、`file_index.go` + `file_index_verbs.go`
  （sha256 文件索引 + req/meta/data/done/err 业务 verb）、`ws_session.go` + `rtc_session.go`
  （Session 抽象：本地 WS / WebRTC DataChannel 双实现）、`mqtt_discovery.go` +
  `http_discovery.go`（发现组件）。
- transport 依赖面收敛到 `config/log/repository/pkg/hashutil`（层0/1），不再触碰
  service 包；`pkg/hashutil` 新增 `IsStrictSHA256`（严格小写 64 hex，替代原
  service 包 isValidHash 在传输层的使用）。
- 外部装配（router/peerjs_routes、cmd/server main）改引用 `transport.*`；
  测试随迁（file_index_test / peerjs_service_test），transport↔service 无循环依赖。

M2 收层要点（本次完成）：
- controller 不再 import repository：集合/分享/任务/pin 直调全部收编进 service——
  `CollectionService`（collection_service.go，含 fork/merge/版本/匿名集合）、
  `ShareService`、`TaskService`、`PinService`；download/file 控制器改走 FileService
  （新增 GetMeta/GetMetaByCID/ListAll/ImportGatewayData/RegisterBTFile）。
- 领域类型上移 model：`FileTypeBlob/FileTypeAnonCollection`、`IPFSPin`；
  repository 保留别名兼容。
- router 不再内联写库（BT onComplete 回调收敛进 FileService.RegisterBTFile）；
  router 仅保留 SyncRepository 等 DI 装配。
- collection.go 中直写 SQL 的 ListPublicCollections 收敛为 repository.ListPublicCollections。

## 8. 环境与验证

```bash
cd back
go build -tags nosqlite ./...          # 必须带 nosqlite（双 SQLite 驱动 CGO 冲突）
go test -tags nosqlite ./...
# E2E 手动验证（需外网）：
#   A/B 节点各设 PEERDRIVE_PEERJS_ID，B 设 PEERDRIVE_PEERJS_PEERS=pd-node-a
#   curl -X POST localhost:PORT/peerjs/fetch -d '{"peer":"pd-node-a","hash":"<64hex>"}'
# admin verb 冒烟（无需外网，本地起服即可）：
#   PEERDRIVE_STORAGE=/tmp/pd-storage PORT=3000 go run ./cmd/server/ &
#   node front/tests/e2e-admin-smoke.mjs   # 连接 /ws/peer 走 admin 全链路（ping/上传/下载/集合/404）
```

**构建环境坑**：go 命令需 `HTTPS_PROXY=http://172.29.80.1:10809 GOPROXY=https://goproxy.cn,direct`
（WSL 出网走宿主机代理，opencode 环境 unset 了代理）。
