# 模块 02：repository 元数据库（SQLite）

- **代码位置**：`back/internal/repository`
- **功能一句话**：全节点唯一的元数据持久化层——以单个 SQLite 文件保存文件元数据与副本、合集与版本、分享链接、本地同步状态、IPFS pin、文件索引（sha256→绝对路径）及增量同步游标，并把匿名合集以内容寻址 JSON 文件落盘后登记进库（`back/internal/repository/db.go:1-14`）。
- **依赖**：`database/sql`；编译期二选一的 SQLite 驱动——有 cgo 用 `mattn/go-sqlite3`、无 cgo 退回纯 Go 的 `modernc.org/sqlite`（`back/internal/repository/db_driver_cgo.go:1-16`、`back/internal/repository/db_driver_pure.go:1-22`）；`peerdrive/internal/log`（日志）、`peerdrive/internal/model`（领域结构体）、`peerdrive/pkg/hashutil`（SHA256/CID 校验与换算）（`back/internal/repository/db.go:24-26`）；以及外部文件系统（匿名集合 JSON 按 `<storageDir>/<hash[:2]>/<hash>` 落盘，见 `back/internal/repository/anon_repo.go:40-49`）。
- **被依赖**：`back/cmd/server/main.go`（启动 `InitDB`/`SetAnonStorageDir`、停机 `CloseDB`，`main.go:76-87`）；`back/internal/service/*`（file/collection/anon/sync/pin/share/peerpull 等全部经此读写，M2 收层后 repository 只被 service 引用，见 `back/internal/service/collection_service.go:1-4`）；`back/internal/transport`（`FileIndexService` 对 file_index 表做 create/upload/delete/sync，`back/internal/transport/file_index.go:238,461,570,575`）；`back/internal/downloader`（下载缓存登记，`back/internal/downloader/universal_downloader.go:378-398`）；`back/internal/source`（IPFS pin 登记与删除，`back/internal/source/ipfs_control.go:56-70`）；`back/internal/router`/`controller`（健康探针经 `repository.Ping`，`back/internal/router/router.go:124`）。

---

## 1. 逻辑

**职责**：进程内全局单连接态的 SQLite 数据访问层。包级 `var DB *sql.DB` 存放唯一连接句柄（`back/internal/repository/db.go:35`），所有表操作函数直接依赖它；使用前必须先调用 `InitDB(dbPath)`（`db.go:5,87`）。

**核心类型与结构**：

- 连接与生命周期：`InitDB`（建连接 + 连接池参数 + Ping + 全量建表 DDL + 幂等迁移，`db.go:87-229`）、`CloseDB`（关闭并把 `DB` 置空，幂等，`db.go:45-52`）、`Ping`（供 `/ready` 探针，包一层而不暴露 DB 句柄，`db.go:54-63`）。
- 表操作函数按域拆分在 7 个文件里：`file_repo.go`（file_meta/file_providers）、`collection_repo.go`（collections/collection_entries/collection_versions/version_entries）、`share_repo.go`（share_links）、`sync_repo.go`（local_collection_sync/local_sync_files，`SyncRepository` 结构体）、`pin_repo.go`（ipfs_pins）、`anon_repo.go`（匿名集合「JSON 文件 + file_meta 登记」双写，见 `anon_repo.go:25-61`）、`file_index_repo.go`（file_index，`FileIndex` 结构体 `file_index_repo.go:12-21`）。
- 类型常量别名：`FileTypeBlob`/`FileTypeAnonCollection` 已上移 `model` 包，repository 保留别名（M2 收层，`db.go:28-33`、`back/internal/model/file.go:10-13`）。

**主要流程**：

