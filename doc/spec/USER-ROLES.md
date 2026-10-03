# Peerdrive User Role Model

## Four Roles

| # | Role | Authentication | Local Node | Permissions |
|---|------|----------------|------------|-------------|
| 1 | Anonymous Visitor | ❌ | ❌ | Browse public collections, download via WebRTC |
| 2 | Authenticated User | ✅ JWT | ❌ | Create collections, upload files, use remote relay |
| 3 | Anonymous + Node | ❌ | ✅ API URL | P2P sharing, manage local files |
| 4 | Authenticated + Node | ✅ JWT | ✅ API URL | All permissions |

## UI Differences

| Feature | Anonymous Visitor | Authenticated User | Anonymous + Node | Authenticated + Node |
|---------|------------------|-------------------|------------------|---------------------|
| Browse public collections | ✅ | ✅ | ✅ | ✅ |
| WebRTC download | ✅ | ✅ | ✅ | ✅ |
| Hash/URL search | ✅ | ✅ | ✅ | ✅ |
| Create collections | ❌ | ✅ | ✅ | ✅ |
| Upload files | ❌ | ✅ | ❌ | ✅ |
| P2P panel | ❌ | ❌ | ✅ | ✅ |
| File management | ❌ | ❌ | ✅ | ✅ |
| Node settings | ❌ | ❌ | ✅ | ✅ |
| BT/IPFS control | ❌ | ❌ | ✅ | ✅ |
| Admin panel | ❌ | ❌ | ❌ | ✅ |
| Registration server management | ❌ | ❌ | ❌ | ✅ (admin) |

## Detection Logic (Frontend)

```javascript
// Anonymous Visitor: nothing configured
// Authenticated User: localStorage has auth_key, no api_base
// Anonymous + Node: localStorage has api_base, no auth_key  
// Authenticated + Node: both present

const hasAuth = !!localStorage.getItem('peerdrive_auth_key');
const hasNode = !!localStorage.getItem('peerdrive_api_base') && 
                localStorage.getItem('peerdrive_api_base') !== DEFAULT_API;

if (!hasAuth && !hasNode) role = 'anonymous';
else if (hasAuth && !hasNode) role = 'authenticated';
else if (!hasAuth && hasNode) role = 'node-owner';
else role = 'full';
```

## WebRTC Anonymous Download Flow

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

Anonymous users never touch HTTP API; they only receive files through WebRTC Data Channel.

## Without Local Node: Ways to Get Files

Users without a local node must "use all means" to get collections from the following sources:

```
User without local node
  ├── P2P DHT Network      → Find by hash → Discover peer → Download collection
  ├── Registration Server  → Get public peer list → Connect → Download
  ├── WebRTC               → Connect to node owner → Data Channel transfer
  ├── Public Relay         → Direct HTTP download (if relay is open)
  └── URL/Hash Paste       → Locate file from P2P network
```

| Source | What's Needed | Latency | Reliability |
|--------|--------------|---------|-------------|
| P2P DHT | File hash | High (DHT lookup) | Medium (peer may be offline) |
| Reg Server | Know reg server URL | Low | High |
| WebRTC | Signaling server + peer online | Low | Medium (needs NAT hole punching) |
| Relay HTTP | Open relay URL | Low | High |
| Hash Paste | Know hash or collection URL | High | Medium |

## With Local Node: Direct Management

Users with a local node directly operate the local filesystem:

```
User with local node
  ├── Local file registration    → /files/register_local
  ├── Folder registration        → /files/register_folder
  ├── HTTP upload                → /files/upload
  ├── URL registration           → /files/register_url
  ├── P2P sharing                → /p2p/announce + /p2p/dual/announce
  ├── Collection creation        → /anon/collections
  └── Node management            → /p2p/status, /bt/status, etc.
