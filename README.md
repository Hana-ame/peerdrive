# Peerdrive
[English](README.md) | [简体中文](README.zh-CN.md)

[![Peerdrive CI](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml/badge.svg)](https://github.com/Hana-ame/peerdrive/actions/workflows/ci.yml)

Peerdrive is a multi-protocol file collection manager, supporting SHA256 content-addressed storage, URL references, P2P transport and BitTorrent downloads. Through the unified abstraction of **Collection + Provider**, it integrates local files, HTTP resources and PeerJS/WebRTC interconnection into a single system.

---

## Core concept

```
Collection = name + entries[]
Entry      = path + providers[]
Provider   = { type: "sha256" | "url", value: hash | url }
```

Strip away the independent "file" concept -- everything is a collection. A file = a single-entry collection + sha256 provider.

### Hybrid Interconnect & Multi-Source Retrieval

Rather than relying on legacy single-network DHTs, Peerdrive interconnects nodes via **PeerJS signaling + WebRTC DataChannels** with HTTP/MQTT presence discovery. Multi-protocol content retrieval routes across local storage, connected P2P peers, public IPFS HTTP gateways, optional BitTorrent DHT (`go-peerdrive-bt`), and upstream HTTP mirrors.

### Key tech stack

| Layer | Technology |
|----|------|
| HTTP | Gin |
| P2P | PeerJS signaling + WebRTC DataChannel (`back/peerjs/` go-peerjs; discovery: MQTT / self-hosted HTTP) |
| BT | `github.com/Hana-ame/go-peerdrive-bt` (back/p2p_bt, standalone library, opt-in via `PEERDRIVE_BT_ENABLE`) |
| Admin surface | Local WS admin verb (frontend all goes through `front/src/ws.js`) |
| Consumer | `packages/peerdrive-client` (zero-dependency pure browser consumer, goes through `share`/`req` frames; `packages/peerdrive-media` is a URL proxy aimed at img/video) |
| Storage | SQLite + content-addressed filesystem (raw SHA files on disk with DB-backed filename mapping) |
| Frontend | React 19 + Vite 8 + TailwindCSS 3 (3-tier navigation: ALWAYS, OWNER, GUEST) |

---

## Features: what you can do now

> All of the following capabilities have been run on this branch (`refactor`): backend unit tests **622** (16 packages) · integration 21 · frontend 101 · client 115 ·
> netdisk end-to-end script 12 assertions · panel real-browser 9 assertions · zero-config panel 3 assertions (new in v0.3.2) · sharing-level end-to-end 12 assertions ·
> admin-surface smoke 19 assertions (item-by-item list, commands and blind spots in `doc/testing/README.md`).
>
> **First run? Start with `peerdrive demo`** — see [Quick start](#quick-start) below. It needs no configuration.

### Usable without running a node (public panel `dist/panel.html`)

| Capability | Description |
|------|------|
| **No input at all** | Since **v0.3.2**, `http://127.0.0.1:<port>/panel` is embedded in the binary. The panel reverse-looks-up its own node id and that node's signaling from the server and connects on its own — **you don't fill in node id / signaling / key** (v0.3.1 and earlier all had to be copied by hand from the log) |
| Connect to a node | Fill in a node's peer id to connect directly, or "auto-search" lists nodes online on the signaling (hits the signaling's REST API, does not occupy a WebRTC connection) |
| See the other side's shared file links | After connecting, automatically pulls the `share` manifest: collections / files / directories. Only shows what the other side **explicitly declared** as open |
| Save · preview | "Save" = streaming pull + browser download; <=2MB can "preview". Task rows carry progress and cancel |
| Retrieve verification | The task row's "retrieve verification" re-pulls by hash and recomputes sha256 -- successful ingest != retrievable, this is the closed-loop evidence |
| Local ingest | Select local files for chunked upload (64KB/chunk, serial, one upload stream per connection), the node computes sha256, stores in CAS and returns the hash |
| Network ingest | Give it a URL and the node fetches and ingests it for you; SSRF protection only allows public http/https, intranet/local addresses are rejected (**rejection is expected behavior**) |

### Node operators

| Capability | Description |
|------|------|
| Content-addressed storage | Incoming content is written to disk by sha256 (raw SHA-named files without extension, original name in SQLite `file_index` / `file_meta`), naturally deduplicated |
| File index | `file_index` table persists sha256 -> absolute path, with a seq cursor for incremental sync (`sync` verb) |
| Multi-protocol content retrieval | The downloader routes by `local -> peer -> ipfsgw -> btdht -> http` (order and timeout configurable via `PEERDRIVE_DOWNLOAD_ORDER`) |
| Node market and joining | Discover nodes on the signaling, after joining write to `joined_nodes.json` and become a resident peer |
| External sharing scope | `share` verb; **default all off** -- without an explicit declaration, no manifest is exposed externally. Scope can be changed **at runtime**: by directory, by collection, or by checking individual files by hash (`GET/PUT /peerjs/share`, `POST /peerjs/share/files`), persisted to `storageDir/share_scope.json`, no restart needed; environment variables are only first-boot initial values. `share_only` policy allows both `share` manifest discovery and `req` data streams |
| Sharing level & protection | Each sharing declaration carries one level: `public` listed and downloadable / `unlisted` not listed but downloadable by hash / `private` only for self and friends (`ShareScope.Friends`). In addition, collections support three-tier access policies (`public`, `protected`, `private`) and extraction passcodes: protected collections hide file entries until unlocked with passcode |
| Quarantined Inbox sandbox | Remote peer uploads are quarantined into `storage/inbox/` (`is_inbox = true`) pending host operator review (`GET /files/inbox`, `POST /files/inbox/approve`, `DELETE /files/inbox/:hash`), preventing untrusted data from directly polluting storage |
| Outbound QoS guard | Outbound stream concurrency limiter (`PEERDRIVE_MAX_CONCURRENT_STREAMS`, default 8) and token bucket upload speed throttler (`PEERDRIVE_MAX_UPLOAD_SPEED`) |
| Dynamic capabilities | Plaza metadata announces active capabilities (`auth: open|psk`, `caps`, `shares_count`), dynamically negotiated over WebRTC DataChannel (`cap/cap-ack/cap-err`) |
| Access control | `PEERDRIVE_PSK`: once set, the peer must present the same key on the connection, otherwise all requests return `PSK_REQUIRED` |
| Admin surface | Local WS `admin` verb, internally reuses all of gin's HTTP controllers; the WebRTC side is intentionally not implemented, to prevent permission exposure |
| Port forwarding | `fwd-open/challenge/auth/data/close`, HMAC challenge auth + port whitelist (opt-in via `PEERDRIVE_PORTFWD_ENABLE`) |

---

## How each feature is implemented

| Feature | Code location | Mechanism |
|------|----------|------|
| Frame protocol | `back/internal/transport/conn.go` · `dispatchFrame` (L278) | **One verb table serves both WS and WebRTC**: `req/meta/data/done/err` pull files; `create/upload/list/info/delete/sync` file index; `share` sharing scope; `admin/admin-resp/admin-bin` admin surface; `fwd-*` port forwarding; `cap/cap-ack/cap-err` dynamic capabilities negotiation |
| Transport | `back/peerjs/` (standalone library `github.com/Hana-ame/go-peerjs`) | PeerJS signaling only forwards SDP/ICE, does not touch the data plane; data goes over WebRTC DataChannel |
| Discovery | `transport/http_discovery.go` / `mqtt_discovery.go` | Self-hosted HTTP discovery takes precedence over MQTT; there is also a fixed node-level "presence room", letting zero-shared-content nodes interconnect too |
| Content-addressed persistence | `service/anon_service.go` + `repository/file_index_repo.go` | Raw SHA files on disk + SQLite filename metadata; `hash[:2]` for CAS directory partitioning; hash length validated before writing |
| File index | `repository/file_index_repo.go` + `service/file_service.go` | SQLite persistence + seq cursor incremental; `sync` verb lets the peer pull only the delta |
| Cross-node save | `service/peerpull.go` + `GET/POST /p2p/pull*` | Streaming to disk -> recompute sha256 -> register index, with progress / cancel / dedup skip |
| Sharing scope | `service/nodeshare.go` + `share` verb + `GET/PUT /peerjs/share` | **Strictly distinguished** from `list`: `list` is the local admin index in full, only for trusted peers; `share` is the operator's explicitly declared external scope. Three sources take the union (directory / individual file hash / collection), modifiable at runtime and persisted; `share_only` policy allows both `share` and `req` |
| Quarantined Inbox | `controller/file_inbox.go` + `repository/file_index_repo.go` | Remote uploads are written to `storage/inbox/` with `is_inbox=1`, awaiting operator approval or deletion |
| QoS Guard | `back/internal/transport/qos.go` | Concurrency limit guard (`PEERDRIVE_MAX_CONCURRENT_STREAMS`) and token-bucket bandwidth limiter (`PEERDRIVE_MAX_UPLOAD_SPEED`) |
| Path safety | `back/internal/pathutil` | This is the only place for the check (`Within/WithinAny`); **read boundary != write boundary**; reads go through `SafeOpen` (`os.Root`), writes through `SafeWriteFileAny` etc., eliminating the TOCTOU of "check then open by path"; hard links judged by **the open handle's** `nlink` |
| Admission (PSK) | `transport/gate.go` | First frame `psk-auth` from this side after connection establishment; only gates verbs where "the peer asks me to do work", **never gates response frames**; `local` sessions are exempt |
| Consumer SDK | `packages/peerdrive-client/src/` | **Transport-agnostic**: only requires `{on, send, open, close}` to be passed in, this package does not import peerjs; ships its own **incremental** sha256 (WebCrypto's `digest()` is one-shot, conflicts with streaming pull) |
| Public panel | Same package `panel/` -> build artifact `dist/panel.html` | Single file, source inlined, opens via `file://`, can be hosted on any static space; after changing `src/` or `panel/` **must** `npm run build:panel` (CI `check:panel` catches drift) |
| Node admin console | `front/src/features/` | Operator view with 3-tier navigation (`ALWAYS_NAV`, `OWNER_NAV`, `GUEST_NAV`), calls the node's HTTP API, **requires the backend to be running** -- a different thing from the public panel |
| Online hosting | `https://hana-ame.github.io/peerdrive/` | Auto-deployed by `pages.yml` after push, and **verifies the online version after deploy** (compares this build's fingerprint, preventing verification of a previous version) |

---

## Netdisk pipeline (2026-09, `doc/NETDISK.md`)

Strings "interconnection + files" together into a pipeline users can follow, corresponding to stages 5/6 of the ROADMAP development order:

```
My node ──join──▶ Node market ──direct connect──▶ Other side's shared "file links" ──select & save──▶ My netdisk
                                                                (transfer task: progress / cancel)
```

| Stage | Implementation |
|---|---|
| Node market and joining | `service.NodeDirectory` + `GET /peerjs/nodes*`; joined list persisted (`joined_nodes.json`) and becomes a resident peer |
| Sharing scope | `share` frame + `PEERDRIVE_SHARE_ENABLE/COLLECTIONS/DIRS/FRIENDS` (**default all off**, without declaration no manifest is exposed externally); these are only **initial values**, modified at runtime via `/peerjs/share`, persisted to `share_scope.json`. Each declaration also has three levels `public/unlisted/private`. Collections support passcodes and `public`/`protected`/`private` access policies |
| Cross-node save | `service.PeerPuller` + `GET/POST /p2p/pull*`: streaming pull -> sha256 verify -> to disk -> register file index, with progress/cancel/dedup skip |
| Netdisk UI | **Public panel** `packages/peerdrive-client/dist/panel.html` (single-file static page, PeerJS direct connect to node, no server needed) + node admin console `front/src/features/` with 3-tier decoupled navigation |
| Node-less consumer | `packages/peerdrive-client`: both the panel and SDK originate from it, no local backend needed. The panel has a **fixed id** (`pd-panel-*`, stored in localStorage) -- `private`'s friend list recognizes this id, a random id is as good as filling the list for nothing; each manifest row can generate a **share link** (`?node=&hash=&auto=1`), the concrete action for `unlisted` |
| Who can connect to my node | **PSK gate** `PEERDRIVE_PSK` (optional): once set, the peer must present the same key on the connection, otherwise all requests return `PSK_REQUIRED` (`doc/NETDISK.md` §9) |

Progress, module breakdown, **plan vs actual deviations**, and verification results are all in `doc/NETDISK.md`; development order is in `doc/ROADMAP.md`.

---

## Quick start

> **Zero-configuration Chinese guide (recommended for first contact)**: [`doc/guide/single-binary-guide.md`](doc/guide/single-binary-guide.md)
> Step-by-step tutorial (download -> start a node -> connect with the panel -> what to do when it won't connect):
> [`doc/tutorial/01-run-and-connect.md`](doc/tutorial/01-run-and-connect.md)
> You do not need to deploy your own signaling: by default it connects to the public signaling `peersignal.moonchan.xyz` (wss).
> To change code / run the latest unreleased commit (skippable): [Appendix A · Compile from source](doc/tutorial/appendix-build-from-source.md)
> Full tutorial index (sharing levels · choosing what to share · cross-node save): [`doc/tutorial/README.md`](doc/tutorial/README.md)

```bash
# ── One binary since v0.3.0: server + signaling + registration are all subcommands of it ──
#   5 assets, no separate peersignal-* to download (see the subcommand notes in doc/guide/single-binary-guide.md)
gh release download --repo Hana-ame/peerdrive --pattern 'peerdrive-linux-amd64'
chmod +x peerdrive-linux-amd64

# ── Step 1 (zero configuration, run this first): self-hosted demo + two nodes, verify the whole chain ──
./peerdrive-linux-amd64 demo
#   Auto-selects ports, generates test data itself, prints the result step by step.
#   No Go / repo / Node / env vars needed. Works offline (self-hosted signaling).

# ── Step 2: start your own node ──
./peerdrive-linux-amd64 serve
#   On startup it prints "next step" directly, including a clickable panel URL:
#     http://127.0.0.1:3000/panel
#   Open it in a browser and you're done — node id and signaling are auto-detected by the panel itself.

# Run from source (when changing code)
cd back && go build -tags nosqlite -o peerdrive ./cmd/peerdrive/ && ./peerdrive

# Netdisk UI = public panel
#   Since v0.3.2 it's embedded in the binary, reachable via the /panel above — the highest priority way to open it
#   Standalone version (for connecting to someone else's node without a local node):
#     Online: https://hana-ame.github.io/peerdrive/   (auto-deployed after push)
#     Local: npm run build:panel generates dist/panel.html, open file:// to run
#   To connect directly to a specific node via params:
#     panel.html?node=<node peer id>&host=peersignal.moonchan.xyz&port=443&path=/
#                &key=pd-signal-1edf5e05e4a52b7351392574&secure=1&auto=1
#   Note: HTTPS pages (including the online version) can only use wss signaling, otherwise the browser blocks it as mixed content.
#   Online hosting self-check: node scripts/verify-pages.mjs

# Node admin console (for node operators: market / my nodes / transfer tasks, requires backend running)
cd front && npm run dev

# Minimal demo (needs an http server serving the package dir, for reference only)
cd packages/peerdrive-client && npm run demo   # http://127.0.0.1:8123/demo/consumer.html

# One-shot run of the whole netdisk pipeline (same flow as `peerdrive demo`; script version for CI/dev)
./scripts/netdisk-local-demo.sh                # --stop to stop

# Panel end-to-end self-check (real browser + real clicks, needs the above environment started first)
cd packages/peerdrive-client
SIG_HOST=<local IP> SIG_PORT=9100 NODE_ID=node-a node scripts/verify-panel.mjs

# End-to-end verification of the three sharing levels (fixed id / unlisted not in manifest but link fetchable / private rejects strangers and admits friends
# / collection whole-package link: recognizable in manifest · unlisted still fetchable by hash · private does not even give the manifest)
NODE_PORT=3001 SIG_PORT=9100 NODE_ID=node-a PW_CHANNEL=default node scripts/verify-panel-share.mjs

# Zero-config panel self-check (v0.3.2: open /panel with no params at all, assert the panel discovers its own node)
cd packages/peerdrive-client && PANEL_URL=http://127.0.0.1:3000/panel PW_CHANNEL=chromium \
  node scripts/verify-panel-zero-config.mjs

# Tests
cd back && go test -tags nosqlite ./... -count=1                                     # 622 (16 packages)
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1    # 21 passed / 4 skipped (offline, self-hosted signaling; must be -p 1)
cd back/signalserver && go test ./...            # 23 (standalone go.mod; ✅ go-build·submodules covered)
cd back/p2p_bt && go test ./...                  # 7 (standalone go.mod; ✅ same)
cd front && npx vitest run                       # 88
cd packages/peerdrive-client && npm test         # node --test (zero deps) 98
bash scripts/test-layers.sh                      # or run layer by layer per AOP (L1-L8 + LB)
node front/tests/e2e-admin-smoke.mjs             # 7 admin-surface assertions (needs node started first; CI already runs)

# Full list of test components, choices and blind spots: doc/testing/README.md
```

## Signaling server implementation

> **By default it connects to the project's public signaling `peersignal.moonchan.xyz` (wss, key `pd-signal-1edf5e05e4a52b7351392574`)**,
> node and panel defaults match, so it works out of the box, no signaling deployment needed.
> It is compatible with the PeerJS protocol (`back/signalserver` is its source, you can deploy your own to replace it):
> to self-host, change `PEERDRIVE_PEERJS_HOST/PORT/KEY` and `PEERDRIVE_DISCOVER_URL`,
> see tutorial appendix A.

```
wintools or any PeerJS-compatible signaling (independently deployed)
│  /peerjs            <- PeerJS-compatible WS signaling
│  /discover/announce <- Go/Web nodes check in on startup
│  /discover/nodes    <- node discovery
└───────────────┬────────────────────────────
                │ only forwards SDP/ICE, does not touch the data plane
┌───────────────▼────────────────────────────
peerdrive
│  back/peerjs  (Go PeerJS client + WebRTC DataChannel)
│  back/internal/transport/peerjs_service.go (file service / node interconnect)
│  front        (browser peerjs consumer)
```

- Go nodes: use `back/peerjs` to connect to the signaling, resident online, providing local file `list/read`.
- Web side: the browser's `peerjs` connects to the same signaling, connects to Go nodes on demand, consumes files.
- Signaling implementation choices:
  1. Use the online/local wintools Go signaling (current default)
  2. Use peerdrive's `back/signalserver` embedded or run independently
  3. Use the public PeerJS cloud (`0.peerjs.com`)
  4. Use Node.js or another PeerJS-compatible signaling

## Environment variables

> Full config in `back/internal/config/config.go` (`PEERDRIVE_*` prefix, defaults used when unset).

| Variable | Default | Description |
|------|--------|------|
| `PORT` | 3000 | HTTP port |
| `PEERDRIVE_STORAGE` | ./storage | Storage directory (content-addressed files) |
| `PEERDRIVE_STORAGE_ENABLE` | true | Storage enabled |
| `PEERDRIVE_MAX_UPLOAD_BYTES` | 100MB | Single-file upload cap |
| `PEERDRIVE_MAX_UPLOAD_ANON_BYTES` | 10MB | Anonymous upload cap |
| `PEERDRIVE_BT_DHT_ENABLE` / `PEERDRIVE_BT_DHT_LISTEN` | true / :6881 | BT DHT (standalone library go-peerdrive-bt) |
| `PEERDRIVE_IPFS_GATEWAY_ENABLE` / `PEERDRIVE_IPFS_GATEWAYS` | true / three gateways | IPFS gateway fallback |
| `PEERDRIVE_WEBRTC_STUN` / `PEERDRIVE_WEBRTC_TURN` | stun.l.google.com / - | ICE servers |
| `PEERDRIVE_PEERJS_ENABLE` | true | PeerJS signaling (interconnect layer) |
| `PEERDRIVE_PEERJS_HOST/PORT/KEY` | 0.peerjs.com/443/peerjs | Signaling server (can point at self-hosted peerserver) |
| `PEERDRIVE_PEERJS_ID` | randomly generated | Node peer id |
| `PEERDRIVE_PEERJS_SECURE` | true | Signaling wss |
| `PEERDRIVE_PEERJS_PEERS` | - | Comma-separated peers for auto interconnect |
| `PEERDRIVE_MQTT_ENABLE` / `PEERDRIVE_MQTT_BROKER` | false / tcp://broker.emqx.io:1883 | MQTT sharded-room discovery |
| `PEERDRIVE_MQTT_TOPIC_PREFIX` / `PEERDRIVE_MQTT_COLLECTIONS` | peerdrive/v1 / - | MQTT topic prefix / collections of interest |
| `PEERDRIVE_DISCOVER_MODE` | `auto` | Discovery mode: `auto` (HTTP if URL set, else MQTT), `peerjs`/`off` (no discovery, static `PEERDRIVE_PEERJS_PEERS` only — use with `PEERDRIVE_PEERJS_HOST=0.peerjs.com` for official signaling), `discover` (force HTTP), `mqtt` (force MQTT) |
| `PEERDRIVE_DISCOVER_URL` | - | Self-hosted discovery API (takes precedence over MQTT) |
| `PEERDRIVE_URL_SOURCE_TEMPLATE` | - | URL source template (%s=hash, multi-source fallback) |
| `PEERDRIVE_DOWNLOAD_DIR` | ./downloads | Download/registration directory (file_index root) |
| `PEERDRIVE_MAX_PEERS` | 8 | Interconnect peer cap |
| `PEERDRIVE_DOWNLOAD_ORDER` / `PEERDRIVE_DOWNLOAD_TIMEOUT` | local,ipfsgw,btdht,http / 30s | Downloader routing order / timeout (any permutation/subset of these four names) |
| `PEERDRIVE_FORWARD_RULES` | - | Port forwarding rules (`key:port,...`, chmod 600) |
| `PEERDRIVE_DISCOVER_PRESENCE` | true | Node-level "presence room" discovery (zero-shared-collection nodes can also interconnect) |
| `PEERDRIVE_SHARE_ENABLE` | **false** | External sharing master switch. Default off -- without explicit enable, no manifest is exposed externally |
| `PEERDRIVE_SHARE_COLLECTIONS` | - | Shared collections: comma-separated hashes or `all` (restricted/private always skipped) |
| `PEERDRIVE_SHARE_DIRS` | - | Shared directories: comma-separated. Empty = no file sharing (files only return basename, not absolute path) |
| `PEERDRIVE_SHARE_FRIENDS` | - | Friend node IDs: comma-separated, `private`-level content is allowed for them (see `model.LevelPrivate`) |

| `PEERDRIVE_PSK` | - | Node access pre-shared key. Empty = open (serves whoever connects); set = peer must present the same key to pull anything (`doc/NETDISK.md` §9) |
| `PEERDRIVE_ADMIN_TOKEN` | - | Local HTTP admin-surface Bearer token when no `PEERDRIVE_REG_SERVER` is configured. Empty + no reg server = auth disabled (loopback only); non-empty = `Authorization: Bearer <token>` required on authRequired routes |
| `PEERDRIVE_PEERJS_XOR_ENABLE` / `PEERDRIVE_PEERJS_XOR_KEY` | false / - | Data-plane XOR obfuscation (lightweight obfuscation for WebRTC traffic) |
| `PEERDRIVE_MAX_CONCURRENT_STREAMS` | 8 | QoS maximum concurrent outgoing data streams per node |
| `PEERDRIVE_MAX_UPLOAD_SPEED` | 0 | QoS upload speed limit in bytes/sec (token bucket throttler; 0 = unlimited) |
| `PEERDRIVE_PORTFWD_ENABLE` | false | Opt-in port forwarding capability switch |
| `PEERDRIVE_BT_ENABLE` | false | Opt-in BitTorrent protocol capability switch |
| `PEERDRIVE_IPFS_ENABLE` | false | Opt-in IPFS protocol capability switch |
| `PEERDRIVE_ANON_POLICY` | open | Anonymous peer policy (`open`, `share_only`, `deny`) |
| `PEERDRIVE_PEER_BLOCKLIST` | - | Comma-separated blocklisted peer IDs |
| `PEERDRIVE_RATE_LIMIT_RPS` | 30 | Per-IP HTTP rate limit requests per second (0 = unlimited) |
| `PEERDRIVE_CSP` | on | Content-Security-Policy header (`off` to disable) |
| `PEERDRIVE_SWAGGER` | on | Swagger API documentation at `/swagger/index.html` (`off` to disable) |
| `PEERDRIVE_AUTO_EXTRACT` | false | Auto-extract uploaded archives in sandbox directory |

> These `PEERDRIVE_SHARE_*` entries are only **first-boot initial values**: after startup they can be changed any time via `GET/PUT /peerjs/share`
> and `POST /peerjs/share/files` (by directory / collection / individual file hash), persisted in
> `PEERDRIVE_STORAGE/share_scope.json`; from then on that file is authoritative, changing env vars will not overwrite
> already-made choices (see `doc/NETDISK.md` §12.6, tutorial chapters 3 and 4).

## Security status after startup (added 2026-10-04)

> **The node prints a security status summary after startup.** Out of the box, several switches are **off** (= open),
> and previously there was **no prompt at all** — you had to know the implementation details to find out it was open.
> Now you can check the log directly right after starting:

```
security: 4 open item(s) need operator attention, 1 informational item(s)
security: [open] inbound P2P has no gate (PEERDRIVE_PSK empty) — ...
security: [open] HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty) — ...
security: [open] node is present in the public roster (PEERDRIVE_DISCOVER_PRESENCE=true) — ...
security: [open] Swagger API docs are public (PEERDRIVE_SWAGGER not off) — ...
security: [info] external sharing not enabled (PEERDRIVE_SHARE_ENABLE not true) — ...
```

Once everything is configured, it becomes one line:

```
security: all gates closed (inbound PSK / admin-surface auth / public roster / Swagger)
```

**What each item means / how to close it**

| Item | Meaning | How to close |
|---|---|---|
| `PEERDRIVE_PSK` empty | Once the peer id is known, anyone can connect and pull share content | Set `PEERDRIVE_PSK`; or bind `PEERDRIVE_HOST=127.0.0.1` |
| `PEERDRIVE_REG_SERVER` empty | List/delete files, change sharing scope, read Swagger — all open without a credential | Set `PEERDRIVE_REG_SERVER`; or bind `PEERDRIVE_HOST=127.0.0.1` |
| `PEERDRIVE_DISCOVER_PRESENCE=true` | peer id and sharing summary are announced to the signaling server, and strangers can call back | Set `PEERDRIVE_DISCOVER_PRESENCE=false` |
| `PEERDRIVE_SWAGGER` not off | `/swagger/index.html` publishes the endpoint and parameter structure of all 105+ endpoints without a credential | Set `PEERDRIVE_SWAGGER=off` |

> **Why `PEERDRIVE_PSK` is not forced by default**: forcing it would make every existing deployment fail to start
> (the env block in `doc/tutorial/01-run-and-connect.md` §1.3 has no PSK, and the public panel does not present one by default),
> CI has no coverage of the PSK path, and PSK itself is a **shared key with no identity** — it can only answer
> "does the other side know this key", not "who is the other side" (see `back/internal/transport/gate.go`).
> So: report first, then add gates as the deployment scenario requires.

> **About `PEERDRIVE_AUTH_TOKEN`**: it has been **removed** (2026-10-04). It was never an inbound gate to begin with —
> in the original implementation (`539efc5`) it was the **outbound** identity this node used when reporting to the
> registration server, and deleting the libp2p stack (`a5b090d`) left the field with no consumer.
> The admin surface's actual auth switch is `PEERDRIVE_REG_SERVER`.

## Subsystems and Standalone Libraries

The Peerdrive monorepo hosts several independent, reusable packages and standalone libraries:

| Subsystem | Description | Path |
|-----------|-------------|------|
| `go-peerjs` | Pure Go PeerJS protocol client and WebRTC DataChannel transport (`github.com/Hana-ame/go-peerjs`). | `back/peerjs/` |
| `go-peersignal` | Lightweight self-hosted Go signaling and discovery server (`github.com/Hana-ame/go-peersignal`, binary `cmd/peersignal`). | `back/signalserver/` |
| `go-peerdrive-bt` | Mainline BitTorrent DHT capability (`github.com/Hana-ame/go-peerdrive-bt`), opt-in bridge. | `back/p2p_bt/` |
| `peerdrive-client` | Zero-dependency pure browser consumer SDK and single-file web panel (`dist/panel.html`). | `packages/peerdrive-client/` |
| `peerdrive-media` | WebRTC DataChannel URL proxy for direct in-browser audio, video, and image streaming. | `packages/peerdrive-media/` |
| Active Core Services | Unified multi-protocol retrieval (`back/internal/provider/`), content-addressed collection schema (`back/internal/model/anon.go`), and interconnect transport (`back/internal/transport/peerjs_service.go`). | `back/internal/` |

> Note: The legacy libp2p stack was completely removed on 2026-08-16 (see `doc/REFACTOR.md` §8 and `doc/archive/LEGACY.md` for historical migration records).
