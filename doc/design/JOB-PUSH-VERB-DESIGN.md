# Job Push Verb Design (WS Server-Side Push)

> **Status:** Design proposed (2026-10)  
> **Related issue:** #285  
> **Related:** #76 (frontend streaming parse), #217 (Context unified state), #213 (capability negotiation)  
> **Implementation complexity:** ~400 lines across backend + frontend; this document defines the protocol for a future implementation PR.

---

## 1. Problem Statement

Frontend currently **polls every 2 seconds** for peer list, pull task progress, and connection status changes. This is documented in `Transfers.jsx`:

```js
// there is no job-push verb on the WS protocol, so we poll while any job is
// running (2s) and stop polling once everything reaches a terminal state
```

### Costs

| Metric | Polling (current) | Push (proposed) |
|---|---|---|
| Latency | Up to 2s delay | <200ms (frame delivery) |
| Idle bandwidth | Constant 2s poll even when idle | Zero when idle (opt-in subscription) |
| Connection load | 0.5 req/s per subscribed page | 0 when no state change |
| Multi-page consistency | Each page polls independently | One subscription, shared updates |

---

## 2. Design Principles

1. **Push is opt-in**: Old clients never see push frames; new clients subscribe explicitly
2. **Subscription-scoped**: Each push type is independently subscribable; no single subscription forces all updates
3. **No breaking changes**: Push frames are additive; existing frame types are untouched
4. **Capability-gated**: New push verbs require capability negotiation (coordinate with #213)
5. **Local WS only**: Push is only implemented on local `/ws/peer` sessions (admin surface), not WebRTC (security boundary — see `admin.go` design)

---

## 3. Frame Protocol Extension

### 3.1 New Frame Types

#### Subscription frames (request-response, same as existing pattern)

```json
// Subscribe to job progress updates
{"type": "subscribe", "topics": ["job-progress"], "reqId": "<uuid>"}
// → {"type": "sub-ack", "topics": ["job-progress"], "reqId": "<uuid>"}

// Unsubscribe
{"type": "unsubscribe", "topics": ["job-progress"], "reqId": "<uuid>"}
// → {"type": "sub-ack", "topics": ["job-progress"], "reqId": "<uuid>", "removed": true}
```

#### Push frames (server-initiated, no `reqId` expected from client)

```json
// Pull job progress update (replaces 2s polling of GET /p2p/pull)
{"type": "job-progress", "topic": "job-progress",
 "job": {"id": "<uuid>", "hash": "<sha256>", "total": 1024000, "received": 512000,
         "status": "running", "rate_bps": 1048576, "eta_seconds": 12,
         "error": null, "started_at": "2026-10-10T12:00:00Z", "ended_at": null}}

// Peer list change (replaces 2s polling of GET /peerjs/nodes)
{"type": "peer-list", "topic": "peer-list",
 "peers": [{"peer_id": "abc123", "connected": true, "last_seen": "2026-10-10T11:58:00Z",
            "node_type": "go-persistent", "shares": {"collections": 3, "files": 42, "directories": 1}}],
 "self": {"peer_id": "def456", "online": true}}

// Connection state change (replaces polling of ws.getStatus())
{"type": "conn-state", "topic": "conn-state",
 "status": "open", "since": "2026-10-10T12:00:00Z"}
```

### 3.2 Topic Registry

| Topic | Source | Frequency | Payload size |
|---|---|---|---|
| `job-progress` | `PeerPuller` task updates | Per-chunk (~64KB, ~10/s per active job) | ~200 bytes |
| `peer-list` | Discovery + connection events | On change only (add/remove/heartbeat) | ~500 bytes |
| `conn-state` | WS session lifecycle | On open/close/reconnect | ~50 bytes |

### 3.3 Throttling

`job-progress` pushes are throttled to **1 frame per 200ms per topic** to avoid overwhelming the WS frame queue during large transfers. If more than 1 update occurs within the throttle window, only the latest is sent (coalescing).

---

## 4. Backend Implementation Plan

### 4.1 Subscription Hub

New file: `back/internal/transport/push_hub.go`

```go
// PushHub manages server-initiated push subscriptions for local WS sessions.
// Thread-safe. Each session has an independent set of subscribed topics.
type PushHub struct {
    mu       sync.RWMutex
    sessions map[string]*SessionSubs  // sessionID → subs
}

type SessionSubs struct {
    session Session  // reference for SendJSON
    topics  map[string]bool
}

func (h *PushHub) Subscribe(sessionID string, session Session, topics []string)
func (h *PushHub) Unsubscribe(sessionID string, topics []string)
func (h *PushHub) Unbind(sessionID string)  // called on session close
func (h *PushHub) Publish(topic string, payload []byte)  // broadcast to all subscribers
```

### 4.2 Push Verb Handler

Add to `back/internal/transport/conn.go` dispatch table:

```go
case "subscribe":
    // Parse topics, call hub.Subscribe(), respond with sub-ack
case "unsubscribe":
    // Parse topics, call hub.Unsubscribe(), respond with sub-ack
```

### 4.3 Push Emission Points

| Trigger | Location | Topic |
|---|---|---|
| Pull job created/updated/cancelled | `service/peerpull.go` `StartPull` / `OnProgress` | `job-progress` |
| Peer connected/discovered/removed | `transport/peerjs_service.go` `onIncomingConnection` / `connectLoop` | `peer-list` |
| WS session opens/closes | `router/peerjs_routes.go` WS handler | `conn-state` |

### 4.4 Capability Gating

New capability bit: `push-subscribe` (string identifier in capabilities handshake).

- Clients that don't send this capability get no push frames (old clients unaffected)
- Clients that send it receive subscription acks and push frames
- Server-side: `VerbRequiredCap("subscribe")` returns `"push-subscribe"`

---

## 5. Frontend Implementation Plan

### 5.1 WS Client Changes (`platform/transport-ws/`)

Add subscription API:

```js
// Subscribe to push topics
export async function subscribe(topics: string[]): Promise<void>
// Unsubscribe
export async function unsubscribe(topics: string[]): Promise<void>

// Register push frame handlers
export function onPush(topic: string, handler: (payload: any) => void): () => void
```

### 5.2 React Context Changes

Replace polling in `Transfers.jsx`:

```js
// Before (polling):
const t = setInterval(load, POLL_MS);

// After (subscription):
useEffect(() => {
  subscribe(['job-progress']);
  const unsub = onPush('job-progress', (payload) => updateJobState(payload.job));
  return () => { unsub(); unsubscribe(['job-progress']); };
}, []);
```

### 5.3 Connection Status

`ConnectionStatus.jsx` already uses event-driven `onStatus()` — no change needed, but `conn-state` push can replace the polling in other components.

### 5.4 Heartbeat Fallback

Keep a 30s heartbeat ping (`hbTimer` already exists in `client.js`) as a fallback to detect disconnected sessions and resubscribe.

---

## 6. Backward Compatibility

| Client type | Behavior |
|---|---|
| Old client (no `push-subscribe` cap) | Server never sends push frames; client continues polling |
| New client, no subscription | Server sends no push; client can poll or subscribe as needed |
| New client, subscribed | Server pushes on state change; client stops polling |
| Server upgrade, old client | Server ignores subscribe frames (unknown verb); old client unaffected |

---

## 7. Implementation Priority

1. **Phase 1** (minimal viable): `job-progress` push only + Transfers page subscription
2. **Phase 2**: `peer-list` push + node directory page
3. **Phase 3**: `conn-state` push + connection status page

---

## 8. Related Issues & Dependencies

- **#213** (capability negotiation): Push verbs require new capability bit; coordinate with #213 implementation
- **#217** (Context unified state, CLOSED): The push frames feed into the unified Context; this design is the backend counterpart
- **#76** (streaming parse, CLOSED): Frontend streaming parse of push payloads
- **#74** (caching): Push updates can invalidate/bypass cache; coordinate
- **`doc/NETDISK.md` §6**: Documents the polling gap this design addresses

---

## 9. Testing Plan

| Test | Type | Description |
|---|---|---|
| `TestPushHubSubscribeUnsubscribe` | Unit | Subscribe/unsubscribe lifecycle, topic isolation |
| `TestPushHubPublish` | Unit | Broadcast to correct subscribers only |
| `TestPushHubThrottle` | Unit | Coalescing within throttle window |
| `TestSubscribeCapabilityGate` | Integration | Old client (no cap) gets no push; new client does |
| `TestJobProgressPush` | Integration | Pull job triggers push frame to subscribed session |
| `TestPeerListPush` | Integration | Connection change triggers push frame |
| `TestConnStatePush` | Integration | WS open/close triggers push frame |
| `test-push-verbs.test.js` (frontend) | Unit | Subscribe/unsubscribe API, onPush handler registration |

---

## 10. Estimation

| Component | Lines | Effort |
|---|---|---|
| `push_hub.go` (backend) | ~150 | 2h |
| `conn.go` dispatch + capability | ~50 | 1h |
| Push emission points (3 files) | ~80 | 2h |
| `client.js` subscription API | ~80 | 2h |
| `Transfers.jsx` + other pages | ~60 | 1h |
| Tests (backend + frontend) | ~200 | 4h |
| **Total** | **~620** | **~12h** |

---

## 11. Future Extensions

- **Custom topic subscription**: Allow clients to subscribe to specific hashes or node IDs (not just broad topics)
- **WebSocket binary frames for push**: Large payloads (e.g. full peer list with metadata) could use binary frames to reduce JSON overhead
- **Server-side subscription persistence**: Persist subscriptions across WS reconnects (currently per-session)
- **Push rate limiting per session**: Per-session throttle in addition to per-topic throttle
