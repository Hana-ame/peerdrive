# Peerdrive Test Matrix

> 2026-04-28 · Test logic and expected behavior covering all functionality points

---

## 1. File System

### 1.1 File Upload

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| F-01 | Upload small file | `curl -F "file=@test.txt" /files/upload` | 200, returns hash+size+mime | Check hash is 64 hex chars |
| F-02 | Upload without file | POST /files/upload without file | 400 "file is required" | HTTP 400 |
| F-03 | Upload large file (exceeds limit) | Upload >100MB file | 413/400 rejected | Authenticated user 100MB, anonymous 10MB |
| F-04 | Duplicate upload same content | Upload same file twice | 200, same hash, already_exists=true | Hash consistent |
| F-05 | Upload with special character filename | `file=@/tmp/hello world.txt` | 200, filename preserves original characters | URL encoding handled correctly |

### 1.2 Local File Registration

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| F-06 | Register existing file | POST /files/register_local {path:"/etc/hostname"} | 200, returns hash | SHA256 downloadable |
| F-07 | Register nonexistent path | {path:"/nonexistent/file"} | 400/500 error | Error message clear |
| F-08 | Register with custom filename | {path:"/etc/hostname", filename:"my.txt"} | filename="my.txt" | Custom name takes effect |
| F-09 | Duplicate registration same file | Register same path twice | Same hash, no duplicate DB insert | file_meta has only one record |
| F-10 | Path traversal protection | {path:"../etc/passwd"} | 400 rejected | `../` not allowed |

### 1.3 Folder Registration

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| F-11 | Register non-empty folder | {folder_path:"/etc/ssl"} | 200, registered array non-empty | count > 0 |
| F-12 | Register empty folder | {folder_path:"/tmp/empty"} | 200, count=0 | No error |
| F-13 | Register nonexistent folder | {folder_path:"/nonexistent"} | 400 error | Error message |
| F-14 | Partially registered | Register one file first, then register entire folder | Only new files registered | count less than total file count |

### 1.4 SHA256 Download

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| F-15 | Download existing file | GET /sha256sum/:hash | 200, returns file content | sha256sum verification matches |
| F-16 | Download nonexistent hash | GET /sha256sum/000...000 | 404 | Error message |
| F-17 | Range chunked download | Range: bytes=0-99 | 206, Content-Range header | Returns 100 bytes |
| F-18 | Range out of bounds | Range: bytes=999999- | 206, returns trailing portion | Correctly handles boundary |
| F-19 | Invalid hash format | GET /sha256sum/abc | 400 "invalid sha256" | No crash |

### 1.5 CID/IPFS Download

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| F-20 | CID download | GET /ipfs/bafkrei... | 200, X-CID header | Same content as SHA256 download |
| F-21 | Invalid CID | GET /ipfs/invalid | 404 | Error message |
| F-22 | IPFS gateway pull | No local file, enable IPFS gateway | Pull from ipfs.io | Auto-cache to local |

### 1.6 File Deletion

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| F-23 | Delete existing file | DELETE /files/:hash | 200 | Then GET /sha256sum/:hash → 404 |
| F-24 | Delete nonexistent hash | DELETE /files/000...000 | 200 (no error) | Idempotent operation |
| F-25 | Physical file not deleted | Check storage after deletion | File still on disk | Only DB record deleted |

---

## 2. Collection System

### 2.1 Anonymous Collection Creation

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| C-01 | Create collection | POST /anon/collections {entries:[{path,hash}], friendly_name, tags} | 200, returns hash | 64 hex chars |
| C-02 | Create empty entries collection | entries:[] | 400 rejected | At least one entry required |
| C-03 | Create invalid hash | entries:[{path:"a", hash:"xxx"}] | 400 rejected | Hash not 64 hex chars |
| C-04 | Path traversal | entries:[{path:"../etc", hash:valid}] | 400 rejected | `../` not allowed |
| C-05 | No name (AI recommended) | friendly_name left empty | Popup → AI recommend/empty | LLM returns name |
| C-06 | With tags | tags:["test","p2p"] | 200, tags saved in collection | GET collection confirms tags |

