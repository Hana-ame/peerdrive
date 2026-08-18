# p2p_bt —— BT DHT 桥（go-peerdrive-bt 独立库）

> 一句话职责：BitTorrent 生态能力桥——Mainline DHT 上的 announce/查找、
> BEP 44（可变/不可变数据存储）、BEP 51（infohash 采样）、torrent/magnet
> 下载与做种，作为**独立 go.mod 库** `github.com/Hana-ame/go-peerdrive-bt`
> （`back/p2p_bt/`，主 go.mod `replace` 引用）。

- 层归属：AOP ⑦ 外部能力切面（`doc/LAYERS.md` §1）
- 独立库事实：`back/p2p_bt/go.mod` 声明 `module github.com/Hana-ame/go-peerdrive-bt`
  （go 1.26.2），仅依赖 `anacrolix/dht/v2 v2.23.0` 与 `anacrolix/torrent v1.61.0`
  （+传递依赖）；主模块 `replace github.com/Hana-ame/go-peerdrive-bt => ./p2p_bt`
- 边界：**新代码禁止 import `internal/p2p_bt`**（旧路径已删）；主模块只准经
  `github.com/Hana-ame/go-peerdrive-bt` 引用（REFACTOR.md §8 规则 1）

---

## 职责

1. **BT Mainline DHT 节点**（`bt_dht.go`）：UDP 服务器 + 引导（router.bittorrent
   .com / dht.transmissionbt.com）——announce 文件 hash、查找提供者。
2. **BEP 44 数据存取**（`bep44.go`）：不可变项（≤1000 字节，target=sha1(v)）
   与可变项（Ed25519 签名 + seq + salt，target=sha1(pubkey‖salt)）的 put/get。
3. **BEP 51 采样**（`bep51.go`）：对 DHT 节点发 `sample_infohashes` 查询收集
   infohash 样本，支持 `DiscoverInfohashes` 爬取。
4. **torrent 下载客户端**（`client.go`）：.torrent 字节 / magnet URI 下载、
   暂停恢复、做种、进度查询、完成回调（sha256 逐文件）。
5. **文件桥**（`bt_bridge.go`）：把 peerdrive 内容寻址存储与 DHT 桥接——
   `ShareFile`（announce）、`FetchFile`（FindProviders → 对端 HTTP 拉取）。

## 模块清单（每个文件：文件名 + 一句话职责 + 关键导出）

### `bt_dht.go` —— DHT 节点 + announce/查找

| 关键导出 | 说明 |
|---|---|
| `PeerdriveDHTNodePrefix = [2]byte{0x70, 0x64}` | 节点 ID 前缀 "pd"（0x70='p', 0x64='d'）——标记自家节点，供 `IsPeerdriveNodeID` 识别 |
| `IsPeerdriveNodeID(id krpc.ID) bool` | 判断 20 字节 DHT 节点 ID 是否属于 peerdrive 节点 |
| `BTDHTService` 结构体 | `Server`(anacrolix dht.Server) / `listenAddr` / `NodeID` / `localBEP44Store`(sync.Map) |
| `NewBTDHT(listenAddr) (*BTDHTService, error)` | 起 UDP DHT + 引导 + 等 1s 路由表填充；可注入已绑定的 `net.Conn` |
| `Announce(hash) error` | sha256 截前 20 字节为 infohash → `Server.Announce(ih, port, false)` |
| `FindProviders(hash) ([]string, error)` | `AnnounceTraversal` 收集 `ip:port` 去重列表，15s 超时 |
| `NumNodes()` / `Close()` | 路由表节点数 / 关服务器 |
| `infoHashFromHex(hash)` | 40 hex（已是 infohash）或 64 hex（SHA256 截 20）→ 20 字节 |
| `generatePeerdriveNodeID()` | 前 2 字节 "pd" + 随机 18 字节 |

### `bep44.go` —— BEP 44 数据存取

