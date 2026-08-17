# 端口转发 v2（forward.go）

> 一句话职责：在 PeerJS DataChannel 上承载 TCP 隧道——把被 NAT 挡住的节点本地
> 端口暴露给持 key 的远端（fwd-open/challenge/auth/ok/err/data/close 七 verb，
> HMAC 质询认证 + 端口白名单 + SSRF 防护）。

## 职责

`forward.go` 实现端口转发 v2（inbound 一环，独立文件承载）：legacy 的 libp2p
流转发（`/peerdrive/forward/1.0.0`，明文 `KEY xxx` 单行认证、无密钥交换、无
白名单）随 libp2p 栈淘汰后，在新 transport 角色体系里以协议级 verb 重建。
转发需求本身保留：把被 NAT 挡住的节点本地端口暴露给持 key 的远端（典型场景：
VPS 常驻节点的 127.0.0.1 服务）。

角色分工（与 inbound/outbound 同构，全双工对称）：

- **服务端**（被转发方）：`serveForwardOpen` → 发一次性质询；
  `serveForwardAuth` → 验证 HMAC + 端口白名单 → dial 127.0.0.1:port → 建隧道；
  `serveForwardClose` → 收尾清槽。数据方向：`forwardPump` 读 TCP → SendFrame。
- **客户端**（发起方）：`OpenForward(ctx, peerID, key, port)` → 返回 net.Conn
  （调用方当 TCP 连接用）；`routeForwardResponse` 收集握手响应。

管理面：`SetForwardRules`（全量装载）/ `AddForwardRule`（运行时追加）/
`ListForwardStreams`（隧道快照）/ `CloseForwardStream`（主动断开），对应
`POST /p2p/forward/create|connect|list|close` 端点。

## 关键机制

### 握手协议（7 步帧序）

```
client ──fwd-open {port, reqId}──────────────▶ server   申请转发目标端口
client ◀──fwd-challenge {nonce, reqId}────────  server   一次性随机数（16 字节）
client ──fwd-auth {hmac, reqId}──────────────▶ server   HMAC-SHA256(key, nonce)
client ◀──fwd-ok / fwd-err──────────────────── server   终局
之后: fwd-data 头 + 二进制块双向透传（复用 SendFrame 原子头-块约束）
      fwd-close 收尾（任一侧 EOF/主动关闭）
```

帧载体是 `dcResp` 通用结构（conn.go:52）——Nonce/Hmac/Port 字段被 forward
独占，其余字段与文件/索引 verb 共用同一 JSON 结构。文本帧与二进制块的
「头-块原子连续」约束与文件传输完全一致（REFACTOR §4 约束 2）：fwd-data
头声明归属 → 下一二进制帧归转发隧道。

### 一次完整握手的真实帧交换（对应 TestOpenForward_ClientSide）

```
客户端                         服务端（fakeSession 扮演）
  │ {type:"fwd-open",port:8080,reqId:"ab12cd34"} →│
  │← {type:"fwd-challenge",nonce:"aabbccdd...",reqId:"ab12cd34"}
  │ {type:"fwd-auth",hmac:hex(HMAC-SHA256(key,nonce)),reqId:"ab12cd34"} →│
  │← {type:"fwd-ok",reqId:"ab12cd34"}
  │ {type:"fwd-data",reqId:"ab12cd34"} + 二进制块 ── 双向 ──▶│
  │ {type:"fwd-close",reqId:"ab12cd34"} ── 任一侧收尾 ──▶│
```

reqId 由客户端生成（`randHex8`，握手槽路由键）；服务端全程回显。HMAC 计算：
`hex(hmac.New(sha256.New, key).Write(nonce))`——客户端与 `serveForwardAuth`
的验证逻辑对称（测试里的 `fwdHMAC` 辅助与实现一一对应）。

### 服务端握手（serveForwardOpen / serveForwardAuth，forward.go:192,234）

