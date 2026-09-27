# 模块 08：downloader 多协议下载器

- **代码位置**：`back/internal/downloader`（`universal_downloader.go`，测试 `universal_downloader_test.go`）
- **功能一句话**：对同一个 64 位 SHA-256 hash，按配置的优先级顺序依次尝试 local（本地内容寻址存储）→ ipfsgw（IPFS HTTP 网关）→ btdht（BitTorrent Mainline DHT HTTP 桥）→ http（`file_providers` 中登记的 URL）四种协议下载；第一个字节级校验通过者胜出，成功后把数据缓存进本地存储并登记元数据/来源，最终以 `(data []byte, protocol string, err error)` 返回给调用方（`back/internal/downloader/universal_downloader.go:1-14, 303-367`）。
- **依赖**：
  - `peerdrive/internal/repository`（直连，不走 service 收层）：`GetFileProviders` / `InsertFileMeta` / `InsertFileProvider` / `MarkProviderUnavailable`（`back/internal/repository/file_repo.go:48-93`）。
  - `peerdrive/internal/model`：`FileMeta` / `FileProvider` 结构体与 `FileTypeBlob` 常量（`back/internal/model/file.go:10-32`）。
  - `peerdrive/internal/provider`：`IPFSProvider`（ipfsgw 抓取；`back/internal/provider/ipfs.go:34-48, 124-166`）。
  - `github.com/Hana-ame/go-peerdrive-bt`（本地模块 `back/p2p_bt`）：`BTDHTService` / `BTBridge`（`back/p2p_bt/bt_bridge.go:19-33, 57-115`）。
  - `peerdrive/pkg/hashutil`：`IsStrictSHA256`（下载入口校验）、`SHA256ToCID`（ipfsgw 的 hash→CID 转换、落库自动算 cid）（`back/pkg/hashutil/hashutil.go:27-41, 62-73`）。
  - 标准库：`crypto/sha256`+`encoding/hex`（校验）、`net/http`（http fetcher）、`os`/`path/filepath`（CAS 读写）、`sync`（metrics 互斥）、`time`。
- **被依赖**：
  - `back/internal/controller/download.go`：包级全局 `universalDownloader`（31-37），`DownloadBySHA256Internal`（60）、`UniversalDownload`（219）、`UniversalDownloadSources`（244）、`UniversalDownloadRefresh`（261, 265）分别调用 `Download` / `CheckSources` / `ClearLocalCache`。
  - `back/internal/controller/anon.go:186`：匿名集合文件下载（`DownloadAnonFile`）调用 `Download`。
  - `back/internal/service/sync_service.go:16-19, 132`：`SyncService` 持有同一实例，`saveFile` 用它拉取集合内文件。
  - `back/internal/router/router.go:199-213, 233-236`：唯一构造点 + 注入 controller/SyncService + 注册 `/download/*` 路由。
  - 测试：`back/internal/downloader/universal_downloader_test.go`。

## 1. 逻辑

**核心职责**：多协议回退下载管线。抽象为 `ProtocolFetcher` 接口 —— `Name() / Fetch(ctx, hash) ([]byte, error) / IsAvailable()`（`universal_downloader.go:55-61`），当前有 4 个实现：

| 协议 | 实现类型 | Fetch 行为 | IsAvailable | 代码位置 |
|---|---|---|---|---|
| `local` | `LocalFetcher` | ① 按两个内容寻址路径读盘：`<storageDir>/<hash[:2]>/<hash>`、`<storageDir>/p2p/<hash[:2]>/<hash>`；② 都未命中则查 DB `file_providers` 中 `available` 的 `local` 条目，按其 `Path` 读盘（相对路径拼接 `storageDir`） | `storageDir != ""` | `universal_downloader.go:68-107` |
| `ipfsgw` | `IPFSGatewayFetcher` | `hashutil.SHA256ToCID(hash)` 转 CIDv1 → `IPFSProvider.FetchByCID`（Bitswap 回调优先，否则多网关并发竞速，每网关指数退避重试 ≤3 次；client 超时 30s） | `provider != nil && len(Gateways) > 0` | `universal_downloader.go:205-221`；`back/internal/provider/ipfs.go:41-48, 124-166, 170-187` |
| `btdht` | `BTDHTFetcher` | `BTBridge.FetchFile`：DHT `FindProviders(hash)` 找到对端地址后逐个 `http://<peerAddr>/files/<hash>` GET（client 超时 15s） | `dhtSvc != nil && dhtSvc.Server != nil` | `universal_downloader.go:115-136`；`back/p2p_bt/bt_bridge.go:57-115` |
| `http` | `HTTPURLFetcher` | 查 DB `file_providers` 的 `http` 条目，`GET p.Path`（带 ctx；client 超时 = `d.timeout`；`io.LimitReader` 限量） | 恒 `true` | `universal_downloader.go:144-197` |

