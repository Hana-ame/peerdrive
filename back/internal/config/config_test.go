package config

// Note: this file is the test for legacy code (see doc/archive/LEGACY.md, to be deleted/migrated); the "discovery background" was not annotated case by case. The "discovery background" convention applies to new code.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	// Make sure we're testing the defaults, unaffected by external environment variables
	os.Unsetenv("PEERDRIVE_BT_DHT_ENABLE")
	os.Unsetenv("PEERDRIVE_STORAGE_ENABLE")
	cfg := Load()

	assert.Equal(t, "3000", cfg.Port)
	assert.Equal(t, "./storage", cfg.StorageDir)
	assert.Equal(t, true, cfg.StorageEnable)

	assert.Equal(t, "stun:stun.l.google.com:19302", cfg.WebRTCSTUNServer)
	assert.Equal(t, "", cfg.WebRTCTURNServer)
}

// TestLoad_TwitterPicDefaultsOff pins the "开关关 = 零变化" contract at the
// config level: the twitter-pic module (twitter-pic-go 数据面的 peerdrive 整合)
// defaults to disabled, and the empty string values defer to the package
// defaults (twitterpic.DefaultBaseURL / DefaultProxyBase) rather than being
// baked into config.
func TestLoad_TwitterPicDefaultsOff(t *testing.T) {
	os.Unsetenv("PEERDRIVE_TWITTERPIC_ENABLE")
	os.Unsetenv("PEERDRIVE_TWITTERPIC_BASE_URL")
	os.Unsetenv("PEERDRIVE_TWITTERPIC_PROXY_BASE")
	os.Unsetenv("PEERDRIVE_TWITTERPIC_MAX_FILES")
	os.Unsetenv("PEERDRIVE_TWITTERPIC_TIMEOUT_SECS")
	cfg := Load()

	assert.False(t, cfg.TwitterPicEnable, "默认关：不显式开启不拉任何第三方数据")
	assert.Equal(t, "", cfg.TwitterPicBaseURL, "空 = 由 twitterpic 包默认")
	assert.Equal(t, "", cfg.TwitterPicProxyBase)
	assert.Equal(t, 0, cfg.TwitterPicMaxFiles)
	assert.Equal(t, 20, cfg.TwitterPicTimeout)
}

func TestGetEnv_DefaultWhenNotSet(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_KEY")
	assert.Equal(t, "fallback", getEnv("TEST_GET_ENV_KEY", "fallback"))
}

func TestGetEnv_ReturnsEnvValue(t *testing.T) {
	os.Setenv("TEST_GET_ENV_KEY", "custom-value")
	defer os.Unsetenv("TEST_GET_ENV_KEY")
	assert.Equal(t, "custom-value", getEnv("TEST_GET_ENV_KEY", "fallback"))
}

func TestGetEnvBool_Default(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_BOOL_KEY")
	assert.Equal(t, true, getEnvBool("TEST_GET_ENV_BOOL_KEY", true))
	assert.Equal(t, false, getEnvBool("TEST_GET_ENV_BOOL_KEY", false))
}

func TestGetEnvBool_ParsesTrueValues(t *testing.T) {
	os.Setenv("TEST_GET_ENV_BOOL_KEY", "true")
	defer os.Unsetenv("TEST_GET_ENV_BOOL_KEY")
	assert.Equal(t, true, getEnvBool("TEST_GET_ENV_BOOL_KEY", false))

	os.Setenv("TEST_GET_ENV_BOOL_KEY", "1")
	assert.Equal(t, true, getEnvBool("TEST_GET_ENV_BOOL_KEY", false))
}

func TestGetEnvBool_ParsesFalseValues(t *testing.T) {
	os.Setenv("TEST_GET_ENV_BOOL_KEY", "false")
	defer os.Unsetenv("TEST_GET_ENV_BOOL_KEY")
	assert.Equal(t, false, getEnvBool("TEST_GET_ENV_BOOL_KEY", true))

	os.Setenv("TEST_GET_ENV_BOOL_KEY", "0")
	assert.Equal(t, false, getEnvBool("TEST_GET_ENV_BOOL_KEY", true))
}

