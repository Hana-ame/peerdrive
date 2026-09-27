# 模块 10：peerjs PeerJS 协议库

- **代码位置**：`back/peerjs`（独立 go.mod，module 路径 `github.com/Hana-ame/go-peerjs`，见 `back/peerjs/go.mod:1`；主仓库在 `back/go.mod:7` require 并在 `back/go.mod:160` 以 `replace github.com/Hana-ame/go-peerjs => ./peerjs` 指向本地目录）
- **功能一句话**：PeerJS 兼容信令客户端 + WebRTC DataChannel 传输原语（连接 / 消息 / 信令 / 流控），纯内存连接态，不持久化任何状态。
- **依赖**：`gorilla/websocket v1.5.3`（信令 WS 客户端，`back/peerjs/go.mod:6`）、`pion/webrtc/v4 v4.1.2`（PeerConnection / DataChannel / ICE，`back/peerjs/go.mod:7`）；测试用 `stretchr/testify`（`back/peerjs/go.mod:8`）；标准库 `crypto/rand`（token/connectionId 生成，`back/peerjs/peer.go:6,319-329`）、`net/http`（取随机 ID，`back/peerjs/peer.go:11,543-578`）、`sync`/`time`。不依赖任何上层框架（gin / sqlite 均无）。
- **被依赖**：主消费者是 `back/internal/transport/peerjs_service.go`（`PeerJSService` 持 `*peerjs.Peer`，`back/internal/transport/peerjs_service.go:26,38-41`）；`back/internal/transport/rtc_session.go:4,11-39` 把 `*peerjs.Connection` 适配为统一 `Session`；`back/internal/transport/ws_session.go:9,15-29` 的 `Session` 接口签名直接使用本库 `peerjs.Frame` 类型（WS 会话复用同一帧协议）；`back/internal/transport/conn.go:36` 帧协议核心引用；独立消费端 `back/cmd/media-node/main.go:46`（媒体节点）、`back/cmd/echclient/main.go:15`（验证客户端）；测试引用见 `back/test/integration/{live,selfhosted}_test.go`、`back/internal/source/peer_test.go`、`back/internal/transport/{conn,psk,peerjs_service,forward,stream}_test.go`。另有独立 repo 镜像 `github.com/Hana-ame/go-peerjs`（tag=v0.1.0 同步，见 `AGENTS.md:24-27`）。

## 1. 逻辑

**模块定位**：传输原语（信令 + 数据面），业务帧协议（verb）由上层定义——与「格式无关」原则一致（`back/peerjs/message.go:1-4`、`back/peerjs/README.md:6-7`）。

**三层结构 + 一个传输抽象**（分层理由：各自可独立替换/测试，见 `back/peerjs/README.md:91-98`）：

| 层 | 职责 | 代码 |
|---|---|---|
| `Signaller` | 信令通道抽象：注册节点、收发信令消息；当前实现为 PeerJS 协议（`peerJSSignaller`） | `back/peerjs/signaller.go:12-30`、`back/peerjs/peer.go:352-369` |
| `Peer` | 节点角色：信令路由（OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE 等分发）、连接注册表（一对多）、生命周期；主动发起（`Connect`）与被动接收（`OnConnection`） | `back/peerjs/peer.go:35-44,168-215` |
| `Connection` | 一条 WebRTC DataConnection：SDP 交换、ICE 候选转发、帧发送（文本/二进制/原子头+体）、内置写缓冲流控、open/message/close 事件 | `back/peerjs/connection.go:24-57` |
| `DataChannel` 接口 | 数据面传输抽象，`Connection` 只依赖此接口（不绑定 pion 具体类型），当前实现为 `pionChannel`（pion/webrtc DataChannel 适配）| `back/peerjs/transport.go:15-26,29-56` |

**信令协议（消息与路由）**：

