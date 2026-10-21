# repository —— data aspect (AOP ⑤)

> One-line responsibility: the SQLite persistence layer — all 14 tables in `back/internal/repository/`:
> table creation,
> migration, and CRUD; among them the `file_index` table (sha256→absolute path + a seq monotonic cursor) is the data
> foundation for the file
> index verb's incremental sync, and the other tables support the business core's file/collection/user/share/pin/
> task/local-sync persistence.

- Layer belonging: AOP ⑤ data aspect (`doc/LAYERS.md` §1)
- Dependency direction: `model ← repository ← provider ← service ← controller ← router ← cmd`
  —— repository depends only on `internal/model` and `pkg/hashutil`, and is consumed by service and
  transport (transport's `file_index.go` references this layer directly for index persistence)
- All table-creation DDL is concentrated in `db.go`'s `InitDB`, with a single file managing the whole schema

---

## Responsibilities

1. **Centralized SQLite schema management and idempotent migration**: `InitDB(dbPath)` opens the connection (the global singleton
   `DB`), creates all tables, runs `ALTER TABLE` migrations, and calls `InitShareTable` /
   `createFileIndexTable`.
2. **File content-addressed registration**: `file_meta` (hash PK: size/mime/gziped/filename/type/cid)
   + `file_providers` (hash → provider_type + path, multiple replicas, an available flag) ——
   the file service's metadata/location registry; anonymous collections, BT completion callbacks, and URL providers all write here.
3. **File index incremental sync**: the `file_index` table (sha256 → absolute path + name/size/deleted/
   seq/timestamps), where the `seq` monotonic cursor supports the peer `sync` verb's metadata incremental sync;
   tombstones (`deleted=1`) ensure deletions are also synced. **This is this layer's core contribution to the new architecture (the frame protocol file
   index verbs)**.
4. **User/collection/share/pin/task/local-sync**: six business table groups + their corresponding repos, covering register/login
   (authkey), collections (with version snapshot rollback), share links (30-day expiry), IPFS pin,
   asynchronous tasks, and collection→local-disk sync status.
5. **Anonymous collection content-addressed persistence**: `anon_repo.go` serializes the `AnonCollection` JSON and
   writes it with the `{storageDir}/{hash[:2]}/{hash}` layout (same as the file CAS layout), and registers
   `file_meta` + a local provider synchronously, so anonymous collections can be retrieved through the ordinary file fetch path.

## Module inventory (each file: filename + one-line responsibility + key exports)

### `db.go` —— database initialization + full table-creation DDL + idempotent migration

| Key exports | Description |
|---|---|
| `DB *sql.DB` | The global connection (`sql.Open("sqlite3", ...)`), used directly by all repo functions |
| `InitDB(dbPath string) error` | Open the connection + run the schema + migrate; tests often use `:memory:` |
| `migrationExec(stmt string)` | The idempotent migration executor: a duplicate column is only logged at debug, a real error is logged at warn (L9) |
| `FileTypeBlob` / `FileTypeAnonCollection` | Aliases after moving up to the `model` package (the M2 layer collapse) |

Table creation list (14 tables): `users`, `local_collection_sync`, `local_sync_files`,
`file_meta`, `file_providers`, `collections`, `collection_entries`,
`collection_versions`, `version_entries`, `transfer_tasks`, `download_progress`,
`share_links` (`InitShareTable`), `ipfs_pins`, `file_index` (`createFileIndexTable`).

### `file_index_repo.go` —— the file index (sha256→path + the seq cursor)

| Key exports | Description |
|---|---|
| The `FileIndex` struct | `Hash/Path/Name/Size/Deleted/Seq/CreatedAt/UpdatedAt` |
| `UpsertFileIndex(hash, path, name, size, deleted) (seq, error)` | Register/update the mapping; **seq is `MAX+1` inside a single transaction** (the M10 concurrency fix), delete also goes through it |
| `GetFileIndex(hash)` | Look up a non-deleted mapping (tombstones are only exposed via `ListFileIndexSince`) |
| `ListFileIndex(offset, limit)` | The full list (`deleted=0`, seq descending, default limit 1000) |
| `ListFileIndexSince(since)` | Incremental sync: `seq > since` ascending, **LIMIT 1000 as a backstop** (prevents a stale remote cursor materializing the whole table) |
| `DeleteFileIndex(hash)` | Logical delete = `UpsertFileIndex(..., deleted=true)` returning a new seq |

### `file_repo.go` —— file metadata + storage location (file_meta / file_providers)

