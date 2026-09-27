# 连接 09：controller ↔ downloader（多协议下载）

- **涉及模块**：`../modules/05-controller.md` 与 `../modules/08-downloader.md`
- **代码位置**：A 侧 `back/internal/controller/download.go`；B 侧 `back/internal/downloader/universal_downloader.go`；唯一装配点 `back/internal/router/router.go:199-213`
- **方向**：A→B 单向（进程内同步函数调用，无网络通道/无消息队列；数据与结论经返回值三件套 `(data []byte, protocol string, err error)` 回流，`universal_downloader.go:305`）

## 1. 连接方式

**通道类型：进程内函数调用**。controller 侧持有包级全局指针 `var universalDownloader *downloader.UniversalDownloader`（`back/internal/controller/download.go:31-32`），由 router 在 `SetupRouter` 内一次性注入：

- **接入点**：`router.SetupRouter` 按配置调用 `downloader.NewUniversalDownloader(btSvc, cfg.StorageDir, cfg.DownloadOrder, downloadTimeout, ipfsProv)` 构造单例（`back/internal/router/router.go:201-207`），随后 `controller.InitUniversalDownloader(uniDownloader)` 注入（`router.go:208`）。同一实例再注入 `SyncService` 复用（`router.go:211-213`；`back/internal/service/sync_service.go:16-19, 132-135`），进程生命周期内常驻。
- **依赖的兄弟注入**：`InitIPFSProvider`（ipfsgw 协议的网关源，可为 nil，`download.go:41-43`；装配于 `router.go:183-197`）；BT DHT 服务按 `cfg.BTDHTEnabled` 前置构建（`router.go:137-145`）。
- **协议帧/参数格式**：无自有协议帧，全部为普通函数签名：
  - `Download(ctx context.Context, hash string) (data []byte, protocol string, err error)` —— 主调用（`universal_downloader.go:305-367`）；
  - `CheckSources(ctx, hash) map[string]bool` —— 源可用性探测（`:405-425`）；
  - `ClearLocalCache(hash)` —— 强制清缓存重拉（`:428-447`）；
  - `LastMetrics() []FetcherMetric` —— 最近一次尝试记录（`:370-374`）。
  - 各协议后端统一实现 `ProtocolFetcher` 接口：`Name() / Fetch(ctx, hash) ([]byte, error) / IsAvailable()`（`:57-61`）。
- **鉴权方式**：本连接为进程内调用，**无自身鉴权**；鉴权全部落在 HTTP 入口与上游：`POST /download/:hash/refresh` 挂 `AuthRequired`（`router.go:236`；中间件 `router.go:112-119`），读端点放开；匿名集合下载在触达下载器前先过 `anonSvc.GetCollectionVisibleTo` 可见性闸门（`back/internal/controller/anon.go:163-167`）。token 校验语义见 module 05 §5。
- **多协议回退优先级**：`local → ipfsgw → btdht → http`（`universal_downloader.go:3-8` 头注释；空 order 时的默认与之一致，`:271-273`）。注意配置默认值 `PEERDRIVE_DOWNLOAD_ORDER="local,ipfs,ipfsgw,btdht,http"`（`back/internal/config/config.go:235`）中 `"ipfs"` 不在 fetcher registry，会被 LogWarn 跳过（`universal_downloader.go:275-299`），实际生效顺序仍为 `local→ipfsgw→btdht→http`。
- **何时/由谁建立**：进程启动时由 router 建立一次（无惰性初始化、无关闭析构；模块自身不持有常驻 goroutine，IPFS 网关竞速 goroutine 以 channel 收齐即退，`back/internal/provider/ipfs.go:73-93`）。

## 2. 时序

### 2.1 正常路径主时序（`GET /download/:hash`）

