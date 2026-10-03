# API wrapper layer (front/src/api.js)

> Layer membership: AOP ⑧ Frontend aspect (doc/LAYERS.md §1). The frontend's single
> business API facade: wraps all backend HTTP semantic endpoints as named functions;
> page components only `import * as api` and call functions, unaware of transport details.
> Since 2026-08-17 all requests go through the local WS session admin frames (ws.js);
> HTTP endpoints are kept as legacy.

## Responsibilities

- **Unified request channel**: `request(method, path, body)` (api.js:148) = `ws.admin(...)`;
  all management endpoints (files/collections/auth/BT/IPFS/tasks/sync/access lists/reg
  proxies) are issued through this; error semantics are consistent with the old fetch
  version (`Error.status`/`Error.data` structured body).
- **Binary API set**: upload (`uploadFile`/`btTorrentUpload`), download
  (`downloadFile`/`downloadFileToDisk`/`downloadAnonFile`/`downloadUserFile`/
  `downloadTorrentFile`), preview (`getBlobUrl`/`revokeBlobUrl`, with caching).
- **Config management (localStorage)**: multi-backend list (api.js:14-111), API base/token
  (api.js:113-143), LLM params (api.js:313-339), auth toggle, P2P network params
  (bootstrap/relay/STUN/TURN, api.js:351-367), IPFS toggle, registration server URL,
  data consent flag.
- **Direct third-party**: the registration server (regserver)'s group management/comments
  interfaces don't go through WS; they directly `fetch(regServerUrl)` (api.js:473-506) ——
  they are not on the local node.

**Why it exists**: page components need to interact with all backend controllers (17 endpoint
groups, see doc/layers/L4-core/controllers.md). Before migration, direct HTTP URL fetch;
after migration the same declaration and same error semantics go through WS admin frames
(REFACTOR.md §3.10), with zero caller changes.

## Key mechanisms

### request() and error semantics (api.js:148)

```js
async function request(method, path, body = null) {
  return ws.admin(method, path, body);
}
```

- `status>=400` → reject `Error`, attach `err.status`/`err.data` (structured error bodies
  such as the 409 conflict list, see the ws.js handleText admin-resp branch). **This is
  the prerequisite for pages to consume error details in popups** (e.g. Explorer merge
  conflict `e.data.conflicts`, `e.status === 409` check).
- Callers are unaware of the transport migration: signatures match the old fetch version
  exactly.

### token system (api.js:136)

`getAuthToken()`: `peerdrive_auth_token` (URL fragment `#token` import) always takes
priority; otherwise when the settings toggle `peerdrive_auth_header_enabled==='true'`
use `peerdrive_auth_key`; otherwise empty string. Same semantics as ws.js `readToken()`
(synchronous localStorage).

`setApiBase(url)` (api.js:121): when the user inputs `http://host:3000#token123`, split
out the fragment to store the token and store a clean URL as base —— **token never
mixes into the API base**.

### Multi-backend management (api.js:14-111)

