# Module 04: router HTTP Routing and Middleware

- **Code location**: `back/internal/router`
- **One-line function**: Assembles the Gin engine as the sole HTTP/WS entry point for the entire backend — registers all routes (health check, files, collections, P2P, PeerJS, source management), mounts cross-cutting middleware (request ID / security headers / access log / per-IP rate limiting / CORS / authentication), and reuses the same engine as the admin backend for browser `/ws/peer` admin frames.
- **Dependencies**: `config` (`back/internal/config/config.go` — reads `RateLimitRPS`/`TrustedProxies`/`RegistrationServer`/`DisableCSP`/`DisableSwagger`/`StorageDir`/`AllowedOrigins` etc.), `controller` (all handlers, `back/internal/router/router.go:225-383` per-route references), `service` (`SetupRouter` constructs `NewFileService`/`NewCollectionService`/`NewShareService`/`NewPinService`/`NewAnonService`/`NewSyncService`, router.go:121-128), `repository` (`Ping` probe, `NewSyncRepository`, router.go:124,211), `source` (`source.Manager`/`NewBTControl`/`NewIPFSControl`, router.go:153,192 and source_routes.go), `transport` (`PeerJSService`/`NewWSSession`, peerjs_routes.go), `downloader` (`NewUniversalDownloader`, router.go:201), `provider` (`NewIPFSProvider`, router.go:190), `p2p_bt` (`go-peerdrive-bt`, router.go:141,151), gin / swaggo / gorilla-websocket.
- **Depended upon by**: `back/internal/serverapp/app.go` (assembly, `app.go:170-339` assembles injectors then calls `router.SetupRouter(cfg)`, returns `*gin.Engine` as `http.Server.Handler`, app.go:370-379); `transport` admin plane (`back/internal/transport/admin.go:13-19` explains admin frame → internal `*http.Request` → injected `AdminHandler` → reuses all controllers from this engine); frontend (sends admin frames via `/ws/peer` local WS sessions, see `back/internal/transport/admin.go:26-27`; old HTTP endpoints retained for backward compatibility with old frontend/curl/integration tests, router.go:216-223); `collection_dispatch_test.go` etc. test dispatcher/routes directly via integration tests.

## 1. Logic

The module has only one entry function `SetupRouter(cfg *config.Config) *gin.Engine` (router.go:44), called once by main after all services are assembled (`app.go:339`). Responsibilities split into four layers:

**① Middleware stack assembly** (router.go:49-107). Does not use `gin.Default()` (its built-in Logger duplicates `AccessLog` output), explicit assembly, execution order is registration order (middleware.go:7-10 comments):

1. `gin.Recovery()` (outermost, must recover from panics) router.go:50;
2. `RequestID()`: propagates upstream `X-Request-ID` or generates one, truncates IDs >128, writes to context and response header (middleware.go:37-49);
3. `SecurityHeaders(cfg.DisableCSP)`: nosniff / X-Frame-Options / Referrer-Policy / COOP / CSP (`/swagger/*` exempt from CSP, middleware.go:75-87);
4. `AccessLog()`: one JSON access log line per request (middleware.go:108-148);
5. `RateLimit(cfg.RateLimitRPS, 0)`: per-IP token bucket rate limiting (middleware.go:203-240);
6. Two inline closures: inject `storageDir` into Gin context (router.go:76-79), CORS whitelist handling (router.go:81-107, before authentication);
7. `AuthOptional()` (only mounted when `RegistrationServer` is configured, router.go:112-115).

`TrustedProxies` handled separately during assembly (router.go:57-73): default trusts none (`ClientIP()` uses `RemoteAddr` directly), `all` allows `0.0.0.0/0`, comma-separated list calls `SetTrustedProxies` for each.

**② Controller dependency assembly** (router.go:121-213). `SetupRouter` internally constructs service instances and injects them into controllers (`controller.InitFileController`/`InitHealth`/`InitCollectionController`/`InitShareController`/`InitPinController`/`InitAnonController` etc., router.go:122-148); initializes BT DHT / BT client / IPFS provider / universal downloader / sync controller based on config. BT and IPFS "control handles" are injected into the source system via `sourceManager.SetBTControl/SetIPFSControl` (router.go:152-155,191-193).

**③ Route registration** (router.go:225-394). Groups per source file header comments (router.go:6-18) and this document's §4 route table. Auth middleware `authRequired := AuthRequired()` constructed once (router.go:119), reused for all mutating/admin routes (**F1 fix**: previously `AuthRequired` had 0 call sites, all file read/write/delete endpoints were anonymously accessible, router.go:116-118 comments). Read-only endpoints (list/status/download) do not mount authentication.

**④ PeerJS and source management plane** (router.go:394-418). `registerPeerJSRoutes(r, authRequired)` registers `/peerjs/*` node discovery and `/ws/peer` local WebSocket sessions (peerjs_routes.go:74-184); if `peerjsService != nil`, injects `SetAdminHandler` into transport — wraps external requests (sets `RemoteAddr` to `127.0.0.1:0` when empty, avoiding rate limit bucketing all admin requests into one "unknown source" bucket, router.go:403-414), passes to `httptest.NewRecorder() + r.ServeHTTP`, so admin frames reuse all HTTP controllers (router.go:396-401 comments: zero duplication, admin plane only exposed to local WS, `serveAdmin` rejects remote by session ID). Finally `registerSourceRoutes(r, authRequired)` registers `/sources*` management endpoints (source_routes.go:24-45).

