# source 层（back/internal/source/）

> 层归属：AOP ④ 业务核心（见 doc/LAYERS.md §1）——统一文件获取抽象（provider 概念落地，
> REFACTOR.md §3.8）。
> 核心思想（source.go:1-18 头注释）：**任何能提供「内容寻址字节流」的东西都是 Source**
> ——本地磁盘、p2p 对端（透传）、URL/HTTP 模板。上层只问「给我 hash 的内容」，不关心
> 来源与网络路径。

**一句话职责**：把多后端（本地 / peer 透传 / URL 模板）文件获取抽象为 `Source` 接口 +
`SourceManager` 统一路由（优先级/能力/统计/运行时调整）；大文件只走流式（CapStream），
全量获取走 OpenAny 降级。

## 职责

### 解决什么问题

重构前 service 里「本地查找」逻辑复制了 6 遍（REFACTOR.md §7 的 provider 层设计动机）。
且节点互联（PeerJS）成熟后出现新需求：**本节点没有的文件可以从在线对端拉**，再不行
从 URL 模板拉——这就是「多源回退」。source 层把这三条路径收敛成：

- **统一接口**：`Source`（Name/Type/Capabilities/Priority/SetPriority/Available/Open/Fetch/Info）
- **统一路由**：`SourceManager`（优先级升序逐源尝试、能力路由、命中统计、全失败汇总错误）
- **统一管理面**：`Snapshot()` → `GET /sources`（状态+统计+优先级运行时调整）

### 在 AOP ④ 中的位置

```
controller（/sources 管理端点走 router/source_routes.go，不经 controller）
router（source_routes.go：GET /sources、POST /sources/:name/priority）
  ↑
source（本层）
  ├→ transport.FileIndexService（LocalSource 的索引映射 + IsPathAllowed）
  ├→ transport.PeerJSService（PeerSource 的连接枚举 + OpenStream）
  └→ pkg/hashutil（IsStrictSHA256）
```

装配在 `cmd/server/main.go`（source.go:17 注释：transport 不反向依赖本包）。

## 模块清单

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| source.go | 接口/类型定义：Source、Capability、FileMeta、Stats、SourceStatus | `Source` 接口、`Capability`（`CapFile=1`/`CapStream=2`）、`IsStream`/`IsFile`、`validHash` |
| manager.go | SourceManager：注册/注销/优先级/统一获取入口/统计快照 | `Manager`：`New`、`Register`、`Unregister`、`SetPriority`、`Open`、`OpenRange`、`OpenAny`、`Info`、`Snapshot` |
| local.go | 本地磁盘源：file_index 映射优先 + CAS 兜底，CapStream | `LocalSource`：`NewLocalSource`、`Open`、`Fetch`、`Info`、`Available`、`resolvePath` |
| peer.go | p2p 透传源：枚举在线对端串行尝试，per-peer 单槽 | `PeerSource`：`NewPeerSource`、`Open`、`Available`、`Fetch`；`peerReadCloser` |
| url.go | URL 模板源：%s/%d 模板 + Range 分片 + sha256 校验 | `URLSource`：`NewURLSource`、`Open`、`Fetch`、`buildURL`；`verifyReadCloser` |

## 关键机制

### 1. 能力标记（source.go:29-39）

```go
const (
    CapFile   Capability = 1 << iota  // 整体获取（Fetch → []byte）
    CapStream                         // 流式/分片（Open(ctx, hash, offset, size)）
)
```

能力决定路由方式（manager.go:11-13 头注释）：

- **大文件必须走 CapStream**——8GB 全量 buffer 会 OOM（transport 流式改造的教训，source.go:9）
- `OpenRange` **只尝试 CapStream 源**：CapFile 源无分片能力，降级 = 全量 buffer 路径
- `OpenAny` 允许降级 CapFile 整体拉取（小文件/元数据场景）

各源能力声明：

| 源 | 能力 | 理由 |
|---|---|---|
| local | CapStream | os.File Seek/ReadAt 天然分片 |
| peer | CapStream | req 帧协议支持 offset/size range |
| url | 模板含 `%d` → CapStream；否则 CapFile | Range 参数在模板里才可流式（url.go:52-57） |

### 2. 路由语义（manager.go:9-14）

