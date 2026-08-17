# service 层（back/internal/service/）

> 层归属：AOP ④ 业务核心（见 doc/LAYERS.md §1）。
> 业务用例编排层：controller 的「下一步做什么」在这里变成「怎么做」——组合 repository
> 读写、文件系统操作、外部能力（downloader/p2p_bt/transport 语义 API）。
> M2 收层（REFACTOR.md §7 M2）后 controller 不再直调 repository，本层是唯一业务入口。

**一句话职责**：把 HTTP 语义业务（文件/合集/认证/同步/分享/任务/pin）的规则与持久化
收敛成可被 controller 单次调用的服务方法；不感知传输层（不 import transport 业务语义
之外的连接细节，LAYERS.md §3 规则 3：service 包内不直接 import transport——装配经
controller/router 完成）。

## 职责

### 解决什么问题

M2 之前 controller 60+ 处散落 repository 直调（collection/fork/merge/file 控制器），
依赖方向混乱且无规则可守。本层目标：

- **依赖单向**：`controller → service → repository`（LAYERS.md §2）
- **安全边界集中**：路径防御（isPathInStorage）、hash 校验、上传限流都在本层做，controller 只传参
- **业务组合点**：同步（SyncService 组合 downloader）、CID 导入（FileService.ImportGatewayData）、
  BT 完成登记（FileService.RegisterBTFile）等跨域逻辑有明确归属

### 两种风格并存

| 风格 | 服务 | 说明 |
|---|---|---|
| 有状态实例（构造注入依赖） | FileService、AnonService、AuthService、SyncService | 持 config/repository/downloader 等依赖 |
| 无状态透明转发（空结构体） | CollectionService、ShareService、TaskService、PinService | M2 收层产物：方法名与 repository 一一对应，仅收口依赖方向（collection_service.go:1-5 头注释明示） |

## 模块清单

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| anon_service.go | 匿名集合（内容寻址 JSON 存储）：创建/读取/列表/版本提交 | `AnonService`：`CreateCollection`、`GetCollectionByHash`、`ListCollections`、`CommitCollection` |
| auth_service.go | 用户注册/登录/登出/authkey 验证（bcrypt） | `AuthService`：`Register`、`Login`、`Logout`、`ValidateKey`；`ErrInvalidCredentials` |
| collection_service.go | 用户集合域用例（M2 透明转发层） | `CollectionService`：`Get/List/Search/ListPublic/Create/CreatePlain/GetOrCreate/SetVisibility/UpdateTags/UpdateCurrentHash/ListEntries/GetEntry/AddEntry/AddProviderEntry/RemoveEntry/CreateVersion/SnapshotEntries/VersionLog/VersionEntries/RestoreVersion/GetAnonByHash/SaveAnon` |
| file_service.go | 文件上传/注册/验证/删除/复制/浏览/元数据（含安全边界） | `FileService`：`Upload`、`RegisterLocal`、`RegisterFolder`、`RegisterURL`、`ResolveURL`、`Verify`、`Delete`、`BrowseDir`、`CopyFile`、`ReadFile`、`MaxUploadBytes`、`GetMeta`、`GetMetaByCID`、`ImportGatewayData`、`RegisterBTFile`、`ListAll`；`ErrStorageDisabled`、`ErrFileAlreadyExists` |
| pin_service.go | IPFS pin 用例层（M2 收编） | `PinService`：`Get`、`Insert`、`Remove`、`List`、`InsertMeta` |
| share_service.go | 分享链接用例层（M2 收编） | `ShareService`：`Create`（30 天）、`GetByToken`、`List` |
| sync_service.go | 集合同步到本地磁盘：过滤/状态跟踪 | `SyncService`：`SaveToDisk`、`GetStatus` |
| task_service.go | 异步任务占位（transfer_tasks 表） | `TaskService`：`Create`、`UpdateStatus`、`Get` |

## 关键机制

### 1. FileService 的存储模型

上传/注册统一收敛到**内容寻址存储**（CAS）布局 `storageDir/{hash[:2]}/{hash}`：

