# 文件索引持久化（file_index.go）

> 一句话职责：本地文件索引服务——sha256 → 绝对路径 映射（SQLite 持久化），
> 支撑帧协议的 create/upload/list/info/delete/sync 六 verb 与增量同步
> （seq 游标 + tombstone）。

## 职责

`FileIndexService` 是帧协议层的数据面组件（归属 LAYERS ⑤ 数据切面，被 ② 帧
协议层持有使用）：维护「内容哈希 → 本地绝对路径」映射，是 `req` 拉取的路径
解析来源（download 优先查索引，其次内容寻址存储）。

三块职责：

1. **登记与查询**：`Create`（外部文件登记，不复制）、`List`、`Info`、
   `DownloadPath`、`Delete`（逻辑删除）。
2. **分片上传会话**：`UploadSession`——64KB chunk 位图跟踪、多 source 并发
   分片、断点续传、`Complete`（fsync + 全文件 sha256 + 登记）、`Abort`。
3. **增量同步**：`SyncSince`（seq 游标后的变更集，含 tombstone）、`ApplySync`
   （对端变更合并到本地）。

SQLite 持久化在 `internal/repository`（`UpsertFileIndex` / `ListFileIndex` /
`GetFileIndex` / `DeleteFileIndex` / `ListFileIndexSince`），本文件不直接碰 SQL。

## 关键机制

### 安全根目录（IsPathAllowed，file_index.go:58）

create 登记的允许根目录 = uploadDir（构造时 `filepath.Abs` + `EvalSymlinks`
解析）。判定：绝对路径 + 符号链接解析后 `filepath.Rel(rootDir, abs)` 不以
`..` 开头且非 `..`。

- **为什么 EvalSymlinks**：防「根目录内软链 → 根外目标」绕过（H2）。
- 根目录外 create 直接拒绝；serveFile 回传索引路径前也过同一判定。

### 分片上传会话（UploadSession，file_index.go:153）

```
BeginUpload(name, size) → 会话（同 name 幂等复用；size 必须一致，M7）
  - size 上限 8GB（防恶意声明）
  - 目标文件：uploadDir/sanitizeName(name)（防路径穿越）
  - 位图：[]uint64，每 word 64 chunk——按 (totalChunks+63)/64 分配
    （坑：曾按 chunk 数分配导致末 word 判满逻辑失效）
  - 断点续传：已有文件大小 → 重建位图 [0, min(size, fsize)) 视为已写
    （坑：空洞/错序可能误标已写，最终 Complete 的 sha256 校验兜底）

WriteAt(offset, data) → 落盘 + 按字节区间置位（一次写可能跨 chunk 边界）
  - offset 越界（offset<0 或 offset+len > size）拒绝
  - aborted / 句柄被摘 → 明确报错（M7 竞态修复）

ContiguousOffset() → 第一个未到位 chunk 的起始字节（resume 起点）

Complete() → 位图全满判定：
  - 全满 → file.Sync() → hashFile(全文件 sha256) → UpsertFileIndex → 返回 FileInfo
  - 未满 → (false, nil, nil)（不登记）
  - 位图全满判定坑：除最后一个 word 外需全 64 位；末 word 只需
    (总 chunk 数 mod 64) 位——曾误判尾部块未满导致永不完成

Abort() → 幂等中止 + 删除目标文件（reap 摘除句柄后调用无副作用）
Close()  → 关句柄不删文件（保留续传）
```

- **多 source 语义**：多个连接/节点并发 `WriteAt` 不同分片，位图合并，全满
  即完成（最后一片的请求方收到 uploaded）。
- **会话键 = sanitize 后的文件名**，不是 hash——分片上传目标由 name 决定，
  hash 由完成时的内容计算。

### 会话回收（reapUploads，file_index.go:74）

`NewFileIndexService` 启动 goroutine，每 5 分钟扫 `s.uploads`：10 分钟无活动 →
置 `aborted` + 摘除句柄 + 关闭 + 删文件（防磁盘耗尽）。M7 修复：持 `sess.mu`
判定 + 标记 + 摘除与 `WriteAt`/`Complete` 串行——之前先解锁再 Abort 存在检查
窗口（判定 idle 后、Abort 前并发分片拿到已关闭句柄 → 上传莫名失败）。

