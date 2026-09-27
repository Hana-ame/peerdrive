# 模块 03：storage 内容寻址文件存储

- **代码位置**：`back/internal/pathutil`（路径安全边界）；落盘布局与读写逻辑分布在 `back/internal/service/file_service.go`、`back/internal/repository/anon_repo.go`、`back/internal/source/local.go`、`back/internal/transport/inbound.go`、`back/internal/downloader/universal_downloader.go`、`back/internal/controller/{file,download,p2p}.go`、`back/cmd/server/main.go`；实际数据落在仓库 `back/storage/`（即运行时 `PEERDRIVE_STORAGE` 指向的目录）。
- **功能一句话**：以内容寻址方式（`storage/<sha256 前两位>/<sha256>`）把文件与匿名合集落盘到本地磁盘，并借助 pathutil 的 os.Root 安全边界，保证任意路径、软链、硬链接都无法逃出允许根目录。
- **依赖**：`path/filepath`、`os`（`os.Root`，Go 1.24）、`crypto/sha256`、SQLite（`back/internal/repository` 的 `file_meta` / `file_providers` / `file_index` 表作元数据旁路）、`back/pkg/hashutil`、`back/internal/config`、`back/internal/log`。
- **被依赖**：`controller`（HTTP 上传/下载/登记）、`transport`（WebRTC `req`/`create`/`upload` 帧）、`source`（`LocalSource`）、`downloader`（`LocalFetcher`/`cacheToLocal`）、`service`（`FileService`/`AnonService`/`PeerPuller`/`SyncService`/`NodeDirectory`/`NodeShare`）、`cmd/server`（启动装配）。

## 1. 逻辑

模块分两层：**安全判定层**（`back/internal/pathutil`，纯包含判定 + 安全读写）与**内容寻址落盘层**（storageDir 的磁盘布局与读写动作，散落在上列各包）。

### 1.1 pathutil 的核心职责

包注释明确定义：只做一件事——判断「一个路径是否落在某个根目录之内」，且必须被文件登记（`service.FileService`）、对外服务（`transport.FileIndexService.serveFile` / `source.LocalSource`）、共享清单过滤（`service.NodeShare`）三处**完全一致**地使用，避免出现过「登记放行、共享清单列得出、唯独读取判越权 → 对端 read failed」的组合错误（`back/internal/pathutil/path.go:1-19`）。

核心判定函数（`back/internal/pathutil/path.go`）：

- `Within(root, path)`（:85-113）：root/path 空 → false（不给「空根 = 全放行」兜底）；先 `normalize`（:135-164：拒 NUL 字节、Windows 拒保留设备名、Abs+Clean+尽力解析软链、Windows 再还原 8.3 短名并折叠大小写），再 `filepath.Rel` 判 `..` 逃逸；root 自身算在内。
- `resolveBestEffort`（:57-81）：`EvalSymlinks` 整条解析不了（最后一段还不存在——写目标/软链目标常见）时退而解析**最长存在前缀**，保证 root 与 path 落在同一套写法上（macOS `/var` → `/private/var` 软链场景的真实修法）。
- `WithinAny`（:116-123）：命中任意一个根即放行。
- `foldCase`（:130）：做成变量而非直接读 `runtime.GOOS`，让 Linux CI 也能测 Windows 分支。

安全读写层：

