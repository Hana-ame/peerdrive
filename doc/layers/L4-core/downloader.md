# downloader 层（back/internal/downloader/）

> 层归属：AOP ④ 业务核心（见 doc/LAYERS.md §1）——多协议回退下载链路
> （LEGACY.md 第 51 行「核心下载链路，保留」）。
> 协议顺序（universal_downloader.go:3-8 头注释）：`local → ipfsgw → btdht → http`，
> 成功后缓存到本地存储，下次请求由 LocalFetcher 直接命中。

**一句话职责**：把「按 sha256 拿到文件内容」变成多协议回退流水线——每个协议一个
`ProtocolFetcher`，按配置顺序尝试、校验 hash、命中即缓存落盘；并向管理端点提供
各协议可用性检查与本地缓存清理。

## 职责

### 解决什么问题

一个文件可能同时存在于：本地 CAS 存储、IPFS 公共网关（按 CID）、BT DHT（peer 提供）、
某个已登记的 http URL。历史实现（libp2p 时代）是 controller 里一串 if-else 手写回退，
每次加协议都要改下载路径。本层把「回退」本身做成可配置的数据结构：

- `ProtocolFetcher` 接口（Name/Fetch/IsAvailable）——每协议一个实现，互不感知
- `buildFetchers(order)` —— 逗号分隔字符串声明优先级，未知协议名跳过并 LogWarn
- `Download` —— 统一入口：校验 hash → 依序尝试 → sha256 验证 → 缓存 → 返回
  `(data, protocol, err)`

### 与 source 层的分工（REFACTOR.md §3.8）

source 层是「多源路由」（流式、Range、运行时优先级调整），本层是「多协议回退」
（全量拉取、写缓存、DB provider 参与）。两者独立演进：
- controller 的 `/download/:hash` 走本层；`GET /sources` 走 source 层
- 本层 LocalFetcher 的 DB provider 回读是历史遗留能力（source 层 LocalSource 不读 DB）

## 模块清单

| 文件 | 一句话职责 | 关键导出 |
|---|---|---|
| universal_downloader.go | 多协议回退下载流水线（接口 + 4 个 fetcher + 下载器 + 缓存 + 源检查） | `ProtocolFetcher` 接口、`FetcherMetric`、`LocalFetcher`、`BTDHTFetcher`（`NewBTDHTFetcher`）、`HTTPURLFetcher`、`IPFSGatewayFetcher`、`UniversalDownloader`（`NewUniversalDownloader`、`Download`、`LastMetrics`、`CheckSources`、`ClearLocalCache`、`Fetchers`）；`maxURLFetchSize = 8GB` |

## 关键机制

### 1. Download 流水线（universal_downloader.go:305-367）

```
Download(ctx, hash):
  1. 防御：IsStrictSHA256 校验（hash 来自 anon collection entry 的远端输入，
     sync/serve 路径；未校验进 LocalFetcher 会 hash[:2] 越界 panic——最后防线，
     本地下载端点已前置校验）
  2. 依序遍历 fetchers：IsAvailable()==false → 记 "not available" 跳过
  3. Fetch 包 per-fetcher context.WithTimeout(d.timeout)（M5 双保险，见坑 2）
  4. 成功 → sha256 重算比对，不匹配记 "hash mismatch" 继续下一协议
  5. 命中 → cacheToLocal 落盘登记 → 返回 (data, protocol, nil)
  6. 全失败 → "file not found on any protocol"
```

每步记录 `FetcherMetric{Name, Duration, Success, Error}`，`LastMetrics()` 供
`/download/:hash/sources` 管理面排查。

**hash 校验语义**：命中协议返回的字节必须过 sha256 重算——内容寻址是整条链的
信任底（local 落盘时已校验故必过；网关/URL/BT 的内容**不可信**，校验即防御投毒）。

**per-fetcher 超时语义**：每个协议独立计时（`context.WithTimeout(ctx, d.timeout)`），
慢协议不拖垮整体——总耗时为 Σ 各协议超时，而非单次全局超时（这是「回退」与
「限时」的刻意权衡：宁可等完所有协议也不中断回退链）。

### 2. 四个 ProtocolFetcher

| fetcher | 名称 | 可用性 | 取数 |
|---|---|---|---|
| `LocalFetcher` | "local" | storageDir != "" | ① 标准 CAS `{dir}/{h[:2]}/{h}`；② `p2p/` 子目录变体（BT 下载落盘目录）；③ DB 中 Available 的 "local" provider（相对路径拼 storageDir） |
| `IPFSGatewayFetcher` | "ipfsgw" | provider 非空且有网关 | `hashutil.SHA256ToCID(hash)` → `provider.FetchByCID`（仅 HTTP 公共网关；libp2p DHT/Bitswap 栈批2 已删，universal_downloader.go:10-11） |
| `BTDHTFetcher` | "btdht" | `dhtSvc.Server != nil`（BT DHT 启用） | `p2p_bt.NewBTBridge(dhtSvc, storageDir).FetchFile`（独立库 go-peerdrive-bt 的 HTTP bridge） |
| `HTTPURLFetcher` | "http" | 恒 true | DB 中 Available 的 "http" provider，逐个 GET；`LimitReader(maxURLFetchSize+1)` 超限即异常跳过 |

