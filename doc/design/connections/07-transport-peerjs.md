# 连接 07：transport ↔ peerjs（协议引擎）

- **涉及模块**：`../modules/09-transport.md` 与 `../modules/10-peerjs.md`
- **代码位置**：A 侧 `back/internal/transport/`（`peerjs_service.go` 装配层 + `conn.go` 帧协议/分派 + `rtc_session.go` 适配器 + `ws_session.go` 的 `Session` 接口）；B 侧 `back/peerjs/`（`peer.go` 信令客户端 / `connection.go` DataConnection / `message.go` 消息与负载 / `signaller.go` 抽象 / `transport.go` Frame 与 DataChannel 抽象）；库装配点 `back/go.mod:7,160`（`require github.com/Hana-ame/go-peerjs v0.0.0` + `replace ... => ./peerjs`）；服务装配点 `back/cmd/server/main.go`（`peerjsSvc.Start()/Close()`，见 `../modules/09-transport.md` §1 与 `如何连接.md` 启动时序）
- **方向**：双向（进程内调用 + DataChannel 全双工；offerer/answerer 无方向之分，同一条 Session 同时承载入站与出站角色，`peerjs_service.go:13-14`）

## 1. 连接方式

**通道类型：进程内对象图调用（两层）**。transport 与 peerjs 编译进同一二进制（`back/go.mod:7,160` replace 本地目录），PeerJSService 直接持有 `*peerjs.Peer`（`peerjs_service.go:38-41,26`）。对外只有两条网络面，都由 peerjs 引擎建立、transport 消费：

- **信令面（控制，WSS）**：`peerjs.Peer` 经 `peerJSSignaller` 连 `wss://host:port/peerjs?key=&id=&token=&version=1.5.4`（`back/peerjs/peer.go:396-443`，URL 组装 410-427；version 常量 `peer.go:22`），收发 OFFER/ANSWER/CANDIDATE/LEAVE/EXPIRE/HEARTBEAT（`message.go:18-28`）。这条面与信令服务器的细节见 [08-transport-signalserver.md](08-transport-signalserver.md)，本连接只描述 transport 如何驱动它。
- **数据面（DataChannel）**：`peerjs.Connection` 封装一条 WebRTC DataConnection（`connection.go:19-57`），SDP/ICE 经信令面交换后，数据走 pion DataChannel；`Connection` 只依赖 `DataChannel` 接口（`transport.go:15-26`），不绑定 pion 具体类型。

**数据面桥（A 侧适配器）**：`rtcSession`（`rtc_session.go:11-39`）把 `*peerjs.Connection` 适配为 transport 的 `Session` 接口（`ws_session.go:22-29`：`ID()/SendJSON/SendFrame/OnMessage/OnClose/Close`）。适配理由（`rtc_session.go:7-10`）：`Connection.ID` 是信令路由键（connectionId），与会话标识（远端 peer id）语义不同且字段名冲突，adapter 在 service 层收敛差异。关键映射：`ID()` = `c.PeerID`（远端 id，`rtc_session.go:20`）；`ConnID()` = `c.ID`（连接级 UUID，**两端可见同一值**，`rtc_session.go:24`，同 peer 去重键，见 `conn.go:196-226`）；`SendJSON/SendFrame` 直通 `Connection`（26-28）；`OnMessage` 传 `peerjs.Frame`（30）；`DataChannel()` 暴露底层供 serveFile 写缓冲流控（38-39，WSSession 无此能力）。

**协议帧格式**（数据面，库定义传输原语、业务帧由 transport 定义）：

- `Frame{IsText, Data}`（`transport.go:5-10`）：`IsText=true` 为文本帧（JSON 控制头，SCTP PPID 51），`false` 为二进制帧（数据块，PPID 53）——`Connection.Send` 发二进制、`SendText` 发文本（`connection.go:91-113`），发反了对端把控制头当数据块吞掉。
- `SendFrame` 原子发送「JSON 头 + 紧随的二进制体」（`connection.go:125-166`）：`sendMu` 串行保证并发下头体不交织；内置写缓冲流控（`bufferedAmount > 512KB` 时等低水位广播，慢消费者 30s 超时，连接关闭立即退出）。流控回调在 `attach` 时注册一次（`connection.go:12-17,297-304`）——pion `OnBufferedAmountLow` 是替换式回调，并发注册会互相覆盖死等。
- 业务帧协议（req/meta/data/done/err + create/upload/list/info/delete/sync + admin/fwd-*/psk-*）由 transport 定义在 `conn.go:13-26` 头注释；库不管语义（`back/peerjs/README.md:126-136`）。