1. **启动**：`main.go:52-87` → `config.Load()` 得 `DBPath`（默认 `./peerdrive.db`）→ `repository.InitDB(cfg.DBPath)` → `SetAnonStorageDir(storageDir)`（匿名集合默认落盘目录）。
2. **文件登记/上传**：service 层「写盘 + `InsertFileMeta` + `InsertFileProvider`(+`UpsertFileIndex`) 三连」（`back/internal/service/file_service.go:59,81-84,249-273,561-597`）。
3. **集合 commit**：controller → `SaveAnon`（匿名快照落盘+登记）→ `UpdateCurrentHash` → `CreateVersion` + `SnapshotVersionEntries`（`back/internal/controller/collection.go:350-416`）。
4. **传输层 verb**：create/upload 成功 → `UpsertFileIndex`（带单调 `seq`）；delete → tombstone；sync → `ListFileIndexSince` 增量拉取、`ApplySync` 本地回放（`back/internal/transport/file_index.go:205-244,415-472,566-591,594-614`）。
5. **下载缓存**：远程协议取到数据后 `cacheToLocal` 落 CAS 并登记（`back/internal/downloader/universal_downloader.go:378-398`）。
6. **IPFS pin**：`PinCID` 取网关数据 → 写 pin 缓存文件 → `InsertPin` + `InsertFileMeta` + `InsertFileProvider`（`back/internal/source/ipfs_control.go:34-73`）。

**生命周期**：`InitDB` 启动时一次建立；进程退出由 `main.go:79-83` 的 `defer CloseDB()` 兜底（生产路径此前不主动关，靠进程退出回收句柄，`db.go:38-44`）；测试路径必须显式 `CloseDB`（Windows 上打开着的 .db 删不掉，`db.go:37-44`、`back/internal/repository/db_test.go:10-27`）。

---

## 2. 如何储存

**介质与位置**：单文件 SQLite，路径来自 `cfg.DBPath`（`PEERDRIVE_DB_PATH` 环境变量，默认 `./peerdrive.db`，相对进程工作目录，`back/internal/config/config.go:27,190,260-262`）。连接串由 `dsn()` 拼出：普通文件路径追加驱动特定 PRAGMA 参数；`":memory:"`、`"file::memory:"`、`file:` 前缀的 URI 直接透传不追加（`db.go:79-84`）。仓库根现存 `peerdrive.db`，即默认路径产物。

**连接与并发**：

- 文件库连接池：`SetMaxOpenConns(8)` / `SetMaxIdleConns(4)` / `SetConnMaxLifetime(30*time.Minute)`（`db.go:102-106`）——SQLite 每个写事务独占库，连接池收口并发写冲突，冲突靠 `busy_timeout` 排队。
- 内存库（`:memory:`）：强制单连接且连接永不过期（`SetMaxOpenConns(1)` / `SetConnMaxLifetime(0)`）——内存库每个连接是一份独立空库，多连接或连接回收会让表「消失」（`db.go:65-73,98-101`）。**只用于测试**（各测试 `InitDB(":memory:")` 隔离，如 `back/internal/repository/file_repo_test.go:14`）。
- PRAGMA：`busy_timeout=5000`（写锁等待 5s）+ `foreign_keys=1`（显式开外键，否则 schema 里的 `ON DELETE CASCADE` 全是纸面约束）；两驱动语法不同（cgo：`?_busy_timeout=5000&_foreign_keys=1`；pure：`?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)`，`db_driver_cgo.go:18-29`、`db_driver_pure.go:19-22`）。

**格式/结构（12 张表）**：全部 DDL 在 `InitDB` 内执行，见 `db.go:115-199`（`local_collection_sync`、`local_sync_files`、`file_meta`、`file_providers`、`collections`、`collection_entries`、`collection_versions`、`version_entries`、`download_progress`）、`back/internal/repository/share_repo.go:86-95`（`share_links`，由 `db.go:216` 调用）、`db.go:219-225`（`ipfs_pins`）、`back/internal/repository/file_index_repo.go:24-36`（`file_index`，由 `db.go:227` 调用）。字段明细见第 4 节。

**非 SQLite 的持久化（委托给文件系统的部分）**：

- **匿名集合本体**：不存 SQLite，而是序列化为 JSON 按内容寻址写入 `<storageDir>/<hash[:2]>/<hash>`（`anon_repo.go:24-61`、`back/internal/service/anon_service.go:140-164,370-393`），SQLite 里只登记一条 `type='anon_collection'` 的 file_meta + 一条 `local` provider（`anon_repo.go:51-58`）。读时以文件为准，hash 校验不过/文件缺失即「未找到」（`anon_repo.go:64-86`）。
- **file_index 的 path 列**：只是「sha256 → 绝对路径」的**索引**，不复制文件本体（登记自 `Create`，仅计算哈希并落映射，`back/internal/transport/file_index.go:203-244`）。

