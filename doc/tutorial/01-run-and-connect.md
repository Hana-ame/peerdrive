# Chapter 1: How to Run and Connect Your Own Node

> Applies to: branch `refactor` · **releases from v0.1.1 onward** (first truly asset-bearing release;
> `v0.1.0` tag was cut but the release step was red, no downloadable artifact). Time: 2026-09.
> **This chapter requires no Go, no compilation** — just download the pre-built binary.
> To modify code / run latest unreleased commits / run tests, see [Appendix A: Build from Source](appendix-build-from-source.md)
> — that's **optional**, only affects those who want to change code.
> Port convention: **Node 3001** — signaling doesn't need you to start it, see §1.2.
>
> ### Since v0.3.2, you don't have to fill in the node ID anymore
>
> Earlier versions made you copy the node ID out of the log and paste it into the panel.
> Now `peerdrive serve` prints a **clickable panel URL** on startup:
>
> ```
>   Next step: open the panel
>     http://127.0.0.1:3000/panel
> ```
>
> Open it and the panel figures out its own node ID and that node's signaling by itself.
> **This entire chapter's "enter the node ID" step is only needed on v0.3.1 and earlier,**
> or when you're connecting to **someone else's** node from the standalone panel (§1.5) —
> there you genuinely need that other person's ID, since it can't be guessed.
> After connecting, to add files: [Chapter 2: Add Local Files to Your Node](02-add-local-files.md).

---

## 1.0 Choose Your Path First

| Your Situation | Approach | Section |
|---|---|---|
| Run your own node, connect to it with the panel | Download node binary → start node → enter id in panel | §1.1 → §1.3 → §1.5 |
| Connect to your node from another machine / phone (external network) | Same as above, nothing extra to configure (signaling is public by default) | §1.1 → §1.3 → §1.6 |
| Don't run a node, just want to connect to someone else's | Open the online panel directly | §1.5 |
| I'm an operator, need to manage files / market / transfer tasks | Node admin panel `front/` | §1.7 |
| The signaling I want to connect to isn't the public one (self-hosted / internal) | Change environment variables (Appendix A has self-hosted signaling) | §1.2 |

---

## 1.1 Download the Release Package

Open <https://github.com/Hana-ame/peerdrive/releases/latest>, pick for your machine:

| Machine | Download This |
|---|---|
| Linux x86_64 | `peerdrive-linux-amd64` |
| Linux ARM64 (Raspberry Pi / server) | `peerdrive-linux-arm64` |
| Windows x86_64 | `peerdrive-windows-amd64.exe` |
| macOS Intel | `peerdrive-darwin-amd64` |
| macOS Apple Silicon | `peerdrive-darwin-arm64` |

> Since **v0.3.0** the release ships **only 5 assets, all the same `peerdrive` binary** — server,
> signaling, and registration are subcommands of it (`serve` / `signal` / `reg` / `all`).
> There is **no separate `peersignal-*` to download**; you get self-hosted signaling via `peerdrive signal`,
> and normal usage doesn't need it anyway, see §1.2.

Command line download (no need to open browser):

```bash
# With gh
gh release download --repo Hana-ame/peerdrive --pattern 'peerdrive-linux-amd64'

# Only curl
curl -LO https://github.com/Hana-ame/peerdrive/releases/latest/download/peerdrive-linux-amd64
```

Post-download actions:

```bash
chmod +x peerdrive-linux-amd64     # Linux / macOS
```

| Platform | First-Run Blocker | Resolution |
|---|---|---|
| macOS | Binary unsigned, Gatekeeper blocks it ("Cannot open because it is from an unidentified developer") | `xattr -d com.apple.quarantine peerdrive-darwin-arm64`, or right-click in Finder → Open |
| Windows | SmartScreen warning "Windows protected your PC" | Click "More info" → "Run anyway" |
| Linux | Generally none | — |

The node program **has no command-line arguments**, all configured via environment variables (see §1.3).
Confirm it runs: `./peerdrive-linux-amd64` then access `http://127.0.0.1:3001/ping`.

---

## 1.2 Signaling: You Don't Need to Deploy It

**All peerdrive programs default to the same public signaling**, nodes and panels alike:

| Item | Value |
|---|---|
| host | `peersignal.moonchan.xyz` |
| port | `443` |
| key | `pd-signal-1edf5e05e4a52b7351392574` |
| encryption | `wss` (also works in HTTPS pages) |

Both sides are hardcoded to this pair, so **nothing needs to be filled in** to find each other.
(Earlier the default was PeerJS public cloud `0.peerjs.com`/`peerjs`, which resulted in "run with defaults" nodes and panels
belonging to two different signaling servers — each had its own node table, neither could find the other. Now fixed.)

It only forwards SDP/ICE to help both ends punch through, **doesn't touch the data plane**: file content goes through WebRTC DataChannel,
never through signaling. To check it's alive:

```bash
curl -s https://peersignal.moonchan.xyz/status
# {"clients":N,"discovered":N,"key":"pd-signal-1edf5e05e4a52b7351392574","nodes":[...]}
```

When you'd need to change it: intranet offline environments, or wanting to control your own signaling. In that case change environment variables to point to yours
(self-hosted signaling build and startup in Appendix A; when enabling TLS, `-tls-cert` / `-tls-key` **must be provided as a pair**).
**All environment variables can be changed, but this tutorial won't expand on them** — defaults are the recommended usage.

> When switching signaling, remember one rule: **node and panel must point to the same one, with matching `key`**.
> Symptoms of mismatch: "both are online but can't connect."

---

## 1.3 Start Your Own Node

```bash
mkdir -p /tmp/pd/n1/root/downloads/shared /tmp/pd/n1/run
cd /tmp/pd/n1/run                      # cwd determines where peerdrive.db lands (multi-node must each have own cwd)
env PORT=3001 \
    PEERDRIVE_PEERJS_ID=my-node-1 \
    PEERDRIVE_STORAGE=/tmp/pd/n1/root \
    PEERDRIVE_DOWNLOAD_DIR=/tmp/pd/n1/root/downloads \
    PEERDRIVE_SHARE_ENABLE=true \
    PEERDRIVE_SHARE_DIRS=/tmp/pd/n1/root/downloads/shared \
    PEERDRIVE_BT_DHT_ENABLE=false    PEERDRIVE_IPFS_GATEWAY_ENABLE=false \
    /tmp/pd/peerdrive-linux-amd64 > /tmp/pd/n1.log 2>&1 &
```

