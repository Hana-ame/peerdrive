# 模块 06：service 业务逻辑层

- **代码位置**：`back/internal/service/`
- **功能一句话**：业务用例编排层——组合 repository（持久化）、transport/source/downloader（数据面）与 pathutil（落盘），对外只暴露给 controller/router 使用，屏蔽下层细节。
- **依赖**：repository（`back/internal/repository/`）、model（`back/internal/model/`）、transport（`back/internal/transport/`）、source（`back/internal/source/`）、downloader（`back/internal/downloader/`）、pathutil（`back/internal/pathutil/`）、config、nodestate、log。
- **被依赖**：`back/internal/controller/` 各控制器（file/collection/share/sync/anon/p2p/node_market/node_share/peer_pull 等）、`back/cmd/server/main.go`（装配 NewNodeDirectory/NewNodeShare/NewPeerPuller/NewAnonService）。

## 1. 逻辑

本层是「M2 收层」的产物：controller 此前直调 repository（60+ 处散落），现在读写都经 service 收编——`controller 只依赖 service，repository 只被 service 引用`（[collection_service.go:1-4](collection_service.go)）。方法命名与 repository 一一对应，多数为透明转发，事务/缓存/跨模块编排的落点放在这层。

九个服务组件（每个文件一个，按职责划分）：

| 组件 | 文件 | 职责 |
|------|------|------|
| FileService | [file_service.go](file_service.go) | 文件上传、URL 注册、本地文件注册/批量注册、验证/删除、目录遍历、元数据查询（M2 收层） |
| CollectionService | [collection_service.go](collection_service.go) | 集合域用例（集合 CRUD、搜索、fork/merge 相关透明转发） |
| ShareService | [share_service.go](share_service.go) | 30 天有效分享链接用例 |
| SyncService | [sync_service.go](sync_service.go) | 把集合文件同步到本地磁盘，含 include/exclude 过滤与同步状态跟踪 |
| PinService | [pin_service.go](pin_service.go) | IPFS pin 用例（M2 收层，legacy p2p 控制器内的独立功能） |
| AnonService | [anon_service.go](anon_service.go) | 匿名合集（visibility + AccessList），无状态读服务（只持 cfg） |
| NodeDirectory | [node_directory.go](node_directory.go) | 节点市场目录（NETDISK M1）：发现服务器在线 ∪ 本机连接 ∪ 已加入清单 |
| NodeShare | [nodeshare.go](nodeshare.go) | 节点共享范围（NETDISK M2）：环境变量初值 + 运行时选择合成；share 帧数据源 + announce 摘要 |
| PeerPuller | [peerpull.go](peerpull.go) | 跨节点拉取保存（NETDISK M3）：对端内容 → 本节点落盘 + 登记 |

**关键流程要点**：

- **NodeShare 装配**（main.go:134-162）：`NewNodeShare(cfg, storageDir)` → `SetDirHook`（运行时新增共享目录补 `FileIndex().AddReadRoot`）→ `SetAnonAccess`（匿名合集读取）→ `SetFileLister/SetFileInfoReader` → 作为 `SetShareProvider(share.SnapshotFor)` 与 `SetShareGate(share)` 喂给 transport。
- **PeerPuller 装配**（main.go:167-182）：`SetSource(peerjsSvc)`；`SetFileAccess(hasher, creator)` 用它判定「内容已在本地则跳过」、落盘后 `file_index.Create` 登记。
- **FileService 上传**（file_service.go:32-45）：构造只持 `storageDir/storageEnable/cfg`；落盘路径 `storageDir/<hash 前 2 位>/<hash>`（内容寻址，见 [03-storage.md](03-storage.md) 与 [10-controller-storage.md](../connections/10-controller-storage.md)）。

## 2. 如何储存

本层自身**不做通用持久化**——它是编排者，持久化按职责委托给下层，自己只保留两类小文件：

1. **委托 repository（SQLite）**：集合、分享链接、pin、同步状态、匿名合集 visibility/AccessList → `back/internal/repository/`（见 [02-repository.md](02-repository.md)、[04-service-repository.md](../connections/04-service-repository.md)）。
2. **委托 storage**：上传内容 → `storageDir/<hash 前 2 位>/<hash>`（内容寻址）；IPFS 网关数据 `ImportGatewayData` 同样落盘 + `InsertFileMeta` + `InsertFileProvider` 三连（file_service.go:57-70）。

**本层自有持久化文件（仅三处，均在 storageDir 下）**：

