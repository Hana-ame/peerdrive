# Peerdrive Node Development API Reference

> For developers: Three-layer interfaces exposed by the node (Go)——**HTTP management plane / Frame protocol (shared by DataChannel/WS) / Go API**.
> Complements `doc/api-reference.md` (HTTP API reference for frontend); see `doc/REFACTOR.md` §4 for protocol details.

## 1. HTTP Management Plane (`back/internal/router/`)

### 1.1 Node Discovery & Fetch (`peerjs_routes.go`)

| Endpoint | Method | Auth | Usage |
|---|---|---|---|
| `/peerjs/node` | GET | None | This node status: `{"id","online":true,"peers":[]}` (peers = currently connected remote node ids) |
| `/peerjs/fetch` | POST | Required | Fetch file from peer: `{peer, hash, offset?, size?}` → file stream (≤64MB, H4; large files use `/ws/peer` chunked) |
| `/ws/peer` | GET(WS) | Origin whitelist | Local direct session, frame protocol identical to DataChannel |

`/peerjs/fetch` example:

```bash
curl -X POST http://localhost:3000/peerjs/fetch \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  -d '{"peer":"peerdrive-abc123","hash":"<64hex>","offset":0,"size":-1}' -o file.bin
```

### 1.2 Port Forwarding (`controller/p2p.go:711`)

| Endpoint | Method | Auth | Usage |
|---|---|---|---|
| `/p2p/forward/create` | POST | Required | Register authorization: `{key, port}` (key is the credential, runtime additions not persisted) |
| `/p2p/forward/connect` | POST | Required | Start loopback listener on this end: `{key, target_peer, local_port, port?}` (`local_port` required for this-end listening; `port` optional target port, 0/omitted = determined by target node rules for unique port) |
| `/p2p/forward/list` | GET | None | Active listeners and tunnels: `{"listeners":[{id,target_peer,local_port}], "tunnels":[{peer_id,port,key_id}]}` |
| `/p2p/forward/close` | POST | Required | Close forwarding: `{key?}` closes listener matching key, `{peer_id?}` disconnects all tunnels for specified peer |

### 1.3 Unified Source Management (`source_routes.go`)

| Endpoint | Method | Auth | Usage |
|---|---|---|---|
| `/sources` | GET | None | Snapshot of each source: priority/capability/online/hit stats |
| `/sources/:name/priority` | POST | None | Adjust priority at runtime: `{"priority": n}` |

### 1.4 General (`router.go`)

| Endpoint | Method | Description |
|---|---|---|
| `/ping` | GET | Health check |
| `/download/:hash` | GET | Unified multi-protocol download (source chain) |
| `/download/:hash/sources` | GET | Download source list |
| `/download/:hash/refresh` | POST | Refresh source (requires auth) |
| `/swagger/*any` | GET | Swagger UI |

## 2. Frame Protocol (Shared by DataChannel / WS, `conn.go`)

### 2.1 File Fetch

```jsonc
// Request (any side); reqId is command UUID v4 (Go side always carries)
{"type":"req","hash":"<64hex>","offset":0,"size":-1,"reqId":"<uuid-v4>"}
// Response (echoes reqId)
{"type":"meta","hash","total","reqId"}
{"type":"data","hash","offset","size","reqId"}   // Followed by size bytes of binary
{"type":"done","hash","offset","size","reqId"}
{"type":"err","msg","reqId"}
```

### 2.2 File Index Verbs (`file_index.go`)

```jsonc
create   {type:"create", path}              → created {hash,size,name,path,seq}
upload   {type:"upload", name, size, offset?, reqId}   // Chunked upload (offset defaults to 0)
         → meta {total, offset} → data×1 → uploaded {hash,path} (complete) | ack {offset} (resume)
list     {type:"list", offset?, size?}      → list-resp {files,total}
info     {type:"info", hash}                → info-resp {hash,size,name,path,seq}
delete   {type:"delete", hash}              → deleted {hash,seq}
sync     {type:"sync", seq}                 → sync-resp {files,lastSeq}
```

- Chunked upload: offset aligned to 64KB chunks, one upload = one chunk (data block ≤64KB);
  Multiple sources = multiple connections upload different chunks concurrently, bitmap full auto-triggers uploaded
- Resume: Same name reopens session with idempotent reuse; session cleaned after 10 min inactivity

### 2.3 Port Forwarding v2 (`forward.go`)

```jsonc
client ──fwd-open {port, reqId}──────────▶ server
client ◀──fwd-challenge {nonce, reqId}──── server   One-time random number
client ──fwd-auth {hmac, reqId}──────────▶ server   HMAC-SHA256(key, nonce)
client ◀──fwd-ok / fwd-err──────────────── server
Afterward: fwd-data header + binary blocks bidirectional pass-through (header text, block binary, atomic consecutive)
      fwd-close Finalize (either side EOF/active close)
```

### 2.4 Admin Management Plane Verbs (`admin.go`, Local WS Sessions Only)

Browser sends admin frames via `/ws/peer` local session to manage this node (internally forwards gin engine, reuses all
HTTP controller logic, zero duplication). **Only `ID()=="local"` sessions accepted**——WebRTC connections
from any node on public signaling, management plane not exposed (prevents privilege escalation).