### 增量同步（SyncSince / ApplySync，file_index.go:396,416）

```
SyncSince(since) → rows = ListFileIndexSince(since) → []FileInfo + lastSeq
  - FileInfo.Delete 标记 tombstone（来自 repository 的 deleted 字段）
  - lastSeq = max(所有 row.Seq, since)
ApplySync(files) → 逐条：Delete=true → DeleteFileIndex（tombstone）；
  Path 非空 → UpsertFileIndex；非法 hash 跳过（防御对端脏数据）
```

同步模型：`file_index.seq` 单调游标（repository 层生成），`sync{seq}` 取增量
变更（含 tombstone），对端 `ApplySync` 合并——**delete 的 tombstone 必须带序**
（L6：原实现丢弃 DeleteFileIndex 的 seq，对端 sync 跟踪不到删除事件）。

### 其它

- **List 的 LIMIT 防御**（file_index.go:344）：limit 来自远端 list verb（可任意
  大），直接进 SQL LIMIT 会全表物化 → 内存 DoS。clamp 到 [1, 1000]（repository
  层只兜 limit<=0）。
- **Info/Delete 的 hash 校验**：`IsStrictSHA256` 前置，防脏 hash 进 SQL。
- **sanitizeName**（file_index.go:453）：`filepath.Base` + 反斜杠归一，空名兜底
  "upload.bin"——防路径穿越（`../../x` → `x`）。
- **hashFile**：全文件流式 sha256（大文件不驻留内存）。

## 与其它模块的关系

| 模块 | 关系 |
|---|---|
| `inbound.go`（入站角色） | 六 verb 服务端只做校验/分派/回帧，真实状态全部在本文件（UploadSession 位图、续传、reap） |
| `outbound.go`（出站角色） | 拉取侧不直接碰索引；serveFile 的路径解析在入站侧（索引优先 + IsPathAllowed + CAS 兜底） |
| `internal/repository` | SQLite 持久化：UpsertFileIndex/ListFileIndex/GetFileIndex/DeleteFileIndex/ListFileIndexSince（file_index 表 + seq 游标） |
| `peerjs_service.go` | `PeerJSService.fileIndex` 字段持有本服务（NewPeerJSService 装配） |
| `admin.go` | admin 管理面上传**不**经 UploadSession（走临时文件收集 + multipart 重包），二者独立 |
| `internal/source/local.go` | LocalSource.resolvePath 复刻 serveFile 的索引优先 + IsPathAllowed + CAS 兜底逻辑（REFACTOR §3.8） |
| `cmd/server/main.go` | `NewFileIndexService(uploadDir)` 装配，storageDir 与上传目录分离 |

## 坑与设计决策

1. **H2 任意文件读取**（IsPathAllowed/Create）：旧实现 create 接受任意绝对路径，
   对端可 `create /etc/shadow` 拿 hash 后 `req` 读取。修复：根目录锚定 +
   EvalSymlinks 解析 + `filepath.Rel` 前缀判定；符号链接逃逸（根内软链 → 根外
   目标）必须拒绝（TestFileIndex_CreateSymlinkEscape）。
2. **位图分配粒度**（BeginUpload，file_index.go:196）：`[]uint64` 每 word 64
   chunk——曾按 chunk 数分配导致末 word 判满逻辑失效（边界 `bits < 64` 的
   `(1<<bits)-1` 遮罩是判满正确性的关键）。
3. **末 word 判满**（Complete，file_index.go:274-290）：除末 word 全 64 位，末
   word 只要求实际 chunk 数——曾误判尾部块未满导致永不完成。
4. **M7 竞态**（reapUploads + aborted）：reap 判定 idle 后解锁再 Abort 存在检查
   窗口（并发 WriteAt 拿到已关闭句柄 → 上传莫名失败）。修复：持 sess.mu 完成
   判定 + 标记 + 摘除；WriteAt/Complete 见 `aborted` 明确报错。
5. **M7 同名会话 size 不一致**（BeginUpload，file_index.go:183）：位图按旧 size
   建，声明不一致会导致续传偏移错乱、末 chunk 判满错误。修复：同名复用必须
   size 一致，否则拒绝。
