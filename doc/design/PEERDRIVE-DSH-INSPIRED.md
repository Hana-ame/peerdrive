# Peerdrive Composition-Based Architecture Design Inspired by dsh

> Date: 2026-08-16
> Goal: Borrowing from DeepSeek Harness (`dsh`)'s **profile / bundle / patch / plugin-row** composition model,
> transform Peerdrive from "a single monolithic main sequential initialization" to a "harness with an empty base + ordered module bundles + user override layer".
> This design also satisfies `today-final-requirements.txt`: the first commit is the base, each module developed on independent branches, finally merged for integration.

---

## 1. Why Reference dsh

Current Peerdrive backend is a typical monolithic assembly:

```go
InitDB → NewP2PService → NewIPFSService → NewPeerJSService → SetupRouter → Run
```

Problems with this approach:

| Problem | Current State |
|---|---|
| Module boundaries only exist in directories, not in assembly | `service/` has 30+ services, `main.go` sequentially News all of them |
| Branch merges easily conflict | Every module modifies `main.go`, `config.go`, `router.go` |
| Cannot trim by deployment form | Public defaults bring all legacy (libp2p/BT/IPFS) into the build |
| Configuration scattered across env vars + code defaults | No "layers", deployment overrides must modify code or rely on env |
| Frontend/backend modules cannot be released in pairs | A feature often requires changes to both `front/` and `back/` |

dsh's composition model solves the same class of problems:

- **Start with empty root**: profile root is just an empty list, all capabilities come from bundle patches.
- **Ordered bundles**: `dsh.profile.bundles` determines bundle order, `cordis.patch.yml` is the per-layer patch.
- **Override by id**: A later patch for the same `id` entirely replaces `config`, last write wins.
- **Profile = Product form**: `web` / `headless` are different profiles, sharing the base bundle.
- **User override last**: Profile's own patch, home-level patch, `--patch` overlay are stacked in sequence.

Peerdrive doesn't need to replicate Cordis/JS exactly, but can reproduce this mental model using Go interfaces + YAML manifests.

---

## 2. Core Concept Mapping

| dsh (DeepSeek Harness) | Peerdrive Design |
|---|---|
| `dsh --profile <name>` | `peerdrive --profile <name>` or `pd --profile <name>` |
| Profile directory (`$DSH_HOME/profiles/<name>`) | `profiles/<name>/`, containing `peerdrive.yml` + `peerdrive.patch.yml` |
| `dsh.profile.bundles` in `package.json` | `bundles: [base, storage, ...]` in `peerdrive.yml` |
| Bundle (`@deepseek-ai/dsh-base` etc.) | `back/bundles/<name>` (Go package + `bundle.yml`), paired with `front/bundles/<name>` when needed |
| `cordis.patch.yml` (bundle patch) | `back/bundles/<name>/bundle.patch.yml` / inline patch in `bundle.yml` |
| Plugin row (`id/name/config`) | Component row (`id/type/config/disabled/inject`) |
| Later layer replaces entire config by `id`, last write wins | Same: later layer patch replaces entire row config by `id` |
| `--patch <path>` override layer | `peerdrive --patch <path>` / `PEERDRIVE_PATCH` |
| `--dump-config` | `peerdrive config --dump` |
| `dsh plugin --profile web add <pkg>` | `peerdrive module add <bundle>` (scaffolding/template generation) |
| Bundle contains both host-side and browser-side | `back/bundles/<name>` + `front/bundles/<name>` exist as pairs |

---

## 3. Target Directory Structure

Preserving existing `/front`, `/back`, `/doc` root directories, adding a composition layer:

```
peerdrive/
├── back/
│   ├── cmd/
│   │   ├── peerdrive/          # Harness entry: parse profile -> assemble bundles
│   │   ├── server/             # Legacy entry for compatibility, equivalent to --profile node --profile web
│   │   └── peerserver/         # Self-hosted signaling/discovery server (can also be a bundle)
│   ├── internal/
│   │   ├── harness/            # Composition engine: Bundle/Profile/Row/Patch/Scope
│   │   ├── core/               # Base: config/log/db/router/cas/nodeinfo
│   │   └── ...
│   └── bundles/
│       ├── base/               # Base bundle, first layer for all profiles
│       ├── auth/               # Auth/users/JWT/permissions/stats
│       ├── storage/            # File indexing, upload/download, collection
│       ├── sync/               # Seq incremental sync, tombstone
│       ├── transport/          # PeerJS/WebRTC + WS sessions + frame protocol
│       ├── discovery/          # Static peer / MQTT / HTTP discover / signalserver
│       ├── web/                # Frontend dist, API gateway, trust fence
│       └── legacy/             # libp2p / BT / IPFS, isolated and optional
├── front/
│   ├── bundles/                # Each frontend feature package
│   │   ├── core/               # Layout, routing, API client
│   │   ├── auth/               # Login/user/permissions UI
│   │   ├── storage/            # File management/upload/download UI
│   │   ├── sync/               # Sync status UI
│   │   ├── transport/          # P2P panel/node/transfer UI
│   │   └── discovery/          # Discovery/signaling config UI
│   └── src/                    # Application source code composed from bundles (build-time generated/explicit imports)
├── profiles/
│   ├── node/                   # Pure persistent node profile
│   ├── web/                    # Local Web + node profile (corresponds to current server+front)
│   └── server/                 # Self-hosted signaling/discovery server profile
└── doc/
```

> Note: Go does not have npm-like runtime dynamic loading; "bundles" use **compile-time composition + runtime config override**:
> Profiles list which bundles, `cmd/peerdrive` links them into the current binary by importing these bundles' registration functions;
> `--patch` only modifies config rows, does not add new code. This is consistent with dsh's "dynamic pluggability" spirit while maintaining Go single-binary deployment.

### 3.1 Relationship with REFACTOR.md Target Package Structure

`doc/REFACTOR.md` already provides dependency layering; this design does not overturn it but further wraps "package structure" into "composable bundles":

| REFACTOR.md Target Layer | Attribution in This Design |
|---|---|
| `domain/` (zero-dependency domain models) | `back/bundles/base` or independent `internal/domain`, common dependency for all bundles |
| `config/`, `log/` (infrastructure leaves) | Provided by `base` bundle |
| `repository/` (persistence) | Split by domain: `auth` owns user repo, `storage` owns file_index/collection repo, `sync` owns sync repo |
| `provider/` (file acquisition abstraction) | Provided by `storage` bundle, `legacy` can register additional providers (BT/IPFS/HTTP) |
| `service/` (use case orchestration) | Each business bundle holds its own service directory internally |
| `transport/` (interconnection transport) | `transport` bundle |
| `legacy/` (old stack isolation) | `legacy` bundle, default disabled |
| `api/` (HTTP layer) | Routes from `web`/`auth`/`storage` etc.; unified gateway mounted by `web` bundle |

This preserves REFACTOR's layering discipline while gaining dsh's "per-module branch development, per-profile trimming" capability.

---

## 4. Composition Engine (`internal/harness`)

### 4.1 Minimal Model

```go
// Component is the minimal interface for each mountable service
type Component interface {
    ID() string
    Start(ctx context.Context, s *Scope) error
    Close(ctx context.Context) error
}

// Bundle describes a module package
type Bundle struct {
    Name    string
    Version string
    Patch   []PatchOp          // insert / patch / disable
    Build   func(b *Builder) error
}

// Profile describes a product form
type Profile struct {
    Name    string
    Bundles []string           // Ordered bundle list
    Patch   []PatchOp          // Profile's own override layer
}

// Scope is the shared context accessible by components
type Scope struct {
    Config  *Config            // Merged config tree
    Log     *Logger
    DB      *DB
    Router  *Router
    Storage *CASStore
    Env     map[string]string
}
```

### 4.2 Patch Operations

Referencing dsh's `cordis.patch.yml`, Peerdrive's patches are also "row operations":

```yaml
# Insert new row
- insert:
    - id: transport
      type: peerjs-transport
      config:
        enable: true
        host: 0.peerjs.com

# Override existing row (entire config replaced, last write wins)
- id: transport
  config:
    enable: true
    host: peersignal.moonchan.xyz
    key: pd-signal-b9447b406828e500

# Disable
- id: legacy
  disabled: true
```

### 4.3 Assembly Order

```
Empty root
  → bundle[base]    patches
  → bundle[storage] patches
  → bundle[transport] patches
  → ...
  → profile/peerdrive.patch.yml
  → $PEERDRIVE_HOME/peerdrive.patch.yml
  → --patch override layer
```

