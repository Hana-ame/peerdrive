// Package config loads all configuration from environment variables (port, storage directory,
// PeerJS/WebRTC, BT DHT, forwarding, etc.).
// Load() reads PEERDRIVE_* environment variables and returns *Config.
package config

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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
	// 2026-10-04: removed the three dead fields PublicAccessDomain / NodeAuthToken /
	// RegServerURL.
	// Why deletion instead of implementation:
	//   NodeAuthToken (PEERDRIVE_AUTH_TOKEN) was never an inbound gate to begin with —
	//   git show 539efc5 shows it was consumed by NodeRegistrar + service/p2p_key.go as the
	//   **outbound** identity this node uses when reporting to the registration server.
	//   Commit a5b090d deleted the libp2p stack along with those two consumers, leaving the
	//   field stranded; NodeRegistrar no longer exists and nodestate.Configure (its only
	//   remaining writer) has zero call sites. So "implement it now" would mean inventing a
	//   brand-new credential system and calling it a security guarantee — strictly worse than
	//   deleting it. Inbound admin-surface auth is RegistrationServer above
	//   (router/auth_middleware.go authDisabled()).
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
		DiscoverPresence:  getEnvBool("PEERDRIVE_DISCOVER_PRESENCE", true),
		URLSourceTemplate: getEnv("PEERDRIVE_URL_SOURCE_TEMPLATE", ""),

		DownloadDir: getEnv("PEERDRIVE_DOWNLOAD_DIR", "./downloads"),
		FolderMaxDepth: getEnvInt("PEERDRIVE_FOLDER_MAX_DEPTH", 0), // 0=unlimited (full recursion; >0 limits depth)
		MaxPeers:    getEnvInt("PEERDRIVE_MAX_PEERS", 8),

		ShareEnable:      getEnvBool("PEERDRIVE_SHARE_ENABLE", false),
		ShareCollections: getEnv("PEERDRIVE_SHARE_COLLECTIONS", ""),
		ShareDirs:        getEnv("PEERDRIVE_SHARE_DIRS", ""),
		ShareFriends:     getEnv("PEERDRIVE_SHARE_FRIENDS", ""),

		DownloadOrder:       getEnv("PEERDRIVE_DOWNLOAD_ORDER", "local,ipfs,ipfsgw,btdht,http"),
		DownloadTimeoutSecs: getEnvInt("PEERDRIVE_DOWNLOAD_TIMEOUT", 30),

		ForwardRules: getEnv("PEERDRIVE_FORWARD_RULES", ""),

		RateLimitRPS:    getEnvFloat("PEERDRIVE_RATE_LIMIT_RPS", 30),
		DisableCSP:      os.Getenv("PEERDRIVE_CSP") == "off",
		DisableSwagger:  os.Getenv("PEERDRIVE_SWAGGER") == "off",
		Host:            getEnv("PEERDRIVE_HOST", ""),
		TrustedProxies:  getEnv("PEERDRIVE_TRUSTED_PROXIES", ""),
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
