# HTTP API 隧道（HTTP API Proxy over P2P）

> 2026-08-16 · 设计文档（尚未实现）。
> 目标:让浏览器/NAT 后节点经既有 PeerJS + WebRTC P2P 连接,访问远端节点的 gin REST API,
> 且**按权限控制**(不可信节点网络,peerId 不可当身份)。

---

## 1. 为什么

peerdrive 的互联层是 PeerJS 信令 + WebRTC DataChannel,节点/浏览器之间目前只交换
文件协议(verb:`req/meta/data/done/err` + 文件索引)。每节点都跑着一个 gin REST API,
但只在本机可访问——NAT 后的远程浏览器/节点完全够不着。

加上 HTTP 隧道后,任意已互联且被授权的对端可以:
- 远程运维 NAT 后节点(`curl http://A:3000/peerjs/r/<BID>/collections`)
- 浏览器经本地节点 A 桥接,读取/管理远端节点 B 的集合、文件、分享、任务
- 未来浏览器可直接 WebRTC 直连 B,同一套 verb,无需中转

## 2. 核心决策

- **verb 化,不搬 wintools 的 webrtc-proxy 二进制**:wintools `cmd/webrtc-proxy` 是独立
  二进制 + 独立 peerjs 实现;peerdrive 已有 `back/peerjs`(go-peerjs)+ `Session` 抽象 +
  reqId 状态机 + `SendFrame` 内置流控。正确做法是复用这些,把 HTTP 隧道做成新 verb。
- **目标固定为本节点 gin**:转发目标恒为 `http://127.0.0.1:<cfg.Port>`,天然无 SSRF。
- **默认拒绝 + token 认证 + 最小权限**:不可信节点网络,peerId 可被抢占冒充,
  授权只认 token,不认 peerId。

## 3. 帧协议(新增 4 个 verb,复用 Session/reqId/SendFrame)

```
auth        (text)  {token}                    连接建立后先认证(DTLS 加密通道内)
                   → auth-ok {perm} | auth-deny {msg}
httpreq     (text)  {reqId, method, url, headers, body:bool}   url 必须以 / 开头
httpbody    (text)  {reqId, size} + 紧随的二进制块             请求/响应体块
httpend     (text)  {reqId, error?}                           请求体结束 / 响应完成
```

- `httpbody` 头+体用 `Session.SendFrame` 原子发送(`back/peerjs/connection.go` sendMu),
  接收端按「连接级 expect」状态机把二进制块挂到紧随其头的 stream 上——与现有下载/上传
  的二进制路由同机制,同一连接并发多个 stream 不冲突。
- `reqId` 为 UUID v4(与现有 `requestFile` 一致),跨连接唯一。
- 请求 body 由 `httpreq{body:true}` 后的多个 `httpbody` 块组成,`httpend` 表示 EOF;
  响应对称(服务端回 `httpresp` + `httpbody` 块 + `httpend`)。

## 4. 权限模型(token → 权限名 → 规则)

### 4.1 连接级认证

DataChannel open 后,对端必须先发 `auth{token}` 文本帧(DTLS 通道内传输,
信令 MITM 拿不到)。服务端校验 token → 把权限名绑定到该连接的 `connState`。
**未认证连接 / token 错误 → 后续 httpreq 一律 403,不触达 gin。**
连接本身可保留(文件拉取照旧不受影响)。

### 4.2 内置角色

| 角色 | 权限 |
|---|---|
| `none`(默认) | 无隧道权限,httpreq → 403 |
| `read` | GET/HEAD + 只读 API 前缀白名单(如 `/collections`、`/peerjs/node`、`/p2p/*`、`/tasks`、`/sha256sum/`) |
| `admin` | 全部 method / path |

### 4.3 可选规则叠加

自定义权限名可在基础角色之外叠加 `method:path-prefix` 规则,
精确到单独端点(如只放行 `post:/collections/fork`)。