// TestIsOriginAllowed verifies the CORS Origin allowlist matching logic: exact
// match, wildcard, subdomain wildcard, empty/star passthrough, and mismatch
// rejection.
//
// Discovery background: 2026-09-05 CORS fix -- Cloudflare Pages preview
// deployments use a subdomain (6f670b67.peerdrive.pages.dev), which the default
// allowlist (exact peerdrive.pages.dev) doesn't match. Added tests to prevent
// the wildcard logic from regressing.
func TestIsOriginAllowed(t *testing.T) {
	tests := []struct {
		name      string
		origins   string
		origin    string
		wantAllow bool
	}{
		// star allows everything
		{"star_allows_all", "*", "https://anything.example.com", true},
		{"star_allows_all_2", "*", "http://evil.com", true},
		// empty string allows everything (default behavior)
		{"empty_allows_all", "", "https://anything.com", true},
		// exact match
		{"exact_match", "https://a.com,https://b.com", "https://a.com", true},
		{"exact_match_2", "https://a.com,https://b.com", "https://b.com", true},
		{"exact_mismatch", "https://a.com,https://b.com", "https://c.com", false},
		// case insensitive
		{"case_insensitive", "https://A.COM", "https://a.com", true},
		// subdomain wildcard *.example.com
		{"subdomain_wildcard_match", "https://*.example.com", "https://sub.example.com", true},
		{"subdomain_wildcard_match_deep", "https://*.example.com", "https://deep.sub.example.com", true},
		{"subdomain_wildcard_no_match", "https://*.example.com", "https://example.com", false},
		{"subdomain_wildcard_wrong_domain", "https://*.example.com", "https://evil.com", false},
		// Cloudflare Pages preview deployment scenario
		{"cf_pages_exact", "https://peerdrive.pages.dev", "https://peerdrive.pages.dev", true},
		{"cf_pages_preview_not_matched", "https://peerdrive.pages.dev", "https://6f670b67.peerdrive.pages.dev", false},
		{"cf_pages_preview_wildcard", "https://*.pages.dev,https://peerdrive.pages.dev", "https://6f670b67.peerdrive.pages.dev", true},
		// mixed list
		{"mixed_list", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://*.pages.dev", "https://preview.pages.dev", true},
		{"mixed_list_no_match", "http://localhost:5173,https://peerdrive.moonchan.xyz", "https://preview.pages.dev", false},
		// comma separated + whitespace tolerance
		{"whitespace_trim", "https://a.com, https://b.com", "https://b.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{AllowedOrigins: tt.origins}
			assert.Equal(t, tt.wantAllow, cfg.IsOriginAllowed(tt.origin))
		})
	}
}

// unsetECHProxyEnv clears every PEERDRIVE_ECH_PROXY_* variable so a test observes the
// documented defaults instead of whatever the developer's shell happens to export.
func unsetECHProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PEERDRIVE_ECH_PROXY_ENABLE", "PEERDRIVE_ECH_PROXY_ADDR",
		"PEERDRIVE_ECH_PROXY_INSTALL_DIR", "PEERDRIVE_ECH_PROXY_VERSION",
		"PEERDRIVE_ECH_PROXY_ENTRY_HOST", "PEERDRIVE_ECH_PROXY_SKIP_TLS",
		"PEERDRIVE_ECH_PROXY_IP_MODE", "PEERDRIVE_ECH_PROXY_START_ATTEMPTS",
	} {
		os.Unsetenv(k)
		t.Cleanup(func() { os.Unsetenv(k) })
	}
}

// TestLoad_ECHProxyDefaults covers the "optional module, default off" contract: with no
// environment set at all the flag is false and every other setting sits on its documented
// default, so a node that never mentions ech-proxy behaves exactly as before.
func TestLoad_ECHProxyDefaults(t *testing.T) {
	unsetECHProxyEnv(t)
	cfg := Load()

	assert.Equal(t, false, cfg.ECHProxyEnable, "the module must be opt-in")
	assert.Equal(t, "127.0.0.1:8443", cfg.ECHProxyAddr)
	assert.Equal(t, "", cfg.ECHProxyInstallDir, "empty means <PEERDRIVE_STORAGE>/ech-proxy, resolved by the caller")
	assert.Equal(t, "v1.3.0", cfg.ECHProxyVersion)
	assert.Equal(t, "twimg-pbs.l.moonchan.xyz", cfg.ECHProxyEntryHost)
	assert.Equal(t, true, cfg.ECHProxySkipTLS, "the local proxy serves a self-signed cert")
	assert.Equal(t, "v4", cfg.ECHProxyIPMode)
	assert.Equal(t, 3, cfg.ECHProxyStartAttempts)
}

