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
		name     string
		origins  string
		origin   string
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
