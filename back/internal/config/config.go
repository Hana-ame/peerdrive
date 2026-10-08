// Package config loads all configuration from environment variables (port, storage directory,
// PeerJS/WebRTC, BT DHT, forwarding, etc.).
// Load() reads PEERDRIVE_* environment variables and returns *Config.
package config

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

	"peerdrive/internal/log"
)

// Project-wide public signaling (everyone connects to it, no self-hosting needed).
// Environment variables can override (for self-hosting / internal debugging), but
// **the default values must be this pair** — the earlier default was PeerJS public cloud
// 0.peerjs.com/peerjs, which caused nodes and panels running with defaults to not find
// each other (they were on two different signaling servers, and discover returned no nodes).
const (
	DefaultSignalHost = "peersignal.moonchan.xyz"
	DefaultSignalPort = "443"
	DefaultSignalKey  = "pd-signal-1edf5e05e4a52b7351392574"
	// DefaultDiscoverURL is the public signaling discovery API (announce + node list).
	DefaultDiscoverURL = "https://peersignal.moonchan.xyz"
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

	PeerJSEnable bool // PEERDRIVE_PEERJS_ENABLE, default true
	// Defaults to the project's own public signaling peersignal.moonchan.xyz (not PeerJS public cloud).
	// Environment variables can still override (for self-hosting / internal debugging), but tutorials
	// don't teach changing this.
	PeerJSHost   string // PEERDRIVE_PEERJS_HOST, default peersignal.moonchan.xyz
	PeerJSPort   string // PEERDRIVE_PEERJS_PORT, default 443
	PeerJSKey    string // PEERDRIVE_PEERJS_KEY, default pd-signal-1edf5e05e4a52b7351392574
	PeerJSID     string // PEERDRIVE_PEERJS_ID, empty generates peerdrive-<random>
	PeerJSSecure bool   // PEERDRIVE_PEERJS_SECURE, default true
	PeerJSPeers  string // PEERDRIVE_PEERJS_PEERS, comma-separated peer node ids for auto-interconnection on startup

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

	DownloadDir         string
	FolderMaxDepth      int // PEERDRIVE_FOLDER_MAX_DEPTH: register_folder max recursion depth (default 1 = scan current directory only)
	MaxPeers            int
	DownloadOrder       string
	DownloadTimeoutSecs int

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
	ECHProxyEnable        bool    // PEERDRIVE_ECH_PROXY_ENABLE (default false)
	ECHProxyAddr          string  // PEERDRIVE_ECH_PROXY_ADDR (default 127.0.0.1:8443)
	ECHProxyInstallDir    string  // PEERDRIVE_ECH_PROXY_INSTALL_DIR (default <PEERDRIVE_STORAGE>/ech-proxy)
	ECHProxyVersion       string  // PEERDRIVE_ECH_PROXY_VERSION (default v1.3.0)
	ECHProxyEntryHost     string  // PEERDRIVE_ECH_PROXY_ENTRY_HOST (default twimg-pbs.l.moonchan.xyz)
	ECHProxySkipTLS       bool    // PEERDRIVE_ECH_PROXY_SKIP_TLS (default true: the proxy uses a local self-signed cert)
	ECHProxyIPMode        string  // PEERDRIVE_ECH_PROXY_IP_MODE (default v4; also "auto", "v6", "")
	ECHProxyStartAttempts int     // PEERDRIVE_ECH_PROXY_START_ATTEMPTS (default 3)

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

// Load reads PEERDRIVE_* environment variables and returns the full configuration struct,
// using defaults for unset items.
func Load() *Config {
	return &Config{
		Port:               getEnv("PORT", "3000"),
		DBPath:             getEnv("PEERDRIVE_DB_PATH", "./peerdrive.db"),
		StorageDir:         getEnv("PEERDRIVE_STORAGE", "./storage"),
		StorageEnable:      getEnvBool("PEERDRIVE_STORAGE_ENABLE", true),
		AllowedOrigins:     getEnv("PEERDRIVE_ALLOWED_ORIGINS", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://peerdrive.pages.dev,https://*.pages.dev"),
		RegistrationServer: getEnv("PEERDRIVE_REG_SERVER", ""),
		AdminToken:         getAdminToken(),
		MaxUploadBytes:     getEnvInt64("PEERDRIVE_MAX_UPLOAD_BYTES", 100*1024*1024),     // 100MB default
		MaxUploadBytesAnon: getEnvInt64("PEERDRIVE_MAX_UPLOAD_ANON_BYTES", 10*1024*1024), // 10MB for anonymous
		BTDHTEnabled:       getEnvBool("PEERDRIVE_BT_DHT_ENABLE", false),                 // Default disabled: DHT init blocks startup; enable manually as needed

		BTDHTListenAddr:   getEnv("PEERDRIVE_BT_DHT_LISTEN", ":6881"),
		IPFSGatewayEnable: getEnvBool("PEERDRIVE_IPFS_GATEWAY_ENABLE", true),
		IPFSGateways:      getEnv("PEERDRIVE_IPFS_GATEWAYS", "https://ipfs.io,https://cloudflare-ipfs.com,https://dweb.link"),

		WebRTCSTUNServer: getEnv("PEERDRIVE_WEBRTC_STUN", "stun:stun.l.google.com:19302"),
		WebRTCTURNServer: getEnv("PEERDRIVE_WEBRTC_TURN", ""),

		PeerJSEnable: getEnvBool("PEERDRIVE_PEERJS_ENABLE", true),
		PeerJSHost:   getEnv("PEERDRIVE_PEERJS_HOST", DefaultSignalHost),
		PeerJSPort:   getEnv("PEERDRIVE_PEERJS_PORT", DefaultSignalPort),
		PeerJSKey:    getEnv("PEERDRIVE_PEERJS_KEY", DefaultSignalKey),
		PeerJSID:     getEnv("PEERDRIVE_PEERJS_ID", ""),
		PeerJSSecure: getEnvBool("PEERDRIVE_PEERJS_SECURE", true),
		PeerJSPeers:  getEnv("PEERDRIVE_PEERJS_PEERS", ""),
		PeerPSK:      getEnv("PEERDRIVE_PSK", ""),

		MQTTEnable:        getEnvBool("PEERDRIVE_MQTT_ENABLE", false),
		MQTTBroker:        getEnv("PEERDRIVE_MQTT_BROKER", "tcp://broker.emqx.io:1883"),
		MQTTTopicPref:     getEnv("PEERDRIVE_MQTT_TOPIC_PREFIX", "peerdrive/v1"),
		MQTTCollections:   getEnv("PEERDRIVE_MQTT_COLLECTIONS", ""),
		DiscoverURL:       getEnv("PEERDRIVE_DISCOVER_URL", DefaultDiscoverURL),
		DiscoverMode:      getEnv("PEERDRIVE_DISCOVER_MODE", "auto"),
		DiscoverPresence:  getEnvBool("PEERDRIVE_DISCOVER_PRESENCE", true),
		URLSourceTemplate: getEnv("PEERDRIVE_URL_SOURCE_TEMPLATE", ""),

		DownloadDir: getEnv("PEERDRIVE_DOWNLOAD_DIR", "./downloads"),
		FolderMaxDepth: getEnvInt("PEERDRIVE_FOLDER_MAX_DEPTH", 0), // 0=unlimited (full recursion; >0 limits depth)
		MaxPeers:    getEnvInt("PEERDRIVE_MAX_PEERS", 8),

		ShareEnable:      getEnvBool("PEERDRIVE_SHARE_ENABLE", false),
		ShareCollections: getEnv("PEERDRIVE_SHARE_COLLECTIONS", ""),
		ShareDirs:        getEnv("PEERDRIVE_SHARE_DIRS", ""),
		ShareFriends:     getEnv("PEERDRIVE_SHARE_FRIENDS", ""),

		AutoExtract:           getEnvBool("PEERDRIVE_AUTO_EXTRACT", false),
		AutoExtractMaxSize:    getEnvInt64("PEERDRIVE_AUTO_EXTRACT_MAX_SIZE", 500*1024*1024),
		AutoExtractMaxRatio:   getEnvInt("PEERDRIVE_AUTO_EXTRACT_MAX_RATIO", 100),
		AutoExtractMaxFiles:   getEnvInt("PEERDRIVE_AUTO_EXTRACT_MAX_FILES", 10000),
		AutoExtractDeleteOrig: getEnvBool("PEERDRIVE_AUTO_EXTRACT_DELETE_ORIGINAL", true),

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

		RateLimitRPS:    getEnvFloat("PEERDRIVE_RATE_LIMIT_RPS", 30),
		DisableCSP:      os.Getenv("PEERDRIVE_CSP") == "off",
		DisableSwagger:  os.Getenv("PEERDRIVE_SWAGGER") == "off",
		Host:            getEnv("PEERDRIVE_HOST", ""),
		TrustedProxies:  getEnv("PEERDRIVE_TRUSTED_PROXIES", ""),

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

// Validate catches "misconfigured but won't error" configs at startup time.
//
// Why it's needed: env vars are strings; one typo won't crash the process, it just
// causes subtle misbehavior (PORT=300o → bind failure; PEERDRIVE_STORAGE= empty →
// files land in the current working directory). By the time the user notices, the
// destination is already a pile of unrecoverable files. The principle is "fail fast":
// a clear message at startup is better than slow errors at runtime.
func Validate(c *Config) error {
	var errs []string

	if n, err := strconv.Atoi(c.Port); err != nil || n <= 0 || n > 65535 {
		errs = append(errs, fmt.Sprintf("PORT=%q is not a valid port (1-65535)", c.Port))
	}
	if strings.TrimSpace(c.DBPath) == "" {
		errs = append(errs, "PEERDRIVE_DB_PATH cannot be empty")
	}
	if strings.TrimSpace(c.StorageDir) == "" {
		errs = append(errs, "PEERDRIVE_STORAGE cannot be empty")
	}
	if strings.TrimSpace(c.DownloadDir) == "" {
		errs = append(errs, "PEERDRIVE_DOWNLOAD_DIR cannot be empty")
	}
	if p := strings.TrimSpace(c.PeerJSPort); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n <= 0 || n > 65535 {
			errs = append(errs, fmt.Sprintf("PEERDRIVE_PEERJS_PORT=%q is not a valid port", p))
		}
	}
	if c.MaxPeers <= 0 {
		errs = append(errs, fmt.Sprintf("PEERDRIVE_MAX_PEERS=%d must be positive", c.MaxPeers))
	}

	// DiscoverMode 合法性 + 交叉校验：模式与依赖项不符时启动期就拦，
	// 而不是运行期静默降级（那会让运营者以为 discovery=discover 其实没开）。
	switch c.DiscoverMode {
	case "auto", "peerjs", "discover", "mqtt", "off":
		// valid
	case "":
		errs = append(errs, "PEERDRIVE_DISCOVER_MODE cannot be empty")
	default:
		errs = append(errs, fmt.Sprintf("PEERDRIVE_DISCOVER_MODE=%q is invalid (valid: auto, peerjs, discover, mqtt, off)", c.DiscoverMode))
	}
	if c.DiscoverMode == "discover" && c.DiscoverURL == "" {
		errs = append(errs, "PEERDRIVE_DISCOVER_MODE=discover requires PEERDRIVE_DISCOVER_URL to be set")
	}
	if c.DiscoverMode == "mqtt" && !c.MQTTEnable {
		errs = append(errs, "PEERDRIVE_DISCOVER_MODE=mqtt requires PEERDRIVE_MQTT_ENABLE=true")
	}

	// ech-proxy: validate only when the optional module is enabled, so a node that never
	// turns it on cannot be broken by a stale default. Every check is a hard startup
	// error — the module refuses to start half-configured rather than degrading silently.
	if c.ECHProxyEnable {
		if strings.TrimSpace(c.ECHProxyVersion) == "" {
			errs = append(errs, "PEERDRIVE_ECH_PROXY_VERSION cannot be empty when ech-proxy is enabled")
		}
		if strings.TrimSpace(c.ECHProxyEntryHost) == "" {
			errs = append(errs, "PEERDRIVE_ECH_PROXY_ENTRY_HOST cannot be empty when ech-proxy is enabled")
		}
		switch c.ECHProxyIPMode {
		case "", "auto", "v4", "v6":
			// valid
		default:
			errs = append(errs, fmt.Sprintf("PEERDRIVE_ECH_PROXY_IP_MODE=%q is invalid (valid: auto, v4, v6)", c.ECHProxyIPMode))
		}
		if c.ECHProxyStartAttempts < 1 {
			errs = append(errs, fmt.Sprintf("PEERDRIVE_ECH_PROXY_START_ATTEMPTS=%d must be >= 1", c.ECHProxyStartAttempts))
		}
		if addr := strings.TrimSpace(c.ECHProxyAddr); addr == "" {
			errs = append(errs, "PEERDRIVE_ECH_PROXY_ADDR cannot be empty when ech-proxy is enabled")
		} else {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				errs = append(errs, fmt.Sprintf("PEERDRIVE_ECH_PROXY_ADDR=%q is not a valid host:port", addr))
			} else if strings.TrimSpace(host) == "" {
				errs = append(errs, fmt.Sprintf("PEERDRIVE_ECH_PROXY_ADDR=%q has an empty host", addr))
			} else if n, err := strconv.Atoi(port); err != nil || n <= 0 || n > 65535 {
				errs = append(errs, fmt.Sprintf("PEERDRIVE_ECH_PROXY_ADDR=%q has an invalid port %q", addr, port))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

// getAdminToken resolves the admin token from either PEERDRIVE_ADMIN_TOKEN (env var)
// or PEERDRIVE_ADMIN_TOKEN_FILE (path to a file containing the token on line 1).
// Env var takes precedence; file is a fallback for secrets stored on disk (chmod 600).
// The file content is trimmed of leading/trailing whitespace and only the first line is used.
func getAdminToken() string {
	if tok := getEnv("PEERDRIVE_ADMIN_TOKEN", ""); tok != "" {
		return tok
	}
	filePath := getEnv("PEERDRIVE_ADMIN_TOKEN_FILE", "")
	if filePath == "" {
		return ""
	}
	// 审计 R2 MEDIUM（2026-10-08）：文件权限与路径校验。
	// 注释推荐 chmod 600——检查并在过宽时告警，但不阻断启动
	//（权限修复需要 root，强制拒绝会让整个节点无法启动）。
	if fi, err := os.Stat(filePath); err == nil {
		perm := fi.Mode().Perm()
		if perm&0077 != 0 {
			log.LogWarn("config: PEERDRIVE_ADMIN_TOKEN_FILE %s permissions %o — recommended 600 (secret file); group/other-readable mode weakens secret isolation", filePath, perm)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			log.LogWarn("config: PEERDRIVE_ADMIN_TOKEN_FILE %s is a symlink — consider using a regular file to avoid symlink-follow attacks", filePath)
		}
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		// 审计 R2 MEDIUM：文件读取失败必须记日志——之前的注释声称
		// "logged at startup (app.go)" 但实际没有，静默返回空串
		// 会让操作员误以为 admin auth 已启用。
		log.LogError("config: PEERDRIVE_ADMIN_TOKEN_FILE %s read failed: %v — admin auth disabled (token empty)", filePath, err)
		return ""
	}
	// Trim whitespace and take the first line only.
	tok := strings.TrimSpace(string(data))
	if idx := strings.IndexByte(tok, '\n'); idx >= 0 {
		tok = tok[:idx]
	}
	return strings.TrimSpace(tok)
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(val)
		if err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val, ok := os.LookupEnv(key); ok {
		n, err := strconv.ParseInt(val, 10, 64)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}

func getEnvFloat(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok {
		f, err := strconv.ParseFloat(val, 64)
		if err == nil && f >= 0 {
			return f
		}
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		n, err := strconv.Atoi(val)
		if err == nil && n > 0 {
			return n
		}
	}
	return defaultVal
}
