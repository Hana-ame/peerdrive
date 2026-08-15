# Peerdrive BitTorrent Module API Design

Base URL: `http://<host>:<port>` (default port `3000`, configurable via `PORT` env var)

All endpoints are under the `/bt/` prefix (mounted on the `/p2p` Gin group).

---

## Endpoints

### 1. BT DHT Status

**`GET /bt/status`**

Returns the BitTorrent DHT node status.

**Response `200 OK`:**

```json
{
  "enabled": true,
  "listen_addr": "0.0.0.0:6881",
  "num_nodes": 42
}
```

**Example:**

```bash
curl -s http://localhost:3000/bt/status | python3 -m json.tool
```

---

### 2. Announce on BT DHT

**`POST /bt/announce`**

Announce an infohash on the BitTorrent Mainline DHT. The server will register itself as a potential provider for the given hash.

**Request Body:**

```json
{
  "hash": "<40-char hex infohash or 64-char SHA256>"
}
```

**Response `200 OK`:**

```json
{
  "status": "announced on BT DHT"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/announce \
  -H "Content-Type: application/json" \
  -d '{"hash":"0123456789abcdef0123456789abcdef01234567"}' | python3 -m json.tool
```

---

### 3. Find Providers on BT DHT

**`POST /bt/find`**

Query the BitTorrent DHT for peers that have announced the given infohash.

**Request Body:**

```json
{
  "hash": "<40-char hex infohash or 64-char SHA256>"
}
```

**Response `200 OK`:**

```json
{
  "hash": "0123456789abcdef0123456789abcdef01234567",
  "peers": ["192.168.1.100:6881", "10.0.0.5:51413"],
  "count": 2
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/find \
  -H "Content-Type: application/json" \
  -d '{"hash":"0123456789abcdef0123456789abcdef01234567"}' | python3 -m json.tool
```

---

### 4. Upload Torrent File

**`POST /bt/torrent`**

Upload a `.torrent` file to start downloading. The torrent is parsed (bencode), and download begins asynchronously via DHT peer discovery and wire-protocol piece exchange.

**Request:** `multipart/form-data`

| Field    | Type | Description     |
|----------|------|-----------------|
| `torrent` | file | The .torrent file |

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "name": "ubuntu-24.04-desktop.iso",
  "files": 1,
  "total": 4831838208,
  "pieces": 14745,
  "status": "downloading"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/torrent \
  -F "torrent=@/path/to/file.torrent" | python3 -m json.tool
```

---

### 5. Resolve Magnet URI

**`POST /bt/magnet`**

Resolve a magnet URI and start downloading. Supports both 40-char hex and 32-char base32 infohash formats.

**Request Body:**

```json
{
  "uri": "magnet:?xt=urn:btih:<infohash>&dn=<name>&tr=<tracker>"
}
```

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "display_name": "ubuntu-24.04-desktop.iso",
  "trackers": ["udp://tracker.ubuntu.com:6969"],
  "status": "downloading"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/magnet \
  -H "Content-Type: application/json" \
  -d '{"uri":"magnet:?xt=urn:btih:a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0&dn=test.txt&tr=udp://tracker.opentrackr.org:1337"}' | python3 -m json.tool
```

---

### 6. Download Progress

**`GET /bt/download/:infohash`**

Query the download progress for a specific infohash.

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "name": "test.torrent",
  "total_size": 1048576,
  "downloaded": 524288,
  "pieces_total": 64,
  "pieces_done": 32,
  "peers": 3,
  "speed_bytes_per_sec": 102400.0,
  "status": "downloading",
  "seeding": false
}
```

**Response `404 Not Found`:**

```json
{
  "error": "download not found"
}
```

**Example:**

```bash
curl -s http://localhost:3000/bt/download/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0 | python3 -m json.tool
```

---

### 7. List All Downloads

**`GET /bt/downloads`**

Returns all active, paused, completed, and failed BT downloads.

**Response `200 OK`:**

```json
{
  "downloads": [
    {
      "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
      "name": "test.torrent",
      "total_size": 1048576,
      "downloaded": 1048576,
      "pieces_total": 64,
      "pieces_done": 64,
      "peers": 0,
      "speed_bytes_per_sec": 0.0,
      "status": "completed",
      "seeding": true
    }
  ],
  "count": 1
}
```

**Example:**

```bash
curl -s http://localhost:3000/bt/downloads | python3 -m json.tool
```

---

### 8. Global BT Statistics

**`GET /bt/stats`**

Returns global BitTorrent client statistics (also includes the seeding list).

**Response `200 OK`:**

```json
{
  "total_up_bytes": 0,
  "total_down_bytes": 1048576,
  "active_torrents": 0,
  "paused_torrents": 0,
  "completed": 1,
  "errors": 0,
  "dht_nodes": 42,
  "seeding": 1,
  "seeding_hashes": ["a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"]
}
```

**Example:**

```bash
curl -s http://localhost:3000/bt/stats | python3 -m json.tool
```

---

### 9. Pause Download

**`POST /bt/download/:infohash/pause`**

Pause an active download.

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "status": "paused"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/download/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0/pause | python3 -m json.tool
```

