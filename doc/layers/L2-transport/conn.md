# 连接共享核心（back/internal/transport/conn.go）

> 层归属：AOP ② 帧协议层（见 doc/LAYERS.md §1）
> 帧协议 verb 定义见 doc/REFACTOR.md §4，本文只写实现机制，不重复定义协议。

**一句话职责**：一条连接的「共享核心」——帧类型定义、reqId 状态机、二进制块路由、
连接生命周期清理；inbound/outbound 两角色共用的机制只此一份，禁止复制。

## 职责

### 解决什么问题

WebRTC DataChannel（及本地 WS）是全双工对称连接：同一条 Session 既要**应答**对端发
来的 verb（入站角色），又要**发起**自己的 verb 并收集响应（出站角色）。如果两个角色
各自维护一套「帧怎么解析、二进制块归谁」的逻辑，就会产生两份互相漂移的协议实现——
任何一边改了帧语义，另一边立刻违反协议一致性。

conn.go 把所有**连接级**机制收成单点（conn.go:3-11 头注释明示「禁止复制」）：

- 帧类型（`dcReq`/`dcResp`）——请求/响应两角色共用同一结构
- 连接状态机（`connState`）——fetches/expect 归出站，pendingUpload/binCh 归入站，
  adminUp/fwd 归各自子系统，**平铺共享不拆两份**
- 分派泵（`bindConn` 的 OnMessage）——文本帧按 verb/reqId 路由，二进制块按
  「连接级 expect」状态机挂到最近的声明头所属请求
- 连接关闭的统一清理（fetches 放行、上传 worker 退出、forward 隧道关 out）

### 在 AOP ② 中的位置

```
① peerjs（传输原语：信令/DataChannel/流控/原子帧）
      ↑  Session 接口（ws_session.go / rtc_session.go）
② transport
   ├── conn.go        ← 本文：共享连接核心（分派泵 + 状态机 + 清理）
   ├── inbound.go     入站角色（serveFile / serve* 索引 verb / uploadWorker）
   ├── outbound.go    出站角色（OpenStream / FetchFromPeer / routeResponse）
   ├── forward.go     端口转发 v2（fwd-* verb，单槽 fwd/fwdHs/fwdCh）
   ├── admin.go       管理面 verb（单槽 adminUp，仅本地会话）
   └── file_index.go  SQLite 持久化（两角色共用）
      ↓
③ admin 切面（内部转发 gin engine）  /  ④ controller → service → repository
```

上下游：

- **上游**：① peerjs 的 `*peerjs.Connection`（WebRTC）与 gorilla 的 `*websocket.Conn`
  （本地 WS）经 `Session` 接口适配后进入本模块；peerjs 层的 `SendFrame` 原子性
  （sendMu + 低水位流控）是本模块「头+块原子连续」假设的物理基础（REFACTOR.md §4
  约束 2、§5 并发流控死锁坑）。
- **下游**：inbound.go 的 `serveFile`/`serve*` 与 outbound.go 的 `routeResponse` 是
  分派泵的叶节点；admin.go / forward.go 的槽位（`adminUp`/`fwd`）由泵按帧序写入。
  业务层（④）不感知 conn.go，只经 `FetchFromPeer`/`OpenStream` 语义 API 走数据面
  （LAYERS.md §5 禁区：业务核心不直接操作连接）。

## 关键机制

### 1. 帧类型（conn.go dcReq/dcResp）

```go
type dcReq struct {   // 出站发起、入站应答的请求帧
    Type/ Hash / Offset / Size / ReqID
    Trace []string  // 可选（omitempty）：回源链路节点链（serveFile 多源路由防环）
}
type dcResp struct {  // 通用响应帧：拉取响应 + 索引 verb 响应 + forward 握手
    Type/ Hash / Total / Offset / Size / Msg / ReqID
    Path / Name / Seq / Files / LastSeq
    Nonce / Hmac / Port   // fwd-challenge / fwd-auth / fwd-open
}
```