**命名规则**：内容寻址相对路径统一为 `hash[:2] + "/" + hash`（`file_service.go:63,566`、`anon_repo.go:51`、`downloader/universal_downloader.go:379`、`source/ipfs_control.go:47`）；匿名集合 JSON 文件名 `anon_<hash>.json` 只作展示用（`anon_repo.go:55`）。

---

## 3. 何时储存

按触发时机逐条列出（均为真实调用点）：

**A. 进程启动**（一次性）

- `main.go:76` `repository.InitDB(cfg.DBPath)`：打开连接、连池、Ping、执行全部建表 DDL 与幂等迁移（`db.go:87-229`）。
- `main.go:87` `repository.SetAnonStorageDir(storageDir)`：设置匿名集合默认落盘目录（包级变量 `anonStorageDir`，`anon_repo.go:17-22`）。
- `main.go:79-83` `defer CloseDB()`：进程退出（含优雅停机 `srv.Shutdown` 后）关闭连接。

**B. HTTP 文件登记/上传/删除请求**（`back/internal/service/file_service.go`）

- `RegisterLocal`（绝对路径登记）：`GetFileMeta` 不存在时 `InsertFileMeta`，再 `InsertFileProvider(hash,"local",absPath)` 与 `UpsertFileIndex` 同层写（`file_service.go:249-273`）。
- `RegisterFolder`：`filepath.WalkDir` 逐文件调 `RegisterLocal`（`file_service.go:280-354`）。
- `Upload`（表单上传）：临时文件算 sha256 → `InsertFileMeta` + `InsertFileProvider(hash,"local","<前2位>/<hash>")`；重复 hash 直接返回 `ErrFileAlreadyExists` 不落库（`file_service.go:540-597`）。
- `RegisterURL`：`InsertFileMeta`（若不存在）+ `InsertFileProvider(hash,"http",rawURL)`，storage 启用时另存 CAS（`file_service.go:431-487`）。
- `CopyFile`：复制成功后追加一条 `local` provider（`file_service.go:740-790`，`InsertFileProvider` 在 786 行）。
- `Delete`：先用 provider 路径删本地文件，再直接 `repository.DB.Exec` 删 file_providers 与 file_meta 两表（`file_service.go:617-645`，越层直连见 641-642，属历史遗留）。
- `ImportGatewayData`（IPFS 网关 fallback 下载命中）：写 CAS + `InsertFileMeta` + `InsertFileProvider`（`file_service.go:60-79`）。

**C. 下载完成回调/缓存路径**

- `downloader.cacheToLocal`：`Download` 按优先级循环各 fetcher，任一 fetcher 成功且 sha256 校验匹配（`hashutil.IsStrictSHA256` 前置，`universal_downloader.go:308`）即调用（`universal_downloader.go:335-356`，调用点 348-349）落 CAS 并登记；重复缓存靠 INSERT 冲突幂等（`universal_downloader.go:378-398`）。
- `ClearLocalCache`：删 CAS 文件并把 local provider 标为不可用（`MarkProviderUnavailable`，`universal_downloader.go:427-447`）。
- `RegisterBTFile`：BT 下载完成回调登记（M2 收层自 router.go 的 onComplete，`file_service.go:81-119`）。

**D. 传输层 verb（对端经 WS/WebRTC 触发）**（`back/internal/transport/file_index.go`）

- `create` verb → `FileIndexService.Create` → `UpsertFileIndex`（`file_index.go:205-244`，登记在 238 行）。
- `upload` 分片全部到位校验通过（位图全满 + sha256 校验）→ `UploadSession.Complete` → `UpsertFileIndex`（`file_index.go:415-472`，登记在 461 行）。
- `delete` verb → `DeleteFileIndex` 写 tombstone（`file_index.go:563-571`）。
- `sync` verb → 读侧 `SyncSince` 走 `ListFileIndexSince`（`file_index.go:573-591`）；`ApplySync` 把对端变更 upsert/tombstone 回本地（`file_index.go:593-614`）。
- peerpull（跨节点拉取保存）经 `FileIndex().Create` 落 index（`back/cmd/server/main.go:174-180`）。