```
Upload（file_service.go:444）：
  multipart reader → TeeReader 边写临时文件边算 sha256 → os.Rename 到 CAS
  （EXDEV 跨设备 fallback copyFile）→ InsertFileMeta + InsertFileProvider("local", relPath)
  → 已存在返回 ErrFileAlreadyExists（controller 转 200 already_exists:true）
```

- **防重复写**：`GetFileMeta(hash)` 存在即跳过 InsertFileMeta（RegisterLocal:212、
  RegisterURL:365）
- **临时文件**：`os.CreateTemp` + defer Remove，写完 rename 原子落位（Upload:454-499）
- **MIME 嗅探**：`http.DetectContentType` 前 512 字节，octet-stream 时按扩展名回退
  （RegisterLocal:198-205、Upload:472-480）

### 2. 安全边界：isPathInStorage（file_service.go:128-151）

`register_local/register_folder/browse/copy/delete` 都接受调用方路径，是历史上
「匿名任意文件读写删」漏洞源（F2/F3/H1/H6，见测试节）。统一防御：

```go
isPathInStorage(absPath):
  storageDir → filepath.Abs + EvalSymlinks（防符号链接逃逸）
  absPath   → filepath.Abs + EvalSymlinks
  filepath.Rel(root, abs) → 必须 "." 或 不含 ".." 前缀
```

与 `transport.FileIndexService.IsPathAllowed`（file_index.go:53）同一模式。
`Delete` 再叠一层 hash 校验（isValidHash）——防历史数据里根外 provider 路径被回读删除
（file_service.go:551-556 注释）。

### 3. AnonService 的内容寻址集合

匿名集合 = 磁盘上的 JSON 文件，hash = JSON 内容的 sha256（anon_service.go:101-133）：

- **验证**：条目 path 必须相对（`isRelativePath` + 禁 `..`）；文件条目必须带合法
  providers（`isValidProviders`：sha256 64hex 或 http/https url）；目录条目（path 以 "/"
  结尾）免 providers
- **排序**：entries 按 path 排序后 marshal——**同内容必同 hash**（可寻址的前提）
- **版本**：`CommitCollection` 合并条目（空 providers = 删除）→ version+1 → 重新落盘；
  旧版本文件保留（hash 不同即新文件）
- **登记**：落盘同时 `InsertFileMeta`（Type=FileTypeAnonCollection）+ provider
- **读取防御**（GetCollectionByHash:143-146 注释）：hash 来自 URL/远端输入，未校验则
  `hash[:2]` 越界 panic + `filepath.Join` 逃逸 storage（H1，见测试节）

### 4. AuthService 的 bcrypt 细节

- **72 字节上限**（auth_service.go:28-32 注释 L1）：bcrypt 只取前 72 字节，超长密码尾部
  被静默忽略（截断熵损失）——超限直接拒绝
- **登录不区分错误**：用户不存在与密码错误都返回 `ErrInvalidCredentials`（防用户名枚举）
- **authkey**：32 随机字节 hex（generateAuthKey），每次 Login/Register 重新生成
  （旧 key 立即失效）

### 5. SyncService 的同步流水线

```
SaveToDisk（sync_service.go:31）：
  1. LocalPath 禁 ".."（路径穿越第一道）
  2. GetAnonCollectionByHash 读集合
  3. filterFiles（include/exclude 模式过滤）
  4. UpsertSyncState + ClearSyncFiles（DB 状态先清后写）
  5. 逐文件 saveFile：禁 ".." → MkdirAll → universalDownloader.Download →
     os.WriteFile → UpsertFileSyncState（单文件失败不中断，记 missing）
```

**matchPattern 的路径边界**（sync_service.go:176-208 注释 L2）：子串回退匹配必须有
路径边界——pattern 以 "/" 结尾=目录前缀匹配，否则子串前后必须是 '/' 或字符串端；
否则排除 "tmp/foo" 会误伤 "tmp/foobar"。

### 6. 上传限额与存储开关（MaxUploadBytes，file_service.go:743-748）

