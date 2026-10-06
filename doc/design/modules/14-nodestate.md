# Module 14: nodestate Runtime Shared State

- **Code Location**: `back/internal/nodestate` (single file `back/internal/nodestate/nodestate.go`, no `_test.go` in directory)
- **One-line Function**: Stores node runtime identity/connection state (operator username, reg server URL, auth token, peerID) in process-level package variables, and provides the ability to report transfer statistics to the registration server; it is an independent package to break the controller↔service circular import (per the package comment); currently it is only read by controller, with **no write callers whatsoever**.
- **Dependencies**: Go standard library only (`sync`/`encoding/json`/`net/http`/`net/url`/`bytes`/`time`/`fmt`, `back/internal/nodestate/nodestate.go:5-15`); zero business dependencies.
- **Depended on by**: `back/internal/controller` — the **only** importer in the entire back module (go list scan + symbol search): `back/internal/controller/anon.go` 6 places, `back/internal/controller/p2p.go` 1 place call `nodestate.GetOperator()`. The package comment claims "both controller and service need to access this state" (nodestate.go:2), which is inconsistent with actual import relationships (see §5).

---

## 1. Logic

**Responsibilities** (nodestate.go:1-3 package comment): Store the runtime state of a Peerdrive node (operator username, reg server connection info) and provide statistics reporting; since both controller and service need to access this state, it is separated into its own package to break circular imports.

**Core Shape: Package-level Global Variables, Not a Struct**. This package defines no `struct`/interface; state is simply 4 package-level `string` variables plus a lock (nodestate.go:17-23):

```go
var (
	mu        sync.Mutex
	operator  string
	regURL    string
	authToken string
	peerID    string
)
```

**5 Public API Functions Total**:

| Function | Purpose | Code Location |
|---|---|---|
| `Configure(op, url, token, pid string)` | Set all four fields at once | nodestate.go:26-33 |
| `SetOperator(username string)` | Set operator separately (empty string = anonymous) | nodestate.go:36-40 |
| `GetOperator() string` | Read operator | nodestate.go:43-47 |
| `GetPeerID() string` | Read peerID | nodestate.go:50-54 |
| `ReportStats(uploadBytes, downloadBytes int64)` | Report transfer statistics to reg server | nodestate.go:57-86 |

Every function except `Configure` independently acquires `mu.Lock()` for protection (write functions 27-32/37-39, read functions 44-46/51-53, ReportStats locks to copy snapshot then unlocks 58-62).

**Two Main Flows**:

1. **Read Flow (currently the only truly triggered flow)**: HTTP request arrives at controller's anonymous collection endpoint → handler calls `nodestate.GetOperator()` to get this node's operator → passed as `owner`/`requester` into `service.AnonService` (`back/internal/controller/anon.go:51,97,141,163,239,317`) → service writes the value into the anonymous collection JSON's `Owner` field for persistence (`back/internal/service/anon_service.go:131,140-160`). Also returned to the frontend via `GET /p2p/auth/status` (p2p.go:895).
2. **Report Flow (exists by design, currently no callers)**: After `ReportStats` guards pass, it POSTs `peerID` and upload/download byte counts to `regURL + "/auth/node/stats"`, with `Authorization: Bearer <authToken>` header (nodestate.go:78-81).

**Lifecycle**: Process-level. Package-level variables exist from process startup (zero-value empty strings), no init function, no `Close`/`Stop` hooks; all state disappears on process exit. The startup/graceful shutdown flow in `internal/serverapp/app.go` (main.go:49-285) does not touch this package at all.

> Note: The module map defines it as "operator/reg/peerID process-shared state (independent package to break circular imports)" (doc/design/how-to-connect.md:28, doc/design/README.md:29); the architecture review document concludes "nodestate package exists solely to resolve circular references" (doc/archive/report/ARCHITECTURE-REVIEW.md:34-35).

---

## 2. Storage

**Medium and Location: Pure In-Memory, No Persistence.** State exists only in the package-level variables at `back/internal/nodestate/nodestate.go:17-23`, with no disk/DB/file writes, and no exported persistence paths — this package does not import repository/storage, the dependency list contains only standard library (nodestate.go:5-15).

**In-Memory Composition**: 4 `string` values (`operator`, `regURL`, `authToken`, `peerID`) + 1 `sync.Mutex`; no struct instances, no map/slice. Initial values are all Go zero-value empty strings.

**Lifecycle**: Exists from process startup → cleared on process exit, no intermediate persistence. **After process restart, all four fields return to `""`**, with no recovery mechanism (no code path found that reloads from disk/DB; `internal/serverapp/app.go` full flow does not touch this package).

