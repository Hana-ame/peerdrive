# provider —— IPFS 网关提供者

> 一句话职责：通过公共 IPFS 网关按 CID 获取文件内容（`back/internal/provider/
> ipfs.go`）——多网关并发竞速取首个成功、失败重试 + 指数退避、可注入
> BitswapFetcher 回调作为第一优先级；是「外部能力切面」中 IPFS 生态位
> 仅存的 HTTP gateway 形态（libp2p DHT/Bitswap 栈已于 2026-08-16 批2 删除）。

- 层归属：AOP ⑦ 外部能力切面（`doc/LAYERS.md` §1）
- 历史：REFACTOR.md §7 目标包结构中的「provider 层1 文件获取抽象」——把
  service 里复制 6 遍的本地查找收敛进来（该愿景由 `internal/source/` 的
  `SourceManager` 承接，见 L4-core/source.md）；本包现存职责收敛为 IPFS
  网关提供者
- 配置：`PEERDRIVE_IPFS_GATEWAY_ENABLE`（默认 true）+ `PEERDRIVE_IPFS_GATEWAYS`
  （默认 `https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link`）

---

## 职责

1. **按 CID 拉取**：`GetReader(cid)`（流式响应体）与 `FetchByCID(ctx, cid)`
   （全量字节）两个入口。
2. **多网关竞速**：全部网关并发请求，第一个成功即返回；其余请求取消、
   迟到响应体关闭（防 goroutine 泄漏 + 连接泄漏）。
3. **失败重试**：单网关最多 3 次尝试（`defaultMaxRetries`），指数退避 +
   抖动（`sleepBackoff`）。
4. **Bitswap 优先级**：`SetBitswapFetcher` 注入回调后，先试 Bitswap 网络
   再回退 HTTP 网关（libp2p 栈删除后此回调由外部提供者注入，本包不内置）。
5. **文件名提示**：`GetFilenameHint(cid, originalFilename)`。

## 模块清单（每个文件：文件名 + 一句话职责 + 关键导出）

### `ipfs.go` —— IPFS 网关提供者核心

| 关键导出 | 说明 |
|---|---|
| `BitswapFetcher` 类型 | `func(ctx, cid) ([]byte, error)`——失败返回 `nil, nil` 由调用方回退 |
| `IPFSProvider` 结构体 | `Gateways`([]string) / `bitswapFetcher` / `client`(http.Client，30s 超时) |
| `NewIPFSProvider(gateways)` | 创建；`http.Client{Timeout: 30s}` |
| `SetBitswapFetcher(f)` | 注入 Bitswap 回调（优先生效） |
| `GetReader(cid) (io.ReadCloser, error)` | Bitswap 优先 → 网关竞速取首个成功响应体；失败者 body 关闭，成功者触发 drain goroutine 收尾其余结果 |
| `FetchByCID(ctx, cid) ([]byte, error)` | 同上但返回全量字节（调用方给 ctx，取消传播到所有网关请求） |
| `GetFilenameHint(cid, originalFilename)` | 原名优先，否则 CID 兜底 |
| `fetchBody(ctx, gw, cid)` | 单网关请求 + 最多 3 次重试（退避后重试） |
| `fetchBytes(ctx, gw, cid)` | `fetchBody` + `io.ReadAll` |
| `doRequest(ctx, gw, cid)` | `GET {gw}/ipfs/{cid}`（`strings.TrimRight(gw, "/")` 防双斜杠）；非 200 关闭 body 报 `HTTP %d` |
| `sleepBackoff(attempt)` | 指数退避 `500ms·2^(n-1)` 封顶 5s + 75%~125% 抖动（`math/rand`） |

常量：`defaultMaxRetries=3`、`defaultBaseInterval=500ms`、`defaultMaxInterval=5s`、
`defaultHTTPTimeout=30s`。

### `ipfs_test.go` —— 测试（见「测试」节）

## 关键机制

### 1. 竞速 + 取消 + 收尾（GetReader 的核心难点）