`MaxUploadBytes(c *gin.Context)` 按认证状态取 `cfg.MaxUploadBytes` /
`cfg.MaxUploadBytesAnon`（controller 侧配 `MaxBytesReader` 落地）。`storageEnable=false`
时（纯中继/纯索引节点）upload/register 系列全部拒绝（403），只允许登记 provider
（RegisterURL 仅写 "http" provider，不落盘）——存储开关是**全服务级**的，
不是每文件判断（file_service.go:32/158/235/389/448/546/576/644 六处入口统一检查）。

### 7. 注册与浏览的递归语义

- `RegisterFolder`（file_service.go:262-290）：递归遍历目录内全部文件逐个
  RegisterLocal（isPathInStorage 先拦根外）——同文件重复注册被 meta 去重跳过。
- `BrowseDir`（file_service.go:305-340）：`path==""` 或 `"/"` 都映射 storage 根
  （controller 层 400 修复的配套，见 controllers.md 测试节）；目录条目聚合
  file_meta 的 path 前缀，返回 `{dirs, files}` 两类清单。
- `DiffVersions`（file_service.go:378-426）：两个 version_id 的 entries 按 path
  比对——added（仅 A）/removed（仅 B）/modified（A≠B 且都在）三组；controller 层
  组合 `CollectionService.VersionEntries` 取数据。

### 8. 依赖注入方向（M2/M4 产物）

```
FileService  ← config（storageDir/storageEnable/MaxUploadBytes）
SyncService  ← SyncRepository + UniversalDownloader + storageDir（下载器注入）
AnonService  ← config（StorageDir）
AuthService  ← UserRepository
PinService / ShareService / TaskService / CollectionService ← 无依赖（内部引 repository）
```

## 与其它模块的关系

```
controller（调用方）
  ↓
service（本层）
  ├→ repository（SQLite：file_meta/file_providers/collections/users/pins/shares/tasks）
  ├→ downloader.UniversalDownloader（SyncService.saveFile 的取数；AnonService 不直接用——
  │   匿名集合下载走 controller 侧 universalDownloader）
  ├→ config（路径/开关/限额）
  └→ model（领域类型）
```

- **不 import transport**（LAYERS.md §3 规则 3）：节点互联语义（OpenStream/FetchFromPeer）
  由 controller 经 `transport.PeerJSService` 装配，service 不直接引用——唯一的例外面是
  `downloader` 与 `p2p_bt`（外部能力切面 ⑦），在 service 是被允许的下游。
- **repository 不再被 controller import**（M2 达成）：本层是 repository 唯一业务入口。
- **PinService/ShareService/TaskService 的收编语义**（M2）：三者的方法直接透传
  repository（PinRepository/ShareRepository/SyncRepository），无中间规则——
  它们是「依赖方向修正」而非「业务提炼」，新逻辑应优先落到 FileService/AnonService
  这类有规则的服务里。

## 坑与设计决策

1. **透明转发层的定位**（collection_service.go:1-5）：方法名与 repository 一一对应、
   零业务逻辑——这是 M2 的「收口」手段而非最终形态；缓存/事务留到未来在 service 里加，
   controller 无需感知。
2. **RegisterLocal 锚定 storage 根**（file_service.go:169-174 注释）：此前接受任意绝对
   路径 → 配合 LocalFetcher 的 provider 回读 = 匿名任意文件读取（F2）。register_folder/
   browse/copy 同款边界，一处漏即全链漏。
3. **Upload 的 EXDEV fallback**（file_service.go:492-498）：tmp 在 /tmp、storage 在别的
   挂载点时 os.Rename 报 cross-device link——fallback copyFile（含 Sync 持久化）。
4. **Delete 只删「storage 内的 local provider」**（file_service.go:557-564）：provider 路径
   必须过 isPathInStorage 才 os.Remove——历史上直接 os.Remove 任意 provider 路径 = 任意
   文件删（H1 同类）。
5. **匿名集合 hash 稳定性依赖排序**（anon_service.go:97-99）：entries 必须先 sort 再
   marshal；新增字段/改 marshal 格式会改变所有历史 hash（内容寻址的不可变契约）。