- 消息类型与 peerjs-server 枚举一致：`OPEN/LEAVE/CANDIDATE/OFFER/ANSWER/EXPIRE/HEARTBEAT/ID-TAKEN/ERROR`（`back/peerjs/message.go:18-28`）；`Message{Type,Src,Dst,Payload}`，Payload 为任意 JSON（`back/peerjs/message.go:38-43`）。
- 连接类负载：`OfferPayload`（SDP + type + connectionId + label + reliable + serialization="raw"，`back/peerjs/connection.go:339-346`）、`AnswerPayload`、`CandidatePayload`（`back/peerjs/message.go:57-80`）。
- 注册流程：指定 ID 时直接连 WS；未指定先 `GET /id?ts=…&version=…` 取服务端分配的随机 ID（校验通过 `validID` 后使用），再建立 WS（`back/peerjs/peer.go:396-408,543-578`）。
- WS 地址形如 `wss://host:port/peerjs?key=&id=&token=&version=1.5.4`（`back/peerjs/peer.go:410-427`；version 常量见 `peer.go:22`）；`src` 由服务端覆盖（`back/peerjs/peer.go:502` 注释）。
- 保活：客户端每 `PingInterval`（默认 5s）发一次 `HEARTBEAT`；收到服务端 `HEARTBEAT` 不应答、无副作用（`back/peerjs/peer.go:483-500,173-175`）。
- 路由分发：`ANSWER/CANDIDATE` 按 payload 里的 `connectionId` 找到既有连接再交给 `conn.handleMessage`；`OFFER` 创建新连接（answerer）；`EXPIRE` 关闭对应连接（让上层重连）；`LEAVE` 关闭该远端所有连接；`ERROR/ID-TAKEN` 记日志（`back/peerjs/peer.go:168-215`）。

**连接建立两条路径**（对称，无方向之分，`back/internal/transport/peerjs_service.go:13-14`）：

- Offerer：`Peer.Connect(ctx, dst, label)` → `newConnection` 建 PC、`CreateDataChannel(label, Ordered=true)`、`makeOffer` 发 `OFFER`（`back/peerjs/peer.go:136-141`、`back/peerjs/connection.go:225-286,330-348`）。
- Answerer：收到 `OFFER` 后 `handleOffer` 用 **offerer 的 connectionId** 建连接、`SetRemoteDescription`、回 `ANSWER`：`back/peerjs/peer.go:217-254`、`back/peerjs/connection.go:350-364`。
- ICE：本地候选 `OnICECandidate` → 手动封装 `CANDIDATE` 经信令转发（pion 不会自动发送，`back/peerjs/connection.go:252-265`）；远端候选经 `conn.handleMessage` 的 `AddICECandidate` 注入（`back/peerjs/connection.go:211-216`）。

**数据面（帧）**：`Frame{IsText, Data}`，`IsText=true` 为文本帧（JSON 控制头，SCTP PPID 51），`false` 为二进制块（PPID 53）——协议依赖此区分「控制头 vs 数据块」（`back/peerjs/transport.go:5-10`、`back/peerjs/connection.go:91-113`）。`SendFrame` 原子发送「JSON 头 + 紧随的二进制体」，`sendMu` 串行保证多 goroutine 并发下头体不交织（`back/peerjs/connection.go:125-132`）。

**流控**：`SendFrame` 内置写缓冲流控——`bufferedAmount > defaultBufferLowThreshold(512KB)` 时等待低水位事件（`lowWater` 通道广播）；连接关闭立即退出；慢消费者 30s 超时返回错误（`back/peerjs/connection.go:12-17,152-166`）。低水位回调在 `attach` 时注册一次（pion `OnBufferedAmountLow` 是替换式回调，并发注册互相覆盖→死等，`back/peerjs/connection.go:16,297-304`）。

**生命周期与重连**：本库只提供原语，重连循环由上层驱动——`Peer.Done()` 透传 `Signaller.Done()`（信令断线通知，H7 修复：`back/peerjs/peer.go:129,392-393`、`back/peerjs/signaller.go:23-27`），上层 `startLoop` 在信令断线时整轮重建新 `Peer`（`back/internal/transport/peerjs_service.go:285-309`）；连接级重连由 `connectLoop` 在 `conn.Done()` 后循环拨号（依赖 EXPIRE/Close 触发 `done`，`back/internal/transport/peerjs_service.go:400-469`）。

**扩展点**：换信令 `NewPeerWithSignaller` / `SignallerFactory`（`back/peerjs/peer.go:74-82`、`back/peerjs/signaller.go:38-39`）；换传输实现 `DataChannel` 接口（`back/peerjs/transport.go:12-26`）；加消息类型 `MessageType` 为开放 string（`back/peerjs/message.go:14-15`）。

## 2. 如何储存

**不持久化。** 本模块是纯内存连接态库，不写磁盘 / DB / 任何文件，也没有委托给 repository / storage 等下级模块——所有状态都是进程内变量。

**内存态构成**（均随进程生命周期，进程退出即消失）：

