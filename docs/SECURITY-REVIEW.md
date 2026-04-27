# Peerdrive Security Review

Conducted: 2026-04-27
Scope: Go backend, React frontend, Registration server, Configuration files

---

## Critical

### C-1: No file upload size limiting

Filesystem fill / denial of service via unrestricted uploads.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/controller/file.go`, line 42
- **Detail**: `UploadFile` calls `c.Request.FormFile("file")` then feeds the stream directly to `io.Copy` in `FileService.Upload` (file_service.go, line 164). There is no `http.MaxBytesReader` wrapper, no Gin `MaxMultipartMemory` setting, and no size check at any point. An attacker can send an arbitrarily large file and fill the server's disk.
- **Fix**: Wrap `c.Request.Body` with `http.MaxBytesReader(c.Writer, c.Request.Body, MAX_UPLOAD_BYTES)` before reading the multipart form, or set `r.MaxMultipartMemory` on the Gin engine, or add a size check after receiving the file. A sensible limit is 100 MB for most deployments.

---

## High

### H-1: Path traversal -- BrowseDir allows reading arbitrary directories

Unauthenticated remote directory listing of any path on the server filesystem.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/service/file_service.go`, lines 261-274
- **Detail**: `BrowseDir` accepts a `path` query parameter. If the path is absolute (e.g. `/etc`, `/home`, `/root`), it is used as-is with zero validation. `filepath.IsAbs(path)` is checked only to decide whether to prepend `storageDir`. An attacker can enumerate any directory: `GET /files/browse?path=/etc` reveals filenames, sizes, and modification times. The route has no authentication.
- **Fix**: Reject absolute paths entirely. Resolve the path against `storageDir` using `filepath.Join(storageDir, path)`, then call `filepath.Clean` and verify the result still starts with `storageDir`. Never operate on arbitrary absolute paths.

### H-2: Path traversal -- RegisterLocal allows reading and hashing arbitrary files

Unauthenticated attacker can register any file on the filesystem by absolute path, then download its content via the resulting SHA256 hash.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/service/file_service.go`, lines 37-98
- **Detail**: `RegisterLocal` accepts a JSON body with a `path` field. If the path is absolute, the file is opened and hashed directly (line 52: `os.Open(absPath)`). The resulting SHA256 hash is inserted into `file_meta` and `file_providers` with the local provider path set to the arbitrary absolute path. The attacker can then download the file via `GET /sha256sum/<hash>` since `LocalProvider.GetReader` opens the stored path (provider/local.go, line 22). This means any readable file on the server can be exfiltrated.
- **Fix**: Reject absolute paths in `RegisterLocal`. Only allow relative paths that resolve within `storageDir`. Apply the same `filepath.Clean` + prefix check described in H-1.

### H-3: No authentication on any route group

Every API endpoint is publicly accessible. The auth middleware exists but is never wired in.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/router/router.go`, lines 181-319
- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/router/auth_middleware.go`, lines 20-61
- **Detail**: `AuthRequired()` and `AuthOptional()` are defined in `auth_middleware.go` but never referenced in `SetupRouter`. Every route group (`/files/`, `/anon/`, `/collections/`, `/p2p/`, `/actions/`, `/tasks/`, `/shares/`, `/local/`) is wide open. Anybody who can reach the port can upload files, delete files, create/modify collections, trigger P2P operations, create shares, and browse the directory tree.
- **Fix**: Apply `AuthRequired()` middleware to mutation endpoints (`POST/PUT/DELETE` on `/files`, `/collections`, `/actions`, `/shares`). Apply `AuthOptional()` to read endpoints where anonymous read may be desired (e.g. public collections). At minimum, add auth to `DELETE /files/:hash`, `POST /files/upload`, `POST /files/register_local`, `POST /files/register_folder`, and all `POST/DELETE` operations on collections.

---

## Medium

### M-1: JWT_SECRET falls back to "change-me-in-production"

Hardcoded weak JWT signing key in the registration server.

- **File**: `/mnt/d/WorkPlace/peerdrive/registration-server/internal/service/auth.go`, lines 33-36
- **File**: `/mnt/d/WorkPlace/peerdrive/registration-server/.env.example`, line 3
- **Detail**: When `JWT_SECRET` environment variable is unset, the code falls back to the literal string `"change-me-in-production"`. This is trivially guessable, so anyone can forge valid JWT tokens and authenticate as any user, including `admin`. The `.env.example` file also documents this default.
- **Fix**: Either fail at startup if `JWT_SECRET` is not set (or is the default), or generate a random key on first run. A minimum of 256 bits (32 bytes) of entropy from a cryptographically secure source should be required.

### M-2: DHT runs in ModeServer with no access control

Anyone on the IPFS public DHT can discover or announce files from this node.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/service/p2p.go`, line 123
- **Detail**: The Kademlia DHT is created with `dht.Mode(dht.ModeServer)` which makes this node a full DHT server on the public IPFS network. Combined with the unauthenticated `/p2p/announce`, `/p2p/bt/announce`, and `/p2p/dual/announce` endpoints (router.go lines 199, 208, 218), any remote caller can announce hashes to the public DHT. The stream handlers (`handleExchange`, `handleAnnounce`, `handleRequest` at lines 494-605) accept requests from any peer and will serve files if they exist locally.
- **Fix**: The DHT exposure itself is expected for a P2P file-sharing app, but the node should not be a `ModeServer` unless explicitly configured. Consider `dht.Mode(dht.ModeClient)` by default. Add an opt-in `PEERDRIVE_DHT_SERVER_MODE` env var. Additionally, rate-limit the stream handlers and consider requiring a capability token for announce operations.