```jsonc
// Normal request → admin-resp
{"type":"admin","method":"GET|POST|DELETE","path":"/files?x=1","body":<JSON>,"token":"<optional>","reqId"}
{"type":"admin-resp","status":200,"body":<raw JSON>,"reqId"}   // 4xx/5xx also use admin-resp (body is structured error body, 409 includes conflicts list)

// Binary upload (declaration frame + subsequent binary frames; after collecting all, multipart repackaged and forwarded)
{"type":"admin","method":"POST","path":"/files/upload","binary":true,"filename":"a.bin","field":"file","size":N,"reqId"} + N bytes binary frame
   // field defaults to "file"; BT torrent upload field="torrent" + path="/bt/torrent"
   // reqPath determined by declaration frame path (old bug with hardcoded /files/upload fixed, see admin_test.go TestAdminBinaryUploadReqPath)

// Binary response (file stream, e.g., collection files) → admin-bin header + immediate binary frame (≤64MB adminBinMax; large files use 2.1 req verb chunked)
{"type":"admin-bin","status":200,"size":N,"reqId"} + binary frame
```

- Upload collection: Connection-level single slot (`st.adminUp`), declaration frame **synchronously** claims slot in message pump (async would lose subsequent
  binary blocks); 30s no data auto-aborts and cleans temp files
- Authentication: admin frame `token` field → inject `Authorization: Bearer` during forwarding → consistent with HTTP behavior
- Frontend usage: `front/src/ws.js` (`admin`/`upload`) + `front/src/api.js` (all requests use this channel)

### 2.5 Three Protocol Hard Constraints (Do Not Break)

1. JSON control headers must be **text frames** (`SendText`), data blocks must be **binary frames** (`Send`)——sending backwards causes peer to treat control header as data block
2. data header and data block must be **atomic consecutive** (SendFrame sendMu); receiver routes by connection-level expect state machine
3. Browser side may omit reqId (backward compatible), Go side always carries (UUID v4)

## 3. Go API (`transport.PeerJSService`, `peerjs_service.go`)

### 3.1 Connection Management

| Method | Description |
|---|---|
| `NewPeerJSService(cfg, storageDir) *PeerJSService` | Construct (node id defaults to `peerdrive-<random hex>`) |
| `Start()` | Register signaling + auto-reconnect on disconnect (async) |
| `Close()` | Close signaling + all WebRTC connections |
| `ID() string` | This node's signaling id |
| `Connections() map[string]Session` | Currently active connections (key = remote peer id, includes `"local"`) |
| `BindLocal(sess Session)` | Register local WS session |
| `FileIndex() *FileIndexService` | Shared local file index (for LocalSource assembly) |

### 3.2 File Fetch (Outbound Role)

| Method | Description |
|---|---|
| `OpenStream(peerID, hash, offset, size) (io.ReadCloser, error)` | **Streaming** fetch; auto SHA256 verify after full read; Close cancels early (does not disconnect) |
| `FetchFromPeer(peerID, hash, offset, size) ([]byte, error)` | Full fetch (OpenStream + ReadAll wrapper) |

```go
// Example: Stream fetch file from remote node
r, err := svc.OpenStream("peerdrive-abc123", hash, 0, -1)
if err != nil { /* No connection / Fetch failed */ }
defer r.Close()
written, err := io.Copy(dstFile, r) // Auto SHA256 verify on EOF
```

### 3.3 Port Forwarding (Client Side)

| Method | Description |
|---|---|
| `AddForwardRule(key string, port int) error` | Add authorization at runtime (not persisted) |
| `SetForwardRules(rules map[string][]int)` | Set full rules table |
| `OpenForward(ctx, peerID, key string, port int) (net.Conn, error)` | Establish forwarding tunnel (port=0 determined by server rules for unique port) |
| `ListForwardStreams() []ForwardStreamInfo` | Active tunnel snapshot |
| `CloseForwardStream(peerID string)` | Actively disconnect tunnel |

```go
// Example: Access peer node's 127.0.0.1:8080 through it
conn, err := svc.OpenForward(ctx, "peerdrive-abc123", "my-key", 8080)
if err != nil { /* Auth failed / Port not authorized */ }
defer conn.Close()
// Use conn as regular TCP connection, bidirectional pass-through
```

## 4. peerjs Module Extension Points (`back/peerjs/`, independent go.mod)

| Extension Point | Interface | Description |
|---|---|---|
| Change signaling | `Signaller` Interface (`signaller.go`) | `NewPeerWithSignaller()` injection; implementation only needs to call injected route callback after receiving message |
| Change transport | `DataChannel` Interface (`transport.go`) | `Connection` only depends on this interface |
| Add verb | `MessageType` / `Frame` | Open string type, send custom messages directly |

## 5. Configuration Quick Reference (Node-related)

| Env Variable | Default | Description |
|---|---|---|
| `PEERDRIVE_PEERJS_ENABLE` | true | Enable node interconnection |
| `PEERDRIVE_PEERJS_ID` | Random | Node id |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY/SECURE` | 0.peerjs.com/443/peerjs/true | Signaling server |
| `PEERDRIVE_PEERJS_PEERS` | - | Comma-separated peer ids, auto-connect on startup |
| `PEERDRIVE_DISCOVER_URL` | - | Self-hosted discovery API (priority over MQTT) |
| `PEERDRIVE_MQTT_ENABLE` | false | MQTT discovery switch |
| `PEERDRIVE_MQTT_BROKER` | tcp://broker.emqx.io:1883 | MQTT broker |
| `PEERDRIVE_MQTT_TOPIC_PREFIX` | peerdrive/v1 | Discovery topic prefix |
| `PEERDRIVE_MQTT_COLLECTIONS` | - | Comma-separated collection hash shards to watch |
| `PEERDRIVE_FORWARD_RULES` | - | Forwarding whitelist `key:port,key2:port2` (key is credential, recommend chmod 600) |
| `PEERDRIVE_WEBRTC_STUN` | stun:stun.l.google.com:19302 | STUN server |
| `PEERDRIVE_WEBRTC_TURN` | - | TURN server (comma-separated multiple URLs) |