```
ctx, cancel := context.WithCancel(ctx)
for 每个网关: go fetchBody(ctx, gw, cid) → ch
结果循环（len(Gateways) 次）：
  r := <-ch
  r 成功 → 启动 drain goroutine 消费剩余 ch（关闭迟到 body）→ 返回 r.body
  失败   → close body，记 firstErr
全失败 → 汇总错误
```

- **第一个成功即返回**，`defer cancel()` 使其余在途请求的 HTTP 连接被取消
  （`http.NewRequestWithContext` 传播）；
- **drain goroutine**（ipfs.go:94-101）继续消费 channel 里迟到的结果并关闭
  body——否则慢网关的响应体泄漏（测试 `TestIPFSProvider_GetReader_NoBodyLeak`
  保护）；`i+1` 起点跳过已消费的成功项；
- `fetchBody` 的退避重试与竞速叠加：每个网关本身可重试 3 次，但**整体竞速
  仍受第一个成功者支配**——失败网关的后续重试在成功返回后随 ctx 取消。

### 2. Bitswap 优先 + HTTP 回退

`GetReader`/`FetchByCID` 的第一段逻辑相同：`bitswapFetcher != nil` 时先试
Bitswap（GetReader 用 30s 超时 ctx），成功即返回；否则进入网关竞速。
`BitswapFetcher` 契约「失败返回 nil 而非 error 也可」——实现约定失败回退。
libp2p 栈删除后（批2），本包不内置任何 Bitswap 实现，回调完全外部注入
（目前主服务未注入——Bitswap 能力整体下线，ipfsgw fetcher 恒走网关路径）。

### 3. 竞速语义的取舍

- 网关竞速是**首胜制**（非最短耗时制）：第 i 个成功立即返回，慢网关若有
  更优内容也不会被选中（内容寻址下内容一致，无影响）。
- `math/rand`（非 crypto/rand）用于退避抖动——只需统计分布，无需安全随机。
- 未配置网关且无 Bitswap → 明确报错 `"ipfs: no gateways configured and
  Bitswap unavailable"`（不静默）。

### 4. 行为参数速查

| 常量 | 值 | 作用 |
|---|---|---|
| `defaultMaxRetries` | 3 | 单网关失败重试次数（503/超时等瞬态错误） |
| `defaultBaseInterval` | 500ms | 退避基数（2 的幂递增） |
| `defaultMaxInterval` | 5s | 退避上限 |
| `defaultHTTPTimeout` | 30s | 单请求整体超时（http.Client） |
| 抖动 | 75%~125% | `delay * (0.75 + rand.Float64()*0.5)`——防多客户端同步重试风暴 |
| GetReader Bitswap ctx | 30s | Bitswap 优先尝试的超时窗口 |

注意 `defaultHTTPTimeout` 是**每请求**超时：单网关重试 3 次的最坏耗时约
`30s×3 + 退避`，但竞速整体不受其约束——首个成功即返回，其余经 ctx 取消。

### 5. 演进历史（为什么是「网关」形态）

1. **M4 落地**（REFACTOR.md §7）：`internal/provider/` 建立，目标是收敛
   service 里复制 6 遍的本地查找——原设计含 local/http/manager 等多实现
   （doc/FILE-REFERENCE.md:115-121 有旧清单）。
2. **批2 删栈**（2026-08-16）：libp2p host + DHT + Bitswap 随旧互联层整体
   删除——`ipfs_service.go`/`ipfs_compat.go` 无法独立存活（复用 P2PService
   的 host/DHT）。**IPFS 能力收敛为仅存 HTTP gateway 抓取**（LEGACY.md C 节）。
3. **source 层接管**（2026-08-16）：「文件获取抽象」的完整愿景由
   `internal/source/` 的 `SourceManager` 承接（Capability/优先级路由/统计，
   见 L4-core/source.md）；本包保留单一职责：IPFS 网关。
4. **现状**：`BitswapFetcher` 注入点保留（契约兼容未来 boxo Bitswap 注入），
   但主服务不再内置实现——ipfsgw fetcher 恒走网关竞速路径。