### M-3: No rate limiting on any endpoint

The server has no protection against abuse.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/router/router.go` (entire file)
- **Detail**: None of the 50+ HTTP routes have rate limiting. An attacker can:
  - Flood `/files/upload` to fill disk
  - Repeatedly hit `/files/browse?path=/proc` for information leakage
  - Hammer `/p2p/connect` to exhaust connection pools
  - Use `/p2p/announce` to spam the DHT
- **Fix**: Integrate a rate-limiting middleware (e.g. `github.com/ulule/limiter/v3` with an in-memory store) on mutation and resource-intensive endpoints. A reasonable baseline: 30 req/min for uploads, 60 req/min for P2P operations, 120 req/min for reads.

### M-4: Share creation is unauthenticated

Anyone can create share links for any file hash without authentication.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/controller/share.go`, lines 12-41
- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/repository/share_repo.go`
- **Detail**: The `POST /shares` endpoint has no auth, allowing any unauthenticated user to create share links for any file hash. Note: share token generation correctly uses `crypto/rand` for 128-bit tokens (share_repo.go lines 12-16) and defaults to a 30-day expiry (line 19), so tokens themselves are not predictable. The primary issue is the lack of auth on the creation endpoint.
- **Fix**: Require authentication for share creation. Consider making the 30-day default expiry configurable.

---

## Low

### L-1: SiliconFlow API key stored in localStorage

The LLM API key is accessible to any JavaScript on the same origin.

- **File**: `/mnt/d/WorkPlace/peerdrive/react/src/api.js`, lines 171-172
- **Detail**: The functions `getLlmApiKey()` and `setLlmApiKey()` read/write the key to `localStorage` under the key `peerdrive_llm_apikey`. While localStorage is scoped to the origin, any XSS vulnerability (even a third-party script loaded on the page) could steal this key. This is a common pattern in browser-based apps, but it exposes the key to every browser extension and script on the page.
- **Fix**: Consider using an HTTP-only cookie served by the backend that proxies LLM requests, so the key never reaches the browser. Alternatively, prompt the user each session rather than persisting it.

### L-2: SQL injection potential in ORDER BY clause

The `ListAllFiles` function concatenates user-provided sort columns into SQL.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/repository/file_repo.go`, lines 72-94
- **Detail**: The `sortBy` query parameter is mapped through a whitelist (line 73-84), so direct injection is prevented. However, the whitelist-mapped column name is concatenated directly into the SQL string (line 94: `ORDER BY ` + orderCol + ` DESC`). This is not currently exploitable but is fragile -- if the whitelist is ever extended with unvalidated input, injection becomes possible.
- **Fix**: Use parameterized sorts with verified column names, or use a numeric enum for sort options instead of mapping strings to column names.

