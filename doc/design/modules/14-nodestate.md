# Module 14: nodestate Runtime Shared State

- **Code Location**: `back/internal/nodestate` (single file `back/internal/nodestate/nodestate.go`, no `_test.go` in directory)
- **One-line Function**: Stores the node operator username in a process-level package variable and exposes it read-only; it is an independent package to break the controller↔service circular import (per the package comment). **Since 2026-10-07 it is a single-field, single-function package** — `GetOperator()` is the only exported symbol, and nothing in the tree can write `operator` (see §1 "Deleted API").
- **Dependencies**: Go standard library only (`sync`, `back/internal/nodestate/nodestate.go:42`); zero business dependencies.
- **Depended on by**: `back/internal/controller` — the **only** importer in the entire back module (go list scan + symbol search): `back/internal/controller/anon.go` 6 places, `back/internal/controller/p2p.go` 1 place call `nodestate.GetOperator()`.

---

## 1. Logic

**Responsibilities** (post-2026-10-07): hold the node's operator username so controller handlers can stamp it onto anonymous collections and report it in `GET /p2p/auth/status`.

**Core Shape: Package-level Global Variable, Not a Struct**. This package defines no `struct`/interface; state is a single package-level `string` plus a lock (`nodestate.go:44-47`):

```go
var (
	mu       sync.Mutex
	operator string
)
```

**1 Public API Function**:

| Function | Purpose | Code Location |
|---|---|---|
| `GetOperator() string` | Read operator (always `""` today — see §5.1) | nodestate.go:49-54 |

**1 Flow — the read flow (the only one left)**: HTTP request arrives at controller's anonymous collection endpoint → handler calls `nodestate.GetOperator()` → passed as `owner`/`requester` into `service.AnonService` (`back/internal/controller/anon.go:51,97,141,163,239,317`) → service writes the value into the anonymous collection JSON's `Owner` field for persistence (`back/internal/service/anon_service.go:131,140-160`). Also returned to the frontend via `GET /p2p/auth/status` (p2p.go:895).

### Deleted API (2026-10-07)

Four exported functions were removed after a whole-module symbol search found **zero call sites** for each. These are plain exported Go functions, and the only importer of the package in the whole tree is `internal/controller` — so "zero call sites" is a complete statement, not a grep approximation; there is no reflection or registry indirection that could hide a caller.

| Deleted | Why |
|---|---|
| `Configure(op, url, token, pid)` | Its only writer caller was `NodeRegistrar`, removed with the libp2p stack in commit `a5b090d`. Zero call sites since. |
| `SetOperator(username)` | No caller. |
| `GetPeerID()` | No caller. |
| `ReportStats(upload, download)` | No caller — **and the endpoint it POSTed to does not exist**: it targeted `regURL + "/auth/node/stats"`, while the bundled registration server only serves `POST /auth/register`, `POST /auth/login`, `GET /auth/whoami`, `GET /auth/list` (`back/internal/regserver/regserver.go:493-496`). This was a fake implementation that could never have succeeded while implying a "stats are reported to the registration server" capability that does not exist — the same failure mode as the three dead config fields deleted on 2026-10-04 (`back/internal/config/config.go`, see §5.5). Its `localClient()` helper, the `var _ = fmt.Sprintf` placeholder, and the `regURL`/`authToken`/`peerID` fields died with it. |

**Why the package was not deleted outright**: `GetOperator()` has 7 real consumers (§Depended on by), so the package survives as a one-function accessor. Inlining it into controller would be churn for no gain, and keeping it means that when a real operator identity is defined (see §5.1) this becomes a one-line change in one place rather than a sweep across seven call sites.

**Lifecycle**: Process-level. The package variable exists from process startup (zero-value empty string), no init function, no `Close`/`Stop` hooks; all state disappears on process exit. The startup/graceful shutdown flow in `internal/serverapp/app.go` does not touch this package at all.

> Note: the module map still describes it as "operator/reg/peerID process-shared state" (doc/design/how-to-connect.md:33); after this cleanup only `operator` remains.

> Note: The module map defines it as "operator/reg/peerID process-shared state (independent package to break circular imports)" (doc/design/how-to-connect.md:28, doc/design/README.md:29); the architecture review document concludes "nodestate package exists solely to resolve circular references" (doc/archive/report/ARCHITECTURE-REVIEW.md:34-35).