6. **GetAnonCollectionByHash 的版本门槛**：Version < 1 拒绝（anon_service.go:159-162）——
   防旧格式/半写文件被当作合法集合解析。
7. **authkey 单值覆盖**：每个用户只有一个有效 authkey（UpdateAuthKey 覆盖）——多端登录
   互相踢下线，设计如此（无多会话概念）。
8. **SyncService 用 fmt.Printf 记错误**（sync_service.go:67）：未走 log 包，属遗留瑕疵，
   非新代码模式。
9. **URL 注册不落盘当存储关闭**（RegisterURL:389-396）：storageEnable=false 时只登记
   provider（"http" 类型），下载时经 HTTPURLFetcher 直取；落盘失败仅 LogWarn 不致命。

## 测试

> 本层测试除明确标注发现背景的 5 个外均属 legacy 标注（文件头注释）。

| 文件 | 测试 | 发现背景 |
|---|---|---|
| anon_service_test.go | `TestCreateCollection_ValidEntries/PathTraversal/InvalidHash/EmptyEntries/EmptyPath/DirEntryWithoutProvider/FileWithoutProvider/AbsolutePath`、`TestGetCollection_ByHash/NonexistentHash`、`TestForkCollection`、`TestDownloadFile_FromCollectionEntry` | legacy（匿名集合创建/校验/读取主链路） |
| anon_service_test.go | `TestGetCollection_InvalidHashNoPanic` | **2026-08-16 传输层审阅 H1：GetCollectionByHash 未校验 hash 就 hash[:2] 切片——短 hash 越界 panic 杀进程，".." 类值逃逸 storage 目录**；修复：入口 isValidHash（anon_service.go:143-146） |
| file_service_test.go | `TestNewFileService`、`TestRegisterLocal(_NonexistentPath/_StorageDisabled/_EmptyFilename)`、`TestRegisterFolder`、`TestVerify(_Nonexistent)`、`TestDelete(_StorageDisabled)`、`TestRegisterURLDefaultFilename` | legacy |
| file_service_test.go | `TestRegisterLocalOutsideStorageRootRejected` | **F2：RegisterLocal 接受任意绝对路径 + LocalFetcher provider 回读 = 匿名任意文件读取（可读 /etc/shadow）**；修复：isPathInStorage 锚定根 |
| file_service_test.go | `TestCopyFileOutsideStorageRootRejected` | **F3：CopyFile 目标可写任意位置（绝对路径原样 / ../ 逃逸），配合公开 upload 可写 authorized_keys**；修复：写盘前校验目标在 storage 根内 |
| file_service_test.go | `TestDeleteInvalidHashRejected` | **H1 同类：Delete 对任意 hash 直接 os.Remove provider 路径**；修复：先校验 hash 再确认路径在根内 |
| file_service_test.go | `TestBrowseDirOutsideStorageRootRejected` | **H6：BrowseDir 接受任意绝对路径 → 任意目录列举（任意文件读取的前提）**；修复：锚定 storage 根 |
| sync_service_test.go | `TestSyncService_PathTraversal`（ValidPath/TraversalAttempt/ContainDotDot）、`TestSyncService_Filtering`（All/ExcludeOne/IncludeOne/IncludeAndExclude） | legacy（SaveToDisk 路径穿越防御 + include/exclude 过滤语义） |

## 文件清单

```
back/internal/service/
├── anon_service.go       匿名集合（内容寻址 JSON 存储 + 版本提交）
├── anon_service_test.go  匿名集合测试（含 H1 发现背景 1 条）
├── auth_service.go       bcrypt 认证（72 字节上限 / authkey）
├── collection_service.go 用户集合透明转发层（M2）
├── file_service.go       文件主服务（上传/注册/删除/复制/浏览 + 安全边界）
├── file_service_test.go  文件服务测试（含 F2/F3/H1/H6 发现背景 4 条）
├── pin_service.go        IPFS pin 收编层（M2）
├── share_service.go      分享链接收编层（M2）
├── sync_service.go       本地同步（过滤 + 状态跟踪 + 路径防御）
├── sync_service_test.go  同步测试（路径穿越/过滤）
└── task_service.go       异步任务占位（M2）
```