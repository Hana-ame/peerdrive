# Connection 04: service ↔ repository (DB read/write)

- **Modules involved**: [../modules/06-service.md](../modules/06-service.md) and [../modules/02-repository.md](../modules/02-repository.md)
- **Code locations**: A side `back/internal/service/` (collection_service.go / share_service.go / pin_service.go / file_service.go / sync_service.go / anon_service.go); B side `back/internal/repository/` (db.go / file_repo.go / collection_repo.go / share_repo.go / sync_repo.go / pin_repo.go / anon_repo.go / file_index_repo.go)
- **Direction**: **A→B one-way**. The service package imports the repository package and calls its package-level functions; repository never callbacks service and has no service interface as a parameter. **No B→A**. The only exception is the transport package (not A side) also directly calling `repository.UpsertFileIndex` (see [11-transport-storage.md](11-transport-storage.md)), but that's another A→B channel outside this connection.

## 1. Connection Method

### 1.1 Channel and Call Form

- **In-process function call**, no network protocol, no queue, no IPC. Most of B side are **package-level functions** (`repository.GetFileMeta`, `repository.InsertFileMeta`…), not interface or struct methods — service uses `import "peerdrive/internal/repository"` and directly calls the package name (`back/internal/service/collection_service.go:7-10`, `back/internal/service/file_service.go:22`).
- **Sole struct-ification exception**: `SyncRepository` is an empty struct (`back/internal/repository/sync_repo.go:10-15`), instantiated by `repository.NewSyncRepository()` during router assembly and injected into `service.NewSyncService` (`back/internal/router/router.go:211-212`). This is to allow `SyncService` to depend on injectable test doubles; no capability difference in form.
- **Shared handle**: the actual handle interacting with SQLite is B side's package-level global variable `var DB *sql.DB` (`back/internal/repository/db.go:35`). A side doesn't hold `*sql.DB`, all access is indirect through B side functions. The entire process has only this one DB handle, no connection/handle passing, and no `BeginTx(ctx,...)` passed down from upper layers.
- **Parameter/return format**: Go native types + domain structs from `peerdrive/internal/model` (`model.FileMeta`, `model.Collection`, `model.ShareLink`, `model.IPFSPin`, `model.AnonCollection`…). Repository retains type aliases (`FileTypeBlob = model.FileTypeBlob`, `back/internal/repository/db.go:28-33`) for compatibility with service-side `repository.FileTypeBlob` usage (`back/internal/service/file_service.go:583`).
- **Query parameterization**: all use `?` placeholders + explicit parameters, no string concatenation — including `WHERE hash = ?`, `WHERE token = ?`, `WHERE collection_hash = ?` etc.; `LIKE` also puts `"%"+q+"%"` in the parameter value rather than the statement (`back/internal/repository/collection_repo.go:141-143`).
- **Authentication**: this layer **has no authentication concept**. Repository doesn't perceive the caller's identity; `GetShareByToken` only validates whether the token is within its validity period (`back/internal/repository/share_repo.go:43-57`, `expires_at > datetime('now')`), not who the requester is. Authentication happens in the controller/middleware layer upstream of A side; this connection's inputs are already "business parameters approved by upper layers".
- **Visibility**: collection's `visibility` (public/unlisted/restricted) is a **data field** rather than an authentication field — `ListPublicCollections`'s `WHERE visibility = 'public'` filter (`back/internal/repository/collection_repo.go:141`) is data-plane trimming, not access control; cross-user reads may still be permitted at other layers.

### 1.2 Connection Establishment and Lifecycle (one-time, startup phase)