| Key exports | Description |
|---|---|
| `GetFileMeta(hash)` / `GetFileMetaByCID(cid)` | Lookup; not found returns `(nil, nil)` |
| `InsertFileMeta(meta)` | On insert, computes the CID automatically with `hashutil.SHA256ToCID` |
| `GetFileProviders(hash)` | The list of available providers, **local first** (`CASE provider_type` ordering) |
| `InsertFileProvider(hash, type, path)` / `MarkProviderUnavailable(id)` | Register/invalidate a replica |
| `ListAllFiles(sortBy)` | The blob list, supporting time/path/name/type/size sorting, **LIMIT 1000** (M11) |

### `collection_repo.go` —— user collections + version snapshots (4 tables)

| Key exports | Description |
|---|---|
| `CreateCollection` / `CreateCollectionWithVisibility` / `WithTags` / `WithFull` | Create a collection (public/unlisted/private + tags + follow_redirects) |
| `GetOrCreateCollection` | Look up, auto-creating if it does not exist |
| `ListCollections` / `ListPublicCollections` / `GetCollection` / `SearchCollections` | Lookup; all carry a LIMIT (1000/100, M11) |
| `UpdateCurrentHash` / `UpdateCollectionTags` / `SetCollectionVisibility` | Update attributes |
| `AddCollectionEntry` / `AddProviderCollectionEntry` | upsert an entry (`ON CONFLICT(collection_id,path)`, including `providers_json`) |
| `RemoveCollectionEntry` / `GetCollectionEntry` / `ListCollectionEntries` | Entry add/remove/lookup (list LIMIT 10000) |
| `CreateVersion` / `SnapshotVersionEntries` / `GetVersionLog` / `GetVersionEntries` / `RestoreVersionEntries` | Version snapshots: snapshot at commit; rollback deletes-then-inserts **inside a transaction** |

### `user_repo.go` —— user authentication

| Key exports | Description |
|---|---|
| `UserRepository` + `NewUserRepository()` | Empty struct instantiation (the only repo that goes through methods) |
| `ErrUserNotFound` / `ErrUserExists` | Package-level sentinel errors |
| `CreateUser` / `GetByUsername` / `GetByAuthKey` / `UpdateAuthKey` / `ClearAuthKey` | users table CRUD (authkey = a long-lived token) |

### `anon_repo.go` —— anonymous collection content-addressed storage

| Key exports | Description |
|---|---|
| `SetAnonStorageDir(dir)` | The package-level default storage directory |
| `SaveCollection(coll, storageDir) (hash, error)` | Sort entries by path then JSON-serialize → sha256 → write `{dir}/{h[:2]}/{h}` → register meta+provider |
| `GetAnonCollectionByHash(hash, storageDir)` | Read back + deserialize; **`IsValidSHA256` before joining the path** (prevents hash[:2] out-of-bounds / path escape) |
| `ListAnonCollections(storageDir)` | The file_meta list where type=anon (LIMIT 1000) + reading the JSON per row to fill the friendly_name preview |

### `pin_repo.go` —— IPFS pin management (ipfs_pins)

| Key exports | Description |
|---|---|
| `InsertPin(cid, hash, filename, size)` | upsert (ON CONFLICT(cid) update) |
| `ListPins()` / `GetPin(cid)` / `RemovePin(cid)` / `PinExists(cid)` | Lookup/delete (list LIMIT 1000, M11) |

### `share_repo.go` —— share links (share_links)

| Key exports | Description |
|---|---|
| `CreateShare(hash, shareType, filename)` | A 16-byte random token (`crypto/rand`) + 30-day expiry |
| `GetShareByToken(token)` | Only looks up unexpired ones; `sql.NullTime` handles NULL/time explicitly (the L7 fix) |
| `ListShares()` | Unexpired links descending, LIMIT 100 |
| `InitShareTable()` | Table creation (called during the `InitDB` migration phase) |

### `sync_repo.go` —— collection→local-disk sync status (2 tables)

| Key exports | Description |
|---|---|
| `SyncRepository` + `NewSyncRepository()` | Instantiation (injected into SyncService by router assembly) |
| `UpsertSyncState` / `GetSyncState` | The collection sync configuration (local_path + include/exclude filter JSON) |
| `UpsertFileSyncState` / `GetSyncFiles` / `ClearSyncFiles` | The per-file is_saved flag (list LIMIT 1000, M11) |

### `task_repo.go` —— asynchronous tasks (transfer_tasks)

| Key exports | Description |
|---|---|
| `CreateTask(type, params) (id, error)` | Create a task (status='pending') |
| `UpdateTaskStatus(id, status, result)` | Update the status + refresh `updated_at` automatically |
| `GetTask(id)` | Lookup; not found returns `(nil, nil)` |

## Key mechanisms

### 0. Table structure quick reference (key columns)

