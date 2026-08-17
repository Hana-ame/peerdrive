# discovery —— 发现客户端（HTTP / MQTT）

> 一句话职责：节点「如何找到彼此」的客户端半区——`transport/http_discovery.go`
> （自托管信令服务器的房间发现 API）与 `transport/mqtt_discovery.go`（公共
> broker 的分片房间发现），只交换 peerId，实际传输仍走 PeerJS 云信令 +
> WebRTC 直连。

- 层归属：AOP ⑥ 发现切面（`doc/LAYERS.md` §1）——⑥ 的禁止项「传输业务数据
  （只交换 peerId/连接信息）」在本模块严格执行
- 位置：两个文件在 `internal/transport/` 包内（REFACTOR.md §3.6 迁入），被
  `peerjs_service.go` 装配消费
- 服务端半区（`signalserver`）见 `L6-discovery/signalserver.md`

---

## 职责

1. **HTTPDiscovery**：替代 MQTT 的优先发现方式——announce 本节点关注的集合
   （`POST /discover/announce`，30s 心跳）+ 轮询查询在线节点
   （`GET /discover/nodes?coll=`，10s 周期）→ `onPeer` 回调互联。
2. **MQTTDiscovery**：公共 broker 分片房间发现——topic 按 collection hash
   分片（`peerdrive/v1/{hash}/nodes`），节点只订阅自己关注的分片；announce
   幂等去重 + 60s 心跳；paho 断线自动重连 + 重订阅。
3. **装配决策**：`PEERDRIVE_DISCOVER_URL` 设置时优先 HTTP 发现，否则
   `PEERDRIVE_MQTT_ENABLE` 时启用 MQTT（peerjs_service.go:197-210）。
4. **去重与防御**：两种发现都对已上报 peer 去重（避免重复 onPeer）；对
   payload 大小、peerId 长度、集合 hash 合法性做校验（M15）。

## 模块清单（每个文件：文件名 + 一句话职责 + 关键导出）

### `http_discovery.go` —— 自托管信令服务器的 HTTP 发现客户端

| 关键导出 | 说明 |
|---|---|
| `HTTPDiscovery` 结构体 | `baseURL` / `peerID` / `collections` / `onPeer` 回调 / `client`(10s 超时) / `seen`(去重表) / ctx |
| `NewHTTPDiscovery(baseURL, peerID, collections, onPeer)` | 创建（自带 `context.WithCancel`） |
| `Start()` / `Stop()` | 异步循环启停（cancel 退出） |
| `loop()` | 先 announce 一次 → 10s poll（discover）+ 30s hb（announce）双 ticker |
| `announce()` | `POST {baseURL}/discover/announce {peerId, collections}`；失败仅 debug |
| `discover()` | 逐集合 `GET /discover/nodes?coll=` → 解码（**LimitReader 256KB**，M15）→ 过滤空/自身/超长 id → `seen` 去重 → `onPeer` |

### `mqtt_discovery.go` —— 公共 broker 分片房间发现客户端

| 关键导出 | 说明 |
|---|---|
| `MQTTDiscovery` 结构体 | `broker` / `topicPrefix` / `clientID` / `onPeer` / `announceTick`(60s) / `announce`(幂等表) / ctx |
| `NewMQTTDiscovery(broker, topicPrefix, clientID, onPeer)` | 创建；默认 broker `tcp://broker.emqx.io:1883`、prefix `peerdrive/v1`、clientID 时间戳后缀 |
| `nodeTopic(hash)` | 分片 topic：`{prefix}/{hash}/nodes` |
| `Start(collections)` | 连 broker（paho：CleanSession + AutoReconnect + ConnectRetry 5s）；`SetOnConnectHandler` 里**重订阅全部集合分片**（paho 不保留旧订阅） |
| `onMessage` | 解析 `{peerId, ts}` → onPeer；**payload ≤64KB、peerId ≤128**（M15） |
| `Announce(peerID, collections)` | 幂等 announce（同 peer+集合只发一次）+ 启动心跳循环 |
| `loop()` | 60s ticker 重发全部已 announce 的 peer+集合（防 broker 清理 + 通知迟到节点） |
| `Stop()` | cancel → 等 `done` → Disconnect |

### 装配点：`peerjs_service.go`

| 位置 | 行为 |
|---|---|
| peerjs_service.go:198-202 | `DiscoverURL != ""` → `NewHTTPDiscovery(...)` + `Start()` |
| peerjs_service.go:203-209 | 否则 `MQTTEnable` → `NewMQTTDiscovery(...)` + `Start(cols)` + `Announce(id, cols)` |
| `onDiscoveredPeer` | 发现回调 → 已连接则跳过，否则 `connectLoop` 自动互联（去重由调用方保证） |

## 关键机制

### 1. 发现协议（HTTP）

