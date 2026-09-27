# 模块 07：source 文件来源管理

- **代码位置**：`back/internal/source`
- **功能一句话**：统一「内容寻址字节流」获取抽象——本地磁盘（file_index 映射 + 内容寻址存储兜底）、p2p 对端（透传竞速）、URL/HTTP 模板三类来源注册进 `Manager` 注册表，按优先级逐源路由读取；另提供按能力拆分的控制面（local 添加/写文件、BT torrent/magnet 下载、IPFS pin/网关状态），并承担 transport 的 `FileRouter` 接口供对端 req 多源回源。
- **依赖**：`internal/transport`（`FileIndexService`、`PeerJSService`，方向为 source→transport，transport 不反向依赖本包，见 `back/internal/source/source.go:16-17`）；`internal/provider`（IPFSProvider，`back/internal/source/ipfs_control.go:25`）；`internal/repository`（InsertPin/InsertFileMeta/InsertFileProvider 等，`back/internal/source/ipfs_control.go:56-70`）；`github.com/Hana-ame/go-peerdrive-bt`（BTClient，`back/internal/source/bt_control.go:7-13`）；`internal/pathutil`（SafeOpen，`back/internal/source/local.go:104`）；`internal/log`、`internal/model`、`pkg/hashutil`。
- **被依赖**：`cmd/server/main.go`（装配：Register 三类 source + SetSourceManager + SetFileRouter，`back/cmd/server/main.go:214-233`）；`internal/router`（`/sources` 管理端点 + 注入 BT/IPFS 控制面，`back/internal/router/source_routes.go`、`back/internal/router/router.go:150-196`）；`internal/transport`（经 `FileRouter` 接口使用 Manager，`back/internal/transport/inbound.go:42-51`、`back/internal/transport/peerjs_service.go:535-537`）。

## 1. 逻辑

### 1.1 核心抽象：Source 接口与能力标记
统一文件获取抽象（`back/internal/source/source.go:1-18`）：任何能提供「内容寻址字节流」的东西都是 Source——本地磁盘、p2p 对端（透传）、URL/HTTP（可经 ech-proxy 等出口）、IPFS 网关。上层只问「给我 hash 的内容」，不关心来源与网络路径。

- `Source` 接口（`back/internal/source/source.go:51-72`）：`Name()`（注册表 key）/`Type()`（local/peer/url/ipfs 分类）/`Capabilities()`/`Priority()`/`SetPriority()`/`Available()`（软健康检查，false 时路由跳过）/`Open(ctx, hash, offset, size)`（流式，需 CapStream）/`Fetch(ctx, hash)`（整体，需 CapFile）/`Info(ctx, hash)`（可选元数据；不支持返回 `nil, nil`）。
- `Capability` 位标记（`back/internal/source/source.go:30-39`）：`CapFile`（整体获取）与 `CapStream`（流式/分片读取）可组合。大文件必须走 CapStream——8GB 全量 buffer 会 OOM（同文件注释，`source.go:8-10`，另见 `manager.go:150-152`）。
- 工具函数 `IsStream`/`IsFile`（`source.go:75-78`）、`FileMeta`（`Hash/Size/Name/Path`，`Path` 仅 local 有意义，`source.go:42-48`）、统一入口防御 `validHash`（必须 64 位小写 hex，`source.go:100-106`、`back/pkg/hashutil/hashutil.go:60-72`）。
- 统一统计 `Stats`（Success/Fail/Bytes/LastErr/LastAt，`source.go:81-87`）与管理快照 `SourceStatus`（JSON 化，`source.go:90-98`）。

### 1.2 SourceManager 注册表与路由
`Manager`（`back/internal/source/manager.go:27-39`）持有：

- `sources []Source`：按优先级升序排列（注册、改优先级时 `sortLocked` 重排，`manager.go:55-58,118-136`）；
- `stats map[string]*Stats`：按 source 名记累计统计；
- `btControl IPFSControl / btControl BTControl`：可选控制面实例（nil 表示未启用），**按实例持有**——历史上曾误用包级全局 var 导致多实例共享，改为字段后各 Manager 独立（`manager.go:33-39` 注释 + `source_test.go:410-427` 回归测试）。

注册表操作：`Register`（重名拒绝，`manager.go:47-59`）、`Unregister`（`manager.go:62-73`）、`Get`（`manager.go:106-115`）、`SetPriority`（运行时调整并重排，`manager.go:118-129`）。