| Table | Key columns | Purpose |
|---|---|---|
| `file_index` | hash PK / path / name / size / deleted / **seq** (index idx_file_index_seq) / created_at / updated_at | sha256→absolute path + the sync cursor (the core of the new architecture) |
| `file_meta` | hash PK / size / mime_type / gziped / filename / type (blob\|anon) / cid | File content metadata (content-addressed registration) |
| `file_providers` | id / hash FK→file_meta / provider_type (local\|http) / path / available | File storage location (multiple replicas, can be marked invalid) |
| `collections` | id / username / collection_name / current_hash / visibility / tags / follow_redirects / UNIQUE(username, collection_name) | User collections (current_hash points at the newest anonymous snapshot) |
| `collection_entries` | id / collection_id FK / path / file_hash / providers_json / UNIQUE(collection_id, path) | Collection workspace entries |
| `collection_versions` | id / collection_id FK / version_number / commit_message / parent_version_id | Version snapshot records (supports a parent version chain) |
| `version_entries` | id / version_id FK / path / file_hash / providers_json | Version snapshot content |
| `users` | id / username UNIQUE / password_hash / authkey UNIQUE | Registered users (authkey = a long-lived token) |
| `share_links` | id / token UNIQUE / hash / type / filename / expires_at | Share links (30-day expiry) |
| `ipfs_pins` | cid PK / hash / size / filename / pinned_at | IPFS pin cache registration |
| `transfer_tasks` | id / type / status / params / result | Asynchronous task tracking |
| `download_progress` | hash PK / total_size / received_size / chunks_* / peers_used | Download progress (legacy, with no active writer) |
| `local_collection_sync` | collection_hash PK / local_path / include_filter / exclude_filter / synced_at | The collection sync configuration |
| `local_sync_files` | id / collection_hash FK / file_path / is_saved / UNIQUE(collection_hash, file_path) | The per-file sync status |

### 1. The seq monotonic cursor (the foundation of file_index incremental sync)

`file_index.seq` increments by 1 on each upsert/delete. `UpsertFileIndex` puts
`SELECT COALESCE(MAX(seq),0)` and the INSERT into **the same write transaction**
(file_index_repo.go:42-68), relying on SQLite single-writer serialization to guarantee that two concurrent requests do not
read the same MAX and produce a duplicate seq (the M10 pitfall, see "Pitfalls and design decisions"). Consumers:

- `Create` (registering an external file), `upload` completion (after the disk write) in `transport/file_index.go`,
  `Delete`, and `ApplySync` (merging a peer's incremental, file_index.go:423-428) all read/write the cursor
  through this layer's
  `UpsertFileIndex` / `ListFileIndexSince`;
- the peer `sync{seq}` verb → `ListFileIndexSince(since)` fetches the incremental (including tombstones) →
  `ApplySync` merges; `GetFileIndex` is blind to tombstones (only exposed incrementally), guaranteeing
  "a deleted file does not resurrect in info/list".

### 2. The idempotent migration strategy (migrationExec)

In `InitDB`, upgrading an old database relies on an `ALTER TABLE ... ADD COLUMN` sequence. A duplicate column
on a repeated run is the expected result —— `migrationExec` logs such errors only at debug;
**other errors (missing tables, IO failures) are logged at warn to leave a trace** (the L9 fix: originally all ALTER errors
were silently swallowed, leaving no way to investigate a real migration failure). New tables use `CREATE TABLE IF NOT EXISTS`
for natural idempotency and need no migration logic.

### 3. Anonymous collection = file (content-addressed dual write)

`SaveCollection` treats the collection JSON as an ordinary file: sort entries → serialize → sha256 →
write `{storageDir}/{hash[:2]}/{hash}`, and simultaneously `InsertFileMeta` (Type=AnonCollection)
+ `InsertFileProvider("local")`. Benefit: an anonymous collection hash is just an addressable file hash,
and the download path (LocalFetcher/CAS reading) has zero special branches; `GetAnonCollectionByHash` reads the
JSON back in reverse. `ListAnonCollections` is the only "batch file read" query —— each row does another
`os.ReadFile` to fill friendly_name/version/preview, so file IO × N, hence LIMIT 1000.

## Relationships with other modules

```
transport (file_index.go / inbound.go) ──► repository (the file_index family)
service (collection/share/task/pin/sync/anon/file) ──► repository
downloader (universal_downloader.go) ──► repository (file_providers readback)
controller ── (after the M2 layer collapse, importing repository directly is forbidden; everything goes through service)
```

- **transport**: all persistence of the file index verbs goes through this layer (see mechanism 1).
- **service**: after the M2 layer collapse the controller no longer touches repository, and business persistence is unified into
  service (CollectionService/ShareService/TaskService/PinService/SyncService/
  FileService); this layer is the sole data exit for those services.
- **downloader**: `LocalFetcher`'s third lookup path reads the DB's "local" provider,
  and `HTTPURLFetcher` reads the "http" provider (use `MarkProviderUnavailable` when a provider goes invalid).
- **provider/anon**: `anon_repo.SaveCollection` dual-writes to file_meta/providers,
  sharing the content-addressed layout with file_repo.

## Pitfalls and design decisions

| Number | Pitfall | Fix |
|---|---|---|
| M10 | `nextFileIndexSeq()` did SELECT MAX then a separate INSERT —— with multiple pool connections writing concurrently, they read the same MAX → duplicate seq, breaking the sync cursor | SELECT+INSERT merged into one transaction (SQLite serialized writes guarantee monotonicity), file_index_repo.go:42-68 |
| L9 | The original migration silently swallowed all ALTER errors —— a duplicate column on a repeated migration is expected, but missing tables/disk failures were swallowed too | `migrationExec` distinguishes: a duplicate column logs at debug, the rest log at warn to leave a trace |
| M11 | A batch of list queries had no LIMIT —— with many files / a maliciously built large collection, the whole table would be materialized into memory (DoS) | `ListAllFiles` 1000, `ListCollections` 1000, `SearchCollections` 100, `ListCollectionEntries`/`GetVersionEntries` 10000, `GetVersionLog` 1000, `ListAnonCollections` 1000, `ListPins` 1000, `GetSyncFiles` 1000 |
| L7 | `GetShareByToken` originally scanned expires_at into `*any` —— the driver's return type is uncertain (time.Time or string), and on an assertion failure the expiry time was silently empty | Changed to `sql.NullTime` to handle NULL/time explicitly |
| Path traversal | `GetAnonCollectionByHash`'s hash may come from a URL/request body/remote sync; without validation, `hash[:2]` panics out of bounds and `..` escapes the storage directory | `hashutil.IsValidSHA256` first, then join the path |
| Layer belonging | The controller once wrote SQL directly (ListPublicCollections) | The M2 layer collapse converged it into repository; the controller depends only on service |
| Compatibility | The FileType constants moved to the model package | repository keeps the aliases `FileTypeBlob`/`FileTypeAnonCollection` to avoid a diff explosion |

## Tests (11 unit tests, the L5 section of `scripts/test-layers.sh`)

> Command: `go test -tags nosqlite ./internal/repository/...`

### `collection_repo_test.go`

> Note: legacy code tests (see doc/archive/LEGACY.md), with no per-test background of discovery annotated; the "background of discovery"
> convention applies to new code (file header comments).

- `TestCollectionRepo_GetOrCreate`: GetOrCreate is idempotent —— the same username+collection name returns
  the same ID.
- `TestCollectionRepo_CreateWithVisibility`: a collection created with visibility can be looked back up.
- `TestCollectionRepo_EntriesCRUD`: the full entry add/remove/lookup flow (2 entries → delete 1).
- `TestCollectionRepo_VersionFlow`: CreateVersion increments the version number from 1 + GetVersionLog.
- `TestCollectionRepo_ListAndSearch`: ListCollections and SearchCollections hit.
- `TestCollectionRepo_Tags`: tag creation and update.

### `file_repo_test.go`

> Same as above, legacy tests, no background of discovery annotated.

- `TestInsertFileMetaAndGetFileMeta`: meta write/read matches on all fields.
- `TestGetFileMetaNonexistent`: not found returns `(nil, nil)` rather than an error.
- `TestInsertFileProviderAndGetFileProviders`: multiple providers registered, sorted with **local first**.
- `TestMarkProviderUnavailable`: after marking invalid, it is no longer returned.
- `TestGetFileProvidersEmptyForNonexistentHash`: an unregistered hash returns empty.

## File inventory

| File | Responsibility |
|---|---|
| `db.go` | Initialization + full table-creation DDL + idempotent migration (L9) |
| `file_index_repo.go` | sha256→path mapping + the seq cursor (the M10 transaction fix) |
| `file_repo.go` | file_meta / file_providers CRUD |
| `collection_repo.go` | The 4 collection tables + version snapshot rollback |
| `user_repo.go` | users table CRUD (authkey) |
| `anon_repo.go` | Anonymous collection content-addressed read/write (including path defense) |
| `pin_repo.go` | ipfs_pins CRUD |
| `share_repo.go` | Share links (token + expiry) |
| `sync_repo.go` | The 2 local sync status tables |
| `task_repo.go` | Asynchronous task CRUD |
| `collection_repo_test.go` | legacy tests (see doc/archive/LEGACY.md) |
| `file_repo_test.go` | legacy tests (see doc/archive/LEGACY.md) |
