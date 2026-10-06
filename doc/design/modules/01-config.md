# Module 01: config Configuration Module

- **Code location**: `back/internal/config`
- **One-line function**: At process startup, reads all runtime configuration from environment variables (`PEERDRIVE_*` series and `PORT`) into a read-only `*Config`, and performs "fail-fast" startup validation (`back/internal/config/config.go:1-3`, `back/internal/config/config.go:186-246`, `back/internal/config/config.go:254-282`).
- **Dependencies**: Only Go standard library `fmt`/`os`/`runtime`/`strconv`/`strings` (`back/internal/config/config.go:5-11`), no third-party dependencies, does not read/write files, does not depend on a database.
- **Depended upon by** (all injected via `back/internal/serverapp/app.go`):
  - `back/internal/serverapp/app.go:52-71`: `config.Load()` + `config.Validate(cfg)` (validation failure `stdlog.Fatalf`) + volume root check/`os.Root` probing (implemented in main package, operating on config values);
  - `back/internal/router`: `SetupRouter(cfg)` (`back/internal/router/router.go:44`), `SetPeerJSConfig(cfg)` (`back/internal/router/peerjs_routes.go:50-52`);
  - `back/internal/transport`: `NewPeerJSService(cfg, storageDir)` (`back/internal/transport/peerjs_service.go:113`), PSK gate reads `cfg.PeerPSK` (`back/internal/transport/psk.go:54,66`), cross-node pull limit reads `cfg.MaxUploadBytes` (`back/internal/transport/pull.go:79-80`);
  - `back/internal/service`: `AnonService{config: cfg}` (`back/internal/service/anon_service.go:25-28`), `NewNodeShare(cfg, storageDir)` (`back/internal/serverapp/app.go:134`), `NewNodeDirectory(storageDir, cfg.DiscoverURL)` (`back/internal/serverapp/app.go:120`), `FileService` upload limit/directory depth (`back/internal/service/file_service.go:309,836-841`);
  - `back/internal/controller`: `WebRTCInfoHandler(cfg)` (`back/internal/controller/webrtc.go:12-23`);
  - `back/internal/source`: URL source templates registered via main (`back/internal/serverapp/app.go:224-228`);
  - Tests: `back/test/integration/*`, `back/internal/.../*_test.go` (construct cfg for components under test).

## 1. Logic

**Responsibility**: The sole assembly point for configuration. The package comment explicitly states "loads all configuration items from environment variables (port, storage directory, PeerJS/WebRTC, BT DHT, forwarding, etc.). `Load()` reads `PEERDRIVE_*` series environment variables and returns `*Config`" (`back/internal/config/config.go:1-3`). All facts are based on actual code; no config files, no config center.

**Core types**:

- `Config` struct (`back/internal/config/config.go:25-149`): A flat set of approximately 40 string/boolean/numeric fields, covering listening port, DB path, storage directory, CORS whitelist, upload limits, BT DHT, IPFS gateway, WebRTC STUN/TURN, PeerJS signaling, PSK, MQTT, node discovery, URL source templates, download, forwarding rules, HTTP hardening, and sharing scope. Each field's comment annotates the corresponding environment variable name and default value (default value comments see `back/internal/config/config.go:27,51-59,72-76,94,99,102-125` etc.).
- Default constants (`back/internal/config/config.go:13-23`): `DefaultSignalHost`/`DefaultSignalPort`/`DefaultSignalKey`/`DefaultDiscoverURL` — defaults for the project's public signaling, with historical lesson comments: earlier the default PeerJS public cloud `0.peerjs.com/peerjs` caused "running with defaults" nodes and panels to be on two different signaling servers and unable to find each other (`back/internal/config/config.go:14-16`).

**Main flow**:

1. `Load()` (`back/internal/config/config.go:186-246`): Reads environment variables field by field via `getEnv*` and fills default values, returns a fresh `*Config`.
2. `Validate()` (`back/internal/config/config.go:254-282`): Startup validation, catching "misconfigured but won't error" configs at once; aggregates all errors and returns them in one shot (`back/internal/config/config.go:278-281`).
3. Call flow (`back/internal/serverapp/app.go:52-71`): `cfg := config.Load()` → `config.Validate(cfg)` (non-nil means `stdlog.Fatalf`) → `checkUnsafeRoots(cfg)` (volume root config rejection, main package implementation, `back/internal/serverapp/app.go:316-333`) → `warnUnsupportedRoots(cfg)` (`os.Root` security boundary probing, `back/internal/serverapp/app.go:340-354`).
4. Read-only distribution (`back/internal/serverapp/app.go:73-241`): `cfg.DBPath` → `repository.InitDB`; `cfg.StorageDir` → Gin context/storage; when `cfg.PeerJSEnable` is true `NewPeerJSService(cfg, storageDir)`; `router.SetPeerJSConfig(cfg)`/`SetupRouter(cfg)`; only registers URL sources when `cfg.URLSourceTemplate` is non-empty (`back/internal/serverapp/app.go:224-228`).

