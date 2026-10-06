# Full-Stack Health Check Report (2026-09-23)

> Against "what a full-stack application should look like" (configuration, error handling, logging, data layer, authentication, frontend-backend integration, real-time, production hardening)
> A health check of `back/` (Go + Gin + SQLite) and `front/` (React + Vite).
> Conclusion first: **the layering and P2P main link are solid, what's missing is the "production operations" layer** —
> health probes, graceful shutdown, rate limiting, observability, connection pooling, reconnection recovery — none of these six exist.
> 12 items were fixed in this pass (see §1), with 7 more recommended for follow-up (see §2).

## 0. One-Line Summary per Dimension

| Dimension | Current State | Rating |
|------|------|------|
| Layering (controller→service→repository) | Controller doesn't directly touch repository; service layer doesn't recognize HTTP types | **Good**, this is what many projects can't achieve |
| Centralized configuration management | All `PEERDRIVE_*` + `Load()`, but zero validation at startup, DB path hardcoded | Structure is right, missing validation |
| Error handling | 153 hand-written `c.JSON(400/500, gin.H{"error":...})` | No typed errors, no unified mapping |
| Logging | Custom leveled text logging (`internal/log`) | No request ID, unstructured, not aggregatable |
| Data layer | SQLite, CREATE TABLE + ALTER idempotent migrations | No connection pool, no busy_timeout, **foreign keys off by default** |
| Authentication | Bearer + external registration server whoami; if not configured, pass through | One remote call per request; role is fetched but not used for authorization |
| Frontend-backend integration | Frontend all goes through local WS admin frames (admin plane not exposed) | Good design; but addresses hardcoded, no retry/offline state |
| Real-time | WS single connection + reqId routing | No heartbeat, no reconnection, no dead connection detection |
| Production hardening | CORS whitelist echo (correct), PSK admission, path boundaries | No health probes, no rate limiting, no security headers, no graceful shutdown |

## 1. Fixed in This Pass (12 items, all verified)

