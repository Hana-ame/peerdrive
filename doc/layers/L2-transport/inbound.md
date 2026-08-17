# 入站角色（inbound.go）

> 一句话职责：应答对端发来的 verb 全集（「别人问我答」）——`req` 文件拉取应答、
> 文件索引六 verb（create/upload/list/info/delete/sync）服务端、上传分片落盘 worker。

## 职责

`inbound.go` 是帧协议层的「入站角色」实现，与 `outbound.go`（本端发起）相对。
**角色是「帧角色」，不是连接方向**：WebRTC 连接全双工对称，同一条 Session 同时
承载两角色——一边应答对端的 `req`，一边收集自己发起的请求的响应（分派在
`conn.go` 的 `bindConn` 里按帧类型 + reqId 区分）。

本文件负责三块：

1. **文件服务**（`serveFile` / `openFile`）：对端发 `req` 帧时，把本节点文件按
   分块回传（`meta` → `data×N` → `done`）。
2. **文件索引 verb 服务端**（`serveCreate` / `serveUploadBegin` / `serveList` /
   `serveInfo` / `serveDelete` / `serveSync`）：对端登记外部文件、流式分片上传、
   查询、逻辑删除、增量同步。请求字段统一由 `dcResp` 通用结构承载
   （Hash/Offset/Size/ReqID/Path/Name/Seq）。
3. **上传落盘 worker**（`uploadWorker`）：连接级 IO worker，消费消息泵投递的
   二进制分片（上传落盘、admin 上传收集、转发块写隧道），把慢 IO 移出消息泵。

注意：**不包含**转发握手的服务端处理——`serveForwardOpen`/`serveForwardAuth`/
`serveForwardClose` 在 `forward.go`（forward v2 是 inbound 一环，独立文件承载）。

### 入站 verb 全集（分派见 conn.go bindConn 的 switch）

| 帧 type | 处理函数 | 响应 | 说明 |
|---|---|---|---|
| `req` | `serveFile` | `meta` / `data×N` / `done` / `err` | 文件拉取（可 range） |
| `create` | `serveCreate` | `created {hash,size,name,path,seq}` / `err` | 登记外部文件，不复制 |
| `upload` | `serveUploadBegin` | `meta {total,offset}` → `uploaded`/`ack` / `err` | 流式分片上传（多 source 并发） |
| `list` | `serveList` | `list-resp {files,total}` / `err` | 分页列出 |
| `info` | `serveInfo` | `info-resp {hash,size,name,path,seq}` / `err` | 按 hash 查元数据 |
| `delete` | `serveDelete` | `deleted {hash,seq}` / `err` | 逻辑删除（tombstone） |
| `sync` | `serveSync` | `sync-resp {files,lastSeq}` / `err` | seq 游标增量同步 |

除 `req` 外全部同步小操作——`bindConn` 里 `go` 逃逸（异步 goroutine），不阻塞
消息泵。`admin` 帧例外：同步执行（adminUp 占槽必须在泵内完成，见 conn.go 注释）。

## 关键机制

### 文件拉取应答（serveFile，inbound.go:35）

对端 `req {hash, offset, size, reqId}` → 本节点按范围回传：

```
1. hash 必须 64 位 hex（IsStrictSHA256），否则回 err 帧（H1）
2. 路径解析优先级：
   ① file_index 命中且 IsPathAllowed(fi.Path) → 用索引路径
   ② 索引命中但路径越权（历史脏数据/恶意登记）→ 回退内容寻址存储（H2）
   ③ 未命中索引 → filepath.Join(storageDir, hash[:2], hash)
3. os.Open + Stat → total
4. offset/size 夹取（offset<0→0、offset>total→total、limit 超界→截断）
5. 发 meta{total} → 循环 ReadAt 分块（chunkSize=64KB）发 data 头+块 → done
```

- 块大小常量 `chunkSize = 64 * 1024`（pion SCTP 单消息上限约 256KB，64KB 兼顾流控
  粒度）；写缓冲流控已下沉到 `peerjs.Connection.SendFrame`（连接级全局回调）。
- 发 `meta` 失败直接返回（连接已死，无谓继续）。
- `openFile`（inbound.go:105）是同一内容寻址打开逻辑的无帧封装（供本地/管理面
  直接读文件），hash 校验同样前置。
