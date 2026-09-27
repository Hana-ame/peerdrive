# 连接 08：transport ↔ signalserver（信令发现）

- **涉及模块**：`../modules/09-transport.md` 与 `../modules/11-signalserver.md`
- **代码位置**：A 侧 `back/internal/transport/`（`peerjs_service.go` 信令生命周期 + 发现装配；`http_discovery.go` / `mqtt_discovery.go` 发现客户端）+ `back/peerjs/`（go-peerjs 信令客户端库，模块 10）；B 侧 `back/signalserver/signalserver.go`（+ 装配 `back/signalserver/cmd/peersignal/main.go`）
- **方向**：双向。分两条面：**信令面**（A↔B：节点注册 WS、OFFER/ANSWER/CANDIDATE 按 dst 转发、LEAVE、HEARTBEAT 双向流转，`signalserver.go:10-15`）；**发现面**（以 A→B 的 HTTP 查询为主：announce 上报 + nodes 轮询，B 回在线节点列表回流 A 触发互联）

## 1. 连接方式

本连接是「一条信令 WS + 一条发现 HTTP」的叠加，均由 A 侧（节点端）主动建立：

### 1.1 信令面（节点注册 + 房间转发）——WebSocket，PeerJS 兼容协议

- **通道**：A 侧经 go-peerjs 库的 `peerJSSignaller`（`back/peerjs/signaller.go:12-30` 的 `Signaller` 接口实现，`back/peerjs/peer.go:351-369`）连 B 侧 `HandleWS`（`back/signalserver/signalserver.go:246-296`）。
- **URL/参数**：`wss://{host}:{port}/{path}peerjs?key=&id=&token=&version=`（`back/peerjs/peer.go:410-427`；`path` 默认 `/`，`peer.go:336-338`）。`key` 来自 `PEERDRIVE_PEERJS_KEY`（默认 `pd-signal-b9447b406828e500`，`back/internal/config/config.go:20,212`）；`id` 来自 `PEERDRIVE_PEERJS_ID`，未配置时生成本地随机 `peerdrive-<randHex8>`（32bit，`back/internal/transport/peerjs_service.go:113-117,548-552`）；`token` 客户端随机生成（`back/peerjs/peer.go:345-347,372-374`）。
- **协议帧**：`{type, src, dst, payload}` 文本 JSON，**服务端覆盖 src** 为连接者 id（`back/signalserver/signalserver.go:12,309`）；类型 `OPEN/LEAVE/OFFER/ANSWER/CANDIDATE/EXPIRE/HEARTBEAT/ID-TAKEN/ERROR`（`back/peerjs/message.go:18-28`）。本连接只转发 SDP/ICE，**不碰数据面**——WebRTC DataChannel 建立后节点间直连，不再经信令（`back/internal/transport/peerjs_service.go:3-14` 头注释；`back/peerjs/peer.go:27-29`）。
- **鉴权**：三把锁依次在 WS 升级处校验——① `id/token/key` 缺一 → HTTP 400（`signalserver.go:250-253`）；② `key != s.key` → 400（`signalserver.go:254-257`）；③ token 白名单启用（`cmd/peersignal/main.go:25,31-33` 的 `-tokens` → `WithTokenWhitelist`，`signalserver.go:57-75`）且 token 不在名单 → 400（`signalserver.go:258-263`）。白名单空 = 不限制（默认，兼容公共部署，`signalserver.go:57-64`）。
- **建立时机**：由 A 侧 `PeerJSService.Start()` 发起（`peerjs_service.go:147-149`）→ `startLoop` 内 `peerjs.NewPeer(s.id, opts)` + `p.Dial(s.ctx)`（`peerjs_service.go:205-210`）；`opts` 从配置填充 Host/Port/Secure/Key，空值回落默认信令（`config.DefaultSignalHost/Port/Key`，`peerjs_service.go:190-204`）。信令断线由 startLoop 整轮重连（H7，见 §3 断连/重连）。

### 1.2 发现面（announce + discover/nodes）——HTTP REST，公开无鉴权