Later layers entirely replace config for the same `id`, implementing "defaults from bundles, deployment/local values from user layers".

### 4.4 Profile Examples

```yaml
# profiles/web/peerdrive.yml
name: web
bundles:
  - base
  - auth
  - storage
  - sync
  - transport
  - discovery
  - web
```

```yaml
# profiles/node/peerdrive.yml
name: node
bundles:
  - base
  - storage
  - sync
  - transport
  - discovery
```

```yaml
# profiles/server/peerdrive.yml
name: server
bundles:
  - base
  - discovery          # Only enable signaling/discovery part
```

### 4.5 Startup Process Pseudocode

```go
func main() {
    p := harness.LoadProfile(flag.Profile)
    b := harness.NewBuilder()

    for _, name := range p.Bundles {
        bundle := bundles.Get(name)          // Compile-time registry
        if err := bundle.Build(b); err != nil { fatal(err) }
    }
    if err := harness.ApplyPatches(b, p.Patch,
        homePatch(), flag.Patches...); err != nil { fatal(err) }

    scope := harness.NewScope(b.Compose())
    for _, c := range scope.Components() {   // Start by dependency service availability
        go c.Start(ctx, scope)
    }
    harness.WaitSignal(ctx)
}
```

---

## 5. Module Bundle Design (Draft)

### 5.1 base (Base, the part that exists in the first commit)

Responsibilities:
- `config` loading and layered merging
- Logging
- SQLite initialization / migration entry point
- Empty Gin Router shell + `/healthz` + `/api/node/info`
- Content-addressed storage (sha256 -> storage/xx/hash)
- Node identity (peer id / instance id)

Does not include: auth, file indexing, P2P, frontend.

### 5.2 auth

Inserted components:
- `auth-service`: Users / JWT / OAuth / permissions
- `auth-middleware`: HTTP auth middleware
- `usage-stats`: Upload/download volume statistics (anti-forgery see `auth-features.txt`)

### 5.3 storage

Inserted components:
- `file-index`: `file_index` table, sha256->path, seq
- `upload-session`: Chunked upload, bitmap, resumable transfer
- `download`: Local file streaming delivery
- `collection`: Anonymous + user collection CRUD

### 5.4 sync

Inserted components:
- `sync-service`: `sync{seq}` incremental pull, `ApplySync` merge, tombstone
- `sync-routes`: HTTP/WS/DataChannel sync interfaces

### 5.5 transport

Inserted components:
- `peerjs`: PeerJS signaling + WebRTC DataChannel
- `ws-session`: Local WS sessions (`/ws/peer`)
- `frame-protocol`: `req/meta/data/done/err` + `create/upload/list/info/delete/sync`
- `rtc-session`: WebRTC session adaptation

### 5.6 discovery

Inserted components:
- `discovery-static`: `PEERDRIVE_PEERJS_PEERS`
- `discovery-mqtt`: Sharded room discovery
- `discovery-http`: Self-hosted `/discover/*`
- `signalserver`: `cmd/peerserver` or bundle-internal service

### 5.7 web

Inserted components:
- `web-api`: API gateway routes (depends on routes registered by other bundles)
- `web-static`: `front/dist` static serving
- `web-trust`: Browser trust fence (Origin/Host whitelist)
- Frontend bundle compiled artifact injection

### 5.8 legacy

Inserted components:
- `libp2p` (old P2P)
- `bt-dht` (BT DHT)
- `ipfs-compat` (IPFS compatibility layer)
- Default `disabled: true`, only enabled when legacy functionality compatibility is needed

---

## 6. Branch and CI Process (Corresponding to "Today's Final Requirements")

### 6.1 First Commit: Base

- Create `back/bundles/base` + `front/bundles/core`
- `profiles/node` + `profiles/web` only attach base (web can attach web shell but no business logic)
- CI: base unit tests + startup health check
- Main branch stays runnable and deployable

### 6.2 One Branch Per Module

Using `feat/transport` as an example:

```
git checkout -b feat/transport
# back/bundles/transport/*
# front/bundles/transport/*
# profiles/web/peerdrive.yml add transport
# profiles/node/peerdrive.yml add transport
# Module tests: unit + transport integration
# Branch CI must be green
```