// TestLoad_ECHProxyEnvOverrides proves each env var reaches its field with the right type.
func TestLoad_ECHProxyEnvOverrides(t *testing.T) {
	unsetECHProxyEnv(t)
	os.Setenv("PEERDRIVE_ECH_PROXY_ENABLE", "1")
	os.Setenv("PEERDRIVE_ECH_PROXY_ADDR", "127.0.0.1:9443")
	os.Setenv("PEERDRIVE_ECH_PROXY_INSTALL_DIR", "/var/lib/peerdrive/ech-proxy")
	os.Setenv("PEERDRIVE_ECH_PROXY_VERSION", "v1.3.1")
	os.Setenv("PEERDRIVE_ECH_PROXY_ENTRY_HOST", "twimg-pbs.l.moonchan.xyz")
	os.Setenv("PEERDRIVE_ECH_PROXY_SKIP_TLS", "false")
	os.Setenv("PEERDRIVE_ECH_PROXY_IP_MODE", "auto")
	os.Setenv("PEERDRIVE_ECH_PROXY_START_ATTEMPTS", "7")
	for _, k := range []string{
		"PEERDRIVE_ECH_PROXY_ENABLE", "PEERDRIVE_ECH_PROXY_ADDR", "PEERDRIVE_ECH_PROXY_INSTALL_DIR",
		"PEERDRIVE_ECH_PROXY_VERSION", "PEERDRIVE_ECH_PROXY_ENTRY_HOST", "PEERDRIVE_ECH_PROXY_SKIP_TLS",
		"PEERDRIVE_ECH_PROXY_IP_MODE", "PEERDRIVE_ECH_PROXY_START_ATTEMPTS",
	} {
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	cfg := Load()
	assert.Equal(t, true, cfg.ECHProxyEnable)
	assert.Equal(t, "127.0.0.1:9443", cfg.ECHProxyAddr)
	assert.Equal(t, "/var/lib/peerdrive/ech-proxy", cfg.ECHProxyInstallDir)
	assert.Equal(t, "v1.3.1", cfg.ECHProxyVersion)
	assert.Equal(t, "twimg-pbs.l.moonchan.xyz", cfg.ECHProxyEntryHost)
	assert.Equal(t, false, cfg.ECHProxySkipTLS)
	assert.Equal(t, "auto", cfg.ECHProxyIPMode)
	assert.Equal(t, 7, cfg.ECHProxyStartAttempts)
}

// echProxyTestConfig is a Config that passes every other Validate rule, so a test can
// isolate the ech-proxy checks.
func echProxyTestConfig() *Config {
	return &Config{
		Port: "3000", DBPath: "./peerdrive.db", StorageDir: "./storage",
		DownloadDir: "./downloads", MaxPeers: 8,
		PeerJSPort: "443", DiscoverMode: "auto",
		ECHProxyEnable: true, ECHProxyAddr: "127.0.0.1:8443",
		ECHProxyVersion: "v1.3.0", ECHProxyEntryHost: "twimg-pbs.l.moonchan.xyz",
		ECHProxyIPMode: "v4", ECHProxyStartAttempts: 3,
	}
}

// TestValidate_ECHProxyEnabled_AcceptsDefaults is the happy path: the documented default
// values are legal, so `PEERDRIVE_ECH_PROXY_ENABLE=true` alone must start.
func TestValidate_ECHProxyEnabled_AcceptsDefaults(t *testing.T) {
	assert.NoError(t, Validate(echProxyTestConfig()))
}

// TestValidate_ECHProxyEnabled_InvalidValues is the fail-fast contract: every misconfigured
// value is rejected at startup instead of degrading at runtime.
func TestValidate_ECHProxyEnabled_InvalidValues(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mutate  func(*Config)
		errHint string
	}{
		{"empty version", func(c *Config) { c.ECHProxyVersion = "  " }, "PEERDRIVE_ECH_PROXY_VERSION cannot be empty"},
		{"empty entry host", func(c *Config) { c.ECHProxyEntryHost = "" }, "PEERDRIVE_ECH_PROXY_ENTRY_HOST cannot be empty"},
		{"bad ip mode", func(c *Config) { c.ECHProxyIPMode = "v8" }, "PEERDRIVE_ECH_PROXY_IP_MODE"},
		{"zero attempts", func(c *Config) { c.ECHProxyStartAttempts = 0 }, "PEERDRIVE_ECH_PROXY_START_ATTEMPTS"},
		{"negative attempts", func(c *Config) { c.ECHProxyStartAttempts = -2 }, "PEERDRIVE_ECH_PROXY_START_ATTEMPTS"},
		{"empty addr", func(c *Config) { c.ECHProxyAddr = "  " }, "PEERDRIVE_ECH_PROXY_ADDR cannot be empty"},
		{"addr without port", func(c *Config) { c.ECHProxyAddr = "127.0.0.1" }, "not a valid host:port"},
		{"addr with bad port", func(c *Config) { c.ECHProxyAddr = "127.0.0.1:0" }, "has an invalid port"},
		{"addr with high port", func(c *Config) { c.ECHProxyAddr = "127.0.0.1:70000" }, "has an invalid port"},
		{"addr with non-numeric port", func(c *Config) { c.ECHProxyAddr = "127.0.0.1:bad" }, "has an invalid port"},
		{"addr with empty host", func(c *Config) { c.ECHProxyAddr = ":8443" }, "has an empty host"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := echProxyTestConfig()
			tt.mutate(cfg)
			err := Validate(cfg)
			assert.Error(t, err, "%s must fail validation", tt.name)
			if err != nil {
				assert.Contains(t, err.Error(), tt.errHint)
			}
		})
	}
}