```
fwd-open:
  1. port ∉ [1, 65535] → fwd-err "invalid port"
  2. 规则表空 = 本节点未开放任何转发（等价旧版 ForwardEnable=false）→ fwd-err
  3. 防洪水：未消费质询 ≥ fwdNonceMax(64) → 先清过期，仍满 → fwd-err
     "too many challenges"
  4. rand 16 字节 nonce → fwNonces[reqId] = {nonce, port, expire: now+5min}
  5. 回 fwd-challenge{nonce}
     ——不建隧道、不验证 key（验证在 fwd-auth 拿着 nonce 才算）

fwd-auth:
  1. 取出即标 used + delete（防重放：同一 nonce 二次提交必失败）
  2. 无 nonce → "no challenge"；过期 → "challenge expired"
  3. 遍历规则表：HMAC-SHA256(key, nonce) == r.Hmac 命中 → matchedKey
     （key 明文只在服务端内存；hmac.Equal 常量时间比较）
  4. 无匹配 → "unauthorized"（不泄露规则）
  5. 端口 ∉ key 授权列表 → "port not authorized"（key 合法但越权，不泄露规则细节）
  6. 连接级单槽占用检查：st.fwd 活跃 → "tunnel already active"
  7. net.Dial("tcp", "127.0.0.1:port")——SSRF 防护：只 dial 本机 loopback
  8. 建 fwdStream 占单槽 → 回 fwd-ok → go forwardPump
```

### 数据面（forwardPump + 泵内路由，forward.go:325）

- **发送方向**（服务端→客户端 / 客户端→服务端）：`forwardPump` 循环
  `out.Read(buf)`（fwdChunkSize=32KB，转发是流，块小延迟低）→
  `SendFrame(fwd-data 头, 块)`；EOF/错误 → `out.Close()` + 回 `fwd-close` +
  清本端槽。
- **接收方向**：conn.go 泵内——`fwd-data` 头把 `fw.pending` 置 true，下一
  二进制帧投递到 `st.fwdCh`（有界 16，背压不卡泵）→ uploadWorker 写隧道
  （inbound.go fwdCh 分支，H5 同款：IO 移出消息泵，对端 TCP 背压不 head-of-line
  冻结整条连接）。写失败（隧道已关/对端断开）静默丢弃：转发是尽力而为的流。
- **连接关闭**：OnClose 关 `st.fwd.out` → forwardPump 读侧 EOF 退出；调用方
  （OpenForward 返回的 net.Conn）读侧随即 EOF。

### 客户端（OpenForward，forward.go:384）

```
1. conns[peerID] 查连接；连接级双单槽检查（st.fwd 活跃 / st.fwdHs 握手中）
2. fwdHandshake{reqID: randHex8, challenge, done} 占握手槽
3. 发 fwd-open → 阶段1：等 challenge（fwdHandshakeTTL=30s / ctx 超时）
4. 算 HMAC-SHA256(key, nonce) → 发 fwd-auth → 阶段2：等 done（ok/err）
5. 成功：net.Pipe() —— 调用方拿 client 端，pump 读 server 端（数据双向透传）
6. defer 清握手槽（无论成败，防重复握手占单槽）
```

- **坑：challenge/done 两个 channel 分开**（forward.go:76-79）。若共用同一
  channel，阶段1「等 challenge」后无法区分「有 challenge 待继续」与「终局」——
  先 close 的 channel 让第二次 select 立即返回 false 终局。故各自独立 close 一次。
- **port=0**：由服务端按 key 规则唯一端口决定（多端口规则须显式指定，否则
  fwd-err）。

### 规则表与管理面

- `SetForwardRules(map[key][]ports)`：全量装载（配置
  `PEERDRIVE_FORWARD_RULES="key:port,..."`）；`AddForwardRule` 运行时追加
  （`POST /p2p/forward/create`，不持久化）。key 等价于凭证——配置文件名
  chmod 600。
- `ensureForwardMaps`（forward.go:97）：懒初始化规则/质询表——结构体字面量
  构造的实例（测试）不经过 NewPeerJSService，防御 nil map 赋值/读 panic。
- `ListForwardStreams`：遍历 conns → 活跃隧道快照 `{peer_id, port, key_id}`
  （keyID 只留前 16 hex，审计不落全量）。
- `CloseForwardStream`：`fw.closed = true; st.fwd = nil`（主动断开即释放单槽：
  close 端点语义是「立刻可开新隧道」）→ 通知对端 fwd-close（对端
  serveForwardClose 幂等清自己的槽）→ `fw.out.Close()`。

