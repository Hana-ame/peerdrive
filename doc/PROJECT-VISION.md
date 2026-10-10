# Peerdrive Project Vision Research (Project Vision)

> 2026-08-16 · Research conclusions: Based on README, REFACTOR, REQUIREMENTS,
> USER-ROLES and recent requirements documents, answering "what kind of project should peerdrive be".
> Serves as the anchor for all future feature decisions: new features first ask "does it fit this vision".

---

## 1. One-Sentence Positioning

> **P2P Netdisk over WebRTC: storage-decentralized, peerId-trusted today, identity-anchored later.**
>
> A netdisk: member nodes connect browser-native via PeerJS signaling + WebRTC, content is stored content-addressed
> (SHA256) and shared through the operator's explicit share scope. Ownership and access today rest on the node's own
> peerId (plus the optional PSK admission gate) — **not on accounts**.
> Analogy: **P2P version of self-hosted NAS + Resilio Sync shared folders** (netdisk + marketplace + select & save),
> not a public network like IPFS/BT.
>
> ⚠️ **Identity is roadmap-only.** Accounts / JWT / user↔node directory / statistics are Phase 7: `doc/ROADMAP.md` §7
> marks Identity Management as **design ready, not implemented**, and its hard constraint forbids account dependencies
> in phases 1-6 (`doc/modules/auth/README.md`). The identity content below describes the *target direction*, not a
> shipping capability — do not present it as one in `README.md` / `doc/NETDISK.md` / `doc/ROADMAP.md`.

## 2. Status Quo: It's a Frankenstein of "Six Things"

1. Multi-protocol downloader — SHA256/IPFS/BT/URL/WebDAV, pulls files from anywhere  
   *(BT and IPFS are optional/experimental, opt-in via env vars; see README for defaults)*
2. Content-addressed collection manager — Collection + Provider
3. P2P network node — libp2p (legacy, removed) / PeerJS + WebRTC (current interconnection layer)
4. Sharing platform — anonymous collections, Plaza, comments, share links
5. Account system — centralized registration server, user↔node directory, statistics  
   *(Phase 7 of ROADMAP; **design ready, not implemented** — not a current feature. The registration server binary and
   the node-side auth middleware exist, but the user↔node directory and statistics described here do not —
   `doc/modules/auth/README.md`)*
6. Operations panel — a pile of P2P/BT/IPFS dashboards (mostly dead code)  
   *(legacy; targeted for cleanup per ROADMAP Phase 1-6 priorities, `doc/archive/LEGACY.md`)*

## 3. Historical Trajectory: Three Pivots Have Pointed the Way

| Phase | Direction | Outcome |
|---|---|---|
| Initial (README) | Public P2P full mesh, dual DHT content addressing | Missing economic model, ISP QoS, overlaps with IPFS, BT fading |
| 2026-08 Architecture Refactor | Interconnection layer switched to **PeerJS public cloud signaling + WebRTC** | Ops-free, browser direct connect, small-scale mesh — abandoning "public full mesh" |
| Recent New Requirements | Identity authentication + collection tri-state visibility + P2P broadcast + authorized HTTP tunnel | **Private domain + identity + authorization** |

**Conclusion: All three pivots point the same way — not building a public full mesh, building a small-group private network with identity endorsement.**

*What shipped vs what is still a target:* the collection tri-state, the share scope and the P2P save pipeline are
built; "identity endorsement" is the Phase 7 target. Until it lands, the private-domain trust rests on the node peerId
plus the optional PSK gate — not on accounts (`doc/ROADMAP.md` §Hard Constraints).

## 4. Key Constraint Signals