**E. 集合 commit / rollback / 匿名集合写操作**

- `CommitCollection`（HTTP POST）：先 `SaveAnon` 生成新快照 hash，再 `UpdateCurrentHash`，再 `CreateVersion`（parent=最新版本 id）+ `SnapshotVersionEntries` 复制工作区条目进快照（`back/internal/controller/collection.go:380-415`）。
- `RollbackCollection`：`RestoreVersionEntries` 事务内先删后插回滚工作区，随后重新 `SaveAnon` + `UpdateCurrentHash`（`collection.go:463-502`、`back/internal/repository/collection_repo.go:337-360`）。
- 匿名集合创建/commit/改可见性：`AnonService.CreateCollectionWithVisibility` / `saveCollectionJSON` / `repository.SaveCollection`——每次写 `<storageDir>/<hash[:2]>/<hash>` JSON 并登记（`anon_service.go:120-164,360-393`、`anon_repo.go:24-61`；改可见性走 `AnonService.UpdateCollectionVisibility`，`anon_service.go:224-251`，内部同样经 `saveCollectionJSON` 生成新 hash 落库）。
- 集合条目增删改（工作区）：`AddCollectionEntry`/`AddProviderCollectionEntry`/`RemoveCollectionEntry` 由 service 层在对应 HTTP/WS 请求时调用（`collection_repo.go:190-224`）。

**F. 本地同步请求**（`SyncService.SaveToDisk`，`back/internal/service/sync_service.go:31-75`）

- 先 `UpsertSyncState`（含 include/exclude 过滤，`sync_service.go:58`）、`ClearSyncFiles` 清旧记录（`:61`）；再逐文件下载写盘，成功/失败分别 `UpsertFileSyncState(...,true|false)`（`:68-70`）。

**G. 分享链接请求**（`POST /shares`）

- `controller.CreateShare` → `service.ShareService` → `repository.CreateShare`（生成 32-hex token + 30 天过期，`back/internal/controller/share.go:21-22`、`back/internal/service/share_service.go:15`、`back/internal/repository/share_repo.go:13-38`）；路由注册 `back/internal/router/router.go:380-381`。

**H. IPFS pin 管理端点**（`back/internal/router/router.go:289` 注册 `/ipfs/pins`）

- `PinCID`：拉网关数据 → 写缓存 → `InsertPin` + `InsertFileMeta` + `InsertFileProvider`（`back/internal/source/ipfs_control.go:34-73`）；`UnpinCID`：`RemovePin` + 删缓存文件（`ipfs_control.go:75-95`）。

**I. 健康探针（只读不写）**

- `/ready` 经 `controller.InitHealth(repository.Ping)` 探活（`back/internal/router/router.go:124`、`db.go:54-63`）。

---

## 4. 储存什么

### 4.1 表清单（12 张，均为 `CREATE TABLE IF NOT EXISTS`）