要点：

- **一个 dcResp 承载全部响应**：meta/data/done/err（拉取）与 created/uploaded/ack/
  list-resp/info-resp/deleted/sync-resp（索引）共用，fwd 握手字段（Nonce/Hmac/Port）
  也并入——避免为每个 verb 造结构，分派时只按 `Type` 区分。
- 请求方 reqId 必填（Go 端 UUID v4），浏览器可不传（向后兼容，REFACTOR.md §4 约束 3）。
- **Trace（2026-08-18 新增，回源防环）**：serveFile 多源路由后对端请求可能回源到
  其它节点（A↔B 互连时 B 请求 A 没有的文件 → A→B→A 无限递归）。req 帧带 trace
  （经过的节点链），转发路径节点发现自己在链中即回 `err "loop detected"`（拒绝）。
  根请求不带该字段（omitempty，旧对端兼容）；`serveFile` 经 context 传播
  （transport.TraceKey，OpenStreamFrom 携带），OpenStreamFrom 是传播点。

### 2. connState：平铺共享的连接级状态（conn.go:74-99）

```
connState
├── fetches  map[reqId]*fetchState   ← 出站：本端发起的下载请求（outbound.go 写）
├── expect   *fetchState             ← 出站：当前期待二进制块的下载（routeResponse 写）
├── pendingUpload *uploadState       ← 入站：当前接收中的流式上传（单槽，serveUploadBegin 写）
├── adminUp  *adminUploadState       ← admin：管理面上传收集（单槽，serveAdmin 写）
├── fwd / fwdHs / fwdCh              ← forward：转发隧道（单槽）+ 握手占位 + 块队列
└── binCh / binDone                  ← 连接级 IO worker 的投递队列/退出信号
```

设计要点：

- **不拆两份状态**：两角色状态平铺在同一结构，由不同字段天然分域（conn.go:71-73
  注释）。拆成 inboundState/outboundState 会引入跨结构的握手与锁序问题。
- **单槽语义**：`pendingUpload`、`adminUp`、`fwd` 各自同一连接同时只有一个——这是
  协议「头+块原子连续」的推论（同一时刻最多一个「声明头待其二进制块」窗口）；
  多 source 并发上传走**多连接**并行（uploadState 注释，conn.go:119-120）。
- **所有槽位操作都在 `st.mu` 保护下**，但只做廉价的路由决策，不做 IO（见下）。

### 3. bindConn：分派泵（conn.go:150-293）

`bindConn` 是连接的核心入口，三条路径共用：

1. **注册**（conn.go:151-163）：`conns[ID]`（按 peer id / "local" 查连接）+ `pending[Session]`
   存状态，启动 `uploadWorker` 协程。之后所有帧只经 OnMessage 回调进入。
2. **文本帧分派**（conn.go:166-226）：JSON 解析失败或无 type 静默丢弃；按 type 走：
   - `req`/`create`/`upload`/`list`/`info`/`delete`/`sync` → `go serve*`（入站角色应答，
     goroutine 异步，无共享状态）
   - `admin` → **同步** `serveAdmin`（见坑 #7：异步会导致上传分片丢失）
   - `fwd-open`/`fwd-auth`/`fwd-close` → `go serveForward*`；`fwd-data` → 泵内同步置
     `fw.pending`（声明「下一二进制块归转发隧道」，帧序保证无竞态）
   - `fwd-challenge`/`fwd-ok`/`fwd-err` → `routeForwardResponse`（与文件拉取响应同槽路由）
   - 其它（meta/data/done/err 及索引响应）→ `routeResponse` 按 reqId 路由
3. **二进制块路由**（conn.go:228-292）：按优先级归属，**路由决策在泵内（廉价）、
   IO 在 worker（H5）**：
   ```
   fw.pending（转发块） → st.fwdCh（worker 写隧道）
   pendingUpload        → 计数 → binCh（worker WriteAt；收齐 last=true → Complete）
   adminUp              → 计数 → binCh（worker 写临时文件；超限/超时 abort）
   expect               → f.q（有界队列，fetchReader 消费；本地取消 closed 则丢弃）
   ```

