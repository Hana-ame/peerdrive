package serverapp

// security_status_test.go — unit tests for the security status summary (security_status.go).
//
// Why these cases matter: the summary is the only thing standing between a misconfigured
// node and an operator who never realizes it is open. A regression that drops a warning
// would silently re-open a hole while every other test still passes — so pin down
// "open items must be reported" and "hardened nodes must report nothing" both ways.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"peerdrive/internal/config"
)

// defaultCfg mirrors config.Load() with no environment variables set — i.e. exactly
// what a new user gets from "run it with defaults".
func defaultCfg() *config.Config {
	return &config.Config{
		Port:               "3000",
		DBPath:             "./test.db",
		StorageDir:         "./storage",
		DownloadDir:        "./downloads",
		PeerPSK:            "",
		RegistrationServer: "",
		AdminToken:         "",
		DiscoverPresence:   true,
		DisableSwagger:     false,
		MaxPeers:           8,
	}
}

// titles extracts just the titles, making assertions readable.
func titles(fs []securityFinding) map[string]string {
	out := make(map[string]string, len(fs))
	for _, f := range fs {
		out[f.Title] = f.Level
	}
	return out
}

// TestSecurityStatus_DefaultConfigReportsAllOpen: a default-config node has four
// open items (PSK / admin auth / public roster / Swagger) plus one informational note
// (sharing off). If this test fails after a refactor, someone removed a warning.
func TestSecurityStatus_DefaultConfigReportsAllOpen(t *testing.T) {
	got := titles(collectSecurityFindings(defaultCfg(), nil))

	warnWanted := []string{
		"inbound P2P has no gate (PEERDRIVE_PSK empty)",
		"HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty)",
		"node is present in the public roster (PEERDRIVE_DISCOVER_PRESENCE=true)",
		"Swagger API docs are public (PEERDRIVE_SWAGGER not off)",
	}
	for _, want := range warnWanted {
		lvl, ok := got[want]
		if !ok {
			t.Errorf("missing warning %q; got %v", want, got)
			continue
		}
		if lvl != "warn" {
			t.Errorf("warning %q has level %q, want warn", want, lvl)
		}
	}

	if got["external sharing not enabled (PEERDRIVE_SHARE_ENABLE not true)"] != "info" {
		t.Errorf("sharing-off note should be info level; got %v", got)
	}
}

// TestSecurityStatus_HardenedConfigReportsNothing: once PSK / REG_SERVER are set, and
// presence and Swagger are off, there should be no open items left —
// the all-closed summary is what tells the operator "no need to act".
func TestSecurityStatus_HardenedConfigReportsNothing(t *testing.T) {
	cfg := defaultCfg()
	cfg.PeerPSK = "demo-key"
	cfg.RegistrationServer = "https://account.moonchan.xyz"
	cfg.DiscoverPresence = false
	cfg.DisableSwagger = true

	// Assert on **warn** items rather than total count: a sharing-off entry is level=info,
	// not warn — sharing being closed is the safe default and not something to "fix".
	// This is exactly what logSecuritySummary keys its "all gates closed" line off.
	warns := 0
	for _, f := range collectSecurityFindings(cfg, nil) {
		if f.Level == "warn" {
			warns++
			t.Errorf("unexpected open item on a hardened node: %s", f.Title)
		}
	}
	if warns != 0 {
		t.Errorf("hardened config should report 0 open items, got %d", warns)
	}
}

// TestSecurityStatus_SwaggerOnly: closing just one switch should only silence that one
// warning — guards against a future edit that returns early after the first finding.
func TestSecurityStatus_SwaggerOnly(t *testing.T) {
	cfg := defaultCfg()
	cfg.DisableSwagger = true

	got := titles(collectSecurityFindings(cfg, nil))
	if _, ok := got["Swagger API docs are public (PEERDRIVE_SWAGGER not off)"]; ok {
		t.Error("Swagger warning should be gone after PEERDRIVE_SWAGGER=off")
	}
	if _, ok := got["inbound P2P has no gate (PEERDRIVE_PSK empty)"]; !ok {
		t.Error("PSK warning must survive when only Swagger was disabled")
	}
}