| # | Problem | Severity | Location Changed |
|---|------|--------|----------|
| 1 | **No graceful shutdown**: `r.Run()` calls `Fatalf` in a goroutine; any error causes `os.Exit`, skipping all `defer` in main (PeerJS connections, DB handles all rely on process exit as safety net); on SIGTERM, it just exits, truncating files being transferred mid-stream | High | `internal/serverapp/app.go` (changed to `http.Server` + `Shutdown(20s)` + forced close on timeout) |
| 2 | **No liveness/readiness probes**: only `/ping`, two probe semantics mixed | Medium | `internal/controller/health.go` (`/health` doesn't check deps, `/ready` actually pings DB) |
| 3 | **No request ID**: dozens of concurrent requests interleave in logs, can only guess by timestamp | Medium | `internal/router/middleware.go` (`X-Request-ID` reuses upstream/newly generated, written to context and response header) |
| 4 | **Unstructured logging**: text lines can't go into a logging system | Medium | Same as above (`AccessLog()` outputs JSON lines; 5xx=error, 4xx=warn; **does not log Authorization/request body**) |
| 5 | **No security response headers at all** | Medium | Same as above (nosniff / SAMEORIGIN / no-referrer / COOP / CSP, `/swagger` exempt, `PEERDRIVE_CSP=off` disables) |
| 6 | **No rate limiting**: upload, cross-node pull — these real bandwidth-consuming endpoints can be hammered by anyone | Medium | Same as above (per-IP token bucket, default 30 rps, `PEERDRIVE_RATE_LIMIT_RPS` adjustable, 0=off) |
| 7 | **gin defaults to trusting all proxies**: `ClientIP()` takes spoofable `X-Forwarded-For` directly, making rate limiting and log IPs meaningless | Medium | `router.go` + `config.TrustedProxies` (default trusts none, only recognizes `RemoteAddr`) |
| 8 | **SQLite no connection pool, no busy_timeout**: concurrent writes (upload registration / BT completion callback / file_index cursor sync happening simultaneously) occasionally trigger `database is locked`, only appears under concurrency, never reproducible single-run | High | `repository/db.go` + two `db_driver_*.go` (MaxOpenConns 8, busy_timeout 5s, memory DB forces single connection) |
| 9 | **Foreign keys off by default**: schema has `ON DELETE CASCADE` but it doesn't take effect, deleting collections leaves orphan entries | High | Same as above (`foreign_keys=1`, each driver writes its own DSN syntax) |
| 10 | **DB path hardcoded** `./peerdrive.db`, changing deployment directory requires "remembering to cd correctly first" | Low | `config.DBPath` (`PEERDRIVE_DB_PATH`) |
| 11 | **Zero config validation at startup**: errors like `PORT=300o`, empty `PEERDRIVE_STORAGE` don't report errors, only cause misbehavior (files land in working directory) | Medium | `config.Validate()` (port/path/max nodes) |
| 12 | **Auth does one remote whoami per request**: an admin console list page with dozens of requests pays a dozen+ network round-trips; one registration server hiccup causes site-wide 401 (token is clearly valid) | High | `router/auth_middleware.go` (30s TTL cache, only caches successful results; cost in comments: max 30s revocation delay) |

Frontend:

| # | Problem | Severity | Location Changed |
|---|------|--------|----------|
| 13 | **WS no heartbeat / no reconnection / no dead connection detection**: `onclose` just sets sock to null, waiting for the next request to reconnect — one node restart with no in-flight requests means reconnection is never triggered, the entire page goes "clicks don't respond" | High | `front/src/ws.js` (25s heartbeat `/ping`, 60s no-frame dead connection detection, 1s→30s exponential backoff reconnection, `onStatus()` subscription) |
| 14 | **Backend address hardcoded**: source code hardcodes a domain and a **plaintext http public IP** (blocked as mixed content on https pages, that default backend was never actually usable) | Medium | `front/src/api.js` (changed to `VITE_API_BASE`, removed plaintext http entries) |

> 13 and 14 combined count as 12 items in the table above; listed separately because they belong to the frontend.

Second round deep-dive fixed 3 more items (all on the "who can touch the admin plane" boundary):

| # | Problem | Severity | Location Changed |
|---|------|--------|----------|
| 15 | **`/ws/peer` allows all connections without Origin**: browser handshakes always carry Origin, so this rule effectively only applies to scripts/curl — meaning **anyone who can reach this port** (LAN machines, port-mapped public networks) can get the full admin plane without Origin; and `WSSession.IsLocal()` is always `true`, it's also treated as "self", making private shared content visible | **High** | `router/peerjs_routes.go` (no Origin only allows loopback `RemoteAddr`; browser requests behind a reverse proxy carry Origin, go through the whitelist path, unaffected) |
| 16 | **Swagger public and can't be disabled**: `/swagger/*any` has no auth, effectively publishing 105 endpoints and parameter structures as a map | Medium | `PEERDRIVE_SWAGGER=off` (default still on, to avoid breaking existing usage) |
| 17 | **Listen address not configurable**: default `0.0.0.0`, anyone on the same LAN can connect and act as admin (admin plane has no account system, port reachability is its only boundary) | Medium | `PEERDRIVE_HOST` (default empty = keep historical behavior; set to `127.0.0.1` when only using admin console locally) |

### Intentionally Preserved Behaviors

- **Loopback and OPTIONS preflight not rate-limited**: admin plane only serves local WS (`serveAdmin` rejects remote by session ID), internal forwarding requests uniformly marked as `127.0.0.1`; OPTIONS getting 429 would turn into a confusing cross-origin error due to missing CORS headers.
- **`/health` doesn't check dependencies**: liveness checking deps would turn "a DB hiccup" into "a restart storm".
- **Layering boundaries**: health checks don't import repository, changed to assembly-layer injection of `func() error` (`controller.InitHealth(repository.Ping)`).

## 2. Not Yet Fixed (by Suggested Priority)

1. **Token stored in localStorage** (`front/src/api.js`): XSS gets full access, and there's no 401 refresh/retry flow. Recommend migrating to httpOnly Cookie (requires backend `Set-Cookie` config); at minimum, change to sessionStorage + short expiry.
2. **No RBAC**: `role` is fetched from whoami but only used for **display** at `controller/p2p.go:871`, with no authorization decision based on role anywhere — all authenticated users have equivalent permissions. Recommend adding `RequireRole(...)` alongside `AuthRequired`, covering delete and shared scope changes first.
3. **No migration version table**: `InitDB` relies on `CREATE TABLE IF NOT EXISTS` + a series of `migrationExec(ALTER ...)`, with no record of whether migrations have run or what version is current, and no rollback support. Recommend adding `schema_migrations(version, applied_at)`.
4. **No typed error system**: 153 hand-written status codes, error messages and HTTP statuses scattered everywhere, clients can't get stable error codes. Recommend defining `AppError{Code, HTTP, Msg}` in `internal/errors.go` + a global handler, **and keeping `err.Error()` out of responses** (internal paths/DSN shouldn't appear in response bodies).
5. **No frontend retry and offline notifications**: 4xx shouldn't retry, 5xx retry up to 3 times, fetch failures need offline notifications — none of these exist. `ws.js` already provides `onStatus()`, UI side not wired up yet.
6. **Multi-step writes not in transactions**: only 2 `DB.Begin()` calls in the entire codebase. A collection commit (write version + write entries + update `current_hash`) interrupted leaves a half-state.
7. **Missing `.env.example`**: config relies entirely on environment variables but there's no template file, newcomers must dig through source code. Added `back/.env.example`.