### 4.4 配置(env)

```bash
PEERDRIVE_HTTP_PROXY_ENABLE=true
PEERDRIVE_HTTP_PROXY_TOKENS=tokenA:admin,tokenB:read,tokenC:custom   # token:权限名
PEERDRIVE_HTTP_PROXY_RULES=custom:post:/collections/fork,custom:get:/collections/*  # 可选叠加
PEERDRIVE_HTTP_PROXY_CLIENT_TOKEN=<本节点发起隧道时持有的 token>
PEERDRIVE_HTTP_PROXY_RATE=50            # 每连接请求/秒
PEERDRIVE_HTTP_PROXY_MAXSTREAMS=16      # 并发隧道流上限
```

## 5. 权限执行链

```
对端连接 open
  → 对端发 auth{token}(DTLS 通道内)
  → 目标节点校验 token → connState 绑定 perm
  → 每个 httpreq:method + path 校验 perm + rules
  → 通过才转发到 http://127.0.0.1:<cfg.Port> + url
  → 未认证 / 越权:httpresp 403,不触达 gin
```

- **与 REST 鉴权叠加,不替代**:隧道层决定「能不能用隧道、能碰哪些 API 组」;
  gin 原有 Bearer 用户鉴权(`AuthOptional`/`AuthRequired`)照常生效,隧道头透传。
- **限流 + 审计**:每连接速率/并发流上限,`serveHTTPProxy` 记审计日志
  (远端 peerId + method + path + 结果),「不负责任」节点可追踪、超限即熔断。

## 6. 客户端入口

### 6.1 节点桥接(第一阶段,前端零改动)

```
ANY /peerjs/r/:peer/*path → 取 conns[peer] → 连接级 auth(用 CLIENT_TOKEN)→ httpreq 隧道
```

浏览器/curl:`curl http://本机:3000/peerjs/r/<BID>/collections`。

### 6.2 浏览器直连(后续扩展)

浏览器在其既有 Session(WS 本地 `/ws/peer` 或 WebRTC 远端)上直接发
`auth` + `httpreq` 帧,JS helper 封装。前端零后端改动即可用。

## 7. 威胁模型(写清楚,防误用)

- **peerId 可被抢占冒充**(PeerJS ID 先到先得)→ **授权只认 token,不认 peerId**。
- token 只在 DTLS 加密的 DataChannel 内传输,信令 MITM 拿不到。
- 对等节点能做的上限 = 它持有的 token 对应权限;默认就是「不能碰你的 REST API」。
- 目标恒为本节点 gin → 无 SSRF;url 校验必须以 `/` 开头、无 scheme/host。

## 8. 实现顺序

1. 配置项(config.go):上面 6 个 `PEERDRIVE_HTTP_PROXY_*`
2. 帧协议 + `auth` verb 处理(`bindConn` switch 加 case)+ `connState` 绑定 perm
3. `serveHTTPProxy`(转发到本节点 gin,带 reqBodyQueue 背压 + 超时 + 审计)
4. 桥接路由 `ANY /peerjs/r/:peer/*path`(client 侧流状态机,满时阻塞不丢块)
5. 限流 + 并发上限 + 审计日志
6. 单元测试(WSSession 互发 auth/httpreq 帧)+ 集成测试(双节点互联 curl)

## 9. 相关文件

- 帧协议/reqId 路由/`bindConn`:`back/internal/service/peerjs_service.go`
- `Session` 抽象:`back/internal/service/ws_session.go`、`rtc_session.go`
- 原子帧 + 流控:`back/peerjs/connection.go`(`SendFrame`)
- 路由挂载:`back/internal/router/peerjs_routes.go`
- 参考实现(wintools):`cmd/webrtc-proxy/main.go`(reqBodyQueue / stream / forward 超时)
- 参考安全配对:`~/p2ptun`(OFFER metadata secret 配对)
