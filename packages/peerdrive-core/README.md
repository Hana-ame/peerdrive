# peerdrive-core

Single Source of Truth (SSOT) for the Peerdrive JavaScript ecosystem (`peerdrive-client`, `peerdrive-media`, and `front`):

- **Frame Protocol (`./protocol`, `./dispatcher`)**: v1 JSON control frames, v2 unified envelopes (`{event, req_id, code, data}`), and 32-byte self-routing binary headers (`PD02`) for order-independent concurrent transfers.
- **Incremental SHA-256 (`./sha256`)**: Zero-dependency streaming SHA-256 implementation compatible with chunked WebRTC/WebSocket transfers.
- **Canonical Self-Contained URLs (`./url`)**: Builders and parsers for `/f/<sha256>` file URLs and `/c/<hash>` collection URLs across SPA, HTTP, and standalone panel modes.
- **Multi-Transport Queue & Live Progress (`./queue`)**: `TransportQueue`, `AsyncQueueLane`, and `TransferProgressHub` with EWMA transfer rate and ETA calculation.
- **Security & Permission Guards (`./security`)**: Defensive sanitizers for untrusted peer manifests, prototype-pollution protection (`safeJsonParse`), and WebRTC read-only admin boundary enforcement (`evaluateAdminPermission`).

## Running Tests

```bash
npm test
```