**Delegation Relationship (values flow downstream and are persisted by downstream, but this package does not persist itself)**: Values read by `GetOperator()` are passed by controller as `owner`/`requester` to `service.AnonService`, and ultimately **written by the service layer** into content-addressed anonymous collection JSON and registered in the database (`back/internal/service/anon_service.go:128-160`: `coll.Owner = owner` participates in `json.MarshalIndent` → `sha256Hex` → write to `storage/<hash[:2]>/<hash>` → `repository.InsertFileMeta` + `InsertFileProvider`). In other words: nodestate provides an in-process "staging slot"; persistence occurs in the downstream service/repository/storage chain (see §6 connections 03/04).

---

## 3. When to Store

Strictly distinguishing "write" and "read" directions, presented as-is:

**Writes (all 4 write entry points have NO callers — exist by design, zero trigger points in current code, marked as unverified):**

| Write Entry Point | Declared Timing in Code | Actual Trigger Point |
|---|---|---|
| `Configure` | Comment states "called by NodeRegistrar.Start()" (nodestate.go:25), i.e., during node startup | **Unverified: no `NodeRegistrar` type/call site exists anywhere in the repository** (go list scan + symbol search found none) |
| `SetOperator` | Comment semantics: "operator account registered after node logs into regserver" (compare p2p.go:888-890 and doc/REFACTOR.md:567,595 semantic descriptions) | **Unverified: no call sites at all**, operator is therefore always empty string |
| `ReportStats` | Comment semantics: report transfer statistics to registration server when node is authenticated (nodestate.go:56) | **Unverified: no call sites** (compare doc/modules/auth/API-DESIGN.md:627 designed `POST /stats/report` batch window, also not implemented) |

There is also a read-only API `GetPeerID` (nodestate.go:50-54) with **no callers at all** (unverified).

**Reads (the only actual access happening currently, all are per-request synchronous reads):**

| Trigger Timing (HTTP Request) | Read Point |
|---|---|
| `POST /anon/collections` (create anonymous collection) → `CreateAnonCollection`, gets operator as `owner` | anon.go:51 |
| `PUT /anon/collections/:hash/visibility` (visibility switch) → `SetAnonCollectionVisibility`, as requester for access check | anon.go:97 |
| `GET /anon/collections/:hash` (read metadata) → `GetAnonCollection` | anon.go:141 |
| `GET /anon/collections/:hash/:filepath` (download entry) → `DownloadAnonFile` | anon.go:163 |
| `POST /anon/collections/fork` (fork) → `ForkAnonCollection` | anon.go:239 |
| `POST /anon/collections/commit` (commit new version) → `CommitAnonCollection` | anon.go:317 |
| `GET /p2p/auth/status` → `AuthStatus`, puts operator in response | p2p.go:895 |

No scheduled tasks, no event callbacks, no graceful shutdown hooks touch this package.

---

## 4. What is Stored

**The 4 self-held fields of this package** (all in-memory, no initialization logic beyond default values):

| Field | Type/Initial Value | Write Function | Read/Consumer | Key Constraints |
|---|---|---|---|---|
| `operator` | `string`, initial `""` | `Configure`(29)/`SetOperator`(38) | `GetOperator`(43) → anon.go 6 places, p2p.go:895 | Empty string = anonymous node (nodestate.go:35,42 comments); **no length/format validation** |
| `regURL` | `string`, initial `""` | `Configure`(30) | `ReportStats`(59,78) | Returns directly when empty (64-66); used as HTTP POST prefix, directly concatenated with `/auth/node/stats` (78) |
| `authToken` | `string`, initial `""` | `Configure`(31) | `ReportStats`(60,80) | Returns directly when empty (64-66); used as `Authorization: Bearer <token>` header (80) |
| `peerID` | `string`, initial `""` | `Configure`(32) | `GetPeerID`(50), `ReportStats`(61,72) | Returns directly when empty (64-66); `peer_id` field in report JSON (72) |