### 3. 缓存与失效（cacheToLocal / ClearLocalCache）

- **cacheToLocal**（378-398）：写 `{dir}/{h[:2]}/{h}`（幂等：InsertFileMeta 忽略冲突）
  → 登记 FileTypeBlob meta + "local" provider。缓存写失败仅 Warn（下载仍算成功，
  只是下次不命中）。
- **ClearLocalCache**（428-447）：删标准 CAS + `p2p/` 两处路径，且把 DB 中 local
  provider 全标 `MarkProviderUnavailable`——否则文件删了但 provider 还在，LocalFetcher
  的 DB 回读路径仍会命中旧文件。`/download/:hash/refresh` 用。
- **p2p/ 子目录**：BT 下载完成的文件落 `{dir}/p2p/{h[:2]}/{h}`，LocalFetcher 两个
  位置都找——download 端点可以直接吃 BT 的成果（TestLocalFetcher_FileInP2PSubdir）。

### 4. 源可用性检查（CheckSources，405-425）

`/download/:hash/sources` 用：本地/http 实际 `Fetch` 试一次；网络协议（btdht/ipfsgw）
只报能力（true）——不真发请求（会真下载，浪费）。Controller 侧包 30s 超时。

### 5. 装配与配置（NewUniversalDownloader，242-256）

```
NewUniversalDownloader(btSvc, storageDir, order, timeout, ipfsProvider):
  storageDir   缓存/回读根目录（与 service.FileService 同源装配）
  order        逗号分隔协议序（"local,ipfsgw,btdht,http" 任意排列/裁剪）
  timeout      每个协议的单次超时（同时注入 HTTPURLFetcher 的 httpClient）
  ipfsProvider 网关 provider（nil 时 ipfsgw fetcher 恒不可用）
```

- 空 order / 全未知协议名 → 回退默认全量序（buildFetchers:271-273）
- `Fetchers()` 暴露内部切片仅供测试断言顺序（Download_FetchersMatchOrder）
- controller 的 `InitUniversalDownloader` 与 service.SyncService 的注入共用
  同一构造参数（两处独立实例，各持各的 metrics）

### 6. 数据流示例（一次 /download/:hash 请求的完整路径）

```
GET /download/{hash}
  └─ controller.DownloadBySHA256 → UniversalDownloader.Download(ctx, hash)
       ├─ local     未命中（文件不在本节点）
       ├─ ipfsgw    CID 网关 404（内容从未上 IPFS）
       ├─ btdht     桥接命中 → FetchFile 返回字节
       │            └─ sha256 比对通过
       └─ cacheToLocal 写 {storageDir}/{h[:2]}/{h} + 登记 meta/provider
  → 200 (data, X-Protocol: btdht)
第二次同 hash：
  ├─ local 直接命中（CAS 路径）→ 不再走网络
```

这也解释了缓存的意义：回退链是「第一印象」成本，缓存把高频 hash 收敛回 local。
`/download/:hash/refresh` 反其道：ClearLocalCache 后强制重走全链（验证远端内容
是否仍一致）。

## 与其它模块的关系

```
controller/download.go（/download/:hash、/sources、/refresh、anon 文件下载、sync 取数）
  ↓
downloader（本层）
  ├→ repository（GetFileProviders/InsertFileMeta/InsertFileProvider/MarkProviderUnavailable）
  ├→ provider.IPFSProvider（网关抓取；与 controller 的 ipfsGatewayProvider 同一装配）
  ├→ p2p_bt（BTDHTService + BTBridge，外部能力切面 ⑦）
  ├→ pkg/hashutil（IsStrictSHA256/SHA256ToCID）
  └→ model（FileMeta）
```

- **消费方**：controller 的 `universalDownloader` 包级变量（InitUniversalDownloader 注入）、
  `service.SyncService`（saveFile 取数）、controller/anon.go 匿名文件下载。
- **不依赖 transport**：取数路径是本地磁盘 / 外部网关 / BT / URL，与节点互联（PeerJS）
  正交；peer 取数由 source.PeerSource 承担（REFACTOR.md §3.8 分工）。
- 装配：`NewUniversalDownloader(btSvc, storageDir, order, timeout, ipfsProvider)`，
  order 来自配置（PEERDRIVE_DOWNLOAD_ORDER 之类），空则默认 `local,ipfsgw,btdht,http`。