### 2.2 Collection Viewing

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| C-07 | View existing collection | GET /anon/collections/:hash | 200, returns entries/friendly_name/tags | entries non-empty |
| C-08 | View nonexistent collection | GET /anon/collections/000...000 | 404 | Error message |
| C-09 | Auto-delete empty collection | View collection with entry_count=0 | Auto deleteFile | Returns "Empty collection, auto-deleted" |
| C-10 | Single-file collection preview | View collection with 1 file | Image→img, PDF→iframe, text→pre | Preview renders correctly |
| C-11 | Nested collection link | Collection contains another collection's hash | Shows 📦 collection link | Click navigates to nested collection |

### 2.3 Collection Listing

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| C-12 | List collections | GET /anon/collections | 200, returns array | Contains hash/friendly_name/entry_count |
| C-13 | Empty list | No collections | Returns [] or dummy collection | No error |
| C-14 | Collection name priority | friendly_name→name_preview→hash→"Untitled" | Correct fallback chain | Shows "0 files" when entry_count=0 |

### 2.4 Collection Operations

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| C-15 | Fork collection | POST /anon/collections/fork {source_hash, ...} | 200, new hash | New collection contains source entries + new entries |
| C-16 | Version commit | POST /collections/:u/:c/commit {message} | 200, version_number++ | New version log entry |
| C-17 | Version rollback | POST /collections/:u/:c/rollback/:vid | 200, workspace restored to specified version | Entries restored |
| C-18 | Version history | GET /collections/:u/:c/log | 200, returns version list | Sorted by time descending |

---

## 3. P2P Network

### 3.1 libp2p Basics

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| P-01 | P2P startup | Start Peerdrive | enabled=true, peer_id non-empty | GET /p2p/status |
| P-02 | P2P disabled | PEERDRIVE_P2P_ENABLE=false | enabled=false, other functions normal | HTTP service still available |
| P-03 | Node info | GET /p2p/node | Returns peer_id + addrs | addrs non-empty |
| P-04 | Ping | GET /p2p/ping/:peer_id | Returns rtt | < 10ms (same machine) |
| P-05 | mDNS discovery | Two nodes on same LAN | discovered > 0 | GET /p2p/discovered |
| P-06 | Manual connect | POST /p2p/connect {addr} | status:"connected" | peers list contains the other node |

### 3.2 Exchange Protocol

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| P-07 | File request | POST /p2p/request-file {hash, peer_ids} | responses > 0, correct size | Returned data hash matches |
| P-08 | Cross-node download | Node A uploads, Node B downloads via P2P | 200, content matches | sha256sum same |
| P-09 | Collection sync | A creates collection, B syncs | synced > 0 | B can access synced files via SHA256 |

### 3.3 DHT

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| P-10 | IPFS Announce | POST /p2p/announce {hash} | status:"announced" | DHT Provide success |
| P-11 | IPFS Find | Find after announce | Should find in bootstrap environment | FindProviders returns peer |
| P-12 | Single node (isolated) | No bootstrap, Announce | 200 + warning | No 500 error, returns "announced locally" |

---

## 4. BitTorrent

### 4.1 BT DHT

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| B-01 | BT DHT startup | PEERDRIVE_BT_DHT_ENABLE=true | num_nodes > 0 | GET /bt/status |
| B-02 | BT DHT disabled | PEERDRIVE_BT_DHT_ENABLE=false | enabled:false | Other functions normal |
| B-03 | BT Announce | POST /bt/announce {hash} | status:"announced on BT DHT" | 64 hex char hash |
| B-04 | BT Find (cross-node) | A announces, B finds | count >= 1 | B finds peers announced by A |
| B-05 | BT Find (self-find) | Find own announced hash | count >= 0 | No error |
| B-06 | Invalid hash (BT) | 40-char hash | Correctly handled | Accepts 40-char infohash |