**鉴权方式**：信令侧靠 URL query 的 `key` + `id` + `token`（`peer.go:410-427`；token 未指定时随机生成，`peer.go:345-347`）；`ID-TAKEN`/`ERROR` 只记日志（`peer.go:201-213`）。数据面准入由 transport 层的 PSK 门禁承载（`psk.go`）——`psk-auth` 是连接建立后本端经本连接发出的**第一帧**（`psk.go:57-71`，必须先挂 OnMessage 再发，`conn.go:250-259`）；库本身无业务鉴权，数据面加密由 WebRTC 强制 DTLS 保证（`psk.go:16-17`）。

**何时/由谁建立**：进程启动 `main` 调 `peerjsSvc.Start()`（`peerjs_service.go:147-149`）→ goroutine `startLoop`（184-311）内 `peerjs.NewPeer(s.id, opts)`（205）+ `p.OnConnection(s.onIncomingConnection)`（206）+ `p.Dial(s.ctx)`（210）；信令连通后按配置 `PEERDRIVE_PEERJS_PEERS` 与 `extraPeers()` 逐对端 `connectLoop`（225-243），发现回调 `onDiscoveredPeer` 也触发拨号（326-341，受 MAX_PEERS 预算，`345-363`）。每条远端连接由 `connectLoop`（主动，offerer）或 `onIncomingConnection`（被动，answerer）建立，两条路径都只是创建一条全双工 Session 并经 `bindConn` 挂同一套角色（`peerjs_service.go:13-14`）。

## 2. 时序

### 2.1 连接建立时序（A 主动拨号 B，offerer/answerer 协商）

```mermaid
sequenceDiagram
  participant TA as transport A (PeerJSService)
  participant PA as peerjs Peer A (offerer)
  participant S as 信令服务器
  participant PB as peerjs Peer B (answerer)
  participant TB as transport B (PeerJSService)

  TA->>PA: Start→startLoop: NewPeer + OnConnection + Dial (peerjs_service.go:205-210)
  PA->>S: wss://.../peerjs?key=&id=&token=&version= (peer.go:410-443)；此后每5s HEARTBEAT
  TA->>PA: connectLoop: Connect(ctx, peerID, "peerdrive") (peerjs_service.go:431)
  PA->>PA: newConnection: NewPeerConnection + CreateDataChannel(ordered) + makeOffer (connection.go:225-286,330-348)
  PA->>S: OFFER {connectionId,label,serialization:"raw",sdp} (connection.go:339-347)
  S->>PB: 按 dst 转发 OFFER
  PB->>PB: route→handleOffer：沿用 offerer 的 connectionId 建连接 (peer.go:217-254)
  PB->>PB: SetRemoteDescription + CreateAnswer (connection.go:350-364)
  PB->>S: ANSWER {connectionId,sdp}
  S->>PA: 转发 ANSWER
  PA->>PA: conn.handleMessage: SetRemoteDescription (peer.go:179-190; connection.go:203-217)
  PA->>S: CANDIDATE（OnICECandidate 手动转发，connection.go:252-265）
  S->>PB: CANDIDATE
  PB->>PB: AddICECandidate (connection.go:211-216)
  PB->>S: CANDIDATE（反向）
  S->>PA: CANDIDATE
  Note over PA,PB: ICE 连通 → DataChannel open
  PA->>TA: dc.OnOpen→OnOpen 回调→bindConn(newRTCSession(c)) (peerjs_service.go:442-450)
  PB->>TB: 同左（必须等 OnOpen 再 bind，peerjs_service.go:471-481）
  TA->>TA: bindConn: conns/dedup/connState/先挂OnMessage/pskSendAuth (conn.go:228-269)
  TB->>TB: 同左
```

逐步骤说明（代码依据）：