| 表 | 关键列 / 默认值 / 约束 | 代码位 |
|---|---|---|
| `file_meta` | `hash` TEXT **PK**；`size` INTEGER DEFAULT 0；`created_at` DATETIME DEFAULT CURRENT_TIMESTAMP；`mime_type` TEXT DEFAULT ''；`gziped` INTEGER DEFAULT 0；`filename` TEXT（可空）；`type` TEXT DEFAULT 'blob'（取值 `blob`/`anon_collection`，见 `model/file.go:10-13`）；`cid` TEXT DEFAULT ''（迁移列，见下） | `db.go:133-141`；cid 迁移 `db.go:213` |
| `file_providers` | `id` INTEGER PK AUTOINCREMENT；`hash` TEXT NOT NULL REFERENCES file_meta(hash)；`provider_type` TEXT NOT NULL（`local`/`http` 等，查询时 local 优先，`file_repo.go:62`）；`path` TEXT NOT NULL（local=绝对路径或 `前2位/hash` 相对路径；http=URL）；`available` INTEGER DEFAULT 1；索引 `idx_provider_hash(hash)` | `db.go:143-150` |
| `collections` | `id` PK；`username`/`collection_name` NOT NULL，`UNIQUE(username,collection_name)`；`current_hash` TEXT DEFAULT NULL（指向最近 commit 的匿名快照 SHA256）；`visibility` TEXT DEFAULT 'public'；`tags` TEXT DEFAULT ''（JSON 数组字符串，`model/collection.go:84-91`）；`follow_redirects` INTEGER DEFAULT 1；`created_at` DATETIME | `db.go:152-160`；迁移列 `db.go:209-212` |
| `collection_entries` | `id` PK；`collection_id` NOT NULL REFERENCES collections(id) ON DELETE CASCADE；`path`/`file_hash` NOT NULL；`providers_json` TEXT DEFAULT ''（JSON 数组，upsert 时写入，`collection_repo.go:190-198`）；`UNIQUE(collection_id,path)` | `db.go:162-169`；providers_json 迁移 `db.go:214` |
| `collection_versions` | `id` PK；`collection_id` NOT NULL REFERENCES collections(id) ON DELETE CASCADE；`version_number` NOT NULL（= 该集合当前 MAX+1，`collection_repo.go:267-288`）；`commit_message` TEXT；`created_at` DATETIME；`parent_version_id` INTEGER（可空，形成版本链） | `db.go:171-179` |
| `version_entries` | `id` PK；`version_id` NOT NULL REFERENCES collection_versions(id) ON DELETE CASCADE；`path`/`file_hash` NOT NULL；`providers_json` TEXT DEFAULT '' | `db.go:181-187`；迁移 `db.go:215` |
| `local_collection_sync` | `collection_hash` TEXT **PK**；`local_path` TEXT NOT NULL；`include_filter`/`exclude_filter` TEXT（JSON 数组，upsert 时序列化，`sync_repo.go:17-32`）；`synced_at` DATETIME DEFAULT CURRENT_TIMESTAMP（upsert 刷新） | `db.go:116-122` |
| `local_sync_files` | `id` PK AUTOINCREMENT；`collection_hash` NOT NULL REFERENCES local_collection_sync ON DELETE CASCADE；`file_path` NOT NULL；`is_saved` INTEGER DEFAULT 0；`last_modified` DATETIME DEFAULT CURRENT_TIMESTAMP；`UNIQUE(collection_hash,file_path)` | `db.go:124-131` |
| `share_links` | `id` PK；`token` TEXT UNIQUE NOT NULL（32 位 hex，随机 16 字节，`share_repo.go:14-19`）；`hash` NOT NULL；`type` NOT NULL DEFAULT 'file'；`filename` DEFAULT ''；`created_at` DATETIME；`expires_at` DATETIME（= 创建时间 + 30 天，`share_repo.go:21-22`；查询只取未过期 `expires_at > datetime('now')`，`share_repo.go:47`） | `share_repo.go:86-95` |
| `ipfs_pins` | `cid` TEXT **PK**；`hash` NOT NULL DEFAULT ''（pin 缓存内容的 sha256）；`size` DEFAULT 0；`filename` DEFAULT ''；`pinned_at` DATETIME DEFAULT CURRENT_TIMESTAMP | `db.go:219-225` |
| `file_index` | `hash` TEXT **PK**（64 位小写 hex sha256）；`path` NOT NULL（绝对路径）；`name` DEFAULT ''；`size` DEFAULT 0；`deleted` INTEGER DEFAULT 0（tombstone，读侧过滤 `deleted=0`，`file_index_repo.go:71-75`）；`seq` NOT NULL DEFAULT 0（单调递增同步游标，每次 upsert/delete +1，`file_index_repo.go:23,42-68,119-122`）；`created_at`/`updated_at` DATETIME；索引 `idx_file_index_seq(seq)` | `file_index_repo.go:24-36` |
| `download_progress` | `hash` PK；`total_size`/`received_size`/`last_chunk`/`chunks_total`/`chunks_done` INTEGER DEFAULT 0；`peers_used` TEXT DEFAULT ''；`started_at`/`updated_at` DATETIME DEFAULT CURRENT_TIMESTAMP | `db.go:189-199` |

