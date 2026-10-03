# Module 03: storage Content-Addressed File Storage

- **Code location**: `back/internal/pathutil` (path security boundaries); persistence layout and read/write logic distributed across `back/internal/service/file_service.go`, `back/internal/repository/anon_repo.go`, `back/internal/source/local.go`, `back/internal/transport/inbound.go`, `back/internal/downloader/universal_downloader.go`, `back/internal/controller/{file,download,p2p}.go`, `back/cmd/server/main.go`; actual data stored in repository `back/storage/` (the directory pointed to by runtime `PEERDRIVE_STORAGE`).
- **One-line function**: Persists files and anonymous collections to local disk in a content-addressed manner (`storage/<sha256 first two chars>/<sha256>`), and uses pathutil's os.Root security boundary to ensure no path, symlink, or hardlink can escape the allowed root directory.
- **Dependencies**: `path/filepath`, `os` (`os.Root`, Go 1.24), `crypto/sha256`, SQLite (`back/internal/repository`'s `file_meta` / `file_providers` / `file_index` tables as metadata sidecar), `back/pkg/hashutil`, `back/internal/config`, `back/internal/log`.
- **Depended upon by**: `controller` (HTTP upload/download/register), `transport` (WebRTC `req`/`create`/`upload` frames), `source` (`LocalSource`), `downloader` (`LocalFetcher`/`cacheToLocal`), `service` (`FileService`/`AnonService`/`PeerPuller`/`SyncService`/`NodeDirectory`/`NodeShare`), `cmd/server` (startup assembly).

## 1. Logic

The module has two layers: **Security judgment layer** (`back/internal/pathutil`, pure containment checks + safe read/write) and **Content-addressed persistence layer** (storageDir disk layout and read/write operations, scattered across the listed packages).

### 1.1 pathutil's Core Responsibilities

The package comment explicitly defines: does one thing only — determine "whether a path falls within a root directory", and must be used **identically** across three places: file registration (`service.FileService`), external serving (`transport.FileIndexService.serveFile` / `source.LocalSource`), and sharing manifest filtering (`service.NodeShare`), to avoid the combined error of "registration allowed, sharing manifest lists it, but read denies as unauthorized → peer read failed" (`back/internal/pathutil/path.go:1-19`).

Core judgment functions (`back/internal/pathutil/path.go`):

- `Within(root, path)` (:85-113): root/path empty → false (no "empty root = allow all" fallback); first `normalize` (:135-164: reject NUL bytes, Windows rejects reserved device names, Abs+Clean+best-effort symlink resolution, Windows restores 8.3 short names and folds case), then `filepath.Rel` checks for `..` escape; root itself is included.
- `resolveBestEffort` (:57-81): If `EvalSymlinks` can't resolve the entire path (last segment doesn't exist yet — common for write targets/symlink targets), falls back to resolving the **longest existing prefix**, ensuring root and path are on the same representation (real fix for macOS `/var` → `/private/var` symlink scenario).
- `WithinAny` (:116-123): Passes if path is contained in any root.
- `foldCase` (:130): Made into a variable rather than reading `runtime.GOOS` directly, so Linux CI can test the Windows branch.

Safe read/write layer:

- `SafeOpen` (`back/internal/pathutil/safeopen.go:41-72`): Combines "validation" and "opening" into one step, eliminating TOCTOU — first normalize to get symlink-free canonical form, then open `os.Root` on root (Linux uses `openat2(RESOLVE_BENEATH)`, other platforms use directory handle + per-segment `O_NOFOLLOW`, see :3-24 comments), `os.Root.Open` and resolution completed in one kernel call. The returned `*os.File` remains readable after Root is closed (:39-40). `SafeOpenAny` (:76-93) opens within the first root in roots that contains the path.
- Same set for write side: `SafeWriteFileAny` / `SafeOpenFileAny` / `SafeMkdirAllAny` / `SafeRemoveAny` / `SafeRemoveAllAny` (`back/internal/pathutil/safewrite.go:125-193`), all through `pickRoot` (:31-83, deliberately doesn't resolve symlinks, doesn't fold case, resolution delegated to os.Root; string-level defense against NUL/reserved device names/`..`/absolute paths) + `withRoot` (:89-103) + `scopedOps` (`back/internal/pathutil/scoped.go:14-88`, one root unified interface, uses Root when available, falls back to `base/rel` path concatenation via old `os.*` when escape valve is open). `rejectSelf` (safewrite.go:116-121) rejects operations targeting the root itself.
- Degradation/self-check: `openRootOrFallback` (`back/internal/pathutil/rootprobe.go:30-57`) auto `MkdirAll` when root doesn't exist yet (first-run storageDir is expected behavior); `RootUnavailable` + `PEERDRIVE_ROOT_FALLBACK=1` escape valve (:80-106); `ProbeRootSupport` (:133-147) for **startup self-check**; `ExplainRootFailure` (:113-128) translates failures into human-readable messages (not found/permissions/filesystem unsupported). `WarnOnce` (:63-72) ensures same directory warns only once.
- Volume root rejection: `IsUnsafeRoot` / `UnsafeRoots` (`back/internal/pathutil/unsaferoot.go:20-59`).
- Windows reserved device names: `HasReservedName` (`back/internal/pathutil/reserved.go:34-60`), `CON`/`PRN`/`AUX`/`NUL`/`COM1..9`/`LPT1..9` checked per-segment (ignoring extension, `CON.txt` also counts as device), only active on Windows (:145 `normalize` enables it).
- 8.3 short names: `ExpandShortNames` (`back/internal/pathutil/shortname_windows.go:59-78`), restores short-name components back to long names (`C:\PROGRA~1` → long name), no indiscriminate replacement.
- Hardlinks: `RejectHardlink` (`back/internal/pathutil/hardlink.go:37-52`) — hardlinks have no direction, path judgment can't detect them, so only "reject if link count > 1" (better safe than sorry). **Must pass an already-opened `*os.File`** (Windows can only call `GetFileInformationByHandle` on handles to get `NumberOfLinks`, see `back/internal/pathutil/links_windows.go:15-34`; Unix side `NlinkOf` does fstat on fd, `back/internal/pathutil/links_unix.go:24-37`). `HardlinkCheckEnabled` (hardlink.go:20-22): platform can get link count and `PEERDRIVE_ALLOW_HARDLINKS=1` is not set.