## 与 source 层的边界（为什么有两套「provider」）

| 维度 | `internal/provider`（本包） | `internal/source`（L4-core） |
|---|---|---|
| 语义 | 一种具体外部能力：IPFS 网关抓取 | 本节点文件获取的**统一路由**（Local/Peer/URL 源） |
| 接口 | `GetReader/FetchByCID`（CID 维度） | `Source` 接口 + Capability（hash 维度） |
| 消费方 | downloader（ipfsgw fetcher）、controller（pin/网关状态） | FileService/serveFile 语义（本地优先降级） |
| 演进 | 旧抽象残留，收敛为单一能力 | M4 愿景的最终形态 |

改名/迁移的教训：抽象收敛时**不要一拆再拆**——provider 包的历史文件
（local/http/manager）已被 source 包替代，本包只保留仍被引用的 IPFS 部分
（ipfs.go + ipfs_test.go），避免「空壳接口 + 死实现」。

## 与其它模块的关系

| 消费方 | 用途 |
|---|---|
| `internal/router/router.go:148-158` | 装配：`PEERDRIVE_IPFS_GATEWAY_ENABLE` + 逗号分隔网关列表 → `provider.NewIPFSProvider` → `controller.InitIPFSProvider` |
| `internal/controller/p2p.go` | 包级 `ipfsGatewayProvider`：`POST /ipfs/pin/:cid`（PinCID，下载并缓存）、`GET /ipfs/gateways`（健康检查，逐网关探测）、`GET /ipfs` 状态 |
| `internal/controller/download.go` | `InitIPFSProvider` + 下载回退路径（`FetchByCID`） |
| `internal/downloader/universal_downloader.go` | `IPFSGatewayFetcher`：`hashutil.SHA256ToCID(hash)` → `provider.FetchByCID`，优先级 "ipfsgw"（IsAvailable = provider 非空且有网关） |
| `internal/service/file_service.go`（经 controller） | `ImportGatewayData` 等网关数据导入 |

反向：本包零依赖（stdlib only），是最干净的叶子包之一。注意**与 L4-core/
source.md 的 SourceManager 是两套体系**：source 层管「本节点文件获取路由」，
本包管「IPFS 网关抓取」这一种具体外部能力；downloader 与 controller 各自
消费，互不绕行。

## 使用示例：PinCID 全链路（controller/p2p.go:938-999）

```
POST /ipfs/pin/:cid
  ├─ ipfsGatewayProvider nil / 无网关 → 503（明确报错）
  ├─ 已 pin → 200 already_pinned（pinSvc.Get 幂等）
  ├─ FetchByCID(ctx(60s 超时), cid)  ← 本包核心入口
  │    ├─ Bitswap（若注入）→ 失败回退
  │    └─ 网关竞速：GET {gw}/ipfs/{cid}，首个成功返回
  ├─ sha256(data) → hashStr
  ├─ 写 CAS 布局 {storageDir}/{h[:2]}/{h}（与 repository 的 anon/文件布局一致）
  ├─ pinSvc.InsertMeta(hash, cid, size, relPath)   ← file_meta 登记
  └─ pinSvc.Insert(cid, hash, cid, size)           ← ipfs_pins 登记（repository/pin_repo.go）
```

`GET /ipfs/gateways` 健康检查（p2p.go:1046-1067）：逐网关 `checkGateway`
探测，返回 `[{url, healthy, latency}]`——前端 BT/IPFS 面板用。未配置网关时
返回空数组而非报错（前端无需感知配置差异）。

## 与 downloader 的配合（IPFSGatewayFetcher）

```go
// universal_downloader.go:205-214
type IPFSGatewayFetcher struct { provider *provider.IPFSProvider }
IsAvailable() = provider != nil && len(provider.Gateways) > 0
// Fetch: hashutil.SHA256ToCID(hash) → provider.FetchByCID(ctx, cid)
```

