# go-peerserver

Self-hosted PeerJS signaling server + built-in room discovery (Go).

Replaces public cloud signaling (0.peerjs.com) and public MQTT broker: nodes only need to point
`PEERDRIVE_PEERJS_HOST/PORT` (or any PeerJS client configuration) at this server,
discovery uses the built-in HTTP API. PeerJS protocol compatible — any `peerjs` JS client and
[go-peerjs](https://github.com/Hana-ame/go-peerjs) clients can connect directly.

## Usage

```bash
go build -o peerserver ./cmd/peerserver/
./peerserver [-addr :9000] [-key peerjs] [-tokens tok1,tok2] [-tls-cert c.pem -tls-key k.pem]
```

- `-addr` listen address (default `:9000`)
- `-key` PeerJS API key (client must match, prevents unrelated clients from connecting)
- `-tokens` optional: signaling token whitelist (comma-separated). When set, WS connection
  tokens must be in the list, otherwise the upgrade is rejected (prevents arbitrary clients from impersonating nodes to receive signaling)
- `-tls-cert` / `-tls-key`: PEM certificate and private key. **When both are provided, HTTPS/WSS is served**,
  providing only one will error and exit immediately (no silent downgrade — see below)

### When Must TLS (wss) Be Enabled

The public panel (`packages/peerdrive-client/dist/panel.html`, online version runs on GitHub Pages) is
an HTTPS page, browsers will block `ws://` initiated from HTTPS pages as **mixed content** directly, and PeerJS
only manifests as "can't connect" with no prompt at all. So:

- Panel uses `localhost` form of ws signaling: most browsers let it through, it works;
- Panel needs to connect to self-hosted signaling on LAN/public network: **must use wss**.

```bash
./peerserver -addr :9100 -tls-cert cert.pem -tls-key key.pem
# Or don't enable TLS in this process, but put caddy / nginx / Cloudflare Tunnel reverse proxy in front
```

REST endpoints (`/peerjs/id`, `/discover/*`, `/status`) all return `Access-Control-Allow-Origin: *`
and short-circuit OPTIONS preflight — the public panel is on a different origin, cross-origin headers are a hard requirement.

## Endpoints

| Path | Description |
|---|---|
| `GET /peerjs` (WS) | PeerJS compatible signaling (ice/offer/answer/leave/open) |
| `GET /peerjs/id` | ID borrowing rotation + expiry reclamation (H3 queue) |
| `POST /discover/announce` | Node online self-report (peerid → last-seen) |
| `GET /discover/nodes` | Room discovery list (expired nodes removed) |

## Library Usage (Embedding in Your Own Service)

```go
srv := signalserver.NewServer("peerjs", signalserver.WithTokenWhitelist([]string{"tok"}))
srv.Start() // Background sweeper: clean up expired offline queue
mux.HandleFunc("/peerjs", srv.HandleWS)
mux.HandleFunc("/discover/nodes", srv.HandleNodes)
```

## Tests

```bash
go test ./... -count=1
```

## Deployment Reference

- systemd + nginx reverse proxy (`wss://` to WS endpoint) see peerdrive main repo AGENTS.md;
- Online instance: `wss://peersignal.moonchan.xyz/peerjs` + `https://peersignal.moonchan.xyz/discover/*`