1. **信令注册**：`startLoop` 构造 `opts`（Host/Port/Secure/Key 取配置，缺省用 `DefaultOptions`：公共信令 `peersignal.moonchan.xyz:443`、key `pd-signal-...`、PingInterval 5s，`peer.go:48-57`；配置覆盖 `peerjs_service.go:190-204`），`NewPeer`（`peer.go:61-71`）后 `Dial`（`peer.go:124-126` → `peerJSSignaller.Dial` 396-408 → `dialWS` 410-443）。注册失败（含取 ID 失败）→ `startLoop` LogWarn 后 backoff 2s 起指数退避重试（`peerjs_service.go:210-221`）。
2. **拨号（offerer 侧）**：`connectLoop`（`peerjs_service.go:400-469`，`connecting` map 去重 407-418）调 `peer.Connect(s.ctx, peerID, "peerdrive")`（431）→ `newConnection(dst, label="peerdrive", offered=true, ...)`（`peer.go:136-141`；`connection.go:225-286`）：建 `PeerConnection`、`CreateDataChannel(label, Ordered=true)`（272-279）、`attach` 绑回调、`makeOffer` 发 OFFER（330-348）。
3. **应答（answerer 侧）**：信令 `route` 收到 OFFER → `handleOffer`（`peer.go:217-254`）用 **offerer 的 connectionId** 建 answerer 连接（沿用规则是协议硬约束，`connection.go:222-224`），`SetRemoteDescription` + `CreateAnswer` 回 ANSWER（`connection.go:350-364`）；answerer 的 `onConn`（即 `onIncomingConnection`）在此时被回调——但 **DataChannel 尚未 open**，必须等 `OnOpen` 再 `bindConn`（`peerjs_service.go:471-481` 注释）。
4. **ANSWER/CANDIDATE 回流**：offerer 的 `route` 按 `connectionId` 找到连接交 `conn.handleMessage`（`peer.go:179-190`；`connection.go:203-217`，SDP/候选解析错误静默忽略）。ICE 候选必须**手动**经信令转发（pion 不自动发），`OnICECandidate` → `CANDIDATE` 消息（`connection.go:252-265`）。
5. **DataChannel open → 绑定**：`attach` 注册的低水位/onOpen/onMessage/onClose 回调触发（`connection.go:291-328`）；`OnOpen` 回调（`peerjs_service.go:442-450` 的闭包）执行 `bindConn(newRTCSession(c))`。offerer 侧同样流程（`OnOpen` 由 `connectLoop` 注册）。
6. **bindConn 装配**（`conn.go:228-269`）：`conns[c.ID()]=c` 登记（228-232）→ 同 peer 双连接去重 `dedupConn`（233-238；决策规则 `conn.go:196-226`，见 §3 重复/并发）→ 建 `connState`（`fetches/verbWaits/binCh/binDone/fwdCh`，239-245）→ **先挂 `OnMessage`/`OnClose` 再发 `psk-auth`**（250-259 顺序敏感：库在 onMessage 为 nil 时直接丢帧，open 瞬间到达的对端首帧可能被静默丢弃）→ 起 `uploadWorker`/`fwdWorker`（262-265）→ `pskSendAuth`（268）。
7. **建立完成**：`connectLoop` 等 `opened`（451-460），随后阻塞等 `conn.Done()`——连接死亡时重新拨号（456-467）；正常连接期间 transport 双向收发业务帧。

### 2.2 数据面帧往返时序（A 拉取 B 的文件，req → meta/data/done）