统一文件获取入口（路由语义，`manager.go:9-14`）：

- `Open/OpenRange`：按优先级升序逐源尝试；`Available()==false` 跳过并记录（`manager.go:166-168,196-198`）；**只尝试 CapStream 源**，CapFile 源无分片能力会被跳过（`manager.go:153-182`）；全失败返回汇总错误（含每源失败原因，`manager.go:181`）。
- `OpenAny`：优先 CapStream 流式，全部失败时降级 CapFile 整体拉取（内存驻留，适合小文件/元数据，`manager.go:186-224`）。
- `Info`：按优先级尝试支持 Info 的 source，第一个命中返回（`manager.go:227-252`）。
- `InfoSize`：把 `FileMeta` 收敛为标量 size 的 transport 适配（`manager.go:254-263`，避免 import 环问题）。
- `Snapshot`：每个 source 的状态 + 统计，`GET /sources` 的数据源（`manager.go:266-287`）。
- `record`：每次尝试落统计（成功/失败/字节/时间，`manager.go:290-309`）。

### 1.3 四个来源实现

**LocalSource（`back/internal/source/local.go`）**——本地磁盘源，语义与 transport.serveFile 的路径决策完全一致（同一逻辑收敛到一处，`local.go:3-8`）：
- 路径决策 `resolvePath`（`local.go:76-87`）：file_index 命中且路径可读 → 读映射路径；否则 → 内容寻址存储 `storageDir/<hash[:2]>/<hash>`。
- 打开走 `pathutil.SafeOpen`（os.Root 锚定，防登记后路径被换成软链的 TOCTOU，`local.go:90-105`）。
- `Open` 流式分片：offset<0→0、越界 clamp、`io.LimitReader` 限长（`local.go:109-140`）；本地文件写入时已完成 sha256 校验（upload Complete），Open 不再校验（`local.go:7-8`）。
- `Info`：优先 file_index（有 name/path），否则 CAS 文件 stat（`local.go:153-172`）。
- 控制面（见 1.5）`AddLocalFile`/`WriteFile`（`local.go:174-200`）。

**URLSource（`back/internal/source/url.go`）**——HTTP URL 源，按模板拉取：
- 模板 `fmt.Sprintf`：`%s`=hash，含 `%d`（依次 offset,size）时声明 CapStream，否则 CapFile（`url.go:30-59,85-90`；配置见 `back/internal/config/config.go:87-91`）。
- `Open`：优先带 Range 头请求（服务器 206 → 流式分片）；服务器回 200 全量时丢弃前 offset 字节再限长截取（正确性优先、带宽浪费可接受，`url.go:93-140`）。
- **内容寻址兜底**：全量请求（offset==0 && size<0）读取完成时校验 sha256（`verifyReadCloser`，`url.go:135-138,180-214`）；`Fetch` 同样校验（`url.go:164-169`）——URL 源内容可能被篡改，校验是底线。
- `Available` 恒 true（无主动健康检查，失败由 LastErr 暴露，`url.go:79-81`）；`Info` 不支持（HTTP HEAD 留给未来，`url.go:172-175`）。

**PeerSource（`back/internal/source/peer.go`）**——p2p 透传源，经 PeerJS/WebRTC 从在线对端拉取：
- 枚举在线对端（`svc.Connections()`，排除自身，`peer.go:61-70`）；单对端走串行路径（`peer.go:92-100`），多对端并发竞速 `raceOpen`（`peer.go:136-178`）——首个流建立成功立即返回，输家流由后台收割 goroutine Close（防 peer 流互斥锁泄漏 + fetch 状态悬挂）。
- **连接级 expect 单槽**：同一对端同时只允许一个流（帧协议约束），用 `peerLocks sync.Map`（peerID→\*sync.Mutex）按对端隔离，`TryLock` 失败即跳过忙对端不等待（`peer.go:8-10,35,104-123`）。锁释放责任：胜者 reader Close / 失败路径立即 Unlock / 竞速收割者 Close（`peer.go:139-178`）。
- `peerReadCloser.Close` 用 `sync.Once` 保证幂等（防 defer + 显式 Close 双解锁 panic，`peer.go:195-215`）。
- 全量请求的 sha256 校验由 transport 的 fetchReader 完成（`peer.go:11`）；`TraceKey` 透传防 A←→B 回源死循环（`peer.go:77-79`，传输侧见 `back/internal/transport/inbound.go:78-89`）；`Info` 不支持（对端 info verb 未在拉取侧实现，`peer.go:190-193`）。

