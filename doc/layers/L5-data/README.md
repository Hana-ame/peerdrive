# repository —— 数据切面（AOP ⑤）

> 一句话职责：SQLite 持久化层——`back/internal/repository/` 全部 14 张表的建表、
> 迁移与 CRUD；其中 `file_index` 表（sha256→绝对路径 + seq 单调游标）是文件
> 索引 verb 增量同步的数据底座，其余表支撑业务核心的文件/合集/用户/分享/pin/
> 任务/本地同步持久化。

- 层归属：AOP ⑤ 数据切面（`doc/LAYERS.md` §1）
- 依赖方向：`model ← repository ← provider ← service ← controller ← router ← cmd`
  ——repository 只依赖 `internal/model` 与 `pkg/hashutil`，被 service 与
  transport 消费（transport 的 `file_index.go` 直接引用本层做索引持久化）
- 建表 DDL 全部集中在 `db.go` 的 `InitDB`，单文件管理全部 schema

---

## 职责

1. **集中管理 SQLite schema 与幂等迁移**：`InitDB(dbPath)` 打开连接（全局单例
   `DB`）、建全部表、跑 `ALTER TABLE` 迁移、调 `InitShareTable` /
   `createFileIndexTable`。
2. **文件内容寻址登记**：`file_meta`（hash PK：size/mime/gziped/filename/type/cid）
   + `file_providers`（hash → provider_type + path，多副本、available 标记）——
   文件服务的元数据/位置登记，匿名合集、BT 完成回调、URL provider 都写这里。
3. **文件索引增量同步**：`file_index` 表（sha256 → 绝对路径 + name/size/deleted/
   seq/时间戳），`seq` 单调游标支撑对端 `sync` verb 的 metadata 增量同步；
   tombstone（`deleted=1`）保证删除也可同步。**这是本层对新架构（帧协议文件
   索引 verb）的核心贡献**。
4. **用户/合集/分享/pin/任务/本地同步**：六组业务表 + 对应 repo，覆盖注册登录
   （authkey）、合集（含版本快照回滚）、分享链接（30 天过期）、IPFS pin、
   异步任务、集合→本地磁盘同步状态。
5. **匿名合集内容寻址落盘**：`anon_repo.go` 把 `AnonCollection` JSON 序列化后
   按 `{storageDir}/{hash[:2]}/{hash}` 布局写入（与文件 CAS 布局一致），并同步
   登记 `file_meta` + local provider，使匿名合集可经普通文件拉取路径取回。

## 模块清单（每个文件：文件名 + 一句话职责 + 关键导出）

### `db.go` —— 数据库初始化 + 全量建表 DDL + 幂等迁移

| 关键导出 | 说明 |
|---|---|
| `DB *sql.DB` | 全局连接（`sql.Open("sqlite3", ...)`），所有 repo 函数直接用它 |
| `InitDB(dbPath string) error` | 打开连接 + 执行 schema + 迁移；测试常用 `:memory:` |
| `migrationExec(stmt string)` | 幂等迁移执行器：duplicate column 只记 debug，真实错误记 warn（L9） |
| `FileTypeBlob` / `FileTypeAnonCollection` | 上移 `model` 包后的别名（M2 收层） |

建表清单（14 张）：`users`、`local_collection_sync`、`local_sync_files`、
`file_meta`、`file_providers`、`collections`、`collection_entries`、
`collection_versions`、`version_entries`、`transfer_tasks`、`download_progress`、
`share_links`（`InitShareTable`）、`ipfs_pins`、`file_index`（`createFileIndexTable`）。

### `file_index_repo.go` —— 文件索引（sha256→路径 + seq 游标）

| 关键导出 | 说明 |
|---|---|
| `FileIndex` 结构体 | `Hash/Path/Name/Size/Deleted/Seq/CreatedAt/UpdatedAt` |
| `UpsertFileIndex(hash, path, name, size, deleted) (seq, error)` | 登记/更新映射；**seq 在单事务内 `MAX+1`**（M10 并发修复），delete 也走它 |
| `GetFileIndex(hash)` | 查未删除映射（tombstone 只经 `ListFileIndexSince` 暴露） |
| `ListFileIndex(offset, limit)` | 全量列表（`deleted=0`，seq 倒序，默认 limit 1000） |
| `ListFileIndexSince(since)` | 增量同步：`seq > since` 升序，**LIMIT 1000 兜底**（防远端游标落后全表物化） |
| `DeleteFileIndex(hash)` | 逻辑删除 = `UpsertFileIndex(..., deleted=true)` 返回新 seq |

### `file_repo.go` —— 文件元数据 + 存储位置（file_meta / file_providers）

