# IPFS Module API Design

> **Version:** 1.0
> **Last updated:** 2026-04-28
> **Scope:** All HTTP endpoints exposed by the Peerdrive IPFS module for content-addressed file retrieval, IPFS gateway proxying, CID pinning, and IPFS compatibility layer management.

---

## Table of Contents

1. [Endpoint Overview](#1-endpoint-overview)
2. [GET /ipfs/:cid -- Download by CID](#2-get-ipfscid-download-by-cid)
3. [GET /ipfs -- IPFS Compat Status](#3-get-p2pipfs-ipfs-compat-status)
4. [POST /ipfs/toggle -- Toggle IPFS Compat](#4-post-p2pipfstoggle-toggle-ipfs-compat)
5. [POST /ipfs/pin/:cid -- Pin CID](#5-post-p2pipfspincid-pin-cid)
6. [DELETE /ipfs/pin/:cid -- Unpin CID](#6-delete-p2pipfspincid-unpin-cid)
7. [GET /ipfs/pins -- List Pins](#7-get-p2pipfspins-list-pins)
8. [GET /ipfs/gateways -- Gateway Health Check](#8-get-p2pipfsgateways-gateway-health-check)
9. [Common Response Patterns](#9-common-response-patterns)

---

## 1. Endpoint Overview

| Method | Path | Description |
|--------|------|-------------|
| GET | `/ipfs/:cid` | Download a file by its IPFS CID (local DB lookup, fallback to public gateways) |
| GET | `/ipfs` | Get IPFS compatibility layer status (enabled/disabled, block count) |
| POST | `/ipfs/toggle` | Enable or disable the IPFS compatibility layer |
| POST | `/ipfs/pin/:cid` | Pin a CID: download from gateway, cache permanently in local storage |
| DELETE | `/ipfs/pin/:cid` | Unpin a CID: remove the pin record (local cached data is preserved) |
| GET | `/ipfs/pins` | List all pinned CIDs with metadata |
| GET | `/ipfs/gateways` | Check health and latency of all configured IPFS gateways |

---

## 2. GET /ipfs/:cid -- Download by CID

Downloads a file by its IPFS Content Identifier (CIDv1). The handler first looks up the CID in the local database's CID-to-SHA256 index. If found, it streams the local file. If not found and IPFS gateways are configured, it falls back to fetching from public IPFS gateways (ipfs.io, cloudflare-ipfs.com, dweb.link), caches the result locally, and streams it back.

### Request

```
GET /ipfs/:cid
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `cid` | string | IPFS CIDv1 string (e.g., `Qm...` or `bafy...`) |

**Query Parameters:**

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `inline` | bool | `false` | When `1`, sets `Content-Disposition: inline` instead of `attachment` |

**Headers:**

| Header | Description |
|--------|-------------|
| `Range` | Optional HTTP Range header for partial content (bytes=start-end) |

### Response (200)

```
HTTP/1.1 200 OK
Content-Type: application/octet-stream
Content-Disposition: attachment; filename=<cid>
X-CID: <cid>
X-Protocol: local | ipfsgw
Content-Encoding: gzip (if the stored file was gzip-compressed)
X-Peerdrive-Collection: true (if the file is an anonymous collection)
```

Body: raw binary file data.

### Response (206 - Partial Content)

```
HTTP/1.1 206 Partial Content
Content-Range: bytes <start>-<end>/<total>
Content-Type: application/octet-stream
```

Body: requested byte range.

### Response (404)

```json
{
  "error": "file not found by cid"
}
```

### Response (500 - Database error)

```json
{
  "error": "database error"
}
```

### Flow

```
Client                  Peerdrive Server
  |                            |
  |  GET /ipfs/QmXyz...        |
  |--------------------------->|
  |                            |
  |  1. Lookup CID in DB       |
  |  2a. Found? Stream local   |
  |      file by SHA256        |
  |  2b. Not found?            |
  |      Try gateways:         |
  |      - ipfs.io             |
  |      - cloudflare-ipfs.com |
  |      - dweb.link           |
  |      If success:           |
  |        Compute SHA256      |
  |        Cache to storage    |
  |        Stream file         |
  |  2c. All fail? Return 404  |
  |                            |
  |  <-- file data / 404 ------|
```

### curl Example

```bash
# Download a CID (local lookup with gateway fallback)
curl -s -o testfile.bin http://localhost:3000/ipfs/QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG

# Download with inline disposition
curl -s -o testfile.bin "http://localhost:3000/ipfs/QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG?inline=1"

# Download with Range header (resume download)
curl -s -H "Range: bytes=0-1023" -o partial.bin http://localhost:3000/ipfs/QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG

# CID not found (not in local DB and gateways unreachable)
curl -s http://localhost:3000/ipfs/QmInvalid123
# => {"error":"file not found by cid"}
```

---

## 3. GET /ipfs -- IPFS Compat Status

Returns the current status of the IPFS compatibility layer, which provides a Bitswap protocol handler for compatibility with standard IPFS nodes.

### Request

```
GET /ipfs
```

**Headers:** None required.

### Response (200)

```json
{
  "enabled": true,
  "block_count": 42,
  "blockstore": "./storage/ipfs-blocks"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | boolean | Whether the IPFS compat layer is active |
| `block_count` | integer | Number of blocks cached in the blockstore |
| `blockstore` | string | Filesystem path to the IPFS blockstore directory |

When the compat layer is not initialized:

```json
{
  "enabled": false,
  "block_count": 0,
  "blockstore": ""
}
```

### curl Example

```bash
curl -s http://localhost:3000/ipfs | jq
```

---

## 4. POST /ipfs/toggle -- Toggle IPFS Compat

Enables or disables the IPFS compatibility layer. When enabled, the server registers Bitswap protocol handlers (`/ipfs/bitswap/1.0.0`, `/ipfs/bitswap/1.1.0`, `/ipfs/bitswap/1.2.0`) on the libp2p host and copies pinned files into the IPFS blockstore.

### Request

```
POST /ipfs/toggle
Content-Type: application/json
```

**Request Body:**

```json
{
  "enabled": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | boolean | yes | `true` to enable, `false` to disable |

### Response (200)

```json
{
  "enabled": true
}
```

### Response (400 - Not initialized)

```json
{
  "error": "IPFS compat not initialized"
}
```

### curl Example

```bash
# Enable IPFS compat
curl -s -X POST http://localhost:3000/ipfs/toggle \
  -H "Content-Type: application/json" \
  -d '{"enabled": true}' | jq

# Disable IPFS compat
curl -s -X POST http://localhost:3000/ipfs/toggle \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}' | jq
```

---

## 5. POST /ipfs/pin/:cid -- Pin CID

Downloads a CID from the configured IPFS gateways, computes its SHA256 hash, stores the file permanently in content-addressed storage, and records the pin in the `ipfs_pins` database table. If the IPFS compat layer is enabled, also adds the file to the blockstore.

### Request

```
POST /ipfs/pin/:cid
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `cid` | string | IPFS CIDv1 string to pin |

### Response (200 - Pinned)

```json
{
  "status": "pinned",
  "cid": "QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG",
  "hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
  "size": 12345
}
```

| Field | Type | Description |
|-------|------|-------------|
| `status` | string | Always `"pinned"` for a successful new pin |
| `cid` | string | The pinned CID |
| `hash` | string | SHA256 hex digest of the pinned content |
| `size` | integer | Content size in bytes |

### Response (200 - Already Pinned)

```json
{
  "status": "already_pinned",
  "pin": {
    "cid": "QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG",
    "hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "size": 12345,
    "filename": "QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG",
    "pinned_at": "2026-04-28 12:00:00"
  }
}
```

### Response (404 - CID not fetchable)

```json
{
  "error": "failed to fetch CID from gateways: ..."
}
```

### Response (503 - Gateways not configured)

```json
{
  "error": "IPFS gateway not configured"
}
```

### Flow

```
Client                  Peerdrive Server
  |                            |
  | POST /ipfs/pin/:cid   |
  |--------------------------->|
  |                            |
  |  1. Check if already       |
  |     pinned (DB lookup)     |
  |  2. If already pinned:     |
  |     return "already_pinned"|
  |  3. Fetch from gateways:   |
  |     - Try each gateway in  |
  |       parallel with retry  |
  |     - First success wins   |
  |  4. Compute SHA256 hash    |
  |  5. Store in content-addr  |
  |     storage:               |
  |     <storage>/<hash[:2]>/  |
  |     <hash>                 |
  |  6. Insert FileMeta +      |
  |     FileProvider records   |
  |  7. If IPFS compat enabled,|
  |     add to blockstore      |
  |  8. Insert pin in          |
  |     ipfs_pins table        |
  |                            |
  |  <-- {"status":"pinned"} --|
```

### curl Example

```bash
# Pin a CID (downloads and caches permanently)
curl -s -X POST http://localhost:3000/ipfs/pin/QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG | jq

# Pin a CID that is already pinned
curl -s -X POST http://localhost:3000/ipfs/pin/QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG | jq
# => {"status":"already_pinned","pin":{...}}
```

---

## 6. DELETE /ipfs/pin/:cid -- Unpin CID

Removes the pin record for a CID from the `ipfs_pins` database table. The cached file data in content-addressed storage and blockstore is **not** deleted -- only the pin metadata is removed.

### Request

```
DELETE /ipfs/pin/:cid
```

**Path Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `cid` | string | IPFS CIDv1 string to unpin |

### Response (200)

```json
{
  "status": "unpinned",
  "cid": "QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG"
}
```

### Response (404 - Pin not found)

```json
{
  "error": "pin not found"
}
```

### curl Example

```bash
# Unpin a CID
curl -s -X DELETE http://localhost:3000/ipfs/pin/QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG | jq

# Unpin a CID that is not pinned
curl -s -X DELETE http://localhost:3000/ipfs/pin/QmDoesNotExist | jq
# => {"error":"pin not found"}
```

---

## 7. GET /ipfs/pins -- List Pins

Returns all pinned CIDs with metadata, ordered by most recently pinned first.

### Request

```
GET /ipfs/pins
```

**Headers:** None required.

### Response (200)

```json
{
  "pins": [
    {
      "cid": "QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG",
      "hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
      "size": 12345,
      "filename": "QmT5NvUtoP5fCaYby8TAGLJSWzLrA6nbgmFZBp9jNV7FdG",
      "pinned_at": "2026-04-28 12:00:00"
    }
  ],
  "count": 1
}
```

| Field | Type | Description |
|-------|------|-------------|
| `pins` | array | Array of pin objects, newest first |
| `pins[].cid` | string | The pinned CID |
| `pins[].hash` | string | SHA256 hex digest of the content |
| `pins[].size` | integer | Content size in bytes |
| `pins[].filename` | string | Filename hint (usually the CID string) |
| `pins[].pinned_at` | string | ISO-format timestamp when the pin was created |
| `count` | integer | Total number of pins |

When no pins exist:

```json
{
  "pins": [],
  "count": 0
}
```

### curl Example

```bash
# List all pinned CIDs
curl -s http://localhost:3000/ipfs/pins | jq
```

---

## 8. GET /ipfs/gateways -- Gateway Health Check

Checks the health and latency of all configured IPFS gateways by sending a HEAD request for a well-known CID (`QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn`, the empty directory CID). This endpoint does **not** require or set up any gateways -- it reports the status of whatever gateways are currently configured.

### Request

```
GET /ipfs/gateways
```

**Headers:** None required.

### Response (200)

```json
{
  "gateways": [
    {
      "url": "https://ipfs.io",
      "healthy": true,
      "latency": "234ms"
    },
    {
      "url": "https://cloudflare-ipfs.com",
      "healthy": true,
      "latency": "89ms"
    },
    {
      "url": "https://dweb.link",
      "healthy": false,
      "latency": ""
    }
  ]
}
```

| Field | Type | Description |
|-------|------|-------------|
| `gateways` | array | Array of gateway status objects |
| `gateways[].url` | string | The gateway base URL |
| `gateways[].healthy` | boolean | Whether the gateway responded (200 or 404 within 5s) |
| `gateways[].latency` | string | Round-trip latency, omitted on failure |

When no gateways are configured:

```json
{
  "gateways": []
}
```

### curl Example

```bash
# Check gateway health
curl -s http://localhost:3000/ipfs/gateways | jq
```

---

## 9. Common Response Patterns

### Success

```
HTTP/1.1 200 OK
Content-Type: application/json (or application/octet-stream for downloads)
```

### Bad Request (400)

```json
{
  "error": "cid is required"
}
```

### Not Found (404)

```json
{
  "error": "file not found by cid"
}
```

```json
{
  "error": "pin not found"
}
```

### Service Unavailable (503)

```json
{
  "error": "IPFS gateway not configured"
}
```

### Server Error (500)

```json
{
  "error": "database error"
}
```

### CORS

All endpoints support CORS preflight (`OPTIONS`) requests. The server responds with:

```
Access-Control-Allow-Origin: *
Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS, PATCH
Access-Control-Allow-Credentials: true
Access-Control-Max-Age: 86400
```

---

## Appendix: Gateways

Default gateway configuration (configurable via `PEERDRIVE_IPFS_GATEWAYS` environment variable):

| Gateway | URL |
|---------|-----|
| ipfs.io | `https://ipfs.io` |
| Cloudflare | `https://cloudflare-ipfs.com` |
| dweb.link | `https://dweb.link` |

Each gateway is tried concurrently when fetching a CID. The first successful response is used. Each gateway attempt includes up to 3 retries with exponential backoff (500ms base, 5s max, 25% jitter). The total per-gateway timeout is 30 seconds.