6. **断点续传的不精确性**（file_index.go:206-219）：进程重启后按文件大小近似
   重建位图（空洞/错序可能误标已写）——由最终 Complete 的 sha256 校验兜底
   （内容不匹配则整体失败，不会登记错误映射）。
7. **空文件**（size==0）：位图天然全满（totalChunks=0，循环不执行，full 保持
   true）——sha256(空) 是合法内容寻址值，上传侧必须能完成。
8. **tombstone 带序**（Delete 返回 seq，L6）：增量同步的删除事件必须可被对端
   游标跟踪，否则对端永久残留已删文件的映射。
9. **list LIMIT 1000 上限**：远端可传任意 limit——防御全表物化内存 DoS，同时
   给分页语义留口（offset 游标）。

## 测试

全部在 `file_index_test.go`（13 个测试），`initTestDB` 用内存 SQLite（file_index
表随 InitDB 建）。

| 测试 | 发现背景 |
|---|---|
| `TestFileIndex_CreateAndInfo`（:26） | **功能测试**：create 只索引绝对路径、不复制文件；根目录外登记必须被拒（H2）；非法 hash 拒绝 |
| `TestFileIndex_CreateSymlinkEscape`（:60） | **H2 防御性测试**：IsPathAllowed 必须 EvalSymlinks 解析后再判，否则「根内软链 → 根外目标」绕过限制 |
| `TestFileIndex_IsPathAllowed`（:74） | 根目录判定：目录内允许、根外拒绝 |
| `TestFileIndex_UploadStream`（:86） | **功能测试**：WriteAt 分片（64KB 块）+ 位图全满判定 + sha256 登记 + 内容可读 |
| `TestFileIndex_UploadEmpty`（:123） | **两端不对称**：拉取侧 hashMatchesSHA256 已支持空文件，上传侧旧实现 size<=0 直接拒——sha256(空) 合法，size=0 位图应天然全满 |
| `TestFileIndex_UploadSizeMismatch`（:142） | **防御性测试**：size 是协议信任边界——超上限拒绝（9GB）、越界写拒绝（声明 100 写 200） |
| `TestFileIndex_ListAndDelete`（:161） | **功能测试**：列表 + 逻辑删除（tombstone）→ 列表不再出现；delete 返回新 seq（L6） |
| `TestFileIndex_SyncSince`（:189） | **功能测试**：metadata 同步依赖单调 seq 游标；ApplySync 幂等合并；删除 tombstone 同步生效（路径原样同步） |
| `TestFileIndex_UploadMultiSource`（:242） | **功能需求**：多节点并行上传同一文件不同分片——4 source 乱序并发 WriteAt、位图合并、最后一个分片触发完成 |
| `TestFileIndex_UploadResume`（:286） | **功能需求**：中断后重开会话，ContiguousOffset 连续已写偏移正确，从续传点补齐完成 |
| `TestFileIndex_UploadPartialNotComplete`（:316） | **防御性测试**：缺分片时 Complete 返回未完成，不得登记映射 |
| `TestFileIndex_BeginUploadSizeMismatch`（:334） | **M7 防御性测试**：同名会话声明 size 不一致必须拒绝（位图按旧 size 建 → 续传偏移错乱）；一致则可复用 |
| `TestFileIndex_AbortIdempotent`（:349） | **M7 防御性测试**：Abort 与 reap 摘除句柄可能竞争，必须幂等；中止后写入必须明确失败（而非写已关闭句柄的莫名失败） |

## 文件清单

- `back/internal/transport/file_index.go`（462 行）——本模块
- `back/internal/transport/file_index_test.go`（366 行）——13 个测试
- `back/internal/repository/file_index_repo.go`（或 db.go 内对应实现）——SQLite 持久化 + seq 游标
- `back/internal/transport/inbound.go` —— verb 服务端（线上面）
- `back/internal/transport/conn.go` —— connState.pendingUpload 槽位
- `doc/REFACTOR.md` §4（文件索引 verb 定义）、§3.8（LocalSource 复刻逻辑）
- `doc/TRANSPORT-REVIEW-2026-08-15.md` —— H2/M7 原始发现与修复记录
- `doc/TRANSPORT-REVIEW2-2026-08-16.md` —— L6（delete 缺 seq）原始发现与修复记录