// TestValidate_ECHProxyDisabled_IgnoresBadValues is the mirror image of the previous test:
// with the flag off, junk in the ech-proxy settings must not block startup, because a
// disabled module must never affect a node's ability to boot.
func TestValidate_ECHProxyDisabled_IgnoresBadValues(t *testing.T) {
	cfg := echProxyTestConfig()
	cfg.ECHProxyEnable = false
	cfg.ECHProxyVersion = ""
	cfg.ECHProxyEntryHost = ""
	cfg.ECHProxyIPMode = "not-a-mode"
	cfg.ECHProxyStartAttempts = 0
	cfg.ECHProxyAddr = "not-an-address"
	assert.NoError(t, Validate(cfg))
}

// TestValidate_ECHProxyAllIPModes enumerates the accepted -ip-mode values.
func TestValidate_ECHProxyAllIPModes(t *testing.T) {
	for _, mode := range []string{"", "auto", "v4", "v6"} {
		t.Run(mode, func(t *testing.T) {
			cfg := echProxyTestConfig()
			cfg.ECHProxyIPMode = mode
			assert.NoError(t, Validate(cfg), "ip-mode %q must be accepted", mode)
		})
	}
}

// echProxyConflictConfig returns a config with both ech-proxy consumers enabled at their
// shared defaults (127.0.0.1:8443), which is the pair that must not be allowed to boot.
func echProxyConflictConfig() *Config {
	cfg := echProxyTestConfig()
	cfg.ECHProxyAddr = "127.0.0.1:8443"
	cfg.IwaraEnable = true
	cfg.IwaraEchProxyPort = 8443
	return cfg
}

// TestValidate_ECHProxyIwaraAddrConflict: both modules each spawn their own ech-proxy
// instance and each binds a local listener, so leaving both at the default address makes
// them fight over 127.0.0.1:8443. Report it at startup as a config error rather than as a
// late, cryptic bind failure from whichever module happened to spawn second.
func TestValidate_ECHProxyIwaraAddrConflict(t *testing.T) {
	err := Validate(echProxyConflictConfig())
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "conflicts with the iwara module's listen address 127.0.0.1:8443")
	}
}

// TestValidate_ECHProxyIwaraAddrDiffer: either module moved to its own address resolves the
// conflict — this is the supported way to run both at once.
func TestValidate_ECHProxyIwaraAddrDiffer(t *testing.T) {
	cfg := echProxyConflictConfig()
	cfg.IwaraEchProxyPort = 8444
	assert.NoError(t, Validate(cfg), "different iwara port must be accepted")

	cfg = echProxyConflictConfig()
	cfg.ECHProxyAddr = "127.0.0.1:8444"
	assert.NoError(t, Validate(cfg), "different twimg address must be accepted")
}

