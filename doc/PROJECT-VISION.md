# Peerdrive Project Vision Research (Project Vision)

> 2026-08-16 · Research conclusions: Based on README, REFACTOR, REQUIREMENTS,
> USER-ROLES and recent requirements documents, answering "what kind of project should peerdrive be".
> Serves as the anchor for all future feature decisions: new features first ask "does it fit this vision".

---

## 1. One-Sentence Positioning

> **P2P Private File Network: identity-centric, storage-decentralized.**
>
> A private cloud storage with a registration server as trust anchor, member nodes connected via WebRTC, and content shared by group authorization.
> Analogy: **P2P version of self-hosted NAS + Resilio Sync shared folders + identity/permissions/statistics**,
> not a public network like IPFS/BT.

## 2. Status Quo: It's a Frankenstein of "Six Things"

1. Multi-protocol downloader — SHA256/IPFS/BT/URL/WebDAV, pulls files from anywhere
2. Content-addressed collection manager — Collection + Provider
3. P2P network node — libp2p (legacy) / PeerJS + WebRTC (new interconnection layer)
4. Sharing platform — anonymous collections, Plaza, comments, share links
5. Account system — centralized registration server, user↔node directory, statistics
6. Operations panel — a pile of P2P/BT/IPFS dashboards (mostly dead code)

## 3. Historical Trajectory: Three Pivots Have Pointed the Way

| Phase | Direction | Outcome |
|---|---|---|
| Initial (README) | Public P2P full mesh, dual DHT content addressing | Missing economic model, ISP QoS, overlaps with IPFS, BT fading |
| 2026-08 Architecture Refactor | Interconnection layer switched to **PeerJS public cloud signaling + WebRTC** | Ops-free, browser direct connect, small-scale mesh — abandoning "public full mesh" |
| Recent New Requirements | Identity authentication + collection tri-state visibility + P2P broadcast + authorized HTTP tunnel | **Private domain + identity + authorization** |

**Conclusion: All three pivots point the same way — not building a public full mesh, building a small-group private network with identity endorsement.**

## 4. Key Constraint Signals

- Historical conclusion: **public content-addressing is a dead direction** (don't compete with IPFS)
- "User scenarios almost never allow http/https connections" → **browser-native WebRTC direct connect** is the core experience, not an option
- "Mutually independent and irresponsible nodes" → **authorization must go through identity/token, not blindly trust peerId**
- Collection tri-state (public/authorized-only/self-only) + Steam-style group sharing → **permissions are a product-level feature, not a security detail**

## 5. Four Pillars

| Layer | Responsibility | Technology |
|---|---|---|
| Identity Layer (Centralized) | Registration server: JWT/OAuth, user↔node directory, permissions, upload/download stats | Sole public entry point |
| Storage Layer (Decentralized) | Content-addressed storage + collection tri-state visibility, P2P between member nodes | SQLite + CAS |
| Transport Layer (P2P) | PeerJS cloud signaling + WebRTC hole punching, browser zero-install direct connect | Self-hosted signaling optional |
| Access Layer (Optional) | Authorized HTTP tunnel: remote node management / access remote REST API | See doc/HTTP_API_PROXY.md |

Four user types (USER-ROLES.md): anonymous visitor / authenticated user / anonymous + node / authenticated + node.

## 6. What the Project Should NOT Do (Boundaries)

- ❌ Public content-addressing network / DHT full-mesh search — BT, libp2p stacks downgraded and cleaned up
- ❌ Generic P2P HTTP proxy (that's wintools webrtc-proxy's separate positioning) — peerdrive only does
  **authorized, self-anchored** node management tunnels
- ❌ All-in-one multi-protocol downloader (download downgraded to optional provider, not a selling point)
- ❌ WebDAV / forward / dead frontend components (high risk, doc/archive/LEGACY.md already marked for deletion)

## 7. Implications for HTTP Tunnel Integration

Authorized HTTP tunnel is a **natural extension** of the "identity-centric" architecture: inter-node management/access also goes through identity authorization,
enabling "authenticated + node" to remotely manage their own multiple nodes or other members' nodes, sharing the same trust model as collection tri-state.
Therefore it should be built on the group authorization system, not as an independent security mechanism (see doc/HTTP_API_PROXY.md).

## 8. Implementation Recommendations

1. Clean up legacy code first (BT/libp2p/WebDAV/dead components), focus on the "Collection + WebRTC + Identity" main pipeline
2. Unify the permission system: collection tri-state, HTTP tunnel, and node management share one "account → group → permission" model
3. Advance the HTTP tunnel module following the "module branch + merge + CI/CD" git architecture (today-last-requirements.txt)
