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
		PeerPSK:            "",
		RegistrationServer: "",
		DiscoverPresence:   true,
		DisableSwagger:     false,
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
