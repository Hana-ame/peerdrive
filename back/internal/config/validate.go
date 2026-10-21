package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

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
	// XOR 开关与密钥的交叉校验：开了却没配密钥 = 配置错误，启动期拦截
	// （库层会防御性降级明文并告警，但那是兜底不是预期状态）。
	if c.PeerJSXOREnable && strings.TrimSpace(c.PeerJSXORKey) == "" {
		errs = append(errs, "PEERDRIVE_PEERJS_XOR_ENABLE=true requires PEERDRIVE_PEERJS_XOR_KEY to be set")
	}
	if c.MaxPeers <= 0 {
		errs = append(errs, fmt.Sprintf("PEERDRIVE_MAX_PEERS=%d must be positive", c.MaxPeers))
	}
	if c.RemoteControlEnable && strings.TrimSpace(c.RemoteControlToken) == "" && strings.TrimSpace(c.AdminToken) == "" {
		errs = append(errs, "PEERDRIVE_REMOTE_CONTROL_ENABLE=true requires PEERDRIVE_REMOTE_CONTROL_TOKEN or PEERDRIVE_ADMIN_TOKEN to be set for security")
	}

	switch c.PeerAnonPolicy {
	case "", "open", "share_only", "deny":
		// valid
	default:
		errs = append(errs, fmt.Sprintf("PEERDRIVE_ANON_POLICY=%q is invalid (valid: open, share_only, deny)", c.PeerAnonPolicy))
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

	// twimg (pbs.twimg.com) ECH module: the only tunable is the egress IP
	// family. There is no listen address, release tag, entry host or TLS skip
	// to validate because the module runs no subprocess and binds no address —
	// the ECH handshake happens in-process (internal/echcore) against
	// cloudflare-ech.com.
	if c.ECHProxyEnable {
		switch c.ECHProxyIPMode {
		case "", "auto", "v4", "v6":
			// valid
		default:
			errs = append(errs, fmt.Sprintf("PEERDRIVE_ECH_PROXY_IP_MODE=%q is invalid (valid: auto, v4, v6)", c.ECHProxyIPMode))
		}
	}

	// The ExHentai config URL is where an operator publishes the routing table.
	// The module accepts an empty value (built-in table only), but a typo here
	// would otherwise only surface as a repeated "config fetch failed" log line
	// after a successful boot. Check the scheme now, while it is still a
	// configuration error rather than a runtime one.
	if c.ExhentaiEnable && strings.TrimSpace(c.ExhentaiConfigURL) != "" {
		u, err := url.Parse(strings.TrimSpace(c.ExhentaiConfigURL))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			errs = append(errs, "PEERDRIVE_EXHENTA_CONFIG_URL="+c.ExhentaiConfigURL+
				" is not a valid http:// or https:// URL")
		}
	}

	if c.Aria2Enable {
		rpcURL := strings.TrimSpace(c.Aria2RPCURL)
		if rpcURL == "" {
			errs = append(errs, "PEERDRIVE_ARIA2_RPC_URL cannot be empty when aria2 is enabled")
		} else {
			u, err := url.Parse(rpcURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
				errs = append(errs, fmt.Sprintf("PEERDRIVE_ARIA2_RPC_URL=%q is not a valid RPC URL", rpcURL))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errs, "\n  - "))
}