**Helper reading function semantics** (`back/internal/config/config.go:284-329`): `getEnv` unset → default value; `getEnvBool` uses `strconv.ParseBool`; `getEnvInt`/`getEnvInt64` require successful parse and value `> 0`; `getEnvFloat` requires successful parse and value `>= 0`; any parse failure **silently falls back to default**.

**Methods**:

- `IsOriginAllowed` (`back/internal/config/config.go:151-176`): CORS and WS local session Origin whitelist judgment. Supports exact match (case-insensitive), `*` allow all, `*.example.com` and `https://*.example.com` subdomain wildcards; when `AllowedOrigins` is empty or `*`, everything is allowed (`back/internal/config/config.go:153-154`). Behavior tests see `back/internal/config/config_test.go:67-106`.
- `DefaultRootPath` (`back/internal/config/config.go:178-184`): Returns `C:\` on Windows, `/` on other platforms.

**Lifecycle**: Process-level singleton — `main` local variable `cfg` is constructed then passed to each module via parameter/injection, read-only during runtime (no writes to `cfg` fields found in production code; test-local assignments excluded, e.g. `back/internal/transport/share_test.go:25`). Only downstream NodeShare

## 2. How It Stores

**No persistent storage**. This module is purely an in-memory, process-level, read-only configuration object — `Config` is a plain Go struct, constructed at startup by `Load()` and then frozen (all fields are effectively immutable after `Validate()` passes). No files, no database, no network writes. It is the single source of truth for all runtime parameters, distributed to each module via function arguments or struct injection.

| Aspect | Details |
|--------|---------|
| Storage medium | In-memory only (Go struct on heap) |
| Persistence | None — config comes from environment variables |
| Lifecycle | Lives for the entire process lifetime; created once at startup |
| Thread safety | Effectively immutable after construction (only test code modifies fields) |
| Size | ~40 fields, trivially small |

## 3. When It Stores

**Never.** Configuration is loaded once at process startup and never persisted back. If configuration needs to change, the process must be restarted with new environment variables.

The only "write" to configuration is at startup:

| Timing | Action | Code Reference |
|--------|--------|----------------|
| Process startup | `Load()` reads env vars into `*Config` | `config.go:186-246` |
| After Load | `Validate()` checks consistency (read-only validation) | `config.go:254-282` |
| After Validate | Value distribution to downstream modules (read-only) | `app.go:170-339` |

No module writes configuration back. The pattern is strictly "read env → validate → distribute → immutable".

## 4. What It Stores

The `Config` struct holds approximately 40 fields, grouped by domain:

**Network/Server** (`config.go:27-48`):
- `Port` (default `8080`), `Host` (default `0.0.0.0`), `TLSEnable`/`TLSCert`/`TLSKey`, `MaxBodyBytes` (default `1073741824` = 1 GB), `RateLimitRPS` (default `0` = unlimited), `TrustedProxies`

**Storage** (`config.go:49-66`):
- `StorageDir` (default `./storage`), `DBPath` (default `./peerdrive.db`), `MaxUploadBytes` (default `1073741824`), `StorageEnable` (default `true`), `DownloadDir` (default `./downloads`), `MaxFileDepth` (default `8`)

**PeerJS/WebRTC** (`config.go:67-84`):
- `PeerJSEnable` (default `true`), `PeerJSHost` (default `peersignal.moonchan.xyz`), `PeerJSPort` (default `443`), `PeerJSKey` (default `peerdrive-2024`), `PeerPSK` (empty = disabled), `STUNServers`, `TURNServers`, `PeerJSDiscoverURL` (default `https://peersignal.moonchan.xyz`)

**BT/BitTorrent** (`config.go:85-98`):
- `BTEnable` (default `false`), `BTDHTBootstrap` (default nodes list), `BTDownloadDir` (default `./downloads`), `BTMaxActiveDownloads` (default `3`)

