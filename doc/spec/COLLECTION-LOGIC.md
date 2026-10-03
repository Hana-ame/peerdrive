# Peerdrive Collection Logic — Full Trace

> 2026-04-28 · Review all detail issues

## 1. Collection Creation

### 1.1 AnonCreator → POST /anon/collections

```
User drags files → entries[] → Click save → POST /anon/collections
  body: {entries: [{path, hash}], friendly_name, tags}
  handler: controller.CreateAnonCollection
    → anonSvc.CreateCollection(name, entries, tags)
      → Validate: reject empty path, path with ../, non-64-char hash
      → model.NewAnonCollection(name, entries, tags)
        → version=1, CreatedAt=now, compute name_preview (first 3 filenames)
      → JSON serialize → SHA256 → write to storage/<hash[:2]>/<hash>
      → Return {hash, name_preview, entry_count}
```

### 1.2 Broadcast Button (Plaza)

```
Search bar input hash → Click 📡 broadcast → 
  → api.createAnonCollection([{path:'broadcast',hash}], 'Broadcast '+hash prefix)
  → api.dualAnnounce(hash) // IPFS + BT
  → Display hash as sharing link
```

### 1.3 URL Registration → Collection

```
FileManager → URL tab → Input URL → POST /files/register_url
  → fileSvc.RegisterURL(url, filename)
    → HTTP GET url → SHA256 → InsertFileMeta + InsertFileProvider(type="http")
```

## 2. Collection List (Plaza)

### 2.1 Data Source

```
Plaza.loadAll()
  → listAnonCollections() → GET /anon/collections
    → Iterate storage directory, read each blob, JSON deserialize
    → Return [{hash, friendly_name, name_preview, entry_count, tags, created_at}]
    → Note: does NOT return entries array!
  → listPublicCollections() → GET /collections/public
    → Return public collections with current_hash
```

### 2.2 Display Logic (CollectionCard)

```
collFileCount(c):
  → c.entry_count || (c.entries ? c.entries.length : 0)
  → list API returns entry_count, entries=undefined → ✅

isSingleFile = count === 1
singleFile = c.entries?.[0] || (c.name_preview ? {path: c.name_preview} : null)
  → list API: entries=undefined → fallback to name_preview ✅
  → name_preview if "hello.txt" → {path: "hello.txt"} ✅

cardIcon:
  isSingleFile && singleFile 
    ? fileIconFromPath(singleFile.path)  // 📝🎬🖼️ etc.
    : isDummy ? '🧪' : '📦'
  → 🔴 BUG: After refresh may still show 📦 → CF cache

Name display:
  isSingleFile && singleFile ? singleFile.path : name
  → Single file shows filename, multiple files show collection name ✅
```

### 2.3 Click Navigation

```
Local collection (has hash):
  handleDownload → navigate(`/anon/collections/${c.hash}`) ✅
   
P2P collection (has current_hash):
  handleDownload → navigate(`/anon/collections/${c.current_hash}`) ✅
   
P2P collection (only username+coll):
  handleDownload → navigate(`/${username}/${collection_name}`)
  → Explorer.jsx → getCollection → navigate to AnonExplorer ✅
```

## 3. Collection View (AnonExplorer)

### 3.1 Loading

```
GET /anon/collections/:hash
  → controller.GetAnonCollection → anonSvc.GetCollectionByHash(hash)
    → Read storage/<hash[:2]>/<hash> → JSON deserialize
    → Return {hash, friendly_name, entries: [{path, hash}], tags, created_at}
    → Note: returns complete entries array!
```

### 3.2 Empty Collection Handling

```
fetchCollection(hash):
  coll = await api.getAnonCollection(hash)
  if (!coll.entries || coll.entries.length === 0)
    → api.deleteFile(hash) // Auto-delete empty collection
    → setError('Empty collection, automatically deleted')
```

### 3.3 Single File Preview

```
isSingleFile = entries.length === 1 && !entries[0].path.includes('/')

If single file:
  Image: <img src={downloadUrl}> ✅
  PDF: <iframe src={downloadUrl}> ✅
  Text/Code: <TextPreview> → fetch content → <pre> ✅
  Others: Download button ✅
```

### 3.4 Nested Collections