// TestSecurityStatus_PSKOnly: same idea from the other side — setting a PSK should
// silence only the PSK warning.
func TestSecurityStatus_PSKOnly(t *testing.T) {
	cfg := defaultCfg()
	cfg.PeerPSK = "demo-key"

	got := titles(collectSecurityFindings(cfg, nil))
	if _, ok := got["inbound P2P has no gate (PEERDRIVE_PSK empty)"]; ok {
		t.Error("PSK warning should be gone after setting PEERDRIVE_PSK")
	}
	if _, ok := got["HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty)"]; !ok {
		t.Error("admin-auth warning must survive when only PSK was set")
	}
}

// TestSecurityStatus_AdminTokenSuppressesAdminAuthWarning: when AdminToken is set
// (and no RegistrationServer), the "HTTP admin surface has no auth" warning should
// be replaced by an informational "local token" note.
// Discovery: C-14 follow-up — AdminToken is a valid auth backend.
func TestSecurityStatus_AdminTokenSuppressesAdminAuthWarning(t *testing.T) {
	cfg := defaultCfg()
	cfg.AdminToken = "ci-test-token"

	got := titles(collectSecurityFindings(cfg, nil))

	// The old "no auth" warning should be gone.
	if _, ok := got["HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty)"]; ok {
		t.Error("admin-auth warning should be gone when PEERDRIVE_ADMIN_TOKEN is set")
	}
	// An info-level note about local token mode should appear.
	if _, ok := got["HTTP admin uses local token (PEERDRIVE_ADMIN_TOKEN)"]; !ok {
		t.Error("local-token info note should appear when PEERDRIVE_ADMIN_TOKEN is set")
	} else if lvl, _ := got["HTTP admin uses local token (PEERDRIVE_ADMIN_TOKEN)"]; lvl != "info" {
		t.Errorf("local-token note should be info level, got %q", lvl)
	}
	// Other warnings (PSK, presence, Swagger) must survive.
	if _, ok := got["inbound P2P has no gate (PEERDRIVE_PSK empty)"]; !ok {
		t.Error("PSK warning must survive when only AdminToken was set")
	}
}

// TestSecurityStatus_NoAuthWhenBothEmpty tests that the warning appears when both are empty.
func TestSecurityStatus_NoAuthWhenBothEmpty(t *testing.T) {
	cfg := defaultCfg()
	cfg.RegistrationServer = ""
	cfg.AdminToken = ""

	got := titles(collectSecurityFindings(cfg, nil))

	if _, ok := got["HTTP admin surface has no auth (PEERDRIVE_REG_SERVER empty)"]; !ok {
		t.Error("admin-auth warning should appear when both are empty")
	}
}

// TestValidateAuthStartup_... tests for validateAuthStartup (C-14 audit fix).
// Discovery: C-14 — non-loopback deployment without auth backend is fully open.

func TestValidateAuthStartup_NonLoopbackNoAuth(t *testing.T) {
	cfg := defaultCfg()
	cfg.Host = "0.0.0.0"
	cfg.RegistrationServer = ""
	os.Setenv("PEERDRIVE_ALLOW_NO_AUTH", "")
	defer os.Unsetenv("PEERDRIVE_ALLOW_NO_AUTH")

	err := validateAuthStartup(cfg)
	require.Error(t, err, "non-loopback with no auth backend must fail")
	assert.Contains(t, err.Error(), "refusing to serve")
}

func TestValidateAuthStartup_NonLoopbackWithAuth(t *testing.T) {
	cfg := defaultCfg()
	cfg.Host = "0.0.0.0"
	cfg.RegistrationServer = "https://account.example.com"
	os.Setenv("PEERDRIVE_ALLOW_NO_AUTH", "")
	defer os.Unsetenv("PEERDRIVE_ALLOW_NO_AUTH")

	err := validateAuthStartup(cfg)
	require.NoError(t, err, "non-loopback with auth backend must pass")
}