(Windows: use `set VAR=...` on separate lines, or PowerShell's `$env:VAR='...'`; change program name to `.exe`.)

> **Actually, none of the above is required just to try it out.** `/tmp/pd/peerdrive-linux-amd64 demo`
> runs the entire chain (market → join → manifest → pull → checksum) with zero configuration,
> then `serve` alone already works with every default — sharing just won't be on (§1.3).
> The table below is for when you want a **stable node ID** or **a specific port**.

Only these need to be set, the rest use defaults:

| Variable | Why Set It |
|---|---|
| `PORT` | HTTP port, default 3000 |
| `PEERDRIVE_PEERJS_ID` | Your name on the network (peer id). Not given = random generation (`peerdrive-<random>`). Since v0.3.2 the panel can self-discover the id, so a random one is no longer fatal — but **to connect from another machine or another node, you still need a fixed one** |
| `PEERDRIVE_STORAGE` | Content-addressed root: incoming content saved as `storageDir/<hash-first-2-chars>/<hash>`, naturally deduplicated |
| `PEERDRIVE_DOWNLOAD_DIR` | Download / registration directory (root of `file_index`) |
| `PEERDRIVE_SHARE_ENABLE` + `PEERDRIVE_SHARE_DIRS` | Enable sharing + declare shared directories (default off; if not enabled, other nodes can't see any content) |
| `PEERDRIVE_BT_DHT_ENABLE=false` | Disable BT DHT (no network access, avoid noise) |

---

## 1.4 Verify Node Started

```bash
curl http://127.0.0.1:3001/ping           # → pong
curl http://127.0.0.1:3001/peerjs/id      # → {"id":"my-node-1"}
```

Check node logs for "signal connected":

```
peersignal dialing wss://peersignal.moonchan.xyz:443 ...
peersignal connected as my-node-1
```

---

## 1.5 Open the Panel

### 1.5.0 Local node: use the one served by your node (recommended since v0.3.2)

`http://127.0.0.1:3000/panel`（replace 3000 with your `PORT`）

The panel is embedded in the binary along with peerjs. **No inputs required** — it asks your node
"who am I / which signaling am I on", fills it in itself, then auto-connects.

> ✅ **No protocol caveat needed — verified 2026-10-06 in a real browser (Chromium).**
> The panel served over http:// connects fine to the default wss:// signaling. Browsers only block
> **downgrade** mixed content (an https page opening ws://), not upgrade (http page opening wss://).
> Observed: WS connects to `wss://peersignal.moonchan.xyz/peerjs?...`, no mixed-content block,
> `自动搜索` lists this node. Out of the box, zero config, works.

The only real mixed-content case is the **reverse** of what's often assumed: an **https** page
cannot open a `ws://` signaling. Chromium's exact wording:
`SecurityError: Failed to construct 'WebSocket': An insecure WebSocket connection may not be
initiated from a page loaded over HTTPS.` (Verified 2026-10-06 on a real domain over https.)

Since the local `/panel` is http and default signaling is wss, this does not bite you.
If you front your node with https and self-host a plain-ws signaling, point the panel at it
over http, or give that signaling TLS (`peerdrive signal -tls-cert/-tls-key`).
For the online panel (§1.5.1, https) with default wss signaling, it works as-is too.

> ⚠️ One trap when reproducing this yourself: testing on `127.0.0.1` **will not trigger the block** —
> browsers treat loopback as a secure context. Use a real hostname (Chromium
> `--host-resolver-rules=MAP your.host 127.0.0.1`) or you'll wrongly conclude it's allowed.

### 1.5.1 Connecting to someone else's node: use the online panel

<https://peerdrive.pages.dev/> (or <https://hana-ame.github.io/peerdrive/>)

Three inputs in the upper right:

| Input | What to Fill |
|---|---|
| Node ID | `my-node-1` (the `PEERDRIVE_PEERJS_ID` you set) |
| Signaling | No need to fill (defaults to the public one above) |
| Key | No need to fill |

Click "Connect" → right side shows node details and shared content list.

> If connecting from an HTTPS page, the signaling must be `wss` (HTTPS pages can't open `ws://` — browser blocks mixed content).
> The default public signaling is already wss, nothing to worry about. Self-hosted signaling needs to enable TLS (Appendix A).

---

## 1.6 Connecting from Outside (External Network)

Two prerequisites:
1. Your node is reachable from the public network (public server, or home broadband with port mapping/UPnP).
2. Your machine and your node don't share the same local network.

If both met, the startup log already printed a **LAN address**（for example `http://172.29.89.192:3000/panel`）——
open that on your phone or another computer and the panel self-discovers, no need to enter the node ID.
Connecting to **another node** is different: you have to enter that node's ID, because it can't be guessed.

Data goes through WebRTC DataChannel, doesn't occupy server bandwidth.

---

## 1.7 Node Admin Panel (`front/`)

For node operators who need web interfaces for: file listing, collection marketplace, transfer tasks, sharing scope configuration, P2P diagnostics.

```bash
cd front && npm install
npm run dev          # → http://localhost:5173
```

Connect the same node ID. Admin panel accesses `ws://localhost:3001/ws/peer` on the same machine,
bypassing the public signaling (same-host admin channel), no public network exposure.

---

## 1.8 What You Should See

Connect the panel to your node and you should see:

| Where to Look | Content |
|---|---|
| Panel right side "Node Details" | Peer ID, version, protocol support, connection info |
| "Market" / shared content list | Initially empty (directory declared but no files indexed yet, see Chapter 2) |
| Node logs | `peersignal connected as my-node-1`, announce heartbeats |
| `curl :3001/peerjs/nodes` | Your node should appear in this list |

> `files:0` is normal: shared directory declared but no file index registered yet
> (registration via `POST /files/register_folder`, see Chapter 2).

---

## 1.9 Symptom → Cause → Resolution

| Symptom | Real Cause | Resolution |
|---|---|---|
| Panel only says "can't connect", no other info | HTTPS page + `ws://` signaling blocked by mixed content | Check wss (§1.5.3); self-hosted signaling needs TLS enabled (Appendix A) |
| Auto-search fails | Signaling doesn't return `Access-Control-Allow-Origin`, or node hasn't announced yet | Check `curl -D - -H 'Origin: null' <signaling>/discover/nodes`; wait for one heartbeat cycle (~30s) then retry |
| Prompt `psk: this node requires pre-shared key`, but I entered the key | Peer's `psk-auth` frame was dropped (old `bindConn` ordering issue), or both keys don't match | Check node log: `psk ok from X` present → no issue; `psk mismatch from X` → key wrong; **neither present** → that frame was never seen at all (not a key issue) |
| Admin panel connected but shows nothing | `/ws/peer` 404: `PEERDRIVE_PEERJS_ENABLE=false` | Enable peerjs and point to working signaling |
| List shows files, but pull says `read failed` | Shared directory not declared in `PEERDRIVE_SHARE_DIRS` | Declare it (directory can be anywhere, but **must be declared**) |
| Request hangs 15s then TIMEOUT (not error) | Running old binary, doesn't recognize new verbs | Download latest release |
| Node just started, panel immediately can't connect | Node hasn't finished announcing, heartbeat not stable | Wait for `/ping` to respond, wait a few seconds for heartbeat stability |
| Blank panel page, nothing renders (check the browser console) | The server's global CSP blocked inline scripts | Already fixed since v0.3.2 (the `/panel` prefix is exempted); if you self-built an old commit, upgrade to v0.3.2+ |
| Panel spins forever, console reports a `ws://` mixed-content error | An **https** page (online panel, or your node behind an https proxy) pointed at a plain `ws://` signaling | Point it at `wss://` (the default public signaling is wss) or enable TLS on self-hosted signaling (Appendix A). ⚠️ Local http `/panel` + default wss is **never** blocked — this row does not apply to the default setup |
| Demo works but your node's panel doesn't | Yours may be on v0.3.1 or earlier (no embedded panel) | Check `peerdrive version`; download v0.3.2 or newer |