### 连接级单槽（connState.fwd / fwdHs / fwdCh）

| 槽位 | 用途 | 生命周期 |
|---|---|---|
| `fwd` | 活跃转发隧道（`fwdStream{reqID, keyID, port, out, pending, closed}`） | serveForwardAuth 成功 / OpenForward 成功时建立；fwd-close、主动 CloseForwardStream、连接关闭时清空 |
| `fwdHs` | 客户端握手等待状态（challenge/done 双 channel） | OpenForward 占位，defer 必清（无论成败） |
| `fwdCh` | 转发块 → worker 的投递通道（有界 16，同 binCh） | bindConn 创建 |

`fwdStream.pending` 是「fwd-data 头已到、期待下一个二进制块」的泵内声明标记
（conn.go 泵内按帧序处理故无竞态）；非法（无隧道/已关）时静默丢弃并清 pending。
服务端与客户端各持一份 `fwdStream`——服务端 `out` = dial 的 TCP 连接；客户端
`out` = net.Pipe 的 server 端（返回给调用方的是 client 端）。

### 安全设计（对应「权限控制 + 密钥交换」要求）

1. 服务端规则表只认 key 原文白名单（key → 允许端口[]）；key 等价凭证。
2. 质询-响应：nonce 一次性（取出即标 used + 5 分钟过期），HMAC 证明持有 key，
   key 明文永不落线（DataChannel 本身 DTLS 加密，双保险）。
3. 端口越权拒绝：请求端口 ∉ key 授权列表 → fwd-err，不泄露规则细节。
4. SSRF 防护：服务端只允许 dial 127.0.0.1（转发目标是本机端口，不许打任意
   内网 IP）。
5. 握手不占隧道槽：fwd-open/fwd-auth 只是质询状态；隧道建立才占连接级单槽
   （同一连接同时只有一条活跃转发流）。文件拉取/上传与之互不阻塞（二进制块
   按「fwd-data 头声明归属」先行路由）。

## 与其它模块的关系

| 模块 | 关系 |
|---|---|
| `conn.go`（共享核心） | 泵内分派：fwd-open/fwd-auth/fwd-data/fwd-close 头帧 + fwd-challenge/fwd-ok/fwd-err 响应；`connState.fwd/fwdHs/fwdCh` 单槽定义；OnClose 关 out 释放读侧 |
| `inbound.go`（入站角色） | uploadWorker 的 `fwdCh` 分支写隧道（H5 同款架构，IO 移出消息泵） |
| `outbound.go`（出站角色） | routeResponse 与 routeForwardResponse 同槽路由（conn.go 按帧类型分流）；reqId 生成同源（客户端侧 randHex8） |
| `peerjs_service.go` | 规则/质询表挂在 PeerJSService 上（forwardMu/nonceMu/forwardRules/fwNonces） |
| `internal/router/peerjs_routes.go` | HTTP 端点 4 个（create/connect/list/close） |
| `config` | `PEERDRIVE_FORWARD_RULES` 环境变量装载 |
| 旧实现 | legacy libp2p `/peerdrive/forward/1.0.0` 已删除（doc/LEGACY.md） |

## 坑与设计决策

1. **legacy 为什么重写**：明文 `KEY xxx` 单行认证（无密钥交换，key 落线）、无
   端口白名单、无防重放。新实现全部 verb 级重建，不在旧代码上打补丁。
2. **nonce 一次性 + TTL**（serveForwardAuth:236-241）：取出即标 used 再 delete
   （防重放：同一 nonce 二次提交必失败）；5 分钟过期（防长时间占用内存）；
   上限 64 防 fwd-open 洪水（先清过期再判满）。
3. **HMAC 验证遍历规则表**（forward.go:252-261）：key 原文只在服务端内存、
   客户端只持有 key；`hmac.Equal` 常量时间比较防时序侧信道。验证通过才取
   ports——**端口越权单独判定**（matchedKey 已确定后才查白名单，报错文案
   "port not authorized" 与 "unauthorized" 区分但不泄露规则）。
4. **握手不占隧道槽**：fwd-open/fwd-auth 只是质询状态；隧道建立才占连接级
   单槽——避免恶意端反复握手把连接转发能力打满。
