// Package config loads all configuration from environment variables (port, storage directory,
// PeerJS/WebRTC, BT DHT, forwarding, etc.).
// Load() reads PEERDRIVE_* environment variables and returns *Config.
package config

import (
	"os"
	"runtime"
	"strings"
)

// Project-wide public signaling (everyone connects to it, no self-hosting needed).
// The default is the PeerJS public cloud (0.peerjs.com) — nodes and panels running
// with defaults both reach the same public server and can find each other.
// Self-hosted deployments override via PEERDRIVE_PEERJS_HOST/PORT/KEY and
// PEERDRIVE_DISCOVER_URL (e.g. peersignal.moonchan.xyz with key pd-signal-...).
const (
	DefaultSignalHost = "0.peerjs.com"
	DefaultSignalPort = "9000"
	DefaultSignalKey  = "peerjs"
	// DefaultDiscoverURL is empty because the PeerJS public cloud (0.peerjs.com)
	// has no discovery API — discovery only works with a self-hosted signaling
	// server that implements /discover/nodes. Self-hosted deployments set
	// PEERDRIVE_DISCOVER_URL to their server (e.g. https://peersignal.moonchan.xyz).
	DefaultDiscoverURL = ""
)

type Config struct {
	Port   string
	DBPath string // PEERDRIVE_DB_PATH: SQLite metadata database path (default ./peerdrive.db)

	StorageDir    string
	StorageEnable bool

	AllowedOrigins     string
	RegistrationServer string
	// AdminToken is the local HTTP admin-surface Bearer token when no registration
	// server is configured (PEERDRIVE_ADMIN_TOKEN, default empty). When
	// RegistrationServer == "" and AdminToken != "": AuthRequired compares incoming
	// Bearer tokens against this value with constant-time comparison — the HTTP
	// management surface gets auth even though the remote login service is off.
	// When both are empty, auth is disabled (local single-machine mode, loopback).
	// Deliberately NOT reusing PEERDRIVE_PSK: PSK is a P2P DataChannel secret;
	// leaking it to HTTP would expose it in browser devtools/logs.
	AdminToken string

	// RemoteControlEnable allows remote control administration via WebRTC/PeerJS channels (PEERDRIVE_REMOTE_CONTROL_ENABLE, default false).
	// When false (default), admin verbs are strictly rejected on non-local sessions (Issue #234).
	RemoteControlEnable bool
	// RemoteControlToken is the pre-shared secret required to authenticate remote control commands (PEERDRIVE_REMOTE_CONTROL_TOKEN).
	// If empty, falls back to AdminToken.
	RemoteControlToken string
	// 2026-10-04: removed the three dead fields PublicAccessDomain / NodeAuthToken /
	// RegServerURL.
	// Why deletion instead of implementation:
	//   NodeAuthToken (PEERDRIVE_AUTH_TOKEN) was never an inbound gate to begin with —
	//   git show 539efc5 shows it was consumed by NodeRegistrar + service/p2p_key.go as the
	//   **outbound** identity this node uses when reporting to the registration server.
	//   Commit a5b090d deleted the libp2p stack along with those two consumers, leaving the
	//   field stranded; NodeRegistrar no longer exists and nodestate.Configure (its only
	//   remaining writer) had zero call sites — and was deleted on 2026-10-07 along with the
	//   rest of nodestate's dead API, so there is now no writer for nodestate's operator at
	//   all. So "implement it now" would mean inventing a brand-new credential system and
	//   calling it a security guarantee — strictly worse than deleting it. Inbound
	//   admin-surface auth is RegistrationServer above (router/auth_middleware.go
	//   authDisabled()).
	//   PublicAccessDomain / RegServerURL were zero-consumption from introduction.
	// Rollback note: to restore, re-add the three fields plus their getEnv lines at the
	// original positions; nothing else in the tree references them.

	BTDHTEnabled    bool
	BTDHTListenAddr string

	IPFSGatewayEnable bool
	IPFSGateways      string

	MaxUploadBytes     int64 // 0 = unlimited
	MaxUploadBytesAnon int64

	WebRTCSTUNServer string
	WebRTCTURNServer string
	// WebRTCTURNUsername / WebRTCTURNPassword are the TURN static-auth credentials (#144).
	// Why separate fields instead of only parsing them out of WebRTCTURNServer:
	// TURN URLs may legitimately carry userinfo, but production TURN (coturn) issues
	// time-limited credentials whose rotation must not require rewriting the URL list.
	// Empty username = no credentials (an anonymous TURN server), which stays valid.
	WebRTCTURNUsername string
	WebRTCTURNPassword string

	PeerJSEnable bool // PEERDRIVE_PEERJS_ENABLE, default true
	// Defaults to the PeerJS public cloud 0.peerjs.com:9000 (key "peerjs").
	// Self-hosted deployments override via PEERDRIVE_PEERJS_HOST/PORT/KEY.
	// Environment variables can still override (for self-hosting / internal debugging), but tutorials
	// don't teach changing this.
	PeerJSHost   string // PEERDRIVE_PEERJS_HOST, default peersignal.moonchan.xyz
	PeerJSPort   string // PEERDRIVE_PEERJS_PORT, default 443
	PeerJSKey    string // PEERDRIVE_PEERJS_KEY, default "peerjs" (public cloud) or pd-signal-... (self-hosted)
	PeerJSID     string // PEERDRIVE_PEERJS_ID, empty generates peerdrive-<random>
	PeerJSSecure bool   // PEERDRIVE_PEERJS_SECURE, default true
	PeerJSPeers  string // PEERDRIVE_PEERJS_PEERS, comma-separated peer node ids for auto-interconnection on startup

	// PeerJSXOREnable 数据面 XOR 混淆总开关（PEERDRIVE_PEERJS_XOR_ENABLE,
	// 默认 false）。false = 现状明文，与浏览器 peerjs / peerdrive-client 互操作
	// 不受影响；true = 本节点所有 WebRTC DataChannel 帧（req/meta/data/done/err/
	// psk-auth/fwd/admin 等）在库层做 XOR（见 back/peerjs/xor.go 设计契约）。
	// 定位：轻量混淆（防明文嗅探），不是强加密——防定向破解请用 PSK + 可信信令。
	PeerJSXOREnable bool
	// PeerJSXORKey 每连接密钥派生种子（PEERDRIVE_PEERJS_XOR_KEY）。
	// 开启时两端节点必须配置同一把 key（每连接再按 connectionId 派生，见
	// back/peerjs/xor.go）；key 不一致的对端解出垃圾帧、确定性失败。
	PeerJSXORKey string

	// PeerPSK is the node access pre-shared key (PEERDRIVE_PSK, default empty = open mode).
	//
	// When set: any peer must present the **same** key on this connection before this node
	// will respond to its data requests (req/share/list/create/upload/info/delete/sync/forward).
	// Not set = fully backward-compatible legacy behavior (serve anyone who connects).
	//
	// Boundary: it verifies "does the peer know this key", not "who the peer is" — no identity,
	// no authorization tiers; all key holders have equal access to the node.
	// To distinguish permissions per user, use the registration server auth (doc/modules/auth), not here.
	PeerPSK string

	// PeerBlocklist comma-separated peer node IDs blocked from connecting (PEERDRIVE_PEER_BLOCKLIST).
	PeerBlocklist string
	// PeerAnonPolicy controls inbound admission policy for anonymous (unauthenticated) peers (PEERDRIVE_ANON_POLICY).
	// Valid values: "open" (default), "share_only", "deny".
	PeerAnonPolicy string

	MQTTEnable      bool   // PEERDRIVE_MQTT_ENABLE, default false (MQTT shard room discovery)
	MQTTBroker      string // PEERDRIVE_MQTT_BROKER, default tcp://broker.emqx.io:1883
	MQTTTopicPref   string // PEERDRIVE_MQTT_TOPIC_PREFIX, default peerdrive/v1
	MQTTCollections string // PEERDRIVE_MQTT_COLLECTIONS, comma-separated collection hash shards to watch
	DiscoverURL     string // PEERDRIVE_DISCOVER_URL, discovery API of a self-hosted signaling server (takes priority over MQTT when set)

	// DiscoverPresence enables node-level "presence room" discovery (PEERDRIVE_DISCOVER_PRESENCE, default true).
	// When enabled, nodes additionally join a fixed public room so that two nodes with **no shared
	// collection hash** can still discover and directly connect to each other (a fundamental capability
	// of the interconnection layer).
	// When disabled, falls back to pure content-shard discovery (nodes only meet if they declare
	// the same collection).
	// Privacy tradeoff: on = discovery server and nodes in the same room can see this node is online
	// and its peerId; off = only visible in rooms where you share collections. Only applies to HTTP
	// discovery (self-hosted signaling); the public MQTT broker does not add a presence room (a global
	// room on a public broker is equivalent to broadcasting).
	DiscoverPresence bool

	// DiscoverMode controls which discovery mechanism the node uses (PEERDRIVE_DISCOVER_MODE).
	//
	// Valid values:
	//   - "auto" (default): current behavior — if DiscoverURL is set, use HTTP discovery
	//     (self-hosted signaling server); otherwise if MQTT_ENABLE=true, use MQTT discovery.
	//   - "peerjs": use only PeerJS signaling for connections; no HTTP or MQTT discovery.
	//     Nodes interconnect via the static PEERDRIVE_PEERJS_PEERS list (and market-joined
	//     peers). This is how a node opts into the official 0.peerjs.com signaling without
	//     announcing presence to any self-hosted discovery API.
	//   - "discover": force HTTP discovery via DiscoverURL (requires DiscoverURL non-empty).
	//   - "mqtt": force MQTT discovery (requires MQTT_ENABLE=true).
	//   - "off": completely disable discovery (same behavior as "peerjs", but semantically
	//     more explicit for "no discovery server" deployments).
	//
	// Background: the old behavior had no way to turn discovery off — as long as the
	// default DiscoverURL was set, auto mode kept announcing to the self-hosted server.
	// "可选用官方信令或自定义信令" needs an explicit off/peerjs lane (goal: signal
	// optional). "off" and "peerjs" behave identically; use whichever reads clearer.
	DiscoverMode string

	// URLSourceTemplate is the URL source template for the unified source system (PEERDRIVE_URL_SOURCE_TEMPLATE).
	// Empty = no URL source registered. %s = sha256 hash; when %d is present (%d for offset,size),
	// declares CapStream (Range chunks); otherwise CapFile (full fetch).
	// Example: https://example.com/ipfs/%s or https://example.com/f/%s?off=%d&size=%d
	URLSourceTemplate string

	// ── OpenList source (PEERDRIVE_OPENLIST_*, optional data source) ──
	//
	// OpenListEnable is the master switch (PEERDRIVE_OPENLIST_ENABLE, default **false**).
	// Why default off: the node would otherwise issue requests to an
	// operator-configured third-party aggregation layer for hashes it does not
	// have locally. Opt-in keeps a node that never configures it byte-identical
	// to one without the source: nothing is constructed and no registration happens.
	//
	// OpenList cannot be imported — its driver layer lives under internal/, which
	// Go's import rules confine to its own module — so the backend is reached over
	// its HTTP API. Only /p/*path is usable: it streams the bytes through the
	// OpenList process and honours Range, whereas /d/*path 302-redirects to the
	// cloud provider's direct URL and would bypass this source's sha256 check.
	//
	// OpenList has no trustworthy content hash of its own, so the hash→path mapping
	// comes from an operator-supplied index file (OpenListIndexPath). Until a
	// crawler builds that table, it is static; OpenListSource.Reload is the seam
	// that delivery will use.
	OpenListEnable      bool   // PEERDRIVE_OPENLIST_ENABLE (default false)
	OpenListBaseURL     string // PEERDRIVE_OPENLIST_BASE_URL: scheme+host, no trailing /p
	OpenListIndexPath   string // PEERDRIVE_OPENLIST_INDEX_FILE: hash→path JSON table
	OpenListToken       string // PEERDRIVE_OPENLIST_TOKEN: sent as Authorization: Bearer ***
	OpenListName        string // PEERDRIVE_OPENLIST_NAME (default "openlist")
	OpenListPriority    int    // PEERDRIVE_OPENLIST_PRIORITY (default 900: after local/peer/url)
	OpenListTimeoutSecs      int    // PEERDRIVE_OPENLIST_TIMEOUT_SECS (default 120)
	OpenListVerify           bool   // PEERDRIVE_OPENLIST_VERIFY (default true: hash full fetches)
	OpenListCrawl            bool   // PEERDRIVE_OPENLIST_CRAWL (default false: crawl remote OpenList /api/fs/list on startup)
	OpenListCrawlRoot        string // PEERDRIVE_OPENLIST_CRAWL_ROOT (default "/": remote root path to crawl)
	OpenListCrawlConcurrency int    // PEERDRIVE_OPENLIST_CRAWL_CONCURRENCY (default 4)
	OpenListCrawlTimeoutSecs int    // PEERDRIVE_OPENLIST_CRAWL_TIMEOUT_SECS (default 30)

	DownloadDir         string
	FolderMaxDepth      int // PEERDRIVE_FOLDER_MAX_DEPTH: register_folder max recursion depth (default 1 = scan current directory only)
	MaxPeers            int
	DownloadOrder       string
	DownloadTimeoutSecs int

	// ── Optional aria2c plugin integration (PEERDRIVE_ARIA2_*, Issue #236) ──
	// When Aria2Enable is false (default): no aria2 client or worker starts, zero overhead.
	// When true: aria2 RPC bridge is active, enabling multi-connection accelerated downloading
	// and transfer task synchronization.
	Aria2Enable      bool   // PEERDRIVE_ARIA2_ENABLE (default false)
	Aria2RPCURL      string // PEERDRIVE_ARIA2_RPC_URL (default "http://127.0.0.1:6800/jsonrpc")
	Aria2RPCSecret   string // PEERDRIVE_ARIA2_RPC_SECRET (optional secret token)
	Aria2DownloadDir string // PEERDRIVE_ARIA2_DIR (target directory, defaults to DownloadDir)

	ForwardRules string // PEERDRIVE_FORWARD_RULES: "key1:8080,key2:8443" (forwarding auth whitelist; key is the credential; recommend chmod 600 on config file)

	// ── Optional ech-proxy module (PEERDRIVE_ECH_PROXY_ENABLE, default OFF) ──
	// When enabled, https://pbs.twimg.com/<path>?<q> is rewritten to
	// https://twimg-pbs.l.moonchan.xyz:8443/<path>?<q> and routed through a local
	// ech-proxy subprocess that the node downloads itself from the GitHub release channel.
	// Disabled means the module's code never runs at all: no download, no child process,
	// no rewrite — behaviour is identical to a node without the module.
	//
	// There is no silent fallback: if the download, the checksum, the port bind or the
	// spawn fails, startup fails with an explicit error. A half-configured rewrite that
	// quietly went direct would defeat the purpose of the module.
	ECHProxyEnable        bool   // PEERDRIVE_ECH_PROXY_ENABLE (default false)
	ECHProxyAddr          string // PEERDRIVE_ECH_PROXY_ADDR (default 127.0.0.1:8443)
	ECHProxyInstallDir    string // PEERDRIVE_ECH_PROXY_INSTALL_DIR (default <PEERDRIVE_STORAGE>/ech-proxy)
	ECHProxyVersion       string // PEERDRIVE_ECH_PROXY_VERSION (default v1.3.0)
	ECHProxyEntryHost     string // PEERDRIVE_ECH_PROXY_ENTRY_HOST (default twimg-pbs.l.moonchan.xyz)
	ECHProxySkipTLS       bool   // PEERDRIVE_ECH_PROXY_SKIP_TLS (default true: the proxy uses a local self-signed cert)
	ECHProxyIPMode        string // PEERDRIVE_ECH_PROXY_IP_MODE (default v4; also "auto", "v6", "")
	ECHProxyStartAttempts int    // PEERDRIVE_ECH_PROXY_START_ATTEMPTS (default 3)

	// ── HTTP hardening (see internal/router/middleware.go) ──
	// RateLimitRPS is the per-IP request rate limit (PEERDRIVE_RATE_LIMIT_RPS, 0 = unlimited).
	// Default 30: enough for normal admin panel usage (list polling + manual ops are far below this
	// volume), while blocking "a script spamming the API". Upload/cross-node pull endpoints, which
	// consume real bandwidth, are also protected by this.
	RateLimitRPS float64
	// DisableCSP disables Content-Security-Policy (PEERDRIVE_CSP=off).
	// Only an escape hatch for when embedding third-party pages or old-browser compatibility breaks. Default on.
	DisableCSP bool
	// DisableSwagger disables /swagger/* (PEERDRIVE_SWAGGER=off). Default on:
	// it publishes all endpoints and parameter structures, which on public deployments is like
	// handing over a map to attackers.
	DisableSwagger bool
	// Host is the listen address (PEERDRIVE_HOST, default empty = listen on all interfaces).
	//
	// Why it's worth configuring: the admin surface (/ws/peer) has no account system, so the boundary
	// is "who can reach this port". Default 0.0.0.0 means anyone on the same LAN can connect and
	// act as admin. If the admin panel is only used locally, setting 127.0.0.1 is the cheapest wall.
	Host string

	// TrustedProxies are trusted reverse proxies (PEERDRIVE_TRUSTED_PROXIES, comma-separated IP/CIDR).
	//
	// Why this must be explicitly configured: gin **trusts all** proxies by default, and ClientIP()
	// directly takes X-Forwarded-For — but this header can be forged by the client, meaning rate
	// limiting and log IPs are all filled in by the attacker. Empty = only trust RemoteAddr (the
	// correct choice for direct deployments); behind a reverse proxy, be sure to fill in the last
	// hop's address, otherwise everyone will be treated as one IP and rate-limited together.
	TrustedProxies string

	// ── Node sharing scope (PEERDRIVE_SHARE_*, doc/NETDISK.md M2 / ROADMAP phase 5) ──
	//
	// ShareEnable is the sharing master switch (PEERDRIVE_SHARE_ENABLE, default **false**).
	// Why default off: peers can list "what this node offers" via share frames; enabling it equals
	// publicly exposing the content manifest. Defaulting to full sharing is a privacy incident —
	// it must be explicitly enabled by the operator.
	ShareEnable bool
	// ShareCollections are collections to share externally (PEERDRIVE_SHARE_COLLECTIONS, comma-separated):
	// 64hex collection hash, or "all" = all public collections. Restricted/private collections listed here
	// will still be skipped (no identity to verify; cannot be shared safely before phase 7).
	ShareCollections string
	// ShareDirs are directories to share externally (PEERDRIVE_SHARE_DIRS, comma-separated).
	// Semantics: files in file_index whose path falls under these directories enter the sharing list.
	// Empty = no directory-based sharing (only collection sharing). Only relative/absolute path
	// prefix matching; true out-of-bounds reads are still caught by file_index.IsPathAllowed (upload root).
	ShareDirs string
	// ShareFriends are friend node IDs (PEERDRIVE_SHARE_FRIENDS, comma-separated):
	// private-level content is allowed through to these nodes (see model.LevelPrivate).
	//
	// Note: peer ids are self-reported by the peer; the signaling server does not verify identity.
	// The friend list is only meaningful on connections that have **passed PSK admission** — without
	// PSK, anyone can connect and claim to be a friend. Strong identity requires the account system
	// (ROADMAP phase 7). Here it's just the initial runtime state; change it from the admin panel later.
	ShareFriends string

	// ── Auto-extract after pull (PEERDRIVE_AUTO_EXTRACT_*) ──
	//
	// AutoExtract is the master switch (default false). When enabled, after a cross-node
	// pull completes and the file is registered in file_index, the node attempts to extract
	// archives (zip/tar/gz/tar.gz/tar.bz2) into a sibling directory. Extracted files
	// are individually registered in file_index. The original archive is optionally deleted.
	//
	// Why default off: auto-extract is a convenience feature that adds attack surface
	// (zip bombs, path traversal). Operators must explicitly opt in.
	AutoExtract           bool
	AutoExtractMaxSize    int64 // PEERDRIVE_AUTO_EXTRACT_MAX_SIZE: max total extracted bytes (default 500MB)
	AutoExtractMaxRatio   int   // PEERDRIVE_AUTO_EXTRACT_MAX_RATIO: max compression ratio (default 100)
	AutoExtractMaxFiles   int   // PEERDRIVE_AUTO_EXTRACT_MAX_FILES: max number of extracted files (default 10000)
	AutoExtractDeleteOrig bool  // PEERDRIVE_AUTO_EXTRACT_DELETE_ORIGINAL: delete archive after extraction (default true)

	// ── iwara.tv via ech-proxy (PEERDRIVE_IWARA_*, optional module) ──
	//
	// IwaraEnable is the master switch (PEERDRIVE_IWARA_ENABLE, default **false**).
	// Why default off: the module downloads and runs an external executable
	// (ech-proxy) and injects a user-supplied cookie into requests to a
	// third-party site. Both are risky operations that must be explicitly
	// opted into.
	//
	// When enabled, the module downloads ech-proxy's Windows executable,
	// verifies it against the published SHA256, starts it as a child process,
	// and routes iwara.tv API calls through the local TLS proxy. Download
	// URLs are resolved via the same API flow used by open-source iwara
	// downloaders (Izumiko/iwaradl, IwaraEnhance/IwaraDownloadTool).
	IwaraEnable          bool
	IwaraCookie          string // PEERDRIVE_IWARA_COOKIE: iwara login cookie (e.g. "iwara_session=...")
	IwaraEchProxyExe     string // PEERDRIVE_IWARA_ECH_PROXY_EXE: local exe path (override auto-download)
	IwaraEchProxyPort    int    // PEERDRIVE_IWARA_ECH_PROXY_PORT: ech-proxy listen port (default 8443)
	IwaraEntrySuffix     string // PEERDRIVE_IWARA_ENTRY_SUFFIX: entry domain suffix (default "l.moonchan.xyz")
	IwaraUpstreamSuffix  string // PEERDRIVE_IWARA_UPSTREAM_SUFFIX: upstream domain (default "iwara.tv")
	IwaraEchProxyVersion string // PEERDRIVE_IWARA_ECH_PROXY_VERSION: ech-proxy release tag (default "v1.3.0")

	// ── ExHentai routing (PEERDRIVE_EXHENTA_*, optional module) ──
	//
	// ExhentaiEnable is the master switch (PEERDRIVE_EXHENTA_ENABLE, default **false**).
	// Why default off: the module rewrites requests to a third-party gallery through
	// an operator-configured mirror, and the routing table it uses carries a login
	// cookie. Like the iwara module above, that is an explicit opt-in.
	//
	// When enabled with no ExhentaiConfigURL the module runs on a built-in table
	// (exhentai.org / e-hentai.org → ex.4545810.xyz) and its requests are only
	// available to the node's own URL sources. With ExhentaiConfigURL set, the
	// routing table is fetched from that URL and re-fetched in the background, so
	// an operator changes the table by pushing a document — no restart.
	//
	// Unlike the ech-proxy modules above, there is no subprocess and no silent
	// fallback to "direct": a bad or unreachable config URL means the module runs
	// on the built-in table and logs the failure. A node must never fail to boot
	// because a config server is down.
	ExhentaiEnable         bool   // PEERDRIVE_EXHENTA_ENABLE (default false)
	ExhentaiConfigURL      string // PEERDRIVE_EXHENTA_CONFIG_URL (default "")
	ExhentaiConfigInsecure bool   // PEERDRIVE_EXHENTA_CONFIG_INSECURE (default false)
	ExhentaiConfigAuth     string // PEERDRIVE_EXHENTA_CONFIG_AUTH (default ""): Authorization header value for config fetches

	// ── twitter-pic gallery (PEERDRIVE_TWITTERPIC_*, optional module) ──
	//
	// TwitterPicEnable is the master switch (PEERDRIVE_TWITTERPIC_ENABLE, default
	// **false**). It is the twitter-pic-go data surface integrated into peerdrive
	// as "one user = one collection": the module pulls a gallery user's timeline
	// from the twitter-pic API, ingests the media into the sha-file system and
	// emits a content-addressed collection JSON per user (see internal/twitterpic).
	//
	// Why default off: the module downloads media from a third-party site on
	// demand and writes into the node's storage; like the iwara/exhentai
	// modules it is an explicit opt-in.
	//
	// Empty string values defer to the package defaults (twitterpic.DefaultBaseURL
	// / DefaultProxyBase), so defaults live in one place.
	TwitterPicEnable    bool   // PEERDRIVE_TWITTERPIC_ENABLE (default false)
	TwitterPicBaseURL   string // PEERDRIVE_TWITTERPIC_BASE_URL (default https://x.moonchan.xyz/api/twitter)
	TwitterPicProxyBase string // PEERDRIVE_TWITTERPIC_PROXY_BASE (default https://pbs.moonchan.xyz): ech-url 备选基址
	TwitterPicMaxFiles  int    // PEERDRIVE_TWITTERPIC_MAX_FILES: 媒体摄取上限（0 = 全部）
	TwitterPicMaxBytes  int64  // PEERDRIVE_TWITTERPIC_MAX_BYTES: 单文件摄取上限（0 = 不限）
	TwitterPicTimeout   int    // PEERDRIVE_TWITTERPIC_TIMEOUT_SECS: 单请求超时秒数（默认 20）

	// ── Signal subcommand (PEERSIGNAL_* / PEERJS_TOKENS, backward compat with old peersignal binary) ──
	//
	// The `peerdrive signal` subcommand preserves the old peersignal env var names so
	// existing deployment scripts don't break. These fields centralise what was
	// previously read directly via os.Getenv inside services.go (C-2 fix): all env
	// parsing now lives in config.Load().
	SignalAddr    string // PEERSIGNAL_ADDR, default ":9000"
	SignalTokens  string // PEERJS_TOKENS (comma-separated signalling token whitelist)
	SignalCORS    string // PEERSIGNAL_CORS (comma-separated CORS allow-list)
	SignalTLSCert string // PEERSIGNAL_TLS_CERT (PEM; with SignalTLSKey serves HTTPS/WSS)
	SignalTLSKey  string // PEERSIGNAL_TLS_KEY (PEM)
	// Rate-limit knobs for the public signal endpoints (N3). Defaults match
	// cmd/peersignal's -rate-* flags; see SignalRateLimit in internal/services.
	SignalRateAnnounce float64 // PEERDRIVE_SIGNAL_RATE_ANNOUNCE, default 1
	SignalRateWS       float64 // PEERDRIVE_SIGNAL_RATE_WS, default 2
	SignalRateID       float64 // PEERDRIVE_SIGNAL_RATE_ID, default 5
	// RegDBPath is the registration server's SQLite path (PEERDRIVE_REG_DB).
	// Empty → falls back to cfg.StorageDir + "/reg.db" then "./reg.db".
	RegDBPath string
	// LegacyDBPath is the old "DB_PATH" env var (without PEERDRIVE_ prefix)
	// used by the reg server's DB path resolution chain.
	LegacyDBPath string // DB_PATH
}