**Lifecycle**: Assembled once per process, resident service, no hot updates (route registration only happens during `SetupRouter` call). Process exit/restart rebuilds everything (see §2).

## 2. How It Stores

The router layer **does not persist any data** (no file writes, no DB writes); its "storage" is all in-process memory state, business data delegated to downstream modules:

| Memory State | Medium/Location | Composition | Lifecycle and Restart Impact |
| --- | --- | --- | --- |
| `*gin.Engine` route table | Heap (engine + route nodes + handler closures) | All registered routes + middleware chain | Lost on process restart; fully rebuilt by `SetupRouter` |
| Rate limiter state | Per-IP token bucket map | `{ip: *bucket}` in `RateLimit` middleware | Lost on restart; per-IP counters reset to zero |
| `storageDir` context value | Gin request context (per-request) | Set by inline closure in middleware chain | Per-request, no persistence |
| CORS whitelist | In-memory `[]string` (from cfg) | `cfg.AllowedOrigins` | Read from config at startup; no runtime changes |
| `sourceManager` | Package-level var (injected by main) | `*source.Manager` with registered sources | Lost on restart; rebuilt during `SetupRouter` |
| `peerjsService` | Package-level var (injected by main) | `*transport.PeerJSService` | Lost on restart; rebuilt during main startup |
| Auth state (if configured) | Per-request context (from Authorization header) | `AuthRequired`/`AuthOptional` middleware | Per-request; stateless design |
| Swagger docs | Embedded (swaggo) + served at `/swagger/*` | Static spec from code generation | Compiled into binary; no runtime change |

## 3. When It Stores

**Never persists.** All state is ephemeral, rebuilt on each process startup:

| Timing | Action | Notes |
|--------|--------|-------|
| Process startup | `SetupRouter(cfg)` assembles engine, middleware, routes, dependencies | Called once from `app.go:339` |
| Each HTTP request | Middleware chain executes (request ID, security headers, rate limit, CORS, auth) | Stateless per request |
| Each request context | `storageDir` injected into Gin context | Per-request, not persisted |
| Process shutdown | Engine discarded with process | No explicit cleanup needed |

## 4. What It Stores

**Nothing persistent.** The router layer is a pure routing/middleware assembly. It holds references to:

- **Route definitions**: URL patterns → handler function mapping (in Gin engine's route tree)
- **Middleware chain**: Ordered list of middleware functions (recovery, request ID, security headers, access log, rate limit, CORS, auth)
- **Dependency references**: Pointers to service instances, source manager, peerjs service, downloader (all injected, not owned)
- **Runtime config values**: From `cfg` (allowed origins, rate limit RPS, trusted proxies, disable CSP, etc.) — read at assembly time

## 5. Boundaries and Pitfalls

- **Single entry point**: `SetupRouter` is the only function that creates the engine and registers routes. No other code should create Gin engines or register routes.
- **Package-level variable injection**: `sourceManager` and `peerjsService` are package-level vars set via `SetSourceManager`/`SetPeerJSService` before `SetupRouter` is called. If these are nil, certain routes (source management, peerjs discovery) will not be registered.
- **Admin frame reuse**: The gin engine is reused for admin frame handling via `SetAdminHandler`. Admin frames from `/ws/peer` are wrapped into internal `*http.Request` and processed by the same router. This means all rate limits, auth checks, and middleware apply to admin frames too.
- **Auth required by default for mutations**: After the F1 fix, all mutating endpoints (POST/PUT/DELETE) require authentication. Read-only endpoints (GET for list/status/download) do not. This is a deliberate security decision.
- **Rate limiter is per-IP**: The token bucket is keyed by client IP. Behind a reverse proxy without trusted proxy configuration, all requests appear to come from the proxy's IP.
- **CORS before auth**: CORS handling happens before authentication in the middleware chain. This means CORS preflight requests (OPTIONS) do not require authentication.
- **Swagger exemption from CSP**: `/swagger/*` routes are exempt from Content Security Policy. This is intentional for API documentation but means swagger is potentially vulnerable to XSS if docs are user-generated.

## 6. External Connections

- [../connections/02-router-controller.md](../connections/02-router-controller.md): Router registers all controller handlers and mounts middleware chain.
- [../connections/03-controller-service.md](../connections/03-controller-service.md): `SetupRouter` directly constructs service instances and injects them into controllers (router.go:121-128,211-213); router is one of the assembly entry points for services (the rest injected by main).
- [../connections/05-router-source.md](../connections/05-router-source.md): Source management endpoints (source_routes.go) all forward to `source.Manager` — status snapshots, priority adjustments, local/BT/IPFS control plane; `sourceManager` injected by main via `SetSourceManager` (source_routes.go:17-22).
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md): This module reads package-level injected `peerjsService` to register node discovery/pull/WS endpoints (peerjs_routes.go:74-184), and uses `SetAdminHandler` to inject gin engine back into transport's admin plane (router.go:402-415).
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md): Node ID exposed by `/peerjs/node` originates from PeerJS signaling layer (`peerjsService.ID()`, peerjs_routes.go:88-94); router only forwards/presents, does not participate in signaling.
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Frontend/MQTT rooms resolve node ID via this module's `/peerjs/node` discovery endpoint (peerjs_routes.go:69-70 comments).