**IPFS / BT**：**不注册为读取型 source**，仅作为控制面注入 Manager（见 1.5）。概念上 IPFS 网关可作来源（`source.go:3-5`），实际读取经 URL 模板（如 `https://gateway/ipfs/<hash>`）或 provider 直接 fetch（`url.go:4-7`、`ipfs_control.go:40`）。实际装配中 Manager 注册表只有 local/peer/url 三个（`back/cmd/server/main.go:217-228`）。

### 1.4 装配与生命周期（进程级）
`cmd/server/main.go`（`back/cmd/server/main.go:214-233`）：
1. `source.New()` 建空 Manager；
2. `Register(NewLocalSource(storageDir, peerjsSvc.FileIndex()))`——storageDir=内容寻址存储根（`cfg.StorageDir`），fileIndex 复用 transport 同一份索引（写根=`cfg.DownloadDir`，见 `back/internal/transport/peerjs_service.go:113-127,531-533`）；
3. `Register(NewPeerSource(peerjsSvc))`；
4. `cfg.URLSourceTemplate != ""` 时 `Register(NewURLSource(cfg.URLSourceTemplate, nil))`；
5. `router.SetSourceManager(mgr)` 注入管理端点；`peerjsSvc.SetFileRouter(mgr)` 装配 transport 回源路由。
控制面注入在 `back/internal/router/router.go:150-196`：`SetBTControl(NewBTControl(btClient))`、`SetIPFSControl(NewIPFSControl(ipfsProv, cfg.StorageDir))`。

生命周期：Source/Manager 均为**纯进程内存态**，无持久化、无定时任务、无 Close/优雅停机钩子（优雅停机只关 HTTP server/PeerJS 服务/数据库句柄，`back/cmd/server/main.go:243-254` 注释）。进程重启后注册表、优先级、统计、peer 锁全部重建。

### 1.5 控制面（按能力拆分，非每 source 必实现）
`back/internal/source/control.go`：读取面只回答「给我 hash 的字节流」，控制面回答「如何把文件加入/写入这个 source」（`control.go:3-8`，设计背景见 `doc/source-control.md`、`control.go:7`）。

- `LocalControl`（`control.go:19-25`）：`AddLocalFile(path)`（计算 hash、登记索引）/`WriteFile(name, r)`（从 reader 写入后登记）；经 `LocalControlOf(s)` 类型断言取用（`control.go:28-31`）；LocalSource 在 `fileIndex==nil` 时返回 `ErrControlUnsupported`（`local.go:177-178,191-192`，测试 `source_test.go:238-244`）。
- `BTControl`（`control.go:34-55`）：DownloadTorrent/DownloadMagnet/ListDownloads/GetDownload/PauseDownload/ResumeDownload/RemoveDownload；`TorrentMeta`、`DownloadStatus`（`control.go:57-77`）。
- `IPFSControl`（`control.go:80-92`）：PinCID/UnpinCID/ListPins/GatewayStatus；`PinInfo`、`GatewayStatus`（`control.go:94-108`）。
- `ErrControlUnsupported`：source 未实现某控制能力/未注入底层（`control.go:16`）。每个控制面方法入口都判 nil（`bt_control.go:22-24,39-41,56-58...`；`ipfs_control.go:35-37,76-78,98-100,119-121`），nil 客户端/提供者全部降级为错误或 nil（测试 `source_test.go:334-376`）。

控制面实现（`ipfs_control.go`：包装 `provider.IPFSProvider` + repository + 磁盘缓存；`bt_control.go`：包装 `p2p_bt.BTClient`）与 HTTP 端点（`back/internal/router/source_routes.go`）见第 3、4 节。

## 2. 如何储存

**核心结论：source 模块自身不持久化任何东西——全部是进程内存态（注册表、统计、控制面引用、peer 锁）与连接态（peer 流）**；持久化全部委托给下级模块（transport 的 file_index → repository SQLite / 磁盘文件；provider + repository 的 pin 登记）。逐项说明：