**主流程 `Download(ctx, hash) (data []byte, protocol string, err error)`**（`universal_downloader.go:305-367`）：

1. 入口防御：`IsStrictSHA256(hash)` 严格校验（仅 64 位小写 hex）；非法直接报错。注释明确这是针对 anon collection entry 远端输入的防线——绕过校验进 `LocalFetcher` 会触发 `hash[:2]` 越界 panic（306-310）。
2. 初始化本轮 `metrics`，`defer` 写回 `lastMetrics`（311-316）。
3. 按 `fetchers` 顺序逐个尝试：
   - 不可用 → 记 `{success:false, error:"not available"}` 跳过（319-327）；
   - 可用 → `context.WithTimeout(ctx, d.timeout)` 限定单次尝试总时长（330）→ `Fetch`；
   - 失败 → 记 `{success:false, error:err}` 继续下一协议（358-364）；
   - 成功 → 重算 SHA-256 与入参比对，不符记 `"hash mismatch"` 并按失败看待继续（337-347）；相符 → `cacheToLocal` 落缓存（349）→ 记成功 metric → `return data, fetcher.Name(), nil`（351-356）。
4. 全部协议失败 → 返回错误 `"file not found on any protocol"`（366）。成功/失败的协议与耗时都打日志（350, 358）。

**fetchers 构建 `buildFetchers(order, btSvc)`**（259-301）：`order` 是逗号分隔字符串，解析后 trim、去空；空串时用默认 `local,ipfsgw,btdht,http`（271-273）；registry 只认 `local/ipfsgw/btdht/http` 四个名字，未知名字 `LogWarn` 跳过（275-299）。`http` fetcher 注入 `&http.Client{Timeout: d.timeout}`（286-289）。

**辅助方法**：

- `LastMetrics()`（370-374）：互斥返回最近一次 `Download` 的各协议尝试记录；当前仓库内无调用方（仅测试/诊断预留，304 注释）。
- `CheckSources(ctx, hash) map[string]bool`（405-425）：对 `local`/`http` 真实调用 `Fetch` 探测文件存在性；对 `ipfsgw`/`btdht` 直接回 `true`（能力声明，不探测）。
- `ClearLocalCache(hash)`（428-447）：删除两个 CAS 路径的文件 + 把 DB 中 `available` 的 `local` provider 标为不可用，使下次下载重走网络。
- `Fetchers()`（450-452）：暴露按优先级排序的 fetcher 切片（测试用）。
- `cacheToLocal(hash, data)`（378-398）：写盘 + 落库，见 §2/§3。

**生命周期**：进程启动时由 `router.SetupRouter` 构造一次（`router.go:199-208`），以 controller 包级全局变量 + `SyncService` 字段的方式常驻整个进程；无关闭/析构逻辑（下载器自身不持有后台常驻 goroutine，IPFSProvider 网关竞速的 goroutine 以 channel 收齐即退，`ipfs.go:73-111`）。

## 2. 如何储存

模块自身不建表、不独占存储介质，写入落在两个下级存储上，另有一块纯内存态：