- **When established**: during process startup phase, completed once before any service assembly. `main.go` order: `repository.InitDB(cfg.DBPath)` → `defer repository.CloseDB()` → `repository.SetAnonStorageDir(storageDir)` (`back/internal/serverapp/app.go:76-87`). After that, all service constructions (`NewFileService(cfg)` etc., `back/internal/router/router.go:121-148`) no longer touch DB.
- **Why it must be before services**: service construction is cheap (only saves `cfg/storageDir`), but repository's table creation/migration must be completed, otherwise the first CRUD will hit an empty database. `InitDB` internally calls `DB.Ping()` immediately after opening (`back/internal/repository/db.go:111-113`) — because `sql.Open` is lazy; without Ping, you can't know if the path/driver is actually usable.
- **Driver one-of-two (compile time)**: when `cgo`, registers `sqlite3` (mattn/go-sqlite3); when `!cgo`, registers `sqlite` (modernc.org/sqlite) (`back/internal/repository/db_driver_cgo.go:14-16`, `back/internal/repository/db_driver_pure.go:15-17`). The repository release process cross-compiles with `CGO_ENABLED=0` for all platforms (`db_driver_cgo.go:7-12` comment); without cgo, mattn degrades to a stub causing "startup dies in InitDB table creation", so the driver name can't be hardcoded.
- **DSN suffix**: `dsn()` (`back/internal/repository/db.go:79-84`) appends `dsnSuffix()` to dbPath. The two drivers have different syntax: cgo uses `?_busy_timeout=5000&_foreign_keys=1`, pure uses `?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)` (`db_driver_cgo.go:29`, `db_driver_pure.go:22`). In-memory DBs and `file:`/`data:` URI prefixes skip the suffix to avoid breaking URI structure.
- **Why `busy_timeout` is mandatory**: SQLite's default behavior when write lock conflicts is to immediately return `database is locked`. This process does **concurrent writes** (HTTP registration / upload persistence / BT completion callback / file_index cursor sync happening simultaneously); without it, it's all luck — single runs never reproduce, concurrent load causes occasional 500s. 5 seconds is sufficient to cover normal transaction duration (`db_driver_cgo.go:21-24` comment).
- **Why `foreign_keys` is explicitly enabled**: SQLite defaults to foreign keys disabled; the schema has `ON DELETE CASCADE` written (collection_entries → collections etc.); without enabling, these cascades are all paper constraints — deleting a collection leaves orphan entries (`db_driver_cgo.go:26-28` comment).
- **Connection pool**: `isMemoryDB` (`back/internal/repository/db.go:71-73`, checks `:memory:` or `file::memory:`) uses different parameters — in-memory DB has each connection as an independent empty database, so `SetMaxOpenConns(1)` / `SetMaxIdleConns(1)` / `SetConnMaxLifetime(0)`; file DB uses `SetMaxOpenConns(8)` / `SetMaxIdleConns(4)` / `SetConnMaxLifetime(30*time.Minute)` (`back/internal/repository/db.go:98-106`). Comment explicitly states "SQLite concurrent write conflicts are queued by busy_timeout".
- **Schema and migration**: one `CREATE TABLE IF NOT EXISTS` covers 12 tables (`back/internal/repository/db.go:115-203`), followed by `migrationExec` idempotent `ALTER` to add columns (`db.go:209-215`), finally `InitShareTable()` + `createFileIndexTable()` (`db.go:216-227`). Migration error handling: `duplicate column` only logs debug, other errors log warn for traceability (`back/internal/repository/db.go:231-241`, L9 fix "silently ignoring all ALTER errors").
- **Close**: `CloseDB` closes DB and sets to nil, idempotent (`back/internal/repository/db.go:45-52`). Comment points out it exists for tests — on Windows, opened db files can't be deleted; test cleanup needs it; production relies on `main.go`'s defer.

### 1.3 Transaction Boundaries (all on B side, only two explicit `Begin`)

CRUD in repository is **mostly single-statement autocommit**; only two functions explicitly use `DB.Begin()` + `defer tx.Rollback()`:

1. **`RestoreVersionEntries`** (`back/internal/repository/collection_repo.go:338-360`): `DELETE FROM collection_entries WHERE collection_id=?` then re-insert version snapshot entries one by one, must be in the same transaction — crashing between delete and insert loses entries. This is the only atomicity guarantee for collection rollback.
2. **`UpsertFileIndex`** (`back/internal/repository/file_index_repo.go:42-68`): `SELECT COALESCE(MAX(seq),0)+1` gets cursor + `INSERT ... ON CONFLICT DO UPDATE`; comment explicitly states "M10: two steps must be in the same transaction, otherwise concurrency yields duplicate seq; SQLite single-writer guarantees monotonicity within a transaction" (`file_index_repo.go:39-41`). This is the only guarantee for file_index incremental sync cursor monotonic increase.

Apart from these, there are **no service-level transactions**: `FileService.Upload` first does `InsertFileMeta` then `InsertFileProvider` (`back/internal/service/file_service.go:585-593`); crashing between the two steps leaves a "metadata without provider" half-registered row — but `ListAllFiles` uses `LEFT JOIN`, so half-registered rows can still be listed (just without available provider), making this acceptable eventual consistency. Service layer doesn't hold `*sql.Tx` and has no interface passing transaction handles to repository.