### 4.2 关键约束与格式约定

- **hash 均为 64 位小写 hex SHA256**（`hashutil.IsValidSHA256`，`back/pkg/hashutil/hashutil.go:16-23`；传输层收紧为 `IsStrictSHA256` 只收小写，`hashutil.go:62-72`）；DB 列本身不强制，校验在各调用点（如 `file_service.go:629`、`transport/file_index.go:544,567`、`anon_repo.go:70`）。
- **CID**：`InsertFileMeta` 自动用 `hashutil.SHA256ToCID` 计算 CIDv1（base32，如 `bafkrei…`）写入 `file_meta.cid`（`file_repo.go:48-55`、`hashutil.go:25-41`）；读侧支持 `GetFileMetaByCID`（`file_repo.go:31-45`）。
- **tags / include_filter / exclude_filter**：以 JSON 数组字符串存储在 TEXT 列（`model/collection.go:84-91`、`model/sync.go:7-13`、`sync_repo.go:19-20`）。
- **seq 单调性**：`UpsertFileIndex` 在**单个事务**内 `SELECT MAX(seq)+1` 再 INSERT/UPDATE（事务体 `file_index_repo.go:42-67`），靠 SQLite 单写者串行保证（`file_index_repo.go:49`）；`DeleteFileIndex` 是逻辑删除，同样返回新 seq（`file_index_repo.go:119-122`）。
- **匿名集合 JSON 文件**：`<storageDir>/<hash[:2]>/<hash>`，条目先按 path 排序再序列化，hash = 对 JSON 字节的 sha256（`anon_repo.go:29-49`、`anon_service.go:124-146,361-377`）；格式版本 `version>=1` 才接受（`anon_repo.go:82-84`），`model/anon.go:1-4` 记录 Version=1（旧 hash 格式）/Version=2（providers 多源）兼容。

---

## 5. 边界与坑

