# Module 12: media-node ECH Media Chain

- **Code location**: `back/cmd/media-node` (process entry and business logic, `back/cmd/media-node/main.go`) + `back/ech` (ECH domain fronting HTTP client library, `back/ech/ech.go`); verification tool `back/cmd/echclient` (`back/cmd/echclient/main.go`). Design document README states module 12's code location is `back/cmd/media-node/` + `back/ech/` (`doc/design/README.md:27`).
- **One-line function**: An **independent binary** media node — registers to PeerJS signaling server, accepts browser WebRTC DataChannel connections, only allows `https://video-cf.twimg.com/` prefix real URLs, goes through built-in ech package for ECH domain fronting (cloudflare-ech.com shell) to directly connect to twitter media CDN, streams media back in 64KB chunks in real-time; does not listen on any extra port, does not persist to disk, does not depend on external proxy processes (`back/cmd/media-node/main.go:1-28`).
- **Dependencies**:
  - `back/ech` (same module, `back/ech/ech.go`): ECH HTTP client — DoH fetch ECH config (`ech.go:131-195`) + in-memory TTL cache (`ech.go:37-67`) + TLS1.3 ECH domain fronting transport (`ech.go:306-341`) + refresh every 5 minutes (`ech.go:394-417`).
  - peerjs library: `github.com/Hana-ame/go-peerjs`, via `back/go.mod`'s `replace github.com/Hana-ame/go-peerjs => ./peerjs` pointing to repository's `back/peerjs/` (module 10) — provides signaling client (HEARTBEAT keepalive, `back/peerjs/peer.go:482-500`) and DataChannel transport primitives (text frames/binary frames/write buffer flow control, `back/peerjs/connection.go`).
  - No others: does not import `internal/config`/`repository`/`storage`/`router`/`controller`/`service` any main process module (`back/cmd/media-node/main.go:31-48` import list), does not write to database, does not read config files.
- **Depended upon by**:
  - `back/cmd/echclient/main.go`: Temporary verification client, joins same signaling with `echclient-<random 5 digits>` identity and connects to media-node's peer id, sends one `url` request to verify "signaling → WebRTC → ECH → twimg" full chain (`back/cmd/echclient/main.go:1-5,40-62,81-86`).
  - Browser-side peerdrive-media: Connects by frame protocol to fixed peer id `media-node` (`back/cmd/media-node/main.go:199`; frame protocol comments `main.go:20-28`).
  - No other Go code references (media-node is independent main package, not a library).

## 1. Logic

**Responsibility**: Proof-of-concept independent media node (`back/cmd/media-node/main.go:2-6` comments "rest of peerdrive not yet implemented, this module goes first alone"). Entire chain built into this binary: signaling registration → WebRTC connection → ECH domain fronting fetch → chunked return.

**ECH domain fronting mechanism** (`back/cmd/media-node/main.go:8-14`, `back/ech/ech.go:1-8`): Browser sends real target URL as-is; this side TCP connects to `cloudflare-ech.com` shell (domain not blocked), TLS handshake uses ECH-encrypted ClientHello (`EncryptedClientHelloConfigList`, `ech.go:325`) carrying real target domain `video-cf.twimg.com`, Cloudflare edge routes to twitter CDN based on this; GFW only sees plaintext SNI of shell domain. Comments explicitly state "ECH domain fronting only works for Cloudflare-hosted domains" (`main.go:14`).

**Core types**:

- `Msg` protocol frame (`back/cmd/media-node/main.go:56-65`): Seven JSON fields `Type/URL/ReqID/Mime/Size/Status/Msg`, `reqId` identifies a request.
- `ech.Client` (`back/ech/ech.go:289-293`): Wraps `*http.Client` (`Timeout: 0`, no timeout for large files, `ech.go:347`), `Do` sets `req.Host` to real target domain before sending (`ech.go:354-359`).
- `ech.Config` (`ech.go:121-129`): Three optional fields `DoHURL`/`ProxyURL`/`ShellDomain`.
- `echEntry` (`ech.go:32-35`): ECH config cache entry `{config []byte, expiry time.Time}`.

**Main flow**:

1. **Startup** (`back/cmd/media-node/main.go:158-199`): Flag parsing (160-175) → `ech.InitDefault` initializes built-in ECH client (failure is `log.Fatalf`, 177-180) → `peerjs.NewPeer` and `Dial` signaling with 15s timeout (184-198).
2. **Connection handling** (`main.go:202-256`): In `peer.OnConnection` callback, initialize active state for each DataChannel and start keepalive goroutine (202-232), `OnMessage` dispatches frames (234-250), `OnClose` cleans up (252-255).
3. **Request handling** (`serveRequest`, `main.go:104-156`): URL non-empty validation (108-111) → **single prefix allowlist** validation (112-116) → `fetchTwimg` fetches via ECH (119-124) → upstream `>=400` returns err (126-129) → sends `meta` (with mime/size, 131-133) → loops in `chunkSize` (default 64KB) chunks via `conn.Send` (136-153) → `done` (154).
4. **ECH client internals** (`back/ech/ech.go`): `New` first-startup 15s timeout fetches ECH config (296-304) → `newTransport` constructs transport dialing to shell domain 443, TLS1.3 + ECH handshake (306-341) → `refreshLoop` changes config every 5 minutes (394-417).
5. **Frame protocol** (`main.go:20-28` comments): Browser → node `{"type":"url","url":...,"reqId":...}`; node → browser sequentially `meta` → binary chunks × N → `done` or `err`; keepalive is node **proactively** sending `{"type":"ping"}` every 5s, peer returns `{"type":"ping-ack"}`, 15s no frames means disconnect.