| 关键导出 | 说明 |
|---|---|
| `GetFileMeta(hash)` / `GetFileMetaByCID(cid)` | 查询；未找到返回 `(nil, nil)` |
| `InsertFileMeta(meta)` | 插入时用 `hashutil.SHA256ToCID` 自动算 CID |
| `GetFileProviders(hash)` | 可用 provider 列表，**local 优先**（`CASE provider_type` 排序） |
| `InsertFileProvider(hash, type, path)` / `MarkProviderUnavailable(id)` | 登记/失效副本 |
| `ListAllFiles(sortBy)` | blob 列表，支持 time/path/name/type/size 排序，**LIMIT 1000**（M11） |

### `collection_repo.go` —— 用户合集 + 版本快照（4 张表）

| 关键导出 | 说明 |
|---|---|
| `CreateCollection` / `CreateCollectionWithVisibility` / `WithTags` / `WithFull` | 建合集（public/unlisted/private + tags + follow_redirects） |
| `GetOrCreateCollection` | 查询不存在则自动创建 |
| `ListCollections` / `ListPublicCollections` / `GetCollection` / `SearchCollections` | 查询；均带 LIMIT（1000/100，M11） |
| `UpdateCurrentHash` / `UpdateCollectionTags` / `SetCollectionVisibility` | 更新属性 |
| `AddCollectionEntry` / `AddProviderCollectionEntry` | upsert 条目（`ON CONFLICT(collection_id,path)`，含 `providers_json`） |
| `RemoveCollectionEntry` / `GetCollectionEntry` / `ListCollectionEntries` | 条目增删查（列表 LIMIT 10000） |
| `CreateVersion` / `SnapshotVersionEntries` / `GetVersionLog` / `GetVersionEntries` / `RestoreVersionEntries` | 版本快照：commit 时快照，回滚在**事务内先删后插** |

### `user_repo.go` —— 用户认证

| 关键导出 | 说明 |
|---|---|
| `UserRepository` + `NewUserRepository()` | 空结构体实例化（唯一走方法的 repo） |
| `ErrUserNotFound` / `ErrUserExists` | 包级哨兵错误 |
| `CreateUser` / `GetByUsername` / `GetByAuthKey` / `UpdateAuthKey` / `ClearAuthKey` | users 表 CRUD（authkey = 长效令牌） |

### `anon_repo.go` —— 匿名合集内容寻址存储

| 关键导出 | 说明 |
|---|---|
| `SetAnonStorageDir(dir)` | 包级默认存储目录 |
| `SaveCollection(coll, storageDir) (hash, error)` | 条目按 path 排序后 JSON 序列化 → sha256 → 写 `{dir}/{h[:2]}/{h}` → 登记 meta+provider |
| `GetAnonCollectionByHash(hash, storageDir)` | 读回 + 反序列化；**先 `IsValidSHA256` 再拼路径**（防 hash[:2] 越界/路径逃逸） |
| `ListAnonCollections(storageDir)` | file_meta 里 type=anon 的列表（LIMIT 1000）+ 每行读 JSON 补 friendly_name 预览 |

### `pin_repo.go` —— IPFS pin 管理（ipfs_pins）

| 关键导出 | 说明 |
|---|---|
| `InsertPin(cid, hash, filename, size)` | upsert（ON CONFLICT(cid) 更新） |
| `ListPins()` / `GetPin(cid)` / `RemovePin(cid)` / `PinExists(cid)` | 查询/删除（列表 LIMIT 1000，M11） |

### `share_repo.go` —— 分享链接（share_links）

| 关键导出 | 说明 |
|---|---|
| `CreateShare(hash, shareType, filename)` | 16 字节随机 token（`crypto/rand`）+ 30 天过期 |
| `GetShareByToken(token)` | 只查未过期；`sql.NullTime` 显式处理 NULL/时间（L7 修复） |
| `ListShares()` | 未过期链接倒序，LIMIT 100 |
| `InitShareTable()` | 建表（`InitDB` 迁移阶段调用） |

### `sync_repo.go` —— 集合→本地磁盘同步状态（2 张表）

| 关键导出 | 说明 |
|---|---|
| `SyncRepository` + `NewSyncRepository()` | 实例化（router 装配注入 SyncService） |
| `UpsertSyncState` / `GetSyncState` | 集合同步配置（local_path + include/exclude 过滤 JSON） |
| `UpsertFileSyncState` / `GetSyncFiles` / `ClearSyncFiles` | 单文件 is_saved 标记（列表 LIMIT 1000，M11） |

### `task_repo.go` —— 异步任务（transfer_tasks）

| 关键导出 | 说明 |
|---|---|
| `CreateTask(type, params) (id, error)` | 新建任务（status='pending'） |
| `UpdateTaskStatus(id, status, result)` | 更新状态 + `updated_at` 自动刷新 |
| `GetTask(id)` | 查询；未找到 `(nil, nil)` |