**(a) 文件系统 · 内容寻址存储（CAS）**（写：`cacheToLocal`；读：`LocalFetcher`；删：`ClearLocalCache`）
- 主布局：`<storageDir>/<hash[:2]>/<hash>`（`universal_downloader.go:379-385`；读候选 `:79`；p2p_bt 侧 `BTBridge.FilePath`/`EnsureFileWritten` 同一布局，`back/p2p_bt/bt_bridge.go:129-143`）。
- 读取时额外兼容历史布局 `<storageDir>/p2p/<hash[:2]>/<hash>`（`:80`）。
- 权限：目录 `0755`、文件 `0644`（`:381-385`）。
- `storageDir` 来自配置 `cfg.StorageDir`（默认 `./storage`，`back/internal/config/config.go:29, 191`）。
- 写入是裸 `os.MkdirAll` + `os.WriteFile`，不经 FileService / pathutil 边界判定（属历史实现，见 §5 坑点）。

**(b) SQLite 数据库（委托 repository）**（库路径 `cfg.DBPath`，默认 `./peerdrive.db`，`back/internal/config/config.go:27, 190`；DDL 见 `back/internal/repository/db.go:133-150`）
- `file_meta` 插入一行（hash 为主键）：`cacheToLocal` → `repository.InsertFileMeta`（`universal_downloader.go:390-396`；`file_repo.go:48-55`，自动计算 `cid`）。
- `file_providers` 插入一行 `local`：`repository.InsertFileProvider(hash, "local", relPath)`（`universal_downloader.go:397`；`file_repo.go:81-87`）。
- 读取也走 repository：`GetFileProviders` 只取 `available = 1` 且 `local` 优先排序（`file_repo.go:60-78`）。
- 委托边界：本模块**跨过 service 层直连 repository**（不是经 FileService 收层落库）。

**(c) 纯内存态（不持久化）**
- `fetchers []ProtocolFetcher`：构造时按 `DownloadOrder` 配置构建的顺序表（`universal_downloader.go:229-239, 259-301`）；进程重启后由配置重建。
- `storageDir` / `timeout` / `ipfsProvider` 字段（229-233）。
- `metricsMu sync.Mutex` + `lastMetrics []FetcherMetric`（235-238）：最近一次 `Download` 的协议尝试记录，每次下载被覆盖（312-316）；进程重启后清空（初始为 nil）。

## 3. 何时储存

- **进程启动（内存态初始化，不落盘）**：`router.SetupRouter` 内 `NewUniversalDownloader(btSvc, cfg.StorageDir, cfg.DownloadOrder, timeout, ipfsProv)`（`back/internal/router/router.go:199-208`）——一次构建 fetchers 顺序表，随后注入 controller（`router.go:208`）与 `SyncService`（`router.go:212`）。BTDHT 服务按 `cfg.BTDHTEnabled` 先建（`router.go:137-145`），IPFSProvider 按 `cfg.IPFSGatewayEnable` + 网关列表先建（`router.go:182-197`）。
- **每次 `Download` 成功且哈希校验通过（唯一的写盘 + 写库时机）**：`Download` 内成功分支调用 `cacheToLocal(hash, data)`（`universal_downloader.go:349`），其中 `os.MkdirAll`+`os.WriteFile` 写 CAS 文件（381-388）、`InsertFileMeta`（390-396）、`InsertFileProvider`（397）。注意：**即使命中 local 协议，也会重写一遍文件并再插入一条 provider 行**（349 无条件执行）。
- **每次 `Download` 结束（无论成败）**：`defer` 把本轮 metrics 原子写入内存 `lastMetrics`（312-316）。
- **`POST /download/:hash/refresh` 请求**：先 `ClearLocalCache`（删两个 CAS 路径文件 + DB 中 local provider 标 `available=0`，`universal_downloader.go:428-447`），再重新 `Download`（`download.go:261-265`）。
- **读取触发点（每次调用实时查库）**：`GetFileProviders` 在 `LocalFetcher.Fetch`（`:90`）、`HTTPURLFetcher.Fetch`（`:160`）、`ClearLocalCache`（`:441`）中执行。
- **无定时任务、无优雅关闭存储动作**：下载器没有独立关闭路径（模块内不存在 `Close`/shutdown 挂钩）。

