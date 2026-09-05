package config

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	// 确保测试的是默认值，不受外部环境变量影响
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

// TestIsOriginAllowed 验证 CORS Origin 白名单匹配逻辑：精确匹配、通配符、
// 子域名通配、空/星号放行、不匹配拒绝。
//
// 发现背景：2026-09-05 CORS 问题修复——Cloudflare Pages 预览部署使用
// 子域名（6f670b67.peerdrive.pages.dev），默认白名单只有精确的
// peerdrive.pages.dev 匹配不上。补测试防止通配符逻辑回归。
func TestIsOriginAllowed(t *testing.T) {
	tests := []struct {
		name     string
		origins  string
		origin   string
		wantAllow bool
	}{
		// 星号放行一切
		{"star_allows_all", "*", "https://anything.example.com", true},
		{"star_allows_all_2", "*", "http://evil.com", true},
		// 空字符串放行一切（默认行为）
		{"empty_allows_all", "", "https://anything.com", true},
		// 精确匹配
		{"exact_match", "https://a.com,https://b.com", "https://a.com", true},
		{"exact_match_2", "https://a.com,https://b.com", "https://b.com", true},
		{"exact_mismatch", "https://a.com,https://b.com", "https://c.com", false},
		// 大小写不敏感
		{"case_insensitive", "https://A.COM", "https://a.com", true},
		// 子域名通配 *.example.com
		{"subdomain_wildcard_match", "https://*.example.com", "https://sub.example.com", true},
		{"subdomain_wildcard_match_deep", "https://*.example.com", "https://deep.sub.example.com", true},
		{"subdomain_wildcard_no_match", "https://*.example.com", "https://example.com", false},
		{"subdomain_wildcard_wrong_domain", "https://*.example.com", "https://evil.com", false},
		// Cloudflare Pages 预览部署场景
		{"cf_pages_exact", "https://peerdrive.pages.dev", "https://peerdrive.pages.dev", true},
		{"cf_pages_preview_not_matched", "https://peerdrive.pages.dev", "https://6f670b67.peerdrive.pages.dev", false},
		{"cf_pages_preview_wildcard", "https://*.pages.dev,https://peerdrive.pages.dev", "https://6f670b67.peerdrive.pages.dev", true},
		// 混合列表
		{"mixed_list", "http://localhost:5173,https://peerdrive.moonchan.xyz,https://*.pages.dev", "https://preview.pages.dev", true},
		{"mixed_list_no_match", "http://localhost:5173,https://peerdrive.moonchan.xyz", "https://preview.pages.dev", false},
		// 逗号分隔 + 空格容错
		{"whitespace_trim", "https://a.com, https://b.com", "https://b.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{AllowedOrigins: tt.origins}
			assert.Equal(t, tt.wantAllow, cfg.IsOriginAllowed(tt.origin))
		})
	}
}