// TestValidate_ECHProxyIwaraNonLoopbackTwimg: the iwara module binds 127.0.0.1, so a
// non-loopback twimg address never collides with it.
func TestValidate_ECHProxyIwaraNonLoopbackTwimg(t *testing.T) {
	cfg := echProxyConflictConfig()
	cfg.ECHProxyAddr = "0.0.0.0:8443"
	assert.NoError(t, Validate(cfg), "non-loopback twimg address cannot collide with iwara")
}

// TestValidate_ECHProxyIwaraDisabledNeither: the check only applies when both modules are on.
func TestValidate_ECHProxyIwaraDisabledNeither(t *testing.T) {
	cfg := echProxyConflictConfig()
	cfg.ECHProxyEnable = false
	assert.NoError(t, Validate(cfg), "disabled twimg module must not trip the check")

	cfg = echProxyConflictConfig()
	cfg.IwaraEnable = false
	assert.NoError(t, Validate(cfg), "disabled iwara module must not trip the check")
}

// openlistEnvKeys is the full set the module reads. Tests must unset all of
// them, or a stale value from the operator's shell leaks into the assertion.
var openlistEnvKeys = []string{
	"PEERDRIVE_OPENLIST_ENABLE",
	"PEERDRIVE_OPENLIST_BASE_URL",
	"PEERDRIVE_OPENLIST_INDEX_FILE",
	"PEERDRIVE_OPENLIST_TOKEN",
	"PEERDRIVE_OPENLIST_NAME",
	"PEERDRIVE_OPENLIST_PRIORITY",
	"PEERDRIVE_OPENLIST_TIMEOUT_SECS",
	"PEERDRIVE_OPENLIST_VERIFY",
}

func unsetOpenListEnv(t *testing.T) {
	t.Helper()
	for _, k := range openlistEnvKeys {
		os.Unsetenv(k)
	}
}

// TestLoad_OpenListDefaults is the safety property the whole module hangs on:
// with no configuration at all the source is disabled, so a node that never
// enables it constructs and registers nothing.
func TestLoad_OpenListDefaults(t *testing.T) {
	unsetOpenListEnv(t)
	cfg := Load()

	assert.Equal(t, false, cfg.OpenListEnable, "the module must be opt-in")
	assert.Equal(t, "", cfg.OpenListBaseURL)
	assert.Equal(t, "", cfg.OpenListIndexPath)
	assert.Equal(t, "", cfg.OpenListToken)
	assert.Equal(t, "openlist", cfg.OpenListName)
	assert.Equal(t, 900, cfg.OpenListPriority, "after local/peer/url, which all default to 0")
	assert.Equal(t, 120, cfg.OpenListTimeoutSecs)
	assert.Equal(t, true, cfg.OpenListVerify, "content addressing is on unless told otherwise")
}

// TestLoad_OpenListEnvOverrides proves each env var reaches its field with the
// right type — an operator sets eight variables, so a wrong type mapping shows
// up as a silent fallback to the default.
func TestLoad_OpenListEnvOverrides(t *testing.T) {
	unsetOpenListEnv(t)
	os.Setenv("PEERDRIVE_OPENLIST_ENABLE", "1")
	os.Setenv("PEERDRIVE_OPENLIST_BASE_URL", "https://ol.example.com")
	os.Setenv("PEERDRIVE_OPENLIST_INDEX_FILE", "/etc/peerdrive/openlist-index.json")
	os.Setenv("PEERDRIVE_OPENLIST_TOKEN", "secret")
	os.Setenv("PEERDRIVE_OPENLIST_NAME", "my-openlist")
	os.Setenv("PEERDRIVE_OPENLIST_PRIORITY", "50")
	os.Setenv("PEERDRIVE_OPENLIST_TIMEOUT_SECS", "30")
	os.Setenv("PEERDRIVE_OPENLIST_VERIFY", "false")
	for _, k := range openlistEnvKeys {
		t.Cleanup(func() { os.Unsetenv(k) })
	}

	cfg := Load()
	assert.Equal(t, true, cfg.OpenListEnable)
	assert.Equal(t, "https://ol.example.com", cfg.OpenListBaseURL)
	assert.Equal(t, "/etc/peerdrive/openlist-index.json", cfg.OpenListIndexPath)
	assert.Equal(t, "secret", cfg.OpenListToken)
	assert.Equal(t, "my-openlist", cfg.OpenListName)
	assert.Equal(t, 50, cfg.OpenListPriority)
	assert.Equal(t, 30, cfg.OpenListTimeoutSecs)
	assert.Equal(t, false, cfg.OpenListVerify)
}

