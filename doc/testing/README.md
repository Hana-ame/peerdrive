# Testing Components Overview

> Updated: 2026-09-20 · Data from this branch (`refactor` @ `e4bd3ed`) measured
> Environment: WSL (`go1.22.2` / `node v22.23.1` / vitest 4); CI side uses go 1.26.
> Related: [Layered docs](../layers/README.md) · [Netdisk manual test guide](../NETDISK.md#7-local-runthrough-manual-test-guide) · [AGENTS.md build & verification](../../AGENTS.md)

---

## 0. In One Sentence

The project has **15 test components**, distributed across 4 go modules (back main module + peerjs / signalserver / p2p_bt three independent go.mod), 3 npm projects (front / peerdrive-client / peerdrive-media), plus 2 local scripts and 5 CI workflows (3 of which run tests, see §3.15). They are **validation at different levels** and cannot substitute for each other: unit tests generally use injected fake dependencies, and the only thing that actually runs the entire chain end-to-end is the end-to-end scripts (§3.14).

> The lesson of 2026-09-20 (`NETDISK.md` §7.3): CI and all unit tests were green, but because nobody ran the "register file → the other side pulls the list → cross-node pull" combination, two critical defects (join-to-disk ENOENT, `RegisterLocal` not writing `file_index`) hid until manual end-to-end testing.
> **What was missing was not the number of test cases, but end-to-end combination coverage.**

---

## 1. I Changed X, Which Tests Should I Run?

| You changed | Must run | Suggested add |
|---|---|---|
| Any single package logic in `back/internal/**` | `cd back && go test -tags nosqlite ./... -count=1` (622, 16 packages) | `bash scripts/test-layers.sh` to pinpoint to a specific layer |
| `back/internal/transport/**` frame protocol | Same as above (transport 86) | Integration tests (21) |
| **Netdisk chain** (`nodes/share/pull/RegisterLocal`) | Integration tests + **`./scripts/netdisk-local-demo.sh`** (manual, must run once) | Browser UI manual test (`NETDISK.md` §7.2) |
| `back/peerjs/**` | `cd back/peerjs && go test ./... -count=1 -race` (23) | — |
| `back/signalserver/**` (peersignal) | `cd back/signalserver && go test ./...` (23) | ✅ `go-build` `submodules` matrix (added 2026-09-21) |
| `back/p2p_bt/**` | `cd back/p2p_bt && go test ./...` (7) | ✅ Same as above |
| `front/src/**` | `cd front && npm test` (101) + `npm run build` | `front/tests/e2e-admin-smoke.mjs` (admin plane, CI already runs); the other two `.mjs` manually (depending on change scope) |
| `packages/peerdrive-client/**` | `npm test` (115) + `npm run check:panel` | `node packages/peerdrive-client/scripts/verify-panel.mjs` (real browser end-to-end); `packages/peerdrive-client/scripts/verify-panel-share.mjs` (share levels end-to-end, requires node + signaling); after merge `pages.yml` will **automatically** verify online (§2 item 11) |
| `packages/peerdrive-media/**` | `npm test` (21) + `npm run build` | `test/e2e-browser.mjs`, `test/media-node-e2e.mjs` |
| Preparing to merge into `refactor` | All must-run items above all green (= same commands as CI) | Run again on trunk after merge |

---

## 2. Full Picture Table (2026-09-20 Measured)

Unit "test cases" = go side `-json` pass lines with `Test` (including subcases); node side is framework self-reported.

| # | Component | Location | Command | Cases | In CI | Needs External Network |
|---|------|------|------|------|-------|--------|
| 1 | Backend unit/package tests | `back/` (main module) | `go test -tags nosqlite ./... -count=1` | **622** (16 packages) | ✅ `backend` + `go-build`×4 | ❌ |
| 2 | Backend integration tests | `back/test/integration/` | `go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1` | **22** pass / 4 skipped | ✅ `integration` | ❌ (self-hosted signaling) |
| 3 | External network integration (manual gated) | Same as 2 | `PEERDRIVE_MQTT_TEST=1` / `PEERDRIVE_LIVE_TEST=1` | 4 test cases | ❌ | ✅ (direct connection, no proxy) |
| 4 | peerjs module | `back/peerjs/` (independent go.mod) | `go test ./... -count=1 -race` | **23** | ✅ `peerjs` | ❌ |
| 5 | signalserver module | `back/signalserver/` (independent go.mod) | `go test ./... -count=1` | **23** | ✅ `go-build`·`submodules` | ❌ |
| 6 | p2p_bt module | `back/p2p_bt/` (independent go.mod) | `go test ./... -count=1` | **7** | ✅ `go-build`·`submodules` | ❌ |
| 7 | Frontend vitest | `front/tests/*.test.{js,jsx}` | `npm test` | **101** (9 files) | ✅ `frontend` (includes build) | npm ci needed |
| 8 | Frontend admin plane smoke | `front/tests/e2e-admin-smoke.mjs` | Start node locally then `node front/tests/e2e-admin-smoke.mjs` (hits `ws://localhost:3000/ws/peer`) | **19** assertions | ✅ `e2e.yml` (added 2026-09-21, no external network) | ❌ |
| 8b | ~~Two other frontend manual scripts~~ | ~~`front/tests/{playwright-smoke,pw-settings-mobile}.mjs`~~ | — | — | — **已删除（2026-10-06 核实：两个脚本均不存在）** | — |
| 9 | client package unit tests | `packages/peerdrive-client/` | `npm test` (zero dependencies) | **115** | ✅ `client-package` | ❌ |
| 10 | client public panel + browser self-check | `packages/peerdrive-client/{dist,scripts}` | `npm run check:panel`·`node scripts/verify-panel.mjs` | Panel 9 assertions | ✅ `check:panel` (artifact consistency) | ✅ **peerjs no longer fetched from CDN since v0.3.2** (embedded in the binary; CI also byte-compares the two copies) |
| 11 | Online hosting self-check (Pages) | `packages/peerdrive-client/scripts/verify-pages.mjs` | `node scripts/verify-pages.mjs` | Online 5 assertions | ✅ `pages.yml`·`verify` (deploy then verify back, added 2026-09-21) | ✅ (verifies the online version) |
| 12 | media package unit tests | `packages/peerdrive-media/` | `npm test` + `npm run build` | **21** | ✅ `media-package` | npm ci needed |
| 13 | media browser E2E (2) | `packages/peerdrive-media/test/{e2e-browser,media-node-e2e}.mjs` | playwright runner | 10 assertions + … | ❌ **(blind spot)** | ✅ (twimg images + peerjs CDN, also needs media-node running) |
| 14 | Layered aggregation script | `scripts/test-layers.sh` | `bash scripts/test-layers.sh [--integration]` | Aggregates L0-L8 + L-sec | ❌ (local aggregation) | — |
| 15 | Netdisk end-to-end script | `scripts/netdisk-local-demo.sh` | Starts 3 processes running full chain | 12 assertions | ✅ **`e2e.yml`** | ❌ (no external network, local processes) |
| 16 | Panel browser end-to-end | `packages/peerdrive-client/scripts/verify-panel.mjs` | Real browser clicks on panel | 9 assertions | ✅ **Same `e2e.yml` (reuses previous environment)** | ❌ |
| 17 | **Zero-config panel** (new in v0.3.2) | `packages/peerdrive-client/scripts/verify-panel-zero-config.mjs` | Opens `/panel` with **zero params**, asserts the panel reverse-looks-up its own node and connects | 3 assertions | ✅ `e2e.yml` (independent node, no PSK) | ❌ |
| 18 | Live node traversal probe | `scripts/netdisk-traversal-probe.sh` | Live binary + HTTP probe with traversal payloads | 29 assertions | ✅ `ci.yml` `security-probes` | ❌ |
| 19 | External share directory full-chain | `scripts/netdisk-sharedir-outside.sh` | Live nodes sharing external media directory | 6 assertions | ✅ `ci.yml` `security-probes` | ❌ |
| 20 | Full-chain lifecycle E2E | `back/test/integration/e2e_full_chain_test.go` | Full publisher -> peer -> consumer chain + security | 3 comprehensive E2E tests | ✅ `ci.yml` `integration` | ❌ (in-process signaling) |

**Total coverage** (2026-10-10 retested): automated (CI) covers 622 + 24 + 101 + 115 = **862**;
Previously added to CI: signalserver (23) · p2p_bt (7) · Pages online self-check (5) · admin plane smoke (19) · security probes (`netdisk-traversal-probe.sh` + `netdisk-sharedir-outside.sh`) · full-chain lifecycle E2E (`e2e_full_chain_test.go`).
Since v0.3.2 the `media-package` job covers the client package; the standalone `signalserver` (23) count
still applies only when building that submodule separately.

> ⚠️ **Note on `go build`**: `-tags nosqlite` is mandatory (three-way SQLite CGO conflict); plain
> `go build ./...` fails at link time. This is a known build constraint, not a code defect.
Still outside CI: 4 external-network gated cases, `front/tests/*.mjs` (2 remaining) · media browser E2E (2) · client demo page —— they either depend on external sites or require manual service startup first, see §4.

---

## 3. Component-by-Component Description

### 3.1 Backend Unit/Package Tests (622, 16 packages)

- **Responsibility**: single-package behavior correctness, dependencies use injected/temporary directory substitutes (e.g. fake `fileList`, fake transport).
- **Command**: `cd back && go build -tags nosqlite ./... && go test -tags nosqlite ./... -count=1`
- **Package distribution** (2026-09-21 measured): transport 119 · service 108 · controller 98 · pathutil 96 ·
  source 32 · config 24 · downloader 20 · provider 17 · model 13 · repository 13 · server 7 · router 3
  (pathutil and the four-layer penetration matrix are new from 2026-09-20/21, so it grew the most)
- **New modules (2026-10-09)**: `internal/collection`（多备选 source + metadata 扩展、Fetch/监视）
  and `internal/twitterpic`（twitter-pic 图库整合：user→collection 生成器 + 文件源访问监视）
  carry their own package tests (`go test -tags nosqlite ./internal/collection/ ./internal/twitterpic/ -count=1`),
  plus controller tests `TestTwitterPic*` and config default tests.
- **Prerequisite**: `-tags nosqlite` is a hard constraint (dual SQLite driver CGO conflict).
- **What it cannot prove**: injected fake dependencies make cross-module combinations like "the index A just wrote is exactly the table B reads" **invalid** —— this is precisely why the `file_index` defect escaped.

### 3.2 Backend Integration Tests (21 pass / 4 skipped)

- **Responsibility**: real signaling + real frames + same-machine WebRTC. `TestMain` starts a global self-hosted signaling, **no external network**.
- **Command**: `cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1`
- **⚠️ Must use `-p 1`**: multiple test groups share the same global signaling, parallel execution will interfere with each other (hard constraint in `AGENTS.md`).
- **Currently skipped 4** (gates not opened, CI defaults to not opening external network gates):
  `TestMQTTDiscovery`, `TestMQTTDiscoverThenPeerJSInterop`, `TestLiveSignal_DiscoveryAndInterop`, `TestLiveSignal_ProtocolCompat`.
- **Files**: `interop/selfhosted/ws/ws_verbs/share_protocol/peer_pull/file_lifecycle/mqtt/live` 8 `_test.go` files total.

### 3.3 External Network Integration (Manual Gated)

```bash
PEERDRIVE_MQTT_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestMQTT -v   # Public broker (via proxy)
PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v   # Online signaling (**direct connection, no proxy**)
PEERDRIVE_SKIP_RTC=1  ...                                                                          # No UDP sandbox (e.g. docker default)
```
- cloudcone 443 **does not go through the host proxy** (the proxy times out connecting to it), it's the only exception on this chain.

### 3.4 `back/peerjs` (23)

- Independent go.mod (`github.com/Hana-ame/go-peerjs`), main repo `replace` points to local.
- **Main repo `go test ./...` cannot find it** —— must run separately. CI has a dedicated job, don't forget locally.
- Recommended with `-race` (flow control/concurrency related).

### 3.5 `back/signalserver` (23)

- Independent go.mod (`github.com/Hana-ame/go-peersignal`), the self-hosted signaling service body (source of the `peersignal` command).
- Skipped by `cd back && go test ./...` (main repo pattern cannot find it), from 2026-09-21 onwards covered by
  `go-build.yml` `submodules` matrix (previously only half-covered in the release gate).
- Local: `cd back/signalserver && go test ./... -count=1`.

### 3.6 `back/p2p_bt` (7)

- Independent go.mod (`github.com/Hana-ame/go-peerdrive-bt`), Mainline DHT / BEP44 / BEP51.
- Previously **had no CI job at all** (broke it and it stayed green), from 2026-09-21 onwards also covered by `submodules` matrix.
  `scripts/test-layers.sh` L7 will also pick it up.

### 3.7 Frontend vitest (101 / 9 files)

- **Command**: `cd front && npm test` (`vitest run`) + `npm run build` (`vite build`)
- **Config**: `vitest.config.ts` (happy-dom + `@vitejs/plugin-react`, setup file `tests/setup.js`)
- **File details**: `netdisk.test.jsx` 45 · `components.test.jsx` 18 · `ws.test.js` 14 · `smoke.test.jsx` 1 · `FileTree/LeftPanel/VisibilityPicker` total 13
- **2026-10-07: the `api` and `api-mock-sync` test files were deleted** along with the zero-importer HTTP wrapper module they guarded (that module + its hand-written mock). They contributed 23 of the cases above, so the "(101 / 9 files)" in this section's header and the per-file totals need a refresh against a real `npm test` run — this branch deliberately did not run the suite locally.
- **⚠️ Only collects `*.test.{js,jsx}`**, `.mjs` under `tests/` is not in vitest's scope (see §3.8).
- **Do not** add `--reporter=basic` (this version of vitest doesn't have that reporter).

### 3.8 Frontend Manual Scripts (3 `.mjs`) — 1 now in CI, 2 still blind spots

| Script | What it does | Prerequisite |
|---|---|---|
| `front/tests/e2e-admin-smoke.mjs` | Node 22 native WebSocket hits local `ws://localhost:3000/ws/peer`, runs admin verb to verify admin plane full chain (including binary frames, `binaryType='arraybuffer'`, 15s timeout). **From 2026-09-21 onwards run by `e2e.yml`** (no external network, no `npm ci` needed): ping / binary upload / download by hash / anonymous collection / file list / share scope (checkbox·level·friend·directory round-trip) / unknown name route 404, total 19. ⚠️ `/ws/peer` belongs to peerjs route group, when `PEERDRIVE_PEERJS_ENABLE=false` this path returns 404 directly, the script will "silently exit with 0 assertions" —— peerjs must be enabled when starting the node. | ✅ `e2e.yml` (port hardcoded to 3000; use `E2E_WS_URL` to override for local hits on other ports) |
| `front/tests/playwright-smoke.mjs` | Runs UI assertions on **online** `https://peerdrive.pages.dev` (three-column layout, tab bar, collection interactions) | playwright runner |
| `front/tests/pw-settings-mobile.mjs` | Mobile viewport (375×667) screenshot/assertions on preview domain | playwright runner |

How to run: `node ~/.claude/skills/playwright-test/scripts/test-runner.mjs front/tests/<script>` (local Firefox).

### 3.9 `packages/peerdrive-client` Unit Tests (115)

- **Command**: `cd packages/peerdrive-client && npm test` (`node --test "test/*.test.mjs"`, 35 suites)
- **Zero runtime dependencies** ⇒ no `npm ci` needed, no network needed. Coverage: protocol state machine + incremental SHA-256 + client API.
- The self-implemented **incremental** SHA-256 (`src/sha256.js`) must not be changed to only use `crypto.subtle.digest()` (one-shot, conflicts with streaming pull).
- **PSK gate** (`test/psk.test.mjs`, 9 cases): nails down the "presentation timing" —— `psk-auth` must be the **first frame** on this connection (before `share`/`req`), and the state transitions of `psk-ok`/`psk-err`, error classification of `code=PSK_REQUIRED`. The gate's failure mode is silent (sent late = peer doesn't respond at all = only timeout remains), so this must be nailed down. The peer (Go) side mirror test cases are in `back/internal/transport/psk_test.go` (8 cases).

### 3.10 Public Panel (`dist/panel.html`) + Browser Self-Check

The netdisk UI form is a **single-file public panel** (can open via file:// or any static hosting, no local server needed),
so it's not in `npm test` coverage, there are two separate paths:

| Command | What it does | In CI |
|------|--------|-------|
| `npm run build:panel` / `check:panel` | Generates `dist/panel.html` by inlining from `src/`; `--check` verifies artifact matches source (prevents drift) | ✅ `client-package` runs `check:panel` |
| `node scripts/verify-panel.mjs` | Real browser (defaults to reusing local Edge) opens `file://` artifact, asserts: peerjs loads → connects to node → manifest → click "Save" really downloads → click "Preview" has content → sha256 matches manifest | ❌ Manual |
| `node scripts/verify-pages.mjs` | Verifies **online hosting** (defaults to <https://hana-ame.github.io/peerdrive/>): panel skeleton / bundle injection / peerjs fetchable / HTTPS+`ws://` mixed content warning / no JS errors. Complementary to the above —— one verifies functionality, one verifies deployment | ✅ `pages.yml`·`verify` (runs after deploy, added 2026-09-21) |
| `node scripts/verify-panel-share.mjs` | **Share policy** browser end-to-end (12 items, needs node + signaling + playwright): fixed id → unlisted not in manifest but link fetchable → private rejects strangers → fixed id enters friend list then same page immediately fetchable → **collection whole-package link** (manifest identifies as collection and lists entries / unlisted fetches manifest by hash / private doesn't even give manifest). ⚠️ Assertion count grew from 7 to 12 with the 4 collection items | ❌ Manual |

- Prerequisites: nodes + signaling started by `./scripts/netdisk-local-demo.sh`; install `playwright-core` (not in this package's dependencies).
- These two pits only expose themselves in a real browser, so browser verification is mandatory: **peerjs CDN load failure** (added multi-source fallback + `npm run vendor:peerjs` for offline use),
  **signaling not enabling CORS** (file:// origin is `null`, `GET /peerjs/id` gets swallowed, PeerJS only reports vague `server-error`;
  now handled by `back/signalserver`'s `allowCORS`).

### 3.10.1 Minimal Demo `demo/consumer.html` (Manual, needs static server)

```bash
npm run demo        # node scripts/serve.mjs → http://127.0.0.1:8123/demo/consumer.html
npm run demo:signal # peerjs --port 9100 (requires devDependency installed to run)
```
The manual acceptance entry for "fetching files from P2P network without running a local node."

### 3.11 `packages/peerdrive-media` Unit Tests + Build (21)

- `npm ci` → `npm test` (21) → `npm run build` (IIFE/ESM/CJS three builds, dist committed)
- `test/protocol.test.mjs`, `test/e2e.test.mjs` (includes node↔node WebRTC E2E), `test/core.test.mjs` (mock peerjs queue boundaries)
- **⚠️ `npm ci` is very sensitive to lockfile sync**: `@vitejs/plugin-react` treats react as a required peer, placing it only in
  `peerDependencies` causes `EUSAGE` —— `react`/`react-dom` are now written into devDependencies.

### 3.12 `packages/peerdrive-media` Manual E2E (10+ assertions)

- `test/e2e-browser.mjs`: mount/load/video/whitelist/dispose rebuild, needs local Firefox + playwright runner
- `test/media-node-e2e.mjs`: **name looks like Node side, but is actually browser E2E too** (consumes playwright runner passed
  `page`/`ok`). Prerequisites: Go `media-node` running + static server :5176, and needs to actually load a twimg image
  ⇒ doesn't match `*.test.mjs` glob, **also uses external network**, so not in CI short-term.
  Also has independent entry `packages/peerdrive-media/scripts/run-media-node-e2e.mjs` (with proxy connecting to remote peersignal)
- Pits and browser E2E seven-serial (including serial slot empty-occupation three entries) see `REFACTOR.md` §3.11

### 3.13 `scripts/test-layers.sh` (Layered Aggregation)

Runs layer by layer per AOP (L1-L8 + LB + optional INT), aggregates at the end, any layer failure exits nonzero (does not stop mid-way).
Script includes go proxy env, also automatically adds highest-version nvm node to PATH; failure logs in `/tmp/layer-test-<layer>.log`.

```bash
bash scripts/test-layers.sh              # L1-L8 + LB
bash scripts/test-layers.sh --integration  # Adds real signaling integration section (-p 1 serial)
```

⚠️ Depends on `${BASH_SOURCE[0]}` to locate repo root, don't feed stdin with `bash -s < script` (will parse to `/`):
`cd <repo root> && bash scripts/test-layers.sh`, or run inside WSL when using WSL.

**Three issues fixed on 2026-09-20 (before the fix, this script would silently FAIL / miss runs)**:

| # | Issue | Current |
|---|---|---|
| 1 | **L6 path wrong**: script writes `./internal/signalserver/...`, but signalserver is actually in `back/signalserver` (independent go.mod) ⇒ pattern doesn't exist, direct error exit, this layer always FAIL, these 21 cases were never executed | ✅ Changed to `cd back/signalserver && go test ./...` |
| 2 | **Missed 57 cases**: `config`(24) / `model`(13) / `provider`(17) / `router`(3) don't belong to any L1-L8 layer, `go test ./...` runs them but the layered script doesn't | ✅ Added `LB-baseline` layer |
| 3 | **L8 false FAIL in WSL**: script didn't inject nvm PATH, `node: command not found` | ✅ Script automatically adds highest-version nvm node to PATH |

After fix, measured: **all 9 layers green** (L1 23 / L2 74 / L3 12 / L4 161 / L5 12 / L6 21 / LB 57 / L7 7 / L8 88), 1m20s.

Current layered scale (2026-09-20 measured):

| Layer | Aspect | Command | Cases |
|----|------|------|------|
| L1 | Signaling/transport primitives | `cd back/peerjs && go test ./... -count=1 -race` | 23 |
| L2 | Frame protocol | `go test -tags nosqlite ./internal/transport/ -skip "^TestAdmin"` | 74 |
| L3 | Admin plane | `go test -tags nosqlite ./internal/transport/ -run "^TestAdmin"` | 12 |
| L4 | Business core | `go test -tags nosqlite ./internal/{controller,service,source,downloader}/...` | 161 |
| L5 | Data | `go test -tags nosqlite ./internal/repository/...` | 12 |
| L6 | Discovery | `cd back/signalserver && go test ./...` (independent go.mod, **cd in and run**) | 23 |
| LB | Base packages | `go test -tags nosqlite ./internal/{config,model,provider,router}/...` | 57 |
| L7 | External capabilities | `cd back/p2p_bt && go test ./...` | 7 |
| L8 | Frontend | `cd front && npm test` | 88 |
| INT | Real signaling integration | `go test -tags "nosqlite integration" ./test/integration/ -p 1` | 21 / skip 4 |

### 3.14 `scripts/netdisk-local-demo.sh` (End-to-End, 8 assertions)

The only component that **completely runs through the netdisk chain**: starts `peersignal :9100` + `node-a :3001` (shared) + `node-b :3002` (consumer),
asserts in sequence: marketplace discovery → join (including `joined_nodes.json` to disk) → share frame manifest non-empty → cross-node pull → sha256 match.

```bash
./scripts/netdisk-local-demo.sh          # Start environment and auto-run once
./scripts/netdisk-local-demo.sh --stop   # Stop
```
Completely no external network. **Must run when modifying netdisk chain** (AGENTS.md hard requirement). Not yet connected to CI (`NETDISK.md` §7.5).

### 3.15 CI Workflow (Local↔CI Mapping)

| workflow | Trigger | job | Components covered |
|---|---|---|---|
| `ci.yml` | push to main/master/refactor/base/docs/`feat/*`/`fix/*`/`module/*`/`*-agent`/`*-frontend` **or tag `v*`**; PR to main/master/refactor | 6 | ①Backend unit (vet+test+build) ②Integration (`-p 1`) ④peerjs (vet+test) ⑦Frontend (test+build) ⑨client ⑪media (ci+test+build) |
| `go-build.yml` | push / PR (any branch) | 5 platform matrix | ①Backend unit×4 (test only on non-Windows) + cross-compiled artifacts |
| `release.yml` | push tag `v*`; manual dispatch (`dry_run` on by default, only verifies gate, doesn't release) | 3 | **`gate` (backend vet+test+build · integration · signaling · peerjs · client+check:panel) → build (5 platforms) → release** |
| `e2e.yml` | push to `refactor` with changes in `back/**`·`packages/peerdrive-client/**`·scripts/workflows; PR; manual dispatch | 1 (sequentially reuses same environment) | ⑭Netdisk E2E script (8) + ⑮Panel browser E2E (9, bundled chromium) |
| `pages.yml` | push to `refactor` with changes in `packages/peerdrive-client/**`; or manual dispatch | 2 | **Not a test**: builds panel and deploys to GitHub Pages (<https://hana-ame.github.io/peerdrive/>). It also runs `check:panel`, so it also catches "changed `src/` but forgot to rebuild artifacts" |

**Release gate (added 2026-09-20)**: `ci.yml`'s `on.push` added `tags: ['v*']` (tag pushes also run
all 6 jobs), and `release.yml` itself has an extra `gate` job —— `build` and `release` both
`needs: gate`, **tests not passing means no package is released**. Why both sides: there's no `needs` between workflows,
`ci.yml` and `release.yml` run in parallel, only a gate on release itself actually blocks.

Verification method (no real release, avoiding tag/release history pollution):

```bash
gh workflow run release.yml --ref refactor -f dry_run=true   # dry_run defaults to true, release step will skip
gh run watch                                                 # gate 2m39s → build×5; measured success
```

> Also fixed an existing red light: the windows matrix cell was always failing —— cross-compilation was written as `GOOS=windows go build`,
> but `windows-latest`'s default shell is PowerShell, the prefix assignment syntax gets treated as a command name.
> Now changed to `env:` passing `GOOS/GOARCH/CGO_ENABLED`.

---

## 4. What Automation Doesn't Cover (Blind Spot Checklist)

By risk from high to low:

| Blind spot | Why it's dangerous | Current |
|---|---|---|
| ~~End-to-end combination (register → manifest → pull → verify)~~ | Both 2026-09-20 critical defects hid here | ✅ **Now covered by `e2e.yml`** (chain 8 + panel browser 9) |
| ~~**`back/signalserver`** (23)~~ | Independent go.mod + no CI job ⇒ signaling service breaks and CI stays green | ✅ **Added 2026-09-21**: `go-build.yml` added `submodules` matrix |
| ~~**`back/p2p_bt`** (7)~~ | Same as above (this one wasn't even covered by the release gate) | ✅ **Added 2026-09-21**: same as above |
| ~~`front/tests/e2e-admin-smoke.mjs`~~ | Admin plane (admin verb forwarding + binary frames) had no regression | ✅ **Added 2026-09-21**: `e2e.yml` starts a `:3000` node then runs (7 assertions) |
| **External network integration 4 cases** | Public broker / online signaling protocol compatibility has no verification | Gated manually |
| `front/tests/*.mjs` 2 remaining | Asserts on online/preview sites, running in CI is like health-checking someone else's deployment | Manual |
| media's 2 non-`*.test.mjs` E2E | Browser real rendering path not in `npm test`; also both use external network (twimg images + peerjs CDN), and need manual `media-node` startup first | Manual (not in CI short-term) |
| client demo page (:8123) | Last line of defense for consumer real-user usability | Manual |
| **Public panel browser self-check** | `dist/panel.html` is a single file via file:///static hosting, unit tests can't touch it at all; peerjs CDN loading, signaling CORS, real click-save can only be verified here | Manual `packages/peerdrive-client/scripts/verify-panel.mjs` (8 assertions, already passing) |
| ~~**Online hosting itself** (Pages deploy)~~ | Same as above | ✅ **Added 2026-09-21**: `pages.yml` added `verify` job (`needs: deploy`, probes release propagation first, then verifies, 3 retries on full failure) |
| ~~**Tag release**~~ | Previously: `ci.yml` didn't include tags, `release.yml` only built ⇒ release had no gate | ✅ **Added 2026-09-20**: `ci.yml` added tags trigger + `release.yml` has its own `gate` (see §3.15) |
| **PSK gate can only be verified in real network** | Frame-level behavior has unit tests, but "with/without key actually being blocked/allowed on real WebRTC" must run the chain | Manual: `netdisk-local-demo.sh` step [6] (A with key → B without key blocked → B with key recovers); could consider connecting to `e2e.yml` |

---

## 5. Before Running: Environment Conventions

```bash
# Proxy (go package fetching/dependency downloading; cloudcone 443 exception, must connect direct)
export HTTPS_PROXY=http://172.29.80.1:10809
export GOPROXY=https://goproxy.cn,direct
# In WSL, node is not in default PATH (vite 8 requires node ≥ 22.12)
export PATH="$HOME/.nvm/versions/node/v22.23.1/bin:$PATH"
```

- Windows side bash tools are limited ⇒ run via WSL:
  `ssh -i ~/.ssh/id_rsa lumin@127.0.0.1 "tr -d '\r' | bash -s" < script`
  (CRLF in scripts will make bash report `$'\r': command not found`, so `tr -d '\r'` is mandatory.)
- Go **must** use `-tags nosqlite`; Node side packages with dependency changes need `npm ci` first (`media` / `front` have locks, `client` has zero deps so not needed).
- npm installing optional native dependencies (e.g. rolldown bindings) via proxy is often skipped, causing local build failure while CI is fine ——
  check this step first when local build fails.

---

## 6. Maintenance Conventions

- Changed code behavior → sync that layer's tests + layered docs (`doc/layers/`, hard requirement).
- Test functions must be annotated with "discovery background" (global AGENTS.md hard requirement).
- **When adding/removing test sets, update this file's §1 selection table, §2 full picture table, §3 corresponding subsections.**
- Case numbers will drift: this table is a 2026-09-20 snapshot, after re-running if there are discrepancies use measured values and update along the way.
- **E2E intermittent red light troubleshooting order**: first check if node logs have `psk ok from <peer id>`.
  If not, and no `psk mismatch` ⇒ the peer's frame **was never seen at all** (not a wrong key),
  check `back/internal/transport/conn.go`'s `bindConn`: `OnMessage` must be attached
  before any send (the pit fixed on 2026-09-21, regression case
  `TestPSK_AuthArrivingDuringBindIsNotDropped`).
- To continue filling in, priority: `front/tests/*.mjs` 2 remaining UI smoke (depends on online sites, need to solve external network first) >
  media's 2 non-`*.test.mjs` E2E (also external network, and need manual `media-node` startup first).
  Both of these are **not** "just add a job" —— the real barrier is external network and manual service startup, not nobody writing them.
  (signalserver / p2p_bt and release gate were completed on 2026-09-20/21; Pages online self-check entered `pages.yml` on
  2026-09-21; `test-layers.sh`'s own issues were fixed on 2026-09-20, see §3.13.)