### 1.2 Content-Addressed Persistence Layer Flow

- **Layout decision**: Entire repository unified to `filepath.Join(storageDir, hash[:2], hash)` — example: `storage/a3/a3f5b2c8d1e4f6a9b7c0d3e2f5a8b1c4d7e0f3a6b9c2d5e8f1a4b7c0d3e6f9a2`. Two-char prefix directories distribute files to avoid single-directory overflow. This layout is enforced consistently across:
  - `FileService.Upload` (write to CAS)
  - `transport.inbound.serveFile` (read from CAS)
  - `source.LocalSource` (read via file_index mapping or CAS fallback)
  - `downloader.LocalFetcher` (check CAS paths)
  - `anon_repo.SaveAnon` (anonymous collection JSON)
- **Upload flow** (`file_service.go:32-45,57-70`):
  1. Calculate SHA-256 of upload stream
  2. `SafeWriteFileAny` writes to `storageDir/<hash[:2]>/<hash>`
  3. `InsertFileMeta` + `InsertFileProvider` (repository)
  4. `UpsertFileIndex` (optional, for cross-node sync)
  5. Return hash, size, filename
- **Download flow** (`controller/download.go` → `downloader.UniversalDownloader`):
  1. Try local CAS paths (see 08-downloader.md)
  2. If not found, try IPFS gateway / BT DHT / HTTP
  3. On success, `cacheToLocal` writes to CAS + registers metadata
- **Anonymous collection save** (`anon_repo.go:25-61`):
  1. Serialize collection entries to JSON
  2. `SafeWriteFileAny` to `<anonDir>/<hash[:2]>/<hash>`
  3. `InsertFileMeta` (type=anon_collection, visibility, access_list)

### 1.3 pathutil Enforcement Points

The security judgment is enforced at exactly these call sites (all use the same `Within`/`SafeOpen`/`SafeWriteFileAny` logic):

| Call Site | Operation | Code Reference |
|-----------|-----------|----------------|
| `FileService.Upload` | Write to CAS | `file_service.go:32-45` |
| `FileService.RegisterLocalFile` | Write file to CAS | `file_service.go:81-84` |
| `transport.inbound.serveFile` | Read from CAS/file_index | `inbound.go` |
| `transport.inbound.serveUploadBegin` | Write uploaded data | `inbound.go` |
| `source.LocalSource.Open` | Read via `pathutil.SafeOpen` | `local.go:90-105` |
| `service.NodeShare` | Filter sharing manifest by `Within` | `nodeshare.go` |
| `service.SyncService.SaveToDisk` | Write synced files to disk | `sync_service.go:15-40` |
| `downloader.LocalFetcher` | Check/read CAS paths | `universal_downloader.go:68-107` |

---

## 2. How It Stores

**Medium**: Local filesystem (block device/SSD/HDD). The storage layer is pure filesystem operations with content-addressed layout.

**Layout**: `storageDir/<sha256[:2]>/<sha256>` — two-level directory structure:
- First level: first 2 chars of SHA-256 hash (256 directories)
- Second level: full 64-char SHA-256 hash as filename

**Anonymous collection JSON**: `<storageDir>/<hash[:2]>/<hash>` (same layout, JSON content).

**Download cache**: `<storageDir>/p2p/<hash[:2]>/<hash>` (separate subdirectory for P2P-pulled content, same CAS layout).

**Download directory for peer pull**: `<DownloadDir>/pulled/` (for cross-node pulled files with `.part` temp files).

**Security boundary enforcement**: All filesystem operations go through `pathutil.SafeOpen`/`SafeWriteFileAny`/`SafeOpenFileAny`/`SafeMkdirAllAny`/`SafeRemoveAny`/`SafeRemoveAllAny`. These use `os.Root` (Go 1.24) to create a scoped root handle, then perform operations relative to that root. This prevents path traversal, symlink escape, and hardlink attacks.