- `DEFAULT_BACKENDS`: `wsl` (https://wsl-3000.moonchan.xyz) + `bwh`
  (http://97.64.30.221:3000), persisted on first read.
- `switchBackend(id)`: writes `peerdrive_api_base` + syncs STUN/TURN/credentials to global
  localStorage (`peerdrive_stun_url` etc., api.js:54-56).
- `addBackend`/`removeBackend`/`updateBackend`: runtime add/remove/edit (default backends
  cannot be deleted; deleting the current backend auto-switches back to the first available).

### Binary upload/download API

| Function | Uses ws.js's | Backend endpoint semantics |
|---|---|---|
| `uploadFile(file)` (api.js:316) | `upload` (field `file`) | `POST /files/upload` multipart |
| `btTorrentUpload(file)` (api.js:284) | `upload` (field `torrent`, path `/bt/torrent`) | `POST /bt/torrent` multipart |
| `downloadFile(hash)` (api.js:157) | `download` (req verb) | sha256 content-addressed fetch |
| `downloadFileToDisk(hash, filename)` | `downloadToFile` | Same as above + `<a download>` to disk |
| `downloadAnonFile(hash, p)` (api.js:224) | `admin GET` (admin-bin response) | Collection file stream |
| `downloadUserFile(username, coll, filepath)` (api.js:250) | `admin GET` | User collection file stream |
| `downloadTorrentFile(infohash)` (api.js:296) | `admin GET` | `.torrent` file stream |

- **encodePath** (api.js:221): for download virtual paths, `encodeURIComponent` each
  segment (filenames may contain spaces/`#`/`?` etc.; without encoding the URL breaks;
  backend gin `*filepath` already decodes URL.Path, so encoded comparison remains correct).
- **getBlobUrl** (api.js:168): pulls via WS → Blob → objectURL, `blobUrlCache`
  Map caches by hash; `revokeBlobUrl` is cleaned up by the caller on page unload. **LRU
  limit 50** (2026-08-18 review fix): when the cache only grows, each unique hash preview
  occupies one Blob + objectURL, accumulating during long browsing sessions; on overflow
  evict the least-recently-used entry and revoke (Map iteration order = insertion order,
  reread does delete+set to refresh position).
- Collection download virtual paths containing a 64-bit hash first call
  `encodeURIComponent(hash)` before path concatenation (api.js:225).

### Data shape compatibility layer

- `registerURL` (api.js:197): backend `RegisterURL` returns `{hash,size,mime,filename}`
  (back file.go:120), `RegisterLocalFile` only returns `{hash,filename}` —— unify by
  filling in `mime_type`/`size` for old callers' compatibility.
- `createAnonCollection` (api.js:211): normalize entries to
  `{path, providers:[{type:'sha256'|'url', value, mime_type}]}`.
- `connectPeer` (api.js:261): backend `POST /p2p/connect` binds `{addr}` requiring a full
  multiaddr (including `/p2p/<peer_id>`) —— auto-appends the `/p2p/` suffix.

### Direct regserver exceptions (api.js:473-506)

`getUserGroups`/`addUserToGroup`/`getComments`/`postComment` directly
`fetch(regServerUrl, ...)` with `Authorization: Bearer` —— comments/group management are
hosted by the registration server, local node doesn't forward. This is the only non-WS
outbound path in api.js (LLMAssistant components also directly fetch LLM endpoints, not
going through api.js's request).

## Relationships with other modules

```
Page components (pages/*) + global components (Navbar/LLMAssistant)
   │ import * as api
   ▼
api.js ──request()──▶ ws.js admin() ──WS──▶ back/internal/transport/admin.go
api.js ──download──▶ ws.js download() ──WS──▶ ws_session.go req verb
api.js ──direct fetch──▶ regServerUrl (group mgmt/comments, exception)
```

- **Down (ws.js)**: request/upload/download all delegate to ws.js; token reading logic is
  synced with it (there is a copy in each place; changing one requires changing the other
  —— api.js comments explicitly say "synced with ws.js").
- **Up (pages)**: all pages `import * as api`; `__mocks__/api.js` provides same-signature
  empty implementations for testing (see "Tests").
- **Peer (backend)**: HTTP routes are kept as legacy (router.go LEGACY comment section),
  only for old clients/curl/integration tests —— the frontend is forbidden from directly
  fetching local HTTP endpoints (LAYERS.md §5). Authoritative reference for endpoint
  semantics: doc/api-reference.md (with the 2026-08-17 migration notes).
- **Frontend dependency facts**: `front/package.json` only has react 19 / react-dom /
  react-router-dom 7 and build/test dependencies (vite 8 / vitest 4 / happy-dom /
  testing-library), **no peerjs/mqtt** —— the interconnect stack only exists in the
  backend; vite.config.ts also has no optimizeDeps configuration.

## Caveats and design decisions

1. **Decision to fully migrate to WS**: On 2026-08-17 the frontend was fully migrated;
   HTTP endpoints kept but marked legacy. The management plane only goes through local WS
   (WebRTC doesn't implement management verbs, prevents privilege boundary leaks). After
   migration "HTTP URL direct links" (`getDownloadUrl`/`getAnonFileDownloadUrl`/
   `getUserFileDownloadUrl`/`btGetTorrentUrl`) are all deprecated, preview/download
   switched to Blob mode (api.js comments mark each spot).
2. **connectPeer field pitfall**: the old implementation sent `{peer_id, addrs}` which
   didn't match backend `{addr}`, empty `req.Addr` caused a 500 on every connection ——
   now auto-completes a full multiaddr (api.js:259-260).
3. **getBlobUrl caching**: same hash only pulled once; objectURL lifecycle is revoked by
   the caller, otherwise memory leaks (api.js:157).
4. **saveConsentLocal misleading name**: old name `uploadConsent` made people think it
   uploaded to the server, but backend has no `/consent` endpoint —— only local
   recording (api.js:431-436).
5. **Token dual-source priority**: URL fragment token is always effective, legacy
   settings token requires the toggle; when both coexist the fragment wins
   (api.js:136-143).
6. **registerURL field mismatch**: `mime` vs `mime_type` inconsistent in backend returns;
   unified normalization (api.js:197-202), pages only recognize `mime_type`.
7. **Direct third-party coexists with WS**: regserver interfaces not going through WS is
   intentional (they're not on the local node); when adding new "third-party service"
   interfaces, follow this pattern; don't force them into request().

## Tests

No independent unit test file (api.js is a pure thin wrapper, logic is tested in ws.js).
Test side provides:

- **`front/src/__mocks__/api.js`**: same-signature empty implementations for all functions
  (`request()` returns `{}`, list functions return `[]`), preventing real HTTP requests in
  the happy-dom environment from generating AbortError/socket hang up noise during window
  teardown (file header comment explains the reason).
- **`front/tests/setup.js`**: `vi.mock('../src/api.js')` mounts the mock globally; component
  tests (smoke.test.jsx / components.test.jsx / FileTree.test.jsx) auto-apply it.
- **Manual verification**: any page function in the browser + DevTools Network to confirm
  no XHR/fetch to `:3000` (except /ws/peer upgrade and direct regserver/LLM).

## File inventory

- `front/src/api.js` (514 lines) —— the main subject of this doc
- `front/src/__mocks__/api.js` —— test mock (same-signature empty implementations)
- `front/tests/setup.js` —— global mount of `vi.mock('../src/api.js')`
- Related peers (not this module): `front/src/ws.js` (transport layer), `doc/api-reference.md` (endpoint semantics)