- `Peer`：`opts Options`、`signaller Signaller`、`onConn ConnectionHandler`、`iceServers`、`conns map[string]*Connection`（connectionId → 连接表）、`closed chan struct{}`（`back/peerjs/peer.go:35-44`）。
- `Connection`：`ID`（connectionId，信令路由键）、`PeerID`（远端 peer id）、`Label`、`Offered`；`pc *webrtc.PeerConnection`、`dc DataChannel`、`ice`；`sendMu`、`lowWater chan struct{}`（容量 1）、`done chan struct{}`、`closeOnce`；回调 `onOpen/onMessage/onClose`（`handlerMu` 保护，`back/peerjs/connection.go:24-57`）。
- `peerJSSignaller`：`id`、`token`、`opts`、`route` 回调、`conn *websocket.Conn`、`connected bool`、`closed/done chan struct{}`、`writeMu`（`back/peerjs/peer.go:352-369`）。

**生命周期与重启影响**：

- conns 表随连接 open/close 增删；`Peer.Close` 清空并逐个 `Connection.Close`（`back/peerjs/peer.go:144-163`）。
- 节点 ID：由上层 `NewPeerJSService` 决定——`cfg.PeerJSID` 为空时每次启动生成 `peerdrive-<randHex8>`，**不落盘**；要跨重启保持身份必须显式设置 `PEERDRIVE_PEERJS_ID`（`back/internal/transport/peerjs_service.go:113-117`、`README.md:217`）。
- token：每次 `NewPeer` 未指定时随机生成（16 字节 hex，`back/peerjs/peer.go:345-347,319-329`），不持久化。
- connectionId：offerer 每次 `newConnection` 生成 `randHex(16)`（32 hex 字符），answerer 沿用（`back/peerjs/connection.go:230-232`），随连接生命周期，关闭即弃。
- 进程重启后：所有内存态重建（上层 `startLoop` 重新 `NewPeer` + `Dial`，`back/internal/transport/peerjs_service.go:184-210`）；除非配置了静态 ID，否则以新 ID 重新注册信令。
- 持久化决策全部在上层：如「已加入节点」清单 `joined_nodes.json` 由 `service.NodeDirectory` 负责（`README.md:62,101`），本库不参与。

## 3. 何时储存

本库没有储存，只有「何时写入 / 更新 / 删除内存态」的触发点：

| 时机 | 触发点 | 内容 |
|---|---|---|
| 进程启动 | 上层 `Start()` → goroutine `startLoop` → `NewPeer` + `Dial`（`back/internal/transport/peerjs_service.go:147-149,184-210`） | 初始化 `conns` 空 map、`closed/done` channel；连接信令 WS 后置 `signaller.connected=true`、保存 `conn` |
| 取 ID | `Dial` 时 `id` 为空：`retrieveID` 走 HTTP 后写入 `s.id`（`back/peerjs/peer.go:396-408,543-578`） | 服务端分配的随机 ID |
| 主动拨号 | `Peer.Connect` → `newConnection` → `registerConnection`（`back/peerjs/peer.go:136-141,272-276`；`back/peerjs/connection.go:225-286`） | conns[connectionId] 写入新 Connection |
| 被动接收 | 信令 `OFFER` → `handleOffer` → `newConnection`（沿用 offerer connectionId）→ `registerConnection`（`back/peerjs/peer.go:217-254`） | conns 表新增 answerer 连接 |
| 通道绑定 | `attach`：写入 `c.dc`、注册低水位/onOpen/onMessage/onClose 回调（`back/peerjs/connection.go:291-328`） | 内存回调表与 dc 引用 |
| 断开/过期 | 对端 `LEAVE``EXPIRE`、ICE `closed/failed/disconnected`、远端关 dc、上层主动：`c.Close()` → `closeOnce` → `forgetConnection` 从 conns 删除 → `close(done)` → 触发 `onClose`（`back/peerjs/connection.go:178-198,244-251,323-327`；`back/peerjs/peer.go:191-200,256-269`） | 内存态清理（幂等，只执行一次） |
| 信令断线 | `readLoop` 因网络错误/EOF 退出 → `close(s.done)`（`back/peerjs/peer.go:446-466`） | 通知上层整轮重建 Peer；旧 conns 全部随旧 Peer 丢弃 |
| 心跳保活 | `heartbeatLoop` 每 5s 发 `HEARTBEAT`（`back/peerjs/peer.go:483-500`） | 网络发送，无状态 |
| 优雅关闭 | 上层 `Close()` → `cancel()` + `peer.Close()`（`back/internal/transport/peerjs_service.go:152-181`；`back/peerjs/peer.go:144-163,524-540`） | 关闭全部连接并置空 conns、关闭信令 WS |

## 4. 储存什么

