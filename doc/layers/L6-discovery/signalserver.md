# signalserver —— 自托管信令服务器

> 一句话职责：兼容 peerjs-server 协议子集的 PeerJS 信令服务器 + 内置房间发现
> API（`back/internal/signalserver/`，独立二进制 `cmd/peerserver`）——替代公共云
> 信令（0.peerjs.com）与公共 MQTT broker，发现只交换 peerId，不承载业务数据。

- 层归属：AOP ⑥ 发现切面（`doc/LAYERS.md` §1）
- 定位：信令 + 发现一体；服务器天然知道所有在线节点（都连着它做信令），
  房间发现因此变成 HTTP 查询，不再需要 MQTT 广播
- 历史：REFACTOR.md §3.6（自托管信令服务器）、§3.6.1（cloudcone 线上部署）

---

## 职责

1. **PeerJS 协议信令**：节点以 `WS /{path}peerjs?key=&id=&token=` 注册；
   `OFFER/ANSWER/CANDIDATE/LEAVE` 消息按 `dst` 转发；`dst` 不在线时入队
   （带 30s 过期，上线后补发）；`OPEN`/`ID-TAKEN` 控制消息；心跳保活。
2. **ID 分配**：`GET /peerjs/id` 返回随机 id（peerjs API 兼容）。
3. **房间发现**（MQTT 功能并入）：`POST /discover/announce {peerId, collections}`
   登记节点关注集合（30s 心跳刷新），`GET /discover/nodes?coll=` 返回在线节点
   列表（90s 心跳过期剔除）。
4. **资源防护**：离线队列上限、读限制、body 上限、sweeper 清理——防恶意
   客户端把服务器内存/CPU 打爆。

## 运行方式

```bash
# 本地起服（单测/集成测试用 httptest 直挂 handler，不需要它）
cd back && go run ./cmd/peerserver/ -addr :9000 -key peerjs

# 验证信令握手（节点端视角，host/port/key 指向自托管即零改动切换）
# 发现 API 冒烟：
curl -X POST localhost:9000/discover/announce -d '{"peerId":"pd-node-a","collections":["<64hex>"]}'
curl "localhost:9000/discover/nodes?coll=<64hex>"   # → {"nodes":[{peerId,lastSeen}]}
curl localhost:9000/peerjs/id                        # → 随机 id（peerjs API 兼容）
```

部署形态是**独立进程**（systemd），与主服务（cmd/server）互不依赖——信令
服务器崩溃不影响已建立的 WebRTC DataChannel 直连（只影响新连接与发现）。

## 模块清单（每个文件：文件名 + 一句话职责 + 关键导出）

### `signalserver.go` —— 信令 + 发现核心

| 关键导出 | 说明 |
|---|---|
| `Server` 结构体 | `key`（API key）/ `path` / `queueTTL`(30s) / `heartbeatTTL`(90s) / `clients`(id→连接) / `queues`(dst→消息) / `disc`(collection→peerId→lastSeen) |
| `Message` 结构体 | `{type, src, dst, payload}`——与 peerjs 客户端协议一致 |
| `NewServer(key string) *Server` | 创建实例（key 空则默认 "peerjs"） |
| `Server.Start()` | 启动后台 sweeper（30s 周期清理过期队列项与空队列） |
| `HandleID(w, r)` | `GET /{path}{key}/id` → 随机字母数字 id（`randomID`，16 字节） |
| `HandleWS(w, r)` | WS 升级 + 注册 + readLoop；校验 id/token/key，token 不匹配同 ID 拒绝（ID-TAKEN） |
| `HandleAnnounce(w, r)` | `POST /discover/announce`——body 限 1KB、collection 数限 64（M15） |
| `HandleNodes(w, r)` | `GET /discover/nodes?coll=` → `{nodes: [{peerId, lastSeen}]}`，过期剔除 |
| `NodeInfo` 结构体 | `{peerId, lastSeen}`（发现响应条目） |
| `maxQueuedPerDst = 100` | 离线队列每 dst 上限，超限丢最旧（H3 修复） |