- `SafeOpen`（`back/internal/pathutil/safeopen.go:41-72`）：把「校验」与「打开」合成一步，消除 TOCTOU——先 normalize 得到无软链的规范形式，再在 root 上开 `os.Root`（Linux 走 `openat2(RESOLVE_BENEATH)`，其它平台走目录句柄 + 逐段 `O_NOFOLLOW`，见 :3-24 注释），`os.Root.Open` 与解析一次性由内核完成。返回的 `*os.File` 在 Root 关闭后依然可读（:39-40）。`SafeOpenAny`（:76-93）在 roots 中第一个包含 path 的根内打开。
- 写侧同一套：`SafeWriteFileAny` / `SafeOpenFileAny` / `SafeMkdirAllAny` / `SafeRemoveAny` / `SafeRemoveAllAny`（`back/internal/pathutil/safewrite.go:125-193`），全部经 `pickRoot`（:31-83，故意不解析软链、不折叠大小写，解析交给 os.Root；字符串层防御 NUL/保留设备名/`..`/绝对路径）+ `withRoot`（:89-103）+ `scopedOps`（`back/internal/pathutil/scoped.go:14-88`，一根统一接口，能 Root 就 Root，不能且逃生阀打开时降级为 `base/rel` 拼绝对路径走老 `os.*`）。`rejectSelf`（safewrite.go:116-121）拒绝以根自身为操作对象。
- 降级/自检：`openRootOrFallback`（`back/internal/pathutil/rootprobe.go:30-57`）在根还不存在时自动 `MkdirAll`（首次运行的 storageDir 是预期行为）；`RootUnavailable` + `PEERDRIVE_ROOT_FALLBACK=1` 逃生阀（:80-106）；`ProbeRootSupport`（:133-147）用于**启动自检**；`ExplainRootFailure`（:113-128）把失败翻译成人话（不存在/权限/文件系统不支持）。`WarnOnce`（:63-72）保证同一目录只告警一次。
- 卷根拒绝：`IsUnsafeRoot` / `UnsafeRoots`（`back/internal/pathutil/unsaferoot.go:20-59`）。
- Windows 保留设备名：`HasReservedName`（`back/internal/pathutil/reserved.go:34-60`），`CON`/`PRN`/`AUX`/`NUL`/`COM1..9`/`LPT1..9` 逐段取主干名（忽略扩展名，`CON.txt` 也算设备），只在 Windows 上生效（:145 `normalize` 里启用）。
- 8.3 短名：`ExpandShortNames`（`back/internal/pathutil/shortname_windows.go:59-78`），把文件系统真给了短名的成分还原成长名（`C:\PROGRA~1` → 长名），不做无所求替换。
- 硬链接：`RejectHardlink`（`back/internal/pathutil/hardlink.go:37-52`）——硬链接没有方向、路径判定看不出来，只能「链接数 > 1 就拒绝」（宁可少给）。**必须传已打开的 `*os.File`**（Windows 只能对着句柄调 `GetFileInformationByHandle` 拿 `NumberOfLinks`，见 `back/internal/pathutil/links_windows.go:15-34`；Unix 侧 `NlinkOf` 对 fd 做 fstat，`back/internal/pathutil/links_unix.go:24-37`）。`HardlinkCheckEnabled`（hardlink.go:20-22）：平台拿得到链接数且未设 `PEERDRIVE_ALLOW_HARDLINKS=1`。

### 1.2 内容寻址落盘层的流程

