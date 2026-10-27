# Architecture Simplification Todo

## Data Model Cleanup

| # | Item | Status | Risk |
|---|------|--------|------|
| M1 | `AnonEntry` old format | ✅ Removed | Low |
| M2 | `file_meta` + `file_providers` tables | 🚧 Step 1/2 landed (#405), Step 3 删表未做 | Highest |

### M2 分步迁移方案：`file_meta` + `file_providers` → `file_index`

**定性（2026-10-25，基于 `491f286` 复核）**：两表**不是死表**。生产代码里有 15 处写点（分布在 10 个文件）
（upload / register_local / register_bt / register_url / anon collection ×2 / anon_repo /
IPFS pin ×2 / universal downloader / transport write-file / transport upload-session）与
10 类读方（`GetMeta`、`GetMetaByCID`、`ListAllFiles`、`ListAnonCollections`、
`GetFileProviders`、`GetLocalPath`、`ReadFile`、`Delete`、downloader、`CheckFileIndexConsistency`），
完整「表 × 文件:行 × 读/写」清单见 PR #405 正文。因此按「确认死表 → 删表」路径处理会直接
删掉活跃数据；本项走「收敛到 `file_index`」路线。

**目标 schema（Step 3 完成后）**：
- `file_index` 为唯一权威：`hash, path, name, size, kind, mime_type, gziped, cid, seq,
  deleted, created_at, updated_at, uploader_peer_id, is_inbox, origin, remote_url`。
- 删除 `file_meta`、`file_providers` 两表，以及 `repository/file_repo.go`、
  `model.FileMeta` / `model.FileProvider`、`Insert/Get/Update/Delete*` 旧方法。
- URL 注册（`POST /files/register_url`）的 http provider 没有本地路径，不能塞进
  `file_index.path`；已迁移到 `file_index.remote_url`（Step 2，见下），因此
  `file_providers` 不再有真实读方，可以在 Step 3 删除。

**Step 1（本 PR 已落地，#405）—— 建立单一写入口 + 回填 + 读路径切换，双写标记**：
1. `file_index` 增列 `kind / mime_type / gziped / cid / origin`（沿用「CREATE 全列 +
   ALTER 忽略 duplicate column」的自迁移，理由见 `file_index_repo.go` 函数头）。
2. `repository.RegisterFile(FileRecord)` 单事务写权威 index + `file_meta`/`file_providers`
   兼容镜像，index 行标 `origin='unified'`；`UpsertFileIndex*` 保持纯 index 写
   （P2P sync / Create 的远端路径不应伪造本地 meta）。
3. 启动时 `repository.BackfillFileIndexFromLegacy(storageDir)`：旧库中只在 `file_meta`
   的行导入 index（`origin='legacy_backfill'`），只增不删、幂等；文件已丢失的旧行跳过。
   回填 SQL 思路：
   ```sql
   INSERT INTO file_index (hash, path, name, size, deleted, seq, kind, mime_type, gziped, cid, origin)
   SELECT m.hash,
          COALESCE((SELECT p.path FROM file_providers p
                    WHERE p.hash=m.hash AND p.provider_type='local'
                    ORDER BY p.available DESC, p.id LIMIT 1), ''),
          m.filename, m.size, 0, <next-seq>, m.type, m.mime_type, m.gziped, m.cid, 'legacy_backfill'
   FROM file_meta m
   WHERE NOT EXISTS (SELECT 1 FROM file_index f WHERE f.hash=m.hash);
   ```
   实现里相对 provider 路径按 storageDir 解析、无 provider 时回退 CAS `<storage>/<h[:2]>/<h>`，
   并对每个候选 `os.Stat`，避免索引到已删除的字节。
4. 读路径 index-first：`FileService.GetMeta/GetMetaByCID/ListAll`、`ListAnonCollections`、
   `downloader.LocalFetcher`；P2P list/sync/search 过滤 `kind='blob'`（合集 JSON 不得
   出现在文件面）；`ClearLocalCache` 同时给 index 打 tombstone。
5. 测试：`repository/file_backfill_test.go`（旧 schema 升级 + 幂等 + 不删数据）、
   `service/file_convergence_test.go`（index 与镜像不一致时以 index 为准、删除双轨一致）。

**Step 2（本 PR 已落地，停写镜像）—— 全部改动如下，逐条可回滚**：
1. `PEERDRIVE_LEGACY_META_MIRROR`（默认 `1`）→ `repository.SetLegacyMetaMirror`。设为 `0`
   时 `RegisterFile` 不再写 `file_meta` / `file_providers`，`file_index` 写入不受影响。
   回滚：设回 `1` 重启即可。
2. `file_index` 增列 `remote_url`（同样的「CREATE 全列 + ALTER 忽略 duplicate column」）。
   URL 注册走 `RegisterFile(FileRecord{RemoteURL: rawURL})` 统一写：写本地成功则
   `path=fullPath` + `remote_url`，失败则 `path=''`（URL-only 行，语义与老的
   `provider_type='http'` 一致）。`remote_url` 采用 **first-writer-wins**：同 hash
   的多条 URL 内容等价，后写入不比先写入更正确，覆盖反而可能丢掉仍可访问的地址。
3. 读方切换：`FileService.ReadFile` 与 `downloader.HTTPURLFetcher` 优先读
   `file_index.remote_url`，再兜底 legacy `file_providers` 的 http 行（废弃窗口内的老库
   仍可达），重复 URL 去重；SSRF 闸门与大小上限不变。
4. `GetMeta` / `GetMetaByCID` 去掉 `file_meta` 兜底，index 唯一来源；保持旧仓库的
   `(nil, nil)` 未命中约定。空 `path` 不再等于未索引。
5. 新增 `repository.BackfillRemoteURLsFromLegacy()`：把老库 `provider_type='http'` 的
   行迁到 `file_index.remote_url`（已有行就地 UPDATE 空值；纯 URL 行新建 `path=''`
   的 index 行，`origin='legacy_backfill'`），忽略 `available=0`，同 hash 取 id 最小者，
   幂等。与 Step 1 的回填分开：那一步按设计拒绝索引无法本地服务的行，而 URL-only
   行恰恰没有本地字节。
6. 启动自检：`serverapp/builder.go` 依次跑本地回填 → URL 回填 →
   `CheckFileIndexConsistency`，`onlyInMeta != 0` 时 `LogWarn`（不中断启动，允许人工
   迁移中途状态）。
7. 测试：`repository/file_backfill_test.go`（镜像关断写旧表、`remote_url` 先到先得、
   URL 回填四条契约）、`service/file_convergence_test.go`（无 index 行的 legacy 元数据
   不可读、URL-only 行可被 `GetMeta` 读到、`ListAll` 投影 `provider_type='http'`）、
   `downloader/universal_downloader_test.go`（`urlsFor` 的顺序/去重/忽略不可用/老库兼容
   + 仅 `remote_url` 也能端到端拉到字节）。
8. **观察一个发布周期再做 Step 3**：
   `sqlite3 peerdrive.db "SELECT origin, COUNT(*) FROM file_index GROUP BY 1"` 与
   `SELECT COUNT(*) FROM file_meta` 做基线；灰度节点关 `PEERDRIVE_LEGACY_META_MIRROR=0`
   后确认 `file_meta` 行数不再增长、`file_index` 行数与 `origin='unified'` 正常增长。

**Step 3（删表，单独 PR + release note）**：
- 前置：`FileService.CopyFile`（`POST /files/copy`，WebDAV MOVE 等价）目前用
  `InsertFileProvider(hash, "local", relPath)` 表达「同一 hash 的第二个本地副本」，而
  `file_index` 每个 hash 只有一个 `path`，直接改写会把索引指向副本、丢掉原始路径。
  需要先定模型（多副本表，或接受「副本即新 canonical path」），本步不做。
- 删除前 dry-run：`sqlite3 peerdrive.db ".dump file_meta file_providers" > backup-m2.sql`，
  再跑一次 `BackfillFileIndexFromLegacy` 确认 `imported=0`。
- 新 `initTables` 不再 `CREATE TABLE file_meta/file_providers`；删 `file_repo.go`、
  `model.FileMeta/FileProvider`、`UpdateFileMetaFilename`、`GetFileProviders`、
  `MarkProviderUnavailable`、`DeleteFileMetaAndProviders`、`model.FileListItem.ProviderType/Path`
  的旧语义（改为从 index 投影）。
- 删除旧库残留表（可选）：在确认备份与回滚版本后，用一次性脚本 `DROP TABLE file_meta;
  DROP TABLE file_providers;`；本步不可逆，必须与 Step 2 至少隔一个发布周期。

**回滚**：
- Step 1：镜像与旧行都在，回退旧二进制即可继续读 `file_meta`；`file_index` 新增列对旧
  代码无影响（旧代码不 SELECT 它们）。
- Step 2：把 `PEERDRIVE_LEGACY_META_MIRROR` 设回 1 并重启；index-first 读取不受影响。
- Step 3：只能从上述 dump 恢复两张表；因此删表必须单独 PR、单独发布、保留 dump。

## API Endpoint Simplification

| # | Item | Status | Risk |
|---|------|--------|------|
| A1 | `/files/upload` → `/collections/` | ✅ Deprecated (after 2026-08-17 admin migration, no longer mandatory) | Medium |
| A2 | `/files/register_local` → `/collections/` | ✅ Deprecated (same as above) | Medium |
| A3 | `/files/register_url` → `/collections/` | ✅ Deprecated (same as above) | Medium |
| A4 | `/files/register_folder` → `/collections/` | ✅ Deprecated (same as above) | Medium |
| A5 | `/actions/*` → `/collections/` | ✅ Merged + 301 redirect | Medium |
| A6 | `/download/` vs `/sha256sum/` | ✅ Distinguished: sha256sum=local, download=multi-protocol | Medium |

> A1-A4 explanation: After 2026-08-17 frontend fully migrated to local WS admin frames (forwarded internally via transport/admin.go),
> frontend no longer directly calls these HTTP endpoints; endpoints retained per legacy compatibility policy (old frontend/curl/external
> scripts + integration tests using HTTP directly), consistent with router.go comments. Merging into `/collections/` has no benefit,
> see REFACTOR.md §3.10.

## Frontend Cleanup

| # | Item | Status | Risk |
|---|------|--------|------|
| F1 | `Explorer.jsx` merge into `AnonExplorer` | ❌ Not done | Medium |
| F2 | `CollectionBuilder.jsx` | ✅ Removed | Low |
| F3 | `AnonCollectionManager.jsx` | ✅ Removed | Low |
| F4 | Navbar search "files" section | ✅ Removed | Low |

## Miscellaneous

| # | Item | Status | Risk |
|---|------|--------|------|
| X1 | package.json residual | ✅ Cleaned up | Low |