### 2.1 模块自身内存态（不持久化）
| 结构 | 位置 | 内容 | 生命周期 |
|---|---|---|---|
| `Manager.sources []Source` | `manager.go:30` | 已注册 source 切片，按优先级升序 | 进程启动 Register 建立；`Register/Unregister/SetPriority` 变更；进程重启后需重新 Register |
| `Manager.stats map[string]*Stats` | `manager.go:31` | 每 source 累计统计（Success/Fail/Bytes/LastErr/LastAt） | 每次路由尝试经 `record` 更新；不落盘，重启清零 |
| `Manager.btControl/ipfsControl` | `manager.go:37-38` | 控制面接口实例或 nil（按实例持有） | main/router 启动时 `Set*Control` 注入；重启后重新注入 |
| `PeerSource.peerLocks sync.Map` | `peer.go:35` | peerID→\*sync.Mutex，连接级单槽互斥 | 随流建立/关闭动态增删；重启清空 |
| `PeerSource.svc`（连接清单在 transport） | `peer.go:27` | 在线对端连接由 `PeerJSService.conns` 维护（transport 侧内存态） | 连接建立/断开驱动；本模块不存 |
| `LocalSource`：`name/storageDir/fileIndex/priority` | `local.go:23-30` | 只读装配参数 + 可变优先级 | 启动构造；priority 可运行时改（重启复原） |
| `URLSource`：`template/client/caps/priority` | `url.go:30-38` | 同 local | 同上 |

### 2.2 委托下级模块的持久化

**LocalSource（读取）**：不复制文件。两个数据源：
- file_index 映射：SQLite 持久化表（`repository.UpsertFileIndex(h, abs, base, size, false)`，`back/internal/transport/file_index.go:238`），key=sha256 hex，值为绝对路径；
- 内容寻址存储：磁盘文件 `storageDir/<hash[:2]>/<hash>`（`local.go:77`）——hash 前 2 字符子目录 + 全 hash 文件名（64 位小写 hex，见第 4 节约束）。

**LocalControl（写入）**：`AddLocalFile`/`WriteFile` 全委托 `FileIndexService.Create/WriteFile`（`local.go:176-200`）：前者只登记索引不复制文件（`file_index.go:203-244`，含硬链接拒绝 `RejectHardlink` 与 TOCTOU 防御）；后者流式写入 uploadDir（= `cfg.DownloadDir`）后复用 Create 计算 sha256 并登记（`file_index.go:250-278`）。持久化落在 SQLite file_index 表 + DownloadDir 磁盘文件。

**IPFS pin（`ipfs_control.go:34-73`）**：PinCID 时
- 内容写缓存：`storageDir/<hash[:2]>/<hash>`，目录 0o755、文件 0o644（`ipfs_control.go:47-54`）；
- 登记 `repository.InsertPin(cid, hash, cid, size)` → SQLite `ipfs_pins` 表（`ipfs_control.go:56`，`back/internal/repository/pin_repo.go:15-27`，`ON CONFLICT(cid) DO UPDATE` 幂等）；
- 登记 `repository.InsertFileMeta`（file_meta 表，`ipfs_control.go:60-67`）与 `repository.InsertFileProvider`（file_provider 表，`ipfs_control.go:68-70`，`back/internal/repository/file_repo.go:48,81`）。
- UnpinCID：`repository.RemovePin`（删 ipfs_pins 行）后 `os.Remove` 缓存文件（`ipfs_control.go:75-95`）。

**BT（`bt_control.go`）**：全部委托 `p2p_bt.BTClient`（`bt_control.go:12-19`），下载数据落 `cfg.DownloadDir`（`back/internal/router/router.go:151`）；任务状态（Downloads/Get/Pause/Resume/Remove）由 BTClient 内部持有，本模块只是薄封装转发（`bt_control.go:21-98`），`DownloadStatus` 字段做了一次适配映射（`bt_control.go:102-115`）。

**URL / Peer 读取**：不产生任何存储（URL 源仅为出站请求；peer 源为内存流，读完即弃）。

## 3. 何时储存

按触发时机逐条列出（无定时任务、无优雅关闭钩子）：

### 3.1 进程启动时（装配，建构内存注册表）
- `Manager` 创建 + 注册 local/peer/url 三 source：`back/cmd/server/main.go:217-228`（每次启动都重建注册表，无持久化恢复——重启后 /sources 快照从零统计开始）。
- BT/IPFS 控制面注入：`back/internal/router/router.go:150-196`。

