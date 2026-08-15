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
- 前端 ~4000 行死组件：**可删**

## 7. 目标包结构（依赖分层，渐进迁移）

```
internal/
├── domain/        层0 领域模型（零依赖）—— 未来把 model 拆 collection/file/peer
├── config/ log/   层0 基础设施叶子
├── repository/    层1 持久化（只依赖 domain）
├── provider/      层1 文件获取抽象（把 service 里复制 6 遍的本地查找收敛进来）
├── service/       层2 用例编排（只依赖 domain/repository/provider/transport）
├── transport/     层2 互联传输（peerjs_service + discovery/ 迁入）
├── legacy/        旧栈隔离（p2p_bt + libp2p 归拢，新代码禁止 import）
└── api/           层3 HTTP（原 controller 只依赖 service）+ router 装配
```

迁移顺序：M0 依赖规则文档 → M1 legacy 隔离 → M2 收 controller 越层依赖 → M3 拆 transport → M4 provider 落地。


### §8 依赖规则（M0，2026-08-16 立）

硬性规则（代码评审 + 文档双通道执行）：
1. **禁止 import `internal/legacy`、`internal/p2p_bt`（除 legacy 包自身与 cmd/test 入口）**。
   legacy 只出不进：新功能缺失依赖时，在 service/transport 侧抽象，不反向依赖旧栈。
2. 包层级单向：`model ← repository ← provider ← service ← controller ← router ← cmd`，
   `transport` 与 `provider` 同级（可被 service/controller 引用，不反向）。
3. `service` 包内不直接 import `transport`；跨层一律经 controller 装配注入。
4. 准出条件：所有新包测试通过；`go build -tags nosqlite ./...` 全绿。
5. legacy 存量引用（file_service/sync_service/controller-p2p/router/main）为过渡期残留，
   目标随旧栈删除（webdav/forward/p2p 端点）清零；删除决策见 LEGACY.md。

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
- 待办：p2p_bt 拆独立库（README"可独立使用"断言错误问题）；webdav/forward 高危删除决策。

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
```

**构建环境坑**：go 命令需 `HTTPS_PROXY=http://172.29.80.1:10809 GOPROXY=https://goproxy.cn,direct`
（WSL 出网走宿主机代理，opencode 环境 unset 了代理）。