内部结构：`client{id, token, conn, sendMu, last}`（sendMu 串行化 gorilla 并发
写）、`queuedMsg{msg, expire}`（入队带过期）、`route`（在线转发/离线入队，
LEAVE/EXPIRE 不入队）、`flushQueue`（上线补发 + 过期清理）、`removeClient`
（LEAVE 广播 + 发现记录清理，**锁外发送**）。

### `main.go`（`cmd/peerserver/`）—— 独立二进制装配

| 关键导出 | 说明 |
|---|---|
| `-addr` / `-key` flag | 监听地址（默认 `:9000`）/ API key（默认 "peerjs"） |
| 路由 | `/peerjs`（HandleWS）、`/peerjs/id`（HandleID）、`/discover/announce`、`/discover/nodes` |

## 关键机制

### 1. 信令消息路由（对齐 peers/peerjs-server）

```
客户端 A ──{type:OFFER, dst:B, payload}──▶ 服务器
                                            ├─ B 在线 → 转发（服务端覆盖 src=A）
                                            └─ B 离线 → 入队（LEAVE/EXPIRE 除外，
                                                30s 过期，上限 100 条）
B 上线 ──OPEN──▶ 服务器 ──flushQueue──▶ B（补发积压 OFFER）
B 断开 ──────────▶ removeClient：对全体在线客户端广播 LEAVE，清发现记录
```

- 服务端**覆盖 `src`**（readLoop 里 `m.Src = cl.id`）——防客户端伪造来源。
- ID 占用保护：同 ID 重连时 token 匹配则**接管**（closeConn 旧连接再挂新），
  不匹配则 `ID-TAKEN`。
- 心跳：客户端每 5s 发 HEARTBEAT；readLoop 每收到消息续 60s 读超时
  （`SetReadDeadline`），断连客户端不再占资源。

### 2. 发现 = 服务器内存表（替代 MQTT 广播）

`disc` 是 `map[collection]map[peerID]lastSeen`。announce 与信令连接解耦
（任意 HTTP 入口都可上报）；`HandleNodes` 以 `heartbeatTTL=90s` 为窗口剔除
过期节点。30s 心跳的节点即使信令 WS 闪断（重连中）也不会从发现列表消失。

### 3. 资源防护（多层）

| 层 | 限制 | 防什么 |
|---|---|---|
| WS 升级 | 必填 id/token/key，key 必须匹配 | 未授权连接 |
| 读限制 | `SetReadLimit(40<<10)` | 恶意超大信令 payload |
| 读超时 | 60s（HEARTBEAT 刷新） | 断连客户端占资源 |
| 离线队列 | 每 dst 100 条（丢最旧）+ 30s 过期 + sweeper 清理 | 永不连线的 dst 使队列无限增长 → OOM（H3） |
| announce body | `MaxBytesReader` 1KB + 64 集合上限（M15） | 无界 decode |
| 写 | `sendMu` + 10s 写超时 | gorilla 并发写 panic / 慢客户端阻塞 |

## 与其它模块的关系

```
cmd/peerserver ──► signalserver（唯一入口，独立二进制部署）
transport/http_discovery.go ──► /discover/announce + /discover/nodes（发现客户端）
transport/peerjs_service.go ──► peerjs 客户端（PEERDRIVE_PEERJS_HOST/PORT/KEY 指向
                                本服务器，协议零改动）
test/integration/selfhosted_test.go ──► 全链路验证（信令+发现+拉文件）
```

- **节点端**：`PEERDRIVE_PEERJS_HOST=peersignal.moonchan.xyz` +
  `PEERDRIVE_PEERJS_KEY=<key>`（信令）+ `PEERDRIVE_DISCOVER_URL=...`（发现，
  优先于 MQTT）——REFACTOR.md §3.6。
