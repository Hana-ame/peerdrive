# Peerdrive 节点开发接口参考

> 面向开发人员：节点（Go）对外暴露的三层接口——**HTTP 管理面 / 帧协议（DataChannel/WS 共用）/ Go API**。
> 与 `doc/api-reference.md`（面向前端的 HTTP API 参考）互补；协议细节见 `doc/REFACTOR.md` §4。

## 1. HTTP 管理面（`back/internal/router/`）

### 1.1 节点发现与拉取（`peerjs_routes.go`）

| 端点 | 方法 | 认证 | 用法 |
|---|---|---|---|
| `/peerjs/node` | GET | 无 | 本节点状态：`{"id","online":true,"peers":[]}`（peers = 当前互联的远端节点 id） |
| `/peerjs/fetch` | POST | 需 | 从对端拉文件：`{peer, hash, offset?, size?}` → 文件流（≤64MB，H4；超大走 `/ws/peer` 分片） |
| `/ws/peer` | GET(WS) | Origin 白名单 | 本地直连会话，帧协议与 DataChannel 完全一致 |

`/peerjs/fetch` 示例：

```bash
curl -X POST http://localhost:3000/peerjs/fetch \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"peer":"peerdrive-abc123","hash":"<64hex>","offset":0,"size":-1}' -o file.bin
```

### 1.2 端口转发（`controller/p2p.go:711`）

| 端点 | 方法 | 认证 | 用法 |
|---|---|---|---|
| `/p2p/forward/create` | POST | 需 | 登记授权：`{key, port}`（key 即凭证，运行时追加不持久化） |
| `/p2p/forward/connect` | POST | 需 | 本端 loopback 起监听转发：`{key, target_peer, local_port, port?}`（`local_port` 本端监听必填；`port` 目标端口可选，0/缺省 = 由目标节点按规则唯一端口决定） |
| `/p2p/forward/list` | GET | 无 | 活跃监听与隧道：`{"listeners":[{id,target_peer,local_port}], "tunnels":[{peer_id,port,key_id}]}` |
| `/p2p/forward/close` | POST | 需 | 关闭转发：`{key?}` 关闭匹配 key 的监听，`{peer_id?}` 断开指定 peer 的全部隧道 |

### 1.3 统一 source 管理（`source_routes.go`）

| 端点 | 方法 | 认证 | 用法 |
|---|---|---|---|
| `/sources` | GET | 无 | 各 source 快照：优先级/能力/在线/命中统计 |
| `/sources/:name/priority` | POST | 无 | 运行时调整优先级：`{"priority": n}` |

### 1.4 通用（`router.go`）

| 端点 | 方法 | 说明 |
|---|---|---|
| `/ping` | GET | 健康检查 |
| `/download/:hash` | GET | 统一多协议下载（source 链） |
| `/download/:hash/sources` | GET | 下载源列表 |
| `/download/:hash/refresh` | POST | 刷新源（需认证） |
| `/swagger/*any` | GET | Swagger UI |

## 2. 帧协议（DataChannel / WS 共用，`conn.go`）

### 2.1 文件拉取

```jsonc
// 请求（任意端）；reqId 为指令 UUID v4（Go 端始终携带）
{"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<uuid-v4>"}
// 响应（回显 reqId）
{"type":"meta","hash","total","reqId"}
{"type":"data","hash","offset","size","reqId"}   // 后随 size 字节二进制
{"type":"done","hash","offset","size","reqId"}
{"type":"err","msg","reqId"}
```

### 2.2 文件索引 verb（`file_index.go`）

```jsonc
create   {type:"create", path}              → created {hash,size,name,path,seq}
upload   {type:"upload", name, size, offset?, reqId}   // 分片上传（offset 缺省 0）
         → meta {total, offset} → data×1 → uploaded {hash,path}（完成）| ack {offset}（续传）
list     {type:"list", offset?, size?}      → list-resp {files,total}
info     {type:"info", hash}                → info-resp {hash,size,name,path,seq}
delete   {type:"delete", hash}              → deleted {hash,seq}
sync     {type:"sync", seq}                 → sync-resp {files,lastSeq}
```

