# peerdrive Modular Design Documentation (doc/design/)

> **Module + Connection** dual-perspective design documentation for peerdrive (PeerJS/WebRTC P2P file network, Go backend + React frontend).
>
> - One document per module in [`modules/NN-*.md`](modules/): Logic / How to Store / When to Store / What is Stored
> - One document per module connection pair in [`connections/NN-*.md`](connections/): Connection Method / Sequence / Situation Handling
> - Connection overview (connection map, startup/shutdown sequence, typical main flows) → [how-to-connect.md](how-to-connect.md)
> - Connection topology visualization (high-resolution vector SVG, zero crossing design):
>   - 🗺️ **Global Overview**: 14 modules / 13 connections panorama → [connections-map.svg](connections-map.svg)
>   - 🗄️ **Storage and Persistence Domain**: Physical disk and SQLite metadata persistence → [domain-storage.svg](domain-storage.svg)
>   - 🌐 **P2P Network and Direct Connection Domain**: Signaling discovery and cross-node/browser DataChannel direct connections → [domain-p2p.svg](domain-p2p.svg)
>   - 🎛️ **Frontend Control and Routing Domain**: Web admin plane, WS admin proxy and internal scheduling → [domain-control.svg](domain-control.svg)
>
> Companion documents: Layered perspective [doc/layers/](../layers/), Architecture decisions and frame protocol [doc/REFACTOR.md](../REFACTOR.md), Deployment [doc/PEERSIGNAL.md](../PEERSIGNAL.md), Netdisk link [doc/NETDISK.md](../NETDISK.md).
> Generation date: 2026-09-27 (based on current code state; when documents and code conflict, code takes precedence and the document should be updated).

## 1. Module List (14)

| # | Document | Module | Code Location | One-liner |
|---|------|------|----------|--------|
| 01 | [modules/01-config.md](modules/01-config.md) | config | `back/internal/config/` | Configuration assembly and startup validation, pure in-memory |
| 02 | [modules/02-repository.md](modules/02-repository.md) | repository | `back/internal/repository/` | SQLite metadata persistence |
| 03 | [modules/03-storage.md](modules/03-storage.md) | storage | `back/internal/pathutil/` + `PEERDRIVE_STORAGE` (default `./storage`) | Content-addressed file storage + path safety |
| 04 | [modules/04-router.md](modules/04-router.md) | router | `back/internal/router/` | HTTP routing / middleware / admin forwarding |
| 05 | [modules/05-controller.md](modules/05-controller.md) | controller | `back/internal/controller/` | HTTP handler layer |
| 06 | [modules/06-service.md](modules/06-service.md) | service | `back/internal/service/` | Business logic orchestration |
| 07 | [modules/07-source.md](modules/07-source.md) | source | `back/internal/source/` | File source + FileRouter |
| 08 | [modules/08-downloader.md](modules/08-downloader.md) | downloader | `back/internal/downloader/` | Multi-protocol downloader |
| 09 | [modules/09-transport.md](modules/09-transport.md) | transport | `back/internal/transport/` | P2P session state machine + verb dispatch |
| 10 | [modules/10-peerjs.md](modules/10-peerjs.md) | peerjs | `back/peerjs/` | PeerJS protocol library |
| 11 | [modules/11-signalserver.md](modules/11-signalserver.md) | signalserver | `back/signalserver/` | Signaling + discovery (pure in-memory) |
| 12 | [modules/12-media-node.md](modules/12-media-node.md) | media-node | `back/cmd/media-node/` + `back/internal/echcore/` | ECH media chain |
| 13 | [modules/13-frontend.md](modules/13-frontend.md) | frontend | `front/src/` | Web consumer |
| 14 | [modules/14-nodestate.md](modules/14-nodestate.md) | nodestate | `back/internal/nodestate/` | Runtime shared state |

## 2. Connection List (13)