`Delete` (`back/internal/service/file_service.go:618-645`) is the only place where service **directly** calls `repository.DB.Exec` (`:641-642`, `DELETE FROM file_providers` + `DELETE FROM file_meta`) — this is a M2 layer-discipline not yet fully closed legacy point: the two-step delete has no transaction wrapper and bypasses repository's function encapsulation.

### 1.4 `InsertFileMeta` vs `file_index` Division of Labor (two independent indexes that don't fill each other)

This is the most easily misread point, written out clearly:

| | `file_meta` + `file_providers` (old) | `file_index` (new) |
|---|---|---|
| Writer | `repository.InsertFileMeta` (`back/internal/repository/file_repo.go:47-55`, auto `hashutil.SHA256ToCID` adds `cid`) + `InsertFileProvider` (`:81-87`) | `repository.UpsertFileIndex` (`back/internal/repository/file_index_repo.go:42-68`) |
| Write path callers | `FileService.Upload` (`file_service.go:585-593`), `ImportGatewayData` (`:71-77`), `RegisterBTFile` (`:107-117`), `PinService.InsertMeta` (`pin_service.go:37-45`), `SaveCollection` (`anon_repo.go:52-60`) | `transport.FileIndexService.Create` (`back/internal/transport/file_index.go:238`), `Complete` (`:461`), after `PeerPuller` disk write |
| Semantics | **Content metadata + multi-provider replicas** (hash is PK; provider is one of "local file path / http URL / sha256" sources; query sorts by local priority `file_repo.go:62`) | **sha256 → absolute path mapping + monotonic seq cursor** (for P2P `sync` verb incremental sync; `deleted` tombstone only exposed via `SyncSince`, `file_index_repo.go:101-117`) |
| Consumers | HTTP list/download/visibility (`ListAllFiles`, `GetFileProviders`, `ListAnonCollections`) | P2P index sync (`GetFileIndex` / `ListFileIndex` / `ListFileIndexSince` / `DeleteFileIndex`), shared manifest (`app.go:170-181`) |
| Relationship | **Two independent registries, don't fill each other, no 1:1 mirror, no sync job** | Same as above |

This separation has executable evidence: `back/internal/repository/separation_proof_test.go:47-77` asserts "old path files only go into old table" (file_meta=1/file_providers=1 row, `GetFileIndex` can't find them), "new path files don't write old table" (file_meta=0/file_providers=0 rows), "row counts symmetric across both tables". Comment explicitly states this is fixing M2 architectural facts "as executable evidence", otherwise any claim about "files downloaded via BT can be pulled by peer" would need re-proof.

Practical implication: **if an upload only goes through `FileService.Upload` (old path), the peer can't find it by hash** — must go through the P2P path (transport's create/upload/sync verb) to enter `file_index`. This is the cost of `file_index` existing independently, and is also the implementation basis for README's "SQLite persistence + seq cursor incremental" statement.

### 1.5 Read/Write Limits (preventing full table materialization)

After M11, all B side `List*` have LIMIT: `ListCollections` 1000 (`collection_repo.go:118`), `SearchCollections` 100 (`:173`), `ListCollectionEntries` 10000 (`:298`), `GetVersionEntries` 10000 (`:319`), `ListShares` 100 (`share_repo.go:76`), `ListPins` 1000 (`pin_repo.go:43`), `GetSyncFiles` 1000 (`sync_repo.go:69`), `ListAllFiles` 1000 (`file_repo.go:119`), `ListFileIndex` default 1000 (`file_index_repo.go:87`), `ListFileIndexSince` 1000 (`:112`), `ListAnonCollections` 1000 (`anon_repo.go:101`). Comments consistently note "frontend pagination not implemented" — exceeding the limit silently truncates, frontend can't get the total count.

## 2. Timing

### 2.1 Startup Assembly Timing