**Lifecycle**: Process-level resident. On startup, DoH fetches ECH config and connects to signaling; then continuously accepts/kicks browser connections; on `SIGINT`/`SIGTERM` exits via `peer.Close()` (258-263), no cleanup actions beyond that. Every 5 minutes `refreshLoop` rotates ECH client (`ech.go:395-416`), Cloudflare rotates ECH configs (`ech.go:394` comments).

## 2. How It Stores

**No persistence — pure in-memory + connection state**. This module has no disk/database writes: `main.go` has no file write calls, no DB import; `ech.go` has no file IO. Data plane is a "stream read from twimg → DataChannel chunk send" direct pipe (`main.go:135-154`), naturally leaves no traces. In-memory state has three layers:

1. **ECH config cache** (shared across goroutines within process, `back/ech/ech.go:37-40`

## 3. When It Stores

**Never.** All operations are transient:

| Timing | Action | Notes |
|--------|--------|-------|
| Startup | Fetch ECH config via DoH, connect to signaling | In-memory |
| Browser connects | Accept DataChannel, start keepalive | In-memory connection state |
| URL request received | Fetch via ECH, stream back in chunks | No storage |
| Keepalive expiry | Disconnect after 15s of no frames | In-memory cleanup |
| Process shutdown | Close peer, exit | No cleanup beyond that |
| ECH config refresh | Every 5 minutes fetch new config | In-memory cache update |

## 4. What It Stores

**Nothing persistent.** In-memory only:

- ECH config cache (ECH config bytes + expiry timestamp)
- Active browser connections (peer connections with DataChannel state)
- Request-in-progress state (active URL fetches with chunk offsets)
- Keepalive timers (per-connection 5s ping intervals, 15s disconnect timeout)

## 5. Boundaries and Pitfalls

- **ECH is Cloudflare-only**: ECH domain fronting only works for Cloudflare-hosted domains. The shell domain must be hosted on Cloudflare with ECH enabled. `video-cf.twimg.com` is the only supported target domain.
- **Single prefix allowlist**: Only `https://video-cf.twimg.com/` prefix is allowed. Any other URL is rejected. This is a hard-coded security boundary.
- **No disk persistence**: No data is written to disk. The media stream is a direct pipe — read from twimg CDN, send through DataChannel, nothing stored locally.
- **ECH config refresh is every 5 minutes**: ECH configs expire and must be refreshed. The 5-minute interval balances freshness with DoH call frequency.
- **Keepalive is proactive from node**: The node sends pings every 5s. If no frame (including pings) for 15s, the connection is considered dead. This is stricter than typical WebRTC keepalive.
- **No authentication**: The only "auth" is the PeerJS key. Any client with the correct key can connect to the media node. There is no per-connection authentication.
- **No rate limiting**: There is no rate limiting on URL requests. A malicious client could use the media node as a proxy for twimg content.
- **No retry on fetch failure**: If ECH fetch fails, the error is returned to the browser. No automatic retry.
- **ECH config fetch is blocking on startup**: If DoH fails on startup, the process exits (`log.Fatalf`). There is no fallback.

## 6. External Connections

- [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md): This module is the media node implementation. The ECH library (`back/ech`) provides domain fronting transport. The peerjs library provides signaling and DataChannel transport.
- [../connections/10-peerjs.md](../connections/10-peerjs.md): Uses the peerjs library for signaling registration and DataChannel transport primitives.
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md): Media node registers to project public signaling `peersignal.moonchan.xyz` with fixed peer id (`main.go:161-164,184-199`), browser connection negotiation messages (CANDIDATE send `back/peerjs/connection.go:255-265`, ANSWER/CANDIDATE handling `connection.go:203-218`, OFFER send `connection.go:331-348`, ANSWER response `connection.go:351-364`) all forwarded via signaling; direction media-node → signaling server.
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Browser-side peerdrive-media and `echclient` dial media-node's peer id via same signaling (`echclient/main.go:34-49` demonstrates consumer perspective: `Connect("media-node","media")`); direction frontend/echclient → signaling → media-node.
- Related note: Related module documents in same directory are [10-peerjs.md](10-peerjs.md) (peerjs library implementation). Media node has **no dependency or data flow** with main process modules (config/repository/storage/router/controller/service/transport etc.) — independent binary, only shared artifact is signaling server and peerjs library.
