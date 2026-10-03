# Peerdrive Test Matrix

> 2026-04-28 · Test logic and expected behavior covering all feature points

---

## 1. File system

### 1.1 File upload

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| F-01 | Upload small file | `curl -F "file=@test.txt" /files/upload` | 200, returns hash+size+mime | Check hash is 64-hex |
| F-02 | Upload without file | POST /files/upload without file | 400 "file is required" | HTTP 400 |
| F-03 | Upload large file (over limit) | Upload >100MB file | 413/400 rejected | Auth user 100MB, anon 10MB |
| F-04 | Duplicate upload same content | Upload the same file twice | 200, same hash, already_exists=true | Hash matches |
| F-05 | Upload with special-char filename | `file=@/tmp/hello world.txt` | 200, filename keeps original chars | URL encoding handled correctly |

### 1.2 Local file registration

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| F-06 | Register existing file | POST /files/register_local {path:"/etc/hostname"} | 200, returns hash | SHA256 downloadable |
| F-07 | Register nonexistent path | {path:"/nonexistent/file"} | 400/500 error | Error message clear |
| F-08 | Register with custom filename | {path:"/etc/hostname", filename:"my.txt"} | filename="my.txt" | Custom name takes effect |
| F-09 | Duplicate register same file | Register same path twice | Same hash, no duplicate DB insert | file_meta has one row |
| F-10 | Path traversal protection | {path:"../etc/passwd"} | 400 rejected | No ../ allowed |

### 1.3 Folder registration

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| F-11 | Register non-empty folder | {folder_path:"/etc/ssl"} | 200, registered array non-empty | count > 0 |
| F-12 | Register empty folder | {folder_path:"/tmp/empty"} | 200, count=0 | No error |
| F-13 | Register nonexistent folder | {folder_path:"/nonexistent"} | 400 error | Error message |
| F-14 | Partially registered | First register one file, then the whole folder | Only new files registered | count less than total files |

### 1.4 SHA256 download

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| F-15 | Download existing file | GET /sha256sum/:hash | 200, returns file content | sha256sum matches |
| F-16 | Download nonexistent hash | GET /sha256sum/000...000 | 404 | Error message |
| F-17 | Range chunk download | Range: bytes=0-99 | 206, Content-Range header | Returns 100 bytes |
| F-18 | Range out of bounds | Range: bytes=999999- | 206, returns tail portion | Boundary handled correctly |
| F-19 | Invalid hash format | GET /sha256sum/abc | 400 "invalid sha256" | Doesn't crash |

### 1.5 CID/IPFS download

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| F-20 | CID download | GET /ipfs/bafkrei... | 200, X-CID header | Same content as SHA256 download |
| F-21 | Invalid CID | GET /ipfs/invalid | 404 | Error message |
| F-22 | IPFS gateway fetch | File not local, IPFS gateway enabled | Fetched from ipfs.io | Auto-cached locally |

### 1.6 File deletion

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| F-23 | Delete existing file | DELETE /files/:hash | 200 | Subsequent GET /sha256sum/:hash → 404 |
| F-24 | Delete nonexistent hash | DELETE /files/000...000 | 200 (no error) | Idempotent operation |
| F-25 | Physical file not deleted | Check storage after delete | File still on disk | Only DB record removed |

---

## 2. Collection system

### 2.1 Anonymous collection creation

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| C-01 | Create collection | POST /anon/collections {entries:[{path,hash}], friendly_name, tags} | 200, returns hash | 64-hex |
| C-02 | Create empty-entry collection | entries:[] | 400 rejected | At least one entry |
| C-03 | Create invalid hash | entries:[{path:"a", hash:"xxx"}] | 400 rejected | Hash not 64-hex |
| C-04 | Path traversal | entries:[{path:"../etc", hash:valid}] | 400 rejected | No ../ allowed |
| C-05 | No name (AI suggest) | friendly_name empty | Dialog→AI suggest/leave blank | LLM returns a name |
| C-06 | With tags | tags:["test","p2p"] | 200, tags saved in collection | GET collection confirms tags |

### 2.2 Collection viewing

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| C-07 | View existing collection | GET /anon/collections/:hash | 200, returns entries/friendly_name/tags | entries non-empty |
| C-08 | View nonexistent collection | GET /anon/collections/000...000 | 404 | Error message |
| C-09 | Empty collection auto-delete | View collection with entry_count=0 | Auto deleteFile | Returns "empty collection, auto-deleted" |
| C-10 | Single-file collection preview | View a 1-file collection | Image→img, PDF→iframe, text→pre | Preview renders correctly |
| C-11 | Nested collection link | Collection contains another collection's hash | Shows 📦 collection link | Click jumps to nested collection |