// TestValidateAuthStartup_NonLoopbackWithAdminToken: AdminToken is a valid auth backend.
// Discovery: C-14 follow-up — local token mode keeps non-loopback safe without a reg server.
func TestValidateAuthStartup_NonLoopbackWithAdminToken(t *testing.T) {
	cfg := defaultCfg()
	cfg.Host = "0.0.0.0"
	cfg.RegistrationServer = ""
	cfg.AdminToken = "ci-test-token"
	os.Setenv("PEERDRIVE_ALLOW_NO_AUTH", "")
	defer os.Unsetenv("PEERDRIVE_ALLOW_NO_AUTH")

	err := validateAuthStartup(cfg)
	require.NoError(t, err, "non-loopback with AdminToken must pass (C-14 follow-up)")
}

func TestValidateAuthStartup_NonLoopbackAllowNoAuth(t *testing.T) {
	cfg := defaultCfg()
	cfg.Host = "0.0.0.0"
	cfg.RegistrationServer = ""
	os.Setenv("PEERDRIVE_ALLOW_NO_AUTH", "1")
	defer os.Unsetenv("PEERDRIVE_ALLOW_NO_AUTH")

	err := validateAuthStartup(cfg)
	require.NoError(t, err, "non-loopback with PEERDRIVE_ALLOW_NO_AUTH=1 must pass")
}

func TestValidateAuthStartup_LoopbackNoAuth(t *testing.T) {
	cfg := defaultCfg()
	cfg.Host = "127.0.0.1"
	cfg.RegistrationServer = ""
	os.Setenv("PEERDRIVE_ALLOW_NO_AUTH", "")
	defer os.Unsetenv("PEERDRIVE_ALLOW_NO_AUTH")

	err := validateAuthStartup(cfg)
	require.NoError(t, err, "loopback without auth is safe")
}

// TestValidate_DiscoverMode tests validation of PEERDRIVE_DISCOVER_MODE values.
func TestValidate_DiscoverMode(t *testing.T) {
	t.Run("valid modes pass", func(t *testing.T) {
		for _, mode := range []string{"auto", "peerjs", "off", "discover", "mqtt"} {
			cfg := defaultCfg()
			cfg.DiscoverMode = mode
			if mode == "discover" {
				cfg.DiscoverURL = "http://example.com"
			}
			if mode == "mqtt" {
				cfg.MQTTEnable = true
			}
			err := config.Validate(cfg)
			assert.NoError(t, err, "mode=%s should be valid", mode)
		}
	})

	t.Run("empty mode fails", func(t *testing.T) {
		cfg := defaultCfg()
		cfg.DiscoverMode = ""
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PEERDRIVE_DISCOVER_MODE cannot be empty")
	})

	t.Run("invalid mode fails", func(t *testing.T) {
		cfg := defaultCfg()
		cfg.DiscoverMode = "invalid"
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid")
	})

	t.Run("discover mode without URL fails", func(t *testing.T) {
		cfg := defaultCfg()
		cfg.DiscoverMode = "discover"
		cfg.DiscoverURL = ""
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires PEERDRIVE_DISCOVER_URL")
	})

	t.Run("mqtt mode without MQTT_ENABLE fails", func(t *testing.T) {
		cfg := defaultCfg()
		cfg.DiscoverMode = "mqtt"
		cfg.MQTTEnable = false
		err := config.Validate(cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires PEERDRIVE_MQTT_ENABLE")
	})
}

// TestDiscoverMode_Default tests that the default DiscoverMode is "auto".
func TestDiscoverMode_Default(t *testing.T) {
	os.Unsetenv("PEERDRIVE_DISCOVER_MODE")
	defer os.Unsetenv("PEERDRIVE_DISCOVER_MODE")

	cfg := config.Load()
	assert.Equal(t, "auto", cfg.DiscoverMode)
}
