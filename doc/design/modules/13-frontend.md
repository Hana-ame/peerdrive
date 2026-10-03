# Module 13: frontend Web Consumer

- **Code location**: `front/src` (along with: `front/public/service-worker.js`, `front/tests/`, `front/package.json`, `front/vite.config.ts`)
- **One-line function**: React SPA consumer — manages this node via admin frames over local WS session `/ws/peer` (managing this node's files/collections/sharing/BT/IPFS/pull tasks/settings), and as a PeerJS/WebRTC pure consumer dials peer nodes by peer id via public signaling, browses their shared content and saves; config and page state stored in localStorage / browser memory, all business data delegated to connected node backend for persistence.
- **Dependencies**: `react@^19.2.5` / `react-dom` (`front/package.json:14-15`), `react-router-dom@^7.14.2` (routing, `front/package.json:16`), `peerjs@^1.5.5` (signaling dial, `front/package.json:13`); build uses vite (`front/vite.config.ts:5-8`), tests use vitest + happy-dom + @testing-library (`front/package.json:19-28`). `lib/pd-client/` subpackage has zero runtime dependencies — does not import peerjs, Peer constructor passed in by caller (`front/src/lib/pd-client/client.js:952-961`).
- **Depended upon by**: No backend code references this module (pure browser side, server doesn't import frontend); behavior tested by `front/tests/` (ws.test.js / api.test.js / api-mock-sync.test.js etc.), and described as browser-side implementation in connection documents 01 / 12.

## 1. Logic

**Module positioning: one SPA facing two backend planes**. After full frontend migration (2026-08-17), all backend communication from pages has only two entry points (`front/src/ws.js:1-7`, `front/src/api.js:1-7`):

| Plane | Channel | Purpose | Implementation |
|---|---|---|---|
| Admin plane (this node) | Local WS session `/ws/peer` admin frames | Manage this node: files / collections / sharing / BT / IPFS / pull tasks / auth etc. all admin operations | `ws.js` (admin/upload/download/stat) + `api.js` request() |
| Consumer plane (peer) | PeerJS DataChannel (public signaling) | Dial peer node by peer id: view sharing manifest (share frames), pull content (req frames), save | `lib/pd-client/` + `lib/PeerJSConnect.jsx` |

Admin plane **only goes through local WS**; PeerJS/WebRTC does not implement admin verbs (user decision to prevent permission plane vulnerability, `front/src/ws.js:30-31`; backend `back/internal/transport/admin.go:5-11` rejects non-local sessions via `c.ID()=="local"`).

**Page inventory** (`App.jsx` route registration, `front/src/App.jsx:59-69`; shell+routing redone 2026-09-25, other pages are usable simplified versions, planned per module ②③④⑤⑥ to be redone, see `front/src/App.jsx:1-2,15-25`):

| Route | Page | Description |
|---|---|---|
| `/` | Connect node search/connect (PeerJS consumer) | Homepage, only entry in navigation (NAV, `front/src/App.jsx:29-31`) |
| `/node` | NodeControl peer node control | Redirected after successful connection (`front/src/pages/Connect.jsx:18`) |
| `/drive` | Drive this node's drive | Implemented, hidden from navigation (URL direct access still works, `App.jsx:27-28`) |
| `/collections` | Collections anonymous collections | Same as above |
| `/settings` | Settings connection method/backend address/auth | Same as above |
| `/transfers` | Transfers cross-node pull tasks | Same as above |
| `/bt` | BT DHT / torrent download | Same as above |
| `/ipfs` | IPFS pin / gateway panel | Same as above |
| `*` | Placeholder "page not found" | `App.jsx:68` |

**Admin plane core flow**: `ws.js` maintains a **single** WS connection (single connection reuse, reqId concurrent routing, `front/src/ws.js:153-154`). `admin(method,path,body)` constructs `{"type":"admin",method,path,body,token,reqId}` and sends (`front/src/ws.js:339-350`); backend converts admin frame to internal `*http.Request` injected into gin engine, reuses all controllers (`back/internal/transport/admin.go:13-19`); response `admin-resp{status,body,reqId}` paired by reqId, status≥400 → reject with `err.status/err.data` (`front/src/ws.js:215-228`). Binary upload = admin declaration frame (`binary:true,filename,size,field`) + continuous binary chunks (64KB chunks, `front/src/ws.js:47,358-375`), backend collects to temp file then constructs multipart for forwarding (`back/internal/transport/admin.go:21-24,47-49`). File pulling goes through req verb: `{"type":"req",hash,offset,size,reqId}` → meta / data header+binary chunks / done / err (`front/src/ws.js:17-20`). Connection keepalive: 25s heartbeat `admin GET /ping` + 60s no-frame dead judgment + exponential backoff auto-reconnect (max 30s, `front/src/ws.js:67-150`).

UI layer does not directly touch frame details: `api.js`'s `request()` is just `ws.admin()` (`front/src/api.js:158-160`), all backend endpoints exposed as path strings (files / collections / anon / access / reg proxy / bt / ipfs / p2p pull / peerjs nodes, share etc.), old fetch semantics preserved (`front/src/api.js:2-7,155-157`).

**Consumer plane core flow**: `PeerJSConnect.jsx` assembles PeerJS options (local stable id always as `id`, `serialization:'raw'`, `reliable:true`, `front/src/lib/PeerJSConnect.jsx:58-88`) → `connectToPeer` creates Peer and `peer.connect(peerId)` (`front/src/lib/pd-client/client.js:987-1021`) → `PeerDriveClient` frame state machine: first frame after open presents PSK (optional, `client.js:156-164`) → `shares()` pulls sharing manifest (share frames → share-resp, `client.js:205-220`) → `stream()/fetch()/saveAs()` pull content via req frames (`client.js:354-445`). Data plane frame protocol is **character-by-character aligned** with Go-side `back/internal/transport/conn.go` (`front/src/lib/pd-client/protocol.js:1-24`). Online node discovery goes through signaling REST `GET /discover/nodes` (`client.js:907-935`).

**Lifecycle**:

- App mount: `App` useEffect calls `registerSW()` (`front/src/App.jsx:53`); SW first install not controlled triggers one refresh (`front/src/lib/swBridge.js:20-27`).
- WS...

## 2. How It Stores

**No persistent storage — browser memory + localStorage only**:

| Aspect | Details |
|--------|---------|
| Config | `localStorage` (backend address, username, theme, etc.) |
| Page state | React component state (in-memory, lost on page refresh) |
| WS connection | In-memory (single connection, auto-reconnect on disconnect) |
| Peer connections | In-memory (PeerJS DataChannel, per-peer) |
| Business data | None — delegated to connected node backend |
| Service Worker | `front/public/service-worker.js` (caching, not business data) |

**localStorage keys**:
- `peerdrive_backend`: Backend server address
- `peerdrive_username`: Username for auth
- `peerdrive_theme`: UI theme (light/dark)
- Other UI preferences

## 3. When It Stores

**Browser-side only, no backend persistence**:

| Trigger | Action | Storage |
|---------|--------|---------|
| User sets config | Write to `localStorage` | Browser localStorage |
| User changes theme | Write to `localStorage` | Browser localStorage |
| WS connection state | Maintain in-memory state machine | React component state |
| Page navigation | Update URL via react-router | Browser history |
| Login state | Store username in `localStorage` | Browser localStorage |
| File upload | Stream via admin frame to backend | Backend (not frontend) |
| File download | Stream from backend via req frames | Backend (not frontend) |
| Peer connection | PeerJS DataChannel state | In-memory |

**No persistent business data on frontend**. All business data (files, collections, shares, etc.) is stored on the connected node backend. The frontend is a pure consumer/client.

## 4. What It Stores

**Browser-local state only**:

- **Configuration**: Backend address, username, theme, language (localStorage)
- **UI state**: Active tab, expanded/collapsed sections, filter states (component state, in-memory)
- **Connection state**: WS connection status, peer connection list (in-memory)
- **Service Worker**: Cached assets for offline support (browser cache, not business data)

**Not stored**:
- File content (streamed through, not cached)
- Collection data (queried on demand)
- Share links (queried on demand)
- Sync state (managed by backend)

## 5. Boundaries and Pitfalls

- **No local business data**: The frontend does NOT store any business data locally. All data comes from the connected backend. This means offline mode only works for cached UI assets, not for data access.
- **Single WS connection**: All admin operations go through one WS connection. If this connection drops, all admin operations fail until reconnection.
- **Peer connections are ephemeral**: PeerJS DataChannel connections are not persisted. On page refresh, all peer connections must be re-established.
- **Frame protocol must be aligned**: The frontend's frame protocol (`protocol.js`) must match the Go backend's frame protocol exactly. Any protocol change requires updating both sides.
- **Admin plane is local-only**: Admin operations only work over local WS sessions. PeerJS/WebRTC connections cannot perform admin operations (security decision).
- **Binary upload is chunked**: Large file uploads are split into 64KB chunks and sent as continuous binary frames. This requires careful buffering and error handling.
- **PSK is optional for consumer**: When connecting to peer nodes, PSK authentication is optional. Without PSK, the connection has reduced security.
- **Service Worker caching**: The service worker caches assets for offline support but does NOT cache API responses or business data.

## 6. External Connections

- [frontend ↔ backend](../connections/01-frontend-backend.md): Admin plane communication — frontend sends admin frames via `/ws/peer` WS session, backend converts to internal HTTP requests via gin engine injection. This is the primary communication channel for all management operations.
- [frontend ↔ signalserver](../connections/12-frontend-signalserver.md): Consumer plane — frontend dials peer nodes via public signaling (PeerJS OFFER/ANSWER/CANDIDATE negotiation) + `GET /discover/nodes` online discovery (`front/src/lib/pd-client/client.js:907-1021`, `front/src/lib/PeerJSConnect.jsx:11-17`).
- [controller ↔ storage](../connections/10-controller-storage.md): Uploads (admin binary → `/files/upload`) and other write operations land in the actual storage layer.
- [service ↔ repository](../connections/04-service-repository.md): File/collection/share/permission/pull task metadata is actually persisted at the repository layer.
- [transport ↔ storage](../connections/11-transport-storage.md): Cross-node pull tasks are executed by backend and persisted to this node's storage / file_index (frontend only sends `/p2p/pull*` admin requests and polls task list, `front/src/api.js:364-370`, `front/src/pages/Transfers.jsx`).

> Note: The separation of admin plane and data plane is the core invariant of this module — local WS sessions carry admin (with auth token) and req data plane; PeerJS/WebRTC only carries peer share/req data plane, does not implement admin verbs (`front/src/ws.js:30-31`, `back/internal/transport/admin.go:5-11`).