### 2.3 Collection listing

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| C-12 | List collections | GET /anon/collections | 200, returns array | Contains hash/friendly_name/entry_count |
| C-13 | Empty list | No collections | Returns [] or dummy collections | No error |
| C-14 | Collection name priority | friendly_name→name_preview→hash→"Untitled" | Correct fallback chain | entry_count=0 shows "0 files" |

### 2.4 Collection operations

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| C-15 | Fork collection | POST /anon/collections/fork {source_hash, ...} | 200, new hash | New collection contains source entries + new entries |
| C-16 | Version commit | POST /collections/:u/:c/commit {message} | 200, version_number++ | Version log adds one entry |
| C-17 | Version rollback | POST /collections/:u/:c/rollback/:vid | 200, workspace restored to that version | Entries restored |
| C-18 | Version history | GET /collections/:u/:c/log | 200, returns version list | Reverse chronological |

---

## 3. P2P network

### 3.1 libp2p basics

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| P-01 | P2P startup | Start Peerdrive | enabled=true, peer_id non-empty | GET /p2p/status |
| P-02 | P2P disabled | PEERDRIVE_P2P_ENABLE=false | enabled=false, other features normal | HTTP service still usable |
| P-03 | Node info | GET /p2p/node | Returns peer_id + addrs | addrs non-empty |
| P-04 | Ping | GET /p2p/ping/:peer_id | Returns rtt | < 10ms (same machine) |
| P-05 | mDNS discovery | Two nodes on same LAN | discovered > 0 | GET /p2p/discovered |
| P-06 | Manual connect | POST /p2p/connect {addr} | status:"connected" | peers list contains the other |

### 3.2 Exchange protocol

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| P-07 | File request | POST /p2p/request-file {hash, peer_ids} | responses > 0, size correct | Returned data hash matches |
| P-08 | Cross-node download | Node A uploads, node B downloads via P2P | 200, content identical | sha256sum same |
| P-09 | Collection sync | A creates collection, B syncs | synced > 0 | B can access synced files via SHA256 |

### 3.3 DHT

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| P-10 | IPFS Announce | POST /p2p/announce {hash} | status:"announced" | DHT Provide succeeds |
| P-11 | IPFS Find | Find after announce | Should find in bootstrap environment | FindProviders returns peer |
| P-12 | Single node (isolated) | No bootstrap, Announce | 200 + warning | No 500, returns "announced locally" |

---

## 4. BitTorrent

### 4.1 BT DHT

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| B-01 | BT DHT startup | PEERDRIVE_BT_DHT_ENABLE=true | num_nodes > 0 | GET /bt/status |
| B-02 | BT DHT disabled | PEERDRIVE_BT_DHT_ENABLE=false | enabled:false | Other features normal |
| B-03 | BT Announce | POST /bt/announce {hash} | status:"announced on BT DHT" | 64-hex hash |
| B-04 | BT Find (cross-node) | A announces, B finds | count >= 1 | B finds A's announced peer |
| B-05 | BT Find (self-lookup) | Find own announced hash | count >= 0 | No error |
| B-06 | Invalid hash (BT) | 40-char hash | Handled correctly | Accepts 40-char infohash |

### 4.2 Torrent/Magnet

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| B-07 | Parse .torrent | ParseTorrent(bencode) | Returns name/pieces/size/infohash | infohash 20 bytes |
| B-08 | Parse Magnet | ParseMagnet("magnet:?xt=urn:btih:...") | Returns infohash/name/trackers | Supports hex and base32 |
| B-09 | Upload .torrent | POST /bt/torrent (multipart) | 200, returns files/infohash/status | status:"downloading" |
| B-10 | Add Magnet | POST /bt/magnet {uri} | 200, same as above | infohash correct |

### 4.3 Wire Protocol

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| B-11 | Handshake | TCP connect → send handshake | Both sides exchange infohash | Protocol "BitTorrent protocol" |
| B-12 | Piece download | Download single piece from seeder | SHA1 verified | Data correct |
| B-13 | Multi-piece download | Download multiple pieces | All SHA1 verified | File reassembled with correct SHA256 |