### L-3: libp2p security transport not explicitly set

The Node relies on default Noise encryption without explicit configuration.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/service/p2p.go`, lines 85-117
- **Detail**: The `libp2p.New()` call passes options for addresses, relay, NAT, and hole-punching, but does not include `libp2p.Security` to explicitly enable Noise or TLS. go-libp2p v0.48.0 defaults to Noise with the identity private key, which is secure, but this is an implicit dependency. A future library upgrade could change the default.
- **Fix**: Add `libp2p.Security(libp2p.Noise, identity)` explicitly to guarantee the security transport. This also makes the security posture clear to reviewers.

### L-4: CORS wildcard risk

The `IsOriginAllowed` function supports `*` as a valid origin.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/config/config.go`, lines 59-75
- **Detail**: The default `PEERDRIVE_ALLOWED_ORIGINS` is a specific list of origins (good). However, `IsOriginAllowed` returns `true` if `AllowedOrigins` is `"*"` or `""`. If an administrator sets `PEERDRIVE_ALLOWED_ORIGINS=*`, all origins will be permitted, and with `Access-Control-Allow-Credentials: true` set at router.go line 71, this would allow credential-bearing cross-origin reads.
- **Fix**: If `Access-Control-Allow-Credentials` is `true`, the spec requires `Access-Control-Allow-Origin` to not be `*`. Add a validation check that rejects `*` when credentials are enabled, or remove the wildcard support.

### L-5: Cache directory writes for downloaded data occur before hash verification

Minor race condition in P2P download.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/service/p2p.go`, lines 459-492
- **Detail**: In `requestData`, the data is received and returned from a peer. The caller in `SyncFiles` (line 433) writes the data to disk, then verifies the hash (line 427-428). This means invalid data is written to disk before it is validated. While the hash check will log an error, the corrupt data remains on disk.
- **Fix**: Verify the hash before writing to disk. Write to a temporary file first, then atomically rename on successful verification.

### L-6: No Content-Security-Policy or security headers

The backend does not set any security-related HTTP headers.

- **File**: `/mnt/d/WorkPlace/peerdrive/go/internal/router/router.go`, lines 59-78
- **Detail**: The CORS middleware sets CORS headers but does not set `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, or `Content-Security-Policy` headers. While the frontend is a separate SPA, direct access to the API from a browser could be vulnerable to content-type sniffing.
- **Fix**: Add a middleware that sets standard security headers on all responses.

---

## Summary

| Severity | Count | Key Issues |
|----------|-------|------------|
| Critical | 1 | No upload size limit |
| High     | 3 | Path traversal (BrowseDir, RegisterLocal), no auth on any endpoint |
| Medium   | 4 | Weak JWT secret, DHT ModeServer, no rate limiting, predictable shares |
| Low      | 6 | API key in localStorage, SQL concat, implicit Noise, CORS wildcard, write-before-verify, missing security headers |

**Most important fixes** (in priority order):

1. Restrict `RegisterLocal` and `BrowseDir` to paths within `storageDir` only -- these are actively exploitable path traversal vulnerabilities.
2. Add authentication middleware to all mutation endpoints.
3. Add a file upload size limit via `http.MaxBytesReader`.
4. Require a strong `JWT_SECRET` environment variable in the registration server.
5. Add rate limiting on upload and P2P endpoints.