- **写并发与连接池**：SQLite 每个写事务独占库，默认连接数无限时写冲突频发（偶发 "database is locked"），故文件库显式收口 8 连接 + `busy_timeout=5000` 排队（`db.go:94-106`、`db_driver_cgo.go:21-24`）。**内存库必须单连接且永不过期**——多连接/连接回收会让表「消失」（`db.go:65-73,98-101`）。
- **外键必须显式开**：SQLite 默认关闭外键，schema 里的 `ON DELETE CASCADE` 依赖 DSN 上的 `foreign_keys=1` 才生效（删合集时条目/版本才会级联清掉，`db_driver_cgo.go:26-28`）。
- **`sql.Open` 是惰性的**：驱动退化（无 cgo 时的 stub）或路径不可写不会在 Open 报错，`InitDB` 先 `DB.Ping()` 再建表，把病因变成启动期明确错误（`db.go:108-113`）；发布流程 `CGO_ENABLED=0` 交叉编译，无 cgo 必须走 pure 驱动，否则「一启动就死在建表」（`db_driver_cgo.go:5-12`）。本仓库构建/CI 统一带 `-tags nosqlite`（`back/cmd/server/main.go:5-7`、AGENTS.md「构建与验证」），注释为「双 SQLite 驱动 CGO 符号冲突」——该标记在第三方驱动侧的具体生效机制**未核实**。
- **Windows 文件锁**：打开着的 .db 删不掉（`CloseDB` 注释，`db.go:37-44`）；生产路径由 `main.go:79-83` defer 关闭，测试一律 `t.Cleanup(CloseDB)`（`db_test.go:10-27`）。同类坑还包括上传会话句柄（见 `transport/file_index.go:407-414`）。
- **迁移幂等**：`migrationExec` 对重复列只记 debug、其余错误记 warn 留痕（`db.go:231-241`）；曾静默吞错（L9，`db.go:205-208`）。
- **列表全表物化（M11 内存 DoS）**：多个列表查询加 LIMIT 兜底——`ListAllFiles` 1000（`file_repo.go:119`）、`ListCollections` 1000（`collection_repo.go:116`）、`SearchCollections` 100（`collection_repo.go:171`）、`ListCollectionEntries`/`GetVersionEntries` 10000（`collection_repo.go:245-246,317`）、`GetVersionLog` 1000（`collection_repo.go:298`）、`GetSyncFiles` 1000（`sync_repo.go:65`）、`ListPins` 1000（`pin_repo.go:30-31`）、`ListAnonCollections` 1000（`anon_repo.go:89-90`）、`ListFileIndexSince` 1000（`file_index_repo.go:100`）。
- **file_index seq 曾重复**：旧实现 `SELECT MAX+1` 与 INSERT 分两步，多连接并发时读到相同 MAX → seq 重复、同步游标错乱；现合并进同一事务（M10，`file_index_repo.go:38-53`，事务体 `42-67`）。
- **匿名集合 hash 越界/逃逸**：`hash[:2]` 切分前必须过 `hashutil.IsValidSHA256`——非法/短 hash 会切片 panic，`..` 类值会让 `filepath.Join` 逃出 storage 目录（`anon_repo.go:64-72`、`anon_service.go:171-176`）。
- **双写不一致容忍**：多处登记错误被忽略（`_ =` / 仅 warn），失败不阻塞主流程（如 `anon_repo.go:52-58`、`file_service.go:71-77,271-273`），读侧（`GetAnonCollectionByHash`）以磁盘 JSON 为准；`RegisterLocal` 的 file_index 写失败也只告警，后果是本节点文件不出现在共享清单（`file_service.go:263-273`）。
- **DELETE 未封装**：`file_service.Delete` 直接 `repository.DB.Exec` 删两表（`file_service.go:641-642`），是 M2 收层后仍残留的越层直连（已知历史遗留）。
- **`download_progress` 疑为死表**：表在 schema 中创建（`db.go:189-199`），但全仓搜索仅命中 DDL 本身，无任何读写代码；包头部注释的「表清单」（`db.go:6-12`）也未列出它——**用途未核实**，疑为旧的 BT 下载进度遗留（BT 进度现在走 `source/bt_control.go:111` 的内存态）。
- **`users`/`transfer_tasks` 已删**：曾由本地 AuthService/TaskService 使用，2026-08-19 随死代码删除；旧库遗留表无读写端，保留无害（`db.go:13-14`）。
- **PK 冲突语义**：`InsertFileMeta` 对已存在 hash 直接报 UNIQUE 错，调用方各自处理（ipfs pin 容忍 `UNIQUE` 错误，`ipfs_control.go:65`；`RegisterURL`/`Upload` 先查后插，`file_service.go:444,561`）。

---

## 6. 对外连接

- [../connections/04-service-repository.md](../connections/04-service-repository.md)：与 service 层的唯一业务调用面——file/collection/anon/sync/pin/share 等全部 DB 读写经此；DB 连接池与事务边界（`file_index` seq 事务、`RestoreVersionEntries` 事务）也在此文档描述。
- [../connections/03-controller-service.md](../connections/03-controller-service.md)：HTTP/WS 请求经 controller → service 层层转发落库（集合 commit/rollback、分享链接、匿名集合创建改档等）的上游约定。
- [../connections/02-router-controller.md](../connections/02-router-controller.md)：路由装配——`/ready` 探针用 `repository.Ping`（`router.go:124`）、`/shares`（`router.go:380-381`）、`/ipfs/pins`（`router.go:289`）等写库端点的入口。
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md)：下载完成路径向本模块写 file_meta/file_providers（`cacheToLocal`、`RegisterBTFile`、`ImportGatewayData`）的时机与幂等约定。
- [../connections/10-controller-storage.md](../connections/10-controller-storage.md)：内容寻址存储与元数据的「内容落盘 + SQLite 登记」双写关系——匿名集合 JSON 与文件 blob 走 `storageDir/hash[:2]/hash` 布局，登记列在本模块。
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md)：传输层（create/upload/delete/sync verb）经 `FileIndexService` 对 `file_index` 表的读写与 seq 增量同步（P2P 拉取落盘 + 索引保存）。
- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md)：前端管理台经本地 WS 会话触发集合 commit、匿名合集创建、本地同步等写库入口（请求侧视角）。