- **浏览器端**：host/port/key 配置指向自托管，信令自有、无公共云 MITM 面。
- **发现客户端**详见 `L6-discovery/discovery.md`。
- 本模块**不 import** 任何业务包（只依赖 gorilla/websocket），是⑥切面的
  服务端半区。

## 协议对齐细节（与 peerjs-server 的差异点）

实现对齐 `peers/peerjs-server`（src/services/webSocketServer、messageHandler），
客户端库零改动即可切换。关键行为：

1. **WS URL 格式**：`/{path}peerjs?key=&id=&token=`（`path` 由部署方决定，
   peerserver 挂 `/peerjs`；go-peerjs 客户端在 path 上还有 `/{path}/` 前缀约定，
   服务器用 `strings.HasSuffix` 或明确挂载点兼容）。
2. **消息信封**：`{type, src, dst, payload}`——服务器**重写 src** 后按 dst 转发；
   payload 是 `json.RawMessage` 原样透传（OFFER/ANSWER/CANDIDATE 的 SDP/ICE
   内容服务器不解析）。
3. **离线队列**：`LEAVE`/`EXPIRE` 不入队（它们只在在线时有效）；`dst` 为空的
   消息不入队。
4. **ID-TAKEN**：token 不匹配时回复 `ID-TAKEN` 并立即关闭；匹配时接管旧连接
   （旧连接被 closeConn，其资源随即由 readLoop defer 清理）。
5. **心跳**：客户端 5s HEARTBEAT；服务器 60s 读超时 + 90s 发现心跳窗口——
   前者管信令连接存活，后者管发现列表新鲜度，两者解耦。
6. **与公共云差异**：无速率限制/无 token 白名单（后续可在服务器加 token
   白名单，REFACTOR.md §3.6 预留）；`CheckOrigin` 恒 true（自托管场景由
   部署方负责来源白名单）。

## 线上部署（cloudcone，2026-08-17 现状）

```
peersignal.moonchan.xyz ──CF 橙云 A 记录──▶ 117.55.237.217（cloudcone nginx）
        │ wss://peersignal.moonchan.xyz/peerjs（信令）
        │ https://peersignal.moonchan.xyz/discover/*（发现 API）
   peerserver（systemd，监听 127.0.0.1:9000，key=pd-signal-b9447b406828e500）
```

- nginx 反代 `127.0.0.1:9000`，WS 升级头透传；CF 100s 空闲超时对信令无影响
  （心跳 5s 保活）。
- **DNS 决策**：proxied=true（橙云）已验证可行——CF 按 A 记录 IP 回源到
  cloudcone nginx（自有证书）。踩坑：加 A 记录前 peersignal 橙云路径下实测
  404（nginx/1.18.0，非 cloudcone 的 1.22.1）——当时回源目标不确定，A 记录
  建立后回源即正确。
- **注意**：`cloudcone.moonchan.xyz` 被 livekit 占用（livekit.conf →
  127.0.0.1:7880），不可复用，故用独立子域名。
- **代理注意**：cloudcone 443 **不走宿主机代理**（代理连 cloudcone 超时）——
  curl/测试无代理直连；0.peerjs.com 等公共服务则必须走代理。两者按目标域名
  区分（项目 AGENTS.md「线上部署」节）。
- 部署更新：`GOOS=linux CGO_ENABLED=0 go build -tags nosqlite -o /tmp/peerserver
  ./cmd/peerserver/` → 上传写 `.new` → `systemctl restart peerserver`（避免
  Text file busy）。
- 线上验证：`PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration"
  ./test/integration/ -run TestLive -v`（无代理跑）。

## 坑与设计决策

