# PeerDrive Backend API Reference

> ⚠️ **As of 2026-08-17 the frontend has fully migrated to admin frames on `/ws/peer`** (see REFACTOR §3.10, NODE-API §2.4): HTTP routes are retained but marked legacy (router.go LEGACY section), used only for compatibility with old frontends/curl/integration tests. The tables below remain the authoritative reference for route and request/response structure (admin frames internally forward to the same controller, so behavior is identical).
>
> Intended for frontend developers. Organized per the frontend WS client `front/src/ws.js` (`admin`/`upload`/`download`/`stat`). The API Base is configurable, defaulting to `https://wsl-3000.moonchan.xyz`. (This doc was previously organized per an HTTP wrapper module in `front/src/` that had zero importers and was deleted on 2026-10-07; nothing reads it anymore.)
> Auth: `Authorization: Bearer <token>`, with the token sourced from the URL fragment (`#token`) or manual entry on the settings page.

---

## 1. File Management

| Endpoint | Method | Description |
|------|------|------|
| `/files?sort=time` | GET | List all registered files |
| `/files/register_local` | POST | Register a local file `{ path, filename }` |
| `/files/register_url` | POST | Register a URL download `{ url, filename }` |
| `/files/register_folder` | POST | Register an entire folder `{ folder_path }` |
| `/files/upload` | POST | Upload a file (multipart, field: `file`) |
| `/files/browse?path=` | GET | Browse filesystem directories |
| `/files/verify/:hash` | GET | Verify whether a file exists |
| `/files/:hash` | DELETE | Delete a registered file |
| `/sha256sum/:hash` | GET | Direct file download (by hash); returns the file stream |

## 2. Anonymous Collections (Anon Collections)

| Endpoint | Method | Description |
|------|------|------|
| `/anon/collections` | GET | List all anonymous collections |
| `/anon/collections` | POST | Create a collection `{ entries, friendly_name, tags, visibility, access_list_hash }` |
| `/anon/collections/:hash` | GET | Get collection details |
| `/anon/collections/:hash/:path` | GET | Download a file at the specified path within a collection |
| `/anon/collections/commit` | POST | Commit a collection version `{ source_hash, entries, commit_message }` |
| `/anon/collections/fork` | POST | Fork a collection `{ source_hash, add_entries, remove_paths, friendly_name }` |

Entry format:

```json
// SHA256 provider (file already registered in storage)
{"path":"readme.txt","providers":[{"type":"sha256","value":"<64-hex>"}]}
// URL provider (external link, no need to download to local storage)
{"path":"readme.txt","providers":[{"type":"url","value":"https://example.com/readme.txt"}]}
// Directory entry (providers not required)
{"path":"images/"}
```

## 3. User Collections

| Endpoint | Method | Description |
|------|------|------|
| `/collections/:username` | GET | List a user's collections |
| `/collections` | POST | Create a collection `{ username, collection_name, visibility, tags }` |
| `/collections/:username/:coll` | GET | Get collection details |
| `/collections/:username/:coll/tags` | POST | Update collection tags `{ tags }` |
| `/collections/:username/:coll/entries` | POST | Add an entry `{ path, hash }` |
| `/collections/:username/:coll/entries/:path` | DELETE | Delete an entry |
| `/collections/:username/:coll/commit` | POST | Commit a version `{ commit_message }` |
| `/collections/:username/:coll/log` | GET | Version history |
| `/collections/:username/:coll/rollback/:vid` | POST | Roll back to a specified version |
| `/collections/:username/:coll/visibility` | POST | Set visibility `{ visibility }` |
| `/collections/search?q=` | GET | Search public collections |
| `/collections/public?q=` | GET | List public collections |
| `/:username/:coll/:filepath` | GET | Direct download of a collection file |

## 4. Collection Collaboration (Actions)

| Endpoint | Method | Description |
|------|------|------|
| `/actions/fork` | POST | Fork a user collection `{ username, source_username, collection_name, source_coll_name }` |
| `/actions/merge` | POST | Merge collections `{ ..., strategy: "ours"|"theirs" }` |
| `/actions/pull` | POST | Pull collection updates `{ username, collection_name }` |

## 5. P2P Network

### Status and Queries

