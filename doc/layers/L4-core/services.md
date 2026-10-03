# service layer (back/internal/service/)

> Layer belonging: AOP ④ business core (see doc/LAYERS.md §1).
> The business use-case orchestration layer: the controller's "what to do next" becomes "how to do it" here — combining repository
> read/writes, filesystem operations, and external capabilities (downloader/p2p_bt/transport semantic APIs).
> After the M2 layer collapse (REFACTOR.md §7 M2), the controller no longer calls repository directly, and this layer is the only business entry point.

**One-line responsibility**: converge the rules and persistence of HTTP semantic business (files/collections/auth/sync/shares/tasks/pins)
into service methods that the controller can call in one shot; it is unaware of the transport layer (it does not import transport connection details
beyond the business semantics; LAYERS.md §3 rule 3: the service package does not import transport directly —
assembly is done via controller/router).

## Responsibilities

### What problem does it solve

Before M2, the controller had 60+ scattered direct repository calls (collection/fork/merge/file controllers),
with confused dependency directions and no rule to follow. This layer's goals:

- **One-way dependencies**: `controller → service → repository` (LAYERS.md §2)
- **A centralized security boundary**: path defense (isPathInStorage), hash validation, and upload rate limits are all done here; the controller only passes arguments
- **Business combination points**: sync (SyncService combines the downloader), CID import (FileService.ImportGatewayData),
  BT completion registration (FileService.RegisterBTFile), and other cross-domain logic have a clear home

### Two styles coexist

| Style | Services | Description |
|---|---|---|
| Stateful instances (dependencies injected by constructor) | FileService, AnonService, AuthService, SyncService | Hold dependencies such as config/repository/downloader |
| Stateless transparent forwarding (empty structs) | CollectionService, ShareService, TaskService, PinService | A product of the M2 layer collapse: method names map one-to-one to repository methods, only correcting the dependency direction (explicit in the header comment at collection_service.go:1-5) |

## Module inventory

| File | One-line responsibility | Key exports |
|---|---|---|
| anon_service.go | Anonymous collections (content-addressed JSON storage): create/read/list/version commit | `AnonService`: `CreateCollection`, `GetCollectionByHash`, `ListCollections`, `CommitCollection` |
| auth_service.go | User register/login/logout/authkey verification (bcrypt) | `AuthService`: `Register`, `Login`, `Logout`, `ValidateKey`; `ErrInvalidCredentials` |
| collection_service.go | User collection domain use cases (the M2 transparent forwarding layer) | `CollectionService`: `Get/List/Search/ListPublic/Create/CreatePlain/GetOrCreate/SetVisibility/UpdateTags/UpdateCurrentHash/ListEntries/GetEntry/AddEntry/AddProviderEntry/RemoveEntry/CreateVersion/SnapshotEntries/VersionLog/VersionEntries/RestoreVersion/GetAnonByHash/SaveAnon` |
| file_service.go | File upload/register/verify/delete/copy/browse/metadata (including the security boundary) | `FileService`: `Upload`, `RegisterLocal`, `RegisterFolder`, `RegisterURL`, `ResolveURL`, `Verify`, `Delete`, `BrowseDir`, `CopyFile`, `ReadFile`, `MaxUploadBytes`, `GetMeta`, `GetMetaByCID`, `ImportGatewayData`, `RegisterBTFile`, `ListAll`; `ErrStorageDisabled`, `ErrFileAlreadyExists` |
| pin_service.go | The IPFS pin use-case layer (absorbed by M2) | `PinService`: `Get`, `Insert`, `Remove`, `List`, `InsertMeta` |
| share_service.go | The share link use-case layer (absorbed by M2) | `ShareService`: `Create` (30 days), `GetByToken`, `List` |
| sync_service.go | Syncing a collection to the local disk: filtering/status tracking | `SyncService`: `SaveToDisk`, `GetStatus` |
| task_service.go | Asynchronous task placeholder (the transfer_tasks table) | `TaskService`: `Create`, `UpdateStatus`, `Get` |

## Key mechanisms

### 1. FileService's storage model

