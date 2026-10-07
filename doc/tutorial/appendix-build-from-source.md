# Appendix A: Build from Source (Can Be Skipped)

> Chapter 1 is "download the release and use directly." **Only look here when the release isn't usable**:
> You changed code, want to run an unreleased commit, need to run tests and e2e, or want to build your own release.
> If you just want to get it running, go back to [Chapter 1](01-run-and-connect.md) — this entire appendix can be skipped.

---

## A.0 When Do You Need to Build

| What You Want | Build Required | Use |
|---|---|---|
| Run a node | ❌ | The release binary downloaded in Chapter 1 (signaling is public, no need to start it) |
| Connect to a node via the panel | ❌ | Online panel, or the Chapter 1 link |
| Modify Go code and verify | ✅ | §A.2 / §A.3 |
| Run the latest unreleased `refactor` | ✅ | This appendix |
| Run e2e acceptance (market/join/pull/gate) | ✅ | §A.4 one-shot script |
| Build your own release package | ✅ | §A.8 (tag `v*` to let CI build) |

---

## A.1 Prerequisites

- **Go ≥ 1.26** (`back/go.mod` specifies `go 1.26.2`, `pathutil` uses `os.OpenRoot`, requires 1.24+)
- **Node ≥ 20** (panel build / admin console / consumer unit tests)
- Proxy: for pulling Go modules in China, `GOPROXY=https://goproxy.cn,direct` is recommended

---

## A.2 Build the Node

```bash
cd back
go build -tags nosqlite -o /tmp/pd/bin/peerdrive ./cmd/peerdrive/
```

> **`-tags nosqlite` is a hard requirement, not optional.**
> The repo supports `CGO_ENABLED=0` at release time by registering two SQLite drivers
> (`mattn/go-sqlite3` via cgo, `modernc.org/sqlite` pure Go), choosing one based on whether cgo is available;
> without this tag, the two drivers conflict. The driver names are `sqlite3` / `sqlite`, and `sql.Open` can't be hardcoded.

Cross-compilation (for other machines):

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags nosqlite -o peerdrive.exe ./cmd/peerdrive/
```

Using the built binary is exactly the same as Chapter 1 §1.3 (all configuration via environment variables).

---

## A.3 Build Self-Hosted Signaling (**Only needed if you want to self-host**)

By default, nodes and the panel connect to the public signaling server `peersignal.moonchan.xyz` (Chapter 1 §1.2).
**Only read this section if you need a separate signaling server** (intranet offline, or want to control it yourself).

It's an **independent Go module** (`back/signalserver`, module `github.com/Hana-ame/go-peersignal`),
so it can't be built as part of the previous step's `./...`:

```bash
cd back/signalserver
go build -o /tmp/pd/bin/peersignal ./cmd/peersignal
go run ./cmd/peersignal -addr :9100 -key pd-signal-1edf5e05e4a52b7351392574
```

After starting, the node side needs to point signaling here (`PEERDRIVE_PEERJS_HOST/PORT/KEY/SECURE` and
`PEERDRIVE_DISCOVER_URL`), and **the host/port/key on the panel must be updated too** — mismatch on both sides
results in "both are online but can't connect." When using TLS, `-tls-cert` / `-tls-key` must be provided as a pair.

Two other independent modules are the same — when modifying them, don't just run the main module's tests:
`back/peerjs` (go-peerjs), `back/p2p_bt` (go-peerdrive-bt).

---

## A.4 One-Shot E2E Acceptance Script

```bash
./scripts/netdisk-local-demo.sh          # Starts signaling :9100 + node-a :3001 + node-b :3002, runs 6 assertions
./scripts/netdisk-local-demo.sh --stop   # Stops all processes it started
```

It compiles itself, then runs: node market discovery → join → share list → cross-node pull → sha256 verification → PSK gate.
**Services remain running for manual testing** — after it completes, you can continue using the panel.
CI's `.github/workflows/e2e.yml` runs the same suite — passing means the environment is fine.

> ⚠️ It assumes exclusive ownership of ports 9100/3001/3002. If the previous processes are still running, new servers
> exit immediately due to port conflicts, and subsequent curl commands hit the **old processes**: nodes are visible, join succeeds,
> but the list and save paths are old — manifesting as "task done but file not on disk."
> Run `--stop` before re-running.

---

## A.5 Build the Public Panel

The panel is a **single static file**: source is inlined into `dist/panel.html`, double-click `file://` to open, or host on any static space.

```bash
cd packages/peerdrive-client
npm run build:panel     # Must rerun if src/ or panel/ changed
npm run check:panel     # CI uses this to catch "source changed but build not regenerated"
```

Ordering pitfall: **must build after modifying**, otherwise production (GitHub Pages) runs the old artifact.
`pages.yml` re-verifies the live site after deployment, and specifically **compares against this build's sha256** — just probing "page opens" would keep verifying the previous version.

---

## A.6 Admin Console (Front)

```bash
cd front
npm ci
npm run dev        # Dev server http://localhost:5173
npm run build      # Build artifact
```

It calls the node's HTTP / WS interfaces, **requires the backend to be running**.

---

## A.7 Run Tests

After modifying code, at minimum run these (full list and strategy in `doc/testing/README.md`):

```bash
cd back && go build -tags nosqlite ./... && go test -tags nosqlite ./... -count=1        # 550
cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1        # 21 (must use -p 1)
cd back/peerjs && go test ./... -count=1 -race                                           # 23
cd back/signalserver && go test ./... -count=1                                           # 23
cd back/p2p_bt && go test ./... -count=1                                                 # 7
cd front && npm test                                                                     # 88
cd packages/peerdrive-client && npm test                                                 # 98
bash scripts/test-layers.sh                                                              # Or run by layer
```

---

## A.8 Build Your Own Release Package

Releases are triggered by **tagging `v*`**, no need to build 5 platforms locally:

```bash
git tag -a v0.1.1 -m "description"
git push origin v0.1.1
```

`.github/workflows/release.yml` will:

1. **Gate**: backend vet + tests + build → integration tests → signaling module → peerjs module → consumer package + panel artifact consistency.
   **Red = no release** — the gate is built into the release itself, because workflows don't have `needs`,
   so parallel `ci.yml` can't stop it.
2. **Build × 5 platforms**: `CGO_ENABLED=0`, artifacts named `peerdrive-<goos>-<goarch>[.exe]`
   (without renaming, all 5 packages are called `peerdrive`, uploads overwrite each other by basename).
3. **Release**: attach the 5 files to the GitHub Release.

> ⚠️ **v0.3.0 changed the asset count**: server, signaling, and registration were merged into a single
> `peerdrive` binary as subcommands (`serve` / `signal` / `reg` / `all`).
> So it's **5 assets, not 10**, and **there is no longer a separate `peersignal-*`** — use `peerdrive signal`
> to self-host signaling. Release also injects the version number via `-ldflags -X`.

To only verify whether the gate passes, without actually releasing: manually run `Release` on the Actions page, keeping `dry_run` at default `true`
(the `release` step will be skipped).

> When modifying the asset naming logic in `release.yml`, don't forget `build` and `release` are paired:
> renaming on one side requires the other's `files:` to follow.