// TestLoad_UnifiedNamespaceEnv tests that PEERDRIVE_PORT and PEERDRIVE_DB_PATH
// take precedence over legacy PORT and DB_PATH, while legacy variables still work as fallbacks.
// 发现背景：Issue #152 指出 PORT/DB_PATH 等无 PEERDRIVE_ 前缀造成命名混乱与误用。
func TestLoad_UnifiedNamespaceEnv(t *testing.T) {
	t.Run("PEERDRIVE_PORT precedence over PORT", func(t *testing.T) {
		t.Setenv("PORT", "8000")
		t.Setenv("PEERDRIVE_PORT", "9000")
		cfg := Load()
		assert.Equal(t, "9000", cfg.Port)
	})

	t.Run("Legacy PORT fallback", func(t *testing.T) {
		t.Setenv("PORT", "8080")
		os.Unsetenv("PEERDRIVE_PORT")
		cfg := Load()
		assert.Equal(t, "8080", cfg.Port)
	})

	t.Run("PEERDRIVE_DB_PATH precedence over DB_PATH", func(t *testing.T) {
		t.Setenv("DB_PATH", "/tmp/legacy.db")
		t.Setenv("PEERDRIVE_DB_PATH", "/tmp/unified.db")
		cfg := Load()
		assert.Equal(t, "/tmp/unified.db", cfg.DBPath)
	})

	t.Run("Legacy DB_PATH fallback", func(t *testing.T) {
		t.Setenv("DB_PATH", "/tmp/legacy.db")
		os.Unsetenv("PEERDRIVE_DB_PATH")
		cfg := Load()
		assert.Equal(t, "/tmp/legacy.db", cfg.DBPath)
	})
}

// TestLoad_TURNCredentialsEnv 覆盖 #144 新增的两个 TURN 凭据变量。
//
// 发现背景：TURN URL 一直能配（PEERDRIVE_WEBRTC_TURN），但没有任何变量能填
// 凭据 —— 认证 TURN 服务器会拒绝无凭据的 Allocate，relay candidate 永不产生，
// 于是「配了 TURN 也没用」且无处可查。这里钉住两个新变量真的被读进来，
// 免得哪天变量名写错、静默退回空串（那正好复现原 bug）。
func TestLoad_TURNCredentialsEnv(t *testing.T) {
	t.Setenv("PEERDRIVE_WEBRTC_TURN", "turn:turn.example.com:3478")
	t.Setenv("PEERDRIVE_WEBRTC_TURN_USER", "peerdrive")
	t.Setenv("PEERDRIVE_WEBRTC_TURN_PASS", "test-only-not-a-credential")

	cfg := Load()
	assert.Equal(t, "turn:turn.example.com:3478", cfg.WebRTCTURNServer)
	assert.Equal(t, "peerdrive", cfg.WebRTCTURNUsername)
	assert.Equal(t, "test-only-not-a-credential", cfg.WebRTCTURNPassword)
}

// TestLoad_TURNCredentialsDefaultEmpty 保证默认部署不受影响：
// 不设新变量时必须是空串（= 匿名 TURN 的旧行为），不能让默认配置凭空
// 带上一组凭据去撞所有 TURN 服务器。
func TestLoad_TURNCredentialsDefaultEmpty(t *testing.T) {
	os.Unsetenv("PEERDRIVE_WEBRTC_TURN_USER")
	os.Unsetenv("PEERDRIVE_WEBRTC_TURN_PASS")

	cfg := Load()
	assert.Equal(t, "", cfg.WebRTCTURNUsername)
	assert.Equal(t, "", cfg.WebRTCTURNPassword)
}