### 4. 并发模型（三条线）

```
消息泵（OnMessage 回调，单 goroutine，帧序保证）
   │  文本帧 → 分发（多数 go 异步执行 serve*）
   │  二进制块 → 路由决策（廉价）→ 投递到 channel
   ▼
连接级 worker（uploadWorker，inbound.go:292）——单 worker 保序
   │  消费 binCh（上传分片 WriteAt/Complete、admin 临时文件收集）
   │  消费 fwdCh（转发块写隧道，背压不卡泵）
   ▼
fetchReader（调用方 goroutine）——消费 f.q，done/errCh 通知结束
```

- **为什么 worker 存在（H5）**：旧实现 `WriteAt`/`Complete`（fsync + 全文件 hashFile）
  同步跑在消息泵上——8GB 上传完成的瞬间，该连接所有其他帧冻结到 Complete 结束
  （头-of-line 阻塞，慢磁盘卡死整条连接）。现在路由在泵内（廉价），IO 在 worker，
  保序由单 worker 保证（conn.go:80-84 注释；REFACTOR.md §5）。
- **有界背压**：`f.q`（8）、`binCh`/`fwdCh`（16）；投递阻塞时连接关闭经
  `binDone`/`f.closed` 放行，不悬挂（conn.go:235-238, 267-269, 275-282）。
- **发送侧串行**：`Session.SendFrame`/`SendJSON` 内部有写锁（peerjs `sendMu` /
  WSSession `sendMu`），并发 serveFile 经锁串行，低水位流控下沉到
  `peerjs.Connection.SendFrame`（连接级全局回调，不再各自注册——替换式回调会被
  覆盖导致并发死等，REFACTOR.md §5 最后一条）。

### 5. 三条协议约束在 conn.go 的实现落点

REFACTOR.md §4 的三条协议约束不是文档口号，每条在代码里都有对应的强制点：

| 约束 | 实现落点 | 破坏后果 |
|---|---|---|
| 1. JSON 控制头必须文本帧、数据块必须二进制帧 | 发送侧：`dcResp{Type:"data"}` 经 `SendFrame`（peerjs/WSSession 写 JSON 头 + BinaryMessage/二进制 body）；接收侧：`OnMessage` 按 `IsText` 分派（conn.go:166, 228） | 对端把控制头当数据块吞掉（REFACTOR.md §5 第三行） |
| 2. data 头与数据块必须原子连续 | peerjs `Connection.SendFrame` 的 sendMu（①层）；conn.go 接收侧按「连接级 expect」状态机挂块（conn.go:271-283），同一时刻最多一个 expect | 二进制块归属错乱、多请求数据交叉 |
| 3. 浏览器可不传 reqId、Go 端始终携带 | 出站 openStream 用 `uuid.NewString()`（outbound.go:60）；入站 serve* 回显对端 reqId | 响应无法配对（Go 端保证 UUID v4 跨连接唯一，randHex8 仅 32bit 会碰撞） |

### 6. 典型流走查：一次 req 拉取（双端各自动作）

以 A 节点经 WebRTC 拉取 B 节点文件为例，展示两条角色路径如何在同一连接上交汇：

