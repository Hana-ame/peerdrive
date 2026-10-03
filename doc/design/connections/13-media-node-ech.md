# Connection 13: media-node ↔ ech (ECH media chain)

- **Modules involved**: `../modules/12-media-node.md` and `../modules/12-media-node.md` (media-node binary and ech package **belong to same module 12**, same code directory, this connection describes internal wiring, see `doc/design/README.md:49`)
- **Code locations**: A side `back/cmd/media-node/main.go`; B side `back/ech/ech.go`
- **Direction**: A→B one-way (in-process function call, no network channel, no message queue; data and results flow back via return value `(*http.Response, error)`, `back/ech/ech.go:354-359`; B side has no callback to A side interface)

## 1. Connection Method

**Channel type: in-process function call**. In same Go binary, A side directly `import "peerdrive/ech"` (`back/cmd/media-node/main.go:47`); B side is a library (`package ech`, `back/ech/ech.go:1-11`), not an independent process, doesn't listen on port, no IPC. Connection has two call surfaces:

1. **Startup initialization surface**: A side `main()` calls `ech.InitDefault(ech.Config{ProxyURL: proxyURL})` (`main.go:178`). B side internally: `New` creates 15s timeout ctx (`ech.go:296-304`) → `fetchECHConfig` fetches ECH config via DoH (`ech.go:299,131-195`) → `newClient` constructs ECH transport → `defaultClient.Store` + starts `go refreshLoop` (`ech.go:371-372`). Failure returns error, A side `log.Fatalf` exits directly (`main.go:179-180`).
2. **Request surface**: A side `fetchTwimg` constructs `http.Request` with anti-hotlink headers then calls package-level `ech.Do(req)` (`main.go:94-102`). B side `Do` loads global `defaultClient` (`ech.go:377-390`; media-node always calls `InitDefault` at startup, so always takes initialized path, lazy initialization is just library-level fallback, `ech.go:379-387`).

**Parameter format**: No proprietary protocol frames, all ordinary Go function signatures:

- `ech.Config{DoHURL, ProxyURL, ShellDomain string}` (`ech.go:121-129`). media-node only sets `ProxyURL` (flag `-proxy`, empty reads `HTTPS_PROXY` env var, `main.go:166,174`; B side `effectiveProxy` explicit priority, else env var, `ech.go:214-220`); `DoHURL`/`ShellDomain` empty uses defaults `https://moonchan.xyz/doh` (self-hosted DoH, `ech.go:117-119`) and `cloudflare-ech.com` (shell domain, `ech.go:137-140,308-311`).
- `InitDefault(cfg Config) error` (`ech.go:366-374`); `Do(req *http.Request) (*http.Response, error)` (package-level `ech.go:377-390`, client method `ech.go:354-359` — `req.Host` empty sets to `req.URL.Host`, i.e. real target domain, `ech.go:355-357`).

**B side's self-built internal channel (actual carrier of ECH domain fronting, for this connection's semantic reference)**: `newTransport` constructed `http.Transport` dials `cloudflare-ech.com:443` via `DialTLSContext` (direct or via HTTP proxy CONNECT tunnel, `ech.go:222-285,314-322`), TLS1.3 handshake carries `EncryptedClientHelloConfigList` (ECH config), `ServerName` is real target domain `video-cf.twimg.com` (`ech.go:323-333`) — GFW only sees shell plaintext SNI, Cloudflare edge routes to twitter CDN by ECH inner SNI (`ech.go:1-8`, `main.go:8-14` comments). This transport's connection pool `MaxIdleConns:100 / IdleConnTimeout:90s` (`ech.go:336-338`) lifecycle = currently effective global client (`defaultClient`, `ech.go:363`).

**Authentication method**: This connection (in-process call) has **no own authentication**; all auth is at external gates:

- Upstream anti-hotlink: `Referer: https://x.com` + Chrome 120 UA hardcoded in request headers (`main.go:93,98-100`), if not satisfied upstream returns 4xx (`main.go:126-129` converted to err frame).
- DoH endpoint public, no auth (`ech.go:117-119`).
- ECH trust model: trust Cloudflare edge routing by ECH inner SNI (`ech.go:1-8` comments); ECH only effective for Cloudflare-hosted domains (`main.go:14` comment).
- Signaling side key auth belongs to [08-transport-signalserver.md](08-transport-signalserver.md) (`main.go:161-164,184-191`).

**Connection establishment/who establishes**: A side actively calls `ech.InitDefault` once at `main()` startup (`main.go:177-180`) — process resident; thereafter B side `refreshLoop` every 5 minutes self-drives re-fetching ECH config and swapping client (`ech.go:394-417`), no longer through A side. Request surface per request goes through `fetchTwimg → ech.Do` using same global client, TCP/TLS connections established/reused on demand by transport connection pool (`ech.go:313-341,336-338`).

## 2. Timing

### 2.1 Startup Initialization: DoH fetch ECH config → global client ready

```mermaid
sequenceDiagram
  participant M as media-node (main.go)
  participant E as ech package (ech.go)
  participant D as DoH: moonchan.xyz/doh

  M->>E: InitDefault(Config{ProxyURL}) (main.go:178)
  E->>E: New: ctx 15s (ech.go:296-304) → fetchECHConfig (ech.go:299)
  E->>E: getCachedECH("cloudflare-ech.com") miss (ech.go:141-143)
  E->>D: GET /doh?name=cloudflare-ech.com&type=65 (ech.go:145-153; Client 8s timeout)
  D-->>E: JSON Answer[type=65]: ech=base64 / # wire (ech.go:162-184)
  E->>E: Parse ECHConfigList; setCachedECH (TTL clamped 60~86400s, ech.go:57-67,190)
  E->>E: newClient → defaultClient.Store → go refreshLoop (ech.go:371-372)
  E-->>M: nil (success) (ech.go:373)
  Note over E: Thereafter every 5 minutes: refreshLoop re-fetch → newClient → Swap (ech.go:394-417)
```

**Step-by-step explanation**:

1. **InitDefault entry**: `main()` after flag parsing calls `ech.InitDefault`, only passes `ProxyURL` (`main.go:174,178`); `DoHURL`/`ShellDomain` empty uses defaults (`ech.go:132-140`).
2. **First fetch**: `New` creates 15s timeout ctx (`ech.go:296-304`), calls `fetchECHConfig` (`ech.go:299`); `getCachedECH("cloudflare-ech.com")` cache miss (`ech.go:141-143`); sends DoH query `GET /doh?name=cloudflare-ech.com&type=65` (`ech.go:145-153`; DoH client 8s timeout, `ech.go:147`).
3. **ECH config parsing**: DoH returns JSON `Answer[type=65]` containing `ech` (base64-encoded ECHConfigList) or wire-format bytes (`ech.go:162-184`); parsed into `ECHConfigList`; TTL clamped to 60~86400 seconds (`ech.go:57-67`); cached via `setCachedECH` (`ech.go:190`).
4. **Client creation**: `newClient` constructs ECH transport → `defaultClient.Store` → starts `go refreshLoop` (`ech.go:371-372`).
5. **Return**: nil on success (`ech.go:373`).
6. **Refresh loop**: Every 5 minutes, `refreshLoop` re-fetches ECH config → `newClient` → `defaultClient.Swap` (`ech.go:394-417`).

### 2.2 Request Path: fetchTwimg → ech.Do → HTTP response

```mermaid
sequenceDiagram
  participant M as media-node (main.go)
  participant E as ech package (ech.go)
  participant CF as Cloudflare edge (cloudflare-ech.com)
  participant TW as Twitter CDN (video-cf.twimg.com)

  M->>M: fetchTwimg: construct http.Request with Referer/UA
  M->>E: ech.Do(req) (main.go:94-102)
  E->>E: Load defaultClient (ech.go:377-390)
  E->>E: req.Host empty → set to req.URL.Host (ech.go:355-357)
  E->>CF: DialTLSContext(cloudflare-ech.com:443) + ECH handshake
  CF->>TW: Route by ECH inner SNI (video-cf.twimg.com)
  TW-->>CF: HTTP response
  CF-->>E: HTTP response
  E-->>M: *http.Response
```

