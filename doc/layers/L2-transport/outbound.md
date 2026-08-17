# 出站角色（outbound.go）

> 一句话职责：本端发起 verb 并收集响应的全集（「我问别人」）——发起 `req` 拉取
> （OpenStream/FetchFromPeer）、按 reqId 路由响应（meta/data/done/err）、流式
> reader 与完整性校验。

## 职责

`outbound.go` 是帧协议层的「出站角色」实现，与 `inbound.go`（应答对端）相对。
同一连接双工并发复用：本端经此文件发起拉取，同时服务对端发来的 verb。

本文件负责三块：

1. **发起拉取**：`OpenStream`（流式，source 体系 peerSource 适配入口）/
   `FetchFromPeer`（[]byte 兼容封装）→ `openStream` 发 `req` 帧并建
   `fetchState` 路由表项。
2. **流式读取**：`fetchReader` 从有界块队列消费数据块，处理 done/err/超时/取消，
   全量请求在 EOF 时做 sha256 校验（内容寻址兜底）。
3. **响应路由**：`routeResponse` 把对端回帧（meta/data/done/err）按 reqId 投递到
   对应 fetch 状态——含全部防御性校验（H6：尺寸上限、done 完整性）。

连接关闭时的清理（errCh 投递 + close(closed)）在 `conn.go` 的 `bindConn`
OnClose 里统一做，本文件只管发起与收集（`openStream` 的 cleanup 与 OnClose
幂等协作）。

**不做**：不处理 admin 帧（admin.go）、不处理 forward 握手响应
（forward.go 的 `routeForwardResponse`，conn.go 按帧类型分流）、不落盘
（上传是入站角色职责）。

### 出站 API 一览

| API | 签名 | 用途 |
|---|---|---|
| `OpenStream` | `(peerID, hash, offset, size) → io.ReadCloser` | 流式拉取（peerSource 适配入口）；Close 可提前取消 |
| `FetchFromPeer` | `(peerID, hash, offset, size) → ([]byte, error)` | []byte 整体拉取（兼容旧调用方/集成测试） |
| `routeResponse` | `(st *connState, r dcResp)` | 泵内 default 分支调用，按 reqId 路由响应帧 |
| `stateFor` | `(c Session) → *connState` | 连接状态查找（pending 表） |
| `hashMatchesSHA256` | `(hash string, data []byte) → bool` | 内容寻址校验（旧路径/防御用） |

## 关键机制

### 发起（OpenStream → openStream，outbound.go:32,58）

```
OpenStream(peerID, hash, offset, size) → io.ReadCloser
  1. conns[peerID] 查连接（无 → error）
  2. openStream：
     - reqId = uuid.NewString()（UUID v4，跨连接唯一——randHex8 仅 32bit 会碰撞）
     - fetchState{q: chan []byte(8), done, errCh(1), closed} 注册进 st.fetches[reqID]
     - SendJSON(req 帧) → 返回 fetchReader
  3. cleanup（sync.Once）：delete 路由表 + close(f.closed)（幂等防双 close panic）
```

`FetchFromPeer` 保留 []byte 签名兼容旧调用方/集成测试，内部 `OpenStream` +
`io.ReadAll`——流式语义下全量请求的 sha256 校验由 reader 在 EOF 时完成，
语义与旧实现一致。

### fetchState（conn.go:133，出站角色的状态核心）

```
fetchState{
  reqID    string         // 路由键（UUID v4）
  size     int64          // 期待中的 data 块大小（routeResponse 设，上限校验见 maxPeerFetchSize）
  received int64          // 已投递队列的字节数（done 完整性校验用）
  q        chan []byte    // 数据块队列（有界 8：消息泵投递 / fetchReader 消费，背压不占泵）
  done     chan struct{}  // close → 对端 done 帧（传输完成；q 中剩余块仍可消费）
  errCh    chan error     // 错误（含连接关闭，缓冲 1 不阻塞泵）
  closed   chan struct{}  // 本地取消（reader.Close）：pump 停止投递，块丢弃
}
```

数据块**不驻留 state**（流式：投递到有界队列由 reader 消费），`received` 只做
字节计数——用于 done 帧的完整性校验。

### 一次拉取的完整帧交换（含 range 请求）

```
本节点（出站角色）                      对端（入站角色 serveFile）
  │ {type:"req",hash:"<64hex>",offset:0,size:-1,reqId:"<uuid-v4>"} →│
  │← {type:"meta",hash,total:1024,reqId}
  │← {type:"data",hash,offset:0,size:700,reqId} + 700B 二进制块
  │← {type:"data",hash,offset:700,size:324,reqId} + 324B 二进制块
  │← {type:"done",hash,offset:0,size:1024,reqId}
  → reader EOF：全量请求（offset==0 && size<0）→ sha256 校验
```