```mermaid
sequenceDiagram
  participant C as 客户端(HTTP)
  participant R as router(gin 路由分发)
  participant K as controller UniversalDownload
  participant D as UniversalDownloader
  participant F as ProtocolFetcher(local/ipfsgw/btdht/http)
  participant DB as repository(file_providers/file_meta)
  participant FS as CAS 存储

  C->>R: GET /download/:hash
  R->>K: router.go:234 → download.go:207
  K->>K: IsValidSHA256 校验 (download.go:208-212) ; nil 注入拦截 (213-216)
  K->>D: Download(ctx, hash) (download.go:219)
  D->>D: IsStrictSHA256 再校验 (universal_downloader.go:308-310)
  loop 按优先级顺序逐个 fetcher (318-331)
    D->>D: IsAvailable()? 否→记"not available"跳过 (319-327)
    D->>F: context.WithTimeout(ctx, d.timeout) → Fetch(ctx, hash) (330-331)
    alt Fetch 成功
      D->>D: 复算 SHA-256 比对 (336-347)
      alt 哈希不符
        D->>D: 记"hash mismatch"，视为失败继续下一协议
      else 哈希相符
        D->>FS: cacheToLocal: 写 storage/<hash[:2]>/<hash> (349, 378-398)
        D->>DB: InsertFileMeta + InsertFileProvider("local") (390-397)
        D-->>K: return data, fetcher.Name(), nil (351-356)
      end
    else Fetch 失败/超时
      D->>D: 记 metric 继续下一协议 (358-364)
    end
  end
  D-->>K: err "file not found on any protocol" (366)
  K-->>C: err→404 {error} (download.go:220-223) ；成功→X-Protocol 头 + 200 octet-stream (225-226)
```

### 2.2 逐步骤说明

1. **路由分发**：`SetupRouter` 注册 `r.GET("/download/:hash", controller.UniversalDownload)`（`router.go:234`）；handler 入口 `download.go:207-227`。
2. **入参数校验**：`IsValidSHA256(hash)` 不合法 → 400 `invalid sha256`（`download.go:208-212`）；`universalDownloader == nil`（未注入）→ 503 `universal downloader not initialized`（213-216）。
3. **主调用**：以 `c.Request.Context()` 为 ctx 调 `universalDownloader.Download(ctx, hash)`（`download.go:219`）。下载端点本身不给 ctx 加整体超时，总耗时上限由下游 per-fetcher 超时逐段兜底。
4. **Download 入口防御**：`IsStrictSHA256` 严格校验（仅 64 位小写 hex，`universal_downloader.go:308-310`）——针对 anon collection entry / sync 路径远端输入的最后一层防线，防止非法 hash 进入 `LocalFetcher` 触发 `hash[:2]` 越界 panic（注释 :306-307）。
5. **多协议循环**：按 `fetchers` 顺序逐个尝试（`:318`）。`IsAvailable()==false` → 记 `{success:false, error:"not available"}` 跳过（319-327）；可用 → `context.WithTimeout(ctx, d.timeout)` 限定单次尝试时长（330）→ `Fetch`（331）。
6. **字节级校验与落缓存**：Fetch 成功必须重算 SHA-256 与入参比对（336-347）：不符记 `"hash mismatch"` 按失败看待并继续下一协议；相符 → `cacheToLocal` 写 CAS + 登记元数据/来源（349；378-398）→ 记成功 metric → `return data, fetcher.Name(), nil`（351-356）。
7. **失败汇聚**：全部协议失败 → 返回 `"file not found on any protocol"`（366）。
8. **响应组装**：controller 拿到 err → 404 `{error: err.Error()}`（`download.go:220-223`）；成功 → 写 `X-Protocol` 响应头（协议名，225）→ `200 application/octet-stream` 输出全文（226）。

### 2.3 变体入口（同一管线，不同前置/后置）

- **`DownloadBySHA256Internal`（多协议版，`download.go:51-94`）**：校验 hash（52-55）→ `universalDownloader != nil` 才走多协议（58），nil 时落 404（93）→ 成功后依次设置 `X-Protocol`（65）、`fileSvc.GetMeta` 补充文件名（`meta.Filename` 为空退回 hash，67-71）、`Content-Disposition`（`inline=1` 参数切换 inline/attachment，72-76）、`Content-Encoding: gzip`（meta.Gziped，77-79）、`X-Peerdrive-Collection: true`（匿名集合类型，80-82）；随后处理 `Range`（84-88，逻辑在 `handleRangeRequest` 171-201：不变式/后缀式/开放式 range、0 字节整包、不可满足 416）→ 200 输出（89）。
  - ⚠️ 路由实际注册：`/sha256sum/:sha256` 注册的是**本地直读版** `DownloadBySHA256Local`（`router.go:229-230`；handler `download.go:97-129`，不含 P2P 回退）。多协议版 `DownloadBySHA256`（46-48）与 `DownloadBySHA256Internal` 未挂在公开路由上，由内部路径复用：`/ipfs/:cid` 的 `DownloadByCID` 命中本地 meta 后（`download.go:134-162`，尤其是 151-152、161）与匿名集合文件下载（`anon.go:183-201`）。下载控制器头注释（`download.go:5`）仍写「/sha256sum 含多协议回退」，与注册实际不符（module 05 §5 已记录）。