### 3.2 某类 HTTP 请求触发（管理面 `back/internal/router/source_routes.go`）
| 端点 | 触发行为 | 储存/副作用 |
|---|---|---|
| `GET /sources`（`source_routes.go:29-31`） | `Snapshot()` 读内存 | 只读，不改任何状态 |
| `POST /sources/:name/priority`（`source_routes.go:32-45`） | `SetPriority` | 内存改优先级 + 重排（`manager.go:118-129`）；不落盘 |
| `POST /sources/local/add`（`source_routes.go:50-79`） | `LocalControl.AddLocalFile` | 委托 `FileIndexService.Create` → SQLite UpsertFileIndex + （仅索引，不复制文件；`file_index.go:203-244`） |
| `POST /sources/local/write`（`source_routes.go:80-108`） | `LocalControl.WriteFile` | 写 DownloadDir 磁盘文件 + SQLite 登记（`file_index.go:250-278`） |
| `POST /sources/bt/torrent|magnet`（`source_routes.go:111-154`） | `BTControl.Download*` | 委托 BTClient 启动下载（数据最终在 DownloadDir） |
| `GET/POST/DELETE /sources/bt/download*`（`source_routes.go:155-211`） | 任务查询/暂停/恢复/删除 | 委托 BTClient 状态管理 |
| `POST /sources/ipfs/pin/:cid`（`source_routes.go:214-226`） | `IPFSControl.PinCID` | 30s 超时 FetchByCID → 写 `storageDir/<h[:2]>/<h>` 缓存 + InsertPin/InsertFileMeta/InsertFileProvider（`ipfs_control.go:34-73`） |
| `DELETE /sources/ipfs/pin/:cid`（`source_routes.go:227-238`） | `IPFSControl.UnpinCID` | RemovePin + 删缓存文件（`ipfs_control.go:75-95`） |
| `GET /sources/ipfs/pins|gateways`（`source_routes.go:239-264`） | ListPins / GatewayStatus | 读，不写（网关状态为实况探测，`ipfs_control.go:118-146`） |

> 注意：`GET /sources` 与 `POST /sources/:name/priority` 未挂 authRequired；其余控制面端点均挂 `authRequired`（`source_routes.go:25-48`）。

### 3.3 文件读取路由时（事件回调/请求处理内，仅内存统计）
- 每次 `OpenRange/OpenAny/Info` 逐源尝试后立即 `record`（成功 Success++ / 失败 Fail++ 与 LastErr，`manager.go:161-224,290-309`）——这是「何时储存统计」的准确触发点：**每次读取尝试**，不是定时聚合。统计仅内存，供 `GET /sources` 快照。
- peer 流建立：`collectPeers` TryLock 占用槽位（lock 写入 `peerLocks`，`peer.go:108-123`）；流结束（Close/失败/收割）释放（`peer.go:139-178,208-215`）。

### 3.4 优雅关闭 / 进程退出
- **无任何 source 侧持久化或清理钩子**：source 包没有 Close/Flush。进程退出时内存态自然消失；已委托持久化的数据（SQLite file_index/ipfs_pins、磁盘 CAS 文件、DownloadDir 文件）不受影响。

## 4. 储存什么

### 4.1 内存态清单（进程内）
- **Manager 注册表**：`sources []Source`——每条含 Name（唯一，重名拒绝，`manager.go:47-59`）、Type、Capabilities、Priority、Available 结果；格式见 `Snapshot()` 输出的 `SourceStatus` JSON 字段（`source.go:90-98`：name/type/priority/capabilities/stream/available/stats）。
- **Stats 统计条目**（`source.go:81-87`，JSON 于 `GET /sources`）：`Success int64`、`Fail int64`、`Bytes int64`（累计传输字节，仅 OpenAny 成功路径记 len(data)，`manager.go:213`）、`LastErr string`（最近失败原因）、`LastAt time.Time`。
- **控制面引用**：`btControl`/`ipfsControl` 接口或 nil（默认零值 nil）。
- **PeerSource 连接态**：`peerLocks`（peerID→mutex）；对端连接清单由 transport 维护。
- **各 source 可变字段**：priority（int，可 SetPriority 改）；URLSource 的 template/client/caps（caps 由模板是否含 `%d` 推导，不可变，`url.go:52-57`）。