| 关键导出 | 说明 |
|---|---|
| `ErrBEP44NotFound` / `ErrBEP44DHTDisabled` | 包级哨兵错误 |
| `PutImmutable(data) (target [20]byte, err)` | bencode 后 ≤1000 字节；三写：`localBEP44Store`（内存，保 Get 回环）→ `putLocal`（自家服务器）→ 远端 close nodes 尽力而为（需先 get 拿写 token） |
| `GetImmutable(target)` | 先查本地 store（自己 put 的必然能 get），再迭代 Kademlia lookup |
| `PutMutable(privKey, salt, data, seq) (target, err)` | Ed25519 签名，salt ≤64 字节，seq 递增 |
| `GetMutable(pubKey, salt) (data, seq, err)` | target=sha1(pubkey‖salt)，任意 seq 查询 |
| `putLocal(put, target)` | **已取消 context 的 `Server.Put`**——anacrolix 在发网络查询前先写本地 store，取消 ctx 让网络查询立即返回（依赖库内部行为，见坑） |
| `lookupValue(target, seq)` | 8 轮迭代 Kademlia（alpha=3 并行），首个带 v 的响应即返回 |
| `closestNodes(target, count)` | 路由表到 target 距离最近的 count 个节点 |
| `MakeBEP44Key()` / `MakeBEP44Target(pubKey, salt)` | 密钥对生成 / target 计算工具 |

### `bep51.go` —— infohash 采样

| 关键导出 | 说明 |
|---|---|
| `ErrBEP51DHTDisabled` / `ErrBEP51NoSamples` | 包级哨兵错误 |
| `SampleInfohashes(target) ([][20]byte, error)` | 并行查 8 个最近节点，去重收集；全失败回退本地服务器查询 |
| `queryNodeForSamples(addr, target)` | 单节点 `sample_infohashes` 查询（10s 超时） |
| `DiscoverInfohashes(maxResults)` | 用 3 个随机 target 覆盖不同 bucket 爬取，`maxResults` 上限 |

### `client.go` —— torrent 下载客户端

| 关键导出 | 说明 |
|---|---|
| `BTClient` 结构体 | `cl`(anacrolix torrent.Client) / `dataDir` / `onComplete` / `downloads` / `customPeers` / `torrentData` / `autoSeed` |
| `NewBTClient(dataDir)` / `newBTClient(dataDir, listenAddr)` | 配置：`Seed=false, NoUpload=true, DisableUTP=true` |
| `SetOnComplete(fn)` | 下载完成回调（infohash + 完成文件列表） |
| `AddTorrentBytes(data)` / `AddMagnetURI(uri)` | 从 .torrent 字节 / magnet 启动下载（返回 `TorrentMeta`） |
| `AddTorrent(meta)` / `AddMagnet(magnet)` | 向后兼容入口（重建 magnet 再走 AddMagnetURI） |
| `PauseDownload` / `ResumeDownload` / `RemoveDownload` | 暂停/恢复/删除（删除含数据目录清理） |
| `GetDownload` / `ListDownloads` / `GetGlobalStats` | 进度/状态查询 |
| `StartSeed` / `StopSeed` / `IsSeeding` / `ListSeeders` / `SetAutoSeed` | 做种管理 |
| `GetTorrentBytes` / `GetMagnetURI` | 导出 .torrent / magnet（磁力元数据就绪后从库导出） |
| `AddPeer` / `GetCustomPeers` | 手动对端注入 |
| `globalDHT` / `SetGlobalDHT(dht)` | 全局 DHT 引用（GetGlobalStats().DHTNodes 用） |
| `torrentMetaFromLibrary` / `metaFromTorrent` | 库类型 → 本库 `TorrentMeta`（HTTP API 响应用） |

内部机制：`downloadState{status: downloading/paused/completed/error/seeding}` +
`watchDownload`（2s 轮询完成判定）→ `finalizeDownload`（逐文件 sha256 → 关
`doneCh` → 触发 onComplete / autoSeed）。

### `bt_bridge.go` —— 文件桥

| 关键导出 | 说明 |
|---|---|
| `BTBridge` 结构体 | `DHT`(BTDHTService) / `storageDir` / `shared`(已共享 hash 集合) |
| `NewBTBridge(dhtSvc, storageDir)` | 创建 |
| `ShareFile(hash) error` | DHT announce + 记入 `shared`（幂等语义由调用方维护） |
| `FetchFile(ctx, hash) ([]byte, error)` | FindProviders → 逐个 `http://{peerAddr}/files/{hash}`（15s 超时）→ 第一个 200 返回 |
| `ListShared()` / `FilePath(hash)` / `EnsureFileWritten(hash, data)` | 已共享列表 / 标准 CAS 路径 / 写入 peerdrive 存储布局 |

### `bt_types.go` —— 对外共享类型

`TorrentFile`、`TorrentMeta`、`MagnetInfo`、`DownloadStatus`、`CompletedFile`、
`OnTorrentComplete`（回调签名）、`GlobalStats`——全部带 JSON tag，直接作
HTTP API 响应体。

### `log.go` —— 库内日志