**os.Root platform implementation**:
- Linux: `openat2(RESOLVE_BENEATH)` system call for kernel-enforced path containment
- macOS/Windows: Directory handle + per-segment `O_NOFOLLOW` when opening path components
- Fallback: `PEERDRIVE_ROOT_FALLBACK=1` enables string-based path construction when os.Root is unavailable

---

## 3. When It Stores

| Trigger | Operation | Code Reference |
|---------|-----------|----------------|
| File upload (HTTP) | Write blob to CAS + register metadata | `file_service.go:32-45,57-70` |
| URL source registration | Write fetched data to CAS + register | `file_service.go:249-273` |
| Local file registration | Write file to CAS + register | `file_service.go:81-84` |
| Anonymous collection commit | Write JSON to CAS + register | `anon_repo.go:25-61` |
| Collection rollback | Write old JSON snapshot to CAS | `anon_repo.go` |
| Sync file save | Write synced file to disk | `sync_service.go:15-40` |
| Cross-node pull | Write `.part` file, rename on completion | `peerpull.go:15-45` |
| Download cache | Write fetched data to CAS + register | `universal_downloader.go:378-398` |
| IPFS pin | Write pinned data to CAS + register | `ipfs_control.go:34-73` |
| Transport upload verb | Write uploaded chunks to disk | `inbound.go` |
| Transport create verb | Register file in file_index | `file_index.go:238` |

---

## 4. What It Stores

**Content-addressed blobs** (CAS):
- File uploads (any binary content: documents, images, video, archives)
- Anonymous collection JSON snapshots (collection metadata + entry list)
- Downloaded files cached from remote protocols
- IPFS-pinned content
- P2P-pulled content from peer nodes

**Path index** (`file_index` in SQLite):
- Maps SHA-256 hash to absolute filesystem path
- Monotonic sequence number for incremental sync
- Used by `source.LocalSource` for path resolution

**Download directory**:
- `<DownloadDir>/pulled/` — cross-node pulled files with `.part` temp files
- Final files renamed from `.part` after SHA-256 verification

**Share scope JSON** (`share_scope.json` in storageDir):
- Runtime sharing scope configuration (dirs/files/collections/friends + levels)

---

## 5. Boundaries and Pitfalls

- **All paths must go through pathutil**: Direct `os.Open`/`os.WriteFile` bypassing pathutil is a security violation. The three enforcement points (registration, serving, sharing) must use identical logic.
- **os.Root is a hard requirement**: `PEERDRIVE_ROOT_FALLBACK=1` escape valve degrades to string-based path construction, which is less secure. This should only be used when os.Root is genuinely unavailable (very old kernels, unsupported filesystems).
- **Hardlinks are rejected by default**: `RejectHardlink` rejects any file with link count > 1. This is safe but restrictive — legitimate hardlinked files will be rejected. Set `PEERDRIVE_ALLOW_HARDLINKS=1` to bypass.
- **CAS layout is fixed**: The `storageDir/<hash[:2]>/<hash>` layout cannot be changed without migrating all existing data. New content goes to the same layout.
- **No garbage collection**: Deleted file metadata does not delete the blob on disk. Blobs referenced by no metadata are "orphaned" but still consume disk space. There is no automatic GC.
- **Volume root rejection**: `IsUnsafeRoot` rejects paths like `/`, `C:\`, home directory as storage roots — these are too broad and dangerous.
- **Windows 8.3 short names**: `ExpandShortNames` restores short names to long names to avoid ambiguity. Without this, `C:\PROGRA~1` and `C:\Program Files` could be treated differently.
- **Symlink resolution is best-effort**: `resolveBestEffort` resolves the longest existing prefix. If the target of a symlink doesn't exist yet, the path is treated as-is. This means a symlink could point to a non-existent location that later gets created — TOCTOU is mitigated by `os.Root` but the initial resolution may differ.

---

## 6. External Connections

- [../connections/03-controller-service.md](../connections/03-controller-service.md): Controller → service layer (FileService/AnonService/SyncService) service boundaries and security validation.
- [../connections/04-service-repository.md](../connections/04-service-repository.md): FileService/repository registers blob metadata into SQLite `file_meta`/`file_providers`/`file_index` (this module's metadata sidecar).
- [../connections/05-router-source.md](../connections/05-router-source.md): Router assembles `LocalSource(storageDir, fileIndex)` and injects `storageDir` into Gin context (multi-source routing on read side).
- [../connections/06-service-transport.md](../connections/06-service-transport.md): Service/transport PeerPuller, source, file_index assembly; cross-node pull persistence and CAS/file_index relationship.
- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md): Frontend uploads/downloads/registrations via local WS sessions and HTTP API ultimately land in this module's CAS layout.
- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md): peerdrive-media consumer requests node content by hash via `req` frame; node side serves from this module's CAS/file_index layer (read-only direction).