- **匿名集合文件 `DownloadAnonFile`（`anon.go:159-209`）**：先 `GetCollectionVisibleTo` 可见性闸门（163）→ 按 entry 的 `GetPrimaryHash()` 调 `universalDownloader.Download`（183-186）→ 成功回 `X-Protocol` + `Content-Disposition` + mime（188-199）；下载器失败则**回退 URL provider**：`c.Redirect(302, p.Value)`（204-209）。
- **源探测 `GET /download/:hash/sources`（`download.go:230-246`）**：给 ctx 挂 30s 超时（241-242）→ `CheckSources`（244）：`local`/`http` 真实 Fetch 探测，`ipfsgw`/`btdht` 恒回 `true`（能力声明，`universal_downloader.go:412-422`）。
- **强制刷新 `POST /download/:hash/refresh`（`download.go:249-273`，挂 authRequired）**：先 `ClearLocalCache(hash)` 删两个 CAS 路径文件并置 `local` provider 不可用（261；`:428-447`）→ 重新走 `Download`（265）——cache 命中被清除后管线强制回源。
- **SyncService 复用（与本连接并列的第三条调用方）**：`syncSvc.saveFile` 以相同实例 `Download` 拉取集合内文件（`sync_service.go:132-135`），命中即回写本地缓存——与 HTTP 下载共享同一套时序。

## 3. 情况处理

| 异常/边界场景 | 行为与依据（代码位置） | 说明 |
|---|---|---|
| **超时** | 每协议独立超时：`context.WithTimeout(ctx, d.timeout)`（`universal_downloader.go:330`），`d.timeout = cfg.DownloadTimeoutSecs`，默认 30s（`config.go:97, 236`；`router.go:200`）；超时按普通失败记 metric 继续下一协议（`:358-364`）。http fetcher 另加 `http.Client{Timeout: d.timeout}` 防单请求悬挂（`:288, 164-167`）；BT 桥 client 15s（`back/p2p_bt/bt_bridge.go:81`）；IPFS 网关 client 30s + 每网关指数退避重试 ≤3 次（`provider/ipfs.go:24-29, 44-46, 170-187`）。`/download/:hash/sources` 端点给 ctx 30s 总限（`download.go:241-242`）。 | 下载主端点无显式总超时，靠 per-fetcher 逐段兜底；全部超时后 404（366 → `download.go:220-223`）。 |
| **断连 / 重连** | 无长连接，每次请求重新建连。URL provider 连接失败 → `client.Do` err → log 并继续下一 provider（`universal_downloader.go:176-179`）；BT peer 逐个尝试，单个失败换下一个（`bt_bridge.go:95-113`）；IPFS 网关整体竞速 + 退避重试（`ipfs.go:81-93, 170-187`）。「重连」语义 = 重新发起 `Download`；`POST /download/:hash/refresh` 可先 `ClearLocalCache` 强制回源（`download.go:261-265`）。 | 断连只影响当前协议尝试，不影响管线推进；缓存命中（local）时无网络路径。 |
| **重复 / 并发** | 并发同 hash 下载是允许的、互不阻塞：每次 `Download` 独立循环（305-367）；`metrics` 读写用 `metricsMu` 互斥保护（235-238, 312-316, 370-374，M5 修竞态注释）。`cacheToLocal` 并发写同一路径为裸 `os.MkdirAll`+`os.WriteFile`（381-388），非原子；`InsertFileMeta` 主键冲突被忽略（`_ =`，390-396，幂等）；`InsertFileProvider` 每次追加新行（397）→ 长期重复下载同一 hash 会累积多余 provider 行（`GetFileProviders` 按 `local 优先 + id ASC` 取第一条生效，`file_repo.go:62`）。refresh 重复点击同语义无副作用。 | 正确性由「内容寻址 + 下载后校验」保证；积累的 provider 行是已知的存储副作用（module 08 §4）。 |
| **数据缺失或校验失败** | 缺失：`LocalFetcher` 两条 CAS 路径 + DB local provider 都未命中 → `local: file not found`（`universal_downloader.go:78-107`）；http provider 非 200 / 读取失败 / 超 8GB 上限均跳过（`:180-193`；上限 `:153`）。校验失败：`Download` 复算 SHA-256 与入参不符 → 记 `"hash mismatch"` **按失败处理继续下一协议，错误数据不落缓存不返回**（336-347）。缓存写盘/建目录失败只 `LogWarn`、不影响本次下载成功（381-388）。`fileSvc.GetMeta` 返回 nil → 文件名退回 hash 原文（`download.go:67-71`）。 | 校验双防线：controller `IsValidSHA256`（52-55）+ downloader `IsStrictSHA256`（308-310），语义不同、合起来保证 `hash[:2]` 不越界。 |
| **鉴权失败** | 本连接内部无鉴权概念；失败发生在触达下载器之前：`POST /download/:hash/refresh` 未带 token → 401（`router.go:236` + `AuthRequired`，112-119）；匿名集合不可见 → 404（等价不存在，`anon.go:163-167` 注释）；未配置 RegistrationServer 时 `AuthRequired` 本地放行（`auth_middleware.go:26, 57-61`，本地单机模式）。 | 读端点（`/download/:hash`、`/download/:hash/sources`、`/sha256sum`）公开，下载器对匿名调用者不做二次鉴权。 |
| **半开状态** | ① 注入缺位：`universalDownloader == nil` → `UniversalDownload`/`sources`/`refresh` 回 503（`download.go:213-216, 236-239, 255-258`），`DownloadBySHA256Internal` 则穿透回 404（58, 93）。② 实例在但底层不可用：`btdht` 的 `dhtSvc.Server == nil`（`universal_downloader.go:130-132`；`router.go:138-145`；`config.go:200` 默认禁用）、`ipfsgw` 的 provider 为 nil 或网关列表空（211-213；`router.go:183-197`）、`local` 的 storageDir 空（74）→ `IsAvailable()==false` 记 `"not available"` 直接跳过（319-327）。③ URL provider 已登记 available 但 URL 失效（半开）→ `Fetch` 逐条失败并续查（159-197）。 | 半开表现为「管线可用但某协议不可用」；下载器把不可用视为失败分支推进，最终 404/503 取决于入口。 |
| **进程重启** | 下载器为进程级单例，重启后由 `SetupRouter` 重建（`router.go:199-208`），controller 包级全局需重新 `InitUniversalDownloader`（208）——漏注入则按上一条目回 503/404。`lastMetrics` 为纯内存态，重启清空（312-316, 370-374）。已落盘 CAS 文件 + `file_meta`/`file_providers` 行持久保留，重启后 `local` 直接命中上次缓存（349, 378-398）。BT DHT 按 `cfg.BTDHTEnabled` 重新初始化（`router.go:137-145`），失败仅 LogWarn 继续（143-144）。 | 下载连接无「恢复会话」概念；重启即冷启动，缓存使热数据免回源。 |
| **未知协议配置** | `buildFetchers` 对 registry 外的名字 `LogWarn` 跳过（`universal_downloader.go:297`）；配置中遗留的 `"ipfs"` 即此路径（`config.go:235`）。 | 非法 order 不会 500，只退化为可用子集。 |