### 4.2 Torrent/Magnet

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| B-07 | Parse .torrent | ParseTorrent(bencode) | Returns name/pieces/size/infohash | infohash 20 bytes |
| B-08 | Parse Magnet | ParseMagnet("magnet:?xt=urn:btih:...") | Returns infohash/name/trackers | Supports hex and base32 |
| B-09 | Upload .torrent | POST /bt/torrent (multipart) | 200, returns files/infohash/status | status:"downloading" |
| B-10 | Add Magnet | POST /bt/magnet {uri} | 200, same as above | infohash correct |

### 4.3 Wire Protocol

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| B-11 | Handshake | TCP connect → send handshake | Both sides exchange infohash | Protocol "BitTorrent protocol" |
| B-12 | Piece download | Download single piece from seeder | SHA1 verification passes | Piece count correct |
| B-13 | Multi-piece download | Download multiple pieces | All SHA1 verifications pass | Reassembled file SHA256 correct |

### 4.4 BEP Standards

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| B-14 | BEP 44 Put | Store immutable data | Returns target hash | Data stored in DHT |
| B-15 | BEP 44 Get | Retrieve stored data | Returns original data | Base64 encoding consistent |
| B-16 | BEP 51 Sample | GET /bt/bep51/sample | Returns samples array | Each 40 hex chars |

---

## 5. Dual-Stack

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| D-01 | Dual Announce | POST /p2p/dual/announce {hash} | Both IPFS+BT succeed | status:"announced on both networks" |
| D-02 | Dual Find | POST /p2p/dual/find {hash} | Returns ipfs_peers + bt_peers | Both fields exist |
| D-03 | Single network disabled | BT disabled, Dual announce | IPFS succeeds, BT skips | No error |

---

## 6. Registration & Authentication

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| R-01 | User registration | POST /auth/register | 201, returns token | Token decodable |
| R-02 | User login | POST /auth/login | 200, returns JWT | JWT contains username+role |
| R-03 | Token verification | GET /auth/whoami (Bearer) | 200, returns username+role | Correctly identified |
| R-04 | Invalid token | Wrong/expired token | 401 | Error message |
| R-05 | Relay registration | POST /p2p/relay/register | 200 | Appears in relay list |
| R-06 | Relay heartbeat | POST /p2p/relay/heartbeat | 200 | last_heartbeat updated |
| R-07 | Relay list | GET /p2p/relay/list | 200, returns active relays | Heartbeat within 5 minutes |
| R-08 | endpoint#token | API URL contains #token | Extract token, used for Authorization | Frontend auto-parses |

---

## 7. Frontend

### 7.1 Page Loading

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-01 | Plaza homepage | Open / | Shows collection cards/search bar/tabs | Playwright |
| UI-02 | AnonCreator | Open /anon/create | 4-tab layout, timeline default | Timeline/registered/local/collection visible |
| UI-03 | FileManager | Open /files | File list+checkboxes+sorting | Checkboxes visible, sort buttons functional |
| UI-04 | P2P panel | Open /ipfs, /bt, /p2p | Each panel renders correctly | Playwright |
| UI-05 | BT Controller | Open /bt/controller | Magnet input+download list+stats bar | Refresh button functional |
| UI-06 | Settings | Open /settings | IPFS/BT/WebDAV/LLM config sections | Settings persisted |
| UI-07 | PWA | Open on mobile | manifest + service worker | Can add to home screen |

### 7.2 Interactions

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-08 | Paste SHA256 navigate | Paste 64-char hash in Plaza search bar | Auto-navigate to collection page | URL becomes /anon/collections/:hash |
| UI-09 | Drag file | Drag file to AnonCreator edit area | Add entry to FileTree | Entry visible |
| UI-10 | Checkbox multi-select | Check multiple files in FileManager | Shows "N files selected" | Create collection button available |
| UI-11 | Share input box | Click share | Shows input box (not alert) | Can copy link |
| UI-12 | Antenna tab | Switch to antenna tab | Pull P2P discovery | Collection cards appear |
| UI-13 | LLM context | Use LLM after switching pages | LLM knows current page | Response relevant |