| Endpoint | Method | Description |
|------|------|------|
| `/p2p/status` | GET | Overall P2P status (includes signal_peers) |
| `/p2p/node` | GET | Local node info |
| `/p2p/peers` | GET | List of connected peers |
| `/p2p/peers/detail` | GET | Detailed peer info |
| `/p2p/peers/detail/:peerId` | GET | Details for a single peer |
| `/p2p/discovered` | GET | Discovered node list |
| `/p2p/connections` | GET | Connection list |
| `/p2p/stats` | GET | P2P statistics |
| `/p2p/topology` | GET | Network topology data |
| `/p2p/quality` | GET | Connection quality info |
| `/p2p/ws/info` | GET | WebSocket transport info |
| `/p2p/auth/status` | GET | JWT auth status |

### Operations

| Endpoint | Method | Description |
|------|------|------|
| `/p2p/ping/:peerId` | GET | Ping a specified peer |
| `/p2p/connect` | POST | Connect to a peer `{ peer_id, addrs }` |
| `/p2p/announce` | POST | Announce a hash to the network `{ hash }` |
| `/p2p/fetch` | POST | Fetch data from a peer `{ peer_id, hash }` |
| `/p2p/sync` | POST | Sync a collection from a peer `{ peer_id, collection_name }` |
| `/p2p/push` | POST | Push a collection to a peer `{ peer_id, collection_name }` |
| `/p2p/request-file` | POST | Request a file `{ hash }` |
| `/p2p/dual/announce` | POST | Dual-stack announce `{ hash }` |
| `/p2p/dual/find` | POST | Dual-stack lookup `{ hash }` |

### WebSocket

| Endpoint | Description |
|------|------|
| `/ws/transfer` | File transfer WebSocket; the scheme of the HTTP API Base is changed to `ws` |

## 6. BT (BitTorrent)

| Endpoint | Method | Description |
|------|------|------|
| `/bt/status` | GET | BT module status |
| `/bt/stats` | GET | BT statistics |
| `/bt/announce` | POST | BT DHT announce `{ hash }` |
| `/bt/find` | POST | BT DHT lookup `{ hash }` |
| `/bt/downloads` | GET | List all download tasks |
| `/bt/download/:infohash` | GET | Details for a single download task |
| `/bt/magnet` | POST | Resolve a Magnet URI `{ uri }` |
| `/bt/torrent` | POST | Upload a .torrent file (multipart, field: `torrent`) |
| `/bt/download/:infohash` | DELETE | Delete a download task |
| `/bt/download/:infohash/pause` | POST | Pause a download |
| `/bt/download/:infohash/resume` | POST | Resume a download |
| `/bt/download/:infohash/seed` | POST | Start seeding |
| `/bt/download/:infohash/unseed` | POST | Stop seeding |
| `/bt/bep51/sample` | GET | BEP 51 sample data (used by the antenna feature) |

## 7. IPFS

| Endpoint | Method | Description |
|------|------|------|
| `/ipfs` | GET | IPFS compatibility layer status |
| `/ipfs/toggle` | POST | Enable/disable IPFS compatibility mode `{ enabled }` |
| `/ipfs/pin/:cid` | POST | Pin a CID |
| `/ipfs/pin/:cid` | DELETE | Unpin a CID |
| `/ipfs/pins` | GET | List pinned CIDs |
| `/ipfs/gateways` | GET | IPFS gateway health check |

## 8. Local Sync & Access Control

| Endpoint | Method | Description |
|------|------|------|
| `/local/save` | POST | Save a collection locally |
| `/local/status/:hash` | GET | Query local save status |
| `/access/list` | POST | Create an ACL `{ users, groups }` |
| `/access/list/:hash` | GET | Get ACL details |

## 9. System & Tasks

| Endpoint | Method | Description |
|------|------|------|
| `/ping` | GET | Health check |
| `/tasks` | GET | Background task list |
| `/tasks/:id` | GET | Single task status/progress |

## 10. Registration Server (external service)

The endpoints below call an external registration server; the URL is configured via `peerdrive_reg_server_url`.

| Endpoint | Description |
|------|------|
| `/reg/users` | Registered user list |
| `/reg/groups` | Group list |
| `/reg/groups/:name/members` | Group members |
| `/comments/:hash` (GET) | Get collection comments |
| `/comments/:hash` (POST) | Post a comment `{ content }` |
| `/stats` | Server statistics |
| `/auth/whoami` | JWT identity verification |
| `/auth/group/:username` (GET/POST) | User group management |

## Frontend Pages → API Mapping