**Step-by-step explanation**:

1. **Request construction**: `fetchTwimg` (`main.go:82-130`) constructs `http.Request` with:
   - URL: twitter video CDN URL (e.g., `https://video-cf.twimg.com/...`)
   - Headers: `Referer: https://x.com` (anti-hotlink), Chrome 120 User-Agent
   - Method: GET
2. **ech.Do**: Package-level `ech.Do(req)` (`ech.go:377-390`) loads `defaultClient`; if `req.Host` empty, sets to `req.URL.Host` (`ech.go:355-357`) — this is the real target domain.
3. **ECH transport**: `defaultClient.Transport` is the ECH `http.Transport` constructed by `newTransport` (`ech.go:313-341`):
   - `DialTLSContext` dials `cloudflare-ech.com:443` (direct or via HTTP proxy CONNECT tunnel, `ech.go:222-285,314-322`)
   - TLS1.3 handshake carries `EncryptedClientHelloConfigList` (ECH config)
   - `ServerName` is real target domain `video-cf.twimg.com` (`ech.go:323-333`)
   - GFW only sees shell plaintext SNI (`cloudflare-ech.com`), Cloudflare edge routes by ECH inner SNI
4. **Response**: HTTP response flows back through Cloudflare → ech client → media-node.
5. **Error handling**: Non-2xx → error frame (`main.go:126-129`); connection failure → error propagation.

### 2.3 Refresh Loop: ECH config renewal

```mermaid
sequenceDiagram
  participant E as ech package (ech.go)
  participant D as DoH
  participant NC as New client

  loop Every 5 minutes
    E->>E: refreshLoop tick
    E->>D: GET /doh?name=cloudflare-ech.com&type=65
    D-->>E: JSON Answer[type=65]
    E->>NC: newClient(ECHConfigList)
    E->>E: defaultClient.Swap(newClient)
    Note over E: Old client's connections drain naturally
  end
```

**Step-by-step explanation**:

1. `refreshLoop` (`ech.go:394-417`) runs in goroutine, ticks every 5 minutes.
2. Re-fetches ECH config via DoH (same as startup).
3. Constructs new client with new ECH config.
4. `defaultClient.Swap(newClient)` atomically swaps the global client.
5. Old client's connections drain naturally (HTTP transport connection pool doesn't force close).

### 2.4 Step-by-step: Proxy handling

```mermaid
sequenceDiagram
  participant M as media-node (main.go)
  participant E as ech package (ech.go)
  participant Proxy as HTTP proxy

  M->>M: flag -proxy or HTTPS_PROXY env
  M->>E: InitDefault(Config{ProxyURL: proxyURL})
  E->>E: effectiveProxy: explicit ProxyURL or env var (ech.go:214-220)
  E->>Proxy: CONNECT cloudflare-ech.com:443
  Proxy-->>E: 200 Connection Established
  E->>E: TLS1.3 + ECH over proxy tunnel
```

**Step-by-step explanation**:

1. media-node gets proxy from `-proxy` flag or `HTTPS_PROXY` env var (`main.go:166,174`).
2. `InitDefault` passes `ProxyURL` to ech config.
3. `effectiveProxy` (`ech.go:214-220`): explicit `ProxyURL` takes priority, else reads `HTTPS_PROXY` env var.
4. `DialTLSContext` (`ech.go:222-285,314-322`): if proxy configured, establishes CONNECT tunnel to proxy, then TLS1.3 + ECH over tunnel.

## 3. Case Handling