```mermaid
sequenceDiagram
  participant TA as transport A（出站角色）
  participant PA as Connection A (peerjs)
  participant PB as Connection B (peerjs)
  participant TB as transport B（入站角色）

  TA->>PA: openStream: SendJSON(req 帧) (outbound.go:234)
  PA->>PB: DataChannel 文本帧 PPID 51（JSON 头）(connection.go:106-123)
  PB->>TB: dc.OnMessage→Frame{IsText:true}→dispatchFrame (transport.go:49-53; conn.go:278-283)
  TB->>TB: pskGate 门禁 → type=req → go serveFile (conn.go:293-306)
  TB->>TB: serveFile：hash 校验/门禁/trace/多源路由 (inbound.go:66-104)
  loop 每 64KB 一块（inbound.go:23-25）
    TB->>PB: SendFrame(data 头 + 二进制块)（原子连续）(rtc_session.go:28; connection.go:132-166)
    PB->>PA: 文本帧头 + 二进制块（流控：bufferedAmount 高则等 lowWater）
    PA->>TA: dispatchFrame 二进制路由 → expect → fetchState.q（有界队列）(conn.go:403-415)
  end
  TB->>PB: SendJSON(done 帧) (inbound.go:…)
  PB->>PA: 文本帧 done
  PA->>TA: routeResponse: done.Size==received 完整性校验 → close(done) (outbound.go:461-475)
  TA->>TA: fetchReader drain q → EOF 前查 errCh → 全量请求校验 sha256 (outbound.go:324-379)
```

逐步骤说明（代码依据）：

1. **发起 req**：`openStream` 生成 UUID reqId（`outbound.go:202`，跨连接唯一路由键）、登记 `st.fetches[reqID]`（215-217）、`c.SendJSON(dcReq{...})` 发出（234）——`c` 即 `rtcSession` → `Connection.SendJSON` → `SendText`（`connection.go:117-123`）。
2. **对端接收**：pion DataChannel 消息 → `pionChannel.OnMessage` 包装成 `Frame{IsText: m.IsString}`（`transport.go:49-53`）→ `attach` 的 onMessage 回调快照调用（`connection.go:315-322`）→ transport `dispatchFrame`（`conn.go:278-425`）。
3. **分派**：文本帧 JSON 解析失败或 type 空 → 静默丢弃（`conn.go:281-283`）；`psk-auth`/`psk-ok`/`psk-err` 先处理（286-292）；`pskGate` 拦未出示密钥的入站 verb（293-295）；`req` 帧 → `go serveFile`（297-306）。
4. **应答数据**：`serveFile` 校验 hash（`inbound.go:67-70`）→ ShareGate（73-77）→ trace 防环（79-86）→ 多源路由/本地读取（93-154），按 `chunkSize=64KB` 分块循环 `c.SendFrame(dcResp{type:"data",...}, 块)`（`inbound.go:23-25`；头+体原子连续由 `Connection.SendFrame` 保证，`connection.go:132-166`），最后发 `done` 帧。
5. **出站收集**：A 侧 pump 收二进制块，按 `fwd.pending → pendingUpload → adminUp → expect` 优先级路由（`conn.go:360-424`），投递到 `fetchState.q`（有界 8，`outbound.go:205`）；`done` 帧经 `routeResponse`（`outbound.go:426-479`）做完整性校验（`done.Size` 必须等于已收字节，471-474）后 `close(f.done)`。
6. **消费**：`fetchReader.Read` 从 q 逐块消费（`outbound.go:290-361`），done 后先 drain 剩余块再 EOF（324-349），EOF 前非阻塞查 errCh（340-346）；全量请求（offset==0 且 size<0）EOF 时重算 sha256 比对（`outbound.go:242,364-379`）。
7. **流控闭环**：对端（B）发数据块时若 `bufferedAmount > 512KB`，`SendFrame` 等待 `lowWater` 广播（`connection.go:152-166`）；A 侧队列满时 pump 投递阻塞（`conn.go:407-413`，`f.closed` 可放行）——两端共同构成有界背压。

## 3. 情况处理