**Content delegated to downstream for persistence (not belonging to this package's fields, but values originate from this package, for traceability)**: `AnonCollection.Owner` — written to the anonymous collection JSON tail (`back/internal/service/anon_service.go:131`), participates in content-addressed digest hash calculation (138); constraints: `private` visible only to Owner, `restricted` allows Owner+AccessList (`back/internal/controller/anon.go:139-163`); unauthorized access returns 404 uniformly (anon_service.go:212-216); hash is 64-character lowercase hexadecimal sha256 (anon_service.go:36-40,138), files stored in `storage/<hash[:2]>/<hash>`, 0644 (anon_service.go:140-149).

---

## 5. Boundaries and Pitfalls

1. **Currently a "read-only empty package"**: No callers of `Configure`/`SetOperator`/`ReportStats`/`GetPeerID` exist anywhere in the repository → `operator` is always `""`. Consequence: anonymous collections created will always have empty Owner, `private` visibility has no owner and cannot be read (`CanView` returns false for empty requester, model/anon.go:158-159), frontend disables "only self" option based on this (doc/REFACTOR.md:567). There is a gap between this and the design intent of "operator only has a value after node logs into regserver" (p2p.go:888-890) — the "writer not implemented" gap.
2. **Concurrency Safety**: All public function reads/writes hold `mu` (nodestate.go:27-32,37-39,44-46,51-53,58-62). `ReportStats` uses the pattern of locking to copy snapshot, unlocking before making HTTP call, avoiding holding the lock during network operations (58-62); this is intentional, callers should not assume read/write atomicity beyond a single function's scope.
3. **Silent Failures**: `ReportStats` swallows all errors — HTTP error `err != nil` directly returns (82-85), response body only closes without reading or checking status codes (85); when upload/download are both 0, no request is sent (67-69). No logging, no retries, no queue.
4. **Proxy Bypass**: `localClient` connects directly without environment proxy for `localhost`/`127.*`/`::1`, others use `http.ProxyFromEnvironment` (88-103); Dial and overall timeout are both 10s (100,102). Note: the "local determination" here is for the HTTP client, unrelated to the "local WS admin plane".
5. **Package comments and current state/docs have multiple inconsistencies**:
   - Comments say both controller and service access this package (nodestate.go:2), actually only controller imports it (see §Depended on by).
   - `Configure` comment references `NodeRegistrar.Start()` (nodestate.go:25) which does not exist in the repository (doc/archive/TRANSPORT-REVIEW2-2026-08-16.md:163 planned `NodeRegistrar`/`RelayRegistry` with `Stop()` also not implemented).
   - `doc/FILE-REFERENCE.md:167` records this package managing "online/offline/busy" state, which are not in the code.
   - This package has **no tests at all** (directory only contains nodestate.go; doc/archive/report/ARCHITECTURE-REVIEW.md:84 also records this).
6. **`username` ≠ `operator`**: `AuthOptional`/`AuthRequired` middleware puts the "request caller's account" into gin context (`c.Set("username", ...)`, auth_middleware.go:43-50,78-79), sourced from per-request remote verification of reg server `/auth/whoami` (auth_middleware.go:147-168, 30s cache); while `operator` is "the account registered after node logs into regserver", the two have completely different sources (p2p.go:888-890 comments explicitly state this). Middleware **never writes** to nodestate.
7. **`var _ = fmt.Sprintf` (nodestate.go:105-106)**: Placeholder added to keep the `fmt` import — `fmt` is not actually used in this package, a historical artifact, don't mistake it for having formatting logic.
8. **No corresponding connection documentation for reg server statistics reporting**: None of the 13 connections/ documents cover the node → reg server `/auth/node/stats` reporting (nodestate.go:78), an undocumented external protocol; the established auth-direction connection is router middleware → reg server `/auth/whoami` (see §6 connection 02).

---

## 6. External Connections

nodestate has no dedicated connection documentation; its reads/writes go through controller/service handshakes with other modules. Related connections (directions are relative to nodestate):

- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md): Outbound (read). Frontend `getAuthStatus()` sends `GET /p2p/auth/status` via local WS admin frame (`front/src/api.js:2-8,574`; admin plane only goes through local WS), `AuthStatus` puts `nodestate.GetOperator()` into the response (p2p.go:891-896), frontend decides whether "only self" option is available based on this (p2p.go:890).
- [../connections/02-router-controller.md](../connections/02-router-controller.md): Peripheral. Router's `AuthOptional`/`AuthRequired` middleware does **request-level** token verification and puts username in gin context (auth_middleware.go:28-82, `RegistrationServer` empty means pass through, auth_middleware.go:23-26) — it reads reg server but **not through nodestate**; nodestate is only read by controller's `AuthStatus` endpoint (p2p.go:867-897).
- [../connections/03-controller-service.md](../connections/03-controller-service.md): Outbound (read → delegate). Controller passes `nodestate.GetOperator()` as `owner`/`requester` to `AnonService` (anon.go:51,97,141,163,239,317), service side does visibility gating and unauthorized access determination (anon_service.go:207-218,224-251,260-330).
- [../connections/04-service-repository.md](../connections/04-service-repository.md): Indirect outbound (values ultimately persisted to DB). `Owner` is written with anonymous collection JSON to content-addressed storage and registered in SQLite `file_meta` (anon_service.go:131,140-160) — persistence happens in the downstream chain of this connection, nodestate does not persist itself.

Related module documentation (same directory): [05-controller.md](05-controller.md) (reader: anonymous collection endpoints and `AuthStatus`, 05-controller.md:161 already records username/operator distinction), [06-service.md](06-service.md) (`AnonService` consumes and persists this value), [01-config.md](01-config.md) (`RegistrationServer`/`PEERDRIVE_REG_SERVER` is the only source for reg server address, config.go:34,195; main.go:186-188 only injects into router middleware, not nodestate).