## 坑与设计决策

1. **M5：HTTPURLFetcher 的 httpClient 超时**（145-153 注释）：原实现 `http.DefaultClient`
   无 timeout——慢速 URL provider 永久挂住 Download（下载端点随之挂死）；修复：
   NewUniversalDownloader 注入 `&http.Client{Timeout: d.timeout}`，且 Download 的
   per-fetcher context 再兜一层。**双保险**（client timeout 防单请求悬挂，context
   防整体耗时超限）。
2. **M5：lastMetrics 竞态**（236-239 注释）：Download 并发写 vs LastMetrics 读
   （/download/:hash/sources 高频调用）→ metricsMu 互斥保护。`Fetchers()` 暴露切片
   也意味着调用方不得并发改写 order。
3. **maxURLFetchSize 8GB**（151-153）：与 peerjs 上传上限一致（peerjs_routes.go 的
   64MB 是 fetch 端点专用；8GB 是 service 内防溢出上限）——`LimitReader(size+1)`
   超一字节即判异常 provider，防恶意/失控 URL 返回无限流。
4. **Download 的 hash 校验是「最后防线」**（306-310）：hash 可能来自远端 anon entry
   （sync/serve 路径），本地端点已前置校验，但下载器自身再挡一道——防 `hash[:2]`
   越界 panic（H1 同类问题在本层的防御复制）。
5. **hash mismatch 不中断回退**：某协议返回了错误内容（网关被投毒/URL 被篡改）→
   记 metrics 继续下一协议——内容寻址让错误来源可被发现，但不可被信任。
6. **CheckSources 对网络协议「报能力不报事实」**：避免管理端点触发真实下载；
   代价是 sources 列表对 btdht/ipfsgw 恒为 true（语义是「可能可用」）。
7. **cacheToLocal 失败不致命**：缓存是优化不是正确性——写盘失败仅 Warn，下载结果
   照常返回（下次再走全链回退）。
8. **ClearLocalCache 必须 MarkProviderUnavailable**：只删文件不够——DB 的 local
   provider 是 LocalFetcher 的第三查找路径，留着会「删了又命中」。
9. **buildFetchers 的 order 容错**：逗号后空格 trim、空段过滤、未知协议名跳过并
   LogWarn——配置写错不 panic，降级默认序（filtered 为空时回退全量默认序）。
10. **文件名语义**：cacheToLocal 的 meta Filename 用 hash 本身（FileTypeBlob），
    下载端点再按请求场景决定 attachment 文件名——缓存与展示名解耦。

## 测试

> 本文件全部测试为 legacy 标注（universal_downloader_test.go:3：「本文件属于 legacy
> 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景」），其中
> M5 相关修复（httpClient 超时/metrics 锁）的回归语义体现在 FetchFromServer 与
> 顺序类测试。

| 测试 | 覆盖 |
|---|---|
| `TestLocalFetcher_FileInStorageDir` | 标准 CAS 路径读取 |
| `TestLocalFetcher_FileInP2PSubdir` | `p2p/` 子目录变体（BT 成果复用语义） |
| `TestLocalFetcher_FileFromDBProvider` | DB local provider 回读（含相对路径拼接） |
| `TestLocalFetcher_NotFound` / `TestLocalFetcher_IsAvailable` | 未命中错误 / 可用性判定 |
| `TestBTDHTFetcher_NotAvailable` / `_Name` | BT 未启用时跳过语义 + 名称 |
| `TestHTTPURLFetcher_Name` / `_IsAvailable` / `_NoProvider` | 恒可用 + 无 provider 报错 |
| `TestHTTPURLFetcher_FetchFromServer` | httptest 服务器真实 GET（含超时 client 语义） |
| `TestNewUniversalDownloader_DefaultOrder` / `_CustomOrder` | order 解析与装配（默认序/自定义序/空段过滤） |
| `TestDownload_LocalFile` | 本地命中 + metrics 记录 |
| `TestDownload_Fallback` | 本地缺 → 回退下一协议（stub fetcher 链） |
| `TestDownload_AllProtocolsFail` | 全失败聚合错误 |
| `TestCacheToLocal` | 落盘 + DB 登记幂等 |
| `TestCheckSources` | 可用性映射（本地实际试、网络报能力） |
| `TestDownload_FetchersMatchOrder` | 顺序一致性 |
| `TestBuildFetchers_UnknownProtocol` | 未知协议名跳过 + Warn |

## 文件清单

```
back/internal/downloader/
├── universal_downloader.go        流水线主文件（接口 + 4 fetcher + Download/缓存/检查，452 行）
└── universal_downloader_test.go   全链路测试（legacy 标注，19 个用例）
```