- 一个 `req` 的响应帧序是 `meta → data×N → done`；任何失败点回 `err` 帧终止。
  对端出站角色（routeResponse）按 reqId 配对，`done.Size` 与实收字节一致性校验
  由对端负责（见 outbound.md）。

### 文件索引 verb 服务端

- **serveCreate**（inbound.go:145）：登记外部文件（sha256 → 绝对路径，不复制
  文件）。`path` 空 → err；`fileIndex.Create` 内部做根目录校验（H2）。响应
  `created {hash,size,name,path,seq}`。
- **serveUploadBegin**（inbound.go:161）：开始一个分片上传接收。
  - 安全：`size < 0 || size > 8GB` 拒绝；`offset` 必须 chunk 对齐（64KB）；
    `offset >= size` 拒绝。
  - **空文件特判**：`size==0` 时没有 data 帧可发，直接 `sess.Complete()` 回
    `uploaded`——否则连接级 `pendingUpload` 会卡到 30s stale 清理（且旧实现
    size<=0 直接拒，两端不对称）。
  - 分片长度：`min(size-offset, uploadChunkSize)`，单次 data 块 ≤64KB。
  - 连接级单槽 `st.pendingUpload`：已占用且未过期 → `err "upload already in
    progress"`（多 source 走多连接）；**占用超过 30s 自动清空**（M6）。
  - 响应 `meta {total, offset: ContiguousOffset()}`——offset 是连续已写偏移
    （resume 起点），由 UploadSession 位图计算。
- **serveList**（inbound.go:227）：`list {offset?, size?}` → `list-resp {files,
  total}`。每项经 `redactDisallowedPath` 脱敏；files 为 nil 时置 `[]`（JSON
  序列化不为 null）。
- **serveInfo**（inbound.go:245）：`info {hash}` → `info-resp`（download 前先查）。
- **serveDelete**（inbound.go:258）：`delete {hash}` → `deleted {hash, seq}`。
  seq 是同步游标，对端 sync 才能跟踪删除（L6）。
- **serveSync**（inbound.go:269）：`sync {seq}` → `sync-resp {files, lastSeq}`
  metadata 增量同步（含 tombstone）。
- **redactDisallowedPath**（inbound.go:136）：对外文件信息的路径脱敏——create 已
  强制根目录内，但历史库/旧版本可能残留根外 Path；serveFile 已回退 CAS，
  list/info/sync 也不能泄露这类绝对路径。

### 上传落盘 worker（uploadWorker，inbound.go:292）

消息泵（conn.go）只做「路由决策」（廉价），落盘 IO 全部移到这里（H5）：

```
select {
  binCh ← 分片：
    - ch.au != nil → admin 管理面上传收集（admin.go 复用同一 worker 架构）
    - 否则 ch.up.sess.WriteAt(offset, data) → 失败回 err 帧
      - ch.last（本分片收齐）→ sess.Complete()：
        done=true → 回 uploaded{hash,path}；位图未满 → 回 ack{offset} 可继续下一分片
  fwdCh ← 转发块：ch.fw.out.Write（尽力而为，失败静默丢弃 + close）
  binDone ← 连接关闭：退出（未消费分片直接丢弃，会话残留由 file_index 10 分钟 reap 清理）
}
```

- 单 worker 保序：分片按到达顺序落盘，与帧协议顺序一致。
- 为什么必须单 worker 串行：多 goroutine 并发 WriteAt 会乱序（分片顺序由帧序
  决定），且 Complete 的「位图全满」判定要求写操作可见性有序。
- 转发块也复用此 worker（H5 同款思路），写方向不卡消息泵。
- 退出语义：`binDone` 在连接关闭时 close（conn.go OnClose）——worker 从 select
  退出，未消费的分片直接丢弃（连接已死），残留的 UploadSession 由 file_index
  的 10 分钟 reap 兜底清理。

### 连接级槽位（connState，本角色占用的部分）

入站角色在 `connState` 上占两个槽位（平铺共享，与出站的 fetches/expect 互不
干扰）：