- **布局决策**：全仓库统一为 `filepath.Join(storageDir, hash[:2], hash)`——例子：`back/internal/source/local.go:77`、`back/internal/service/file_service.go:796`、`back/internal/transport/inbound.go:107,191`、`back/internal/controller/download.go:110`、`back/internal/repository/anon_repo.go:73`、`back/internal/downloader/universal_downloader.go:79-80`。`back/storage/` 实际目录形态与之一致：`back/storage/<05|09|13|…|ff>/<64 位 hex 文件名>`。
- **读取优先级**（`source/local.go:70-105`，与 `transport/inbound.go:106-153` 同一决策收敛）：file_index 命中**且路径可读** → 读映射路径；否则回退内容寻址副本 `storageDir/<hash[:2]>/<hash>`。读取侧用 `IsPathReadable`（下载根 ∪ 运营者声明的可读根）而**不是**登记侧 `IsPathAllowed`——否则「共享目录在下载目录之外」会被误判越权、回退到不存在的 CAS 副本 → 对端 read failed（local.go:73-75 注释）。CAS 副本的打开一律 `pathutil.SafeOpen(storageDir, p)`（local.go:104、inbound.go:223）。
- **写侧（HTTP 上传）**：multipart → 写系统临时文件边算 sha256（`file_service.go:532-548`）→ `copyInto` 在允许根内打开目标并拷贝（`file_service.go:566-574, 699-717`；不用 `os.Rename`，因为它没法 Root 化、会跟着 dst 父目录软链走，见 :568-570 注释）→ 登记 `file_meta` + `file_providers`（:585-593）。同 hash 已存在时直接返回 `ErrFileAlreadyExists`（:561-564，内容寻址天然去重）。
- **写侧（匿名合集）**：JSON 序列化 → 对 JSON 字节算 sha256 → 同样写 `storageDir/<hash[:2]>/<hash>`（`back/internal/repository/anon_repo.go:24-61`、`back/internal/service/anon_service.go:138-160`）。读取侧 `GetAnonCollectionByHash` 先 `hashutil.IsValidSHA256` 校验再 `os.ReadFile`（anon_repo.go:64-86；:68-69 注释：hash 未校验时 `hash[:2]` 越界 panic、`..` 逃逸 storage 目录）。
- **WebRTC 分片上传（区别于 HTTP 上传）**：对端经 `upload` verb 上传时，落点是 **file_index 的 uploadDir（= `cfg.DownloadDir`，不是 storageDir）**——`NewPeerJSService` 里 `fileIndex: NewFileIndexService(cfg.DownloadDir)`（`back/internal/transport/peerjs_service.go:127`；`file_index.go:49-63` 默认 `./files`）。`UploadSession` 按 64KB 分片 `WriteAt` + 位图（`file_index.go:280-390`），`Complete` 时 fsync + 全文件 sha256 + `UpsertFileIndex` 登记（:415-472）。完成后文件留在 downloadDir 下、登记进索引（对外服务的副本），并**不**复制进 storage CAS。
- **下载侧**：`LocalFetcher` 依次试 `storageDir/<h[:2]>/<h>`、`storageDir/p2p/<h[:2]>/<h>`、DB provider 回读（`back/internal/downloader/universal_downloader.go:76-107`）；`FileService.ReadFile` 同理（`file_service.go:793-834`）。非 local 协议取到内容后 `cacheToLocal` 写回 CAS（:378-398）。
- **生命周期**：启动装配（见 §3）→ 运行期被 HTTP/WebRTC/拉取回调驱动读写 → 收到 SIGINT/SIGTERM 后 `srv.Shutdown(20s)` 收尾 + `peerjsSvc.Close()`（中止未完成上传会话）+ `repository.CloseDB()`（`back/cmd/server/main.go:264-284, 79-83, 110-111`）。

## 2. 如何储存

**介质与位置**：本地磁盘目录，由环境变量 `PEERDRIVE_STORAGE` 指定，默认 `./storage`（`back/internal/config/config.go:191`）；`PEERDRIVE_STORAGE_ENABLE` 默认 true（:192），置 false 时上传/登记/删除整体拒绝（`file_service.go:184-188, 526-530, 622-626, 652-656`）。`Validate` 在启动期拒绝空 `PEERDRIVE_STORAGE`（config.go:263-265）。仓库内 `back/storage/` 是默认目录的实际落点，其 `.gitignore` 为 `*`（整个数据目录不入库）；当前含 16 个真实 blob（05/09/13/34/39/40/49/58/81/9b/a3/a6/b2/bf/ea/ff 各一），内容为原始测试数据与匿名合集 JSON。

**格式与结构**：

