# Peerdrive P2P Test Grid — 5W1H

> Last: 2026-04-28 · VPS: bwh.moonchan.xyz (97.64.30.221) · Relay: ✅ Running

## 5W1H Context

| 5W1H | Detail |
|------|--------|
| **What** | Peerdrive P2P relay node — file exchange, collection sync, DHT announce, hole punching |
| **Who** | Claude Code (automated) + Lumin (review) |
| **Where** | bwh.moonchan.xyz (Debian 13, 528MB RAM, 1 IPv4 + multiple IPv6) |
| **When** | 2026-04-28 ~ ongoing, systemd auto-restart |
| **Why** | Public P2P entry point — bootstrap peer, relay server, always-on API |
| **How** | Go binary cross-compiled → VPS → systemd service → 2 nodes (A:relay server, B:client) |

## Node Topology

```
┌─────────────────────────────────────────────┐
│  bwh.moonchan.xyz (97.64.30.221)            │
│                                             │
│  ┌─ Node A (relay server) ────────────────┐ │
│  │ HTTP :3000  P2P tcp/37537             │ │
│  │ Peer: 12D3KooWFgwXYLY84fi6UgwT3k4...  │ │
│  │ Relay: server  Public: true            │ │
│  │ Storage: /root/storage-a               │ │
│  │ systemd: peerdrive-relay (auto-start)  │ │
│  └────────────────────────────────────────┘ │
│               ↕ connected (975µs)            │
│  ┌─ Node B (relay client) ────────────────┐ │
│  │ HTTP :3001  P2P tcp/40699             │ │
│  │ Peer: 12D3KooWMNea5PsTNDzCdzbgfqAJ...  │ │
│  │ Relay: client  Bootstrap: Node A       │ │
│  │ Storage: /root/storage-b               │ │
│  └────────────────────────────────────────┘ │
└─────────────────────────────────────────────┘
         ▲
         │ P2P multiaddr (public)
         │ /ip4/97.64.30.221/tcp/37537
         │   /p2p/12D3KooWFgwXYLY84fi...
         │
    ┌────┴────┐
    │ 外部节点  │ (any peer with bootstrap addr)
    └─────────┘
```

## Test Results

| # | Category | Test | Result |
|---|----------|------|--------|
| 1 | Health | Node A ping | ✅ pong |
| 2 | Health | Node B ping | ✅ pong |
| 3 | P2P | Node A enabled + relay=server | ✅ |
| 4 | P2P | Node B enabled + relay=client | ✅ |
| 5 | P2P | Nodes connected (bootstrap) | ✅ 1 peer |
| 6 | P2P | Ping A→B | ✅ <2ms |
| 7 | P2P | Ping B→A | ✅ <2ms |
| 8 | File | Register local file | ✅ hash returned |
| 9 | File | Register folder (recursive) | ✅ registered[] |
| 10 | File | Upload multipart | ✅ SHA256 |
| 11 | File | SHA256 download | ✅ HTTP 200 |
| 12 | P2P | Announce hash on DHT | ✅ announced |
| 13 | P2P | File request B→A (exchange) | ✅ data received |
| 14 | Collection | Create anon collection | ✅ hash returned |
| 15 | Collection | Get collection by hash | ✅ HTTP 200 |
| 16 | Collection | List all collections | ✅ non-empty |
| 17 | P2P | Sync collection A→B | ✅ synced |
| 18 | DHT | DHT provider discovery | ⚠️ isolated (needs ext bootstrap) |
| 19 | CORS | CORS headers for peerdrive.pages.dev | ✅ |
| 20 | Relay | Hole punch enabled | ✅ true |

**Score: 18/20 pass, 1 DHT (needs ext bootstrap), 1 ping format**

## Public Endpoints

| Endpoint | URL |
|----------|-----|
| HTTP API | `http://97.64.30.221:3000` |
| P2P multiaddr | `/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33` |
| Frontend | `https://peerdrive.pages.dev` (set API to VPS in Settings) |
| Test grid page | `http://97.64.30.221:8080` |

## Quick Test Commands

```bash
# Health
curl http://97.64.30.221:3000/ping

# P2P Status
curl http://97.64.30.221:3000/p2p/status | jq

# Connect your node to relay
curl -X POST http://localhost:3000/p2p/connect \
  -H 'Content-Type: application/json' \
  -d '{"addr":"/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33"}'

# Bootstrap your node
PEERDRIVE_BOOTSTRAP_PEER="/ip4/97.64.30.221/tcp/37537/p2p/12D3KooWFgwXYLY84fi6UgwT3k4KFbDVdU7nKzAoErz2eskvrW33" \
PEERDRIVE_RELAY_MODE=client \
  go run ./cmd/server/main.go
```

## Known Issues

| Issue | Severity | Fix |
|-------|----------|-----|
| DHT isolated (single-machine) | Low | Add external bootstrap peer from public DHT |
| No HTTPS on VPS | Medium | Caddy/nginx reverse proxy or Cloudflare Tunnel |
| 528MB RAM limit | Low | Monitor; Go binary ~50MB, runtime ~100MB |