| 槽位 | 用途 | 生命周期 |
|---|---|---|
| `pendingUpload` | 当前接收中的流式上传（单流） | 建立于 serveUploadBegin，收齐（泵内 got>=size）或 30s stale 清理清空 |
| `binCh`/`binDone` | 二进制分片投递通道 + worker 退出信号 | bindConn 创建，OnClose close(binDone) |

`uploadState`（conn.go:121）记录本分片的 reqID/offset/size/got/sess/created——
`got` 由泵内计数（`up.got += len(msg.Data)`），收齐即清 `pendingUpload` 并投递
`last=true` 的 chunk 触发 Complete。

### 一次分片上传的完整帧交换（多 source 语义）

```
对端（source 1）                    本节点（入站角色）
  │ {type:"upload",name:"a.bin",size:131072,offset:0,reqId:"u1"} →│
  │← {type:"meta",total:131072,offset:0,reqId:"u1"}
  │ {type:"data",...} + 64KB 二进制块（chunk 0）───────────────→│
  │← {type:"ack",offset:65536,reqId:"u1"}        ← 位图未满，续传起点
对端（source 2，另一连接）
  │ {type:"upload",name:"a.bin",size:131072,offset:65536,reqId:"u2"} →│
  │← {type:"meta",total:131072,offset:0,reqId:"u2"}   ← 连续已写仍是 0？否：
  │                                                    （本图是顺序示例；多 source
  │                                                     乱序时 offset=ContiguousOffset()）
  │ {type:"data",...} + 64KB 二进制块（chunk 1）───────────────→│
  │← {type:"uploaded",hash:"<sha256>",path:"...",reqId:"u2"}  ← 位图全满，最后一片的请求方收到
```

要点：
- 同 name 重开会话 = 幂等复用（`BeginUpload` 按 sanitize 后的 name 找会话，
  size 必须一致，M7）。
- 每个分片经 `serveUploadBegin` → 泵内二进制路由（pendingUpload 槽）→
  `binCh` → worker `WriteAt`；`last` 分片触发 `Complete`。
- 位图未满 → 回 `ack{offset}` 而非 `uploaded`——客户端可据此继续下一分片
  （offset 建议从 `ContiguousOffset` 续传）。
- 空文件（size=0）无 data 帧：serveUploadBegin 内直接 Complete 回 uploaded。

### 帧协议约束对照（本文件必须遵守，REFACTOR §4）

1. **文本帧 = JSON 控制头，二进制帧 = 数据块**：data 头必须 `SendJSON`，
   数据块必须 `SendFrame`（本文件的 serveFile / 泵内路由都遵守；发反了对端
   把控制头当数据块吞掉）。
2. **头-块原子连续**：`SendFrame` 的 sendMu 保证——接收端按连接级 expect
   状态机把二进制块挂到最近的 data 头所属请求。
3. **reqId**：Go 端始终携带（UUID v4）；浏览器端可不传（向后兼容）。入站
   角色回显 reqId 保持配对。

## 与其它模块的关系

| 模块 | 关系 |
|---|---|
| `conn.go`（共享核心） | `bindConn` 分派 verb → 本文件 serve*；`connState` 的 `pendingUpload`/`binCh`/`binDone` 槽位由入站角色独占 |
| `outbound.go`（出站角色） | 同一连接双工并发，`connState` 内槽位平铺共享互不干扰（fetches/expect 归出站，pendingUpload/binCh 归入站） |
| `file_index.go` | 本文件是索引 verb 的「线上面」：协议层只做校验/分派/回帧，真实状态在 `UploadSession`（位图、续传、reap） |
| `admin.go` | admin 二进制上传的收集分片经 `binCh` 投递到本 worker（`ch.au != nil` 分支），复用 H5 架构 |
| `forward.go` | 转发块的写隧道也走本 worker 的 `fwdCh` 分支；转发握手服务端在本文件之外（forward.go） |
| `peerjs_service.go` | `serveFile` 是 `PeerJSService` 的方法——storageDir/fileIndex/conns 等状态在服务上 |
| `ws_session.go` / `rtc_session.go` | `Session` 接口抽象（SendJSON/SendFrame/OnMessage/OnClose），入站角色只依赖接口不依赖具体通道 |

## 坑与设计决策