5. **SSRF 防护**（forward.go:289）：`net.Dial("tcp", "127.0.0.1:%d")`——转发
   目标是本节点端口，不许打任意内网 IP（规则表只控制端口，不控制目标主机）。
6. **challenge/done 双 channel**（forward.go:76-79）：共用 channel 会让阶段1
   等 challenge 后无法区分「待继续」与「终局」——各自独立 close 一次。
7. **转发块写不卡消息泵**（fwdCh + worker）：对端 TCP 背压若直接同步写会
   head-of-line 冻结整条连接（H5 同款思路）；写失败静默丢弃——转发是尽力而
   为的流，不因隧道死亡拖垮文件传输。
8. **fwd-data 无隧道防御**（conn.go 泵内）：非法 fwd-data 头/块静默丢弃并清
   pending、不 panic、不建状态——公共信令上可被一行帧打崩（与 H1 同类威胁）。
9. **单槽语义的主动性**：CloseForwardStream 先清槽再通知对端（close 端点语义
   =「立刻可开新隧道」）；serveForwardClose 按 reqID 幂等清槽（对端先发 close
   场景）。
10. **keyID 截断**：审计日志只记 key 前 16 hex，不落全量（key=凭证，防日志
    泄露）。

## 测试

全部在 `forward_test.go`（8 个测试），不走真实 WebRTC/WS——用 fakeSession +
`bindConn` 全链路（含 uploadWorker，转发块经 fwdCh 投递由 worker 写隧道，与
文件帧路由共用同一套泵内逻辑）。辅助：`startEchoServer`（loopback echo TCP）、
`fwdHMAC`（与客户端计算对称）。

| 测试 | 发现背景 |
|---|---|
| `TestForward_HandshakeAndData`（:117） | **功能测试**：服务端握手全流程（fwd-open→challenge→auth→ok）+ 双向数据透传（客户端块经 fwdCh→worker→TCP；echo 回包经 pump→fwd-data 帧回） |
| `TestForward_AuthRejected`（:165） | **权限控制核心断言**：坏 key → fwd-err；**nonce 重放**（同一 nonce 二次提交）→ "no challenge"（已消费）；合法请求确实建了隧道（重放被拒的前提成立） |
| `TestForward_PortNotAuthorized`（:206） | **端口越权**：key 合法但端口不在授权列表（65000）→ "port not authorized"，不泄露规则 |
| `TestOpenForward_ClientSide`（:226） | **客户端全链路**：fakeSession 扮演服务端——握手（open→challenge→auth→ok，断言 HMAC 用调用方 key 计算）、写隧道 → 对端收 fwd-data 头+块、对端注入块 → 调用方读出 |
| `TestForward_FwdDataWithoutTunnel`（:320） | **防御性测试（手写协议测试时想到）**：恶意对端不发握手直接发 fwd-data 头/块，泵内路由必须优雅处理（不 panic、不建状态）；随后正常握手仍可用 |
| `TestForward_CloseStream`（:334） | **主动断开**（CloseForwardStream）：对端收到 fwd-close、本端隧道槽清空、out 关闭（调用方读侧 EOF） |
| `TestForward_ListAndInfo`（:359） | **管理面**：ListForwardStreams 返回隧道快照（peer_id/port 正确） |
| `TestForward_TimeoutNoChallenge`（:382） | **防御性测试**：对端不回包（伪造/宕机）时 OpenForward 必须超时退出，不能永久挂住调用方或占住 fwdHs 单槽（超时后握手槽必须释放） |

## 文件清单

- `back/internal/transport/forward.go`（461 行）——本模块
- `back/internal/transport/forward_test.go`（396 行）——8 个测试
- `back/internal/transport/conn.go` —— fwd 单槽定义、泵内 fwd-data 路由、OnClose 隧道清理
- `back/internal/transport/inbound.go` —— uploadWorker fwdCh 分支（写隧道）
- `back/internal/router/peerjs_routes.go` —— HTTP 端点（create/connect/list/close）
- `doc/REFACTOR.md` §3.9（forward v2 重建记录）、§4（帧协议）
- `doc/LAYERS.md` §6（forward 边界说明：verb 语义属 ②，底层隧道实现可抽离）
- `doc/LEGACY.md` —— legacy libp2p forward 处置记录