## 关键机制

### 0. 表结构速查（关键列）

| 表 | 关键列 | 用途 |
|---|---|---|
| `file_index` | hash PK / path / name / size / deleted / **seq**（索引 idx_file_index_seq）/ created_at / updated_at | sha256→绝对路径 + 同步游标（新架构核心） |
| `file_meta` | hash PK / size / mime_type / gziped / filename / type（blob\|anon）/ cid | 文件内容元数据（内容寻址登记） |
| `file_providers` | id / hash FK→file_meta / provider_type（local\|http）/ path / available | 文件存储位置（多副本，可标记失效） |
| `collections` | id / username / collection_name / current_hash / visibility / tags / follow_redirects / UNIQUE(username, collection_name) | 用户合集（current_hash 指向最新匿名快照） |
| `collection_entries` | id / collection_id FK / path / file_hash / providers_json / UNIQUE(collection_id, path) | 合集工作区条目 |
| `collection_versions` | id / collection_id FK / version_number / commit_message / parent_version_id | 版本快照记录（支持父版本链） |
| `version_entries` | id / version_id FK / path / file_hash / providers_json | 版本快照内容 |
| `users` | id / username UNIQUE / password_hash / authkey UNIQUE | 注册用户（authkey = 长效令牌） |
| `share_links` | id / token UNIQUE / hash / type / filename / expires_at | 分享链接（30 天过期） |
| `ipfs_pins` | cid PK / hash / size / filename / pinned_at | IPFS pin 缓存登记 |
| `transfer_tasks` | id / type / status / params / result | 异步任务跟踪 |
| `download_progress` | hash PK / total_size / received_size / chunks_* / peers_used | 下载进度（遗留，无活跃写入方） |
| `local_collection_sync` | collection_hash PK / local_path / include_filter / exclude_filter / synced_at | 集合同步配置 |
| `local_sync_files` | id / collection_hash FK / file_path / is_saved / UNIQUE(collection_hash, file_path) | 单文件同步状态 |

### 1. seq 单调游标（file_index 增量同步的基石）

`file_index.seq` 每次 upsert/delete 递增 1。`UpsertFileIndex` 把
`SELECT COALESCE(MAX(seq),0)` 与 INSERT 放进**同一个写事务**
（file_index_repo.go:42-68），依赖 SQLite 单写者串行化保证两个并发请求不会
读到相同 MAX 产生重复 seq（M10 坑，见「坑与设计决策」）。消费方：

- `transport/file_index.go` 的 `Create`（登记外部文件）、`upload` 完成（写盘后）、
  `Delete`、`ApplySync`（合并对端增量，file_index.go:423-428）都经本层
  `UpsertFileIndex` / `ListFileIndexSince` 读写游标；
- 对端 `sync{seq}` verb → `ListFileIndexSince(since)` 取增量（含 tombstone）→
  `ApplySync` 合并；`GetFileIndex` 对 tombstone 不可见（只经增量暴露），保证
  「已删除文件不会在 info/list 里复活」。

### 2. 幂等迁移策略（migrationExec）

`InitDB` 里旧库升级靠 `ALTER TABLE ... ADD COLUMN` 序列。重复执行时
duplicate column 是预期结果——`migrationExec` 把这类错误只记 debug；
**其他错误（表缺失、IO 故障）记 warn 留痕**（L9 修复：原来所有 ALTER 错误
被静默吞掉，真实迁移失败无从排查）。新增表则用 `CREATE TABLE IF NOT EXISTS`
天然幂等，无需迁移逻辑。

### 3. 匿名集合 = 文件（内容寻址双写）

`SaveCollection` 把合集 JSON 当普通文件处理：排序条目 → 序列化 → sha256 →
写 `{storageDir}/{hash[:2]}/{hash}`，同时 `InsertFileMeta`（Type=AnonCollection）
+ `InsertFileProvider("local")`。好处：匿名合集 hash 就是一个可寻址文件 hash，
下载路径（LocalFetcher/CAS 读取）零特殊分支；`GetAnonCollectionByHash` 反向
读回 JSON。`ListAnonCollections` 是唯一的「批量读文件」查询——每行再
`os.ReadFile` 补 friendly_name/version/预览，文件 IO × N，所以 LIMIT 1000。

## 与其它模块的关系

```
transport（file_index.go / inbound.go）──► repository（file_index 系列）
service（collection/share/task/pin/sync/anon/file）──► repository
downloader（universal_downloader.go）──► repository（file_providers 回读）
controller ──（M2 收层后禁止直接 import repository，一律经 service）
```