1. **H1 远程崩溃：`req.Hash[:2]` 越界 panic**（serveFile:36）。旧实现直接切片，
   对端发空/短 hash 即 panic——serveFile 跑在 goroutine 里，panic 直接杀死整个
   进程（公共信令网络上任意节点一行 JSON 打崩全节点）。修复：先
   `hashutil.IsStrictSHA256` 校验，非法回 err 帧。
2. **H2 远程任意文件读取**（serveFile:41-49 + serveCreate）。旧实现接受任意绝对
   路径：对端 `create /etc/shadow` 拿 hash 后 `req` 即可读取，info verb 还会泄露
   路径。修复：file_index 命中路径必须 `IsPathAllowed`（Abs + EvalSymlinks 解析
   后判断是否在根目录内），越权回退内容寻址存储。**历史脏数据防御**：索引里已
   存在的根外路径（旧版本写入）也会被拒绝回传，不回退信任。
3. **H5 头-of-line 阻塞**：WriteAt/Complete（fsync + 全文件 hashFile 是慢操作）
   之前同步跑在 pion 消息泵上——一个 8GB 上传完成的瞬间整条连接其他帧全部冻结。
   修复：路由决策留在泵内（廉价），IO 交给连接级 worker（本文件 uploadWorker）。
   另：并发 serveFile 曾各自注册 OnBufferedAmountLow（替换式回调被覆盖 → 死等），
   现统一由 peerjs 层 sendMu + lowWater 广播处理。
4. **M6 pendingUpload 无超时 → 连接级 DoS**（serveUploadBegin:205-219）。对端发
   upload 头后不发数据块会永久占用槽位，之后该连接所有 upload 全部 "already in
   progress"（重连才恢复）。修复：`uploadState.created` + 30s 过期自动清空。
   注意：清空的是连接级槽，会话本身留待 file_index 10 分钟 reap，不影响多 source。
5. **空文件是合法内容寻址值**（serveUploadBegin:162,177-195）。sha256(空) =
   e3b0c442...，拉取侧已支持空文件校验；上传侧 `size==0` 必须能完成（无 data 帧
   可发 → 直接 Complete），且 `offset` 必须为 0（"offset beyond size"）。
6. **upload 响应是 `meta` 而非专用头**：与文件拉取共用 meta 帧类型（offset 字段
   返回连续已写偏移，供断点续传），reuse 帧协议无需新 verb。
7. **err 帧语义**：所有失败统一回 `err {msg, reqId}`（reqId 回显保持请求-响应
   配对），对端 routeResponse 按 reqId 收错。
8. **路由决策与 IO 分离是硬约束**：二进制块「归谁」（fwd/upload/admin/fetch）
   必须在消息泵内定（保持与文本帧的顺序一致性），落盘由 worker 做（保序由单
   worker 保证）。任何把 IO 挪回泵的改动都会复活 H5。
9. **serve* 全 goroutine 逃逸**：除 admin 外，所有入站处理都 `go` 逃逸（conn.go
   bindConn）——慢操作（读大文件分块发送）不能阻塞消息泵。代价是响应帧序不再
   严格保证（文件分块与索引响应可能交错），协议侧靠 reqId 配对容忍。
10. **分片长度语义**：一个 upload 请求 = 一个分片（≤64KB），`length = size -
    offset` 夹取 chunkSize——对端若声明超大分片（>64KB）也只收 64KB，多余字节
    留到下一 data 帧？否——泵内按 `up.got >= up.size` 判定收齐，超发字节会计入
    下一个分片归属，协议靠「data 头声明 size」路由（conn.go 的 expect 机制）。
    这是帧协议「头+块原子连续」约束的直接体现（REFACTOR §4 约束 2）。

### 异常路径汇总（对端可触发的全部失败面）

| 对端行为 | 本节点响应 | 防线 |
|---|---|---|
| req 空/短/非 hex hash | `err "invalid hash"` | H1（IsStrictSHA256） |
| req 不存在的 hash | `err "not found"`（CAS 未命中） | 正常路径 |
| req 索引路径越权 | 回退 CAS；再未命中 → `err "not found"` | H2（IsPathAllowed） |
| create 根目录外路径 | `err "path outside allowed root"` | H2 |
| create 目录 | `err "<path> is a directory"` | 文件语义校验 |
| upload size<0 / >8GB | `err "invalid upload size"` | 上限（与拉取对称） |
| upload offset 未对齐 | `err "offset must be chunk-aligned"` | 位图粒度约束 |
| upload 超发（写越界） | `err "write out of range"` / `"upload write failed"` | WriteAt 边界校验 |
| upload 头后不发数据 | 30s 后槽位自动清空 | M6 |
| upload 单流重入 | `err "upload already in progress"` | 连接级单槽 |
| info/delete 非法 hash | `err "invalid hash"` | IsStrictSHA256 |
| list 超大 limit | 内部 clamp 1000 | 防全表物化 DoS |