---

## 2. Storage

**Medium and Location: Pure In-Memory, No Persistence.** State exists only in the package-level variable at `back/internal/nodestate/nodestate.go:44-47`, with no disk/DB/file writes, and no exported persistence paths — this package does not import repository/storage, the dependency list contains only the standard library (`sync`).

**In-Memory Composition**: 1 `string` (`operator`) + 1 `sync.Mutex`; no struct instances, no map/slice. Initial value is the Go zero-value empty string. (Was 4 strings + mutex before the 2026-10-07 cleanup; see §1 "Deleted API".)

**Lifecycle**: Exists from process startup → cleared on process exit, no intermediate persistence. **After process restart, `operator` returns to `""`**, with no recovery mechanism (no code path reloads it; `internal/serverapp/app.go` does not touch this package). In fact it never leaves `""` — there is no writer at all (§5.1).

**Delegation Relationship (values flow downstream and are persisted by downstream, but this package does not persist itself)**: Values read by `GetOperator()` are passed by controller as `owner`/`requester` to `service.AnonService`, and ultimately **written by the service layer** into content-addressed anonymous collection JSON and registered in the database (`back/internal/service/anon_service.go:128-160`: `coll.Owner = owner` participates in `json.MarshalIndent` → `sha256Hex` → write to `storage/<hash[:2]>/<hash>` → `repository.InsertFileMeta` + `InsertFileProvider`). In other words: nodestate provides an in-process "staging slot"; persistence occurs in the downstream service/repository/storage chain (see §6 connections 03/04).

---

## 3. When to Store

Strictly distinguishing "write" and "read" directions, presented as-is:

**Writes: there are none.** As of the 2026-10-07 cleanup this package has **no write entry point at all** — `Configure`, `SetOperator` and `ReportStats` were all deleted for having zero call sites (see §1 "Deleted API"), and `GetPeerID` was deleted for the same reason. So `operator` is structurally always `""`, and there is no timing at which it could ever become non-empty. The intended semantics ("operator only has a value after the node logs into regserver") remain unimplemented; the design record for that lives in p2p.go:888-890 and doc/modules/auth, and the sequencing constraint (identity last) is in doc/ROADMAP.md.

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

**2026-10-07: the package now holds exactly one field.** `regURL`/`authToken`/`peerID` and the four functions that only existed to feed `ReportStats` were deleted — see §1 "Deleted API" below.

| Field | Type/Initial Value | Write Function | Read/Consumer | Key Constraints |
|---|---|---|---|---|
| `operator` | `string`, initial `""` | **none left in the tree** (the two writers were deleted; see below) | `GetOperator()` → anon.go 6 places, p2p.go:895 | Empty string = anonymous node; **no length/format validation** |