对应流程：
1. `openStream` 发 req 帧，注册 fetchState 到 `st.fetches[reqID]`。
2. 泵内收到 `meta` → routeResponse 校验 total 上限 → 无 expect。
3. 泵内收到 `data` 头 → routeResponse 校验 size → `st.expect = f`。
4. 泵内收到二进制块 → `st.expect` 命中 → 投递 `f.q`（有界 8，背压不占泵）+
   `f.received += len`；`received >= f.size` 清 expect。
5. 泵内收到 `done` → routeResponse 校验 `done.Size == f.received` → close(f.done)。
6. reader 消费 q；done 后 q 空 → EOF 前非阻塞查 errCh → finish：全量请求
   校验 sha256，不匹配报 "content hash mismatch"。

range 请求（offset>0 或 size>=0）：不开启 verify（无完整内容可比），`done.Size`
依然必须等于实收字节——完整性校验与内容校验解耦（H6 vs H5）。

### 流式读取（fetchReader.Read，outbound.go:130）

```
循环：
  1. buf 有剩余 → copy 返回
  2. select 四路：
     - q ← 块：verify 时喂 sha256，buf = chunk
     - done ← 对端 done 帧：q 仍可能有块（pump 顺序：块先入队、done 后 close）
       一次只取一块——buf 消费完外层 for 回 select（done 一直 ready 再取下一块），
       直到 q 空才 EOF；EOF 前非阻塞检查 errCh（防 err 帧被 done 掩盖）
     - errCh ← 错误：finish(err) 返回
     - fetchIdleTimeout（5 分钟块间隔超时）/ ctx.Done：finish 报错
```

- **块间隔超时**：旧实现是 5 分钟总超时——流式下总超时对大文件（8GB 多块）
  无意义，改为「对端 5 分钟不发任何数据块视为卡死」（原总超时语义由对端连接
  保活覆盖）。
- **verify**：仅全量请求（offset==0 && size<0）开启——EOF 时 `finish` 比较
  sha256，不匹配报 "content hash mismatch"（H5 内容寻址兜底，防中间人/对端
  损坏数据静默入库）。
- **finish 幂等**（outbound.go:189）：eof 标记防重复；全量请求 EOF 时校验
  sha256；清理（cleanup）只执行一次。
- **Close 提前取消**：finish(io.ErrClosedPipe)——本地丢弃后续块（pump 经
  f.closed 停止投递），不断连。

### 响应路由（routeResponse，outbound.go:231）

对端回帧（reqId 匹配 fetches[reqID]）按类型处理；全部防御逻辑在这里：

| 帧 | 处理 |
|---|---|
| `meta` | `Total > maxPeerFetchSize(8GB)` → reject（total 是全量大小，range 请求时 ≠ 本次接收量，只做上限校验） |
| `data` | `Size <= 0 || Size > maxPeerFetchSize` → reject（H6 无界分配）；否则 `f.size = r.Size`，`st.expect = f`（期待下一二进制块，泵内按此路由） |
| `done` | 幂等（重复 done 不处理，防重复 close(done) panic）；**完整性校验**：`done.Size`（对端声明的实际发送字节）必须等于 `f.received`（已投递字节），否则 reject（H6 截断文件当成功） |
| `err` | reject（迟到 err 帧：done 已 close 则忽略；errCh 有缓冲不阻塞） |

- `f.size` 是「期待中的 data 块大小」——泵内 `received >= size` 时清 expect。
- `reject` 只往 errCh 投递一次错误，done 之后到达的帧一律忽略（finish 幂等）。

## 与其它模块的关系

| 模块 | 关系 |
|---|---|
| `conn.go`（共享核心） | 泵内二进制块按 `st.expect` 投递到 `fetchState.q`；OnClose 统一清理（errCh 投递 + close(f.closed)）；`routeResponse` 由泵内 default 分支调用 |
| `inbound.go`（入站角色） | 同一连接双工：本文件发的 `req` 由对端 serveFile 应答；本文件 routeResponse 收集。`connState.fetches/expect` 归出站，`pendingUpload/binCh` 归入站，平铺共享 |
| `source/peer.go`（PeerSource） | `OpenStream` 是 peerSource 的适配入口（source 体系：本地命中即返回，未命中降级 peer） |
| `service/peerjs_service.go` | `FetchFromPeer` 被 /peerjs/fetch 端点等直接调用（REFACTOR §3.8 边界：保持语义，未切 SourceManager） |
| `file_index.go` | 拉取路径解析在入站侧（serveFile 优先索引 + CAS 兜底）；本文件只按 hash 拉 |
| `forward.go` | `routeResponse` 与 `routeForwardResponse` 同槽路由（conn.go 按帧类型分流：fwd-* 归 forward，其余归本文件） |