```
A（本端）                          B（对端）
outbound.go openStream             inbound.go serveFile（B 的 bindConn 泵 go 启动）
  ├ reqId=uuid，fetches[reqId]=f     ├ IsStrictSHA256 校验 → 路径决策（file_index/CAS）
  ├ SendJSON(req 帧) ────────────→   ├ SendJSON(meta{total})  ← 出站 routeResponse 记 total
  └ fetchReader 等待 f.q             ├ SendFrame(data 头 + 64KB 块) × N（SendFrame 原子）
                                     └ SendJSON(done)
A 的 bindConn 泵：
  ├ meta 文本帧 → routeResponse（expect 不置）
  ├ data 文本帧 → routeResponse：f.size=r.Size，st.expect=f
  ├ 二进制块    → expect 命中 → f.q <- 块（received 计数，收满清 expect）
  └ done 文本帧 → routeResponse：received==done.Size 校验 → close(f.done)
fetchReader：q 消费块 → EOF 前 drain q（pump 顺序保证块先入队）→ sha256 校验 → io.EOF
```

A 侧同一连接上若同时有 B 发来的 req（B 也拉 A 的文件），serveFile 与 fetchReader
互不干扰——文本帧按 verb 与 reqId 区分，二进制块按 expect/pendingUpload 区分
（conn.go:144-149 注释）。

### 7. 典型流走查：一次分片上传（连接级单槽）

```
B 的 bindConn 泵：
  ├ upload 文本帧 → serveUploadBegin（go 异步）：校验 size/offset 对齐 →
  │   st.pendingUpload = &uploadState{...}（30s stale 上限）→ 回 meta{total,offset}
  ├ 二进制块 → up=st.pendingUpload 计数 → binCh <- binaryChunk{up,...}
  │   （last = got>=size 时置位并清 pendingUpload）
  └ 下一 upload 帧 → serveUploadBegin：pendingUpload 非空 → "already in progress"
B 的 uploadWorker（单 goroutine，保序）：
  ├ 消费 binCh → sess.WriteAt(offset, data)（IO 移出泵，H5）
  └ last → sess.Complete()（位图全满 → uploaded{hash,path}；否则 ack{offset} 续传）
```

多 source 并发 = 多连接各传不同分片（UploadSession 位图合并）；同连接分片串行
（请求-响应配对）。上传完成瞬间的 fsync+hash 在 worker 里，消息泵不冻结
（conn.go:80-84 注释）。

### 8. fetchState：流式收集状态（conn.go:133-141）

```
q        chan []byte   // 数据块有界队列（8），泵投递 / reader 消费
done     chan struct{} // 对端 done 帧 → close；q 中剩余块仍可消费
errCh    chan error    // 错误（含连接关闭）
closed   chan struct{} // 本地取消（reader.Close）→ 泵停止投递
```

- 数据块**不驻留 state**（流式），`received` 只做字节计数，供 done 帧完整性校验
  （outbound.go routeResponse 的 H6 校验）。
- 通道语义精挑细选：`done` 一次性 close（重复 done 帧防 panic，routeResponse 内
  幂等检查）、`errCh` 缓冲 1（迟到 err 帧不阻塞泵）、`closed` 幂等 close（连接先
  断开场景与 reader cleanup 双重 close 会 panic，outbound.go:81-87）。

### 6. OnClose：统一清理（conn.go:294-340）

按「锁内只收集、解锁后动作」原则（持锁调用 `Close` 死锁坑，REFACTOR.md §5）：

1. `conns`/`pending` 注销（先查 `pending[c]` 再删，锁外处理）
2. 锁内：adminUp 中止（删临时文件 + 关文件）、每个 fetch 投 errCh + 幂等 close(closed)、
   取 fwd 的 out 连接
3. 解锁后：`close(binDone)` 让 worker 退出（未消费分片直接丢弃——会话残留由
   file_index 10 分钟 reap 清理）、`fwdOut.Close()`（转发调用方读侧随即 EOF）

## 与其它模块的关系

