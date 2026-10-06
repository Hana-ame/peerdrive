# Module 02: repository Metadata Database (SQLite)

- **Code location**: `back/internal/repository`
- **One-line function**: The sole metadata persistence layer for the entire node — saves file metadata and replicas, collections and versions, share links, local sync state, IPFS pins, file index (sha256→absolute path), and incremental sync cursors to a single SQLite file; anonymous collections are persisted as content-addressed JSON files then registered into the database (`back/internal/repository/db.go:1-14`).
- **Dependencies**: `database/sql`; compile-time either-or SQLite driver — uses `mattn/go-sqlite3` with cgo, falls back to pure-Go `modernc.org/sqlite` without cgo (`back/internal/repository/db_driver_cgo.go:1-16`, `back/internal/repository/db_driver_pure.go:1-22`); `peerdrive/internal/log` (logging), `peerdrive/internal/model` (domain structs), `peerdrive/pkg/hashutil` (SHA256/CID validation and conversion) (`back/internal/repository/db.go:24-26`); and the external filesystem (anonymous collection JSON persisted as `<storageDir>/<hash[:2]>/<hash>`, see `back/internal/repository/anon_repo.go:40-49`).
- **Depended upon by**: `back/internal/serverapp/app.go` (starts `InitDB`/`SetAnonStorageDir`, closes `CloseDB`, `main.go:76-87`); `back/internal/service/*` (file/collection/anon/sync/pin/share/peerpull etc. all read/write through this, after M2 convergence repository is only referenced by service, see `back/internal/service/collection_service.go:1-4`); `back/internal/transport` (`FileIndexService` performs create/upload/delete/sync on file_index table, `back/internal/transport/file_index.go:238,461,570,575`); `back/internal/downloader` (download cache registration, `back/internal/downloader/universal_downloader.go:378-398`); `back/internal/source` (IPFS pin registration and deletion, `back/internal/source/ipfs_control.go:56-70`); `back/internal/router`/`controller` (health probe via `repository.Ping`, `back/internal/router/router.go:124`).

---

## 1. Logic

**Responsibility**: Process-global single-connection-state SQLite data access layer. Package-level `var DB *sql.DB` holds the sole connection handle (`back/internal/repository/db.go:35`); all table operation functions directly depend on it; must call `InitDB(dbPath)` first before use (`db.go:5,87`).

**Core types and structure**:

- Connection and lifecycle: `InitDB` (create connection + connection pool params + Ping + full table creation DDL + idempotent migration, `db.go:87-229`), `CloseDB` (close and set `DB` to nil, idempotent, `db.go:45-52`), `Ping` (for `/ready` probe, wraps DB handle without exposing it, `db.go:54-63`).
- Table operation functions split across 7 files by domain: `file_repo.go` (file_meta/file_providers), `collection_repo.go` (collections/collection_entries/collection_versions/version_entries), `share_repo.go` (share_links), `sync_repo.go` (local_collection_sync/local_sync_files, `SyncRepository` struct), `pin_repo.go` (ipfs_pins), `anon_repo.go` (anonymous collection "JSON file + file_meta registration" dual-write, see `anon_repo.go:25-61`), `file_index_repo.go` (file_index, `FileIndex` struct `file_index_repo.go:12-21`).
- Type constant aliases: `FileTypeBlob`/`FileTypeAnonCollection` moved up to `model` package, repository retains aliases (M2 convergence, `db.go:28-33`, `back/internal/controller/file.go:10-13`).

**Main flow**:

1. **Startup**: `main.go:52-87` → `config.Load()` gets `DBPath` (default `./peerdrive.db`) → `repository.InitDB(cfg.DBPath)` → `SetAnonStorageDir(storageDir)` (anonymous collection default persistence directory).
2. **File registration/upload**: Service layer "write to disk + `InsertFileMeta` + `InsertFileProvider` (+`UpsertFileIndex`) triple" (`back/internal/service/file_service.go:59,81-84,249-273,561-597`).
3. **Collection commit**: Controller → `SaveAnon` (anonymous snapshot persist+register) → `UpdateCurrentHash` → `CreateVersion` + `SnapshotVersionEntries` (`back/internal/controller/collection.go:350-416`).
4. **Transport layer verb**: create/upload success → `UpsertFileIndex` (with monotonic `seq`); delete → tombstone; sync → `ListFileIndexSince` incremental pull, `ApplySync` local replay (`back/internal/transport/file_index.go:205-244,415-472,566-591,594-614`).
5. **Download cache**: After remote protocol fetches data, `cacheToLocal` persists to CAS and registers (`back/internal/downloader/universal_downloader.go:378-398`).
6. **IPFS pin**: `PinCID` fetches gateway data → writes pin cache file → `InsertPin` + `InsertFileMeta` + `InsertFileProvider` (`back/internal/source/ipfs_control.go:34-73`).

**Lifecycle**: `InitDB` establishes once at startup; process exit handled by `defer CloseDB()` in `main.go:79-83` (production path previously did not actively close, relied on process exit to reclaim handle, `db.go:38-44`); test path must explicitly `CloseDB` (on Windows, open .db files cannot be deleted, `db.go:37-44`, `back/internal/repository/db_test.go:10-27`).

---

## 2. How It Stores

**Medium and location**: Single-file SQLite, path from `cfg.DBPath` (`PEERDRIVE_DB_PATH` environment variable, default `./peerdrive.db`, relative to process working directory, `back/internal/config/config.go:27,190,260-262`). Connection string built by `dsn()`: normal file paths append driver-specific PRAGMA params; `":memory:"`, `"file::memory:"`, `file:` prefixed URIs pass through directly without appending (`db.go:79-84`). Repository root has `peerdrive.db`, i.e. the default path artifact.

**Connection and concurrency**:

- File DB connection pool: `SetMaxOpenConns(8)` / `SetMaxIdleConns(4)` / `SetConnMaxLifetime(-1)` (`db.go:95-97`). SQLite's single-writer model means concurrent writes serialize on the driver level; 8 max connections provide sufficient read concurrency.
- `:memory:` database: `SetMaxOpenConns(1)` forced to single connection (in-memory DB does not share across connections, `db.go:90-94`).
- PRAGMA: `journal_mode=WAL` (read-write concurrent), `busy_timeout=5000` (busy wait 5s), `synchronous=NORMAL` (`db.go:105-118`). WAL allows concurrent reads with writes; busy_timeout prevents immediate SQLITE_BUSY errors; NORMAL provides good performance with acceptable durability (data loss only possible in power failure scenarios).
- `Ping()` called immediately after connection established to ensure available (`db.go:120`).
- Full DDL: all tables created in `InitDB` in strict order (file_meta → file_providers → collections → collection_entries → collection_versions → version_entries → share_links → local_collection_sync → local_sync_files → ipfs_pins → file_index, `db.go:131-218`). Each DDL is `CREATE TABLE IF NOT EXISTS`, safe for incremental creation.
- Idempotent migration: `migrateAddColumns` loop adds missing columns (`db.go:220-228`), handles schema evolution.

**Table design highlights**:

- `file_meta` (`db.go:131-148`): sha256 as primary key (content-addressed); fields include filename, size, mime, type, current_hash (for collection), visibility, access_list, updated_at, created_at.
- `file_providers` (`db.go:150-161`): (sha256, provider_type, provider_id) composite primary key; protocol_type (local/http/bt/ipfs), path (storage path or URL), available, created_at.
- `collections` (`db.go:163-175`): (username, name) composite primary key; current_hash, version, visibility, follow_redirects, tags, updated_at.
- `collection_entries` (`db.go:177-186`): (collection_id, path) composite primary key; sha256, type, current_hash, updated_at.
- `collection_versions` (`db.go:188-195`) + `version_entries` (`db.go:197-206`): Version history and per-version entry snapshots.
- `share_links` (`db.go:208-214`): token as primary key; hash, type, filename, expires_at (default 30 days).
- `local_collection_sync` (`db.go:216-222`): (username, name) composite primary key; status, progress, last_synced_at, error.
- `local_sync_files` (`db.go:224-231`): (sync_id, path) composite primary key; sha256, size, status, error.
- `ipfs_pins` (`db.go:233-241`): cid as primary key; hash, filename, size, pinned_at, gateway_url.
- `file_index` (`db.go:243-249`): sha256 as primary key; path, seq (monotonic sequence for incremental sync), updated_at.