所有失败统一 `err {msg, reqId}` 帧，对端 routeResponse 按 reqId 收错——错误面
可审计、不 panic、不悬挂（这是帧协议层「公共信令上任意节点可打帧」的安全底线）。

## 测试

覆盖入站角色的测试分散在 `peerjs_service_test.go`（serve* 直测）与
`file_index_test.go`（UploadSession 层，见 file-index.md）。共用 `fakeSession`
（内存版 Session：记录发送帧 + 手动注入对端帧）。

| 测试 | 发现背景 |
|---|---|
| `TestServeFile_InvalidHashNoPanic`（peerjs_service_test.go:130） | **H1 远程崩溃漏洞**：serveFile 直接 `req.Hash[:2]`，对端发 `{"type":"req","hash":""}` 或短 hash 即越界 panic 杀进程（公共信令上任意节点一行 JSON 打崩全节点）。修复：先 IsStrictSHA256 校验；断言每种坏 hash 恰好回一个 err 帧 |
| `TestServeFile_IndexPathOutsideRoot`（peerjs_service_test.go:145） | **H2 任意文件读取漏洞**：对端 create 任意绝对路径后 req 读取。防御性测试：模拟历史脏数据（根外路径已 upsert 进索引），serveFile 必须拒绝回传 |
| `TestServeUploadBegin_StalePendingCleared`（peerjs_service_test.go:245） | **M6 连接级 DoS**：对端发 upload 头后不发数据块，pendingUpload 永久占用，该连接后续所有 upload 全部 "already in progress"（重连才恢复）。修复：created + 30s 过期清空；断言过期占位被清且新上传正常开始 |
| `TestUploadWorker_WriteThenComplete`（peerjs_service_test.go:269） | **H5 头-of-line 阻塞**：二进制帧的 WriteAt/Complete 移出消息泵到连接级 worker。本测试直接驱动 worker 验证 chunk 投递 → 落盘 → uploaded 回帧全链路（坑：不能立刻 close(binDone)——select 随机选路会丢掉未处理 chunk，须先轮询回帧） |
| `TestFileIndex_UploadEmpty`（file_index_test.go:123） | **两端不对称**：拉取侧 hashMatchesSHA256 已支持空文件，但上传侧旧实现 size<=0 直接拒绝。修复：size==0 直接 Complete（serveUploadBegin 的直连分支） |
| `TestFileIndex_UploadMultiSource`（file_index_test.go:242） | **功能需求**：多节点并行上传同一文件不同分片——乱序 WriteAt 合并、位图全满触发完成（serveUploadBegin 的多 source 语义来源） |
| `TestFileIndex_UploadResume`（file_index_test.go:286） | **功能需求**：上传中断后从已接收位置继续（meta.offset 连续已写偏移的来源） |
| `TestFileIndex_UploadPartialNotComplete`（file_index_test.go:316） | **防御性测试**：缺分片时 Complete 不登记——serveUploadBegin 回 ack 让客户端续传的语义基础 |

## 文件清单

- `back/internal/transport/inbound.go`（330 行）——本模块
- `back/internal/transport/conn.go` —— 分派（bindConn）、connState 槽位、泵内二进制路由
- `back/internal/transport/peerjs_service_test.go` —— serve* 直测 + fakeSession 基础设施
- `back/internal/transport/stream_test.go` —— 流式拉取测试（出站角色，但用同一 fakeSession）
- `back/internal/transport/file_index_test.go` —— UploadSession 层测试
- `doc/REFACTOR.md` §3.7（角色拆分）、§4（帧协议）、§5（E2E 坑）
- `doc/TRANSPORT-REVIEW-2026-08-15.md` —— H1/H2/H5/H6/M6/M7 全部原始发现与修复记录