```
conn.go（分派泵） ──帧──▶ inbound.go  serveFile/serveCreate/serveUploadBegin/...
                          （应答对端，安全校验 H1/H2、uploadWorker 落盘）
conn.go（发起+收集）◀──帧── outbound.go  OpenStream/FetchFromPeer/routeResponse
                          （reqId 状态机写入 fetches/expect，H6 上限校验）
conn.go（单槽 fwd）◀──▶  forward.go  fwd-open/challenge/auth 握手 + 数据透传
conn.go（单槽 adminUp）──▶ admin.go  serveAdmin 内部转发 gin engine
conn.go ──fileIndex──▶   file_index.go  SQLite sha256→路径（inbound 读写、outbound 不碰）
conn.go ◀──Session──     ws_session.go / rtc_session.go（两种传输适配）
conn.go ◀──绑定──        peerjs_service.go  connectLoop/onIncomingConnection/BindLocal
```

- **inbound ↔ outbound**：同连接双工复用，互不共享可变状态（除 connState 各自槽位，
  inbound.go:6-7 注释）。
- **file_index**：serve* 索引 verb 是 file_index 的帧协议出口；`UploadSession`
  位图跟踪在 inbound 侧，conn.go 只负责把分片投递给 worker。
- **forward**：握手响应（fwd-challenge/ok/err）与文件拉取响应**同槽路由**
  （routeForwardResponse，conn.go:220-222），隧道建立后才占 `fwd` 单槽（REFACTOR.md
  §3.9：握手不占槽，防隧道洪水）。
- **业务层（④）**：经 `FetchFromPeer("local", ...)` 复用同一拉取路径——本地 WS 会话
  与远端节点零分支（peerjs_service.go BindLocal 注释）。

## 坑与设计决策

> 每条注明来源：代码注释（conn.go/inbound.go/outbound.go）、测试文件、REFACTOR.md。

| # | 坑 | 设计/修复 | 来源 |
|---|---|---|---|
| 1 | 旧实现 `serveFile` 直接 `req.Hash[:2]`，对端发空/短 hash 越界 panic，**在 goroutine 里 panic 直接杀死整个进程**（公共信令上任意节点一行 JSON 打崩全节点） | serveFile 先 `hashutil.IsStrictSHA256` 校验（H1）；测试 TestServeFile_InvalidHashNoPanic | inbound.go serveFile 开头；peerjs_service_test.go TestServeFile_InvalidHashNoPanic |
| 2 | 对端 create 任意绝对路径后可 req 读取（/etc/shadow 攻击链） | file_index 命中路径必须 `IsPathAllowed` 落在允许根内，否则回退内容寻址存储（H2）；测试 TestServeFile_IndexPathOutsideRoot | inbound.go serveFile fallback 分支；peerjs_service_test.go TestServeFile_IndexPathOutsideRoot |
| 2a | 多源路由后回源死循环：A↔B 互连，B 请求 A 没有的文件 → A 回源 B → B 回源 A → 无限递归 | req 帧 trace 节点链，节点发现自己已在链中即拒（`err "loop detected"`）；根请求不带 trace（omitempty 兼容旧对端），OpenStreamFrom 是传播点；测试 TestServeFile_LoopDetected / TestOpenStreamFrom_TracePropagation | inbound.go serveFile 开头；conn.go dcReq.Trace；outbound.go OpenStreamFrom（2026-08-18） |
| 3 | 8GB 上传 Complete（fsync+hash）同步跑在消息泵 → 整条连接头-of-line 冻结 | WriteAt/Complete 移出泵到连接级 worker（H5），泵内只做路由决策；2026-08-18 上传与转发拆成 uploadWorker/fwdWorker 双 worker（8GB 上传不再阻塞转发隧道）；测试 TestUploadWorker_WriteThenComplete + 集成 TestConcurrentLargeFetches | conn.go bindConn（双 worker）；inbound.go uploadWorker/fwdWorker；REFACTOR.md §5 |
| 4 | 对端发 upload 头后不发数据块 → pendingUpload 永久占位，之后该连接所有 upload 全 "already in progress"（连接级 DoS，重连才恢复） | 30s stale 自动清空（M6）；测试 TestServeUploadBegin_StalePendingCleared | conn.go connState.pendingUpload；inbound.go serveUploadBegin；peerjs_service_test.go TestServeUploadBegin_StalePendingCleared |
| 5 | 恶意对端声明超大 data size / 提前 done（meta+done 截断数据当成功）→ 无界分配 OOM / 静默数据损坏 | `maxPeerFetchSize` 8GB 上限 + done.Size 与实收字节比对（H6）；测试 TestRouteResponse_DataSizeCap / DoneSizeMismatch | outbound.go fetchReader/routeResponse；peerjs_service_test.go TestRouteResponse_* |
| 6 | `f.size = r.Size` 后 expect 由 done 帧 close——重复 done 或 err 后到达会双重 close(f.done) panic | routeResponse 幂等检查（done 已 close 即忽略） | outbound.go routeResponse |
| 7 | admin 声明帧初版 `go` 异步 → 后续二进制帧先到泵时 `adminUp` 仍为空，**上传分片全部丢失** | `case "admin"` 泵内**同步**执行 serveAdmin（占槽必须在泵内完成）；测试 admin_test.go | conn.go bindConn；admin.go serveAdmin |
| 8 | 持锁调用 `conn.Close()` 死锁（Go mutex 非重入） | bindConn OnClose 锁内只收集、解锁后 Close | conn.go bindConn OnClose；REFACTOR.md §5 |
| 9 | 并发 serveFile 各自注册 `OnBufferedAmountLow`（pion 替换式回调）→ 只有最后注册者能收到低水位事件，其余死等（4 并发 × 2MB 复现卡死） | 流控下沉 peerjs.SendFrame（全局注册一次 + lowWater 广播），serveFile 零流控代码 | inbound.go serveFile；REFACTOR.md §5 末条 |
| 10 | 连接关闭时 fetch 投递阻塞悬挂 | errCh 投递（非阻塞）+ 幂等 close(f.closed) 放行泵；测试 TestOpenStream_ConnClosed / CloseCancel | conn.go bindConn OnClose；stream_test.go TestOpenStream_* |
| 11 | 对端只发 meta+done 不校验实收字节 → 截断文件当成功 | done.Size 完整性校验（H6） | outbound.go routeResponse |
| 12 | 非法 admin 帧 / 无 type 文本帧 | 泵内静默丢弃（不 panic、不回帧） | conn.go bindConn |