```mermaid
sequenceDiagram
    participant Main as main.go
    participant Repo as repository (DB)
    participant Router as router.go
    participant Svc as service
    participant Ctrl as controller

    Main->>Repo: InitDB(cfg.DBPath)   [back/internal/serverapp/app.go:76]
    Repo->>Repo: sql.Open + dsnSuffix (busy_timeout=5000, foreign_keys=1)
    Repo->>Repo: SetMaxOpenConns/MaxIdle/ConnMaxLifetime
    Repo->>Repo: DB.Ping()            [db.go:111-113]
    Repo->>Repo: CREATE TABLE × 12    [db.go:115-203]
    Repo->>Repo: migrationExec × 6    [db.go:209-215]
    Repo->>Repo: InitShareTable + createFileIndexTable [db.go:216-227]
    Main->>Repo: SetAnonStorageDir(storageDir) [app.go:181]
    Main->>Router: SetupRouter(cfg, ...)
    Router->>Svc: NewFileService(cfg) / NewCollectionService() / NewShareService() / NewPinService() / NewAnonService(cfg) [router.go:121-148]
    Router->>Repo: NewSyncRepository() [router.go:211]
    Router->>Svc: NewSyncService(syncRepo, uniDl, storageDir) [router.go:212]
    Router->>Ctrl: InitFileController(fileSvc) etc. [router.go:124-128]
    Note over Main,Repo: defer CloseDB() registered at app.go:174; handle released on process exit
```

### 2.2 Typical Read Path (HTTP → DB)

Taking `/collections/{username}/{name}` query as example:

```
controller.Collection.Get
  → service.CollectionService.Get(username, name)        [back/internal/service/collection_service.go:19-21]
    → repository.GetCollection(username, name)           [back/internal/repository/collection_repo.go:161-168]
      → DB.QueryRow(SELECT ... WHERE username=? AND collection_name=?)
      → model.ScanCollection(rows)                       [back/internal/controller/collection.go]
      → sql.ErrNoRows → return (nil, nil)                [collection_repo.go:164-166]
```

Key point: service layer is **transparent forwarding** (`collection_service.go:1-4` comment: "method names correspond one-to-one with repository, only transparent forwarding (future caching/transactions go here)"), no parameter transformation. Query misses uniformly use the `(nil, nil)` convention; controller decides whether to return 404 or empty list.

### 2.3 Typical Write Path (file upload: disk write + two DB writes)

`FileService.Upload` (`back/internal/service/file_service.go:520-597`, key segments):

1. `os.Create(tmpName)` writes temp file → `hashFile` computes sha256 → `repository.GetFileMeta(hash)` dedup (`file_service.go:560-564`); hit returns `ErrFileAlreadyExists`.
2. `s.copyInto(s.allowedRoots(), tmpName, fullPath)` copies within allowed roots to `storageDir/<hash[:2]>/<hash>` (`:571-574`). **No longer `os.Rename`** — source is in system temp directory; rename would follow symlinks on dst parent directory (`:568-570` comment).
3. `repository.InsertFileMeta(meta)` (`:585-588`), Type takes `repository.FileTypeBlob`.
4. `repository.InsertFileProvider(hash, "local", relPath)` (`:590-593`).

Steps 3 and 4 are **not in the same transaction** and **don't write `file_index`** (see §1.4 division). Error handling is asymmetric: Upload path wraps both step errors as `%w` returns (`:586-592`), while `ImportGatewayData` uses `_ =` to silently ignore (`file_service.go:71-77`), `RegisterBTFile` only `log.LogWarn` (`:113-116`) — this is historical legacy difference; Upload is the "main path" so most strict.

### 2.4 Transaction Path (file_index incremental cursor)

`transport.FileIndexService.Create` (`back/internal/transport/file_index.go:205-244`) calls `repository.UpsertFileIndex`:

```
transport.FileIndexService.Create(path)                     [file_index.go:205]
  → s.OpenAllowed(path) / f.Stat() / RejectHardlink         [file_index.go:213-229] (TOCTOU defense, only resolve once)
  → hashReader(f) → h, abs, size
  → repository.UpsertFileIndex(h, abs, name, size, false)  [file_index.go:238]
      → DB.Begin()                                          [file_index_repo.go:46]
      → defer tx.Rollback()
      → SELECT COALESCE(MAX(seq),0)+1 FROM file_index       [file_index_repo.go:48-52]
      → INSERT ... ON CONFLICT(hash) DO UPDATE SET path,name,size,deleted,seq,updated_at
                                                            [file_index_repo.go:54-61]
      → tx.Commit()
      → return seq
  → return FileInfo{Hash, Path, Name, Size, Seq}
```