- **通道**：A 侧 `HTTPDiscovery`（`back/internal/transport/http_discovery.go`）→ B 侧发现 API（`signalserver.go:449-638`）；信令连接成功后创建，跨信令重连常驻不重建（`peerjs_service.go:245-272` 及头注释）。
- **接口**：
  - `POST {baseURL}/discover/announce`：body `{peerId, collections[], nodeType:"go-persistent", loadInfo, peers}`（`http_discovery.go:103-117`；服务端 `HandleAnnounce` `signalserver.go:449-528`）；
  - `GET {baseURL}/discover/nodes?coll=<hash>`：按集合查在线节点，回 `{nodes:[{peerId,lastSeen,nodeType,loadInfo,collections}], links:[...]}`（`http_discovery.go:136-170`；服务端 `HandleNodes` `signalserver.go:568-638`）；
  - 服务端另提供 `POST /discover/leave`（`signalserver.go:531-559`）、`GET /peerjs/id`（`signalserver.go:236-243`）、`/status`、`/` dashboard（`signalserver.go:672-752`；装配 `cmd/peersignal/main.go:37-47`）——**节点客户端不调用 leave**（全 transport 无引用，下线靠心跳过期），浏览器面板复用 `/peerjs/id` 与 `/discover/nodes`（见连接 12）。
- **鉴权**：发现端**公开、不鉴权**——「发现的目的就是让任何人找到节点，白名单只约束信令面」（`signalserver.go:62-64` 注释）；所有 REST 端点经 CORS 全放开 `Access-Control-Allow-Origin: *` 并短路 OPTIONS 预检（`signalserver.go:209-234`）。
- **兜底旁路**：`DiscoverURL` 为空时才启用 MQTT 发现（`peerjs_service.go:273-283`）——`MQTTDiscovery` 连公共 broker、按 collection hash 分片 topic 订阅/发布（`back/internal/transport/mqtt_discovery.go:64-99,119-172`），**不经 signalserver**；HTTP 优先 MQTT 的装配在 `peerjs_service.go:245-283`。HTTP 发现的在线节点经 `onDiscoveredPeer` → 拨号预算检查 → `connectLoop` 互联（`peerjs_service.go:326-341`）。

## 2. 时序

### 2.1 信令面：节点注册与 OFFER 房间转发

```mermaid
sequenceDiagram
  participant N as 节点 PeerJSService
  participant L as go-peerjs 库 (back/peerjs)
  participant SS as signalserver (HandleWS/readLoop/route)

  N->>L: Start→startLoop: NewPeer(s.id,opts)+p.Dial (peerjs_service.go:205-210)
  L->>SS: WSS /peerjs?key=&id=&token=&version= (peer.go:410-427)
  SS->>SS: 校验 id/token/key、key、token 白名单 (signalserver.go:249-263)
  SS-->>L: OPEN (signalserver.go:292)
  SS->>SS: flushQueue 补发离线队列 (293,405-416)
  loop 每 5s (peer.go:482-500)
    L->>SS: HEARTBEAT（服务端续 60s 读超时，signalserver.go:312-313）
  end
  N->>L: connectLoop→peer.Connect(ctx,peerID,"peerdrive")→makeOffer (peerjs_service.go:431; connection.go:331-347)
  L->>SS: OFFER {dst, sdp, connectionId} (NewMessage MsgOffer)
  alt dst 在线
    SS->>SS: route 转发（出锁后写，10s 写超时，signalserver.go:327-336）
    SS->>L: OFFER（answerer 侧 handleOffer 回 ANSWER）
  else dst 不在线
    SS->>SS: 入队 queues[dst]，TTL 30s、每 dst ≤100 条 (signalserver.go:343-355,77-80)
  end
  L-->>SS: ANSWER / CANDIDATE（按 connectionId 路由，peer.go:179-190）
  L->>L: DataChannel open → OnOpen → bindConn(newRTCSession) (peerjs_service.go:442-450)
  L->>N: conns[peerID]=Session；之后数据面 P2P 直连不再经信令
```

**逐步骤说明**：