Upload/register converge uniformly to a **content-addressed storage** (CAS) layout `storageDir/{hash[:2]}/{hash}`:

```
Upload (file_service.go:444):
  multipart reader → TeeReader writes a temp file while computing sha256 → os.Rename to the CAS
  (EXDEV cross-device fallback copyFile) → InsertFileMeta + InsertFileProvider("local", relPath)
  → an existing file returns ErrFileAlreadyExists (the controller converts this to 200 already_exists:true)
```

- **Duplicate-write protection**: if `GetFileMeta(hash)` exists, skip InsertFileMeta (RegisterLocal:212,
  RegisterURL:365)
- **Temp file**: `os.CreateTemp` + defer Remove; rename puts it into place atomically after writing (Upload:454-499)
- **MIME sniffing**: `http.DetectContentType` on the first 512 bytes, falling back to the extension when octet-stream
  (RegisterLocal:198-205, Upload:472-480)

### 2. The security boundary: isPathInStorage (file_service.go:128-151)

`register_local/register_folder/browse/copy/delete` all accept a caller-supplied path, and historically were the
source of the "anonymous arbitrary file read/write/delete" vulnerability family (F2/F3/H1/H6, see the tests section). Unified defense:

```go
isPathInStorage(absPath):
  storageDir → filepath.Abs + EvalSymlinks (prevents symlink escape)
  absPath   → filepath.Abs + EvalSymlinks
  filepath.Rel(root, abs) → must be "." or have no ".." prefix
```

The same pattern as `transport.FileIndexService.IsPathAllowed` (file_index.go:53).
`Delete` layers on an extra hash validation (isValidHash) — preventing an out-of-root provider path in historical
data from being read back and deleted (comment at file_service.go:551-556).

### 3. AnonService's content-addressed collections

An anonymous collection = a JSON file on disk, hash = the sha256 of the JSON content (anon_service.go:101-133):

- **Validation**: an entry path must be relative (`isRelativePath` + `..` forbidden); a file entry must carry legal
  providers (`isValidProviders`: a 64hex sha256 or an http/https url); a directory entry (path ending with "/") is exempt from providers
- **Sorting**: entries are sorted by path before marshaling — **same content must produce the same hash** (the precondition for addressability)
- **Versioning**: `CommitCollection` merges entries (empty providers = delete) → version+1 → rewrite to disk;
  the old version file is kept (a different hash is a new file)
- **Registration**: on write, also `InsertFileMeta` (Type=FileTypeAnonCollection) + provider
- **Read defense** (comment at GetCollectionByHash:143-146): the hash comes from URL/remote input, and without validation
  `hash[:2]` would panic out of bounds and `filepath.Join` would escape storage (H1, see the tests section)

### 4. AuthService's bcrypt details

- **The 72-byte cap** (comment L1 at auth_service.go:28-32): bcrypt only takes the first 72 bytes, and the tail of an
  overlong password is silently ignored (truncated entropy loss) — reject outright when over the limit
- **Login does not distinguish errors**: user does not exist and wrong password both return `ErrInvalidCredentials` (prevents username enumeration)
- **authkey**: 32 random bytes in hex (generateAuthKey), regenerated on each Login/Register
  (the old key expires immediately)

### 5. SyncService's sync pipeline

```
SaveToDisk (sync_service.go:31):
  1. LocalPath forbids ".." (the first path traversal guard)
  2. GetAnonCollectionByHash reads the collection
  3. filterFiles (include/exclude pattern filtering)
  4. UpsertSyncState + ClearSyncFiles (the DB state is cleared before writing)
  5. Per-file saveFile: forbid ".." → MkdirAll → universalDownloader.Download →
     os.WriteFile → UpsertFileSyncState (a single file failure does not abort; recorded as missing)
```

**matchPattern's path boundary** (comment L2 at sync_service.go:176-208): the substring fallback match must
have a path boundary — a pattern ending with "/" = directory prefix matching, otherwise the substring must be
bounded by '/' on both sides or by string ends;
otherwise excluding "tmp/foo" would also hit "tmp/foobar".

### 6. Upload limits and the storage switch (MaxUploadBytes, file_service.go:743-748)

