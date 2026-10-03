# Peerdrive Netdisk Goal: Planning & Module Breakdown

> Source: User's goal statement on 2026-09-20. This file is **goal definition + module breakdown + acceptance criteria**,
> Read alongside `doc/ROADMAP.md` (development order): ROADMAP covers "what to do first", this file covers "what it should look like".

## 0. Goal Statement (Original Semantics)

1. **The frontend is a regular netdisk UI**:
   - Has **your own node** and **other people's nodes**;
   - Can **join nodes** in the **marketplace**;
   - After joining a node, can see **file links** (packaged collections or individual files);
   - Select file -> **Save** -> Download from someone else.
2. **Also needs a WebRTC pure client consumer**: Fetch files from this P2P network without installing a node.

User's analogy:

- The former (netdisk + marketplace + join + select files to save) = **PT / BT / Kuaibo** approach:
  There's a resource directory, pick a resource, download locally.
- The latter (pure client consumer) = **WebRTC / PeerJS compatible**: Only consume, don't serve.

## 1. Goal -> Capability Gap Comparison

| Goal Capability | Current Status | Gap | Module |
|---|---|---|---|
| Your own node | ✅ `GET /peerjs/node` (id/online/peers) | No "what am I providing" view for local node | M1/M2 |
| Other nodes (marketplace) | 🟡 Discovery server has online nodes but **no user-facing node directory endpoint**; frontend only sees connected peerId list | Marketplace list (online/offline, capability summary, join status) | **M1** |
| "Join node" in marketplace | 🔴 None | Persisted joined nodes + dial on join + exit | **M1** |
| See peer's "file links" (collection/single file) | 🔴 None: frame verb only has local management list `list` (full file_index), **no "share scope" concept**; ROADMAP §5 states "file scope management not done" | Explicit node share scope + `share` verb | **M2** |
| Select file to save -> download from peer | 🟡 Has `FetchFromPeer` (in-memory whole pack, 64MB limit) / `OpenStream` (streaming but no disk write, no progress) | Streaming fetch -> CAS disk write -> verify -> index registration, with progress/cancel task UI | **M3** |
| Netdisk-style frontend | 🔴 Existing pages are anonymous collection editor/browser (AnonCreator/AnonExplorer/Plaza), not netdisk information architecture | Netdisk IA (My Files/Nodes/Marketplace/Transfers) + integration with M1-M3 | **M4** |
| WebRTC pure client consumer | 🟡 `peerdrive-media` is a **browser PeerJS client**, but frame protocol is `url` (tells node to fetch URL), **not** peerdrive's `req` (fetch content by sha256) | Independent zero-dependency client package speaking `req`/`info`/`share` protocol | **M5** |

**Key judgment**: The goal itself already chains ROADMAP phases 3->6 into a user-visible flow
(interconnect -> files -> composition -> management chain -> scope -> upload/download/save). Therefore this goal **does not change**
ROADMAP order, but is its "acceptance form": Phase 5 (scope) and Phase 6 (upload/download/save) get
concrete UI for the first time.

## 2. Hard Constraints (Following ROADMAP §Hard Constraints)

- Before Phase 7 (identity management), **everything works at node-level peerId**: marketplace, join, share,
  save - the entire chain does not depend on accounts/regserver. Use peerId when "ownership" is needed.
- Share scope defaults **off**: No explicit declaration means no public listing (prevents "default full-disk sharing").
- Frontend **introduces no new dependencies** (unstable npm network + repo already has Tailwind/React19/router7);
  Layout **references mature netdisk information architecture** (Nextcloud Files, Cloudreve, Alist), components implemented in-house.
  Satisfies "reference good designs, don't invent interactions from scratch".

## 3. Module Breakdown

Branch model follows `AGENTS.md`: `module/<name>`, each branch runs CI, then merges back to `refactor`.