| Case | Trigger condition | Handling strategy | Code location |
|---|---|---|---|
| **Timeout** | DoH fetch timeout; HTTP request timeout; refresh loop timeout | DoH: client 8s timeout (`ech.go:147`); ECH config fetch: ctx 15s (`ech.go:296-304`); HTTP request: no explicit timeout (relies on upstream/transport); refresh loop: 5min interval, no per-iteration timeout. | `ech.go:147,296-304`, `ech.go:394-417` |
| **Disconnect/Reconnect** | Network interruption; proxy disconnect; Cloudflare disconnect | DoH: transient error → log + retry on next refresh loop tick; HTTP request: `http.Transport` handles connection reuse and retry on next request; no explicit reconnect logic. | `ech.go:394-417`, `ech.go:336-338` |
| **Duplicate/Concurrent** | Concurrent HTTP requests; refresh overlapping with request | HTTP transport connection pool handles concurrent requests (`MaxIdleConns:100`, `ech.go:336-338`); `defaultClient.Swap` is atomic (`ech.go:394-417`); old client's in-flight requests complete on old client; new requests use new client. | `ech.go:336-338`, `ech.go:394-417` |
| **Data missing or validation failure** | DoH returns no ECH config; ECH parse failure; non-2xx response; upstream 4xx | DoH no result: returns error → `InitDefault` fails → `log.Fatalf` exits (`main.go:179-180`); ECH parse failure: returns error; non-2xx: `main.go:126-129` converts to err frame; upstream 4xx (anti-hotlink): same handling. | `main.go:126-129,179-180` |
| **Auth failure** | Upstream anti-hotlink rejection; wrong Referer/UA | Hardcoded `Referer: https://x.com` + Chrome 120 UA (`main.go:93,98-100`); if upstream rejects (4xx), `main.go:126-129` converts to error. No user-facing auth — media-node is a server-side proxy. | `main.go:93,98-100,126-129` |
| **Half-open state** | `defaultClient` not initialized; proxy tunnel half-open | `defaultClient.Store` always called before requests (startup `InitDefault`); lazy initialization fallback (`ech.go:379-387`) if `InitDefault` not called; proxy tunnel half-open handled by `http.Transport` connection pool (retries on new request). | `ech.go:371-372,379-387` |
| **Process restart** | Process killed/restarted | ECH config cache is in-memory (lost on restart); `refreshLoop` starts on new process; DoH config refetched from scratch; no persistent state. HTTP connection pool also in-memory. | `ech.go:394-417` |

## 4. Related Documents

- Connection documents (same directory):
  - [08-transport-signalserver.md](08-transport-signalserver.md): media-node registers to same signaling `peersignal.moonchan.xyz` with peer id (`main.go:161-164,184-199`); browser media DataChannel OFFER/ANSWER/CANDIDATE negotiation messages forwarded via signaling — this connection's data surface (2.2/2.3 frames and keepalive) is carried on top of it.
  - [07-transport-peerjs.md](07-transport-peerjs.md): peerjs library (module 10) provides DataChannel text/binary frame primitives (`back/peerjs/connection.go:90-123`), idempotent Close (`connection.go:175-198`), ICE state fallback cleanup (`connection.go:244-251`) and `Ordered: true` ordering guarantee (`connection.go:272-274`) — this connection's A side `conn.Send/SendJSON/Close` all depend on these semantics; media-node reuses same library via `back/go.mod` replace (module 12 §dependencies).
  - [12-frontend-signalserver.md](12-frontend-signalserver.md): Browser-side peerdrive-media / `echclient` dials media-node's peer id via same signaling (`back/cmd/echclient/main.go:34-49` demonstrates consumer-side perspective: `Connect("media-node","media")`), is the negotiation peer for frame protocol (`main.go:20-28` comments) and keepalive.
- Module documents: `../modules/12-media-node.md` (this connection's both sides belong to this module: §1 logic includes ECH domain fronting mechanism and main flow, §3 when to store refresh timing, §5 boundaries and pitfalls including keepalive/SSRF/anti-hotlink/timeout parameter overview), `../modules/10-peerjs.md` (DataChannel transport primitives and flow control semantics), `../modules/11-signalserver.md` (signaling channel, this connection's data surface negotiation carrier).