**Anonymous collection special handling** (`anon_repo.go:25-61`): Anonymous collections use "content-addressed JSON file + file_meta registration" dual-write pattern:
- Data stored as JSON at `<anonDir>/<hash[:2]>/<hash>` (JSON contains all collection entries)
- `file_meta` registers metadata (sha256, type=anon_collection, visibility, access_list)
- This allows the same hash to be fetched via IPFS/gateway and locally queried
- `anon_repo.go` provides `SaveAnon` (write JSON + register), `LoadAnon` (read JSON + verify), `UpdateVisibility`/`UpdateAccessList`

---

## 3. When It Stores

| Trigger | Behavior | Code Reference |
|---------|----------|----------------|
| Process startup | `InitDB(dbPath)` creates connection + tables + migration | `db.go:87-229` |
| File upload complete | `InsertFileMeta` + `InsertFileProvider` (+`UpsertFileIndex`) | `file_service.go:57-70,561-597` |
| URL source registration | `InsertFileMeta` + `InsertFileProvider` | `file_service.go:249-273` |
| Local file registration | `InsertFileMeta` + `UpsertFileIndex` | `file_service.go:81-84` |
| File deletion | Delete `file_meta` + `file_providers` + `file_index` | `file_repo.go` |
| Collection commit | `SaveAnon` + `UpdateCurrentHash` + `CreateVersion` + `SnapshotVersionEntries` | `collection.go:350-416` |
| Collection rollback | `LoadVersion` + `SaveAnon` + `UpdateCurrentHash` | `collection.go:418-460` |
| Collection fork | New collection + entries from source collection | `collection.go` |
| Collection delete | Delete collection + entries + versions | `collection_repo.go` |
| Share link creation | `InsertShareLink` (with 30-day expiry) | `share_repo.go` |
| Share link lookup | `GetShareLink` (validates expiry) | `share_repo.go` |
| Sync save | `SyncRepository.StartSave`/`UpdateFile`/`CompleteSave` | `sync_repo.go` |
| IPFS pin | `InsertPin` + `InsertFileMeta` + `InsertFileProvider` | `ipfs_control.go:56-70` |
| IPFS unpin | Delete pin + delete meta/provider | `ipfs_control.go` |
| Transport create verb | `UpsertFileIndex` (with monotonic seq) | `file_index.go:238` |
| Transport upload verb | Update `file_index` | `file_index.go:461` |
| Transport delete verb | Tombstone in `file_index` | `file_index.go:570,575` |
| Download cache | `InsertFileMeta` + `InsertFileProvider` | `universal_downloader.go:378-398` |
| Process shutdown | `CloseDB()` closes connection | `main.go:79-83` |

---

## 4. What It Stores

| Table | Data | Key Fields |
|-------|------|------------|
| `file_meta` | File metadata (content-addressed) | sha256 (PK), filename, size, mime, type, visibility, access_list, updated_at |
| `file_providers` | File provider replicas | (sha256, provider_type, provider_id) composite PK, path, protocol_type, available |
| `collections` | Collection registry | (username, name) composite PK, current_hash, version, visibility, tags |
| `collection_entries` | Collection entry list | (collection_id, path) composite PK, sha256, type, current_hash |
| `collection_versions` | Collection version history | version PK, collection_id, hash, timestamp |
| `version_entries` | Per-version entry snapshots | (version, path) composite PK, sha256, type |
| `share_links` | 30-day share links | token (PK), hash, type, filename, expires_at |
| `local_collection_sync` | Sync job state | (username, name) composite PK, status, progress, error |
| `local_sync_files` | Sync file list | (sync_id, path) composite PK, sha256, size, status |
| `ipfs_pins` | IPFS pinned CIDs | cid (PK), hash, filename, size, pinned_at |
| `file_index` | File index for cross-node sync | sha256 (PK), path, seq (monotonic) |