- **transport**：文件索引 verb 的持久化全部走本层（见机制 1）。
- **service**：M2 收层后 controller 不再碰 repository，业务持久化统一收编进
  service（CollectionService/ShareService/TaskService/PinService/SyncService/
  FileService），本层是这些服务的唯一数据出口。
- **downloader**：`LocalFetcher` 第三查找路径读 DB 的 "local" provider，
  `HTTPURLFetcher` 读 "http" provider（provider 失效用 `MarkProviderUnavailable`）。
- **provider/anon**：`anon_repo.SaveCollection` 双写 file_meta/providers，
  与 file_repo 共享内容寻址布局。

## 坑与设计决策

| 编号 | 坑 | 修复 |
|---|---|---|
| M10 | `nextFileIndexSeq()` 先 SELECT MAX 再单独 INSERT——连接池多连接并发写时读到相同 MAX → seq 重复，sync 游标错乱 | SELECT+INSERT 合并进同一事务（SQLite 串行写保证单调），file_index_repo.go:42-68 |
| L9 | 原迁移静默吞掉所有 ALTER 错误——重复迁移的 duplicate column 是预期，但表缺失/磁盘故障也被吞 | `migrationExec` 区分：duplicate column 记 debug，其余记 warn 留痕 |
| M11 | 一批列表查询无 LIMIT——文件多/恶意构造大集合时全表物化内存 DoS | `ListAllFiles` 1000、`ListCollections` 1000、`SearchCollections` 100、`ListCollectionEntries`/`GetVersionEntries` 10000、`GetVersionLog` 1000、`ListAnonCollections` 1000、`ListPins` 1000、`GetSyncFiles` 1000 |
| L7 | `GetShareByToken` 原把 expires_at Scan 进 `*any`——driver 返回类型不确定（time.Time 或 string），断言失败时过期时间静默为空 | 改 `sql.NullTime` 显式处理 NULL/时间 |
| 路径穿越 | `GetAnonCollectionByHash` 的 hash 可能来自 URL/请求体/远端 sync，未校验时 `hash[:2]` 越界 panic、`..` 逃逸 storage 目录 | 先 `hashutil.IsValidSHA256` 再拼路径 |
| 层归属 | controller 曾直写 SQL（ListPublicCollections） | M2 收层收敛进 repository，controller 只依赖 service |
| 兼容 | FileType 常量迁移到 model 包 | repository 保留别名 `FileTypeBlob`/`FileTypeAnonCollection` 避免 diff 爆炸 |

## 测试

### `collection_repo_test.go`

> 注：legacy 代码测试（见 doc/LEGACY.md），未逐一标注发现背景；「发现背景」
> 规范对新代码生效（文件头注释）。

- `TestCollectionRepo_GetOrCreate`：GetOrCreate 幂等——同一用户名+集合名返回
  相同 ID。
- `TestCollectionRepo_CreateWithVisibility`：带可见性创建后可查回。
- `TestCollectionRepo_EntriesCRUD`：条目增删查全流程（2 条 → 删 1 条）。
- `TestCollectionRepo_VersionFlow`：CreateVersion 版本号从 1 递增 + GetVersionLog。
- `TestCollectionRepo_ListAndSearch`：ListCollections 与 SearchCollections 命中。
- `TestCollectionRepo_Tags`：标签创建与更新。

### `file_repo_test.go`

> 同上，legacy 测试，未标注发现背景。

- `TestInsertFileMetaAndGetFileMeta`：meta 写入读出全字段一致。
- `TestGetFileMetaNonexistent`：未找到返回 `(nil, nil)` 而非错误。
- `TestInsertFileProviderAndGetFileProviders`：多 provider 登记，**local 优先**
  排序。
- `TestMarkProviderUnavailable`：标记失效后不再返回。
- `TestGetFileProvidersEmptyForNonexistentHash`：未登记 hash 返回空。

## 文件清单

| 文件 | 职责 |
|---|---|
| `db.go` | 初始化 + 全量建表 DDL + 幂等迁移（L9） |
| `file_index_repo.go` | sha256→路径映射 + seq 游标（M10 事务修复） |
| `file_repo.go` | file_meta / file_providers CRUD |
| `collection_repo.go` | 合集 4 表 + 版本快照回滚 |
| `user_repo.go` | users 表 CRUD（authkey） |
| `anon_repo.go` | 匿名合集内容寻址读写（含路径防御） |
| `pin_repo.go` | ipfs_pins CRUD |
| `share_repo.go` | 分享链接（token + 过期） |
| `sync_repo.go` | 本地同步状态 2 表 |
| `task_repo.go` | 异步任务 CRUD |
| `collection_repo_test.go` | legacy 测试（见 LEGACY.md） |
| `file_repo_test.go` | legacy 测试（见 LEGACY.md） |