**Content delegated to downstream for persistence (not belonging to this package's fields, but values originate from this package, for traceability)**: `AnonCollection.Owner` — written to the anonymous collection JSON tail (`back/internal/service/anon_service.go:131`), participates in content-addressed digest hash calculation (138); constraints: `private` visible only to Owner, `restricted` allows Owner+AccessList (`back/internal/controller/anon.go:139-163`); unauthorized access returns 404 uniformly (anon_service.go:212-216); hash is 64-character lowercase hexadecimal sha256 (anon_service.go:36-40,138), files stored in `storage/<hash[:2]>/<hash>`, 0644 (anon_service.go:140-149).

---

## 5. Boundaries and Pitfalls

1. **`operator` has no writer — it is structurally always `""`**: as of the 2026-10-07 cleanup there is **no code path that can assign it** (both writers were deleted along with the rest of the dead API). Consequence: anonymous collections created will always have empty Owner, `private` visibility has no owner and cannot be read (`CanView` returns false for empty requester, model/anon.go:158-159), frontend disables "only self" option based on this. This is **not a regression**: the deleted writers had no callers either, so the value was already always `""` at runtime — the difference is that now the code cannot pretend otherwise. There remains a gap versus the design intent of "operator only has a value after node logs into regserver" (p2p.go:888-890) — the "writer not implemented" gap. **Do not add a writer ad hoc**: deciding what an operator *is* (per-user session vs. node owner) belongs to the identity work in doc/modules/auth, which is explicitly sequenced last in doc/ROADMAP.md.
2. **Concurrency Safety**: `GetOperator()` holds `mu` while reading (nodestate.go:51-53). The lock is currently uncontended, but it is kept so that adding a writer later is race-free by construction rather than by review.
3. ~~**Silent Failures**~~ — resolved by deletion: `ReportStats` swallowed every error (HTTP error returns immediately, response status never checked, no logging/retry/queue). Removed with the function.
4. ~~**Proxy Bypass**~~ — resolved by deletion: `localClient`'s localhost-proxy-bypass special case existed only for `ReportStats`. Removed with it.
5. **Package comments and current state/docs have multiple inconsistencies**:
   - The package comment claimed "both controller and service need to access this state"; only controller ever imported it. Corrected in the 2026-10-07 package comment.
   - `Configure`'s comment referenced a non-existent `NodeRegistrar.Start()` (doc/archive/TRANSPORT-REVIEW2-2026-08-16.md:163 planned `NodeRegistrar`/`RelayRegistry` with `Stop()`, also never implemented). Gone with the function.
   - `doc/FILE-REFERENCE.md` recorded this package managing "online/offline/busy" state, which were never in the code. Row updated to reflect the one remaining function.
   - This package has **no tests at all** (directory only contains nodestate.go; doc/archive/report/ARCHITECTURE-REVIEW.md:84 also records this). The remaining function is a pure getter, so the gap is much smaller than it was.
6. **`username` ≠ `operator`**: `AuthOptional`/`AuthRequired` middleware puts the "request caller's account" into gin context (`c.Set("username", ...)`), sourced from per-request remote verification of reg server `/auth/whoami` (auth_middleware.go, 30s cache); while `operator` is "the account registered after node logs into regserver", the two have completely different sources (p2p.go:888-890 comments explicitly state this). Middleware **never writes** to nodestate — and, as of this cleanup, nothing does.
7. ~~**`var _ = fmt.Sprintf`**~~ — resolved by deletion: the placeholder existed only to keep the `fmt` import alive for the removed code.
8. ~~**No connection documentation for reg server statistics reporting**~~ — resolved by deletion: the undocumented node → reg server `/auth/node/stats` protocol no longer exists anywhere in the tree. The established auth-direction connection is router middleware → reg server `/auth/whoami` (see §6 connection 02).

---

## 6. External Connections

nodestate has no dedicated connection documentation; its reads/writes go through controller/service handshakes with other modules. Related connections (directions are relative to nodestate):

- [../connections/01-frontend-backend.md](../connections/01-frontend-backend.md): Outbound (read). Frontend `getAuthStatus()` sends `GET /p2p/auth/status` via local WS admin frame (`front/src/api.js:2-8,574`; admin plane only goes through local WS), `AuthStatus` puts `nodestate.GetOperator()` into the response (p2p.go:891-896), frontend decides whether "only self" option is available based on this (p2p.go:890).
- [../connections/02-router-controller.md](../connections/02-router-controller.md): Peripheral. Router's `AuthOptional`/`AuthRequired` middleware does **request-level** token verification and puts username in gin context (auth_middleware.go:28-82, `RegistrationServer` empty means pass through, auth_middleware.go:23-26) — it reads reg server but **not through nodestate**; nodestate is only read by controller's `AuthStatus` endpoint (p2p.go:867-897).
- [../connections/03-controller-service.md](../connections/03-controller-service.md): Outbound (read → delegate). Controller passes `nodestate.GetOperator()` as `owner`/`requester` to `AnonService` (anon.go:51,97,141,163,239,317), service side does visibility gating and unauthorized access determination (anon_service.go:207-218,224-251,260-330).
- [../connections/04-service-repository.md](../connections/04-service-repository.md): Indirect outbound (values ultimately persisted to DB). `Owner` is written with anonymous collection JSON to content-addressed storage and registered in SQLite `file_meta` (anon_service.go:131,140-160) — persistence happens in the downstream chain of this connection, nodestate does not persist itself.

Related module documentation (same directory): [05-controller.md](05-controller.md) (reader: anonymous collection endpoints and `AuthStatus`, 05-controller.md:161 already records username/operator distinction), [06-service.md](06-service.md) (`AnonService` consumes and persists this value), [01-config.md](01-config.md) (`RegistrationServer`/`PEERDRIVE_REG_SERVER` is the only source for reg server address, config.go:34,195; app.go:181 only injects into router middleware, not nodestate).