- 分片上传：offset 按 64KB chunk 对齐，一次 upload = 一个分片（data 块 ≤64KB）；
  多 source = 多连接并发传不同分片，位图全满自动 uploaded
- 断点续传：同 name 重开会话幂等复用；会话 10 分钟无活动清理

### 2.3 端口转发 v2（`forward.go`）

```jsonc
client ──fwd-open {port, reqId}──────────▶ server
client ◀──fwd-challenge {nonce, reqId}──── server   一次性随机数
client ──fwd-auth {hmac, reqId}──────────▶ server   HMAC-SHA256(key, nonce)
client ◀──fwd-ok / fwd-err──────────────── server
之后: fwd-data 头 + 二进制块双向透传（头文本、块二进制、原子连续）
      fwd-close 收尾（任一侧 EOF/主动关闭）
```

### 2.4 admin 管理面 verb（`admin.go`，仅本地 WS 会话）

浏览器经 `/ws/peer` 本地会话发 admin 帧管理本节点（内部转发 gin engine，复用全部
HTTP controller 逻辑，零重复实现）。**仅 `ID()=="local"` 会话接受**——WebRTC 连接
来自公共信令任意节点，不开放管理面（防权限面暴露）。

```jsonc
// 普通请求 → admin-resp
{"type":"admin","method":"GET|POST|DELETE","path":"/files?x=1","body":<JSON>,"token":"<可选>","reqId"}
{"type":"admin-resp","status":200,"body":<原始 JSON>,"reqId"}   // 4xx/5xx 也走 admin-resp（body 为结构化错误体，409 含 conflicts 清单）

// 二进制上传（声明帧 + 后续二进制帧；收齐后 multipart 重包转发）
{"type":"admin","method":"POST","path":"/files/upload","binary":true,"filename":"a.bin","field":"file","size":N,"reqId"} + N 字节二进制帧
   // field 默认 "file"；BT torrent 上传 field="torrent" + path="/bt/torrent"
   // reqPath 由声明帧 path 决定（旧版硬编码 /files/upload 的 bug 已修复，见 admin_test.go TestAdminBinaryUploadReqPath）

// 二进制响应（文件流，如集合文件）→ admin-bin 头 + 紧随一个二进制帧（≤64MB adminBinMax；大文件走 2.1 req verb 分片）
{"type":"admin-bin","status":200,"size":N,"reqId"} + 二进制帧
```

- 上传收集：连接级单槽（`st.adminUp`），声明帧在消息泵内**同步**占槽（异步会丢后续
  二进制块）；30s 无数据自动中止清理临时文件
- 认证：admin 帧 `token` 字段 → 转发时注入 `Authorization: Bearer` → 与 HTTP 行为一致
- 前端使用：`front/src/ws.js`（`admin`/`upload`）+ `front/src/api.js`（全部 request 走此通道）

### 2.5 三条协议硬约束（勿破坏）

1. JSON 控制头必须是**文本帧**（`SendText`），数据块是**二进制帧**（`Send`）——发反了对端把控制头当数据块吞掉
2. data 头与数据块必须**原子连续**（`SendFrame` 的 sendMu）；接收端按连接级 expect 状态机路由
3. 浏览器端可不传 reqId（向后兼容），Go 端始终携带（UUID v4）

## 3. Go API（`transport.PeerJSService`，`peerjs_service.go`）

### 3.1 连接管理