| Page (route) | Component | Primary APIs called |
|-----------|------|---------------|
| `/` | Plaza | `listAnonCollections`, `searchCollections`, `listPublicCollections`, `getBEP51Sample` |
| `/files` | FileManager | `listFiles`, `registerLocalFile`, `registerFolder`, `deleteFile`, `browseDir`, `uploadFile` |
| `/create` | AnonCreator | `createAnonCollection`, `commitAnonCollection`, `listFiles`, `registerLocalFile`, `registerURL`, `browseDir`, `saveLocal` |
| `/anon/collections/:hash` | AnonExplorer | `getAnonCollection`, `forkAnonCollection` |
| `/:username/:collName` | Explorer | `getUserCollection`, `getVersionLog`, `commitCollection`, `rollbackVersion` |
| `/p2p` | P2PPanel | `getP2PStatus`, `getConnections`, `getP2PPeers`, `getWSInfo` |
| `/p2p/topology` | P2PTopology | `getP2PTopology`, `getP2PQuality` |
| `/p2p/dht` | DHTExplorer | `p2pAnnounce`, `p2pFetch` |
| `/p2p/dashboard` | P2PDashboard | `getP2PStats`, `getP2PDiscovered` |
| `/ipfs` | IPFSPanel | `getIPFSCompatStatus`, `getIPFSGatewayStatus`, `listPins`, `pinCID`, `unpinCID` |
| `/bt` | BTPanel | `getBTStatus`, `btFind`, `btAnnounce`, `getBEP51Sample` |
| `/bt/controller` | BTController | `btGetDownloads`, `btMagnetResolve`, `btTorrentUpload`, `btPauseDownload`, `btResumeDownload`, `btRemoveDownload` |
| `/settings` | Settings | `ping`, `getAuthStatus`, `getApiBase`/`setApiBase`, LLM configuration |

## Authentication Configuration (localStorage keys)

| Key | Description |
|-----|------|
| `peerdrive_api_base` | API Base URL |
| `peerdrive_auth_token` | Token passed via URL fragment |
| `peerdrive_auth_key` | Token entered manually on the settings page |
| `peerdrive_auth_header_enabled` | Whether the auth header is enabled |
| `peerdrive_reg_server_url` | Registration server address |

## LLM Configuration (localStorage keys)

| Key | Description |
|-----|------|
| `peerdrive_llm_endpoint` | LLM API endpoint |
| `peerdrive_llm_model` | Model name |
| `peerdrive_llm_apikey` | API Key |
| `peerdrive_llm_body_template` | Request body template (JSON) |
| `peerdrive_data_consent` | Data consent |

---

## Additional Routes

### P2P Resumable / Multi-Source Download

| Method | Path | Description |
|------|------|------|
| POST | `/p2p/download/resume` | Resumable download |
| GET | `/p2p/download/progress/:hash` | Download progress |
| POST | `/p2p/download/cancel/:hash` | Cancel a download |
| POST | `/p2p/download/multipeer` | Multi-peer concurrent download |
| GET | `/p2p/download/sources/:hash` | Download source list |
| GET | `/p2p/download/multipeer/progress/:hash` | Multi-source download progress |

### Port Forwarding

| Method | Path | Description |
|------|------|------|
| POST | `/p2p/forward/create` | Create a forwarding session |
| POST | `/p2p/forward/connect` | Connect to a forwarding session |
| GET | `/p2p/forward/list` | List active forwards |
| POST | `/p2p/forward/close` | Close a forwarding session |

### Additional BT Endpoints

| Method | Path | Description |
|------|------|------|
| GET | `/bt/stats` | Global BT statistics |
| GET | `/bt/download/:infohash/torrent` | Download the .torrent file |
| GET | `/bt/download/:infohash/magnet` | Get the magnet URI |
| POST | `/bt/seed-collection` | Seed an anonymous collection as BT |

### IPFS CID Download

| Method | Path |
|------|------|
| GET | `/ipfs/:cid` |

### Shares

| Method | Path | Description |
|------|------|------|
| POST | `/shares` | Create a share |
| GET | `/shares` | List shares |
| GET | `/s/:token` | Access a share |

### WebDAV

| Method | Path |
|------|------|
| ANY | `/webdav/*path` |

### Relay Proxy

| Method | Path |
|------|------|
| GET | `/relay/proxy` |

### Swagger UI

| Method | Path |
|------|------|
| GET | `/swagger/*any` |