1. **注册发起**：`Start()` 起 `startLoop` goroutine（`peerjs_service.go:147-149`）；循环首步按配置构造 `peerjs.Options`（Host/Port/Secure/Key/ICEServers，空值回落 `config.DefaultSignalHost/Port/Key`，`peerjs_service.go:190-204`）→ `peerjs.NewPeer(s.id, opts)`（205）→ 注册被动连接回调 `p.OnConnection(s.onIncomingConnection)`（206）→ `p.Dial(s.ctx)`（210）。
2. **WS 连接与 id**：`peerJSSignaller.Dial` 在 `s.id` 非空时直接 `dialWS`；空 id 先 `GET /{path}peerjs/id` 向同一信令借随机 id（`back/peerjs/peer.go:396-407`；`retrieveID` 543-577，HTTP 超时 15s、响应限 256B、`validID` 校验）。`dialWS` 拼 `wss://host:port/peerjs?key=&id=&token=&version=`，`HandshakeTimeout 15s`（`peer.go:410-443`）。
3. **服务端受理**：`HandleWS` 依次校验 ① 三参数非空 ② key ③ token 白名单（`signalserver.go:249-263`）→ 升级（读限 40KB、初始 60s 读超时，`signalserver.go:272-275`）→ **ID 占用检查**：同 id 已在线且 token 匹配 → 关旧连接接管；不匹配 → 回 `ID-TAKEN` 并关闭（`signalserver.go:277-291`）→ 注册 `clients[id]`、回 `OPEN`、`flushQueue` 补发离线消息（`signalserver.go:288-295`）→ 起 `readLoop`（295）。
4. **心跳保活**：客户端 `heartbeatLoop` 每 `PingInterval`（默认 5s，`peer.go:55,342-344`）发 `HEARTBEAT`（`peer.go:482-500`，Send 失败即退出等重连）；服务端 `readLoop` 收到**任意消息**都更新 `cl.last` 并续 60s 读超时（`signalserver.go:309-314`），配合初始 60s deadline 兜底半开连接。
5. **OFFER 房间转发（主动拨号）**：`connectLoop` 持 `connecting` 去重 → `peer.Connect(ctx, peerID, "peerdrive")`（`peerjs_service.go:406-441`）→ 库内 `newConnection` 建 offerer Connection 并 `makeOffer` 发 `OFFER`（`back/peerjs/connection.go:225-264,331-347`）。服务端 `route`：`m.Src = cl.id` 后查 `clients[m.Dst]`——在线则出锁转发（写失败走 `handleDeadDst`，`signalserver.go:329-340,360-402`）；不在线且非 `LEAVE/EXPIRE`/空 dst → 入队（TTL 30s、每 dst 上限 100 条丢最旧，`signalserver.go:343-355`）。
6. **ANSWER/CANDIDATE 回流**：answerer 收到 OFFER → `handleOffer` 建 answerer Connection 回 ANSWER（`peer.go:217-240`；`connection.go:351-363`）；offerer 侧按 `connectionId` 路由 ANSWER/CANDIDATE 到对应 Connection（`peer.go:179-190`；`connection.go:203-223`）。期间 `EXPIRE`（OFFER 队列过期）关闭该连接让上层重连（`peer.go:191-200`）。
7. **连接就绪**：DataChannel open → `conn.OnOpen` → `bindConn(newRTCSession(c))`（`peerjs_service.go:442-450,471-481`）；此后同一条连接挂 inbound+outbound 帧角色，数据面 WebRTC 直连，**信令只承载后续新连接协商**（`peerjs_service.go:13-14` 注释）。

### 2.2 发现面：announce 登记 → nodes 轮询 → 互联

```mermaid
sequenceDiagram
  participant N as 节点 PeerJSService
  participant D as HTTPDiscovery
  participant SS as signalserver
  participant O as 对端节点（另一 transport 实例）

  N->>D: 信令 Dial 成功后：NewHTTPDiscovery(DiscoverURL,s.id,rooms,onDiscoveredPeer,peersFn)+Start（peerjs_service.go:257-272）
  D->>SS: POST /discover/announce（立即一次；http_discovery.go:85,103-117）
  SS->>SS: 限 8KB/≤64 集合 → 写 disc/peerColls/peerStats/peerLinks，旧集合即移除（signalserver.go:457-525）
  loop 每 30s 心跳 (http_discovery.go:87,94-95)
    D->>SS: POST /discover/announce（lastSeen 刷新 + peers 恒覆盖）
  end
  loop 每 10s 轮询 (http_discovery.go:86,92-93; 136-170)
    D->>SS: GET /discover/nodes?coll=<hash>（每个 collection 一次）
    SS-->>D: {nodes:[peerId,lastSeen,...], links}（heartbeatTTL 90s 剔除，signalserver.go:568-638）
    D->>D: 解码限 256KB；跳过空/自身/超长 id；seen 去重（http_discovery.go:148-163）
    D->>N: onPeer(peerID)（http_discovery.go:165-167）
    N->>N: conns 已连则跳过；discoveryDialAllowed 预算 → connectLoop（peerjs_service.go:326-341,345-363）
    N->>O: peer.Connect → OFFER（信令面转发，见 2.1 步骤 5-7）
  end
```

**逐步骤说明**：