```
节点上线 ──POST /discover/announce {peerId, collections}──▶ 服务器
   │  30s 心跳续期（服务器端 90s 过期窗口）
   ▼  10s 轮询
GET /discover/nodes?coll={hash} ◀── {nodes:[{peerId,lastSeen}]}
   │  过滤：空 id / 自己 / >128 字符；seen 去重
   ▼
onPeer(peerID) → PeerJSService.connectLoop（WebRTC 直连）
```

announce 与信令连接解耦——即使 WS 信令闪断，HTTP 发现仍能维持在线状态。
轮询/心跳各自独立 ticker，服务器端 TTL 比心跳间隔宽裕（90s vs 30s）容忍
丢包。

### 2. 发现协议（MQTT）

```
broker topic: peerdrive/v1/{collectionHash}/nodes
节点 A ──Publish {peerId, ts}（announce，幂等）──▶ broker
节点 B ◀─Subscribe（同分片）── onMessage → onPeer(A)
60s 心跳：loop() 重发全部已 announce 键（幂等表只记键，重发无副作用）
断线：paho SetAutoReconnect + SetOnConnectHandler 重订阅（paho 不保留旧订阅）
```

分片设计动机：公共 broker 无规模上限的前提是**订阅数与消息量随集合摊开**——
全局单 topic 的 fan-out 是瓶颈，按集合 hash 分片后每片只服务关注该集合的
节点（mqtt_discovery.go:17-23 注释）。

### 3. 优先级与互斥

`DiscoverURL` 优先于 `MQTTEnable`（if/else if 结构），且各自只装配一次
（`s.httpDisc == nil` / `s.discovery == nil` 守卫，防止信令重连循环重复起
发现组件）。

### 4. HTTP 发现 vs MQTT 发现（选型对比）

| 维度 | HTTPDiscovery | MQTTDiscovery |
|---|---|---|
| 服务端 | 自托管 signalserver（自己管，无第三方依赖） | 公共 broker（broker.emqx.io，免费但有外部依赖） |
| 发现方式 | 10s 轮询查询（pull） | 订阅分片 topic 实时推送（push，迟到的节点靠心跳重发兜底） |
| 时序窗口 | 服务器 90s 心跳过期 vs 客户端 30s 心跳 | 60s 心跳；无过期窗口（消息即时生效） |
| 隐私面 | 集合 hash 只发给自己的服务器 | 集合 hash 上公共 broker，任何人可订阅观察 |
| 配置 | `PEERDRIVE_DISCOVER_URL` | `PEERDRIVE_MQTT_ENABLE/BROKER/COLLECTIONS` |

历史：MQTT 是首批发现实现（REFACTOR.md §3.4，2026-08-13，分片模式设计）；
自托管信令落地后（§3.6）发现并入服务器（服务器天然知道所有在线节点），
HTTP 方式成为优先选择——**公共云（0.peerjs.com + broker.emqx.io）零信任场景
用 MQTT，自有服务器场景用 HTTP**。

### 5. 集合来源：collectionHashes()

两种发现都以 `s.collectionHashes()`（peerjs_service.go）为集合列表输入——
节点配置 `PEERDRIVE_MQTT_COLLECTIONS`（逗号分隔 64hex）。hash 合法性过滤
（`IsStrictSHA256`）发生在 subscribe/announce 之前：非法值不订阅、不发布，
防 topic 注入（如 `../` 或超长字符串污染分片命名空间）。

## 与其它模块的关系

```
transport/peerjs_service.go（装配/消费）
    ├─► HTTPDiscovery ──► signalserver（/discover/*，服务端半区）
    └─► MQTTDiscovery ──► 公共 broker（broker.emqx.io）
test/integration/mqtt_test.go（公共 broker 集成，需外网+代理）
test/integration/selfhosted_test.go（自托管 HTTP 发现全链路）
internal/config/config.go（PEERDRIVE_DISCOVER_URL / PEERDRIVE_MQTT_* 配置）
```

- 两种发现产出同一个东西：`peerID` 字符串回调。上层（PeerJSService）不感知
  发现方式，符合⑥「发现与传输解耦」的设计——换发现方式不动互联层。
- 配置项（config.go:44-48,126-130）：`PEERDRIVE_MQTT_ENABLE`（默认 false）、
  `PEERDRIVE_MQTT_BROKER`（默认 tcp://broker.emqx.io:1883）、
  `PEERDRIVE_MQTT_TOPIC_PREFIX`（默认 peerdrive/v1）、
  `PEERDRIVE_MQTT_COLLECTIONS`（逗号分隔）、`PEERDRIVE_DISCOVER_URL`（设置后
  优先于 MQTT）。
- 集合 hash 合法性过滤用 `hashutil.IsStrictSHA256`（严格小写 64 hex，M3 收层
  时从 service 迁入 pkg 的传输层工具）——非法值不订阅/不 announce。

## 坑与设计决策