```
OpenRange(ctx, hash, offset, size):
  1. validHash（IsStrictSHA256，统一防御——所有 source 入口）
  2. 快照 sources（RLock 拷贝，路由时不持锁——Available/Open 是慢操作）
  3. 按优先级升序：Available()==false 跳过（记录 "unavailable"）；
     IsStream 源依次 Open，第一个成功返回
  4. 全失败 → "all sources failed: <最后一个源的错误>"（lastErr 只保留最后一条，
     每源详细失败原因在 Stats.LastErr）
```

装配优先级（cmd/server/main.go）：`local → peer → url(可选, PEERDRIVE_URL_SOURCE_TEMPLATE)`。
「本地命中即返回」是内容寻址的本地权威语义；未命中降级 peer；URL 最后兜底。

### 3. 统计与管理面（manager.go:205-248）

每次尝试（含 unavailable 跳过）都 `record`：

```
Stats{ Success, Fail, Bytes, LastErr, LastAt }
```

`Snapshot()` 输出 `SourceStatus`（Name/Type/Priority/Capabilities/Stream/Available/Stats）
→ `GET /sources`；`POST /sources/:name/priority` 运行时调优先级（SetPriority 后重排，
sort.SliceStable）。这是「运行时调整路由」的管理能力——故障源可临时降权，不重启。

### 4. LocalSource：file_index 优先 + CAS 兜底（local.go:69-82）

```go
resolvePath(hash):
  CAS: storageDir/{hash[:2]}/{hash}
  若 fileIndex.Info(hash) 命中且 IsPathAllowed(路径在允许根内) → 返回索引路径
  否则 → 回退 CAS（历史脏数据/恶意登记不回传根外文件，H2 语义）
```

与 `transport.serveFile` 的路径决策完全一致（同一逻辑收敛到一处，local.go:3-8 注释）。
本地文件写入时已完成 sha256 校验（upload Complete），`Open` 不再校验（与 serveFile 行为
一致）；`Available` = 存储目录可读。

### 5. PeerSource：per-peer 单槽串行（peer.go:5-15 注释）

```
Open:
  枚举 PeerJSService.Connections()（排除自身 ID）
  每个 peer：peerLocks.LoadOrStore 拿 *sync.Mutex → TryLock()
    - 拿不到 = 该 peer 已有流在进行 → 跳过试下一个（不等待！大文件流会阻塞整个路由）
    - 拿到 → svc.OpenStream(pid, hash, offset, size)，成功返回包装 reader
      （Close 时解锁释放槽位）；失败解锁继续
```

- **单槽约束来源**：连接级 expect 状态机（transport/conn.go bindConn）——同一连接并发
  两个 fetch 流数据会交错。TryLock 而非 Lock 是「忙则跳过」语义。
- 当前**串行尝试**，未来可升级多 peer 并发竞速（不同连接并发安全，同一连接仍需互斥，
  peer.go:13-14 注释）。
- `Info` 不支持（对端 info verb 未在拉取侧实现，peer.go:114-117）。

### 6. URLSource：模板 + Range + 内容寻址兜底（url.go）

- **模板**：`fmt.Sprintf`，`%s`=hash；含 `%d`（两次）= offset,size → 声明 CapStream
  （url.go:40-58）。例：`https://example.com/f/%s?off=%d&size=%d`
- **Range 语义**（Open）：`bytes=start-end`（size<0 到文件尾）；服务器 206 → 直用 body；
  回 200 全量 → `io.CopyN` 丢弃 offset 段 + `LimitReader` 截 size 段（带宽浪费但正确，
  url.go:121-134 注释）；416/4xx → 报错
- **sha256 校验**（verifyReadCloser，url.go:177-214）：**全量请求（offset==0 && size<0）
  读取时校验**——URL 源内容可能被篡改，校验是内容寻址语义的底线；EOF 时比对，不匹配
  返回 hash mismatch（ReadAll 会拿到）。Fetch 同样校验（url.go:164-168）
- `Available` 恒 true（ping 浪费请求，url.go:79-81 注释）；失败由路由统计暴露（LastErr）
- 可注入 http.Client 的 Transport 指向 ech-proxy 等出口（wintools cmd/ech-proxy），
  不建独立 source 类型（url.go:5-8 注释）