### M1 `module/node-market` — Node Marketplace & Join
- **Model** `internal/model/node.go`: `NodeSummary{PeerID,NodeType,Collections,Files,Joined,Connected,LastSeen}`.
- **Service** `internal/service/node_directory.go`:
  - Marketplace list: Query discovery server (`DiscoverURL`'s `/discover/nodes`, rooms = content shard rooms + presence rooms)
    Aggregate online nodes, merge with local `conns` for `Connected`, merge with joined list for `Joined`.
  - Joined list persistence: `<storageDir>/joined_nodes.json` (atomic write: temp file + rename).
    Why not SQLite: This is a small operator preference, and must work without DB (pure client mode/CI).
- **Endpoints** (via `/ws/peer` admin frames, frontend only uses admin):
  - `GET /peerjs/nodes` -> `{self:{id,...}, nodes:[NodeSummary]}` (marketplace)
  - `GET /peerjs/nodes/joined` -> Joined list
  - `POST /peerjs/nodes/join {peer}` -> Persist + dial immediately (`EnsureConnection`)
  - `DELETE /peerjs/nodes/join?peer=` -> Remove (no active disconnect, connections naturally converge via discovery/dial budget)
- **Transport additions**: `EnsureConnection(peerID string)` (idempotent dial, reuses `connectLoop`'s
  `connecting` dedup); `ConnectedPeerIDs()` (already has `Connections()`, add a lightweight id-only version).

### M2 `module/node-share` — Node Share Scope (ROADMAP Phase 5)
- **Model** `internal/model/share.go`: `ShareScope{Collections []ShareCollection, Files []FileInfo, Dirs []string}`;
  `ShareCollection{Hash,Name,Size,Entries []ShareEntry}`.
- **Service** `internal/service/nodeshare.go`: Resolve share scope from config + local state:
  - `PEERDRIVE_SHARE_COLLECTIONS`: Comma-separated collection hashes (or `all` = all public collections);
  - `PEERDRIVE_SHARE_DIRS`: Comma-separated directories, registered files within enter share scope.
    **Location unrestricted**: Can be outside storage root, another disk, another mount point;
    Conversely, directories not listed here are rejected (prevents arbitrary file R/W). See §7.4;
  - `PEERDRIVE_SHARE_ENABLE` (default **false**) master switch -- default shares nothing.
  - The above three are only **first-boot initial values**: At runtime, change via `GET/PUT /peerjs/share`,
    `POST /peerjs/share/files` (check individual files by hash, by directory, by collection),
    Persist to `storageDir/share_scope.json`, persists after restart. See §12.
- **Frame verb** `share` (inbound, `transport/share.go`):
  `{type:"share"}` -> `{type:"share-resp", collections:[...], files:[...], total}`
  - **Do not reuse `list`**: `list` is local management list (full file index), semantics is "I manage",
    not "I share". Mixing equals default full-disk exposure (which is exactly ROADMAP's "broad
    local collection broadcast" -- effectively revealing what this node holds).
  - `share` only returns **explicitly shared** content; returns empty `share-resp` when sharing is off (not an error, so frontend can render empty state).
  - **Announce extension**: Report `shares:{collections:n,files:n}` summary (**only reports count, not hashes**),
    Marketplace card can display "node shares 3 collections 12 files", while not leaking specific content.

### M3 `module/peer-pull` — Cross-node Fetch & Save
- **Service** `internal/service/peerpull.go`: Task-based fetch (format references BT download: list/progress/cancel).
- `Start(peer, hash, filename) (jobID, error)`: Background goroutine using
  `transport.OpenStreamFrom` streaming read -> write `<storageDir>/tmp/<jobID>.part` ->
  verify sha256 -> rename to CAS `<storageDir>/<h[:2]>/<h>` -> register file_index ->
  `done`. Any step failure -> `failed` + cleanup `.part`.
- Progress: `{jobID, peer, hash, name, total, received, status, error, startedAt, endedAt}`;
  `total` from stream's meta frame (`OpenStreamFrom`'s read side first frame includes total; if unavailable then -1 unknown).
- `StartCollection(peer, hash)`: Fetch collection from share list -> expand entries -> individually Start
  (concurrency 3), return batch job list.
- `FetchManifest(peer, hash, maxBytes)`: Fetch a collection manifest by hash (no disk write).
  Fallback when collection not found in list -- **unlisted collections by definition are not in the list**, if only looking at the list
  "giving a collection link for full save" can never be done (not in the list -> 404). The manifest itself is
  content-addressed JSON, can fetch by hash; fetch goes through the same `req` channel, so private collections
  are still blocked by peer's `ShareGate`. `maxBytes` is a hard limit (default 8MB): input may be casually
  entered hash, pointing to something that may not be a manifest.
- **Endpoints**: `GET /p2p/pull`, `POST /p2p/pull`, `POST /p2p/pull/collection`,
  `DELETE /p2p/pull/:job`.
- **Save semantics**: CAS means "saved to my netdisk" (content-addressed dedup, same hash stored only once);
  `file_index` registration provides human-readable filename -> Frontend "My Files" directly visible.

### M4 `module/netdisk-ui` — Netdisk Frontend
Reference information architecture (not copying code, copying structure):

| Area | Reference Source | Implementation |
|---|---|---|
| Left nav (Netdisk/Marketplace/Transfers/Settings) | Nextcloud Files, Cloudreve | `components/SideNav.jsx` |
| Main file table (Name/Size/Source/Time/Actions) | Cloudreve, Alist | `components/FileTable.jsx` |
| Top breadcrumb + action bar (upload/create new/refresh) | Cloudreve | `components/DriveToolbar.jsx` |
| Node card (Online status + capability summary + join button) | BT/PT site resource list | `components/NodeCard.jsx` |
| Transfer task list (Progress bar + cancel) | qBittorrent/Cloudreve | `components/TransferRow.jsx` |
| Preview (image/video/text/PDF) | -- | **Reuse** `AnonExplorer/ImagePreview|PdfPreview|TextPreview` |

Pages:

- `pages/Drive/index.jsx` — My Netdisk: Local files (`/files`), upload, delete, preview, "My Collections".
- `pages/Market/index.jsx` — Node marketplace: Online nodes, share summary, join/exit.
- `pages/Peers/index.jsx` — My Nodes: Joined nodes + direct connection status.
- `pages/PeerDetail/index.jsx` — Peer node: **file links** list (collections expandable -> entries; single files),
  select -> "Save to my netdisk" -> M3 fetch -> jump to Transfers page for progress.
- `pages/Transfers/index.jsx` — Transfer tasks: Progress/cancel/retry.
- `App.jsx` routing and `Navbar` grouping adjusted; old pages (Plaza/AnonCreator/AnonExplorer/BT/IPFS/Settings)
  kept in "Advanced" group, not deleted.

### M5 `module/peer-client` — WebRTC Pure Consumer
- **Independent package** `packages/peerdrive-client/`: No React/Tailwind dependency, pure vanilla JS + PeerJS.
- **Protocol**: Implement peerdrive's `req` (fetch file by sha256), `info` (node info), `share` (share list) verbs.
- **UI**: Minimal file list + download button, single HTML file deployable.
- **Positioning**: For pure browsers who don't want to install a node -- open the page and connect to nodes to fetch files.

## 4. Acceptance Criteria

Run through after each module is complete, all pass to count as done:

- **M1**: Two nodes start -> marketplace shows each other -> join maintains connection -> exit stops dialing
- **M2**: Set share scope -> peer's `share` verb sees corresponding list -> no scope set returns empty
- **M3**: Fetch a large file from peer (>64MB) -> progress bar normal -> CAS persists correctly -> file_index registered correctly
- **M4**: All four Drive tabs work -> file list shows local CAS content -> transfers have progress -> share can be configured
- **M5**: Browser without node opens -> connects to node -> fetches file successfully

## 5. Relationship with Existing Modules

| Module | Type | Role in this goal |
|---|---|---|
| `transport/` (interconnection layer) | Core | Provides connection + verb framework, M1/M2/M3 extend here |
| `file_index/` (file index) | Core | M3 fetched files registered here; M4 list page displays |
| `service/` (business logic) | Core | M1/M2/M3 service layer goes here |
| `front/src/pages/` (frontend pages) | Frontend | M4 creates new Drive directory; M5 independent package |
| `peerdrive-media/` (existing WebRTC client) | Reference | M5 references but does not reuse -- different protocols (`url` vs `req`) |

## 6. Development Order

```
M1 (marketplace+join) ─┬─ M2 (share scope) ── M3 (fetch&save)
              │                                    │
              └────────── M4 (UI integration) ◄────┘
                                    │
                                    └── M5 (independent client, can run in parallel)
```

- M1 is a prerequisite (without marketplace list, M2/M3 are meaningless)
- M2 and M4 can partially parallelize (M4's UI skeleton doesn't depend on M2 completion)
- M3 depends on M2 (without share scope, don't know what to fetch)
- M5 is completely independent, can start anytime

## 7. Known Technical Constraints

### 7.1 Large File Streaming Fetch
- Existing `FetchFromPeer` is in-memory whole pack (64MB limit), M3 must use `OpenStream` streaming
- `OpenStream` currently has no sha256 verification, M3 must verify before persisting
- Progress callback needs per-block update, be careful not to block data flow

### 7.2 Resume from Breakpoint
- ROADMAP Phase 6 requires resume, M3 needs to implement offset parameter
- Temp file naming suggestion: `<hash>.partial.<timestamp>`

### 7.3 Frontend State Management
- Drive's four tabs have different data sources, recommend React Context for global node state
- Transfer list needs polling (`GET /peerjs/transfers`) or WebSocket push

### 7.4 Security
- `share` verb returns list may contain many entries, frontend must limit render count
- Fetched file hash is not trustworthy, must sha256 verify before registering CAS

## 8. Testing Strategy

- **M1/M2/M3**: Go unit tests + integration tests (two local nodes + discovery server)
- **M4**: Frontend unit tests (React Testing Library) + end-to-end (Cypress optional)
- **M5**: Browser end-to-end tests (Playwright)

## 9. Risks

| Risk | Impact | Mitigation |
|---|---|---|
| WebRTC connection instability | P2P data plane unavailable | M3 needs fallback plan (HTTP fallback) |
| Large file streaming complexity high | M3 schedule delay | M3 split into two phases: Phase 1 streaming fetch, Phase 2 resume |
| Frontend new page maintenance overhead | Maintenance overhead | M4 reuse existing components, new pages only add 3-4 files |

## 10. Future Extensions

- **Phase 7 (identity management)**: Replace peerId with account system, marketplace can filter by user
- **Phase 8 (payment/credit)**: Nodes earn credits for providing large bandwidth/files
- **Multi-protocol support**: Support HTTP/WebDAV/SFTP as alternative to P2P
- **Offline sync**: Sync files when node comes back online
- **Content deduplication**: SHA256 content addressing already deduplicates, can add reference counting
- **File sharing links**: Generate shareable links with access control

## 11. Path Traversal Audit (2026-09-20)

### 11.1 How tests are distributed

Not "add a few test cases", but **four layers each with a matrix**, totaling 162 assertions:

| Layer | File | Coverage |
|---|---|---|
| Decision logic | `internal/pathutil/traversal_test.go` | `..` escape, sibling directory same-prefix, NUL, symlinks (file/dir/dangling/root itself), cross-disk, UNC, `\?\`, ADS, 8.3 short names, `/proc/self/root`, empty root, config split |
| Verb layer | `internal/transport/traversal_test.go` | `create` rejected, `write-file`'s `name` must be within uploadDir, symlink escape in shared root directory, `redactDisallowedPath` masking |
| Service layer | `internal/service/traversal_test.go` | Same payload hits four entry points; `copy` additionally asserts "nothing extra on disk" |
| HTTP layer | `internal/controller/traversal_test.go` | **Encoded forms** (`%2e%2e%2f`, `....//`, `..%5c`, `\u002f`) |
| Real binary | `scripts/netdisk-traversal-probe.sh` | Hit payloads against running node, including disk verification |

```bash
cd back && go test -tags nosqlite -run TestTraversal ./internal/...
./scripts/netdisk-traversal-probe.sh
```

### 11.2 Two real issues found by tests (both fixed)

1. **`CopyFile` boundary is not the first gate.** It first calls `GetFileMeta(hash)` then validates target path,
   so unauthorized `dest_path` returns `source hash ... not found`——
   ① rejection reason is masked, operators reading logs would think it's a data problem;
   ② security boundary is not first, if someone adds "auto-fetch if source not found" logic above, it degrades to arbitrary file write;
   ③ also leaks "whether a hash exists".
   Now `isValidHash` + `isPathAllowed` moved to the front.

2. **`sanitizeName("..")` returns `..`**. `filepath.Base("../../x")` is `x` which is fine,
   but `Base("..")` is still `..`, `Join(uploadDir, "..")` points to **uploadDir's parent directory**.
   Caught by `Create`'s boundary, but that layer's dependency order is too fragile——now blocked directly in `sanitizeName`.

Also hardened: `pathutil.normalize` explicitly rejects paths containing NUL (Go's `os.Open` also rejects,
but pure string validation doesn't recognize NUL, can't let "accidentally blocked by system call" be the only defense).

### 11.3 Four "known not prevented"——fixed (2026-09-20)

The four previously listed here are now all implemented. **Don't treat them as "not done" anymore**:

| Original gap | Now how | Code |
|---|---|---|
| **TOCTOU** (after `EvalSymlinks` then `os.Open`, symlink can be swapped in between) | Resolve and open combined: first `normalize` to canonicalize, then use **Go 1.24's `os.Root`** to open (`openat2(RESOLVE_BENEATH)`, other platforms walk each component with `O_NOFOLLOW` via directory fd)——this is the "need O_NOFOLLOW/openat" mentioned before | `pathutil.SafeOpen` / `SafeOpenAny`; all four read entry points now use it |
| **Hard links** (no direction, `EvalSymlinks` can't detect) | Reject normal files with multiple names (on opened fd, `fstat` checks `nlink > 1` and rejects). Registration has two entry points (peer `create` and HTTP `register_local`), **both share `pathutil.RejectHardlink`**, checking only one leaves a hole | `pathutil.RejectHardlink`; `PEERDRIVE_ALLOW_HARDLINKS=1` bypass (pnpm `node_modules`, `cp -l` backup directories need this) |
| **Root configured as `/`** (`Within("/", "/etc/passwd")` is true——that's config intent, not traversal) | Can only intercept at **startup** per config intent: `checkUnsafeRoots` checks storage / download / each share dir, if configured as volume root, refuse to start and name which env variable | `cmd/server/main.go` + `pathutil.IsUnsafeRoot`; `PEERDRIVE_ALLOW_UNSAFE_ROOT=1` can bypass |
| **Windows-specific payloads `t.Skip` on Linux CI** | CI's Windows cell **no longer skips `go test`** (`.github/workflows/go-build.yml`), and replaces hardcoded POSIX payload (`/etc/passwd`) with platform-specific values——otherwise on Windows it's not an absolute path, would be concatenated into storage root, validation passes, assertion false-green | `systemAbsolutePath()` (controller), platform branch (service) |

> The hard link defense in §11.3 had another update: Windows originally **had no** hard link count available (see
> §11.5 first item), now fixed by calling `GetFileInformationByHandle` on the opened handle.

### 11.4 Four bugs found on real Windows first run

Linux CI all green doesn't mean Windows is fine. Running tests on real Windows, found four
**only-on-Windows** issues (all "symptoms invisible on Linux" type):

1. **`filepath.Rel` on Windows itself folds case.** `path/filepath`'s
   `sameWord` on Windows is `strings.EqualFold`, comparing component by component, `C:\Temp` and
   `c:\tEMP` are considered the same. So "case folding" logic on Windows is done by Rel,
   what value `foldCase` takes doesn't change the result (consistent with NTFS, correct).
   The original assertion "when folding is off, should not pass" can never be true on Windows.
2. **`filepath.Base("/")` on Windows returns `\`, not `/`.** `sanitizeName`
   only blocked `"/"`, so `Join(uploadDir, "\")` = uploadDir itself -> "is a directory".
   Now both separators are blocked (added `\` in `sanitizeName` alongside `pathutil`).
3. **Didn't close handle before deleting file.** On Windows, open files can't be deleted
   ("being used by another process"), `os.Remove` silently fails:
   - Admin upload temp file: `os.Remove(au.path)` was before `au.f.Close()`
     -> each aborted upload leaves garbage on disk (moved to `adminUploadState.cleanupTemp`);
   - Chunked upload `Complete()` then handle stays until 10-min reap closes it -> file can't be deleted,
     can't be moved. Now closes handle immediately after completion, added `FileIndexService.Close()` for test cleanup.
4. **Published binary dies on Windows/macOS at startup.** Release pipeline sets
   `CGO_ENABLED=0` for all platforms, and `mattn/go-sqlite3` degrades to stub without cgo (errors on first
   SQL execution), so `InitDB` table creation fails, process exits immediately. Now chooses based on cgo availability:
   with cgo uses mattn, without uses pure Go `modernc.org/sqlite`
   (`repository/db_driver_cgo.go` / `db_driver_pure.go`, driver names are
   `sqlite3` / `sqlite`, so `sql.Open` can no longer be hardcoded).
   Side note: CI's Windows cell no longer needs mingw.

How to run tests on Windows (can verify without local Go install: cross-compile `.test.exe` and run directly):

```bash
cd back
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -tags nosqlite -c -o /tmp/x.test.exe ./internal/pathutil/
# Copy x.test.exe to Windows, cd to that package directory and run directly (tests' relative paths depend on cwd)
```

### 11.5 Second "not prevented" list——also fixed (2026-09-20 evening)

The four previously listed are also now implemented. **Don't treat them as "not done" anymore**:

| Original gap | Now how | Code / verification |
|---|---|---|
| **Can't get hard link count on Windows** (that defense was empty on Windows) | No longer from `os.FileInfo` (`Win32FileAttributeData` doesn't have nlink), changed to call `GetFileInformationByHandle` on **opened handle** for `NumberOfLinks`. `RejectHardlink` param changed from `os.FileInfo` to `*os.File`, both registration entry points already have open fds, also saves one path resolution | `pathutil.NlinkOf` / `links_windows.go` / `links_unix.go`; Windows real machine `TestTraversal_CreateHardlinkRejected`, `TestTraversal_RegisterLocalHardlinkRejected` now **no longer `t.Skip`** |
| **TOCTOU on write path** (copy / delete / upload disk write still by path) | Write also Root-ized: added `SafeWriteFileAny` / `SafeOpenFileAny` / `SafeMkdirAllAny` / `SafeRemoveAny`, open `os.Root` on allowed root then **resolve and write in one step**; `FileService.Delete`'s `os.Remove(p.Path)`, `CopyFile`'s `MkdirAll+WriteFile`, `Upload`'s `Rename`, chunked upload's `Abort/reap` all use it | `pathutil/safewrite.go`; `safeopen_write_test.go` each is **race-shape** (first validate -> swap to symlink -> then write), all with **control arm** proving old `os.WriteFile` would write through symlink |
| **8.3 short names no dedicated test cases** | Added Windows-specific cases, also found a real bug: Windows `EvalSymlinks` only restores short names for **existing** paths, write operation's last path segment doesn't exist -> `Within` judges "another name of same directory" as unauthorized (long-name shared dir, short-name access can't read files). Now `GetLongPathName` (per-segment fallback: deepest successful prefix uses long name, non-existing parts kept as-is) normalizes to long name | `pathutil/shortname_windows.go` + `shortname_other.go` (non-Windows is identity); `shortname_windows_test.go`. Local test: 8.3 generation is enabled (`C:\Program Files -> C:\PROGRA~1`) |
| **`os.Root` silently fail-closed on exotic filesystems** (manifests as "can't share this directory") | Three things: **classify** (unsupported / not exist / no permission, each with different next step), **startup self-check** (`warnUnsupportedRoots`, startup failure/unshareable clearly logged with remedy), **explicit escape hatch** `PEERDRIVE_ROOT_FALLBACK=1` (default off; when on, continuously warns "fell back to path-based validation, TOCTOU window exists") | `pathutil/rootprobe.go` + `scoped.go` (Root and fallback modes unified into one operation surface); `cmd/server/main.go` startup self-check |

Conclusion on the last item's real-world testing (local WSL2, 2026-09-20): `/tmp`, `/home`, DrvFs
`/mnt/c`, `/mnt/d`, `/mnt/e`, procfs, sysfs, devtmpfs **all** can establish `os.Root`——
only failure is `/root` (permission denied). So the most common real "can't share this directory" is actually a
**permission** issue, previously logs only said `open root ...: permission denied`,
now tells you to check owner/ACL. For truly unsupported bare-metal hosts use the escape hatch above.

#### 11.5.1 Two shapes only red on other people's machines (darwin / windows runner)

Common trait of these bugs: **can't reproduce locally, only CI cell is red**. Both are "same directory written
two ways", just different causes:

**(a) Symlink root + missing leaf** (`macos-latest/arm64` red)

`TestTraversal_RedactDisallowedPath` fails on `macos-latest/arm64`, all other platforms green.
Root cause isn't macOS having different validation semantics, but **its temp directory itself is a symlink**:

- macOS `t.TempDir()` is `/var/folders/...`, and `/var` -> `/private/var` is a symlink;
- `normalize(root)`: root exists -> `EvalSymlinks` succeeds -> `/private/var/...`;
- `normalize(path)`: path's last segment (`a.txt`) **doesn't exist yet** -> `EvalSymlinks` fails ->
  keeps `/var/...` as-is;
- So same directory is written two ways, `filepath.Rel` computes a series of `..` -> judged as unauthorized.

This is also a bug in real deployments: when shared directory is configured under a symlink path, files in that directory become "visible but unreadable".
Fix is resolve by **longest existing prefix** (`pathutil.resolveBestEffort`): resolve as deep as possible,
non-existing parts concatenated back as-is, ensuring root and path always fall into the same writing style.
Regression case `TestTraversal_SymlinkedRootMissingLeaf` (creates own symlink, reproducible on Linux;
removing the fix makes it red, verified).

**(b) Allowed root itself is 8.3 short name** (`windows-latest` red 3 cases)

Windows runner's `t.TempDir()` is `C:\Users\RUNNER~1\AppData\Local\Temp\...`.
Write path's `pickRoot` only restored **path**'s short name, not **root**'s ->
`RUNNER~1` and `runneradmin` treated as two trees -> writes clearly within root judged
`path outside allowed root`. Fix: `pickRoot` does same short-name restoration for allowed roots
(read path `SafeOpen` goes through `normalize` on both sides, already consistent).

Regression case `TestSafeWriteFile_ShortNameRootAlias` (creates "shared directory's short name" as allowed root,
reproducible even without 8.3 alias in local username; removing fix makes it red).

> Two lessons: ① Cross-platform validation must **create shapes yourself** (symlinks, short names), can't rely on platform built-ins——
> when local machine happens to lack that condition, test cases will be always-green testing nothing; ② After fixing one, **check all cells**,
> failures masked by cancel in earlier cells will surface on the next run.

One more practical experience: **default fail closed is correct** (better to give less than more), but conflating
"unsupported" with "no permission/not exist" is the main source of troubleshooting cost——`RootUnavailable` only accepts
`EINVAL`/`ENOSYS`/`ENOTSUP`, `ENOENT` and `EACCES` must not be classified as
"filesystem unsupported", otherwise operators will waste time checking OS compatibility.

## 12. Share Scope: Freely Choose What to Share (2026-09-22)

When M2 was implemented, share scope could only be fixed at startup via `PEERDRIVE_SHARE_*`: to share one more file,
either move it into a shared directory, or change environment variable and **restart the node**. This section changes it to runtime-modifiable.

### 12.1 Why runtime modification is necessary

"Which content am I willing to give out" is inherently a casual decision: just uploaded a file and want to share it with a friend immediately,
or a directory you no longer want to share. Requiring restart turns this decision into an ops action, the practical result is one of two——
long-term sharing an overly broad directory (for convenience), or simply not enabling sharing (too much hassle). Both are worse than
"casually check/select".

### 12.2 Three sources, union set

| Source | Granularity | How to select |
| --- | --- | --- |
| `dirs` | Entire directory | `PUT /peerjs/share {"dirs":[...]}`; all files in file_index whose paths fall within are shared |
| `files` | Individual file (by hash) | `POST /peerjs/share/files {"hashes":[...],"shared":true}`; **need not be in any shared directory** |
| `collections` | Collection | 64hex collection hash, or `all` = all public collections |

`enable` is the master switch: off = completely empty list externally (**already selected content is preserved**, re-enable doesn't need reselection).

### 12.3 Environment variables downgraded to "initial values"

- On first boot (no `share_scope.json` under storage), environment variables seed runtime state and persist to disk;
- Afterward **disk state is authoritative**: operator-cancelled share items won't resurrect after restart
  ("I clearly cancelled sharing" is the hardest class of feedback to self-diagnose).
- Corrupted persistence file: keep original file (human-inspectable), treat as "never saved", don't block startup.

### 12.4 Endpoints

```
GET  /peerjs/share        Current scope + optional file list (each row has shared / by_dir / level)
PUT  /peerjs/share        Partial update {enable?,dirs?,files?,collections?,friends?} (untransmitted items stay as-is)
POST /peerjs/share/files  {hashes:[...], shared:bool, level?}  Row-level check/uncheck/change level
```

Entries are like `{"id":"<dir path|file hash|collection hash>","level":"public|unlisted|private"}`;
also accepts plain strings (level defaults to public, backward compatible with `share_scope.json` from before this upgrade).
`GET` additionally returns `selected` (pure id list, convenient for rendering) and `levels` (three-level values).

All three endpoints have `AuthRequired` (pass through when no registration server configured = single-node mode). Naming is
deliberately distinguished from "ask peer for list" `GET /peerjs/nodes/:peer/shares`.

### 12.5 Two points that will cause problems if not changed

1. **Newly added shared directories must be registered as file_index readable roots** (`main.go`'s `SetDirHook`).
   Only entering scope but not registering readable root -> "list can be shown, but peer fetch gets read failed", same source as §11.
2. **Volume root directory rejected at entry** (`PUT {"dirs":["/"]}` -> 400).
   `pathutil.Within` would judge `/etc/passwd` as "within root"——validation is correct, config intent is wrong,
   and this value comes from HTTP request body, one typo shares the entire disk.

### 12.6 Share Levels: public / unlisted / private (2026-09-22)

Each share declaration (directory / single file / collection) has one level, answering "to whom":

| Level | In shared list | Who can download |
|---|---|---|
| `public` | Yes | Anyone who can connect |
| `unlisted` | **No** | Anyone who can connect (knows the hash, i.e., "link sharing") |
| `private` | No (except friends) | **Only self and friends** |

One-line memory aid: **public = list it and give; unlisted = don't list but give; private = only for people you know.**

Validation and wiring:

- Constants and merge rules in `internal/model/share_level.go`; **when multiple sources hit same content, take
  the loosest** (`LoosestLevel`): directory `unlisted` + single file `public` => that file is `public`.
  Taking the strictest would silently invalidate "I deliberately loosened this one".
- List side: `NodeShare.SnapshotFor(peerID)`. Share frame goes through established connection, peer id
  is known, so **friends can see private entries** (otherwise gave permission but no directory). `unlisted`
  never listed. `announce` reported count uses anonymous perspective (`SnapshotFor("")`), don't broadcast "I have X
  things only for friends".
- Download side: `transport.ShareGate` -> judged in `serveFile`. **Only private blocks**:
  public / unlisted / **undeclared** all pass through.
- "Self" = local channel not through P2P (HTTP management API / local WS direct,
  `WSSession.IsLocal`); "Friends" = `ShareScope.Friends` (node ID whitelist, case-
  insensitive), env variable `PEERDRIVE_SHARE_FRIENDS` is only initial value.

Two boundaries, don't loosen:

1. **Peer id is self-reported by peer, signaling doesn't verify** (no account system before Phase 7). So friend list
   is only reliable in networks **with PSK set**——without PSK, anyone can connect and claim to be a friend. PSK is
   entry gate, levels are post-entry grading.
2. **"Undeclared" can still be downloaded**: Content-addressed retrieval (know hash = can fetch) is existing behavior.
   Changing to "must declare to fetch" would break even basic self-checks like "upload -> fetch by hash to verify".
   The difference between `unlisted` and "undeclared" is **it's explicitly declared**: management panel can see it, can audit, can statistics,
   when truly tightening downloads in the future, won't be accidentally affected.
3. Wrong level (`"pubilc"`) **rejects entire batch** (400), never defaults to public——that equals making intended
   restricted content public. Explicit level change is **overwrite** (`public -> private` must be changeable),
   not union.
4. Collection's own `visibility` non-public (restricted/private) treated as **private**:
   AccessList is account list, no identity to verify, only for friends.

### 12.6.1 Consumer Side: Fixed Identity & Share Links

Three levels need two more "usable by humans" entry points (2026-09-22):

1. **Panel must have fixed id**. Friend list recognizes peer's self-reported peer id, and peerjs defaults to
   random id per `new Peer`——filling in an id that changes tomorrow is like not filling it, `private` would degenerate to
   "nobody can get it, including the person you promised to give it to". So panel generates `pd-panel-<random>` and saves to
   localStorage, copyable and replaceable in top-right corner; id collision (same browser two tabs) returns
   `unavailable-id` from signaling, panel retries with new one and logs the reason.
   ⚠️ Early startup (before `var $ = ...` is assigned) **must not touch DOM**: calling `renderMyId()`
   at that point throws `$ is not a function` and breaks the entire IIFE, panel completely fails. `persistMyId` (disk only)
   and `saveMyId` (disk + repaint) are therefore separated.
2. **Unlisted needs an exit**. Its semantics is "not listed, fetchable by hash", needs an action to hand over the hash:
   each row in the list has a "Link" button, generates `?node=<id>&hash=<64hex>&auto=1`.
   Person receiving the link, upon connecting, sees this content listed **above** the shared list——it **deliberately doesn't depend on the list**
   (unlisted by definition is not in the list, only searching the list means user sees "this node has no shared content"
   and leaves, while the content is always fetchable). Link doesn't contain PSK.
3. **Collections can also get a single link for full pack**. Collection manifest is itself content-addressed JSON
   (hash is its sha256), so "fetch by hash" applies to collections too——what comes back is the **entry list**.
   Panel identification: if this collection hash hits in the list, use directly; if not in list (unlisted), fetch
   the manifest back, has `entries` array = collection (fetch has `LINK_SNIFF_BYTES` limit, otherwise
   a multi-GB file link would first be fully loaded into memory before judging). Fetch failure always treated as file——then
   "fetch" shows the server's real reason, better than guessing a reason on the client.
   Panel can only save one at a time to browser (multiple downloads triggered at once get blocked by browser), so entry rows clearly
   point to management panel's "Save entire collection".
   ⚠️ **Private collections don't give manifest to strangers** (manifest contains all entries' paths and
   hashes, leaking = directory leak): `collectionHashesLocked` includes the collection's own hash
   in the collection's level validation——missing it means private collection's manifest gets taken by strangers.

### 12.7 Fallback Tests

- `back/internal/service/nodeshare_scope_test.go`: Runtime selection overrides env vars,
  volume root/invalid hash batch rejection, single file check and directory share don't interfere, `by_dir` marking,
  partial update doesn't change untransmitted items, directory callback only notifies additions, corrupted file falls back to config.
- `back/internal/service/nodeshare_level_test.go`: Three-level boundaries (public listed /
  unlisted not listed but fetchable / private blocks strangers, allows friends and self), loosest merge,
  invalid level batch reject, level and friends persist across restart, historical string entries read as public,
  collection visibility downgrade; `TestNodeShareCollectionManifestFollowsLevel`: collection's **own
  hash** also governed by that collection's level (private = strangers can't even get manifest,
  unlisted = manifest and entries still fetchable).
- `back/internal/transport/peerjs_service_test.go`
  `TestServeFile_PrivateDeniedToStrangers`: Gate wiring actually works in `serveFile`
  (strangers get error, friends and self get data).
- `front/tests/netdisk.test.jsx` `pages/Drive share check`: Checking a file sends hash
  list (not entire scope), master switch only sends `enable`, endpoints unavailable hides controls;
  `pages/Drive share level`: Not shared shows no dropdown, changing level keeps `shared=true`,
  friend list splits by comma, dropdown backfills current level;
  `pages/Drive share directory`: Adding directory **sends existing directories too** (`PUT`'s `dirs` is whole
  replace, only sending new one silently clears others), new directory with selected level, removal only sends remaining,
  empty input doesn't send request.
- `front/tests/e2e-admin-smoke.mjs`: Real node read -> check -> verify -> change level ->
  write friends -> reject misspelled level -> reject volume root -> share directory round-trip (write/read back/clear).
- `packages/peerdrive-client/test/panel-contract.test.mjs`: Panel's two contracts——
  must carry local id when connecting out, share links never include psk, copy must have fallback path, early startup doesn't touch DOM;
  collection links: title row has full-pack "Link" button (with `stopPropagation`, otherwise accidentally expands details),
  can identify collections (list hit + `Array.isArray(obj.entries)` + `LINK_SNIFF_BYTES` limit),
  identification results tracked by node+hash, placeholder at `resolveLinked` after **second judgment** (sync hit
  list path already repainted once, unconditional placeholder write would overwrite just-drawn table——actually stepped on:
  `linkedKind` already `collection` but `#linked` has 0 rows), entries can be individually saved/previewed/shared again.
  (Panel is file:// IIFE, can't run tests, only this way to pin down "change breaks silently" edge cases.)
- `packages/peerdrive-client/scripts/verify-panel-share.mjs`: Real browser end-to-end
  (needs node + signaling + playwright, 12 items): Fixed id -> unlisted not in list but link fetchable ->
  private rejects strangers -> fixed id in friend list, same page immediately fetchable -> collection full-pack link
  (list identifies as collection and lists entries / unlisted fetches manifest by hash and not in list /
  private can't even get manifest).
- `scripts/netdisk-local-demo.sh` step [6]: Real two-node **unlisted collection full-pack
  save** (create collection -> set unlisted -> assert not in list -> `POST /p2p/pull/collection`
  creates task by manifest hash -> entries persist to disk in directory structure with matching sha256).
- `back/internal/service/peerpull_test.go` `TestFetchManifest`: The fallback path itself——
  manifest fetch, size limit `maxBytes` effective, invalid hash errors, **peer rejection (private
  blocked by ShareGate) must propagate** rather than swallowed as "empty collection".