### 4.2 委托持久化的条目（最终形态）
| 条目 | 表/路径 | 关键字段 | 默认值/约束 |
|---|---|---|---|
| 内容寻址文件 | `storageDir/<hash[:2]>/<hash>` | — | hash 严格 64 位小写 hex（`source.go:100-106`、`hashutil.go:60-72`）；CAS 写入 0o644/0o755（`ipfs_control.go:49-52`）；本地读取时 file_index 映射优先于 CAS（`local.go:76-87`） |
| file_index 映射 | SQLite（repository.UpsertFileIndex，`file_index.go:238`） | Hash/Path/Name/Size/Seq | 登记只允许 allowed root 内路径（H2，`file_index.go:203-208`）；`cfg.DownloadDir` 为写根（`peerjs_service.go:127`） |
| ipfs_pins | SQLite `ipfs_pins`（`pin_repo.go:16-27`） | cid, hash, size, filename, pinned_at | cid 唯一（ON CONFLICT 更新）；ListPins 上限 1000（`pin_repo.go:31-35`） |
| file_meta / file_provider | SQLite（`file_repo.go:48,81`） | Hash/Size/Filename/Type；Hash/Provider/path | IPFS pin 时登记 Type=FileTypeBlob、provider="local"、path=`<h[:2]>/<h>`（`ipfs_control.go:60-70`） |
| BT 下载任务/数据 | BTClient 内部态 + `cfg.DownloadDir` | infohash/name/total_size/files 等（`control.go:57-77`） | 本模块只做适配转发（`bt_control.go:102-115`）；BT 完成后由 router 侧回调 `FileService.RegisterBTFile` 登记（`router.go:161-176`，属 BT 模块链路） |

### 4.3 路由语义约束（「储存顺序」的规则）
- 优先级升序尝试；local 命中即返回（内容寻址本地权威），未命中降级 peer → url（`manager.go:9-14`、`source.go:12-14`）；默认优先级均为 0，实际顺序 = 注册顺序（main.go:217-228 先 local 后 peer 后 url），可运行时调整。
- OpenRange 只走 CapStream；URLSource 模板无 `%d` 时是 CapFile，OpenRange 会跳过它（`manager.go:162-165`）。

## 5. 边界与坑

**关键不变式**
1. **hash 防御**：所有读取/写入入口统一 `validHash`（64 位小写 hex，`source.go:100-106`）——防止 `hash[:2]` 切片越界 panic（历史事故：对端发空/短 hash 打崩进程，见 `back/internal/transport/inbound.go:55-59`）。
2. **内容寻址兜底**：对端/URL 给的内容必须等于请求的 hash——URL 全量请求读取完成即校验（`url.go:135-138,180-214`、`Fetch` 同 `url.go:164-169`）；peer 全量由 transport fetchReader 校验（`peer.go:11`）；本地写入时已校验（`local.go:7-8`）。
3. **本地权威**：local 命中即返回，未命中才降级（`source.go:12-14`）；file_index 映射优先于 CAS，但映射路径**必须可读**否则回退 CAS（`local.go:70-71,83-84` 注释：历史脏数据/恶意登记不回传根外文件）。
4. **peer 连接级单槽**：同 peer 同时只一个流（帧协议 expect 单槽，`peer.go:8-10`）；锁按 peer 隔离，多连接并行互不阻塞（`peer_test.go:611-667`）。

**失败处理**
- 路由全失败返回汇总错误，含每源失败原因（`manager.go:175-181,216-223`）；PeerSource 全对端失败聚合各对端原因（`peer.go:173-177`），无在线对端明确报 "no online peer available"（`peer.go:89-91`）。
- 忙对端 TryLock 失败即跳过不等待（等大文件流结束会阻塞整个路由，`peer.go:104-107`）；失败路径立即释放锁，竞速输家由收割 goroutine Close（防锁泄漏 + fetch 状态悬挂，`peer.go:135-178`；测试 `peer_test.go:256-272,430-469`）。
- 对端中途断开（未见 done）→ 读取期报错，不静默返回截断数据（内容寻址语义，`peer_test.go:539-568`）；服务关闭后读取必须报错（`peer_test.go:515-535`）。
- 控制面 nil 降级：未注入 BT/IPFS/未配 fileIndex 一律 `ErrControlUnsupported` 或 nil（`control.go:16`；`source_test.go:238-244,334-376`）；IPFS 登记失败仅 LogWarn 不中断 pin 主流程（`ipfs_control.go:56-70`）。
- URL 服务器不认 Range 回 200 全量时截取 offset 段（正确性优先，带宽浪费可接受，`url.go:121-133`）；`Available` 无主动探测恒 true，失败由统计暴露（`url.go:79-81`）。