## 4. 储存什么

**CAS 文件条目**（写于 `cacheToLocal`，`universal_downloader.go:378-398`）：

| 项 | 值 | 约束/默认 |
|---|---|---|
| 相对路径 | `<hash[:2]>/<hash>` | hash 为 64 位小写 hex（`IsStrictSHA256` 前置保证，`:308`） |
| 内容 | 原始文件字节（明文，非 gzip） | 登记 `gziped=false`；http 协议另有 8GB 上限（`:153`） |
| 权限 | 目录 0755 / 文件 0644 | `:381-385` |

**`file_meta` 行**（`cacheToLocal` 390-396 → `file_repo.go:48-55`；建表 `db.go:133-141`）：

| 列 | 值 | 说明 |
|---|---|---|
| `hash` | 下载的 64 位 hex | 主键（`db.go:134`） |
| `size` | `int64(len(data))` | — |
| `mime_type` | `''`（未设置） | 列默认 `''` |
| `gziped` | `0` | `FileMeta.Gziped=false` 显式传入 |
| `filename` | hash 本身 | 即以哈希原文作文件名（392-395） |
| `type` | `'blob'`（`model.FileTypeBlob`） | `file.go:11` |
| `cid` | 自动 `hashutil.SHA256ToCID(hash)` | `file_repo.go:49` |
| `created_at` | DB 默认 `CURRENT_TIMESTAMP` | `db.go:136` |

**`file_providers` 行**（`:397` → `file_repo.go:81-87`；建表 `db.go:143-149`）：`hash`、`provider_type='local'`、`path='<hash[:2]>/<hash>'`（相对路径）、`available=1`（DB 默认）。重复缓存**每次追加新行**（`InsertFileProvider` 无去重；`InsertFileMeta` 因 hash 主键冲突被忽略 `_=` 而幂等）——长时间重复下载同一 hash 会累积多余 provider 行（`GetFileProviders` 按 `local 优先 + id ASC` 取第一条生效，`file_repo.go:62`）。

**内存 `lastMetrics`**（`FetcherMetric`，`:44-49`；每次 `Download` 覆盖，312-316）：每条 `{name, duration(ns), success, error(omitempty)}`，条数 ≤ `len(fetchers)`，含 `"not available"` / `"hash mismatch"` / 实际错误；互斥读写（235-238, 370-374）。

**清缓存副作用**（`ClearLocalCache` 428-447）：删除 `<storageDir>/<hash[:2]>/<hash>` 与 `<storageDir>/p2p/<hash[:2]>/<hash>` 两个文件；对应 `local` provider 行 `available` 置 0（**不删除行**）。

## 5. 边界与坑