## 4. 相关文档

- 连接文档（同目录）：
  - [02-router-controller.md](02-router-controller.md)：router→controller 装配面。`SetupRouter` 构造 `UniversalDownloader` 并 `Init*` 注入 controller（`router.go:121-214`），注册 `/download/*` 与 `/sha256sum/*` 路由（229-236）；全局中间件（认证/限流/CORS）与 admin 帧内部转发（router.go:44-107, 396-415）。
  - [03-controller-service.md](03-controller-service.md)：service 复用同一下载器实例——`SyncController → SyncService.saveFile → downloader.Download` 拉取集合内文件（`sync_service.go:130-135`；装配 `router.go:211-213`）。
  - [04-service-repository.md](04-service-repository.md)：downloader **跨过 service 层直连 repository** 读写 `file_meta`/`file_providers`（`universal_downloader.go:90-105, 378-398, 441-445`；`file_repo.go:48-93`）——本连接的数据面打通依赖该委托边界。
  - [10-controller-storage.md](10-controller-storage.md)：CAS 落盘布局 `storageDir/<hash[:2]>/<hash>`（controller 取 `storageDir` 于 gin context，`router.go:75-79`；downloader 读写同一布局，`universal_downloader.go:379-397`）。
  - [01-frontend-backend.md](01-frontend-backend.md)：前端文件下载主路径走本地 WS `req` verb（transport `serveFile`/FileRouter），**不经 downloader**；downloader 对前端仅经本地 WS admin 帧转发后的 anon/sha256 端点间接可达（`router.go:215-223, 396-415`；`anon.go:186`）。
- 模块文档：`../modules/05-controller.md`（HTTP 处理器面；§4.4 下载顺序/超时约束、§4.3 响应头）、`../modules/08-downloader.md`（下载器内部逻辑、储存、边界与坑，含 btdht/ipfsgw/http 子协议细节）。