模块自身**无表、无文件、无 DB 条目**；下列为进程内内存条目清单：

**连接注册表 `Peer.conns`**（`back/peerjs/peer.go:42,272-283`）：

| 字段 | 说明 / 约束 |
|---|---|
| 键 | `connectionId`，offerer 生成 32 位 hex（`randHex(16)`，`back/peerjs/connection.go:230-232`）；answerer 必须沿用（`back/peerjs/connection.go:222-224`） |
| 值 | `*Connection`，含 `ID/PeerID/Label/Offered/pc/dc/done/lowWater` 等（`back/peerjs/connection.go:24-57`） |
| 约束 | 同一 connectionId 重复 OFFER 时旧连接完整 Close 后新连接接管（`back/peerjs/peer.go:227-240`）；一远端可有多条连接，`ConnectedPeers()` 按 PeerID 去重且只计 open（`back/peerjs/peer.go:94-106`） |

**信令侧身份与连接**（`peerJSSignaller`，`back/peerjs/peer.go:352-369`）：

| 字段 | 默认值 / 约束 |
|---|---|
| `id` | 显式传入或服务端分配；合法规则：1–256 字符、首尾必须字母数字、中间允许 `- _ 空格`（`back/peerjs/peer.go:299-317`） |
| `token` | 空则 `randHex(16)` 随机（`back/peerjs/peer.go:345-347`） |
| `conn` / `connected` | WS 连接与标志，断线/关闭时置空（`back/peerjs/peer.go:446-466,524-540`） |

**Options 配置默认值**（`DefaultOptions`，`back/peerjs/peer.go:48-57`；补默认见 `normalizeOptions`，`back/peerjs/peer.go:332-349`）：

| 项 | 默认值 |
|---|---|
| Host / Port / Secure | `peersignal.moonchan.xyz` / `443` / `true`（项目公共信令，`README.md:116`；不是 0.peerjs.com） |
| Path / Key | `/` / `pd-signal-b9447b406828e500` |
| PingInterval | 5s |
| ICEServers | 由上层注入（`back/internal/transport/peerjs_service.go:123,204`） |

**关键常量**（`back/peerjs/connection.go:17,154`、`back/peerjs/peer.go:22,449,512,429`）：`defaultBufferLowThreshold=512*1024` 字节；流控等待上限 30s；信令帧读上限 1MB；WS 写超时 15s；WS 握手超时 15s；协议 version `"1.5.4"`。

## 5. 边界与坑

