# Peerdrive user role model

## Four roles

| # | Role | Auth | Local node | Permissions |
|---|------|------|----------|------|
| 1 | Anonymous visitor | ❌ | ❌ | Browse public collections, download via WebRTC |
| 2 | Authenticated user | ✅ JWT | ❌ | Create collections, upload files, use remote relay |
| 3 | Anonymous+node | ❌ | ✅ API URL | P2P sharing, manage local files |
| 4 | Authenticated+node | ✅ JWT | ✅ API URL | Full permissions |

## UI differences

| Feature | Anonymous visitor | Authenticated user | Anonymous+node | Authenticated+node |
|------|---------|---------|----------|----------|
| Browse public collections | ✅ | ✅ | ✅ | ✅ |
| WebRTC download | ✅ | ✅ | ✅ | ✅ |
| Hash/URL search | ✅ | ✅ | ✅ | ✅ |
| Create collection | ❌ | ✅ | ✅ | ✅ |
| Upload file | ❌ | ✅ | ❌ | ✅ |
| P2P panel | ❌ | ❌ | ✅ | ✅ |
| File management | ❌ | ❌ | ✅ | ✅ |
| Node settings | ❌ | ❌ | ✅ | ✅ |
| BT/IPFS control | ❌ | ❌ | ✅ | ✅ |
| Admin panel | ❌ | ❌ | ❌ | ✅ |
| Registration server admin | ❌ | ❌ | ❌ | ✅ (admin) |

## Detection logic (frontend)

```javascript
// Anonymous visitor: nothing configured
// Authenticated user: localStorage has auth_key, no api_base
// Anonymous+node: localStorage has api_base, no auth_key  
// Authenticated+node: both present

const hasAuth = !!localStorage.getItem('peerdrive_auth_key');
const hasNode = !!localStorage.getItem('peerdrive_api_base') && 
                localStorage.getItem('peerdrive_api_base') !== DEFAULT_API;

if (!hasAuth && !hasNode) role = 'anonymous';
else if (hasAuth && !hasNode) role = 'authenticated';
else if (!hasAuth && hasNode) role = 'node-owner';
else role = 'full';
```

## WebRTC anonymous download flow

```
Anon Browser                    Node Owner
    │                               │
    │── ws://relay/ws/signal ──→    │  (join room by file hash)
    │                               │
    │  ← SDP offer ─────────────    │  (node creates offer)
    │                               │
    │  ──── ICE candidates ───→    │
    │                               │
    │  ═══ WebRTC Data Channel ═══ │
    │  ← file chunks (16KB each) ─ │
    │                               │
    │  download complete            │
```

Anonymous users never touch the HTTP API; they only receive files through the WebRTC Data Channel.

## Without a local node: ways to obtain files

Users without a local node must "use every trick" to get collections from the following sources:

```
No-local-node user
  ├── P2P DHT network     → find by hash → discover peer → download collection
  ├── Registration Server → get public peer list → connect → download
  ├── WebRTC              → connect to a node user → Data Channel transfer
  ├── Public Relay        → direct HTTP download (if relay is open)
  └── URL/Hash paste      → locate file from the P2P network
```

| Source | What's needed | Latency | Reliability |
|------|---------|------|--------|
| P2P DHT | File hash | High (DHT lookup) | Medium (peer may be offline) |
| Reg Server | Know the reg server URL | Low | High |
| WebRTC | Signaling server + peer online | Low | Medium (needs NAT hole punching) |
| Relay HTTP | Relay URL is open | Low | High |
| Hash paste | Know the hash or collection URL | High | Medium |

## With a local node: direct management

Users with a local node operate the local filesystem directly:

```
Local-node user
  ├── Local file register    → /files/register_local
  ├── Folder register        → /files/register_folder
  ├── HTTP upload            → /files/upload
  ├── URL register           → /files/register_url
  ├── P2P share              → /p2p/announce + /p2p/dual/announce
  ├── Collection create      → /anon/collections
  └── Node management        → /p2p/status, /bt/status, etc.
```