| # | Document | Connection Pair | One-liner |
|---|------|--------|--------|
| 01 | [connections/01-frontend-backend.md](connections/01-frontend-backend.md) | frontend ↔ backend | Local WS sessions: admin verb + file data frames |
| 02 | [connections/02-router-controller.md](connections/02-router-controller.md) | router ↔ controller | HTTP dispatch + admin internal forwarding |
| 03 | [connections/03-controller-service.md](connections/03-controller-service.md) | controller ↔ service | Business invocation and error mapping |
| 04 | [connections/04-service-repository.md](connections/04-service-repository.md) | service ↔ repository | DB read/write and transaction boundaries |
| 05 | [connections/05-router-source.md](connections/05-router-source.md) | router ↔ source | Source management injection and endpoints |
| 06 | [connections/06-service-transport.md](connections/06-service-transport.md) | service ↔ transport | P2P business (pull/share/directory) |
| 07 | [connections/07-transport-peerjs.md](connections/07-transport-peerjs.md) | transport ↔ peerjs | Connection lifecycle / DataChannel |
| 08 | [connections/08-transport-signalserver.md](connections/08-transport-signalserver.md) | transport ↔ signalserver | Registration / heartbeat / forwarding / discovery |
| 09 | [connections/09-controller-downloader.md](connections/09-controller-downloader.md) | controller ↔ downloader | Multi-protocol download |
| 10 | [connections/10-controller-storage.md](connections/10-controller-storage.md) | controller ↔ storage | Upload write to disk |
| 11 | [connections/11-transport-storage.md](connections/11-transport-storage.md) | transport ↔ storage | P2P pull to disk + indexing |
| 12 | [connections/12-frontend-signalserver.md](connections/12-frontend-signalserver.md) | frontend ↔ signalserver | Consumer dial + discovery |
| 13 | [connections/13-media-node-ech.md](connections/13-media-node-ech.md) | media-node ↔ ech | ECH domain front media chain |

> Note: media-node and ech belong to the same module 12 (same code directory); connection 13 describes their **internal** wiring.

## 3. Reading Order Suggestions

1. Read [how-to-connect.md](how-to-connect.md) first — get the connection map, startup/shutdown sequence, and typical main flows.
2. To dive deep into a specific module → `modules/NN-*.md` (each has "External Connections" at the end).
3. To modify a specific interaction → the corresponding `connections/NN-*.md` (connection method/sequence/situation handling).

## 4. Document Writing Standards (Follow During Maintenance)

**Module documents** (`modules/NN-*.md`) fixed sections:

1. Logic — Responsibilities, core types, main flows, lifecycle
2. How to Store — Medium/location/format/naming; pure in-memory must explicitly state "not persisted" and composition; delegation to lower layers must be explained
3. When to Store — Exact trigger points for startup/request/event/scheduled/shutdown
4. What is Stored — Fields/tables/file list, noting default values and key constraints
5. Boundaries and Pitfalls — Invariants, failure handling, known pitfalls
6. External Connections — Pointing to related `connections/NN-*.md`

**Connection documents** (`connections/NN-*.md`) fixed sections:

1. Connection Method — Channel/protocol frames/auth/establisher
2. Sequence — ASCII or mermaid + step-by-step explanation (with code evidence)
3. Situation Handling — Timeout/disconnection/concurrency/validation failure/auth failure/half-open/restart scenario tables
4. Related Documents — Cross-references

**Shared Discipline**: Facts must cite code locations (relative to repo root, with line numbers where possible); when information is insufficient, write "unverified" as-is; behavior changes after code modifications must be synced to the corresponding documents (per AGENTS.md coding standards).

## 5. Related Documents

- [doc/layers/](../layers/README.md) — Layered (L1-L8) perspective module documentation and layered testing
- [doc/REFACTOR.md](../REFACTOR.md) — Architecture decision records, frame protocol/verb definitions (§4)
- [doc/PEERSIGNAL.md](../PEERSIGNAL.md) — Signaling server deployment and protocol
- [doc/NETDISK.md](../NETDISK.md) — Netdisk link (node marketplace/sharing/cross-node saving)
- [doc/NODE.md](../NODE.md), [doc/NODE-API.md](../NODE-API.md) — Node concepts and node API
- [DISCOVERY-FALLBACK-LOBBY-SPEC.md](DISCOVERY-FALLBACK-LOBBY-SPEC.md) — Loopback node discovery and lobby fallback specification (#201)
- [SHA-ACL-INFERENCE-SPEC.md](SHA-ACL-INFERENCE-SPEC.md) — Content-addressed SHA-level ACL and source type inference specification (#202)
- [PEERJS-CONCURRENCY-MATRIX-AUDIT.md](PEERJS-CONCURRENCY-MATRIX-AUDIT.md) — PeerJS multi-client concurrency matrix audit specification (#205)
- [DOC-REFS-EVALUATION-SPEC.md](DOC-REFS-EVALUATION-SPEC.md) — doc-refs job trigger conditions and failure grading audit specification (#206)