- 文件 blob：`<storageDir>/<hash[:2]>/<hash>`，内容为原始字节（不加密、不额外压缩，`.gitignore` 里的 `.gitignore` 除外），目录 `0755`、文件 `0644`（`file_service.go:65-68, 89-93, 566-574`、`anon_repo.go:41-47`）。
- 匿名合集：同一布局，文件体为 JSON（实测含 `version`/`entries[]`/`providers` 等字段；代码侧取 `version >= 1`，见 anon_repo.go:82-84、:115-137）；写入前先 `sort.Slice` 按 Path 排序使序列化稳定（anon_repo.go:29-31）。
- 分片上传临时文件：`<DownloadDir>/<sanitizeName(name)>`（`file_index.go:329-333, 637-650`，文件名净化防 `..`/分隔符穿越；Windows 上 `filepath.Base("/")` 返回 `\` 也一并挡掉）。
- 兼容读取路径 `<storageDir>/p2p/<hash[:2]>/<hash>`：`LocalFetcher`/`ReadFile`/`ClearLocalCache` 都会读写它（`universal_downloader.go:80, 432`、`file_service.go:797`），但当前代码中**找不到向该路径写入的调用点**（git 历史显示 2026-08-16 一次 refactor 涉及 `"p2p"`）；判断为历史拉取流程遗留的读取兼容位（**未核实**：写入方）。
- 同级运行时状态 JSON（非内容寻址，但也在 storageDir 下）：`share_scope.json`（`nodeshare.go:66-69, 227`，测试中 0600）、`joined_nodes.json`（`node_directory.go:36-37`）。
- 元数据旁路（委托 `back/internal/repository`）：SQLite `file_meta`（hash/size/mime_type/gziped/filename/type/created_at）、`file_providers`（hash → provider_type + path + available）、`file_index`（hash → 绝对路径 + seq + tombstone）三表与 blob 同层登记（`file_service.go:585-593, 271-273`、`anon_repo.go:52-58`）。

**命名规则与约束**：文件名 = 64 位小写十六进制 SHA-256（`hashutil.IsStrictSHA256`，`back/pkg/hashutil/hashutil.go`）；`[:2]` 切片前所有公开入口都先校验（`source.go:101-105`、`inbound.go:67`、`anon_repo.go:70`、controller 各 handler）。

**是否持久化**：blob 是**持久化**的（磁盘文件，进程重启后仍在，见 `reapUploads` 不删已完成文件、`file_index.go:145-151`）。纯内存态有两处：① `repository.anonStorageDir` 包级变量（`anon_repo.go:17-22`，由 `main` 在启动时 `SetAnonStorageDir` 注入，进程重启后需重新注入）；② `FileIndexService.uploads` map（`file_index.go:43-44`，分片上传会话，**进程重启即失效**——注释明说「续传语义只在进程存活期间有意义」，Close 时 Abort 删除未完成的目标文件，file_index.go:174-191）。`share_scope.json`/`joined_nodes.json` 虽是 disk 态但语义上可视为「运行时可改的状态快照」，环境变量只是首次初值（nodeshare.go、main.go:128-134）。

## 3. 何时储存

- **进程启动**（`back/cmd/server/main.go`）：`config.Validate`（:55-57）→ `checkUnsafeRoots` 拒绝卷根配置（:68-70, 316-333，逃生阀 `PEERDRIVE_ALLOW_UNSAFE_ROOT=1`）→ `warnUnsupportedRoots` 对每个配置目录跑 `pathutil.ProbeRootSupport` 自检（:71, 340-355）→ `repository.InitDB`（:76）→ `repository.SetAnonStorageDir(storageDir)`（:87）→ `peerjsSvc.FileIndex().AddReadRoot(storageDir)`（:105）。
- **HTTP 请求**（同步写盘）：`POST /files/upload` → `FileService.Upload`（`controller/file.go:39-87` 路由注释 :8-14）；`POST /files/register_local` → `RegisterLocal`（只算 hash 登记、不复制，:180-277）；`POST /files/register_folder` → `RegisterFolder`（递归 `WalkDir`，:280-357）；`POST /files/register_url` → `RegisterURL`（取回 body 后若 `storageEnable` 则写 CAS，:468-474）；`POST /files/copy` → `CopyFile`（从 CAS/提供者读内容写进允许根内 dest，:725-790）。
- **匿名合集创建/更新**：`AnonService.CreateCollectionWithVisibility`（`anon_service.go:138-160`）与 `CollectionService.SaveAnon` → `repository.SaveCollection`（`collection_service.go:138-139`、`anon_repo.go:25-61`）。
- **WebRTC 对端帧**：`create` 只登记 file_index **不复制文件**（`inbound.go:242-253`、`file_index.go:205-244`）；`upload` 分片落盘 downloadDir（`serveUploadBegin` inbound.go:258-320 + `uploadWorker` :391-422 + `UploadSession.Complete` file_index.go:415-472）；`req` 只读（inbound.go:66-185）。
- **跨节点拉取保存（网盘 M3）**：`PeerPuller` 流式写 `<DownloadDir>/pulled/<清洗后的相对路径>`，完成后回调 `create` 登记（`peerpull.go:437-463, 380-401`；"saved but not indexed" 分支说明文件已在盘上）。
- **下载缓存回写**：`UniversalDownloader` 从 local 之外的协议（ipfs/btdht/http）取到内容后 `cacheToLocal` 写 CAS（`universal_downloader.go:378-398`）。
- **IPFS CID 网关 fallback**：`FileService.ImportGatewayData` 拉网关数据落盘 + 登记（`file_service.go:60-79`，由 `controller/download.go:148` 调用）。
- **BT 下载完成**：`FileService.RegisterBTFile` 把完成文件拷入 CAS + 登记（`file_service.go:85-119`）。
- **定时任务**：`reapUploads` 每 5 分钟（`time.Tick(5*time.Minute)`）扫描 uploads 表，10 分钟无活动的未完成会话 Abort 并删除目标文件（防磁盘耗尽），已完成会话只摘表项**不删文件**（`file_index.go:139-172`）。
- **优雅关闭**：SIGINT/SIGTERM → `srv.Shutdown(20s)` 让进行中的上传/拉取把半截文件写完，超时强制 `srv.Close()`（main.go:264-284）；defer 链 `repository.CloseDB`（:79-83）、`peerjsSvc.Close()` → `FileIndexService.Close()` 中止全部未完成上传会话（peerjs_service.go:152-178、file_index.go:180-191）。

## 4. 储存什么

| 条目 | 位置/内容 | 关键约束 |
|---|---|---|
| 文件 blob | `<storageDir>/<hash[:2]>/<hash>`，原始字节 | hash 为 64 位小写 hex；目录 0755、文件 0644；内容寻址去重（同 hash 不重复写，`ErrFileAlreadyExists`，file_service.go:561-564） |
| 匿名合集 | 同一 CAS 布局，JSON 体 | `version >= 1`（anon_repo.go:82-84）；写入前按 `Path` 排序稳定序列化（:29-31） |
| 分片上传临时文件 | `<DownloadDir>/<sanitizeName(name)>` | 名称净化（file_index.go:637-650）；声明 size 上限 8GB（:315、inbound.go:260）；分片/位图粒度 64KB（:281、inbound.go:25） |
| 兼容读取位 | `<storageDir>/p2p/<hash[:2]>/<hash>` | 只读/清理，无当前写入方（**未核实**） |
| 运行时状态 | `<storageDir>/share_scope.json`、`joined_nodes.json` | 非内容寻址；`share_scope.json` 0600（nodeshare_level_test.go:235） |
| 元数据（SQLite 旁路，委托 repository） | `file_meta`（hash/size/mime/gziped/filename/type/created_at）、`file_providers`（provider_type+path+available）、`file_index`（hash→abs path+name+size+seq+deleted） | blob 与记录同层登记；`file_index` 是「本节点能对外提供什么」的唯一真源（file_service.go:262-273 注释） |

配置默认值（`back/internal/config/config.go:191-199`）：`PEERDRIVE_STORAGE=./storage`、`PEERDRIVE_STORAGE_ENABLE=true`、`PEERDRIVE_DOWNLOAD_DIR=./downloads`、`PEERDRIVE_MAX_UPLOAD_BYTES=100MB`（认证）、`PEERDRIVE_MAX_UPLOAD_ANON_BYTES=10MB`（匿名）。安全开关均为默认关：`PEERDRIVE_ALLOW_HARDLINKS=1`（hardlink.go:21）、`PEERDRIVE_ROOT_FALLBACK=1`（rootprobe.go:81）、`PEERDRIVE_ALLOW_UNSAFE_ROOT=1`（main.go:317）。空文件合法（sha256(空)，inbound.go:259）。

## 5. 边界与坑

- **TOCTOU 是全局主题**：判定（`Within`）与打开/写入必须同一次解析——读取走 `SafeOpen`，写入走 `SafeWriteFileAny`/`SafeOpenFileAny`，全部基于 `os.Root`（safeopen.go:3-24、safewrite.go:3-14）。历史教训：`os.Rename`/`os.Create(dst)` 会跟着父目录软链出根，所有调用点已迁到 `copyInto`（file_service.go:695-721）。
- **hash 校验不可省**：处处 `hash[:2]`，未校验会越界 panic 或 `..` 逃逸；`serveFile` 曾因对端发空/短 hash 直接 panic 杀进程（inbound.go:54-60 注释），现在每条入口先 `IsStrictSHA256`/`IsValidSHA256`（inbound.go:67、anon_repo.go:70、source.go:101-105）。
- **登记侧与读取侧边界分离**：写/登记边界只有 rootDir（`IsPathAllowed`，file_index.go:72-83），读取边界 = rootDir ∪ `AddReadRoot` 声明的可读根（:110-122）；共享目录**不能**塞进写边界，否则等于允许对端写入你的共享目录（:70-71 注释）。
- **file_index 路径不可读 → 回退 CAS，不回传根外文件**（H2）：serveFile / LocalSource 对索引命中但 `IsPathReadable` 失败的历史脏数据/恶意登记一律回退内容寻址副本并告警（inbound.go:117-120、local.go:79-84）；list/info/sync 对根外 Path 做脱敏（inbound.go:233-238）。
- **硬链接**：链接数 > 1 拒绝登记（宁可少给）；误伤 pnpm node_modules / `cp -l` 备份时设 `PEERDRIVE_ALLOW_HARDLINKS=1`（hardlink.go:24-49）。Windows 必须用句柄取 nlink，2026-09-20 前该防线在 Windows 是空的（links_windows.go:15-22）。
- **降级模式的代价**：`PEERDRIVE_ROOT_FALLBACK=1` 退回按路径判定，存在 TOCTOU 窗口；日志里持续告警（rootprobe.go:47-54, 166），`Degraded()` 供调用方自查（scoped.go:88）。
- **卷根配置**：`PEERDRIVE_STORAGE=/`（环境变量没展开的常见事故）启动即拒绝，逃生阀 `PEERDRIVE_ALLOW_UNSAFE_ROOT=1`（main.go:61-70, 316-333；unsaferoot.go:3-8 注释）。
- **Windows 专属**：保留设备名 `CON`/`NUL`/`COM1`… 在任何目录下都是设备（登记 `CON` 会挂住请求），逐段主干名判定（reserved.go:3-17）；8.3 短名必须还原成长名再比较，且短名**不创造新的可达范围**（shortname_windows.go:5-19）；`t.TempDir()` 在 GitHub Windows runner 上是短名，只还原一边会算成两棵树（safewrite.go:64-68 注释）。
- **上传会话生命周期**：10 分钟无活动会被 reap 删除目标文件（file_index.go:139-172）；连接级 `pendingUpload` 30 秒无数据自动清空，防连接级 DoS（inbound.go:303-316）；会话在 Windows 上句柄必须显式关闭，否则 TempDir 清理失败（file_index.go:174-179 注释）；已完成文件名由 `sanitizeName` 兜底（传入 `..`/分隔符时统一叫 `upload.bin`，file_index.go:637-650）。
- **集合兼容**：历史集合无 visibility 字段时 `EffectiveVisibility()` 兜底 public，空串不要直接透传前端（anon_repo.go:132-135 注释）；`version < 1` 拒绝反序列化。
- **同步写盘非原子**：`SaveCollection`/`CreateCollection` 直接 `os.WriteFile`（无临时文件 + rename），崩溃可能留下不完整 blob（代码未做原子化，此条按现状描述，未核实是否有外部兜底）。
- **真实数据目录**：`back/storage/.gitignore` 为 `*`，仓库内 blob 全是测试/演示数据。

## 6. 对外连接

- [../connections/10-controller-storage.md](../connections/10-controller-storage.md)：controller（`file.go`/`download.go`/`p2p.go`）→ FileService/AnonService，HTTP 侧的上传、登记、复制、下载全部经此读写 CAS。
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md)：transport 侧 `req`/`create`/`upload` 帧与 storageDir、file_index 的读写关系（serveFile 双路径决策、分片上传落点 downloadDir）。
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md)：download controller → `UniversalDownloader`/`LocalFetcher` 从 CAS 读（`cacheToLocal` 反向把网络内容写回 CAS）。
- [../connections/03-controller-service.md](../connections/03-controller-service.md)：controller → service 层（FileService/AnonService/SyncService）的服务边界与安全校验。
- [../connections/04-service-repository.md](../connections/04-service-repository.md)：FileService/repository 把 blob 元数据登记进 SQLite `file_meta`/`file_providers`/`file_index`（本模块的元数据旁路）。
- [../connections/05-router-source.md](../connections/05-router-source.md)：router 装配 `LocalSource(storageDir, fileIndex)` 与将 `storageDir` 注入 Gin context（读取侧多源路由）。
- [../connections/06-service-transport.md](../connections/06-service-transport.md)：service/transport 间 PeerPuller、source、file_index 的装配；跨节点拉取保存与 CAS/file_index 的关系。
- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md)：前端经本地 WS 会话与 HTTP API 发起的上传/下载/登记，最终落到本模块的 CAS 布局。
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md)：peerdrive-media 消费端按 hash 经 `req` 帧请求节点内容，节点侧由本模块的 CAS/file_index 层供给（只读方向）。