```
On load, get all collection hashes: Set(allCollHashes)
File hash in this set → Show as 📦 collection link → Click to navigate ✅
```

## 4. P2P Broadcast and Discovery

### 4.1 Announce

```
POST /p2p/announce → p2pSvc.AnnounceHash(hash)
  → DHT.Provide(CID) → IPFS network
  → Failure returns 200+WARN (single-node DHT isolated) ✅

POST /bt/announce → btSvc.Announce(hash)
  → SHA256 → infohash(first 20 bytes) → DHT.Announce
  → Success returns "announced on BT DHT" ✅

POST /p2p/dual/announce → dualSvc.Announce(hash)
  → Parallel: IPFS announce + BT announce ✅
```

### 4.2 Find

```
POST /bt/find → btSvc.FindProviders(hash)
  → DHT traverse → get_peers → Collect IP:port
  → Cross-node test: Node B searches Node A's announced file → count=1 ✅

POST /p2p/dual/find → dualSvc.FindProviders(hash)
  → Parallel: IPFS find + BT find → Merge results ✅
```

## 5. User Collections (Explorer)

### 5.1 Routing

```
/:username/:collection_name → Explorer.jsx
   
Current behavior:
  1. getCollection(username, collName)
  2. If has current_hash → redirect to /anon/collections/:hash ✅
  3. If not → show old Explorer UI (version management/commit/merge/sync)
```

### 5.2 🔴 Issue: Explorer Extra Features Lost

```
After redirect, user cannot use:
  - Commit new version
  - View version history
  - Fork
  - Merge
  - Save to local
  - Upload file to collection
  
AnonExplorer doesn't have these features!
```

## 6. 🔴 Discovered Detail Issues

### 6.1 Explorer Features Lost
- Explorer's redirect loses commit/merge/sync/upload features
- **Fix**: AnonExplorer needs "Version History" button and actions

### 6.2 Single File Icon CF Cache
- Source code fix is correct but CF may cache old version
- **Verify**: Clear CF cache or wait for max-age=0 to take effect

### 6.3 name_preview May Be Empty
- Old collections have name_preview as null → fallback to "N files" ✅
- But if friendly_name is also empty and entries=undefined → show "Unnamed Collection"

### 6.4 Empty Collection Handling Inconsistent
- AnonExplorer: auto-delete empty collections ✅
- Plaza list: empty collections still show "0 files"
- **Fix**: Plaza should also mark empty collections as cleanable in list

### 6.5 Collection Name Priority
```
CollectionCard: collection_name → friendly_name → name_preview → N files → hash prefix → 'Unnamed Collection' ✅
AnonExplorer: friendly_name → name_preview → N files → 'Unnamed Collection' ✅
Inconsistent: CollectionCard has collection_name and hash fallback, AnonExplorer doesn't
```

### 6.6 P2P Collections Cannot Be Distinguished in Plaza
- P2P collections show "⚪ Local Only" — wrong!
- P2P collections should show "🔵 P2P Available"
- **Root cause**: `c._type === 'public'` check — if reg server doesn't return public collections, this is always false

## 7. Fix Priority

| Priority | Issue | Fix |
|----------|-------|-----|
| P0 | Single file icon | Fixed after CF deploy, verify |
| P0 | Explorer features lost | Add version history + action buttons to AnonExplorer |
| P1 | P2P collection marking | Fix _type marking logic |
| P1 | Empty collections in list | Plaza auto-hide or mark empty collections |
---

## Dual-Channel Architecture (2026-04-28)

Peerdrive implements two communication channels, users can freely switch:

### Channel 1: Central Server (hardcoded, enabled by default)
```
User → Registration Server (VPS :4000)
     → User authentication, relay list, peer discovery
     → Collection publishing, message board, statistics
```
- Hardcoded address, always available
- Provides reliable service discovery
- Works even without P2P network

### Channel 2: P2P Free Network (optional enable)
```
User → IPFS DHT / BT DHT
     → P2P node discovery, file exchange
     → WebRTC direct connection, port forwarding
```
- Fully decentralized
- Users choose enable/disable themselves
- Doesn't depend on any central server

### Consistency Design
- Both channels have **completely identical user logic** — same API, same UI
- When nodes are available, prioritize P2P; when no nodes, fallback to central server
- Collection creation, sharing, downloading have the same experience across both channels