`LogDebug/LogInfo/LogWarn/LogError/LogDuration` 五个包级函数；级别由
`PEERDRIVE_LOG_LEVEL` 环境变量控制（默认 INFO）。**坑：拆独立库后不能 import
主模块 `internal/log`（Go internal 规则），原来委托 `p2p_bt.LogDebug ->
log.LogDebug` 的写法改为自实现，签名/行为对齐**（log.go:1-3 注释）。

## 关键机制

### 1. 独立库约束与依赖方向

```
主模块 back/（go.mod replace）──► github.com/Hana-ame/go-peerdrive-bt
   ├─ router.go：NewBTDHT / NewBTClient / SetGlobalDHT / onComplete 登记
   ├─ controller/p2p.go：BT HTTP 端点（薄包装）
   └─ downloader：BTDHTFetcher（NewBTBridge + FetchFile）
```

- 库内**零 import 主模块**（不 import internal/*）；唯一跨界是 env
  `PEERDRIVE_LOG_LEVEL`（日志级别约定）。
- 测试：`cd back/p2p_bt && go test ./... -count=1 -race`（独立 go.mod，
  无需主模块 build tag）。

### 2. SHA256 → infohash 的映射

peerdrive 用 sha256 内容寻址（64 hex）；BT DHT 用 20 字节 infohash。
`infoHashFromHex` 统一入口：40 hex 原样解码，64 hex 截前 20 字节。
**截断意味着 DHT 层寻址空间是 160bit**——collision 风险理论上 2^80，工程
可接受（bt_dht.go:201-222 注释）。

### 3. BEP 44 的三级写策略（可靠性兜底）

`PutImmutable` 一次写三处：

1. `localBEP44Store`（内存 sync.Map）——**保证自己 put 的 Get 必然回环**，
   不依赖远端节点支持 BEP 44（大多数 DHT 节点不支持任意数据存储）；
2. `putLocal`（自家服务器本地 store）——能应答其他节点的 get；
3. 远端 close nodes 尽力而为（先 get 拿写 token 再 put，失败不报错）。

`GetImmutable` 对称地先查本地 store 再走网络 lookup。`PutMutable` 只有
putLocal + 远端两级（可变项不查本地缓存，靠 lookupValue 的 seq filter）。

### 4. BTBridge 的 HTTP 拉取约定

`FetchFile` 假设 DHT 对端「以 DHT 监听端口相同/邻近的 HTTP 端口提供
`/files/{hash}`」——这是 peerdrive 节点间的约定协议（非标准 BT 线协议）。
失败逐个换对端，全失败报错。**该假设是历史遗留设计**：新架构（PeerJS +
WebRTC）下文件互传已走帧协议，BT DHT 桥只作为 downloader 的 "btdht"
回退 fetcher 保留（见 downloader.md）。

## 与其它模块的关系

| 消费方 | 用途 |
|---|---|
| `internal/router/router.go:107-145` | `PEERDRIVE_BT_DHT_ENABLE`（默认 true）起 DHT；`PEERDRIVE_BT_DHT_LISTEN`（默认 :6881）；BT 下载完成 → `FileService.RegisterBTFile` 登记入 storage |
| `internal/controller/p2p.go` | BT HTTP 端点（torrent 上传/磁力下载/做种/状态）——薄包装，业务在库内 |
| `internal/downloader/universal_downloader.go` | `BTDHTFetcher`：`NewBTBridge(dhtSvc, storageDir)` + `FetchFile`，优先级 "btdht" |
| `internal/config/config.go` | `BTDHTEnabled` / `BTDHTListenAddr` / `DownloadDir` |
| 主模块 go.mod | `replace github.com/Hana-ame/go-peerdrive-bt => ./p2p_bt` |

反向：库不依赖主模块任何东西（除 env 约定）。

## HTTP 端点一览（controller/p2p.go 薄包装）

| 端点 | 函数 | 库内调用 |
|---|---|---|
| `GET /bt/status` | `BTDHTStatus` | `NumNodes()` 等 |
| `POST /bt/announce` | `BTAnnounce` | `DHT.Announce(hash)` |
| `POST /bt/find` | `BTFindProviders` | `DHT.FindProviders(hash)` |
| `POST /bt/bep44/put` | `BEP44Put` | `PutImmutable` |
| `POST /bt/bep44/get` | `BEP44Get` | `GetImmutable` |
| `GET /bt/bep51/sample` | `BEP51Sample` | `SampleInfohashes` |
| `POST /bt/torrent` | `BTTorrentUpload` | `AddTorrentBytes`（.torrent 上传启动下载） |
| `POST /bt/magnet` | `BTMagnetResolve` | `AddMagnetURI` |
| `GET /bt/download/:infohash` 系列 | `BTDownloadProgress` / `BTDownloadTorrent` / `BTDownloadMagnet` | `GetDownload` / `GetTorrentBytes` / `GetMagnetURI` |
| `POST /bt/download/:infohash/{pause,resume}` | `BTPauseDownload` / `BTResumeDownload` | `PauseDownload` / `ResumeDownload` |
| `DELETE /bt/download/:infohash` | `BTRemoveDownload` | `RemoveDownload` |
| `GET /bt/downloads` / `GET /bt/stats` | `BTDownloadList` / `BTGlobalStats` | `ListDownloads` / `GetGlobalStats` |
| `POST /bt/seed/:infohash` / `POST /bt/download/:infohash/unseed` | `BTSeedTorrent` / `BTStopSeed` | `StartSeed` / `StopSeed` |
| `POST /bt/seed-collection` | `BTSeedCollection` | 合集 → 生成 torrent → 做种（约 130 行，controller 侧组装） |

> 前端经 admin verb（`path=/bt/...`，admin 二进制上传的 `field:"torrent"` 声明
> 就为此设计，见 REFACTOR.md §3.10）调用这些端点；router 装配见
> `InitBTController` / `InitBTClient`（controller/p2p.go:56-73）。

## 坑与设计决策

| 编号 | 坑 | 修复 |
|---|---|---|
| Go internal | 拆独立库后 p2p_bt 不能 import 主模块 `internal/log` | log.go 自实现日志（签名/行为对齐），级别经 `PEERDRIVE_LOG_LEVEL` |
| 独立库断言 | README 说 p2p_bt「可独立使用」是**错的**（REFACTOR.md §6）：依赖 `internal/log`、`PutImmutable` 本地 store 优先掩盖网络失败、`putLocal` 依赖 anacrolix 内部行为（server.go:1081 先写 store 再发查询） | 已拆独立库（commit 5fb1193）后上述依赖解除；文档以 REFACTOR.md §6 为准 |
| 库内部行为依赖 | `putLocal` 用「已取消 context」的 `Server.Put` 达到「只写本地 store」效果——anacrolix 内部实现变化会导致行为漂移 | bep44.go:237-253 注释明确标注依赖点 |
| 截断 | 64 hex sha256 截 20 字节进 DHT | 寻址空间 160bit，collision 2^80，工程可接受（bt_dht.go:201-222 注释） |
| 全量读 | `FetchFile` 用 `io.ReadAll`——文件无限大时内存炸 | 桥是 legacy 回退路径（download 语义为全量缓存），新拉取走 PeerJS 流式；防御性上限未做（历史遗留） |
| 测试超时 | `TestFullBTDownload` 30s 内未完成 | `t.Skip` 跳过而非失败（DHT 引导/对端连通性属环境问题） |

## 测试（7 单测，独立 go.mod；`scripts/test-layers.sh` L7 段）

> 命令：`cd back/p2p_bt && go test ./... -count=1`

### `bt_test.go`

> 注：legacy 代码测试（文件头声明），未逐一标注发现背景；「发现背景」规范
> 对新代码生效。

| 测试 | 覆盖 |
|---|---|
| `TestParseTorrent` | .torrent 生成→`metainfo.Load` 解析一致性 + `AddTorrentBytes` 包装 |
| `TestParseMagnet` | magnet URI 解析（合法/非法）+ `AddMagnetURI` |
| `TestBTClientPauseResume` | 暂停/恢复/列表/全局统计/删除生命周期 |
| `TestFullBTDownload` | 进程内 seeder + BTClient 下载全流程，完成回调校验 sha256（30s 超时 skip） |
| `TestAddMagnetBackwardCompat` / `TestAddTorrentBackwardCompat` | 旧 API 入口兼容 |
| `TestGlobalStats` | 统计字段非负 + DHTNodes 经 globalDHT |

## 文件清单

| 文件 | 职责 |
|---|---|
| `go.mod` / `go.sum` | 独立模块（anacrolix/dht + torrent） |
| `bt_dht.go` | DHT 节点 + announce/FindProviders |
| `bep44.go` | BEP 44 不可变/可变数据存取 |
| `bep51.go` | BEP 51 infohash 采样 + 爬取 |
| `client.go` | torrent 下载/做种客户端 |
| `bt_bridge.go` | 文件桥（Share/Fetch/存储布局） |
| `bt_types.go` | 对外共享类型 |
| `log.go` | 库内日志（独立库约束） |
| `bt_test.go` | 库测试 |