### 4.4 BEP standards

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| B-14 | BEP 44 Put | Store immutable data | Returns target hash | Data stored in DHT |
| B-15 | BEP 44 Get | Retrieve stored data | Returns original data | base64 encoding matches |
| B-16 | BEP 51 Sample | GET /bt/bep51/sample | Returns samples array | Each 40-hex |

---

## 5. Dual-Stack

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| D-01 | Dual Announce | POST /p2p/dual/announce {hash} | IPFS+BT both succeed | status:"announced on both networks" |
| D-02 | Dual Find | POST /p2p/dual/find {hash} | Returns ipfs_peers + bt_peers | Both fields present |
| D-03 | Single-net disabled | BT disabled, Dual announce | IPFS succeeds, BT skips | No error |

---

## 6. Registration and authentication

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| R-01 | User register | POST /auth/register | 201, returns token | Token decodable |
| R-02 | User login | POST /auth/login | 200, returns JWT | JWT contains username+role |
| R-03 | Token verify | GET /auth/whoami (Bearer) | 200, returns username+role | Correctly identified |
| R-04 | Invalid Token | Bad/expired token | 401 | Error message |
| R-05 | Relay register | POST /p2p/relay/register | 200 | Appears in relay list |
| R-06 | Relay heartbeat | POST /p2p/relay/heartbeat | 200 | last_heartbeat updated |
| R-07 | Relay list | GET /p2p/relay/list | 200, returns active relays | Heartbeats within 5 min |
| R-08 | endpoint#token | API URL contains #token | Token extracted, used in Authorization | Frontend auto-parses |

---

## 7. Frontend

### 7.1 Page load

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| UI-01 | Plaza home | Open / | Shows collection cards/search bar/tabs | Playwright |
| UI-02 | AnonCreator | Open /anon/create | 4-tab flat layout, timeline default | Timeline/Registered/Local/Collection visible |
| UI-03 | FileManager | Open /files | File list + checkboxes + sort | Checkboxes visible, sort button works |
| UI-04 | P2P panels | Open /ipfs, /bt, /p2p | Each panel renders correctly | Playwright |
| UI-05 | BT controller | Open /bt/controller | Magnet input + download list + stats bar | Refresh button works |
| UI-06 | Settings | Open /settings | IPFS/BT/WebDAV/LLM config sections | Settings persist |
| UI-07 | PWA | Open on mobile | manifest + service worker | Can add to home screen |

### 7.2 Interactions

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| UI-08 | Paste SHA256 to navigate | Paste 64-hex hash in Plaza search bar | Auto-navigates to collection page | URL becomes /anon/collections/:hash |
| UI-09 | Drag & drop file | Drag file onto AnonCreator edit area | Adds entry to FileTree | Entry visible |
| UI-10 | Checkbox multi-select | Check multiple files in FileManager | Shows "N files selected" | Create-collection button enabled |
| UI-11 | Share input box | Click share | Shows input box (not alert) | Can copy link |
| UI-12 | Antenna | Switch to antenna tab | Pulls P2P discovery | Collection cards appear |
| UI-13 | LLM context | Use LLM after switching pages | LLM knows current page | Reply relevant |

### 7.3 Error handling

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| UI-14 | No-node banner | API not configured, open BT controller | Shows "Not connected to local node" banner | Orange warning |
| UI-15 | Error tooltip | BT task error, hover mouse | Shows error details | title attribute |
| UI-16 | Empty collection hidden | Plaza empty collection (0 entries) | Not shown or marked | At least not "Untitled collection" |

---

## 8. Deployment and operations

| Test ID | Item | Operation | Expected result | Verification |
|--------|--------|------|----------|----------|
| O-01 | Single-binary build | go build ./cmd/server/ | 51MB binary | file peerdrive-server |
| O-02 | Cross-platform build | GOOS=windows/darwin/linux | All succeed | CI matrix |
| O-03 | VPS relay startup | systemctl start peerdrive-relay | active, P2P online | curl /ping |
| O-04 | CF Tunnel | curl wsl-3000.moonchan.xyz/ping | pong | TLS connection |
| O-05 | CF Pages | curl peerdrive.pages.dev | HTTP 200 | Frontend accessible |
| O-06 | Docker compose up | docker compose up -d | 5 containers running | relay healthcheck passes |
| O-07 | Memory usage | After long run | < 100MB (Go process) | ps aux RSS |