`MaxUploadBytes(c *gin.Context)` picks `cfg.MaxUploadBytes` /
`cfg.MaxUploadBytesAnon` based on authentication state (the controller side enforces it with `MaxBytesReader`). When `storageEnable=false`
(pure relay/pure index node), the entire upload/register family is rejected (403); only provider registration is allowed
(RegisterURL writes only an "http" provider, no disk write) — the storage switch is **service-wide**,
not judged per file (all six entry points at file_service.go:32/158/235/389/448/546/576/644 check uniformly).

### 7. The recursive semantics of registration and browsing

- `RegisterFolder` (file_service.go:262-290): recursively walks all files in the directory and RegisterLocal each one
  (isPathInStorage blocks out-of-root first) — a duplicate registration of the same file is skipped by meta de-duplication.
- `BrowseDir` (file_service.go:305-340): both `path==""` and `"/"` map to the storage root
  (the counterpart to the controller-layer 400 fix, see the controllers.md tests section); directory entries aggregate
  file_meta path prefixes and return two lists, `{dirs, files}`.
- `DiffVersions` (file_service.go:378-426): entries of two version_ids are compared by path
  — added (only A)/removed (only B)/modified (A≠B and both present) in three groups; the controller layer
  combines `CollectionService.VersionEntries` for the data.

### 8. Dependency injection directions (a product of M2/M4)

```
FileService  ← config (storageDir/storageEnable/MaxUploadBytes)
SyncService  ← SyncRepository + UniversalDownloader + storageDir (downloader injection)
AnonService  ← config (StorageDir)
AuthService  ← UserRepository
PinService / ShareService / TaskService / CollectionService ← no dependencies (reference repository internally)
```

## Relationships with other modules

```
controller (the caller)
  ↓
service (this layer)
  ├→ repository (SQLite: file_meta/file_providers/collections/users/pins/shares/tasks)
  ├→ downloader.UniversalDownloader (data fetching in SyncService.saveFile; AnonService does not use it directly —
  │   anonymous collection downloads go through the controller-side universalDownloader)
  ├→ config (paths/switches/limits)
  └→ model (domain types)
```

- **Does not import transport** (LAYERS.md §3 rule 3): the node interconnection semantics (OpenStream/FetchFromPeer)
  are assembled by the controller via `transport.PeerJSService`, and service does not reference it directly — the only exception is
  `downloader` and `p2p_bt` (the external capability aspect ⑦), which are allowed downstream in service.
- **repository is no longer imported by the controller** (M2 achieved): this layer is the only business entry point to repository.
- **The absorption semantics of PinService/ShareService/TaskService** (M2): all three methods pass through to
  repository directly (PinRepository/ShareRepository/SyncRepository) with no intermediate rules —
  they are a "dependency direction correction" rather than "business refinement"; new logic should preferably land in services with rules like FileService/AnonService.

## Pitfalls and design decisions

1. **The positioning of the transparent forwarding layer** (collection_service.go:1-5): method names map one-to-one to repository,
   with zero business logic — this is M2's "convergence" means, not the final form; caching/transactions are left for the future to be added in service,
   and the controller does not need to be aware.
2. **RegisterLocal anchored to the storage root** (comment at file_service.go:169-174): previously it accepted any absolute
   path → combined with LocalFetcher's provider readback = anonymous arbitrary file reading (F2). register_folder/
   browse/copy share the same boundary, and one leak leaks the whole chain.
3. **Upload's EXDEV fallback** (file_service.go:492-498): when tmp is on /tmp and storage is on a different
   mount, os.Rename reports cross-device link — fallback copyFile (with Sync persistence).
4. **Delete only removes "local providers inside storage"** (file_service.go:557-564): a provider path
   must pass isPathInStorage before os.Remove — historically os.Remove on any provider path = arbitrary
   file deletion (same class as H1).
5. **Anonymous collection hash stability depends on sorting** (anon_service.go:97-99): entries must be sorted before
   marshaling; adding fields/changing the marshal format changes every historical hash (the immutability contract of content addressing).
