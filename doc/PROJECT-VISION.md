# Peerdrive Project Vision Research (Project Vision)

> 2026-08-16 · Research conclusions: Based on README, REFACTOR, REQUIREMENTS,
> USER-ROLES and recent requirements documents, answering "what kind of project should peerdrive be".
> Serves as the anchor for all future feature decisions: new features first ask "does it fit this vision".
>
> **⚠️ Implementation status (updated 2026-10-10)**: Identity/account layer (Phase 7) is **designed but not yet implemented**.
> The current working system operates at the **node-level peerId** — marketplace, join, share, save all work without accounts.
> This document describes the target architecture; actual shipped capabilities are in `README.md` and `doc/NETDISK.md`.
> See `doc/ROADMAP.md` for phase status (Phase 1–6 🟢 mostly ready, Phase 7 🔴 not implemented).

---

## 1. One-Sentence Positioning

> **P2P Private File Network: node-centric today, identity-centric when Phase 7 ships.**
>
> Current (Phase 1–6): A self-hosted P2P file node — connect via PeerJS/WebRTC, share collections
> at node level, save files cross-node. No accounts required; peerId is the identity.
>
> Target (Phase 7+): A private cloud storage with a registration server as trust anchor,
> member nodes connected via WebRTC, and content shared by group authorization.
> Analogy: **P2P version of self-hosted NAS + Resilio Sync shared folders + identity/permissions/statistics**,
> not a public network like IPFS/BT.
>
> **⚠️ Phase 7 (identity/accounts) is NOT yet implemented.** The system currently works
> fully at node-level peerId. The identity layer is designed (`doc/modules/auth/API-DESIGN.md`)
> but not built. Do not reference accounts/authentication as a working feature until Phase 7 lands.

## 2. Status Quo: It's a Frankenstein of "Six Things"

1. **Multi-protocol downloader** — SHA256/IPFS/BT/URL/WebDAV, pulls files from anywhere *(BT optional, IPFS optional, WebDAV deprecated)*
2. **Content-addressed collection manager** — Collection + Provider *(🟢 core feature)*
3. **P2P network node** — PeerJS + WebRTC (current) / libp2p *(✅ legacy libp2p removed 2026-08)*
4. **Sharing platform** — anonymous collections, Plaza, comments, share links *(🟢 core feature)*
5. **Account system** — centralized registration server, user↔node directory, statistics *(🔴 designed, not implemented)*
6. **Operations panel** — a pile of P2P/BT/IPFS dashboards *(🟡 core pages rebuilt; BT/IPFS/Iwara marked optional)*

**What's actually shipped today (2026-10-10)**: A self-hosted P2P file node with content-addressed
storage, PeerJS/WebRTC interconnect, node-level sharing with scope control, and a netdisk UI
for browsing/joining/saving cross-node. No accounts, no identity layer, no centralized auth.
The "Frankenstein" has been cleaned: libp2p removed, WebDAV deprecated, BT/IPFS downgraded to optional.

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

| Layer | Responsibility | Technology | Status |
|---|---|---|---|
| **Storage Layer** (Decentralized) | Content-addressed storage + collection tri-state visibility, P2P between member nodes | SQLite + CAS | 🟢 Phase 2 ready |
| **Transport Layer** (P2P) | PeerJS cloud signaling + WebRTC hole punching, browser zero-install direct connect | Self-hosted signaling optional | 🟢 Phase 1 ready |
| **Sharing Layer** (Node-level) | Share scope declaration + access control (PSK, passcodes, tri-state) at peerId level | `share` verb + scope JSON | 🟢 Phase 5 ready |
| Identity Layer (Centralized) | Registration server: JWT/OAuth, user↔node directory, permissions, upload/download stats | Sole public entry point | 🔴 **Phase 7 — designed, not implemented** |
| Access Layer (Optional) | Authorized HTTP tunnel: remote node management / access remote REST API | See doc/HTTP_API_PROXY.md | 🟡 Port forwarding v2 (Phase 4) |

Four user types (USER-ROLES.md): anonymous visitor / authenticated user / anonymous + node / authenticated + node.
**Current**: Only "anonymous visitor" and "anonymous + node" work (peerId-based, no accounts).
"Authenticated user" and "authenticated + node" require Phase 7 identity layer.

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

## 8. Implementation Recommendations (Current — updated 2026-10-10)

**What's done (Phase 1–6, mostly ready)**:
1. ✅ PeerJS/WebRTC interconnect layer (libp2p removed 2026-08, replaced by PeerJS signaling + WebRTC)
2. ✅ Content-addressed storage with Collection + Provider model
3. ✅ Cross-node file fetch/serve (share + req verbs, streaming, progress, cancel)
4. ✅ Node-level management (admin WS verb, PSK gate, share scope declaration)
5. ✅ File scope management (share scope JSON, passcodes, tri-state visibility)
6. ✅ Upload/download/save closed loop (chunked upload, streaming fetch, CAS write, verify)
7. ✅ Netdisk UI (node market, join, browse, save, transfer management)
8. ✅ No-node consumer panel (single-file `panel.html`, file:// compatible, share/req frames)
9. ✅ Optional extensions: BT (opt-in), IPFS (opt-in), Iwara (opt-in), port forwarding v2 (opt-in)

**What's NOT done (Phase 7 — identity/accounts)**:
1. 🔴 Registration server / account system — designed (`doc/modules/auth/API-DESIGN.md`) but not implemented
2. 🔴 JWT/OAuth authentication — not implemented
3. 🔴 User↔node directory — not implemented
4. 🔴 Identity propagation for cross-node restricted content — peerId used instead
5. 🔴 Upload/download statistics with fraud prevention — not implemented

**Next steps**:
1. Stabilise Phase 1–6 (fix tech debt: #273–287, security defaults #282, WAL mode #277, DB indexes #275)
2. Decide Phase 7 timeline: implement account system or keep peerId-only model indefinitely
3. If Phase 7 proceeds: build on the existing share scope / PSK / passcode patterns, don't reinvent
4. Mark all optional extensions (BT/IPFS/Iwara/SMB/WebDAV) consistently in docs and UI navigation

**Boundary reminder**: Do NOT build Phase 7 features as if they exist. The current system is
fully functional at node-level peerId. Identity layer is a future concern, not a current dependency.