## 坑与设计决策

1. **H6 无界分配 OOM**（maxPeerFetchSize，outbound.go:27）。data 帧声明的块大小/
   文件大小无上限 → 恶意对端声明 1<<62 并持续发 data 帧 → `f.got` 无界增长。
   修复：`data.Size` 与 `meta.Total` 均设 8GB 上限（与上传上限一致）。
2. **H6 静默数据损坏**（routeResponse done 分支，outbound.go:277）。旧实现 done
   不校验实收字节——对端只发 meta+done 就把截断/空数据当成功返回。修复：
   `done.Size` 必须等于 `f.received`（对照 done 帧的 Size 而非 meta.total——
   range 请求场景 total 是全量大小）。配合流式侧 EOF 前 errCh 检查，err 帧
   不被 done 掩盖。
3. **H5 内容寻址兜底**（fetchReader.finish，outbound.go:194）。字节数对得上但
   内容不对（对端损坏/中间人篡改）→ EOF 时 sha256 校验失败报错。仅全量请求
   开启（range 请求无完整内容可比）。
4. **reqId 用 UUID v4**（outbound.go:60）。reqId 是响应路由键，randHex8 仅
   32bit，并发高时可能碰撞——UUID v4 保证跨连接唯一。
5. **done 后 q 仍有块**（outbound.go:149-173）。pump 顺序保证块先入队、done 后
   close(done)。Read 的 done 分支**一次只取一块**——曾写循环取块，循环里 buf
   赋值覆盖未消费的块（块1 丢失 bug，流式测试复现）。
6. **幂等清理**（cleanup sync.Once + 幂等 close，outbound.go:75-89）。连接先断开
   场景：OnClose 可能已 close(f.closed)——重复 close 会 panic；select 检查后
   close。finish 本身也幂等（eof 标记）。
7. **空文件可拉取**（hashMatchesSHA256，outbound.go:216）。`len(data)==0` 不应
   被直接拒绝：sha256(空) 是合法内容寻址值。hash 是用户输入，须先确保合法 64
   位 hex（否则比较恒失败）。
8. **错误收集不阻塞**（reject 的 select+default，outbound.go:248-251）。errCh
   缓冲 1，重复错误/迟到错误直接丢弃，不挂起消息泵。
9. **流式改造的遗留语义**（FetchFromPeer 保持 []byte）。source 体系流式化时
   requestFile 改块队列 + pump 投递，但 FetchFromPeer 签名保留兼容调用方；
   旧实现 8GB 文件全量 buffer OOM 风险由流式消除。
10. **连接关闭 → reader 错误而非 EOF**（conn.go OnClose）。OnClose 向每个
    fetch 的 errCh 投递 "connection closed"，reader 返回错误不悬挂；close(closed)
    幂等检查（reader 可能已自行 cleanup）。测试用错误不能是 io.EOF——ReadAll
    把 EOF 当正常结束。

### 与 source 体系的协作（peerSource 适配）

`OpenStream` 是 `internal/source/peer.go`（PeerSource）的流式适配入口，链路：

```
source 体系路由（本地未命中） → PeerSource.Open(...) → PeerJSService.OpenStream
  → conns[peerID] → openStream 发 req → fetchReader
  → reader 给 Source 上层逐块读取（CapStream 语义）
```

- 大文件只走 CapStream（OpenRange 拒绝 CapFile 源——防 8GB 全量 buffer OOM，
  REFACTOR §3.8）。
- `FetchFromPeer`（[]byte 兼容签名）被 `/peerjs/fetch` 端点直接调用——保持
  语义，未切 SourceManager（REFACTOR §3.8 边界说明）。
- 连接级 expect 单槽约束：同一 peer 同时只允许一个 fetch 流（per-peer
  Mutex.TryLock 串行尝试，见 PeerSource）——这是 fetchState 单 expect 槽的
  业务层体现。

### 对端响应帧的防御矩阵（routeResponse 全部分支）