**IPFS** (`config.go:99-113`):
- `IPFSGatewayURL` (default `https://ipfs.io`), `IPFSGatewayList` (comma-separated), `IPFSRetry` (default `3`), `IPFSTimeout` (default `30` seconds)

**MQTT** (`config.go:114-125`):
- `MQTTEnable` (default `false`), `MQTTBroker` (default `tcp://localhost:1883`), `MQTTTopicPrefix` (default `peerdrive`), `MQTTCollections` (comma-separated filter)

**Security/HTTP** (`config.go:126-138`):
- `AllowedOrigins` (CORS), `DisableCSP` (default `false`), `RegistrationServer` (empty = disabled), `AuthRequired` (default `false`)

**Node Discovery** (`config.go:139-149`):
- `DiscoverURL` (empty = disabled), `DiscoverPresence` (default `true`), `URLSourceTemplate` (empty = disabled)

**Sharing Scope** (`config.go:141-149`):
- `ShareEnable` (default `false`), `ShareDirs`, `ShareFiles`, `ShareCollections`, `ShareFriends`

**Validation rules** (`config.go:254-282`):
- Port must be `0` or `1024-65535`
- `StorageDir` and `DBPath` cannot be the same path
- If `PeerJSEnable` is true, `PeerJSHost` must be non-empty
- If `RegistrationServer` is set, `AuthRequired` must be considered
- `URLSourceTemplate` must be a valid URL if non-empty

## 5. Boundaries and Pitfalls

- **Environment variable naming**: All config keys use `PEERDRIVE_` prefix (except `PORT`). `Load()` maps them to struct fields; mismatched names silently use defaults.
- **`getEnvInt`/`getEnvInt64` only accept `> 0` values** — a value of `0` or negative silently falls back to default. This means you cannot explicitly set a value to `0` via environment variable.
- **`getEnvFloat` accepts `>= 0`** — `0.0` is valid.
- **No config file support**: This is a deliberate design choice. All configuration comes exclusively from environment variables. The module intentionally has no file I/O or database dependency.
- **Default signaling lesson** (`config.go:14-16`): The original default `0.peerjs.com/peerjs` caused nodes running with defaults to end up on different signaling servers than their panels. Changed to `peersignal.moonchan.xyz` as a project-maintained default.
- **`AllowedOrigins`**: Empty means allow all (for development). In production, must be explicitly set. `IsOriginAllowed` supports `*`, exact match, and `*.domain.com` wildcards.

## 6. External Connections

- [../connections/02-router-controller.md](../connections/02-router-controller.md): `SetupRouter(cfg)` and `SetPeerJSConfig(cfg)` consume config to build the HTTP router and PeerJS config.
- [../connections/05-router-source.md](../connections/05-router-source.md): URL source templates from `cfg.URLSourceTemplate` are registered into source management.
- [../connections/07-transport-peerjs.md](../connections/07-transport-peerjs.md): `NewPeerJSService(cfg, storageDir)` uses config for PeerJS/WebRTC/PSK settings; `transport.PSK` gate reads `cfg.PeerPSK`.
- [../connections/11-transport-storage.md](../connections/11-transport-storage.md): Cross-node pull limit reads `cfg.MaxUploadBytes`.
- [../connections/08-transport-signalserver.md](../connections/08-transport-signalserver.md): `DiscoverURL`/`DiscoverPresence`/`MQTTEnable`/`MQTTBroker`/`MQTTTopicPrefix`/`MQTTCollections` drive self-hosted signaling discovery API and MQTT room discovery (`back/internal/transport/peerjs_service.go:257-282,370,390`); direction config → transport discovery chain (self-hosted signaling/MQTT broker).
- [../connections/12-frontend-signalserver.md](../connections/12-frontend-signalserver.md): Frontend obtains node peer id (`/peerjs/node`, `back/internal/router/peerjs_routes.go:78-90`) and ICE config (`GET /p2p/webrtc/info`, `back/internal/router/router.go:242`, `back/internal/controller/webrtc.go:12-23`) then connects to signaling directly — these endpoint values and the signaling the frontend ultimately points to all originate from the same cfg (`PeerJSKey`/STUN/TURN etc.); direction config → backend HTTP delivery → frontend → signaling.
- Related note: [../connections/13-media-node-ech.md](../connections/13-media-node-ech.md) has **no dependency** on this module: `back/cmd/media-node/main.go` does not import `internal/config`; the media node uses `ech.InitDefault(ech.Config{ProxyURL: proxyURL})` (`back/cmd/media-node/main.go:178`), and the ech package has its own config cache, not part of this configuration module.