| 文件 | 写入方 | 介质/格式 | 说明 |
|------|--------|-----------|------|
| `joined_nodes.json` | NodeDirectory | JSON（`{peers:[{peer_id, joined_at}]}`）| 运营者加入过的节点，离线也保留；**原子写**（临时文件 + rename），防进程被杀留半截文件（node_directory.go:14-40） |
| `share_scope.json` | NodeShare | JSON | 运行时共享范围（dirs/files/collections + 级别），重启仍然有效（nodeshare.go:10-60） |
| `<DownloadDir>/pulled/…` | PeerPuller | 文件（`.part` 临时 → rename 正式名） | 跨节点拉取落盘，单写不写 CAS 副本（peerpull.go:15-45） |

**为什么不把这些进 SQLite**：joined_nodes 是极小的运营者偏好（peerId + 时间），要能在无 DB 场景（纯 client 模式/单元测试/CI）工作；share_scope 同理是「随手的决定」，与 file_index 的登记无关。

## 3. 何时储存

| 触发点 | 行为 | 代码依据 |
|--------|------|----------|
| 进程启动（main） | `NewNodeDirectory` 加载 `joined_nodes.json`；`NewNodeShare` 读 `share_scope.json`（环境变量 `PEERDRIVE_SHARE_*` 只在**首次启动**播种后落盘） | main.go:120-162；nodeshare.go:10-60 |
| 市场加入/退出操作（API） | `NodeDirectory.Join/Leave` → 更新 `joined_nodes.json`（原子写） | node_directory.go |
| 管理台勾选 / PUT `/peerjs/share` | NodeShare 更新 `share_scope.json`（运行时持久化；改环境变量不回灌） | nodeshare.go |
| 文件上传 / 网关导入（请求） | FileService 写内容寻址文件 + repository.InsertFileMeta + InsertFileProvider | file_service.go:57-70 |
| 同步保存（请求） | SyncService `SaveToDisk`：路径穿越防御 → 下载 → 落盘（downloader） | sync_service.go:15-40 |
| 跨节点拉取（请求） | PeerPuller：流式写 `.part` → 校验 sha256 → rename → `file_index.Create` 登记 | peerpull.go |

## 4. 储存什么

- **委托 repository 的数据**（明细见 [02-repository.md](02-repository.md)）：collections（用户名/集合名/visibility/follow_redirects/tags）、share_links（token、hash、type、filename、30 天过期）、pins（cid/hash/filename/size）、sync 状态、anon 合集（visibility/access_list）。
- **joined_nodes.json**：`{peers: [{peer_id, joined_at}]}` —— 市场「已加入」持久清单。
- **share_scope.json**：`{dirs: [], files: [], collections: [], friends: []}` + 每条声明的级别（public/unlisted/private）。
- **pulled 落盘**：`<DownloadDir>/pulled/<相对路径>.part→<相对路径>`；去重依赖 file_index 预查（同一 hash 已登记就不再下载）。

## 5. 边界与坑

- **NodeShare 默认关闭**：`PEERDRIVE_SHARE_ENABLE=false`，不显式开启就不对外暴露任何清单（nodeshare.go 头注释）。
- **安全边界（两条，代码注释点名别放宽）**：
  1. share 帧无可校验身份 → private 判定只在已通过 PSK 准入的连接上有意义（无 PSK 时好友名单退化为「自称该 id 的人」）；
  2. 合集 visibility 非 public 时**不进对外清单**，按 private 处理（AccessList 是账号列表，无身份无法校验）。
- **路径穿越防御**：SyncService `SaveToDisk` 开头拒绝含 `..` 的路径；PeerPuller 落盘路径同样受限。
- **M2 收层纪律**：controller 不得直调 repository（收编目标）；PinService 所在控制器 p2p.go 整体仍属 legacy（待 M1 迁移），但 pin 端点是收编后的独立功能。

## 6. 对外连接

- [04-service-repository.md](../connections/04-service-repository.md)：本层委托 SQLite 持久化的读写与事务边界。
- [03-controller-service.md](../connections/03-controller-service.md)：controller 调用本层的业务编排入口。
- [06-service-transport.md](../connections/06-service-transport.md)：NodeShare/NodeDirectory/PeerPuller 与 transport 的接线（share 帧、announce、拉取流）。
- [05-router-source.md](../connections/05-router-source.md)：SyncService 经 downloader 取数、FileService 经 pathutil 落盘的相邻面。
- [10-controller-storage.md](../connections/10-controller-storage.md)：上传/落盘路径安全（本层 FileService 与 controller 共用 pathutil 约定）。