// IsOriginAllowed checks whether the given Origin is in the allow list, supporting
// wildcards (*) and subdomain wildcards (*.example.com).
func (c *Config) IsOriginAllowed(origin string) bool {
	if c.AllowedOrigins == "*" || c.AllowedOrigins == "" {
		return true
	}
	for _, o := range strings.Split(c.AllowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" || strings.EqualFold(origin, o) {
			return true
		}
		// Subdomain wildcard: supports both "*.example.com" and "https://*.example.com" formats
		if strings.HasPrefix(o, "*.") || strings.Contains(o, "://*.") {
			pattern := o
			if idx := strings.Index(o, "://*"); idx >= 0 {
				pattern = o[idx+3:] // "https://*.example.com" → "*.example.com"
			}
			if strings.HasSuffix(origin, pattern[1:]) {
				return true
			}
		}
	}
	return false
}

// DefaultRootPath returns the root path of the current OS (C:\ on Windows, / on others).
func DefaultRootPath() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	}
	return "/"
}

// DefaultConfig returns the default configuration struct without reading environment variables.
func DefaultConfig() *Config {
	return &Config{
		Port:               "3000",
		DBPath:             "./peerdrive.db",
		StorageDir:         "./storage",
		StorageEnable:      true,
		AllowedOrigins:     "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev,https://*.pages.dev",
		RegistrationServer: "",
		AdminToken:          "",
		RemoteControlEnable: false,
		RemoteControlToken:  "",
		MaxUploadBytes:      100 * 1024 * 1024,
		MaxUploadBytesAnon: 10 * 1024 * 1024,
		BTDHTEnabled:       false,
		BTDHTListenAddr:    ":6881",
		IPFSGatewayEnable:  true,
		IPFSGateways:       "https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link",
		WebRTCSTUNServer:   "stun:stun.l.google.com:19302",
		WebRTCTURNServer:   "",
		WebRTCTURNUsername: "",
		WebRTCTURNPassword: "",
		PeerJSEnable:       true,
		PeerJSHost:         DefaultSignalHost,
		PeerJSPort:         DefaultSignalPort,
		PeerJSKey:          DefaultSignalKey,
		PeerJSID:           "",
		PeerJSSecure:       true,
		PeerJSPeers:        "",
		PeerJSXOREnable:    false,
		PeerJSXORKey:       "",
		PeerPSK:            "",
		PeerBlocklist:      "",
		PeerAnonPolicy:     "open",
		MQTTEnable:         false,
		MQTTBroker:         "tcp://broker.emqx.io:1883",
		MQTTTopicPref:      "peerdrive/v1",
		MQTTCollections:    "",
		DiscoverURL:        DefaultDiscoverURL,
		DiscoverMode:       "auto",
		DiscoverPresence:   true,
		URLSourceTemplate:  "",
		OpenListEnable:     false,
		OpenListBaseURL:     "",
		OpenListIndexPath:   "",
		OpenListToken:       "",
		OpenListName:        "openlist",
		OpenListPriority:    900,
		OpenListTimeoutSecs:         120,
		OpenListVerify:              true,
		OpenListCrawl:               false,
		OpenListCrawlRoot:           "/",
		OpenListCrawlConcurrency:    4,
		OpenListCrawlTimeoutSecs:    30,
		DownloadDir:         "./downloads",
		FolderMaxDepth:      0,
		MaxPeers:            8,
		ShareEnable:         false,
		ShareCollections:    "",
		ShareDirs:           "",
		ShareFriends:        "",
		AutoExtract:         false,
		AutoExtractMaxSize:  500 * 1024 * 1024,
		AutoExtractMaxRatio: 100,
		AutoExtractMaxFiles: 10000,
		AutoExtractDeleteOrig: true,
		IwaraEnable:          false,
		IwaraCookie:          "",
		IwaraEchProxyExe:     "",
		IwaraEchProxyPort:    8443,
		IwaraEntrySuffix:     "l.moonchan.xyz",
		IwaraUpstreamSuffix:  "iwara.tv",
		IwaraEchProxyVersion: "v1.3.0",
		ExhentaiEnable:         false,
		ExhentaiConfigURL:      "",
		ExhentaiConfigInsecure: false,
		ExhentaiConfigAuth:     "",
		TwitterPicEnable:       false,
		TwitterPicBaseURL:      "",
		TwitterPicProxyBase:    "",
		TwitterPicMaxFiles:     0,
		TwitterPicMaxBytes:     0,
		TwitterPicTimeout:      20,
		DownloadOrder:          "local,ipfsgw,btdht,http",
		DownloadTimeoutSecs:    30,
		ForwardRules:           "",
		ECHProxyEnable:        false,
		ECHProxyAddr:          "127.0.0.1:8443",
		ECHProxyInstallDir:    "",
		ECHProxyVersion:       "v1.3.0",
		ECHProxyEntryHost:     "twimg-pbs.l.moonchan.xyz",
		ECHProxySkipTLS:       true,
		ECHProxyIPMode:        "v4",
		ECHProxyStartAttempts: 3,
		RateLimitRPS:          30,
		DisableCSP:            false,
		DisableSwagger:        false,
		Host:                  "",
		TrustedProxies:        "",
		SignalAddr:            ":9000",
		SignalTokens:          "",
		SignalCORS:            "",
		SignalTLSCert:         "",
		SignalTLSKey:          "",
		SignalRateAnnounce:    1,
		SignalRateWS:          2,
		SignalRateID:          5,
		RegDBPath:             "",
		LegacyDBPath:          "",
	}
}