- Module branches only allowed to modify:
  - Their own `bundles/<name>/`
  - Their own `front/bundles/<name>/`
  - Profile manifest enabling their own row
  - Shared interfaces (`internal/harness`, `Scope`) additions only, no modifications
- Directly modifying other bundles' source code is prohibited; collaborate by merging into main first, then modifying.

### 6.3 Merge Integration

- After each module branch merges into `main`, CI runs **full profile integration tests**:
  - `node` profile: base + storage + sync + transport + discovery
  - `web` profile: node + web
  - `server` profile: base + discovery
- Merging itself may be a pure manifest change (adding a row in `peerdrive.yml` `bundles` list),
  so conflict surface is minimal.

### 6.4 Test Assets

Each bundle should include at least:
- Go unit tests (no external network dependency)
- Integration tests (tag with `integration` when depending on real signaling/public brokers)
- Frontend tests (Vitest, only testing own components)
- A `TEST-MATRIX.md` or test scope clearly documented in README

---

## 7. Migration Path (From Current to Target)

| Phase | Action | Acceptance |
|---|---|---|
| M0 | Implement minimal composition engine in `back/internal/harness`, supporting `base` first | `peerdrive --profile node` can start empty base |
| M1 | Refactor `config.Load` to layered config; split `main.go` initialization into `base/storage/sync/transport/discovery/web` bundles | Existing functionality equivalent, all tests green |
| M2 | Collect auth/legacy into separate bundles; legacy default disabled | Default binary does not contain legacy dependencies or at least does not start them |
| M3 | Organize front into bundles, `front/dist` provided by web bundle | Web profile fully usable |
| M4 | Add `profiles/` and `peerdrive --profile` command, retain old `server` command for compatibility | New entry matches old entry behavior |

Recommended migration order referencing `doc/REFACTOR.md` Section 7:
First fix `internal/harness` interfaces, then wrap existing services into components one by one.
Do not attempt a big-bang refactor all at once.

---

## 8. Environment Variables and Configuration Layering

dsh uses patch layers to manage configuration; Peerdrive should also reduce "scattered env vars":

| Priority (Low → High) | Source |
|---|---|
| 1 | Bundle default config (`bundle.yml`) |
| 2 | Profile default config (`profiles/<name>/peerdrive.yml`) |
| 3 | Repository `peerdrive.patch.yml` (committable) |
| 4 | `$PEERDRIVE_HOME/peerdrive.patch.yml` (local machine, not committed) |
| 5 | `--patch` command-line override |
| 6 | Environment variables `PEERDRIVE_*` (deployment emergency override, maintain compatibility) |

Environment variables are retained but recommended only as the "topmost override", no longer the sole source of default values.

---

## 9. Key Decisions and Rationale

1. **No runtime plugin loading**
   - Go's dynamic plugins (`plugin`) are complex across platforms/build chains, and conflict with CGO/SQLite.
   - Using compile-time registry + YAML profile, gaining composition capability while maintaining single-binary deployment.

2. **Profile is not a bundle**
   - Bundle is a capability package, profile is a deployment form.
   - `web` profile includes `web` bundle; `node` profile does not include it, but both include `base`.

3. **Frontend bundles exist in pairs**
   - A feature with both backend and frontend must be in the same branch/under the same bundle name.
   - Backend bundle provides API, frontend bundle provides UI; integration mounts both simultaneously via web profile.

4. **"Entire row replacement" deliberately mimics dsh**
   - Avoids patch merge semantics causing implicit behavior like "add a key here, delete a key there".
   - Each component writes its complete config in one row; overriders must write complete new config, making behavior predictable.

---

## 10. TODO / Future Design Refinement

- [ ] Define complete Go API and error semantics for `internal/harness`
- [ ] Define `bundle.yml` schema (YAML spec for insert/patch/disable)
- [ ] Define inter-bundle service dependency declarations (dsh uses inject, Peerdrive can simplify to Scope fields)
- [ ] Design `peerdrive module add <name>` scaffolding (generate back/front bundle + test + CI templates)
- [ ] Clarify legacy package compilation isolation method (build tag? independent Go module?)
- [ ] Merge with `doc/REFACTOR.md` target package structure to avoid conflicting dual layering
