# Peerdrive Collection Logic — Full Trace

> 2026-04-28 · Review all detail issues

## 1. Collection Creation

### 1.1 AnonCreator → POST /anon/collections

```
User drags files → entries[] → clicks save → POST /anon/collections
  body: {entries: [{path, hash}], friendly_name, tags}
  handler: controller.CreateAnonCollection
    → anonSvc.CreateCollection(name, entries, tags)
      → Validate: reject empty path, path with ../, non-64-char hash
      → model.NewAnonCollection(name, entries, tags)
        → version=1, CreatedAt=now, compute name_preview (first 3 filenames from entries)
      → JSON serialize → SHA256 → write to storage/<hash[:2]>/<hash>
      → Return {hash, name_preview, entry_count}
```

### 1.2 Broadcast Button (Plaza)

```
Enter hash in search bar → click 📡Broadcast →
  → api.createAnonCollection([{path:'broadcast',hash}], 'Broadcast '+hash prefix)
  → api.dualAnnounce(hash) // IPFS + BT
  → Display hash as share link
```

### 1.3 URL Registration → Collection

```
FileManager → URL tab → enter url → POST /files/register_url
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
  → 🔴 BUG: May still show 📦 after refresh → CF cache

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

## 3. Collection Viewer (AnonExplorer)

### 3.1 Load

```
GET /anon/collections/:hash
  → controller.GetAnonCollection → anonSvc.GetCollectionByHash(hash)
    → Read storage/<hash[:2]>/<hash> → JSON deserialize
    → Return {hash, friendly_name, entries: [{path, hash}], tags, created_at}
    → Note: Returns full entries array!
```

### 3.2 Empty Collection Handling

```
fetchCollection(hash):
  coll = await api.getAnonCollection(hash)
  if (!coll.entries || coll.entries.length === 0)
    → api.deleteFile(hash) // Auto-delete empty collection
    → setError('Empty collection, auto-deleted')
```

### 3.3 Single File Preview

```
isSingleFile = entries.length === 1 && !entries[0].path.includes('/')

If single file:
  Image: <img src={downloadUrl}> ✅
  PDF: <iframe src={downloadUrl}> ✅
  Text/code: <TextPreview> → fetch content → <pre> ✅
  Other: Download button ✅
```

### 3.4 Nested Collections

```
Get all collection hashes on load: Set(allCollHashes)
File hash in this set → display as 📦collection link → click to navigate ✅
```

## 4. P2P Broadcast & Discovery

### 4.1 Announce

```
POST /p2p/announce → p2pSvc.AnnounceHash(hash)
  → DHT.Provide(CID) → IPFS network
  → Return 200+WARN on failure (single-node DHT isolation) ✅

POST /bt/announce → btSvc.Announce(hash)
  → SHA256 → infohash (first 20 bytes) → DHT.Announce
  → Return "announced on BT DHT" on success ✅

POST /p2p/dual/announce → dualSvc.Announce(hash)
  → Parallel: IPFS announce + BT announce ✅
```

### 4.2 Find

```
POST /bt/find → btSvc.FindProviders(hash)
  → DHT traversal → get_peers → collect IP:port
  → Cross-node test: Node B finds file announced by Node A → count=1 ✅

POST /p2p/dual/find → dualSvc.FindProviders(hash)
  → Parallel: IPFS find + BT find → merge results ✅
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
After redirect, users cannot use:
  - Commit new version
  - View version history
  - Fork
  - Merge
  - Save to local
  - Upload file to collection
  
AnonExplorer does not have these features!
```

## 6. 🔴 Detail Issues Found

### 6.1 Explorer Features Lost
- Explorer redirect loses commit/merge/sync/upload features
- **Fix**: AnonExplorer needs "Version History" button and actions

### 6.2 Single File Icon CF Cache
- Source code fix is correct but CF may cache old version
- **Verify**: Clear CF cache or wait for max-age=0 to take effect

### 6.3 name_preview May Be Empty
- Old collection name_preview is null → fallback to "N files" ✅
- But if friendly_name is also empty and entries=undefined → shows "Unnamed Collection"

### 6.4 Inconsistent Empty Collection Handling
- AnonExplorer: Auto-deletes empty collections ✅
- Plaza list: Empty collections still show "0 files"
- **Fix**: Plaza should also mark empty collections as cleanable during list

### 6.5 Collection Naming Priority
```
CollectionCard: collection_name → friendly_name → name_preview → N files → hash prefix → 'Unnamed Collection' ✅
AnonExplorer: friendly_name → name_preview → N files → 'Unnamed Collection' ✅
Inconsistent: CollectionCard has collection_name and hash fallback, AnonExplorer does not
```

### 6.6 P2P Collections Not Distinguishable in Plaza
- P2P collections show "⚪ Local only" — Wrong!
- P2P collections should show "🔵 P2P available"
- **Root cause**: `c._type === 'public'` check — if reg server doesn't return public collections, this is always false

## 7. Fix Priority

| Priority | Issue | Fix |
|--------|------|------|
| P0 | Single file icon | Fixed after CF deploy, verify |
| P0 | Explorer features lost | AnonExplorer add version history + action buttons |
| P1 | P2P collection marking | Fix _type marking logic |
| P1 | Empty collections in list | Plaza auto-hide or mark empty collections |
---

## Dual-Channel Architecture (2026-04-28)

Peerdrive follows two communication channels, users switch freely:

### Channel 1: Central Server (Hardcoded, Default Enabled)
```
User → Registration Server (VPS :4000)
      → User authentication, Relay list, Peer discovery
      → Collection publishing, message board, statistics
```
- Hardcoded address, always available
- Provides reliable service discovery
- Works without P2P network

### Channel 2: P2P Free Network (Optional)
```
User → IPFS DHT / BT DHT
      → P2P node discovery, file exchange
      → WebRTC direct connection, port forwarding
```
- Fully decentralized
- Users choose to enable/disable
- Does not depend on any central server

### Consistency Design
- Both channels have **identical user logic** — same API, same UI
- Prioritize P2P when nodes exist, fall back to central server when not
- Collection creation, sharing, download have identical experience on both channels
