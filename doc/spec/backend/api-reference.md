# Peerdrive API Reference

Base URL: `http://localhost:3000`

All write operations can be disabled via the `PEERDRIVE_STORAGE_ENABLE=false` environment variable (returns 403).

---

## File Management

### Upload File
```
POST /files/upload
Content-Type: multipart/form-data
Field: file
```
**Response 201 (new file):**
```json
{"hash":"abc123...","size":1024,"mime":"text/plain","filename":"a.txt","already_exists":false}
```
**Response 200 (duplicate file):**
```json
{"hash":"abc123...","size":1024,"mime":"text/plain","filename":"a.txt","already_exists":true}
```
**Response 403:** `{"error":"storage is disabled"}`

### Register Local File (Zero-Copy)
```
POST /files/register_local
Content-Type: application/json
```
**Request body:**
```json
{"path":"/absolute/path/to/file.txt","filename":"file.txt"}
```
**Response 200:** `{"hash":"abc123...","filename":"file.txt"}`

### Register Folder (Recursive)
```
POST /files/register_folder
Content-Type: application/json
```
**Request body:**
```json
{"folder_path":"/absolute/path/to/folder"}
```
**Response 200:**
```json
{"registered":[{"filename":"a.txt","hash":"abc..."},{"filename":"b.txt","hash":"def..."}]}
```

### Verify File Metadata
```
GET /files/verify/:hash
```
**Response 200:** `{"hash":"abc...","filename":"a.txt","size":1024,"mime":"text/plain"}`

### Delete File
```
DELETE /files/:hash
```
**Response 200:** `{"message":"deleted"}`

---

## SHA256 Download

```
GET /sha256sum/:hash
```
**Response 200:** File stream with `Content-Disposition: attachment; filename=xxx`

---

## Anonymous Collections

### Create Collection
```
POST /anon/collections
Content-Type: application/json
```
**Request body:**
```json
{
  "entries": [
    {"path":"docs/readme.txt","hash":"abc123..."},
    {"path":"images/logo.png","hash":"def456..."}
  ]
}
```
- `path` must be a relative path and must not contain `..`
- `hash` must be 64-character hexadecimal
- Server-side sorts entries lexicographically by `path`

**Response 201:** `{"hash":"sha256-of-the-collection-json"}`

### Get Collection
```
GET /anon/collections/:hash
```
**Response 200:**
```json
{
  "version": 1,
  "friendly_name": "my-collection",
  "entries": [
    {"path":"docs/readme.txt","hash":"abc123..."},
    {"path":"images/logo.png","hash":"def456..."}
  ],
  "created_at": "2026-04-25T12:00:00Z"
}
```

### Download File from Collection
```
GET /anon/collections/:hash/entries/path/to/file
```
**Response 200:** File stream

### Fork Collection
```
POST /anon/collections/fork
Content-Type: application/json
```
**Request body:**
```json
{
  "source_hash":"abc123...",
  "add_entries":[{"path":"new.txt","hash":"def..."}],
  "remove_paths":["old.txt"]
}
```
**Response 201:** `{"hash":"new-collection-hash"}`

---

## P2P Network

### P2P Status
```
GET /p2p/status
```
**Response 200:**
```json
{"enabled":true,"peer_id":"12D3Koo...","addrs":["/ip4/..."],"connected_count":1,"discovered_count":2,"relay_mode":"client","hole_punch":true,"ws_connections":0}
```

### Node Information
```
GET /p2p/node
```
**Response 200:** `{"peer_id":"...","addrs":["/ip4/..."]}`

### Connected Peers
```
GET /p2p/peers
```
**Response 200:** `{"peers":["12D3Koo..."]}`

### Discovered Peers
```
GET /p2p/discovered
```
**Response 200:** `{"peers":[{"peer_id":"...","addrs":["..."]}]}`

### Ping Peer
```
GET /p2p/ping/:peer_id
```
**Response 200:** `{"peer":"...","rtt":"12.345ms"}`

### Connect to Peer
```
POST /p2p/connect
Content-Type: application/json
{"addr": "/ip4/127.0.0.1/tcp/12345/p2p/12D3Koo..."}
```
**Response 200:** `{"status":"connected"}`

### Announce Collection Hash
```
POST /p2p/announce
Content-Type: application/json
{"hash": "abc123..."}
```
**Response 200:** `{"status":"announced"}`

### Fetch Collection from P2P
```
POST /p2p/fetch
Content-Type: application/json
{"hash": "abc123..."}
```
**Response 200:** Collection JSON (with `friendly_name` + `entries`)

### Sync Files from Peer
```
POST /p2p/sync
Content-Type: application/json
{
  "peer_id": "12D3Koo...",
  "hash": "abc123...",
  "file_hashes": ["def456..."],
  "target_dir": "/tmp/sync"
}
```
**Response 200:** `{"synced":["def456..."],"count":1,"saved_to":"/tmp/sync"}`

### Receive Pushed Collection
```
POST /p2p/push
Content-Type: application/json
{
  "hash": "abc123...",
  "entries": [{"path":"a.txt","hash":"def..."}],
  "target_dir": "callback-folder"
}
```
**Response 200:** `{"entries":[...],"target_dir":"./callback-folder","message":"collection received"}`

### Broadcast File Request
```
POST /p2p/request-file
Content-Type: application/json
{"hash": "abc123...", "peer_ids": ["12D3Koo..."]}
```
Request a file from connected peers (broadcasts to all if `peer_ids` is empty). Fetches via the `/peerdrive/exchange/1.0.0` protocol.
**Response 200:** `{"hash":"...","requested":1,"responses":1,"details":[{"hash":"...","size":1024}]}`

### WS Transfer Info
```
GET /p2p/ws/info
```
**Response 200:** `{"ws_connections":0,"ws_endpoint":"/ws/transfer","message_types":["request","response","ping","pong"]}`

### WebSocket File Transfer
```
GET /ws/transfer
Upgrade: websocket
```
Bidirectional file transfer channel.

**Client → Server:**
```json
{"type":"request","hash":"abc123..."}
{"type":"response","hash":"abc123..."}  (forwarded to other WS clients)
{"type":"ping"}
```
**Server → Client:**
```json
{"type":"response","hash":"abc123...","size":1024}
  → Followed by Binary frame = file content
{"type":"error","hash":"abc123...","message":"not found locally"}
{"type":"pong"}
```
- `request`: Query for a file. If available locally, respond with `response` + binary; otherwise forward to other WS clients
- `response`: Broadcast to other connections (excluding the sender)
- Each node can accept multiple concurrent WS connections

---

## Frontend Call Examples

```js
// Register folder
const res = await fetch('/files/register_folder', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({folder_path: '/data/my-folder'})
});
const {registered} = await res.json();
// registered = [{filename:"a.txt", hash:"abc..."}, ...]

// Create anonymous collection from registration results
const entries = registered.map(f => ({path: f.filename, hash: f.hash}));
const coll = await fetch('/anon/collections', {
  method: 'POST',
  headers: {'Content-Type': 'application/json'},
  body: JSON.stringify({friendly_name: 'my-folder', entries})
});
const {hash} = await coll.json();

// Download file from collection
window.open(`/anon/collections/${hash}/entries/${registered[0].filename}`);
```

---

## Response Headers

| Header | Description |
|---|------|
| `Access-Control-Allow-Origin` | Matches the request Origin (dynamic) |
| `Access-Control-Allow-Credentials` | `true` |
| `Content-Disposition` | Includes filename when downloading files |
| `X-Peerdrive-Collection` | Set to `true` when downloading collection JSON |