1. **装配**：`startLoop` 在 `Dial` 成功后、拨完配置/extra 对端后，读 `s.cfg.DiscoverURL`（非空则 HTTP，`peerjs_service.go:257-272`）：`NewHTTPDiscovery(baseURL, s.id, discoveryRooms(), onDiscoveredPeer, peersFn)`——`peersFn` 取 `currentPeer().ConnectedPeers()` 供服务端画 graph（`peerjs_service.go:259-264`）；`SetShareInfo(s.shareLoadInfo)` 注入共享摘要读取器（267，无条件注册、晚注入也生效）；`Start()` 起循环（268）。房间列表 `discoveryRooms() = collectionHashes() + 可选 PresenceRoom`（节点级存在房间，`peerjs_service.go:365-381`；`PresenceRoom` 常量 `http_discovery.go:16-26`）。
2. **announce（节点登记）**：`loop()` 启动先 `announce()` 一次（`http_discovery.go:84-85`），之后 30s 心跳周期重复（`http_discovery.go:87,94-95`）。body：`{peerId, collections, peers(当前 WebRTC 直连), nodeType:"go-persistent", loadInfo}`（`http_discovery.go:103-110`；`loadInfo.shares` 只报数量不报 hash，`share.go:125-149`）。
3. **服务端登记**：`HandleAnnounce` 限 body 8KB（`signalserver.go:457`）、`collections` ≤64（469-473）、逐项 trim 去空串（474-481）；写 `disc[coll][peerID]=now`、**立即从旧集合移除**（483-503）、更新 `peerStats/peerColls`（505-514）、`peers` 恒覆盖 `peerLinks`（空也清旧边，515-524）；回 `{"ok":true}`（526-527）。
4. **nodes 轮询（发现）**：`discover()` 每 10s 对各 collection 发 `GET /discover/nodes?coll=<hash>`（`http_discovery.go:136-141`）；服务端按 `heartbeatTTL`(90s) cutoff 剔除过期节点、可选 `type` 过滤、回 nodes+graph links（两端都活跃的边、字典序去重，`signalserver.go:568-638`）。
5. **去重与回调**：客户端解码限 256KB（M15，`http_discovery.go:148-153`），跳过 `PeerID 为空/等于自身/>128`（155），按 `seen` map 只对新节点调 `d.onPeer(n.PeerID)`（154-167）。
6. **互联落地**：`onDiscoveredPeer`（`peerjs_service.go:326-341`）——空/自身跳过；`conns` 已含该 peer 跳过；`discoveryDialAllowed()` 预算不足（`MaxPeers`，默认 8，`peerjs_service.go:345-363`、`config.go:228`）跳过；否则 `go s.connectLoop(peerID)` 走 2.1 步骤 5-7 建 WebRTC 直连。
7. **剔除兜底**：节点若离线，30s 心跳停发 → 服务端 30s 周期 `sweepDiscovery` 按 90s `heartbeatTTL` 清除 `disc` 及 graph/元数据残留（`signalserver.go:82-93,114-160`）；存活节点下一轮 nodes 轮询自然看不到它。

## 3. 情况处理