| 方法 | 说明 |
|---|---|
| `NewPeerJSService(cfg, storageDir) *PeerJSService` | 构造（节点 id 默认 `peerdrive-<随机hex>`） |
| `Start()` | 注册信令 + 断线自动重连（异步） |
| `Close()` | 关闭信令 + 全部 WebRTC 连接 |
| `ID() string` | 本节点信令 id |
| `Connections() map[string]Session` | 当前活跃连接（key = 远端 peer id，含 `"local"`） |
| `BindLocal(sess Session)` | 注册本地 WS 会话 |
| `FileIndex() *FileIndexService` | 共享本地文件索引（LocalSource 装配用） |

### 3.2 文件拉取（出站角色）

| 方法 | 说明 |
|---|---|
| `OpenStream(peerID, hash, offset, size) (io.ReadCloser, error)` | **流式**拉取；全量读完自动 sha256 校验；Close 提前取消（不断连） |
| `FetchFromPeer(peerID, hash, offset, size) ([]byte, error)` | 整体拉取（OpenStream + ReadAll 封装） |

```go
// 示例：从对端节点流式拉取文件
r, err := svc.OpenStream("peerdrive-abc123", hash, 0, -1)
if err != nil { /* 无连接 / 拉取失败 */ }
defer r.Close()
written, err := io.Copy(dstFile, r) // EOF 时自动校验 sha256
```

### 3.3 端口转发（客户端侧）

| 方法 | 说明 |
|---|---|
| `AddForwardRule(key string, port int) error` | 运行时追加授权（不持久化） |
| `SetForwardRules(rules map[string][]int)` | 全量设置规则表 |
| `OpenForward(ctx, peerID, key string, port int) (net.Conn, error)` | 建立转发隧道（port=0 由服务端按规则唯一端口决定） |
| `ListForwardStreams() []ForwardStreamInfo` | 活跃隧道快照 |
| `CloseForwardStream(peerID string)` | 主动断开隧道 |

```go
// 示例：经对端节点访问其 127.0.0.1:8080
conn, err := svc.OpenForward(ctx, "peerdrive-abc123", "my-key", 8080)
if err != nil { /* 认证失败 / 端口未授权 */ }
defer conn.Close()
// conn 当普通 TCP 连接使用，双向透传
```

## 4. peerjs 模块扩展点（`back/peerjs/`，独立 go.mod）

| 扩展点 | 接口 | 说明 |
|---|---|---|
| 换信令 | `Signaller` 接口（`signaller.go`） | `NewPeerWithSignaller()` 注入；实现只需收到消息后调注入的 route 回调 |
| 换传输 | `DataChannel` 接口（`transport.go`） | `Connection` 只依赖此接口 |
| 加 verb | `MessageType` / `Frame` | 开放 string 类型，自定义消息直接发送 |

## 5. 配置速查（节点相关）

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `PEERDRIVE_PEERJS_ENABLE` | true | 启用节点互联 |
| `PEERDRIVE_PEERJS_ID` | 随机 | 节点 id |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY/SECURE` | 0.peerjs.com/443/peerjs/true | 信令服务器 |
| `PEERDRIVE_PEERJS_PEERS` | - | 逗号分隔对端 id，启动自动互联 |
| `PEERDRIVE_DISCOVER_URL` | - | 自托管发现 API（优先于 MQTT） |
| `PEERDRIVE_MQTT_ENABLE` | false | MQTT 发现开关 |
| `PEERDRIVE_MQTT_BROKER` | tcp://broker.emqx.io:1883 | MQTT broker |
| `PEERDRIVE_MQTT_TOPIC_PREFIX` | peerdrive/v1 | 发现 topic 前缀 |
| `PEERDRIVE_MQTT_COLLECTIONS` | - | 逗号分隔关注的 collection hash 分片 |
| `PEERDRIVE_FORWARD_RULES` | - | 转发白名单 `key:port,key2:port2`（key 即凭证，建议 chmod 600） |
| `PEERDRIVE_WEBRTC_STUN` | stun:stun.l.google.com:19302 | STUN 服务器 |
| `PEERDRIVE_WEBRTC_TURN` | - | TURN 服务器（逗号分隔多 URL） |