| 编号 | 坑 | 修复 |
|---|---|---|
| M15 | 公共 broker / 被攻破的发现服务器可回任意 payload——异常大 JSON / 超长 id 打爆内存或污染互联状态 | HTTP：解码 `LimitReader(256KB)`；MQTT：payload ≤64KB、peerID ≤128；均过滤空 id/自身 id |
| 幂等 | 重复 announce 会刷屏公共 broker | `announce map[string]bool` 幂等表：同 peer+集合只发一次上线 announce，心跳循环重发 |
| 重订阅 | paho 断线重连不保留旧订阅 | `SetOnConnectHandler` 里重新 Subscribe 全部集合分片 |
| 集成并行 | 公共信令/broker 上多组测试并行互相干扰（发现：默认并行时 ThreeNodes/MQTT 偶发失败） | 集成测试必须 `-p 1` 串行（REFACTOR.md §5.1） |
| 优先级 | 两种发现同时开会双份 onPeer | `DiscoverURL` 优先（if/else if），且 `== nil` 守卫防重连循环重复装配 |
| 无 announce 边界 | MQTT `Announce` 与 `Start` 分离——调用方需先 Start 后 Announce | 见装配点（Start 后立即 Announce） |

## 测试

### 单测

本层两个客户端文件**无独立单测**（依赖真实 broker/服务器），单元级覆盖经
`peerjs_service_test.go` 的间接路径与 signalserver 侧测试（
`TestDiscover_AnnounceAndQuery`）；发现客户端的完整行为由集成测试兜底。

### 集成测试（`test/integration/`，`go test -tags "nosqlite integration" -p 1`）

| 测试 | 发现背景 |
|---|---|
| `TestMQTTDiscovery`（mqtt_test.go:16） | 功能测试——MQTT 分片房间互相发现（announce/订阅/onPeer 回调/心跳幂等），A 收到 B、B 收到 A，60s 心跳兜底时序 |
| `TestMQTTDiscoverThenPeerJSInterop`（mqtt_test.go:59） | 功能测试——MQTT 发现 → PeerJS 互联 → 拉文件全链路：B 完全不知道 A 的 peer id，仅靠分片发现后经公共云信令直连拉取 |
| `TestSelfHostedSignalAndDiscover`（selfhosted_test.go:37） | 功能需求——自托管后 PeerJS 信令与房间发现都归自己管：本地起信号服务器，B 仅靠 HTTP 发现互联 A 并拉文件（无任何外部服务） |

> 两个 MQTT 测试都需外网 + 代理（broker.emqx.io），自托管测试无外部依赖。

### 运行

```bash
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1 -v
# ⚠️ 必须 -p 1 串行（公共 broker 上并行互相干扰）
```

## 发现链路全貌（一次「节点互相找到」的完整旅程）

以自托管（HTTP 发现）为例：

```
[节点 A]                              [signalserver]                    [节点 B]
   │ 1. 启动：PEERJS_HOST 指向自托管         │                              │
   │ 2. WS 注册 {key,id,token} ───────────▶│ 回 OPEN，入 clients             │
   │ 3. HTTPDiscovery.Start()               │                              │
   │ 4. POST /discover/announce {peerId:A,  │ disc[coll][A]=now             │
   │    collections:[...]} ───────────────▶│                              │
   │ 5. 每 30s 心跳续期 ──────────────────▶│                              │
   │                                       │ 6. B 同样 announce（同集合）    │
   │ 7. 每 10s GET /discover/nodes?coll= ◀─│ {nodes:[{peerId:B,...}]}       │
   │ 8. onPeer(B) → connectLoop(B)         │                              │
   │ 9. PeerJS OFFER ──────────────────────▶│ ──转发──▶ B                  │
   │ 10. WebRTC DataChannel 直连建立        │                              │
   │ 11. 帧协议（req/meta/data/done）拉文件  │                              │
```

关键点：第 3-8 步只交换 peerId（⑥的禁区内不传业务数据）；第 9 步起的信令
转发与第 11 步的数据面都不再依赖发现组件——**发现只负责「初见」，互联与
传输由 peerjs 层全权接管**。

## 文件清单

| 文件 | 职责 |
|---|---|
| `back/internal/transport/http_discovery.go` | HTTP 发现客户端（announce + 10s 轮询 + 30s 心跳） |
| `back/internal/transport/mqtt_discovery.go` | MQTT 分片发现客户端（幂等 announce + 60s 心跳 + 重订阅） |
| `back/internal/transport/peerjs_service.go` | 装配点（DiscoverURL 优先，onDiscoveredPeer → connectLoop） |
| `back/internal/config/config.go` | 发现配置（PEERDRIVE_DISCOVER_URL / PEERDRIVE_MQTT_*） |
| `back/test/integration/mqtt_test.go` | MQTT 发现集成测试（2 个） |
| `back/test/integration/selfhosted_test.go` | 自托管 HTTP 发现集成测试 |