## 测试（transport 包全量，`scripts/test-layers.sh` L2 段）

### 单元测试（无网络，`go test -tags nosqlite ./internal/transport/ -count=1 -skip "^TestAdmin"`）

| 测试文件 | 覆盖 | 发现背景 |
|---|---|---|
| peerjs_service_test.go | 帧路由与安全边界 | fakeSession 注入帧驱动泵（见各测试注释） |
| ├ TestServeFile_InvalidHashNoPanic | 非法 hash → err 帧不 panic | **H1 远程崩溃**：越界切片杀进程 |
| ├ TestServeFile_IndexPathOutsideRoot | 索引命中但路径越权 → 拒绝回传 | **H2 任意文件读取**：防御历史脏数据 |
| ├ TestRouteResponse_DataSizeCap | 超大 data 帧 → errCh | **H6 OOM**：无上限块大小 |
| ├ TestRouteResponse_DoneSizeMismatch/Match | 截断 done 拒绝 / 正常 done 通过 | **H6 静默数据损坏** |
| ├ TestServeUploadBegin_StalePendingCleared | 过期 upload 占位自动清空 | **M6 连接级 DoS** |
| ├ TestUploadWorker_WriteThenComplete | worker 落盘 + uploaded 回帧 | **H5 链路**：IO 移出泵后的回归 |
| └ TestHashMatchesSHA256_AllowsEmptyFile | 空文件 sha256 校验放行 | **代码审阅**：空数据特判误伤合法空文件 |
| stream_test.go | 流式 OpenStream 全链路 | 见各测试注释 |
| ├ TestOpenStream_StreamingRead | meta/data/data/done 分块重组 + sha256 校验 | **source 体系**：旧全量 buffer 8GB OOM 风险 |
| ├ TestOpenStream_CloseCancel | 提前 Close 不阻塞泵、清理路由表、幂等 | 防御性（清理路径回归） |
| └ TestOpenStream_ConnClosed | 连接关闭 reader 报错不悬挂 | 防御性（关闭时序） |
| forward_test.go | fwd 握手/越权/重放/数据透传（8 个单测） | REFACTOR.md §3.9 |