| 异常/边界场景 | 行为与依据（代码位置） | 说明 |
|---|---|---|
| **超时** | ① 信令 WS 握手：`HandshakeTimeout 15s`（`back/peerjs/peer.go:429`）；借 id 的 HTTP `Timeout 15s` + 非 200 报错（`peer.go:556-568`）。② 服务端读超时：WS 初始 60s、每消息续期（`signalserver.go:272-275,312-313`），客户端 5s HEARTBEAT 续命（`peer.go:482-500`）；写超时服务端 10s（`signalserver.go:777`）、客户端 15s（`peer.go:512`）。③ 拨号握手超时：`connectLoop` 30s 内无 `OnOpen` → `conn.Close()` → sleepCtx 退避重试（`peerjs_service.go:451-467`）。④ 发现 HTTP：client `Timeout 10s`（`http_discovery.go:67`），announce/discover 失败仅 `LogDebug` 丢本轮、下个周期重试（`http_discovery.go:112-115,139-142`）。⑤ MQTT 连接 `WaitTimeout 15s`（`mqtt_discovery.go:95`）。 | 信令面超时靠 startLoop/connectLoop 的退避重连兜底（详见下行）；发现面超时静默跳过，心跳周期自愈。 |
| **断连 / 重连** | 信令 WS 断：客户端 `readLoop` 出错退出 → 关闭 `Signaller.Done()`（H7，`peer.go:446-465,460-464`）→ `startLoop` select 到 `p.Done()` → `p.Close()` + 退避后整轮重连（`peerjs_service.go:285-309`）；`Dial` 失败同样 LogWarn + 退避（`peerjs_service.go:210-221`）。backoff 2s 起步、失败翻倍、上限 60s（`peerjs_service.go:185,217-219,306-308`）。对端 WebRTC 断：`conn.Done()` → `connectLoop` 重连（`peerjs_service.go:455-460`）。**发现组件跨信令重连持续运行，不随 startLoop 重建**（`peerjs_service.go:246-249` 注释）。服务端侧：`removeClient`/`handleDeadDst` 向其余节点广播 `LEAVE`（`signalserver.go:388-392,426-430,444-446`），收方 `handleLeave` 断掉对应连接（`peer.go:176-177`）。 | 「节点永久失聪」老 bug（readLoop 静默退出、startLoop 等不到信号）已由 H7 修复。LEAVE 同时是主动方停止重试的信号。 |
| **重复 / 并发** | ① 同 id 双连：token 匹配 → 关旧接管；不匹配 → `ID-TAKEN` + 关闭（`signalserver.go:277-291`）。② 重复 announce：服务端 `lastSeen` 幂等覆盖（`signalserver.go:493`）；节点列表客户端 `seen` map 去重（`http_discovery.go:157-163`）。③ 重复拨号：`connectLoop` 的 `connecting` 去重（`peerjs_service.go:404-413,61-62`）；发现回调对已在 `conns` 的对端跳过（330-335）；`discoveryDialAllowed` 拨号预算（345-363）。④ 重复 OFFER：按 `connectionId` 建连，旧连接完整 `Close`（`peer.go:227-240`）。⑤ 并发写：客户端 `writeMu` 串行化（`peer.go:365-368,510-511`）、服务端 `client.sendMu`（`signalserver.go:167,774-779`）；服务端路由出锁后写防持锁卡死（`signalserver.go:327-334`）。 | 信令消息本身是幂等 SDP/ICE 文案，重复转发无害；双连接去重最终由数据面 `dedupConn` 兜底（`conn.go:213-226`，属 06 连接范围）。 |
| **数据缺失或校验失败** | ① WS 缺 id/token/key、key 不符、token 不在白名单 → HTTP 400 拒绝升级（`signalserver.go:249-263`）。② 借 id 响应非 200 / 非法格式 → Dial 失败进退避（`peer.go:566-575`）。③ announce 缺 `peerId` → 400（`signalserver.go:465-467`）；超 8KB（`http.MaxBytesReader`）或集合超 64 → 400（457,469-473）。④ nodes 响应解码失败 → `continue` 跳过该集合（`http_discovery.go:148-153`）；`peerID` 空/超 128 → 丢（155）。⑤ 信令消息 JSON 损坏：客户端 `Unmarshal` 失败 `continue`（`peer.go:472-475`）；服务端 `ReadJSON` 出错即按断开清理（`signalserver.go:306-308`）。⑥ OFFER 缺 sdp/非 data 类型 → 忽略（`peer.go:218-225`）。 | 两端都做了输入防御（M15）：信令 40KB/1MB 读限、发现 8KB/256KB/64KB 限，防被攻破/异常的对端打爆内存。 |
| **鉴权失败** | 信令面：key/token 校验见 §1.1（`signalserver.go:254-263`）；配置了 `-tokens` 白名单但节点 token 不在名单 → 拒升级、节点端 Dial 失败进退避。ID 被占且 token 不匹配 → `ID-TAKEN`（`signalserver.go:279-285`）；客户端收到 `ID-TAKEN/ERROR` 仅记日志、**不自动重连**——同 id 并发是配置错误，重连无解（`peer.go:201-213,208-212`）。发现面**无鉴权**，CORS 全放开（`signalserver.go:62-64,209-224`）。 | token 白名单只约束信令面；发现数据本来就是公开信息。数据面 PSK 门禁在 transport↔transport（连接 06/07），不属本连接。 |
| **半开状态** | 服务端：目标 socket 半开但仍在 `clients` 表 → `route` 的 `dst.send` 失败 → `handleDeadDst` 摘除表项、清理该 id 在 disc/peerLinks/peerStats/peerColls 残留、关连接、广播 `LEAVE`、并向消息发起方补发 `LEAVE` 停止其重试（`signalserver.go:319-328,360-402`——修复旧实现 `_ = dst.send(m)` 静默吞 OFFER/ANSWER 致发起方永久卡握手）。客户端：`Send` 带 15s 写超时（`peer.go:512`），heartbeatLoop 失败即退出等重连（`peer.go:489-493`）。服务端 60s 读超时兜底「断连但没发 FIN」的死连接（`signalserver.go:272-275,312-313`）。发现半开：进程活着但网络断 → 30s 心跳停发 → 90s `heartbeatTTL` 后 sweeper/查询剔除（`signalserver.go:114-160,576`）。 | 半开的两种表现都有独立兜底：信令面走「写失败即摘除广播 LEAVE」路径；发现面走「心跳过期剔除」路径。 |
| **进程重启** | 服务端：**纯内存态、无持久化、无优雅关闭 hook**（`signalserver.go:35-52`；`cmd/peersignal/main.go:49-51` 仅阻塞 Serve）——重启后 clients/queues/disc/peerLinks 全空，恢复全靠客户端行为：节点 `startLoop` 的 Dial 退避重连 + `HTTPDiscovery` 30s 心跳 announce（`http_discovery.go:94-95`）、10s nodes 轮询（86,92-93）自动重新登记。节点端：重启后 Start 重新 Dial + 重建发现组件；节点 id 默认每次启动新生成（`peerjs_service.go:113-117`），跨重启保持身份须显式设 `PEERDRIVE_PEERJS_ID`（`README.md:217`）。节点崩溃下线：服务端 `removeClient`（读错误触发，`signalserver.go:418-447`）广播 LEAVE；若进程被杀无 FIN，则由发现心跳过期剔除。 | 发现链路设计成「有心跳、无优雅下线依赖」——节点不调 `/discover/leave`（全 transport 无调用），B 侧 `HandleLeave` 是保留能力（`signalserver.go:531-559`）。 |