Second round deep-dive additions (not fixed):

8. **Default CORS whitelist includes `https://*.pages.dev`** (`config.go`): Cloudflare Pages subdomains are **freely registrable by anyone**, so with default config, a malicious page hosted on any `*.pages.dev` subdomain can cross-origin connect to the local node's admin plane (even after fix 15, this browser path remains open). Must explicitly set `PEERDRIVE_ALLOWED_ORIGINS` when deploying to public internet or shared use; don't use the default.
9. **WS upload limit decoupled from config**: HTTP upload uses `http.MaxBytesReader(MaxUploadBytes)`, but WS binary upload uses hardcoded constant `adminBinMax` — changing `PEERDRIVE_MAX_UPLOAD_BYTES` has no effect on admin console upload, the two paths have inconsistent limits.
10. **Collection commit not in transaction**: write version + write entries + update `current_hash` are three independent writes; mid-way failure leaves a "version exists, entries incomplete" half-state.
11. **No frontend ErrorBoundary**: any component rendering error causes full page white screen, no fallback UI.

## 3. Verification Records (This Pass's Changes)

| Item | Command | Result |
|------|------|------|
| Backend build | `go build -tags nosqlite ./...` (WSL) | Passed |
| Backend vet | `go vet -tags nosqlite ./internal/... ./cmd/...` | No warnings |
| Backend unit tests | `go test -tags nosqlite ./...` | 12 packages all ok |
| Backend integration | `go test -tags "nosqlite integration" ./test/integration/ -p 1` | ok (57.9s) |
| Startup smoke test | Real instance + curl | `/health` 200, `/ready` `{"status":"ready"}` |
| Security headers / request ID | `curl -D -` | CSP, nosniff, SAMEORIGIN, no-referrer, COOP, `X-Request-Id` all present |
| Rate limiting | 12 rapid requests with spoofed XFF (rps=3, burst=6) | `200×6 → 429×6`; loopback 8 requests all 200 |
| Structured logging | Log sampling | `{"ts":...,"level":"info","msg":"http request","request_id":...,"latency_ms":0.054,...}` |
| Graceful shutdown | `kill -TERM` | Exits within 2s, log `main: stopped`, exit code 0 |
| WS handshake boundaries | 4 groups of curl simulated handshakes | Loopback no Origin `101`, non-loopback no Origin `403`, non-loopback whitelisted Origin `101`, unknown Origin `403` |
| Swagger toggle | `PEERDRIVE_SWAGGER=off` | `/swagger/index.html` 404, `/health` still 200 |
| Frontend unit tests | `vitest run` | 9 files / 101 cases all passed (including ws 14) |
| Frontend build | `vite build` | Passed (490KB / gzip 137KB) |

## 4. New Configuration Items

| Env Variable | Default | Description |
|----------|------|------|
| `PEERDRIVE_DB_PATH` | `./peerdrive.db` | SQLite metadata database path |
| `PEERDRIVE_RATE_LIMIT_RPS` | `30` | Per-IP rate limit, 0=unlimited |
| `PEERDRIVE_CSP` | (on) | Set to `off` to disable CSP |
| `PEERDRIVE_TRUSTED_PROXIES` | empty | Trusted reverse proxies (IP/CIDR comma-separated, or `all`); empty = only trust RemoteAddr |
| `PEERDRIVE_SWAGGER` | on | Set to `off` to disable `/swagger/*` (recommended off for public deployment) |
| `PEERDRIVE_HOST` | empty | Listen address; empty = all network interfaces. Set to `127.0.0.1` when only using admin console locally |
| `VITE_API_BASE` (frontend build-time) | Fallback domain | Default backend address |

> `PEERDRIVE_ALLOWED_ORIGINS` default includes `https://*.pages.dev`; anyone can freely register on this subdomain —
> please explicitly list your own origins before deploying to public internet, see §2 item 8.