**已知坑（代码注释/README 里提到的）**
- 大文件走 CapFile 会 8GB 全量 buffer OOM——OpenRange 故意不降级 CapFile（`manager.go:150-152`）。
- `resolvePath` 用 `IsPathReadable` 而非 `IsPathAllowed`：运营者把共享目录设在下载目录外时，用 IsPathAllowed 会把正当文件判成越权并回退到不存在的 CAS 副本 → 对端 "read failed"（`local.go:72-75`）。
- `SafeOpen`（os.Root）防索引路径在登记后被换成软链（TOCTOU，`local.go:90-105`）。
- Manager 控制面历史坑：曾用包级全局 var，多实例共享 + 测试需防御性清理；改按实例字段（`manager.go:33-39`）。
- `peerReadCloser` 双 Close 二次 Unlock panic 坑 → sync.Once 幂等（`peer.go:199-215`；`peer_test.go:688-703`）。
- 回源死循环：A←→B 互连互相回源 → trace 防环（`peer.go:77-79`、`inbound.go:78-89`、`transport/conn.go:47`）。

## 6. 对外连接

- [../connections/05-router-source.md](../connections/05-router-source.md)：router → source。`back/internal/router/source_routes.go` 注册 `GET /sources`、`POST /sources/:name/priority` 与 local/BT/IPFS 控制面端点，经 `SetSourceManager` 注入的同一 `*source.Manager` 读写注册表/统计/控制面。
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md)：双向。PeerSource → transport：经 `PeerJSService.Connections()/OpenStreamFrom` 拉取对端流（`peer.go:27,94,140`）；transport → source：`serveFile` 经 `FileRouter` 接口（OpenRange/InfoSize，`inbound.go:42-51`）多源回源，`peerjsSvc.SetFileRouter(mgr)` 装配于 main（`main.go:233`）。
- [../connections/10-controller-storage.md](../connections/10-controller-storage.md)：控制面 → 存储。local add/write 经 FileIndexService 写 DownloadDir + 登记；IPFS PinCID 写 `storageDir/<h[:2]>/<h>` 缓存并登记 file_meta/file_provider，UnpinCID 删缓存文件。
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md)：source → transport 存储能力。LocalSource 复用 `transport.FileIndexService` 同一份索引（装配于 `peerjs_service.go:127,531-533`），读取路径决策与 transport.serveFile 收敛一致（`local.go:3-8`）。
- [../connections/04-service-repository.md](../connections/04-service-repository.md)：source → repository。`ipfsController` 直调 `repository.InsertPin/ListPins/GetPin/RemovePin/InsertFileMeta/InsertFileProvider`（`ipfs_control.go:56-70`；`pin_repo.go`、`file_repo.go`）；LocalControl 底层经 `repository.UpsertFileIndex`（`file_index.go:238`）。
- [../connections/02-router-controller.md](../connections/02-router-controller.md)：路由装配链。main → router：`SetSourceManager` 后 `SetupRouter` 内注入 BT/IPFS 控制面（`router.go:150-196`）；管理面写操作统一挂 `authRequired`（`source_routes.go:25-48`）。
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md)：对照关系。HTTP 下载（`GET /sha256sum/:sha256`、`/download/:hash`）走 `downloader.UniversalDownloader.Download`（`back/internal/controller/download.go:45-94`），是另一套多协议下载体系，不经过 source.Manager；两套「源」概念并存（下载器的 local/http/ipfs/bt 源 与 source 模块的 local/peer/url 源为不同实现）。
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md)：source → 出口代理。URLSource 可注入带 ech-proxy 出口 Transport 的 `http.Client`（`url.go:44-47` 注释：wintools 的 ech-proxy 可作为 url source 出站代理，不需要独立 source 类型；`url.go:4-7`）。

## 附：未核实项
1. `back/cmd/server/main.go:230-232` 注释称「HTTP 下载等根请求已走 mgr」，但当前 `GET /sha256sum` 实际走 `universalDownloader.Download`（`back/internal/controller/download.go:45-94`），downloader 包未导入 source 包——该注释疑为历史说明，未能在当前代码中核实到对应路径。
2. 模块文档交叉引用：本仓库 `doc/design/modules/` 尚无其它编号模块文档，无法按编号交叉引用；涉及下载器/存储/控制器模块处均以代码路径标注。