下载优先级链 `local → ipfsgw → btdht → http`（downloader.md 可查）：本地
未命中时把 sha256 转 CID 试公共网关。**CID 转换**：`hashutil.SHA256ToCID` 把
sha256 的 32 字节 raw 编码为 CIDv1（raw codec + sha2-256 multihash）——同一
文件内容在 IPFS 侧与 peerdrive 侧寻址一致，网关缓存命中率最大化。

## 坑与设计决策

| 编号 | 坑 | 修复 |
|---|---|---|
| 竞速泄漏 | 首个网关成功返回后，慢网关的响应体无人关闭 → 连接/内存泄漏 | drain goroutine 消费剩余 channel 并关闭全部迟到 body（ipfs.go:94-101；`TestIPFSProvider_GetReader_NoBodyLeak` 保护） |
| 无超时 | `http.DefaultClient` 无 timeout → 慢网关永久挂住 | `http.Client{Timeout: 30s}`（`defaultHTTPTimeout`） |
| URL 拼接 | 网关地址尾部 `/` 与路径拼接出双斜杠 | `strings.TrimRight(gw, "/")` 再拼 `/ipfs/{cid}` |
| 重试风暴 | 网关瞬断时无退避疯狂重试 | `sleepBackoff`：指数 500ms→5s + 抖动 |
| 非 200 语义 | 404/500 的 body 泄漏 | `doRequest` 非 200 先 `resp.Body.Close()` 再报错 |
| 架构边界 | service 里「本地查找」复制 6 遍的历史问题 | M4 落地 `internal/provider/`（本包）与后续 `internal/source/`（SourceManager 路由化）；本包收敛为单一能力：IPFS 网关 |
| 功能删减 | libp2p DHT+Bitswap 栈随旧互联层删除（批2，universal_downloader.go:10-11） | IPFS 兼容 API/Bitswap 不再提供，仅保留 HTTP gateway 抓取；`BitswapFetcher` 回调保留注入点但主服务不再内置实现 |

## 测试（`ipfs_test.go`）

> 注：legacy 代码测试（文件头声明），未逐一标注发现背景；「发现背景」规范
> 对新代码生效。全部测试用 `httptest.Server` 模拟网关，无外部网络依赖。

| 测试 | 覆盖 |
|---|---|
| `TestIPFSProvider_GetReader_Success` / `_NoGateways` / `_NotFound` | 基本路径：成功 / 无网关明确报错 / 404 报错 |
| `TestIPFSProvider_GetReader_RaceWinner` | **竞速语义**：快网关胜出，耗时 <1s（慢网关被取消），内容为快网关的 |
| `TestIPFSProvider_GetReader_AllFail` | 全网关失败 → 汇总错误 |
| `TestIPFSProvider_FetchByCID_Success` / `_NoGateways` / `_NotFound` | FetchByCID 基本路径 |
| `TestIPFSProvider_FetchByCID_ContextCancel` / `_ParentContextCancel` | ctx 取消传播：50ms 超时 / 父 ctx 取消 → 报错返回 |
| `TestIPFSProvider_FetchByCID_RaceWinner` | FetchByCID 竞速（同 GetReader） |
| `TestIPFSProvider_GetFilenameHint` | 原名优先 / CID 兜底 |
| `TestIPFSProvider_SetBitswapFetcher` | Bitswap 回调优先：无网关 + 有回调 → 返回 Bitswap 数据 |
| `TestIPFSProvider_GetReader_NoBodyLeak` | 竞速收尾：快胜出后慢网关 body 被 drain goroutine 关闭（防泄漏回归） |
| `TestIPFSProvider_GetReader_ManyGateways` | 10 网关压力（不同延迟） |
| `TestIPFSProvider_RetryOnError` / `_RetryExhausted` | 重试：前 2 次 503 第 3 次成功 / 恒失败恰好 `defaultMaxRetries` 次 |

## 文件清单

| 文件 | 职责 |
|---|---|
| `back/internal/provider/ipfs.go` | IPFS 网关提供者（竞速 + 重试 + Bitswap 注入点） |
| `back/internal/provider/ipfs_test.go` | 测试（14 个，httptest 模拟网关） |