6. **The version gate on GetAnonCollectionByHash**: Version < 1 is rejected (anon_service.go:159-162) —
   preventing old-format/half-written files from being parsed as a legal collection.
7. **authkey single-value overwrite**: each user has only one valid authkey (UpdateAuthKey overwrites) — multi-device logins
   kick each other off; this is by design (no multi-session concept).
8. **SyncService uses fmt.Printf for errors** (sync_service.go:67): it does not go through the log package, a legacy wart,
   not a new code pattern.
9. **URL registration does not write to disk when storage is disabled** (RegisterURL:389-396): when storageEnable=false, only the
   provider is registered (type "http"), and downloads go through HTTPURLFetcher directly; a write failure is only LogWarn and not fatal.

## Tests

> Except for the 5 tests explicitly marked with a background of discovery below, this layer's tests are legacy-marked (file header comments).

| File | Test | Background of discovery |
|---|---|---|
| anon_service_test.go | `TestCreateCollection_ValidEntries/PathTraversal/InvalidHash/EmptyEntries/EmptyPath/DirEntryWithoutProvider/FileWithoutProvider/AbsolutePath`, `TestGetCollection_ByHash/NonexistentHash`, `TestForkCollection`, `TestDownloadFile_FromCollectionEntry` | legacy (the anonymous collection create/validate/read main chain) |
| anon_service_test.go | `TestGetCollection_InvalidHashNoPanic` | **2026-08-16 transport layer review H1: GetCollectionByHash sliced hash[:2] without validating the hash — a short hash panicked out of bounds and killed the process, and ".."-like values escaped the storage directory**; fix: isValidHash at the entry point (anon_service.go:143-146) |
| file_service_test.go | `TestNewFileService`, `TestRegisterLocal(_NonexistentPath/_StorageDisabled/_EmptyFilename)`, `TestRegisterFolder`, `TestVerify(_Nonexistent)`, `TestDelete(_StorageDisabled)`, `TestRegisterURLDefaultFilename` | legacy |
| file_service_test.go | `TestRegisterLocalOutsideStorageRootRejected` | **F2: RegisterLocal accepted any absolute path + LocalFetcher provider readback = anonymous arbitrary file reading (could read /etc/shadow)**; fix: anchor the root with isPathInStorage |
| file_service_test.go | `TestCopyFileOutsideStorageRootRejected` | **F3: CopyFile could write to anywhere (absolute paths passed through / `../` escape), and combined with the public upload could write authorized_keys**; fix: validate the target is inside the storage root before writing |
| file_service_test.go | `TestDeleteInvalidHashRejected` | **Same class as H1: Delete did os.Remove on any provider path for any hash**; fix: validate the hash first, then confirm the path is inside the root |
| file_service_test.go | `TestBrowseDirOutsideStorageRootRejected` | **H6: BrowseDir accepted any absolute path → arbitrary directory listing (the precondition for arbitrary file reading)**; fix: anchor the storage root |
| sync_service_test.go | `TestSyncService_PathTraversal` (ValidPath/TraversalAttempt/ContainDotDot), `TestSyncService_Filtering` (All/ExcludeOne/IncludeOne/IncludeAndExclude) | legacy (SaveToDisk path traversal defense + include/exclude filtering semantics) |

## File inventory

```
back/internal/service/
├── anon_service.go       anonymous collections (content-addressed JSON storage + version commit)
├── anon_service_test.go  anonymous collection tests (includes 1 H1 background-of-discovery entry)
├── auth_service.go       bcrypt authentication (72-byte cap / authkey)
├── collection_service.go user collection transparent forwarding layer (M2)
├── file_service.go       the main file service (upload/register/delete/copy/browse + the security boundary)
├── file_service_test.go  file service tests (includes 4 F2/F3/H1/H6 background-of-discovery entries)
├── pin_service.go        IPFS pin absorption layer (M2)
├── share_service.go      share link absorption layer (M2)
├── sync_service.go       local sync (filtering + status tracking + path defense)
├── sync_service_test.go  sync tests (path traversal/filtering)
└── task_service.go       asynchronous task placeholder (M2)
```