| 异常/边界场景 | 行为与依据（代码位置） | 说明 |
|---|---|---|
| **超时** | 信令侧：WS 握手 15s（`peer.go:429`）、取随机 ID 的 HTTP client 15s（`peer.go:560`）、信令写 15s（`peer.go:512`）；心跳发送失败立即退出（`peer.go:483-500`）。连接建立：`connectLoop` 等 `opened` 30s 超时 → `conn.Close()` 进重连（`peerjs_service.go:461-464`）。数据面：`SendFrame` 慢消费者 30s 封顶（`connection.go:152-166`）；transport 侧「等对端响应」超时另行分层——fetch 块间隔 5min（`outbound.go:247-250,294-304`）、一次性 verb 15s（`outbound.go:26-29,74-76`）。 | 库只管「单次发送/单次协商」的硬上限；「等待对端业务响应」的超时由 transport 层按流式/一次应答区分，避免网络抖动误报。 |
| **断连 / 重连（信令）** | 信令 WS 断：`readLoop` 网络错误/EOF 退出 → `close(s.done)`（`peer.go:446-466`，H7 修复）→ `startLoop` 的 `p.Done()` 分支：`p.Close()`、backoff 2s↔60s 指数翻倍后**整轮重建 Peer**（`peerjs_service.go:285-309`）。 | 旧 Peer 的所有连接随 `Peer.Close` 关闭（`peer.go:144-163`）；重建后按配置/extraPeers 重新 `connectLoop`（`peerjs_service.go:222-243`）。 |
| **断连 / 重连（数据连接）** | 触发关闭的四个来源：OFFER 过期 `EXPIRE` → `conn.Close()`（`peer.go:191-200`）；对端下线 `LEAVE` → 关闭该远端全部连接（`peer.go:256-269`）；ICE `closed/failed/disconnected` → `conn.Close()`（`connection.go:244-251`）；远端关 dc → `pionChannel.OnClose` → `c.Close()`（`connection.go:323-327`）。`conn.Done()` 后 `connectLoop` 循环重拨（`peerjs_service.go:451-468`）；`bindConn` 的 `OnClose` → `cleanupConn` 清 `conns/pending` 并放行 worker（`conn.go:430-477`）。 | 库只发断线通知（`Done()` 通道 + `OnClose`），重连策略全在上层 `startLoop`/`connectLoop`（`peerjs_service.go:400-469` 注释）。 |
| **重复 / 并发** | 同一 peer 重复拨号：`connecting` map 去重（`peerjs_service.go:406-418`）、`EnsureConnection` 幂等（518-529）、发现回调先查 `conns`（330-335）。同 peer 双连接（双向互拨/重连竞态）：`conns` 按 peerID 键 + `dedupConn` 按连接级 UUID 字典序**小者胜**、两端一致（`conn.go:196-226`；淘汰连接锁外 Close，清理带 `s.conns[c.ID()]==c` 值相等守卫）；`local` WS 会话不去重（`conn.go:204-205`）。重复 connectionId 的 OFFER：旧连接完整 Close 后接管（`peer.go:227-240`）。并发发送：`SendFrame` sendMu 串行 + 单例 lowWater（`connection.go:12-17,132-166,297-304`）；信令 `writeMu`（`peer.go:365-368,502-514`）；回调字段 `handlerMu` 快照、dc 访问 `dcMu`（`connection.go:30-53`）。拉取撞去重窗口：`FetchFromPeer` 恒最多 2 次重试（`outbound.go:141-181`）。 | 双向互拨是正常拓扑（双方同时发现对方），去重必须两端同规则，否则保留的恰是对方已关闭的断链（`conn.go:206-212` 注释）。 |
| **数据缺失或校验失败** | 发送前先查 `dc.Open()`，未 open 报 `peerjs: connection not open`（`connection.go:97-101,143-145`）。信令消息 JSON 解析失败 → 丢弃继续（`peer.go:472-475`）；SDP/候选解析失败静默忽略（`connection.go:203-217`）。数据面文本帧解析失败/type 空 → 静默丢弃（`conn.go:281-283`）；二进制块无归属（无 fwd/pendingUpload/expect）→ 丢弃（`conn.go:403-415`）。对端声明超限：meta total / data size > 8GB 拒绝（`outbound.go:112-115,448-458`）；`done.Size` 必须等于已收字节，否则报 incomplete transfer（防提前 done 把截断文件当成功，`outbound.go:461-475`）。全量拉取 EOF 重算 sha256（`outbound.go:364-379`）。 | 「发出去」与「收回来」的完整性校验分居两端：库保证头体原子与有界流控，transport 保证业务级字节数/哈希校验。 |
| **鉴权失败** | 信令侧：`ID-TAKEN`/`ERROR` 只记日志（`peer.go:201-213`，同 ID 是配置错误，重连无解）；`Connect` 对空 dst 报错（`peer.go:137-139`）。数据面：PSK 门禁在 transport 侧——`psk-auth` 必须早于业务帧到达（`psk.go:57-71`；顺序保证见 `conn.go:250-259`）；校验失败回 `psk-err` 且**不关连接**（让对端重发，`psk.go:76-90`）；未出示密钥的入站 verb 回 `PSK_REQUIRED` err（`psk.go:94-110`）。 | 库无业务鉴权概念：它只保证传输；准入长在本连接的 transport 侧（`psk.go:3-7` 注释——信令只牵线不做准入）。 |
| **半开状态** | 写侧：`SendFrame` 流控等待被 `done`/30s 打断（`connection.go:152-166`）。读侧：远端关 dc → 立即清理（`connection.go:323-327`）；ICE `disconnected/failed` → `Close`（`connection.go:244-251`）。信令半开：`readLoop` 退出即 `done`、心跳随之停止（`peer.go:446-466,483-500`）。连接建立半开：answerer 未 open 即被使用会报 "connection not open"（必须等 OnOpen 再 bind，`peerjs_service.go:471-481`）；`connectLoop` 30s 兜底（`peerjs_service.go:461-464`）。 | 无显式 keepalive 帧：信令面靠 HEARTBEAT，数据面靠 ICE 状态机 + 写侧低水位活性 + 30s 流控超时兜底。 |
| **进程重启** | 全部连接态在内存（`Peer.conns`/`Connection`/`conns`/`pending`，见模块 10 §2）；重启后 `startLoop` 重新 `NewPeer` + `Dial`（`peerjs_service.go:184-210`）。身份：节点 id 不落盘，未设 `PEERDRIVE_PEERJS_ID` 每次启动换 id（`peerjs_service.go:113-117,548-552`）；token 每次随机（`peer.go:345-347`）。优雅关停：`Close()` → cancel → 关全部 conns → `peer.Close()`（`peerjs_service.go:152-182`）→ 对端收到 LEAVE/ICE closed 走各自清理。 | 无「恢复会话」概念；对端视角=一次断连重连。静态对端由配置 PEERS/extraPeers 重拨（`peerjs_service.go:225-243`），上传断点续传靠文件大小重建位图（模块 09 §2C，不在本连接范围）。 |