**Data relationships**:
- `file_meta.sha256` ←→ `file_providers.sha256` (1:N, one file can have multiple providers)
- `file_meta.sha256` ←→ `file_index.sha256` (1:1, index for cross-node sync)
- `collections.current_hash` ←→ `file_meta.sha256` (1:1, points to anonymous collection content)
- `collection_entries.sha256` ←→ `file_meta.sha256` (N:1, collection entries reference files)
- `collection_entries.current_hash` ←→ `file_meta.sha256` (N:1, version pointer)
- `share_links.hash` ←→ `file_meta.sha256` (N:1)
- `ipfs_pins.hash` ←→ `file_meta.sha256` (1:1)

---

## 5. Boundaries and Pitfalls

- **Single connection global variable**: `repository.DB` is a package-level `*sql.DB`. All operations go through this single connection. No connection pooling per-domain; all table operations share the same pool.
- **SQLite writer serialization**: SQLite's WAL mode allows concurrent reads with writes, but writes are still serialized. Under high write concurrency, busy_timeout (5s) helps avoid immediate SQLITE_BUSY, but heavy write workloads may still cause contention.
- **Anonymous collection dual-write**: Anonymous collections use both JSON file storage and SQLite registration. This means two places to update; if the JSON write succeeds but the SQLite INSERT fails, there's an inconsistency window. `SaveAnon` does not wrap these in a transaction across both.
- **`:memory:` forced single connection**: In-memory databases share data only within a single connection, so `SetMaxOpenConns(1)` is mandatory. This means all queries are serialized, suitable for tests only.
- **No soft delete for collections**: Collection deletion is a hard delete (cascade to entries, versions, version_entries). File metadata deletion only removes the registration; the actual blob on disk (CAS) is not deleted — it may be referenced by other collections/providers.
- **No foreign key constraints**: SQLite tables do not use FOREIGN KEY constraints. Referential integrity is enforced by application logic. This allows orphaned rows if application logic has bugs.
- **Migration is additive only**: `migrateAddColumns` only adds missing columns, never drops or modifies existing ones. Schema changes must be additive; column removals require manual migration or version bump.
- **Share link expiry is 30 days**: Hardcoded in `share_repo.go`, not configurable. Tokens are 32-char random hex strings.

---

## 6. External Connections

- [../connections/04-service-repository.md](../connections/04-service-repository.md): Service layer delegates all persistence to this module — collections (CRUD/version/share/merge/fork), share links, anonymous collections (visibility/access list), sync state, IPFS pins.
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md): Transport layer uses `FileIndexService` to read/write `file_index` table — create/upload/delete/sync verbs with monotonic seq for incremental cross-node sync.
- [../connections/09-controller-downloader.md](../connections/09-controller-downloader.md): Download completion paths write file_meta/file_providers to this module (timing and idempotency conventions for `cacheToLocal`, `RegisterBTFile`, `ImportGatewayData`).
- [../connections/05-router-source.md](../connections/05-router-source.md): IPFS pin registration (`InsertPin`) and deletion via `source.Manager`.
- [../connections/10-controller-storage.md](../connections/10-controller-storage.md): Content-addressed storage and metadata dual-write relationship — anonymous collection JSON and file blobs use `storageDir/hash[:2]/hash` layout, registration in this module.
- [../connections/02-router-controller.md](../connections/02-router-controller.md): Router assembly — `/ready` probe uses `repository.Ping` (`router.go:124`), `/shares` (`router.go:380-381`), `/ipfs/pins` (`router.go:289`) and other write-endpoint entry points.
- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md): Frontend admin console triggers collection commits, anonymous collection creation, local sync and other DB write entry points via local WS sessions (request-side perspective).