Incremental sync read side: `ListFileIndexSince(since)` (`back/internal/repository/file_index_repo.go:101-117`) uses `WHERE seq > ? ORDER BY seq ASC LIMIT 1000`; it's the underlying query for the `sync` verb allowing peers to "only pull incremental" (README: "SQLite persistence + seq cursor incremental"). `DeleteFileIndex` (`:120-122`) is not physical deletion, but `UpsertFileIndex(hash, "", "", 0, true)` writing a tombstone — read side `GetFileIndex` uses `WHERE hash=? AND deleted=0` to filter (`:71-75`); tombstones are only exposed to peers in `SyncSince`, so peers can also perceive delete events.

### 2.5 Collection Rollback Transaction

`service.CollectionService.RestoreVersion` (`back/internal/service/collection_service.go:126-128`) → `repository.RestoreVersionEntries` (`back/internal/repository/collection_repo.go:338-360`):

```
RestoreVersionEntries(versionID, collectionID)
  → entries, err := GetVersionEntries(versionID)            [collection_repo.go:318-335]
  → tx, err := DB.Begin()                                   [collection_repo.go:341]
  → defer tx.Rollback()
  → tx.Exec(DELETE FROM collection_entries WHERE collection_id=?)
  → for each entry: tx.Exec(INSERT INTO collection_entries (...providers_json) VALUES (...))
  → tx.Commit()
```

This is one of the two explicit transactions listed in §1.3; without a transaction between delete and insert, entries would be lost.

### 2.6 Sync Save (multi-file + per-entry status writeback, no transaction)

`SyncService.SaveToDisk` (`back/internal/service/sync_service.go:31-75`):

```
SyncService.SaveToDisk(req)
  → reject LocalPath containing ".."                         [sync_service.go:33-35]
  → repository.GetAnonCollectionByHash(req.CollectionHash, storageDir)  [sync_service.go:43]
  → s.filterFiles(entries, include, exclude)                  [sync_service.go:49]
  → s.syncRepo.UpsertSyncState(syncState)   INSERT ... ON CONFLICT DO UPDATE  [sync_service.go:58]
  → s.syncRepo.ClearSyncFiles(collectionHash)                 [sync_service.go:61]
  → for each entry:
      → s.saveFile(...) (via downloader fetch + os.WriteFile)  [sync_service.go:112-139]
      → s.syncRepo.UpsertFileSyncState(hash, path, true/false)  [sync_service.go:68-70]
```

The entire segment has **no transaction**: single file failure only writes back that file's `is_saved=0` (`:68`), remaining files continue. This is intentional eventual consistency — `SaveToDisk`'s return value only means "scheduling complete", not guaranteeing all success; progress is checked via `GetSyncFiles` querying `is_saved` (`sync_service.go:87-109`). `isSavedInt int` scanning then `f.IsSaved = isSavedInt == 1` (`sync_repo.go:78`) is the explicit conversion point for converting SQLite INTEGER 0/1 back to Go bool.

## 3. Case Handling