## 与其它模块的关系

```
router/source_routes.go（管理端点：/sources 快照 + 优先级调整）
  ↑ Snapshot/SetPriority
source（本层）
  ├→ transport.FileIndexService（LocalSource 索引；Info/IsPathAllowed）
  ├→ transport.PeerJSService（PeerSource 连接枚举；Connections/OpenStream/ID）
  └→ pkg/hashutil（IsStrictSHA256 统一 hash 防御）
cmd/server/main.go（装配：local → peer → url）
```

- **边界**（REFACTOR.md §3.8）：`serveFile` 保持本地语义**不接 manager**（避免入站→出站
  透传递归环）；`/peerjs/fetch` 仍直调 FetchFromPeer（保持语义）。
- **消费方**：目前主要是 transport 层文件索引的 `LocalSource` 装配；未来 downloader/
  controller 的取数路径可切 manager（预留，未切）。

## 坑与设计决策

1. **OpenRange 拒绝 CapFile 源**（manager.go:102-103）：CapFile 源无分片能力，OpenRange
   降级会走全量 buffer（OOM 路径）；需要整体获取的调用方显式用 OpenAny——API 语义强制
   调用方声明内存预算。
2. **全失败只返回最后一条错误**（manager.go:126-132）：`all sources failed: <lastErr>`，
   每源详细原因在 Stats.LastErr（管理面排查用）——错误体不膨胀但信息可查。
3. **路由期间不持锁**（manager.go:108-110）：snapshot 拷贝后释放 RLock——Available/Open
   是网络/磁盘慢操作，持锁会让 SetPriority/Register 全部阻塞。
4. **per-peer TryLock 而非全局锁**：同 peer 单槽是帧协议硬约束；不同 peer 的流天然并发
   安全（不同连接）——锁粒度精确到 peer，避免一个慢 peer 阻塞全部路由。
5. **URLSource 的 200 全量截取**：服务器不支持 Range 时正确性优先（带宽浪费可接受），
   第一版不做 HEAD 探测（url.go:122-124 注释）。
6. **verifyReadCloser 只在 EOF 校验**：中途 Close 提前取消不校验（与 transport 的
   fetchReader 语义一致，H5 兜底在 transport 侧）。
7. **Available 的软状态语义**：local=目录可读、peer=有在线连接（排除自身）、url=恒 true
   ——都是「可能可用」而非「一定有该文件」，真实命中由 Open 决定（source.go:63-64 注释）。
8. **重名注册拒绝**（manager.go:40-52）：Name 是注册表 key，重复注册返回错误——防装配
   时误注册同名源静默覆盖。

## 测试

| 文件 | 测试 | 发现背景 |
|---|---|---|
| source_test.go | `TestLocalSource_OpenCAS` | source 体系新功能（本地 CAS 分片/全量读取 + 非法 hash 拒绝 + 不存在报错）——无前置 bug，防御性 |
| source_test.go | `TestLocalSource_IndexPriority` | file_index 映射优先于 CAS（同一 hash 两处存在时读索引路径）；测试用 `repository.InitDB(":memory:")` + 真实 `transport.NewFileIndexService`（依赖 SQLite 持久化） |
| source_test.go | `TestManager_RoutePriority` | 路由语义回归：local 命中即返回（peer 不被调）、未命中降级链、OpenRange 跳过 CapFile 源、OpenAny 兜底、SetPriority 运行时调整、Snapshot 统计、重名拒绝——用可编程 stubSource 验证，覆盖 5 个路由分支 |

> 注：本层测试均标注了「source 体系」发现背景（source_test.go:3-5 头注释：路由优先级/
> 能力标记/统计管理/错误降级链），为新代码的防御性测试。

## 文件清单

```
back/internal/source/
├── source.go         Source 接口 + Capability + FileMeta/Stats/SourceStatus + validHash
├── manager.go        SourceManager（注册/路由/统计/快照）
├── local.go          LocalSource（file_index 优先 + CAS 兜底，CapStream）
├── peer.go           PeerSource（连接枚举 + per-peer 单槽串行）
├── url.go            URLSource（模板 + Range + sha256 校验）
└── source_test.go    Local/Manager 测试（stub source 驱动 5 个路由分支）
```