### 7.3 Error Handling

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-14 | No node banner | API not configured, open BT controller | Shows "Not connected to local node" banner | Orange warning |
| UI-15 | Error tooltip | BT task error, mouse hover | Shows error details | title attribute |
| UI-16 | Empty collection hidden | Plaza empty collection (0 entries) | Hidden or marked | At least not "Untitled Collection" |

---

## 8. Deployment & Operations

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| O-01 | Single binary build | go build ./cmd/server/ | 51MB binary | file peerdrive-server |
| O-02 | Cross-platform build | GOOS=windows/darwin/linux | All platforms succeed | CI matrix |
| O-03 | VPS relay startup | systemctl start peerdrive-relay | active, P2P online | curl /ping |
| O-04 | CF Tunnel | curl wsl-3000.moonchan.xyz/ping | pong | TLS connection |
| O-05 | CF Pages | curl peerdrive.pages.dev | HTTP 200 | Frontend accessible |
| O-06 | Docker compose up | docker compose up -d | 5 containers running | relay healthcheck pass |
| O-07 | Memory usage | After long-running | < 100MB (Go process) | ps aux RSS |

---

## 10. Real Network Tests (2026-04-28)

### I-01 IPFS Real Node Interoperability
| Item | Details |
|------|---------|
| Objective | Peerdrive pulls file from public IPFS CID |
| Steps | Known CID `QmUNLLsP...` → POST /ipfs/pin → GET /ipfs/ |
| Result | ✅ HTTP 200, 249,154 bytes, pin succeeded |
| Verification | File written to local storage, SHA256 index registered |

### I-02 BT Real Seeder Test
| Item | Details |
|------|---------|
| Objective | Peerdrive BT client discovers peers via Tracker+DHT and downloads |
| Steps | .torrent → POST /bt/torrent → Tracker discovery → Wire Protocol |
| Result | ✅ 2026-04-29: 16/16 pieces, 1,048,576 bytes, SHA256 verification passed |
| Details | Peerdrive ←Tracker→ Python seeder → Wire Protocol 16-piece download |
| Verification | sha256sum matches original file exactly: `39b90efc...` |

### I-04 BT Global DHT Connectivity (NEW)
| Item | Details |
|------|---------|
| Objective | Peerdrive BT DHT connects to global Mainline DHT network |
| Result | ✅ DHT 13+ nodes, discovered Transmission client (-TR2210-) |
| Handshake | ✅ BT wire protocol handshake successful |
| Date | 2026-04-29 |

### I-05 P2P Collection Cross-Server Sync (NEW)
| Item | Details |
|------|---------|
| Objective | BWH creates collection → P2P announce → WSL discovers and pulls |
| Operation | BWH: upload → create → dual announce; WSL: dual find → fetch → save → download |
| Nodes | BWH (relay server) ↔ WSL (relay client) |
| Result | ✅ Collection data fully synced, file content end-to-end consistent |
| Date | 2026-05-03 |

### I-03 IPFS Local Node Interoperability
| Item | Details |
|------|---------|
| Objective | kubok add → CID → Peerdrive pin |
| Steps | ipfs add → Peerdrive POST /ipfs/pin/:cid |
| Result | ⚠️ Local kubo node files not published to IPFS DHT, public gateway cannot find |
| Fix | Need kubo connected to IPFS public network or Peerdrive directly connected to kubo's libp2p node |

---

## 11. Frontend UI Mobile Adaptation