- Historical conclusion: **public content-addressing is a dead direction** (don't compete with IPFS)
- "User scenarios almost never allow http/https connections" → **browser-native WebRTC direct connect** is the core experience, not an option
- "Mutually independent and irresponsible nodes" → **authorization must not blindly trust peerId**. Today that is met
  at node level (peerId + PSK admission + the operator's explicit share scope, no accounts involved); "authorization
  through identity/token" is the Phase 7 upgrade and is **not implemented** — `doc/ROADMAP.md` forbids account
  dependencies before then
- Collection tri-state (public/authorized-only/self-only) + Steam-style group sharing → **permissions are a product-level feature, not a security detail**

## 5. Four Pillars (Target State)

> ⚠️ **Status note (2026-10):** The Identity Layer (Phase 7 of `doc/ROADMAP.md`) is **design ready but not implemented**.  
> The Access Layer is optional. Until Phase 7 lands, all identity/authorization operates at the **peerId level**  
> (node identity, no accounts). The core product experience — Collections + WebRTC + sharing scope —  
> is fully functional without any of the Identity Layer. Do not describe Phase 7 capabilities as available features.

| Layer | Responsibility | Technology | Status |
|---|---|---|---|
| Storage Layer (Decentralized) | Content-addressed storage + collection tri-state visibility, P2P between member nodes | SQLite + CAS | 🟢 Implemented |
| Transport Layer (P2P) | PeerJS cloud signaling + WebRTC hole punching, browser zero-install direct connect | Self-hosted signaling optional | 🟢 Implemented |
| Identity Layer (Centralized) | Registration server: JWT/OAuth, user↔node directory, permissions, upload/download stats | Not yet a public entry point; ownership is the node peerId | 🔴 Phase 7, not implemented |
| Access Layer (Optional) | Authorized HTTP tunnel: remote node management / access remote REST API | See doc/HTTP_API_PROXY.md | 🟡 Partial (port forwarding v2) |

**Shipped user model** (what the UI actually splits on today): **consumer** — guest viewing and saving shared content
through the public panel or `packages/peerdrive-client`, no local node required — vs **operator** — owner of a node,
using the management console `front/src/features/`. The account-based four user types of `doc/modules/auth/USER-ROLES.md`
(anonymous visitor / authenticated user / anonymous + node / authenticated + node) are a Phase 7 target and are
**not implemented** as an information architecture.

## 6. What the Project Should NOT Do (Boundaries)

- ❌ Public content-addressing network / DHT full-mesh search — BT, libp2p stacks downgraded and cleaned up
- ❌ Generic P2P HTTP proxy (that's wintools webrtc-proxy's separate positioning) — peerdrive only does
  **authorized, self-anchored** node management tunnels
- ❌ All-in-one multi-protocol downloader (download downgraded to optional provider, not a selling point):
  **BT (`go-peerdrive-bt`) and IPFS are optional / experimental plugins, off by default**
  (`PEERDRIVE_BT_ENABLE`, `PEERDRIVE_IPFS_ENABLE`), not part of the default netdisk path
- ❌ WebDAV / forward / dead frontend components (high risk, doc/archive/LEGACY.md already marked for deletion)

## 7. Implications for HTTP Tunnel Integration

Authorized HTTP tunnel is intended as a **natural extension** of the target identity architecture: inter-node management/access would eventually go through identity authorization,
enabling "authenticated + node" to remotely manage their own multiple nodes or other members' nodes, sharing the same trust model as collection tri-state.
Therefore it should be built on the group authorization system, not as an independent security mechanism (see doc/HTTP_API_PROXY.md).

**Where it actually stands today**: the tunnel exists (`fwd-open/challenge/auth/data/close`, HMAC challenge auth + port whitelist, opt-in via `PEERDRIVE_PORTFWD_ENABLE`),
but its gate is a per-tunnel HMAC secret — **not account identity**. The identity-based gate above is a Phase 7 outcome, not implemented.

## 8. Implementation Recommendations

> ⚠️ **Alignment with `doc/ROADMAP.md`:** The implementation order is user-specified (Phases 1-7).  
> Phase 7 (Identity) is last — do not describe it as a current feature in user-facing docs.

1. Follow the ROADMAP order: Interconnect (P1) → Files (P2) → Combine (P3) → Management Chain (P4) → Scope (P5) → Upload/Download/Save (P6) → Identity (P7)
2. Clean up legacy code (BT/libp2p/WebDAV/dead components), focus on the **Collection + WebRTC + netdisk** main pipeline;
   BT / IPFS stay optional / experimental plugins behind `PEERDRIVE_BT_ENABLE` / `PEERDRIVE_IPFS_ENABLE`
3. The core product is **Collection + WebRTC + sharing scope** — fully functional without accounts (peerId-level identity)
4. When Phase 7 lands, unify the permission system: collection tri-state, HTTP tunnel, and node management share one "account → group → permission" model
5. Advance the HTTP tunnel module following the "module branch + merge + CI/CD" git architecture

## 9. Narrative Consistency (README / NETDISK / ROADMAP / this doc)

One positioning, four documents — same wording, different depth:

| Doc | Owns |
|---|---|
| `README.md` / `README.zh-CN.md` | What works today: **consumer entry** (public panel, no node) and **operator entry** (node + management console); optional plugins marked as such |
| `doc/NETDISK.md` | Goal shape + module breakdown (M1-M6) of the netdisk pipeline, incl. plan-vs-actual deviations |
| `doc/ROADMAP.md` | Development order, and the hard constraint: **no account dependencies before Phase 7** |
| this doc | Direction / boundaries / rationale; every identity claim is marked roadmap-only |

Rules of thumb when editing any of them:

- **Identity = roadmap.** Accounts / JWT / user↔node directory / statistics are Phase 7 — design ready, **not implemented**
  (`doc/modules/auth/README.md`). Never describe them as available today; until Phase 7, ownership is the node peerId.
- **Two entries, not four personas.** Consumer (view & save shared content, no local node) vs operator (own and manage a node).
  The account-based four-role model is a Phase 7 target, not the current information architecture.
- **Optional / experimental plugins.** BT, IPFS and Iwara sit behind their own opt-in switches, and WebDAV / SMB are
  evaluation-stage sources (`doc/WEBDAV-SOURCE-EVALUATION.md`, `doc/SMB-INTERFACE-EVALUATION.md`). None of them is part of
  the default netdisk path — document them as plugins, never as core features.