---

### 10. Resume Download

**`POST /bt/download/:infohash/resume`**

Resume a paused download.

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "status": "downloading"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/download/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0/resume | python3 -m json.tool
```

---

### 11. Remove Download

**`DELETE /bt/download/:infohash`**

Remove a download task and its data directory.

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "status": "removed"
}
```

**Example:**

```bash
curl -s -X DELETE http://localhost:3000/bt/download/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0 | python3 -m json.tool
```

---

### 12. Start Seeding

**`POST /bt/download/:infohash/seed`**

Start seeding a completed download. The server listens on a TCP port and responds to BitTorrent wire-protocol piece requests.

**Note:** The canonical path is `POST /bt/download/:infohash/seed`. A shorthand form `/bt/seed/:ih` may be added in future releases.

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "status": "seeding"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/download/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0/seed | python3 -m json.tool
```

---

### 13. Stop Seeding

**`POST /bt/download/:infohash/unseed`**

Stop seeding a completed download.

**Response `200 OK`:**

```json
{
  "infohash": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "status": "stopped"
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/download/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0/unseed | python3 -m json.tool
```

---

### 14. BEP 44 Put

**`POST /bt/bep44/put`**

Store immutable data on the BitTorrent DHT (BEP 44). Data is base64-encoded. Maximum value size: 1000 bytes (bencoded).

**Request Body:**

```json
{
  "data": "<base64-encoded data>",
  "mutable": false
}
```

**Response `200 OK`:**

```json
{
  "target": "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
  "size": 42,
  "mutable": false
}
```

**Example:**

```bash
# Encode "Hello, BT DHT!" as base64
DATA=$(echo -n "Hello, BT DHT!" | base64)

curl -s -X POST http://localhost:3000/bt/bep44/put \
  -H "Content-Type: application/json" \
  -d "{\"data\":\"$DATA\",\"mutable\":false}" | python3 -m json.tool
```

**Error (mutable not supported via API):**

```json
{
  "error": "mutable put via API requires key management; use Go API directly"
}
```

---

### 15. BEP 44 Get

**`POST /bt/bep44/get`**

Retrieve immutable data from the BitTorrent DHT (BEP 44) by its 40-char hex target hash.

**Request Body:**

```json
{
  "target": "<40-char hex target hash>"
}
```

**Response `200 OK`:**

```json
{
  "data": "<base64-encoded data>",
  "size": 42
}
```

**Example:**

```bash
curl -s -X POST http://localhost:3000/bt/bep44/get \
  -H "Content-Type: application/json" \
  -d '{"target":"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0"}' | python3 -m json.tool
```

---

### 16. BEP 51 Sample Infohashes

**`GET /bt/bep51/sample`**

Collect infohash samples from the DHT routing table using BEP 51 (`sample_infohashes` query). Returns up to 200 unique infohashes.

**Response `200 OK`:**

```json
{
  "samples": [
    "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0",
    "0123456789abcdef0123456789abcdef01234567"
  ],
  "count": 2
}
```

**Example:**

```bash
curl -s http://localhost:3000/bt/bep51/sample | python3 -m json.tool
```

---

## Route Summary

| Method   | Path                                     | Handler              | Description                       |
|----------|------------------------------------------|----------------------|-----------------------------------|
| `GET`    | `/bt/status`                         | BTDHTStatus          | DHT node status                   |
| `POST`   | `/bt/announce`                       | BTAnnounce           | Announce on BT DHT                |
| `POST`   | `/bt/find`                           | BTFindProviders      | Find providers on BT DHT          |
| `POST`   | `/bt/torrent`                        | BTTorrentUpload      | Upload .torrent file              |
| `POST`   | `/bt/magnet`                         | BTMagnetResolve      | Resolve magnet URI                |
| `GET`    | `/bt/download/:infohash`             | BTDownloadProgress   | Download progress                 |
| `GET`    | `/bt/downloads`                      | BTDownloadList       | List all downloads                |
| `GET`    | `/bt/stats`                          | BTGlobalStats        | Global BT statistics              |
| `POST`   | `/bt/download/:infohash/pause`       | BTPauseDownload      | Pause download                    |
| `POST`   | `/bt/download/:infohash/resume`      | BTResumeDownload     | Resume download                   |
| `DELETE` | `/bt/download/:infohash`             | BTRemoveDownload     | Remove download                   |
| `POST`   | `/bt/download/:infohash/seed`        | BTSeedTorrent        | Start seeding                     |
| `POST`   | `/bt/download/:infohash/unseed`      | BTStopSeed           | Stop seeding                      |
| `POST`   | `/bt/bep44/put`                      | BEP44Put             | BEP 44 immutable put              |
| `POST`   | `/bt/bep44/get`                      | BEP44Get             | BEP 44 immutable get              |
| `GET`    | `/bt/bep51/sample`                   | BEP51Sample          | BEP 51 infohash sample            |

---

## Implementation Details

- **BT DHT**: Wraps `github.com/anacrolix/dht/v2`. Bootstraps from `router.bittorrent.com:6881` and `dht.transmissionbt.com:6881`.
- **Torrent Parsing**: Custom bencode decoder -- parses the `info` dict, computes SHA1 infohash, extracts piece hashes and file list.
- **Magnet Parsing**: Supports both 40-char hex and 32-char base32 infohash formats.
- **Wire Protocol**: Full BitTorrent wire protocol -- handshake, bitfield, interested/unchoke, request/piece message exchange with SHA1 verification.
- **Seeder**: TCP listener that responds to BT wire protocol piece requests. Random port allocation.
- **BEP 44**: Iterative Kademlia lookup; puts data on local server and up to 8 closest remote nodes.
- **BEP 51**: Queries up to 8 closest routing-table nodes with `sample_infohashes` query; falls back to local server.
- **HTTP Tracker**: BEP 3 HTTP tracker announce for peer discovery from tracker URLs in .torrent files.

## Files

| File                                      | Package       | Description                          |
|-------------------------------------------|---------------|--------------------------------------|
| `internal/p2p_bt/bt_dht.go`              | `p2p_bt`      | DHT service (announce, find)         |
| `internal/p2p_bt/client.go`              | `p2p_bt`      | BTClient (download management)       |
| `internal/p2p_bt/torrent.go`             | `p2p_bt`      | Bencode decoder + torrent parsing    |
| `internal/p2p_bt/magnet.go`              | `p2p_bt`      | Magnet URI parser                    |
| `internal/p2p_bt/piece.go`               | `p2p_bt`      | Wire protocol (handshake, piece)     |
| `internal/p2p_bt/seeder.go`              | `p2p_bt`      | BTSeeder (TCP piece server)          |
| `internal/p2p_bt/bep44.go`               | `p2p_bt`      | BEP 44 put/get                       |
| `internal/p2p_bt/bep51.go`               | `p2p_bt`      | BEP 51 infohash sampling             |
| `internal/p2p_bt/tracker.go`             | `p2p_bt`      | HTTP tracker announce (BEP 3)        |
| `internal/p2p_bt/bt_bridge.go`           | `p2p_bt`      | File-DHT bridge                      |
| `internal/p2p_bt/log.go`                 | `p2p_bt`      | Logging utilities                    |
| `internal/p2p_bt/bt_test.go`             | `p2p_bt`      | Go unit/integration tests            |
| `internal/controller/p2p.go`             | `controller`  | HTTP handlers for all BT endpoints   |
| `internal/router/router.go`              | `router`      | Route registration                   |