// Load reads PEERDRIVE_* environment variables and returns the full configuration struct,
// using defaults for unset items.
func Load() *Config {
	return &Config{
		Port:               resolvePort(),
		DBPath:             resolveMainDBPath(),
		StorageDir:         getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable:      getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
		AllowedOrigins:     getEnv("PEERDRIVE_ALLOWED_ORIGINS", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev,https://*.pages.dev"),
		RegistrationServer:  getEnv("PEERDRIVE_REG_SERVER", ""),
		AdminToken:          getAdminToken(),
		RemoteControlEnable: getEnvBool("PEERDRIVE_REMOTE_CONTROL_ENABLE", false),
		RemoteControlToken:  getEnv("PEERDRIVE_REMOTE_CONTROL_TOKEN", ""),
		MaxUploadBytes:      getEnvInt64("PEERDRIVE_MAX_UPLOAD_BYTES", 100*1024*1024),     // 100MB default
		MaxUploadBytesAnon: getEnvInt64("PEERDRIVE_MAX_UPLOAD_ANON_BYTES", 10*1024*1024), // 10MB for anonymous
		BTDHTEnabled:       getEnvBool("PEERDRIVE_BT_DHT_ENABLE", false),                 // Default disabled: DHT init blocks startup; enable manually as needed

		BTDHTListenAddr:   getEnv("PEERDRIVE_BT_DHT_LISTEN", ":6881"),
		IPFSGatewayEnable: getEnvBool("PEERDRIVE_IPFS_GATEWAY_ENABLE", true),
		IPFSGateways:      getEnv("PEERDRIVE_IPFS_GATEWAYS", "https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link"),

		WebRTCSTUNServer: getEnv("PEERDRIVE_WEBRTC_STUN", "stun:stun.l.google.com:19302"),
		WebRTCTURNServer: getEnv("PEERDRIVE_WEBRTC_TURN", ""),

		WebRTCTURNUsername: getEnv("PEERDRIVE_WEBRTC_TURN_USER", ""),
		WebRTCTURNPassword: getEnv("PEERDRIVE_WEBRTC_TURN_PASS", ""),

		PeerJSEnable: getEnvBool("PEERDRIVE_PEERJS_ENABLE", true),
		PeerJSHost:   getEnv("PEERDRIVE_PEERJS_HOST", DefaultSignalHost),
		PeerJSPort:   getEnv("PEERDRIVE_PEERJS_PORT", DefaultSignalPort),
		PeerJSKey:    getEnv("PEERDRIVE_PEERJS_KEY", DefaultSignalKey),
		PeerJSID:     getEnv("PEERDRIVE_PEERJS_ID", ""),
		PeerJSSecure: getEnvBool("PEERDRIVE_PEERJS_SECURE", true),
		PeerJSPeers:  getEnv("PEERDRIVE_PEERJS_PEERS", ""),
		// 注意：本块列对齐是 deployconsistency 测试钉死的格式（逐字节 grep），
		// 新行只能追加、不能重排既有行的列（C-8）。
		PeerJSXOREnable: getEnvBool("PEERDRIVE_PEERJS_XOR_ENABLE", false),
		PeerJSXORKey:    getEnv("PEERDRIVE_PEERJS_XOR_KEY", ""),
		PeerPSK:      getEnv("PEERDRIVE_PSK", ""),
		PeerBlocklist:  getEnv("PEERDRIVE_PEER_BLOCKLIST", ""),
		PeerAnonPolicy: getEnv("PEERDRIVE_ANON_POLICY", "open"),

		MQTTEnable:        getEnvBool("PEERDRIVE_MQTT_ENABLE", false),
		MQTTBroker:        getEnv("PEERDRIVE_MQTT_BROKER", "tcp://broker.emqx.io:1883"),
		MQTTTopicPref:     getEnv("PEERDRIVE_MQTT_TOPIC_PREFIX", "peerdrive/v1"),
		MQTTCollections:   getEnv("PEERDRIVE_MQTT_COLLECTIONS", ""),
		DiscoverURL:       getEnv("PEERDRIVE_DISCOVER_URL", DefaultDiscoverURL),
		DiscoverMode:      getEnv("PEERDRIVE_DISCOVER_MODE", "auto"),
		DiscoverPresence:  getEnvBool("PEERDRIVE_DISCOVER_PRESENCE", true),
		URLSourceTemplate: getEnv("PEERDRIVE_URL_SOURCE_TEMPLATE", ""),

		OpenListEnable:      getEnvBool("PEERDRIVE_OPENLIST_ENABLE", false),
		OpenListBaseURL:     getEnv("PEERDRIVE_OPENLIST_BASE_URL", ""),
		OpenListIndexPath:   getEnv("PEERDRIVE_OPENLIST_INDEX_FILE", ""),
		OpenListToken:       getEnv("PEERDRIVE_OPENLIST_TOKEN", ""),
		OpenListName:        getEnv("PEERDRIVE_OPENLIST_NAME", "openlist"),
		OpenListPriority:    getEnvInt("PEERDRIVE_OPENLIST_PRIORITY", 900),
		OpenListTimeoutSecs:      getEnvInt("PEERDRIVE_OPENLIST_TIMEOUT_SECS", 120),
		OpenListVerify:           getEnvBool("PEERDRIVE_OPENLIST_VERIFY", true),
		OpenListCrawl:            getEnvBool("PEERDRIVE_OPENLIST_CRAWL", false),
		OpenListCrawlRoot:        getEnv("PEERDRIVE_OPENLIST_CRAWL_ROOT", "/"),
		OpenListCrawlConcurrency: getEnvInt("PEERDRIVE_OPENLIST_CRAWL_CONCURRENCY", 4),
		OpenListCrawlTimeoutSecs: getEnvInt("PEERDRIVE_OPENLIST_CRAWL_TIMEOUT_SECS", 30),

		DownloadDir:    getEnv("PEERDRIVE_DOWNLOAD_DIR", "./downloads"),
		FolderMaxDepth: getEnvInt("PEERDRIVE_FOLDER_MAX_DEPTH", 0), // 0=unlimited (full recursion; >0 limits depth)
		MaxPeers:       getEnvInt("PEERDRIVE_MAX_PEERS", 8),

		Aria2Enable:      getEnvBool("PEERDRIVE_ARIA2_ENABLE", false),
		Aria2RPCURL:      getEnv("PEERDRIVE_ARIA2_RPC_URL", "http://127.0.0.1:6800/jsonrpc"),
		Aria2RPCSecret:   getEnv("PEERDRIVE_ARIA2_RPC_SECRET", ""),
		Aria2DownloadDir: getEnv("PEERDRIVE_ARIA2_DIR", ""),

		ShareEnable:      getEnvBool("PEERDRIVE_SHARE_ENABLE", false),
		ShareCollections: getEnv("PEERDRIVE_SHARE_COLLECTIONS", ""),
		ShareDirs:        getEnv("PEERDRIVE_SHARE_DIRS", ""),
		ShareFriends:     getEnv("PEERDRIVE_SHARE_FRIENDS", ""),

		AutoExtract:           getEnvBool("PEERDRIVE_AUTO_EXTRACT", false),
		AutoExtractMaxSize:    getEnvInt64("PEERDRIVE_AUTO_EXTRACT_MAX_SIZE", 500*1024*1024),
		AutoExtractMaxRatio:   getEnvInt("PEERDRIVE_AUTO_EXTRACT_MAX_RATIO", 100),
		AutoExtractMaxFiles:   getEnvInt("PEERDRIVE_AUTO_EXTRACT_MAX_FILES", 10000),
		AutoExtractDeleteOrig: getEnvBool("PEERDRIVE_AUTO_EXTRACT_DELETE_ORIGINAL", true),

		// iwara.tv via ech-proxy (optional module)
		IwaraEnable:          getEnvBool("PEERDRIVE_IWARA_ENABLE", false),
		IwaraCookie:          getEnv("PEERDRIVE_IWARA_COOKIE", ""),
		IwaraEchProxyExe:     getEnv("PEERDRIVE_IWARA_ECH_PROXY_EXE", ""),
		IwaraEchProxyPort:    getEnvInt("PEERDRIVE_IWARA_ECH_PROXY_PORT", 8443),
		IwaraEntrySuffix:     getEnv("PEERDRIVE_IWARA_ENTRY_SUFFIX", "l.moonchan.xyz"),
		IwaraUpstreamSuffix:  getEnv("PEERDRIVE_IWARA_UPSTREAM_SUFFIX", "iwara.tv"),
		IwaraEchProxyVersion: getEnv("PEERDRIVE_IWARA_ECH_PROXY_VERSION", "v1.3.0"),

		// ExHentai routing (optional module)
		ExhentaiEnable:         getEnvBool("PEERDRIVE_EXHENTA_ENABLE", false),
		ExhentaiConfigURL:      getEnv("PEERDRIVE_EXHENTA_CONFIG_URL", ""),
		ExhentaiConfigInsecure: getEnvBool("PEERDRIVE_EXHENTA_CONFIG_INSECURE", false),
		ExhentaiConfigAuth:     getEnv("PEERDRIVE_EXHENTA_CONFIG_AUTH", ""),

		// twitter-pic gallery (optional module)
		TwitterPicEnable:    getEnvBool("PEERDRIVE_TWITTERPIC_ENABLE", false),
		TwitterPicBaseURL:   getEnv("PEERDRIVE_TWITTERPIC_BASE_URL", ""),
		TwitterPicProxyBase: getEnv("PEERDRIVE_TWITTERPIC_PROXY_BASE", ""),
		TwitterPicMaxFiles:  getEnvInt("PEERDRIVE_TWITTERPIC_MAX_FILES", 0),
		TwitterPicMaxBytes:  getEnvInt64("PEERDRIVE_TWITTERPIC_MAX_BYTES", 0),
		TwitterPicTimeout:   getEnvInt("PEERDRIVE_TWITTERPIC_TIMEOUT_SECS", 20),

		// 2026-10-07: dropped the bogus "ipfs" entry from the default (was
		// "local,ipfs,ipfsgw,btdht,http"). "ipfs" has not been in the fetcher
		// registry since the libp2p stack was removed (commit a5b090d); the
		// registry only knows local/ipfsgw/btdht/http
		// (downloader/universal_downloader.go buildFetchers). Every buildFetchers
		// call therefore emitted `downloader: unknown protocol "ipfs" in order,
		// skipping` at startup — a permanent warning about a protocol that cannot
		// exist, which trains operators to ignore downloader log lines.
		// The default now equals the empty-string fallback in buildFetchers, so
		// the default is exactly what the code would pick with no config at all.
		DownloadOrder:       getEnv("PEERDRIVE_DOWNLOAD_ORDER", "local,ipfsgw,btdht,http"),
		DownloadTimeoutSecs: getEnvInt("PEERDRIVE_DOWNLOAD_TIMEOUT", 30),

		ForwardRules: getEnv("PEERDRIVE_FORWARD_RULES", ""),

		ECHProxyEnable:        getEnvBool("PEERDRIVE_ECH_PROXY_ENABLE", false),
		ECHProxyAddr:          getEnv("PEERDRIVE_ECH_PROXY_ADDR", "127.0.0.1:8443"),
		ECHProxyInstallDir:    getEnv("PEERDRIVE_ECH_PROXY_INSTALL_DIR", ""),
		ECHProxyVersion:       getEnv("PEERDRIVE_ECH_PROXY_VERSION", "v1.3.0"),
		ECHProxyEntryHost:     getEnv("PEERDRIVE_ECH_PROXY_ENTRY_HOST", "twimg-pbs.l.moonchan.xyz"),
		ECHProxySkipTLS:       getEnvBool("PEERDRIVE_ECH_PROXY_SKIP_TLS", true),
		ECHProxyIPMode:        getEnv("PEERDRIVE_ECH_PROXY_IP_MODE", "v4"),
		ECHProxyStartAttempts: getEnvInt("PEERDRIVE_ECH_PROXY_START_ATTEMPTS", 3),

		RateLimitRPS:   getEnvFloat("PEERDRIVE_RATE_LIMIT_RPS", 30),
		DisableCSP:     os.Getenv("PEERDRIVE_CSP") == "off",
		DisableSwagger: os.Getenv("PEERDRIVE_SWAGGER") == "off",
		Host:           getEnv("PEERDRIVE_HOST", ""),
		TrustedProxies: getEnv("PEERDRIVE_TRUSTED_PROXIES", ""),

		SignalAddr:         getEnv("PEERSIGNAL_ADDR", ":9000"),
		SignalTokens:       getEnv("PEERJS_TOKENS", ""),
		SignalCORS:         getEnv("PEERSIGNAL_CORS", ""),
		SignalTLSCert:      getEnv("PEERSIGNAL_TLS_CERT", ""),
		SignalTLSKey:       getEnv("PEERSIGNAL_TLS_KEY", ""),
		SignalRateAnnounce: getEnvFloat("PEERDRIVE_SIGNAL_RATE_ANNOUNCE", 1),
		SignalRateWS:       getEnvFloat("PEERDRIVE_SIGNAL_RATE_WS", 2),
		SignalRateID:       getEnvFloat("PEERDRIVE_SIGNAL_RATE_ID", 5),
		RegDBPath:          getEnv("PEERDRIVE_REG_DB", ""),
		LegacyDBPath:       getEnv("DB_PATH", ""),
	}
}