- **文本帧 = JSON 控制头，二进制帧 = 数据块，不能互换**：`SendText` 发头（PPID 51）、`Send` 发块（PPID 53）；发反了对端把控制头当数据块吞掉（`back/peerjs/connection.go:91-113`；回归测试 `back/peerjs/peer_test.go:293-322`）。
- **`SendFrame` 原子性不可破坏**：头+体由 `sendMu` 串行落线，并发发送交织会把数据块挂到错误请求上（`back/peerjs/connection.go:125-132`；暴力验证 `back/peerjs/peer_test.go:324-360`）。
- **`OnBufferedAmountLow` 是替换式回调**：只能在 `attach` 注册一次（`lowWater` 广播、容量 1 防堆积）；每个并发发送方各自注册只有最后一个能收到事件，其余死等——曾导致并发 serveFile 卡死（`back/peerjs/connection.go:12-17,297-304`；`back/peerjs/flowcontrol_test.go:8-47`）。
- **流控等待必须可中断**：连接关闭立即退出等待，慢消费者 30s 超时封顶（`back/peerjs/connection.go:152-163`；`back/peerjs/flowcontrol_test.go:49-77`）。
- **connectionId 由 offerer 定义、answerer 必须沿用**：曾因 answerer 新生成 ID 导致 ANSWER 路由不到、ICE 卡 checking（`back/peerjs/connection.go:222-224`；`back/peerjs/peer_test.go:83-115`）。
- **ICE 候选必须手动经信令转发**：pion 不会自动发候选，否则双方停在 checking 永远连不上（`back/peerjs/connection.go:252-265`）。
- **重复 connectionId 的 OFFER 必须完整 Close 旧连接**（只关 pc 会泄漏：残留 conns map、done 永不关闭、onClose 不触发）；且必须在解锁 `p.mu` 后调用（Close→forgetConnection 需要同一把锁，Go mutex 非重入，持锁调用死锁）（`back/peerjs/peer.go:227-240`；`back/peerjs/peer_test.go:117-152`）。
- **`handleLeave` 先锁内收集、解锁后再逐个 Close**：同样因 Go mutex 非重入（`back/peerjs/peer.go:256-269`；`back/peerjs/peer_test.go:181-212`）。
- **`EXPIRE`（OFFER 入队过期）必须 Close 连接**，触发 `done` 让上层 `connectLoop` 重连，否则永远等 OnOpen（`back/peerjs/peer.go:191-200`；`back/peerjs/peer_test.go:154-179`）。
- **Close 幂等**：`Peer.Close`/`Connection.Close`/`signaller.Close` 三处都有 channel 关闭逻辑，靠 `closeOnce` 与 closed 判空防 repeat close panic（`back/peerjs/peer.go:144-163`、`back/peerjs/connection.go:178-198`；`back/peerjs/peer_test.go:239-272,378-404`）。
- **answerer 的 `OnConnection` 回调在 DataChannel open 之前触发**：上层必须等 `OnOpen` 再绑定消息处理，过早使用报 "connection not open"（`back/internal/transport/peerjs_service.go:471-481`；`back/peerjs/peer_test.go:365-376`）。
- **远端主动关 dc 必须接线清理**：`pionChannel.OnClose` → `c.Close()`，否则连接悬挂靠 ICE disconnected 兜底（秒~分钟级，太慢）（`back/peerjs/connection.go:323-327`；`back/peerjs/peer_test.go:406-426`）。
- **信令读上限 1MB**：信令消息（SDP/ICE）本身体积很小，设限防云端被攻破回超大帧 OOM（`back/peerjs/peer.go:446-449`）。
- **WS 并发写必须串行**：`writeMu` 保护 `WriteJSON`（心跳/ICE 候选/ANSWER 多 goroutine 并发发送会让 gorilla panic，3 节点集成测试真实触发过）（`back/peerjs/peer.go:365-368,502-514`）。
- **`ID-TAKEN`/`ERROR` 记日志**：两节点同 ID 双双失联且无声无息是配置错误，重连无解，只能靠日志排查（`back/peerjs/peer.go:201-213`）。
- **`NewPeerWithSignaller` 路径必须调 `SetICEServers`**：否则 WebRTC 只有局域网 host 候选、无法跨公网打洞（`back/peerjs/peer.go:108-115`；`back/peerjs/peer_test.go:274-289`）。
- **并发安全**：`dcMu` 保护 dc 读写（attach 无锁写 vs Open/Send/Close 并发读，-race 必现）；`handlerMu` 保护三个回调字段（注册方 vs pion 回调 goroutine 快照读）（`back/peerjs/connection.go:30-53`；`back/peerjs/peer_test.go:452-496`）。
- **对上层的约束**：信令断线（H7）靠 `Done()` 通道通知，上层必须整轮重建 Peer；心跳空转无意义时立即退出（`back/peerjs/peer.go:483-500` 注释）。

## 6. 对外连接

- [transport ↔ PeerJS](../connections/07-transport-peerjs.md)：本模块与 transport 层的核心接线——`PeerJSService` 经 `NewPeer/Connect/OnConnection/SendFrame` 使用本库承载节点互联与文件服务，数据面帧协议（文本头+二进制块）由 transport 层定义、本库只保证传输原语。
- [transport ↔ signalserver](../connections/08-transport-signalserver.md)：本库作为信令客户端经 WS（`/peerjs?key=&id=&token=&version=`）连信令服务器，OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE/HEARTBEAT 双向流转；信令只转发 SDP/ICE、不碰数据面。
- [frontend ↔ signalserver](../connections/12-frontend-signalserver.md)：浏览器端 peerjs 以同一协议连同一信令，是 `OFFER → ANSWER → CANDIDATE` 协商流程的对端（浏览器客户端）实现；本库需与其 `serialization: "raw"` 互通（`back/peerjs/connection.go:345`）。
- [media-node ↔ ech](../connections/13-media-node-ech.md)：`back/cmd/media-node`（`back/cmd/media-node/main.go:46`）用本库注册信令并承载到浏览器的媒体 DataChannel，帧协议（`url/meta/二进制块/done/err` + keepalive）与 peerdrive 主帧协议同族但独立（`back/cmd/media-node/main.go:20-28`）。

> 备注：frontend↔backend 本地 WS 会话（[01-frontend-backend.md](../connections/01-frontend-backend.md)）与本模块**不直接相关**——`BindLocal` 走本地 `WSSession`（`back/internal/transport/ws_session.go:31-34`），不经本库信令，只复用同一 `Session` 接口与帧协议。