| 编号 | 坑 | 修复 |
|---|---|---|
| H3 | 离线队列无上限——dst 永不连接时队列无限增长（每个恶意客户端可对任意随机 ID 发 OFFER 打爆内存） | `maxQueuedPerDst=100` 超限丢最旧（信令消息过期即失效，丢旧比丢新合理）；配套 `Start()` 30s sweeper 清理过期项与空队列（原清理只在 flushQueue 触发，dst 永不连接则过期消息堆积） |
| M15 | announce body 无界 decode、collection 数量无上限 | `http.MaxBytesReader` 1KB + `maxCollectionsPerAnnounce=64` |
| 并发写 | gorilla/websocket 不允许并发 WriteJSON——心跳/ICE/多 goroutine 转发会 panic（3 节点互通测试触发） | `client.sendMu` 串行化所有写 |
| 持锁 Close | 锁内调用 `conn.Close()` 与 sendMu 交互可能死锁 | removeClient 锁内只收集 victims，解锁后逐一 send（REFACTOR §5 同类坑） |
| 协议细节 | answerer 新生成 connectionId → ANSWER 路由不到 | 服务端只管按 dst 转发，connectionId 沿用由客户端保证（REFACTOR §5 E2E 坑） |
| 部署 | cloudcone.moonchan.xyz 被 livekit 占用（:7880） | peerserver 走独立域名 peersignal.moonchan.xyz（橙云 A 记录 → cloudcone nginx → 127.0.0.1:9000）；443 直连不走代理（代理连 cloudcone 超时） |

## 测试

### `signalserver_test.go`（单测，全部内存 httptest + gorilla 客户端）

| 测试 | 发现背景 |
|---|---|
| `TestSignal_OpenAndForward` | 功能测试——注册 OPEN + 消息按 dst 转发，**服务端覆盖 src 是协议要求**（防伪造来源） |
| `TestSignal_OfflineQueue` | peerjs-server 行为对齐——OFFER 在目标上线前到达不能丢，上线后补发 |
| `TestSignal_LeaveBroadcast` | 功能测试——LEAVE 广播让对端感知断开（peerjs-server 行为对齐） |
| `TestSignal_IDTaken` | 功能测试——ID 占用保护：token 不匹配拒绝（防劫持他人 ID） |
| `TestSignal_InvalidKey` | 防御性测试——key 校验失败必须拒绝连接 |
| `TestDiscover_AnnounceAndQuery` | 功能测试——自托管后房间发现并入信令服务器（替代 MQTT 广播）；coll-a 只返回 node-1/node-2，coll-b 节点不混入 |

### `selfhosted_test.go`（集成，`go test -tags "nosqlite integration"`）

| 测试 | 发现背景 |
|---|---|
| `TestSelfHostedSignalAndDiscover` | 功能需求——自托管后 PeerJS 信令与房间发现都归自己管：本地起信令服务器，B 仅靠 HTTP 发现互联 A 并拉文件（无任何外部服务） |
| `TestSelfHostedPeerJSSignal` | 功能需求——节点端零改动（仅改 host 配置）切到自托管：直接用 peerjs 客户端模块连自托管完成 WebRTC 数据面互通 |

### `live_test.go`（`PEERDRIVE_LIVE_TEST=1`，无代理直连）

- `TestLiveSignal_DiscoveryAndInterop` / `TestLiveSignal_ProtocolCompat`：
  发现背景=部署验证——自托管服务器（peersignal.moonchan.xyz）上线后，节点
  零改动（仅改 host/key/discover）即完成信令+发现+拉文件全链路与客户端协议兼容。

## 文件清单

| 文件 | 职责 |
|---|---|
| `back/internal/signalserver/signalserver.go` | 信令 + 发现核心（约 344 行） |
| `back/internal/signalserver/signalserver_test.go` | 单测（6 个，含发现背景标注） |
| `back/cmd/peerserver/main.go` | 独立二进制装配（flag + 4 条路由） |
| `back/test/integration/selfhosted_test.go` | 自托管全链路集成测试 |
| `back/test/integration/live_test.go` | 线上部署验证（TestLiveSignal_*） |