- **校验双防线**：`Download` 用严格小写校验 `IsStrictSHA256`（`hashutil.go:62-73`）压住 sync/serve 路径的远端输入（306-307 注释）；controller 侧另有 `IsValidSHA256`（允许大写，`hashutil.go:16-23`）前置——两处语义不同，合起来保证 `hash[:2]` 切片不越界、查找不下沉到大写表。
- **超时体系**：per-fetcher 超时 `d.timeout`（`:330`）= `cfg.DownloadTimeoutSecs`（env `PEERDRIVE_DOWNLOAD_TIMEOUT`，默认 30s，`config.go:97, 236`）；http fetcher 另加 client 超时 `d.timeout`（`:288`）；BT 桥内部 client 15s（`bt_bridge.go:81`）；IPFSProvider client 30s + 网关级退避重试 ≤3 次（`ipfs.go:24, 170-187`）。
- **http 协议 8GB 上限**（`:153`）：`io.LimitReader(maxURLFetchSize+1)` 读取，超限记日志并按坏 provider 跳过（185-193）——防恶意/失控 URL 返回无限流。
- **哈希不匹配 ⇒ 视为该协议失败并继续下一协议**（337-347）：错误数据不落缓存、不返回给调用方。
- **缓存尽力而为**：`cacheToLocal` 写盘/建目录失败只 `LogWarn`（381-388），`Download` 成功分支不检查其返回值——缓存失败不影响本次下载成功。
- **配置遗留坑**：`config.go:235` 默认 `DownloadOrder = "local,ipfs,ipfsgw,btdht,http"`，其中 `"ipfs"` 不在 registry（275-290），会被 `LogWarn` 跳过（297）——实际默认生效顺序为 `local→ipfsgw→btdht→http`（与空 order 的默认一致，271-273）。文件头注释（10-11）说明 IPFSFetcher 已于 2026-08-16 批2 随 libp2p 栈删除，配置里的 `ipfs` 为遗留项。
- **btdht 默认不可用**：`PEERDRIVE_BT_DHT_ENABLE` 默认 `false`（`config.go:200`）→ `btSvc` 为 nil → `IsAvailable=false`（130-132, router.go:138-145）；即使启用，`NewBTDHT` 失败也只 `LogWarn` 继续（143-144）。
- **CheckSources 语义**（405-425）：对 `ipfsgw`/`btdht` 恒回 `true`（能力声明而非文件存在性）；对 `http` 会真实发起 GET，慢 URL 会让 sources 端点一起变慢（controller 给它 30s context，`download.go:241`）。
- **metrics 竞态**（235-238 注释）：`Download` 并发写与读之间用 `metricsMu` 互斥；注释提到 `/download/:hash/sources` 高频读——当前实现 sources 走 `CheckSources` 并不读 `lastMetrics`（`LastMetrics` 现无调用方）。
- **repository 直通**（90-105）：`LocalFetcher` 读 DB 中 `local` provider 的 `Path` 时不做 pathutil 边界判定，路径来自其他模块登记，若库被污染可读到任意路径文件（读端起信任 DB）。
- **与仓库最新写路径硬性要求不一致（未核实是否已有迁移计划）**：`cacheToLocal` 仍裸 `os.MkdirAll`+`os.WriteFile`（381-385）、`ClearLocalCache` 裸 `os.Remove`（435-437），未走 AGENTS.md §11.5（2026-09-20 起）要求的 `pathutil.Safe*Any`/`os.Root`。hash 本身受严格校验不含分隔符，但写/删/读未过允许根判定。
- **BT 桥假设**（`bt_bridge.go:79-84`）：假定 DHT 发现的对端在 DHT 监听端口（或相邻 HTTP 端口）提供 `/<hash>` 拉取服务，否则 `FetchFile` 全部失败回错（112-114）。

## 6. 对外连接

- `../connections/09-controller-downloader.md`：controller → downloader 主通道。`back/internal/controller/download.go` 的 `/download/:hash`、`/download/:hash/sources`、`/download/:hash/refresh` 与 `/sha256sum/:sha256` 内部路径分别驱动 `Download` / `CheckSources` / `ClearLocalCache`，并把命中的协议名写进 `X-Protocol` 响应头（`download.go:60-65, 219-226, 244, 261-265`）。
- `../connections/02-router-controller.md`：router 装配层。`SetupRouter` 按配置构造 `UniversalDownloader` 并注入 controller（`router.go:199-208`），随后注册 `/download/*` 路由（233-236）。
- `../connections/03-controller-service.md`：service 层复用同一实例。`SyncController → SyncService.saveFile → downloader.Download` 拉取集合内文件（`sync_service.go:16-19, 130-135`）。
- `../connections/04-service-repository.md`：持久化委托。downloader 跨过 service 层**直连 repository** 读写 `file_meta` / `file_providers`（`file_repo.go:48-93`；downloader 侧 378-398, 441-445）。
- `../connections/01-frontend-backend.md`：前端正常文件下载走 `req` verb（transport `serveFile`/FileRouter，非 downloader），不进本模块；downloader 对前端只经本地 WS admin 帧转发后的 anon/sha256 端点间接可达（`router.go:215-223` legacy HTTP 路由区说明；`front/src/api.js:290-293` anon 下载即 `ws.admin('GET', '/anon/...')`，后端落到 `anon.go:186` 的 `Download`）。