| 帧 | 合法场景 | 拒绝场景 | 拒绝动作 |
|---|---|---|---|
| `meta` | total ≤ 8GB | total > 8GB（恶意声明） | errCh ← reject |
| `data` | 0 < size ≤ 8GB | size ≤ 0 或 > 8GB（H6 无界分配） | errCh ← reject，不设 expect |
| `done` | size == received（实收一致） | size != received（H6 截断当成功） | errCh ← reject，不 close(done) |
| `err` | 任何 msg | 迟到（done 已 close） | 忽略（reject 内 select done） |
| 未知 reqId | — | reqId 不在 fetches 表 | 静默忽略（返回） |
| 空 reqId | — | 无路由键 | 静默忽略（返回） |

防御要点：**reject 不 panic**（done 的幂等 close 检查）、**reject 不阻塞**
（errCh 缓冲 1 + select default）、**reject 不重复**（迟到帧忽略）。配合
reader 侧 EOF 前非阻塞查 errCh——err 帧永远不被 done 掩盖。

### fetchReader 生命周期状态

| 状态 | 进入条件 | 退出条件 | 退出动作 |
|---|---|---|---|
| 进行中 | openStream 注册成功 | 收到 done / err / 超时 / ctx 取消 / Close / 连接关闭 | — |
| 完成（EOF） | done 帧 + q 空 | — | finish：全量请求校验 sha256；错误路径返回 err |
| 失败 | errCh 有错 | — | finish(err) 返回错误 |
| 已取消 | reader.Close() | — | finish(io.ErrClosedPipe)；pump 停止投递 |

finish 是唯一出口，幂等（eof 标记）：重复调用直接返回。cleanup（delete
fetches + close(closed)）经 sync.Once 只执行一次——与 bindConn OnClose 的
清理协作（连接先断开时 OnClose 已做，cleanup 幂等跳过）。

## 测试

| 测试 | 发现背景 |
|---|---|
| `TestOpenStream_StreamingRead`（stream_test.go:57） | **流式改造（source 体系）**：旧 requestFile 全量 buffer 内存驻留，8GB 文件 OOM 风险；改块队列流式后验证块投递→消费→校验链路（含 reqId 必须携带、sha256 校验） |
| `TestOpenStream_CloseCancel`（stream_test.go:131） | **防御性**：提前 Close 后 pump 投递不阻塞（closed 通道放行）、路由表清理、重复 Close 幂等 |
| `TestOpenStream_ConnClosed`（stream_test.go:149） | **防御性**：连接关闭后 reader 返回错误而非悬挂（errCh 投递；错误不能用 io.EOF——ReadAll 把 EOF 当正常结束） |
| `TestRouteResponse_DataSizeCap`（peerjs_service_test.go:174） | **H6 无界分配 OOM**：恶意 data 帧声明 1<<62 → 必须 errCh 报错（≤8GB 上限） |
| `TestRouteResponse_DoneSizeMismatch`（peerjs_service_test.go:192） | **H6 静默数据损坏**：对端只发 meta+done（实收 0 字节 vs 声明 100）把截断数据当成功 → 必须报错且不得 close(done) |
| `TestRouteResponse_DoneSizeMatch`（peerjs_service_test.go:214） | 正常 done（Size 与实收一致）→ 成功（done 已 close = 传输完成，流式语义：数据经 f.q 消费） |
| `TestHashMatchesSHA256_AllowsEmptyFile`（peerjs_service_test.go:316） | **代码审阅**：原实现 `len(data)==0` 直接 return false，sha256(空) 合法空文件永远无法从对端拉取。修复：移除空数据特判 |
| `TestServeFile_InvalidHashNoPanic` / `TestServeFile_IndexPathOutsideRoot`（peerjs_service_test.go:130,145） | H1/H2 是入站侧漏洞，但 H1 的触发面是对端 req——与出站发起的帧同协议；列为交叉参照 |

## 文件清单

- `back/internal/transport/outbound.go`（285 行）——本模块
- `back/internal/transport/conn.go` —— fetchState 定义、泵内 expect 路由、OnClose 统一清理
- `back/internal/transport/stream_test.go` —— OpenStream/fetchReader 全链路测试
- `back/internal/transport/peerjs_service_test.go` —— routeResponse 防御测试 + fakeSession 基础设施
- `back/internal/source/peer.go` —— PeerSource（OpenStream 的业务消费方）
- `doc/REFACTOR.md` §3.8（统一 source 体系、requestFile 流式化）、§4（帧协议）
- `doc/TRANSPORT-REVIEW-2026-08-15.md` —— H5/H6 原始发现与修复记录