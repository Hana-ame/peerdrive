# Peerdrive API Usage Manual

> Organized by functional module; each module describes API call order, purpose, and call conditions.

## Table of Contents

1. [Authentication & Identity](#1-authentication--identity)
2. [File Management](#2-file-management)
3. [Collection System](#3-collection-system)
4. [P2P Network](#4-p2p-network)
5. [BT DHT](#5-bt-dht)
6. [IPFS](#6-ipfs)
7. [Relay](#7-relay)
8. [Comment System](#8-comment-system)
9. [WebDAV](#9-webdav)
10. [LLM Assistant](#10-llm-assistant)

---

## 1. Authentication & Identity

### 1.1 Register User

```
POST /auth/register  →  Get JWT token
```

**Call condition**: Unregistered user
**Purpose**: Create account and get identity credentials. JWT is used for all subsequent authenticated requests.

```bash
curl -X POST http://localhost:4000/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"secret123"}'
# → {"token":"eyJ...", "username":"alice", "role":"user"}
```

### 1.2 Login

```
POST /auth/login  →  Get JWT token
```

**Call condition**: Registered user, re-fetch after token expiry
**Purpose**: Get new identity credentials (72-hour validity)

### 1.3 Verify Identity

```
GET /auth/whoami
Authorization: Bearer <token>
```

**Call condition**: Need to verify current token validity
**Purpose**: Returns username and role; Peerdrive Node uses this endpoint to verify user tokens

### 1.4 Register Node Identity

```
POST /node/register  →  Bind node to user account
Authorization: Bearer <token>
{"token":"<jwt>"}
```

**Call condition**: User already has JWT and wants to bind this Peerdrive Node to their account
**Purpose**:
- Node calls central server to verify token → get username → register node↔user binding with central server
- Other nodes can query "who operates this peer" through the central server

**Call order**:
1. First register user on central server (`POST /auth/register`)
2. After getting JWT, call `POST /node/register` on Peerdrive Node
3. Node contacts hardcoded central server to complete registration

```bash
# Execute on Peerdrive Node
curl -X POST http://<node>:<port>/node/register \
  -H 'Content-Type: application/json' \
  -d '{"token":"eyJ..."}'
# → {"status":"registered", "username":"alice", "peer_id":"12D3Koo..."}
```

### 1.5 Query Node Operator

```
GET /node/operator
```

**Call condition**: Any node (no auth)
**Purpose**: Query who operates this Peerdrive Node. Anonymous nodes return `null`.

```bash
curl http://<node>:<port>/node/operator
# Registered → {"operator":"alice"}
# Anonymous  → {"operator":null, "note":"anonymous node"}
```

### 1.6 Query Service Policy

```
GET /auth/service-policy/:username
Authorization: Bearer <token>
```

**Call condition**: Authenticated user needing to determine if a user has relay/P2P permissions
**Purpose**: Before providing service to a peer node, Peerdrive Node queries whether the other party is allowed to use relay/P2P

### 1.7 Set Service Policy

```
POST /auth/service-policy/:username
Authorization: Bearer <token>
{"allow_relay":true, "allow_p2p":false, "notes":"reason"}
```

**Call condition**: Administrator or user themselves
**Purpose**: Control whether a user can use relay/P2P services

---

## 2. File Management

### 2.1 Upload File

```
POST /files/upload
Content-Type: multipart/form-data
```

**Call condition**: Need to add new file to Peerdrive storage
**Purpose**: Upload file, compute SHA256, store in content-addressed storage. Returns hash for subsequent use.

**Upload size limits**: Authenticated users 100MB, anonymous 10MB

### 2.2 Register Local File

```
POST /files/register_local
{"path":"/home/user/file.txt", "filename":"file.txt"}
```

**Call condition**: File already exists on server's local disk, no need to upload again
**Purpose**: Directly bring local file under Peerdrive management, compute SHA256 and register metadata

### 2.3 Register Folder (Recursive)

```
POST /files/register_folder
{"folder_path":"/home/user/docs"}
```

**Call condition**: Need to bulk register an entire directory
**Purpose**: Recursively scan and register all files in the directory

### 2.4 Browse Files

```
GET /files/browse?path=/home/user
```

**Call condition**: Need to view server filesystem
**Purpose**: Browse directory contents, select files to register

### 2.5 Verify File

```
GET /files/verify/:hash
```

**Call condition**: Need to verify file integrity
**Purpose**: Check if file exists and content matches hash

### 2.6 Download File (SHA256)

```
GET /sha256sum/:hash
```

**Call condition**: Known file SHA256 hash
**Purpose**: Download file through content addressing, supports Range chunked download

### 2.7 Delete File

```
DELETE /files/:hash
```

**Call condition**: Need to remove file from Peerdrive storage
**Purpose**: Delete file metadata and storage

---

## 3. Collection System

Collections are logical groupings of files, supporting version management and P2P sharing.

### 3.1 Create Anonymous Collection

```
POST /anon/collections
{"title":"My Collection", "description":"..."}
```

**Call condition**: Need to create a shareable collection (no login required)
**Purpose**: Create an anonymous collection, return `anonymousHash`

### 3.2 Read Anonymous Collection

```
GET /anon/collections/:hash
```

**Call condition**: Known anonymous collection hash
**Purpose**: Get collection metadata and file list

### 3.3 Download from Anonymous Collection

```
GET /anon/collections/:hash/:filepath
```

**Call condition**: Need to download specific file from anonymous collection
**Purpose**: Download the file; supports range requests

### 3.4 Add File to Anonymous Collection

```
POST /anon/collections/:hash/entries
{"filename":"photo.jpg", "hash":"<sha256>", "filepath":"photos/photo.jpg"}
```

**Call condition**: File already uploaded to server, need to add to collection
**Purpose**: Add file entry to anonymous collection

### 3.5 Commit Anonymous Collection

```
POST /anon/collections/commit
{"anonymousHash":"...", "title":"...", "files":[...]}
```

**Call condition**: Ready to publish anonymous collection
**Purpose**: Finalize and commit the anonymous collection

### 3.6 Create User Collection

```
POST /collections
Authorization: Bearer <token>
{"name":"My Project", "description":"..."}
```

**Call condition**: Registered user wants to create personal collection
**Purpose**: Create a user-owned collection

### 3.7 Upload to Collection

```
POST /collections/:user/:name/entries
Content-Type: multipart/form-data
```

**Call condition**: Need to add files to user collection
**Purpose**: Upload and index file into collection

### 3.8 Commit Collection

```
POST /collections/:user/:name/commit
Authorization: Bearer <token>
{"message":"update v2"}
```

**Call condition**: Files uploaded, need to commit changes
**Purpose**: Create a new version snapshot for the collection

### 3.9 Merge Collections

```
POST /collections/:user/:name/merge
Authorization: Bearer <token>
{"source":"alice:other-collection", "strategy":"local"}
```

**Call condition**: Need to merge another collection's changes into current collection
**Purpose**: Three-way merge, supports conflict resolution strategies (local/remote)

### 3.10 Fork Collection

```
POST /collections/:user/:name/fork
Authorization: Bearer <token>
```

**Call condition**: Need to fork another user's collection
**Purpose**: Create a complete copy of another user's collection under your own account

---

## 4. P2P Network

### 4.1 Get Node Info

```
GET /p2p/node
```

**Call condition**: Need to check local node status
**Purpose**: Returns peer ID, listen addresses, online status

### 4.2 Connect to Peer

```
POST /p2p/connect
{"peer_id":"Qm..."}
```

**Call condition**: Need to establish P2P connection with specified node
**Purpose**: Initiate WebRTC connection to specified peer

### 4.3 Get P2P Status

```
GET /p2p/status
```

**Call condition**: Need to check P2P network overall status
**Purpose**: Returns active connections, peers, transfer statistics

### 4.4 WebRTC Signaling

```
POST /signal/offer
POST /signal/answer
POST /signal/ice
```

**Call condition**: During WebRTC connection establishment
**Purpose**: Exchange SDP offers/answers and ICE candidates

### 4.5 P2P Download

```
GET /p2p/download/:peer/:hash
```

**Call condition**: Need to download file from remote peer
**Purpose**: P2P download file, returns range chunks

### 4.6 Multi-Peer Download

```
POST /p2p/multipeer/download
{"hash":"<sha256>", "peers":["Qm1","Qm2","Qm3"]}
```

**Call condition**: Multiple peers have the same file, want parallel chunk download
**Purpose**: Download file from multiple peers simultaneously

---

## 5. BT DHT

### 5.1 Get BT Status

```
GET /bt/status
```

**Call condition**: Check if BT DHT is enabled and routing table size
**Purpose**: Returns DHT server info, node count

### 5.2 Announce on BT DHT

```
POST /bt/announce
{"hash":"<sha256>"}
```

**Call condition**: Want to announce a file hash on BT DHT for discovery
**Purpose**: Announce hash to BT DHT network

### 5.3 Find on BT DHT

```
POST /bt/find
{"hash":"<sha256>"}
```

**Call condition**: Want to find who has a specific hash on BT DHT
**Purpose**: Search BT DHT for peers with the hash

### 5.4 BEP 44 Put

```
POST /bt/bep44/put
{"data":"<base64>", "mutable":false}
```

**Call condition**: Need to store a key-value pair on BT DHT (BEP 44)
**Purpose**: Write data to BT DHT, return target hash

### 5.5 BEP 44 Get

```
POST /bt/bep44/get
{"target":"<hex-hash>"}
```

**Call condition**: Need to retrieve data previously stored with BEP 44
**Purpose**: Read data from BT DHT

### 5.6 BEP 51 Sample

```
GET /bt/bep51/sample
```

**Call condition**: Want to sample infohashes from DHT network
**Purpose**: Return infohash samples (BEP 51)

---

## 6. IPFS

### 6.1 Announce on IPFS

```
POST /p2p/announce
{"hash":"<sha256>"}
```

**Call condition**: Want to announce file on IPFS DHT
**Purpose**: IPFS DHT provide

### 6.2 Find on IPFS

```
POST /p2p/find
{"hash":"<sha256>"}
```

**Call condition**: Want to find providers of a file on IPFS
**Purpose**: IPFS DHT findProviders

### 6.3 CID Conversion

```
GET /p2p/cid?hash=<sha256>
```

**Call condition**: Need to convert SHA256 to IPFS CID
**Purpose**: Return IPFS CID (v1)

---

## 7. Relay

### 7.1 Relay Status

```
GET /relay/status
```

**Call condition**: Check relay service status
**Purpose**: Returns whether this node acts as a relay

### 7.2 Register Relay

```
POST /relay/register
```

**Call condition**: Want to register this node as a relay
**Purpose**: Start accepting relay traffic

### 7.3 Unregister Relay

```
DELETE /relay/register
```

**Call condition**: Want to stop being a relay
**Purpose**: Stop accepting relay traffic

---

## 8. Comment System

### 8.1 Get Comments

```
GET /collections/:user/:name/comments
Authorization: Bearer <token>
```

**Call condition**: Need to view comments on a collection
**Purpose**: Return comment list

### 8.2 Add Comment

```
POST /collections/:user/:name/comments
Authorization: Bearer <token>
{"text":"Great collection!"}
```

**Call condition**: Want to comment on a collection
**Purpose**: Add a new comment

### 8.3 Delete Comment

```
DELETE /collections/:user/:name/comments/:commentId
Authorization: Bearer <token>
```

**Call condition**: Need to delete a comment
**Purpose**: Delete the comment

---

## 9. WebDAV

### 9.1 WebDAV Root

```
GET /webdav/
Authorization: Bearer <token>
```

**Call condition**: Connect to Peerdrive via WebDAV client
**Purpose**: Browse collections as WebDAV directory

### 9.2 WebDAV Collection

```
GET /webdav/:user/:name/
Authorization: Bearer <token>
```

**Call condition**: Browse specific collection via WebDAV
**Purpose**: List files in collection

### 9.3 WebDAV File

```
GET /webdav/:user/:name/:filepath
Authorization: Bearer <token>
```

**Call condition**: Download file via WebDAV
**Purpose**: Stream file content

---

## 10. LLM Assistant

### 10.1 Configure LLM

LLM assistant configuration stored in localStorage:
- `peerdrive_llm_endpoint`: LLM API endpoint
- `peerdrive_llm_model`: Model name (default `Qwen/Qwen3-8B`)
- `peerdrive_llm_apikey`: API Key (optional)

### 10.2 Available Tools (Function Calling)

LLM assistant has 17 available tools:

| Tool | Called API | Purpose |
|------|-----------|------|
| `get_node_info` | `GET /ping`, `GET /p2p/node` | Node status |
| `list_collections` | `GET /anon/collections` | View collections |
| `create_anon_collection` | `POST /anon/collections` | Create collection |
| `register_local_file` | `POST /files/register_local` | Register file |
| `add_file_to_collection` | `POST /collections/.../entries` | Add file to collection |
| `commit_collection` | `POST /collections/.../commit` | Commit collection version |
| `get_p2p_status` | `GET /p2p/status` | P2P network status |

**Usage conditions**: LLM endpoint properly configured and can access Peerdrive API

---

## Quick Reference: Authentication Header

All authenticated requests include:
```
Authorization: Bearer <jwt_token>
```

Anonymous requests do not include this header and access public endpoints as anonymous users.