### 11.1 Create Page (AnonCreator)

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-01 | Mobile 3-panel toggle | Open /create at 375px viewport | Shows "Files", "Preview", "Editor" tabs | Only one panel visible at a time |
| UI-02 | Desktop 3-panel layout | Open /create at >768px viewport | Left-center-right 3 panels visible simultaneously | md:flex takes effect |
| UI-03 | Select file auto-switch preview | Click file on left side | Auto-switch to preview panel | mobilePanel → 'preview' |
| UI-04 | Local PC file add | Local PC tab → file "+" | Auto-register file then add to collection | Entry has hash/provider |
| UI-05 | Local PC folder add | Local PC tab → folder "+" | Expand folder, register files then add, empty folders ignored | Entry prefixed with foldername/ |
| UI-06 | Save button disabled/enabled | No entries/has entries | disabled=true when no entries | Button disabled attribute |
| UI-07 | Tags on independent line | Editor toolbar | Tag input on independent line below name | DOM structure verification |
| UI-08 | Inline new folder | Click "+ New Folder" | Inline input appears in file tree | Enter confirms, Escape cancels |
| UI-09 | Folder right-click rename | Right-click folder → Rename | Enters inline edit mode | path + '/' passed to rename |

### 11.2 Settings Page (Settings)

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-10 | Mobile tab bar | Open /settings at 375px viewport | Horizontally scrollable tab bar visible | Node connection/auth/storage management etc. |
| UI-11 | Desktop sidebar | 375px viewport | Left nav aside not visible | hidden md:flex |
| UI-12 | LLM panel width adaptive | Open LLM panel at 375px viewport | w-[calc(100vw-1.5rem)] and no overflow | Panel within viewport |

### 11.3 Navbar

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-13 | Hamburger menu button | Any page at 375px viewport | Hamburger button visible top-left | aria-label="Menu" |
| UI-14 | Hamburger menu expand | Click hamburger button | Dropdown menu shows navigation items | Includes local file management, create collection etc. |
| UI-15 | Desktop dropdown menu | Hover P2P/BT/IPFS at >768px viewport | Dropdown menu expands | Submenu items visible |
| UI-16 | Touch dropdown menu | Click dropdown arrow button | Menu toggle expands/collapses | Click outside to close |
| UI-17 | Search button compact | 375px viewport | Only 🔍 icon shown, text and shortcut hidden | No "Search..." text on mobile |

### 11.4 Collection Explorer (Explorer/AnonExplorer)

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-18 | Explorer VersionLog hidden | Open /:user/:coll at 375px viewport | Right-side VersionLog panel not visible | hidden md:block |
| UI-19 | Explorer top bar compact | 375px viewport | Button text shortened, no overflow | "Save" instead of "Save to Local" |

### 11.5 Others

| Test ID | Test Item | Operation | Expected Result | Verification |
|---------|-----------|-----------|-----------------|--------------|
| UI-20 | DHT input adaptive | 375px viewport | Input box min-w=0, buttons auto-wrap | flex-wrap takes effect |
| UI-21 | LLM panel viewport adaptation | Open LLM at 375px viewport | Panel does not overflow screen right edge | w-[calc(100vw-1.5rem)] |

---

## Appendix A: Test Environment

| Item | Value |
|------|-------|
| Playwright browser | Edge via CDP (port 9222) |
| Mobile viewport | 375×667 (iPhone SE) |
| Desktop breakpoint | md: 768px |
| Test URL | peerdrive.pages.dev (production) or *.peerdrive.pages.dev (preview) |
| Network limitation | Host browser cannot directly access WSL2 localhost, must use *.moonchan.xyz or deployed cloudflare pages |

## Appendix B: How to Run

```bash
# Go unit tests
cd back && go test -tags nosqlite ./internal/service/ -run "TestCreateCollection" -v

# Frontend unit tests
cd front && npx vitest run

# Playwright mobile tests (requires host Edge on 9222)
cd /home/lumin/.claude/skills/playwright-test
node scripts/test-runner.mjs /tmp/pw-mobile-final.mjs

# Frontend build
cd front && npx vite build
```