### 集成测试（`-tags "nosqlite integration" ./test/integration/ -count=1 -p 1`，脱外网）

> 2026-08-18（第 6 项优化）：集成测试信令默认指向 TestMain 起的**全局自托管
> signalserver**（httptest 内存服务），数据面为同机真实 WebRTC（host candidate
> 直连，无 STUN）——不再依赖 0.peerjs.com 公共云（无需代理/外网）。
> MQTT 公共 broker 测试需 `PEERDRIVE_MQTT_TEST=1`；线上全链路需 `PEERDRIVE_LIVE_TEST=1`。

| 测试 | 覆盖 | 发现背景 |
|---|---|---|
| interop_test.go TestTwoNodes/ThreeNodes/FourNodesStar | 双/三/四节点互通、星型一对多并发 | 功能验收 |
| interop_test.go TestConcurrentLargeFetches | 4 并发 × 2MB 大文件拉取 | **流控死锁回归**：修复前卡到超时（REFACTOR.md §5） |
| file_lifecycle_test.go TestFileLifecycleEndToEnd | create/req/upload/download/sync 跨节点闭环 | 功能验收 |
| ws_verbs_test.go TestFrameVerbs_* | 分片上传/断点续传/同步链路 | 功能需求 |
| ws_test.go TestLocalWSSessionFetch 等 | 本地 WS 会话 + FetchFromPeer("local") 复用 | 架构决策（§3.5） |
| selfhosted_test.go TestSelfHosted* | 自托管信令协议兼容 + 发现 API 全链路 | 功能需求（§3.6） |
| live_test.go TestLive* | 线上信令+发现+拉文件全链路（`PEERDRIVE_LIVE_TEST=1`） | 线上验证（AGENTS.md 部署节） |

> ⚠️ 集成测试必须 `-p 1` 串行：多组测试共享全局自托管信令，并行会互相干扰。
> 数据面是真实 WebRTC——无 UDP 的沙箱环境（docker 默认）会连不上，可用
> `PEERDRIVE_SKIP_RTC=1` 跳过互联类测试（本地/WS 类不受影响）。

## 文件清单

> 引用一律函数名（行号易漂移，见 REFACTOR.md §10 约定）。

| 文件 | 说明 |
|---|---|
| `conn.go` | 本文：帧类型、connState、bindConn 分派泵、OnClose 清理 |
| `inbound.go` | 入站角色：serveFile（多源路由 FileRouter）/serve* 索引 verb/uploadWorker+fwdWorker（泵的叶节点） |
| `outbound.go` | 出站角色：OpenStream/OpenStreamFrom/FetchFromPeer/routeResponse/fetchReader |
| `ws_session.go` | WSSession：本地 WS 适配（见 sessions.md） |
| `rtc_session.go` | rtcSession：DataChannel 适配（见 sessions.md） |
| `peerjs_service.go` | 装配层：信令生命周期、连接建立、BindLocal、Connect 去重、SetFileRouter |
| `admin.go` | admin verb（conn.go 同步分派 + adminUp 单槽收集） |
| `forward.go` | fwd-* verb（fwd 单槽 + fwdCh worker 写隧道） |
| `file_index.go` | SQLite sha256→路径索引（inbound 读写） |
| 测试：`peerjs_service_test.go` / `stream_test.go` / `forward_test.go` / `file_index_test.go` / `admin_test.go` / `servefile_router_test.go` | 各模块单元测试（发现背景见上表） |