| Exception/Edge Case | Behavior & Rationale (code location) | Description |
|---|---|---|
| **Timeout (write lock queue)** | No application-layer timeout; `busy_timeout=5000` handed to SQLite via DSN suffix — write lock conflicts queue for up to 5 seconds, then return `database is locked` (`back/internal/repository/db_driver_cgo.go:21-29`, `db_driver_pure.go:19-22`). Read locks don't queue (WAL mode). | 5 seconds is sufficient for normal transactions; what's not covered are real failures like "some DDL getting stuck" — those manifest as controller returning 500, not this layer hanging. |
| **Timeout (query duration)** | No per-query timeout; relies on unified LIMIT on B side to control materialization volume (`collection_repo.go:118/173/298/319`, `share_repo.go:76`, `pin_repo.go:43`, `sync_repo.go:69`, `file_repo.go:119`, `file_index_repo.go:87/112`, `anon_repo.go:101`). Exceeding silently truncates. | Frontend pagination not implemented; truncation is invisible to users (module 02 §5 already noted this). |
| **Disconnect / Reconnect** | **Not applicable — local file DB has no network disconnect semantics**. Only connection pool level recycling: file DB `SetConnMaxLifetime(30*time.Minute)` (`db.go:98-106`); expired connections are discarded and next query reopens. Disk failures (disk full/path unwritable) are exposed in `InitDB`'s `Ping` (`db.go:111-113`); runtime disk failures manifest as CRUD returning `err`, not reconnection. | No runtime handle rebuild logic; process must restart to get a new DB path. |
| **Duplicate / Concurrent (write conflict)** | ①`file_meta.hash` is PK; duplicate `InsertFileMeta` returns PK conflict error (`file_repo.go:47-55`); Upload side first `GetFileMeta` dedup then insert (`file_service.go:560-564`), but **there's a race between dedup and insert** — concurrent same-hash uploads will have one side get PK conflict. ②Other upserts use `ON CONFLICT ... DO UPDATE`: `UpsertSyncState` (`sync_repo.go:18-32`), `UpsertFileSyncState` (`:53-62`), `InsertPin` (`pin_repo.go:15-27`), `AddCollectionEntry` (`collection_repo.go:191-198`), `UpsertFileIndex` (`file_index_repo.go:54-61`) — duplicate calls are idempotent. ③`GetOrCreateCollection` (`collection_repo.go:99-107`) is read-then-insert, not wrapped in transaction; concurrent same `(username, collection_name)` has a race, relying on `UNIQUE(username, collection_name)` constraint as fallback (`db.go:159`) — one side gets the unique constraint error. | The only monotonicity requirement is seq, solved by §1.3's transaction (M10 fix). Other concurrent writes are "winner writes", relying on PK/UNIQUE constraints rather than locks. |
| **Data missing or validation failure** | ①Query misses uniformly return `(nil, nil)`: `GetFileMeta` (`file_repo.go:22-24`), `GetFileMetaByCID` (`:38-40`), `GetCollection` (`collection_repo.go:164-166`), `GetSyncState` (`sync_repo.go:40-42`), `GetPin` (`pin_repo.go:59-61`), `GetFileIndex` (`file_index_repo.go:71-75`). ②`GetAnonCollectionByHash` rejects non-64-hex hash (`hashutil.IsValidSHA256`, `anon_repo.go:68-70` — comment: unvalidated `hash[:2]` would panic or read out of bounds); rejects `version < 1`; corrupted JSON returns error not panic. ③`GetShareByToken` silently returns empty row for expired token (`share_repo.go:47`'s `expires_at > datetime('now')`), doesn't distinguish "non-existent" from "expired". ④`ListAnonCollections` skips rows that can't be read from disk (`anon_repo.go:108`), doesn't fail entirely. | The `(nil, nil)` convention distinguishes "business non-existence" from "lower-level error"; caller can safely `if x == nil`. But the cost is caller forgetting to nil-check will nil-deref (service layer is mostly transparent forwarding, doesn't check). |
| **Auth failure** | **No authentication at this layer** — repository doesn't perceive the caller, no 401/403 semantics. The only approximate authentication is share token time-window validation (`share_repo.go:43-57`): token non-existent or expired both return empty row; controller layer then decides 404 or 403. Collection's `visibility` is a data field (`ListPublicCollections`'s `WHERE visibility='public'`), not access control. | Authentication responsibility is in A side upstream (controller/middleware); this connection assumes inputs are already approved. PSK/identity validation is in the transport layer (see [06-service-transport.md](06-service-transport.md)). |
| **Half-open state (DB handle unavailable)** | `Ping()` is the only probe: when `DB == nil`, returns `"database not initialized"` (`db.go:58-63`), used by `/ready` probe (module 02 §3.I, `back/internal/router/router.go:124`). But CRUD functions **have no nil guard** — calling `DB.QueryRow`/`DB.Exec` when `DB == nil` causes nil-pointer panic. After `CloseDB` (`db.go:45-52` sets nil), any CRUD will panic; only test environments actively close. Additionally, `SaveCollection` falls back to caller-provided `storageDir` when `anonStorageDir` is not set (`anon_repo.go:20-22`, `:25-61`), while `SetAnonStorageDir` is a one-time call during main assembly (`app.go:181`) — missing the call won't panic, but uses the fallback path, behavior diverges. | Half-open state's actual manifestation is **panic rather than error** — this is the sharpest boundary of this connection: no defensive checks, relying on the convention of "always InitDB at startup, never CloseDB at runtime". |
| **Half-open state (incomplete data)** | ①`Upload`'s `InsertFileMeta` succeeds but `InsertFileProvider` fails leaves a "metadata without provider" half-registered row (`file_service.go:585-593`, no transaction wrapper) — `ListAllFiles` uses `LEFT JOIN` so can still list (just without available provider, `file_repo.go:96-136`), eventual consistency. ②`SaveCollection` / `ImportGatewayData` / `RegisterBTFile` use `_ =` or `log.LogWarn` to **silently ignore** these two-step write errors (`anon_repo.go:52-60`, `file_service.go:71-77`, `:113-116`) — file already on disk but metadata not registered; peer can't find by hash. | These silent ignores are historical code from before M2 layer-closing; comments already point out "original logic was inline in controller, ignoring all errors". Fix direction is to propagate errors, but currently not done (otherwise BT/gateway downloads would fail entirely due to occasional DB jitter). |
| **Process restart** | ①SQLite is a disk file; `DBPath` comes from config (`app.go:170`); schema/migration idempotently re-runs (`migrationExec` tolerates `duplicate column`, `db.go:231-241`). ②`file_index`'s `seq` persists in the table; `MAX(seq)+1` is monotonic across restarts (`file_index_repo.go:48-52`) — incremental sync continues from the upstream seq saved by the peer, doesn't re-pull from scratch. ③`share_links.expires_at`, `ipfs_pins.pinned_at` etc. are all `DATETIME DEFAULT CURRENT_TIMESTAMP`, still present after restart. ④In-memory DB (`:memory:`) is lost on restart, test path only. ⑤`CloseDB` must be called on Windows to delete the db file (`db.go:37-44` comment) — production relies on `app.go:174`'s defer. ⑥Service-side own persistence (`joined_nodes.json` / `share_scope.json`) is read back by module 06 itself, unrelated to this connection. | No in-memory cache to invalidate: repository doesn't cache (`collection_service.go:3-4` comment "future caching/transactions go here" — hasn't been added yet). `file_index`'s index content is additionally read from the table by transport-side `FileIndexService` at startup as needed, no pre-warming step. |
| **Data corruption / migration failure** | `migrationExec` only logs debug for `duplicate column`, **other errors log warn but don't return** (`db.go:233-241`) — migration failure doesn't abort startup; continues running on old schema, manifesting as subsequent CRUD reporting `no such column`. Table creation failure (`CREATE TABLE`'s `DB.Exec(schema)`) returns error and aborts startup (`db.go:201-203`). | This is a deliberate trade-off: most migration errors are harmless repeats of "already applied", but real IO failures also can't let the entire process fail to start. The cost is "silently degrading to old schema", relying on logs for debugging. |

## 4. Related Documents

- [03-controller-service.md](03-controller-service.md): upstream — controller's M2 layer-discipline dependency on service only, and where this connection's "service transparent forwarding" comes from.
- [06-service-transport.md](06-service-transport.md): service's other A→B channel (NodeShare/NodeDirectory/PeerPuller ↔ transport); PeerPuller after disk write goes through transport to write `file_index` (see §1.4 division).
- [11-transport-storage.md](11-transport-storage.md): the adjacent surface where transport directly calls `repository.UpsertFileIndex` / `GetFileIndex` / `ListFileIndexSince` (`back/internal/transport/file_index.go:238/461/531/547/570/575`) — this connection's `file_index` writers are mostly in transport, not service.
- [09-controller-downloader.md](09-controller-downloader.md): download completion path triggers `RegisterBTFile` / `ImportGatewayData` timing for writing to `file_meta` + `file_providers` (`back/internal/service/file_service.go:85-119`, `:60-79`).
- [10-controller-storage.md](10-controller-storage.md): content-addressed disk layout `storageDir/<hash first 2 chars>/<hash>` and the double-write relationship with `InsertFileMeta` registration; path safety boundaries (`file_service.go:126-177`).
- [../modules/02-repository.md](../modules/02-repository.md): B side module document — 12 table listing, constraints and format conventions, "when to store" panorama.
- [../modules/06-service.md](../modules/06-service.md): A side module document — nine service components, M2 layer-discipline, why this layer's own persistence files (`joined_nodes.json` / `share_scope.json`) don't go into SQLite.
- [../modules/01-config.md](../modules/01-config.md): sources and defaults of `cfg.DBPath` / `cfg.StorageDir` / `cfg.DownloadDir` (determining where `InitDB` opens).
- [../modules/14-nodestate.md](../modules/14-nodestate.md): contrast of node state persistence unrelated to DB (which states deliberately don't go into SQLite).