## 4. 相关文档

- 连接文档（同目录）：
  - [08-transport-signalserver.md](08-transport-signalserver.md)：本连接第 1 步的信令面——注册/HEARTBEAT 保活/OFFER/ANSWER/CANDIDATE 经 WS 流转（`peer.go:396-443,483-500`），只转发 SDP/ICE、不碰数据面。
  - [01-frontend-backend.md](01-frontend-backend.md)：本地 `WSSession` 与 `rtcSession` 实现同一 `Session` 接口、复用同一帧协议（`ws_session.go:22-29,31-34`）；`BindLocal` 与 DataChannel 连接共用 `bindConn` 分派（`peerjs_service.go:313-319`）。
  - [12-frontend-signalserver.md](12-frontend-signalserver.md)：浏览器端 peerjs 是 OFFER→ANSWER→CANDIDATE 协商的对端实现，本库需与其 `serialization:"raw"` 互通（`connection.go:339-346`）。
  - [06-service-transport.md](06-service-transport.md)：上游——`EnsureConnection`/`extraPeers`（节点市场「加入节点」）触发本连接的 `connectLoop`（`peerjs_service.go:512-529`）。
  - [05-router-source.md](05-router-source.md)：serveFile 多源路由 `FileRouter` 经 `Session.SendFrame` 回数据帧（`inbound.go:46-51,93-104`）。
  - [11-transport-storage.md](11-transport-storage.md)：本连接数据面帧的最终去向——拉取/上传落盘与 file_index 登记。
  - [13-media-node-ech.md](13-media-node-ech.md)：同一 `go-peerjs` 库、独立帧族（url/keepalive），无数据面交互。
- 模块文档：`../modules/09-transport.md`（会话状态机、帧协议与 3 条硬约束、连接生命周期、H5/H6/M5-M9 修复、内存态清单）、`../modules/10-peerjs.md`（三层结构 Signaller/Peer/Connection、信令协议细节、流控与单例低水位、边界与坑 13 条）。