## 4. 相关文档

- 连接文档（同目录）：
  - [07-transport-peerjs.md](07-transport-peerjs.md)：本连接的信令面载体——go-peerjs 库（模块 10）作为客户端与 signalserver 互通（OFFER/ANSWER/CANDIDATE/LEAVE/HEARTBEAT 双向流转、服务端覆盖 src、5s 心跳、H7 done 通道）；`rtcSession` 把 `*peerjs.Connection` 适配为 `Session`（`rtc_session.go:11-18`）。
  - [12-frontend-signalserver.md](12-frontend-signalserver.md)：浏览器面板以同一 PeerJS 协议、同一信令实例连本服务并按 peer id 拨号——本连接 A 侧的 `onIncomingConnection` 正是被动接受方（`peerjs_service.go:471-481`）；面板 `file://` 的 `null` origin 是 CORS 全放开的原因（`signalserver.go:209-218`）。
  - [13-media-node-ech.md](13-media-node-ech.md)：`back/cmd/media-node` 注册到同一信令承载浏览器媒体 DataChannel，是本服务信令通道的另一消费方。
  - [06-service-transport.md](06-service-transport.md)：发现结果互联的装配侧——`service.NodeDirectory` 的 `extraPeers`/`EnsureConnection` 与 `onDiscoveredPeer`/`connectLoop` 共用同一拨号去重（`peerjs_service.go:74-77,510-529,518-529`）。
  - [01-frontend-backend.md](01-frontend-backend.md)：本地 WS 会话（id="local"）与本连接**无关**——本地会话走 router 的 `/ws/peer`，不经 signalserver；信令/发现只服务节点间与面板→节点（模块 11 §6 关联说明）。
  - [09-controller-downloader.md](09-controller-downloader.md)：无直接关系——下载管线不经过信令/发现（模块 09-transport.md §6 有同样结论）。
- 模块文档：`../modules/09-transport.md`（§1 发现链「HTTP 优先 MQTT」、`startLoop`/`connectLoop` 生命周期、`onDiscoveredPeer` 预算；§2-3 内存态与发现负载）；`../modules/11-signalserver.md`（§1 信令/发现主流程、§5 边界与坑 H3/send 失败/token 白名单/janitor）；`../modules/10-peerjs.md`（信令客户端侧协议、`Dial/dialWS/readLoop/heartbeatLoop`、H7 done）；`../modules/01-config.md`（`PEERDRIVE_PEERJS_HOST/PORT/KEY/DISCOVER_URL/RESENCE` 等决定节点默认指向的自托管实例，`config.go:18-22,54-58,210-223`）。